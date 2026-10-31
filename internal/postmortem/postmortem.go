// Package postmortem is the post-mortem retro a fresh agent writes when an
// issue closes (issue-cli postmortem): its six sections, its proposed
// actions and the one test each must pass, the outcome lines the owner's
// round writes back, and the apply step. A post-mortem is a retro under
// retros/ with the header line "Kind: postmortem", so internal/retro reads
// it like any other.
package postmortem

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/memory"
	"github.com/michal-franc/issue-viewer/internal/retro"
)

// Kind is the retro header value that marks a post-mortem.
const Kind = "postmortem"

// Sections are the headings a post-mortem has, in order.
var Sections = []string{"Steps", "Worked", "Did not work", "Decisions", "Known", "Proposed actions"}

// The places an action goes.
const (
	PlaceMemory   = "memory"
	PlaceFile     = "file"
	PlaceSkill    = "skill"
	PlaceWorkflow = "workflow"
	PlaceNone     = "none"
)

// Places is every place, in the order the shape lists them.
var Places = []string{PlaceMemory, PlaceFile, PlaceSkill, PlaceWorkflow, PlaceNone}

// TheTest is the one question every action answers on its Differently: line.
const TheTest = "would a fresh agent do something differently next session because of it?"

// Shape is the format of one action, printed with every refusal.
const Shape = `## Proposed actions

- [ ] A1: <the action, one line>
  Differently: <what a fresh agent would do differently next session because of it>
  Place: memory <fact|convention|pitfall|howto|who> | file <repo path .md> | skill <name> | workflow | none <doc path#anchor that already says it>
  Text: <the memory, or the text to add to the file or skill; for workflow, the change to try>
  Under: <optional: the file's heading to add it under; else the end>`

// Action is one proposed action.
type Action struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Differently string `json:"differently"`
	// Place is the place's kind (memory, file, skill, workflow, none) and
	// Arg its argument (a memory kind, a path, a skill name, a doc ref).
	Place string `json:"place"`
	Arg   string `json:"arg,omitempty"`
	Text  string `json:"text,omitempty"`
	Under string `json:"under,omitempty"`
	// Done is the box ticked; Applied or Refused says how it ended.
	Done    bool   `json:"done"`
	Applied string `json:"applied,omitempty"`
	Refused string `json:"refused,omitempty"`
	// Line is the 0-based line of the item in the body it was parsed from.
	Line int `json:"-"`
}

// Decided is whether the owner's round has answered this action.
func (a Action) Decided() bool { return a.Done || a.Applied != "" || a.Refused != "" }

// Target is the file a file or skill action edits, relative to the repo.
func (a Action) Target() string {
	switch a.Place {
	case PlaceFile:
		return filepath.ToSlash(filepath.Clean(a.Arg))
	case PlaceSkill:
		return ".claude/skills/" + a.Arg + "/SKILL.md"
	}
	return ""
}

// PlaceString is the place as the Place: line writes it.
func (a Action) PlaceString() string {
	if a.Arg == "" {
		return a.Place
	}
	return a.Place + " " + a.Arg
}

var (
	itemRe    = regexp.MustCompile(`^\s*[-*]\s+\[([ xX])\]\s+(A\d+)\s*[:.)]\s*(.*)$`)
	looseRe   = regexp.MustCompile(`^\s*[-*]\s+(?:\[[ xX]\]\s+)?(.*)$`)
	fieldRe   = regexp.MustCompile(`^\s+(?:[-*]\s+)?([A-Za-z]+)\s*:\s*(.*)$`)
	headingRe = regexp.MustCompile(`^#{1,4}\s+(.+?)\s*#*\s*$`)
	skillRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
)

// SplitSections maps each heading of body (lower case) to its text.
func SplitSections(body string) (map[string]string, []string) {
	out := map[string]string{}
	var order []string
	cur := ""
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		if m := headingRe.FindStringSubmatch(line); m != nil {
			cur = strings.ToLower(strings.TrimSpace(m[1]))
			if _, ok := out[cur]; !ok {
				order = append(order, cur)
			}
			out[cur] += ""
			continue
		}
		if cur != "" {
			out[cur] += line + "\n"
		}
	}
	for k, v := range out {
		out[k] = strings.TrimSpace(v)
	}
	return out, order
}

// Actions parses the Proposed actions section of body. It reads what is
// there and judges nothing: Check refuses.
func Actions(body string) []Action {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	in := false
	var out []Action
	var cur *Action
	lastKey := ""
	for i, line := range lines {
		if m := headingRe.FindStringSubmatch(line); m != nil {
			in = strings.EqualFold(strings.TrimSpace(m[1]), "proposed actions")
			cur = nil
			continue
		}
		if !in {
			continue
		}
		if m := itemRe.FindStringSubmatch(line); m != nil {
			out = append(out, Action{ID: strings.ToUpper(m[2]), Title: strings.TrimSpace(m[3]), Done: m[1] != " ", Line: i})
			cur, lastKey = &out[len(out)-1], ""
			continue
		}
		if cur != nil {
			if m := fieldRe.FindStringSubmatch(line); m != nil && knownField(m[1]) {
				lastKey = strings.ToLower(m[1])
				setField(cur, lastKey, strings.TrimSpace(m[2]))
				continue
			}
			if strings.TrimSpace(line) != "" && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) && lastKey == "text" {
				cur.Text += "\n" + strings.TrimSpace(line)
				continue
			}
		}
		if m := looseRe.FindStringSubmatch(line); m != nil && !strings.HasPrefix(line, " ") && strings.TrimSpace(m[1]) != "" {
			// A list item that isn't "- [ ] A<n>: …": kept with no id, so
			// Check names it.
			out = append(out, Action{Title: strings.TrimSpace(m[1]), Line: i})
			cur, lastKey = &out[len(out)-1], ""
		}
	}
	return out
}

func knownField(k string) bool {
	switch strings.ToLower(k) {
	case "differently", "place", "text", "under", "applied", "refused":
		return true
	}
	return false
}

func setField(a *Action, key, val string) {
	switch key {
	case "differently":
		a.Differently = val
	case "place":
		f := strings.Fields(val)
		if len(f) > 0 {
			a.Place = strings.ToLower(f[0])
			a.Arg = strings.TrimSpace(strings.TrimPrefix(val, f[0]))
		}
	case "text":
		a.Text = val
	case "under":
		a.Under = strings.TrimLeft(val, "# ")
	case "applied":
		a.Applied = val
	case "refused":
		a.Refused = val
	}
}

// Refusal is one reason a post-mortem is refused.
type Refusal struct {
	ID, Why string
}

func (r Refusal) String() string {
	if r.ID == "" {
		return r.Why
	}
	return r.ID + ": " + r.Why
}

// RefusedError lists every refusal and the shape that would pass.
type RefusedError struct{ Refusals []Refusal }

func (e *RefusedError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "post-mortem refused, nothing was saved (%d problem(s)):\n", len(e.Refusals))
	for _, r := range e.Refusals {
		fmt.Fprintf(&b, "  - %s\n", r)
	}
	fmt.Fprintf(&b, "\nEvery action passes one test, %q, and names one place:\n\n%s\n\nHeadings: %s.", TheTest, Shape, "## "+strings.Join(Sections, ", ## "))
	return b.String()
}

// Check parses body as a post-mortem and refuses it unless it has the six
// sections and every action passes the test and names one place. root is
// the repo a file action and a none reference are checked against.
func Check(body, root string) ([]Action, error) {
	var refs []Refusal
	secs, _ := SplitSections(body)
	for _, s := range Sections {
		if _, ok := secs[strings.ToLower(s)]; !ok {
			refs = append(refs, Refusal{Why: "missing the ## " + s + " section (write \"none\" when it has nothing)"})
		}
	}
	acts := Actions(body)
	seen := map[string]bool{}
	for _, a := range acts {
		for _, why := range a.problems(root) {
			refs = append(refs, Refusal{ID: a.label(), Why: why})
		}
		if a.ID != "" {
			if seen[a.ID] {
				refs = append(refs, Refusal{ID: a.ID, Why: "the id is used twice"})
			}
			seen[a.ID] = true
		}
	}
	if len(refs) > 0 {
		return acts, &RefusedError{Refusals: refs}
	}
	return acts, nil
}

func (a Action) label() string {
	if a.ID != "" {
		return a.ID
	}
	t := a.Title
	if r := []rune(t); len(r) > 40 {
		t = string(r[:39]) + "…"
	}
	return fmt.Sprintf("%q", t)
}

// problems are why a fails the test or the place rule; none is a pass.
func (a Action) problems(root string) []string {
	var out []string
	if a.ID == "" {
		return []string{"not an action item: write it as \"- [ ] A<n>: <action>\" with Differently:, Place: and Text: lines below"}
	}
	if a.Title == "" {
		out = append(out, "no action text after the id")
	}
	switch d := strings.TrimSpace(a.Differently); {
	case d == "":
		out = append(out, "no Differently: line: it fails the test ("+TheTest+") and is dropped")
	case retro.None(d) || len(strings.Fields(d)) < 4 || strings.EqualFold(normal(d), normal(a.Title)):
		out = append(out, fmt.Sprintf("Differently: %q doesn't say what a fresh agent would do differently: it fails the test and is dropped", d))
	}
	if a.Place == "" {
		return append(out, "no Place: line: name one place ("+strings.Join(Places, " | ")+")")
	}
	known := false
	for _, p := range Places {
		known = known || p == a.Place
	}
	if !known {
		return append(out, fmt.Sprintf("Place: %q is not a place: one of %s", a.PlaceString(), strings.Join(Places, " | ")))
	}
	if strings.ContainsAny(a.Arg, "|,") || (a.Place != PlaceNone && len(strings.Fields(a.Arg)) > 1) {
		out = append(out, fmt.Sprintf("Place: %q names more than one place: one action, one place", a.PlaceString()))
	}
	needText := a.Place != PlaceNone
	if needText && strings.TrimSpace(a.Text) == "" {
		out = append(out, "no Text: line: say what goes into "+a.Place)
	}
	if k := memory.FindSecret(a.Text); k != "" {
		out = append(out, "Text: looks like a secret ("+k+"): a post-mortem never carries one")
	}
	switch a.Place {
	case PlaceMemory:
		if a.Arg == "" {
			out = append(out, "Place: memory needs a kind: "+strings.Join(memory.Kinds, ", "))
		} else if err := memory.CheckKind(a.Arg); err != nil {
			out = append(out, "Place: "+err.Error())
		}
		if strings.TrimSpace(a.Text) != "" {
			if err := memory.CheckText(a.Text); err != nil {
				out = append(out, "Text: "+err.Error())
			}
		}
	case PlaceFile:
		if why := badFile(a.Arg); why != "" {
			out = append(out, "Place: file "+why)
		}
	case PlaceSkill:
		if !skillRe.MatchString(a.Arg) {
			out = append(out, fmt.Sprintf("Place: skill needs a skill name (lower case, e.g. skill playtest), not %q", a.Arg))
		}
	case PlaceWorkflow:
		if a.Arg != "" {
			out = append(out, "Place: workflow takes no argument: the change goes through the experiment loop, Text: says what to try")
		}
	case PlaceNone:
		ref := strings.TrimSpace(a.Arg)
		if ref == "" {
			out = append(out, "Place: none needs the doc that already says it (none docs/workflow.md#local-ci)")
			break
		}
		file, _, _ := strings.Cut(ref, "#")
		if why := badFile(file); why != "" {
			out = append(out, "Place: none "+why)
		} else if root != "" {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(file))); err != nil {
				out = append(out, fmt.Sprintf("Place: none names %s, which isn't in the repo", file))
			}
		}
	}
	return out
}

// badFile is why path can't be a file action's target, or "".
func badFile(path string) string {
	p := strings.TrimSpace(path)
	switch {
	case p == "":
		return "needs a repo path (file CLAUDE.md, file docs/workflow.md)"
	case filepath.IsAbs(p) || strings.HasPrefix(p, "~"):
		return fmt.Sprintf("%q is absolute: name a path inside the repo", p)
	}
	clean := filepath.ToSlash(filepath.Clean(p))
	if clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(p, "..") {
		return fmt.Sprintf("%q leaves the repo", p)
	}
	if !strings.HasSuffix(strings.ToLower(clean), ".md") {
		return fmt.Sprintf("%q is not a markdown file: lessons go into CLAUDE.md, AGENTS.md, docs/ or a skill; code changes are issues", p)
	}
	for _, pre := range []string{"issues/", "retros/", "sessions/", ".git/", ".worktrees/", ".agent-logs/"} {
		if strings.HasPrefix(clean, pre) {
			return fmt.Sprintf("%q is under %s, which isn't a place for lessons", p, pre)
		}
	}
	return ""
}

func normal(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.Trim(s, ".!")), " "))
}

// Pending are the actions the owner hasn't answered yet.
func Pending(acts []Action) []Action {
	var out []Action
	for _, a := range acts {
		if a.ID != "" && !a.Decided() {
			out = append(out, a)
		}
	}
	return out
}

// Mark records one action's outcome in raw (a retro file): it ticks the
// box and adds the "Applied: …" line, or adds "Refused: …" and leaves the
// box open. An action already decided is refused.
func Mark(raw, id string, applied bool, outcome string) (string, error) {
	lines := strings.Split(raw, "\n")
	for _, a := range Actions(raw) {
		if a.ID != strings.ToUpper(id) {
			continue
		}
		if a.Decided() {
			return "", fmt.Errorf("%s is already decided", a.ID)
		}
		item := lines[a.Line]
		key := "Refused: "
		if applied {
			item = strings.Replace(item, "[ ]", "[x]", 1)
			key = "Applied: "
		}
		indent := "  "
		if j := a.Line + 1; j < len(lines) {
			if m := fieldRe.FindStringSubmatch(lines[j]); m != nil {
				indent = lines[j][:len(lines[j])-len(strings.TrimLeft(lines[j], " \t"))]
			}
		}
		end := a.Line + 1
		for end < len(lines) && (strings.HasPrefix(lines[end], " ") || strings.HasPrefix(lines[end], "\t")) && strings.TrimSpace(lines[end]) != "" {
			end++
		}
		out := append([]string{}, lines[:a.Line]...)
		out = append(out, item)
		out = append(out, lines[a.Line+1:end]...)
		out = append(out, indent+key+strings.TrimSpace(outcome))
		out = append(out, lines[end:]...)
		return strings.Join(out, "\n"), nil
	}
	return "", fmt.Errorf("no action %s in the post-mortem", id)
}
