package postmortem

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/gitfile"
	"github.com/kacperwerema/dark-factory/internal/memory"
)

// An action can be fixed already: the work it asks for happened elsewhere,
// so it needs no answer. A "Fixed: <slug>|<sha>" line under the action (the
// lead's issue-cli postmortem fixed) links it to the issue or the commit
// that fixes it; a memory action is fixed too when an active memory has its
// very words (memory.Hash: case, spacing and punctuation aside), where
// applying it would add nothing. A near one is not: on this board every
// memory action within memory's near-duplicate bar was an update of that
// memory, which agreeing applies. It is read at load time and
// never written as an outcome: a reopened issue makes the action wait again.

// lineFixed is the field of an action's fix link.
const lineFixed = "Fixed"

// Fix is what fixed an action, or what will.
type Fix struct {
	// By names it: an issue slug, a short sha or a memory id.
	By string `json:"by"`
	// Kind is issue, commit or memory.
	Kind string `json:"kind"`
	// Done is whether it is fixed now; an issue still open is a fix
	// underway (Status), and the action still waits.
	Done   bool   `json:"done"`
	Status string `json:"status,omitempty"`
}

// The kinds of a Fix.
const (
	FixIssue  = "issue"
	FixCommit = "commit"
	FixMemory = "memory"
)

// Words is the fix as a row shows it: "fixed by <slug>", "known as m-…",
// "fix underway: <slug> (<status>)".
func (f Fix) Words() string {
	switch {
	case f.By == "":
		return ""
	case f.Kind == FixMemory:
		return "known as " + f.By
	case f.Done:
		return "fixed by " + f.By
	case f.Status != "":
		return "fix underway: " + f.By + " (" + f.Status + ")"
	}
	return "fix underway: " + f.By
}

// Fixes resolves the fix of an action. Every field is optional: a nil
// IssueState knows no issue, an empty Repo no commit, no Memories no memory.
type Fixes struct {
	// IssueState is a slug's status and whether it is done; known false
	// for a slug the board doesn't have.
	IssueState func(slug string) (status string, done, known bool)
	Repo, Main string
	// MainOf names main when Main is "", the first time a sha needs it.
	MainOf   func() string
	Memories []memory.Memory
	shas     map[string]string
	memHash  map[string]string
}

// Of is a's fix; a zero Fix when nothing fixes it.
func (f *Fixes) Of(a Action) Fix {
	if f == nil {
		return Fix{}
	}
	if by := strings.TrimSpace(a.Fixed); by != "" {
		if shaRe.MatchString(by) && f.Repo != "" {
			if f.shas == nil {
				f.shas = map[string]string{}
			}
			short, ok := f.shas[by]
			if !ok && f.Main == "" && f.MainOf != nil {
				f.Main = f.MainOf()
			}
			if !ok {
				short, _ = OnMain(f.Repo, f.Main, by)
				f.shas[by] = short
			}
			if short != "" {
				return Fix{By: short, Kind: FixCommit, Done: true}
			}
		}
		if f.IssueState != nil {
			if status, done, known := f.IssueState(by); known {
				return Fix{By: by, Kind: FixIssue, Done: done, Status: status}
			}
		}
		// A link to nothing the board knows: shown, but it fixes nothing.
		return Fix{By: by}
	}
	if a.Place == PlaceMemory && strings.TrimSpace(a.Text) != "" {
		if f.memHash == nil {
			f.memHash = map[string]string{}
			for _, m := range f.Memories {
				f.memHash[memory.Hash(m.Text)] = m.ID
			}
		}
		if id := f.memHash[memory.Hash(a.Text)]; id != "" {
			return Fix{By: id, Kind: FixMemory, Done: true}
		}
	}
	return Fix{}
}

// Split sets each item's Fix and parts items into the ones that still wait
// and the ones fixed already, each in items' order.
func (f *Fixes) Split(items []Item) (open, fixed []Item) {
	for _, it := range items {
		it.Fix = f.Of(it.Action)
		if it.Fix.Done {
			fixed = append(fixed, it)
		} else {
			open = append(open, it)
		}
	}
	return open, fixed
}

// MemoriesFor is the project's active memories, for Fixes.Memories; none
// when the log can't be read.
func MemoriesFor(issueDir string, now time.Time) []memory.Memory {
	set, err := memory.ProjectStore(issueDir).Load()
	if err != nil {
		return nil
	}
	return set.Active(now)
}

// LinkFix writes "Fixed: <by>" under action id of the retro at path,
// replacing a Fixed: line it has. A decided action is refused: it waits on
// nothing.
func LinkFix(path, id, by string) error {
	mu.Lock()
	defer mu.Unlock()
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	next, err := linkFixIn(string(raw), id, by)
	if err != nil {
		return err
	}
	return gitfile.WriteAtomic(path, []byte(next), 0o644)
}

func linkFixIn(raw, id, by string) (string, error) {
	by = strings.TrimSpace(by)
	if by == "" || strings.ContainsAny(by, " \t\n") {
		return "", fmt.Errorf("fixed by %q: name one issue slug or commit sha", by)
	}
	lines := strings.Split(raw, "\n")
	for _, a := range Actions(raw) {
		if a.ID != strings.ToUpper(strings.TrimSpace(id)) {
			continue
		}
		if a.Decided() {
			return "", fmt.Errorf("%s is already decided", a.ID)
		}
		indent := "  "
		end := a.Line + 1
		for end < len(lines) && (strings.HasPrefix(lines[end], " ") || strings.HasPrefix(lines[end], "\t")) && strings.TrimSpace(lines[end]) != "" {
			if m := fieldRe.FindStringSubmatch(lines[end]); m != nil {
				indent = lines[end][:len(lines[end])-len(strings.TrimLeft(lines[end], " \t"))]
				if strings.EqualFold(m[1], lineFixed) {
					lines[end] = indent + lineFixed + ": " + by
					return strings.Join(lines, "\n"), nil
				}
			}
			end++
		}
		out := append([]string{}, lines[:end]...)
		out = append(out, indent+lineFixed+": "+by)
		out = append(out, lines[end:]...)
		return strings.Join(out, "\n"), nil
	}
	return "", fmt.Errorf("no action %s in the post-mortem", id)
}
