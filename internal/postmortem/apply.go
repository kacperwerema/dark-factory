package postmortem

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/gitfile"
	"github.com/kacperwerema/dark-factory/internal/memory"
)

// Env is what the owner's round applies agreed actions with.
type Env struct {
	// Repo is the project checkout a file or skill action edits (on main).
	Repo string
	// IssueDir holds the project's memory log.
	IssueDir string
	Project  string
	Issue    string
	System   string
	// RetroPath is the post-mortem file; RetroRef its name as a source
	// ("retros/<file>").
	RetroPath string
	RetroRef  string
	// Owner is the author the memory and the outcome lines name.
	Owner string
	Now   time.Time
	// NewExperiment files a workflow action as an experiment proposal and
	// returns its slug.
	NewExperiment func(a Action) (string, error)
}

// Answer is the owner's answer to one action.
type Answer struct {
	ID    string `json:"id"`
	Agree bool   `json:"agree"`
	Note  string `json:"note,omitempty"`
}

// Outcome is what happened to one answered action.
type Outcome struct {
	ID     string `json:"id"`
	Agreed bool   `json:"agreed"`
	// Line is the Applied: or Refused: text written to the retro.
	Line string `json:"line,omitempty"`
	// Err is why an agreed action could not be applied; it stays open.
	Err string `json:"error,omitempty"`
}

// ErrDecided is a round on a post-mortem with nothing left to answer.
var ErrDecided = errors.New("every action of this post-mortem is already decided")

// Decide is the owner's one round: answers must cover every open action.
// Each agreed action is applied (memory, file or skill commit, experiment
// proposal, or nothing for none) and ticked with an Applied: line naming
// what it made; each refused one gets a Refused: line. An agreed action
// that fails stays open with its error, and the others still apply. The
// retro is rewritten after each action.
func Decide(env Env, answers []Answer) ([]Outcome, error) {
	raw, err := os.ReadFile(env.RetroPath)
	if err != nil {
		return nil, err
	}
	pend := Pending(Actions(string(raw)))
	if len(pend) == 0 {
		return nil, ErrDecided
	}
	byID := map[string]Answer{}
	for _, a := range answers {
		byID[strings.ToUpper(strings.TrimSpace(a.ID))] = a
	}
	open := map[string]bool{}
	var missing []string
	for _, a := range pend {
		open[a.ID] = true
		if _, ok := byID[a.ID]; !ok {
			missing = append(missing, a.ID)
		}
	}
	for id := range byID {
		if !open[id] {
			return nil, fmt.Errorf("%s is not an open action of this post-mortem", id)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("one round answers every action: agree or refuse %s too", strings.Join(missing, ", "))
	}
	if env.Now.IsZero() {
		env.Now = time.Now()
	}
	date := env.Now.Format("2006-01-02")
	text := string(raw)
	var out []Outcome
	for _, a := range pend {
		ans := byID[a.ID]
		o := Outcome{ID: a.ID, Agreed: ans.Agree}
		if ans.Agree {
			line, err := env.apply(a)
			if err != nil {
				o.Err = err.Error()
				out = append(out, o)
				continue
			}
			o.Line = line + " · agreed " + date + " (" + env.Owner + ")"
		} else {
			o.Line = date + " (" + env.Owner + ")"
			if n := strings.Join(strings.Fields(ans.Note), " "); n != "" {
				o.Line += ": " + n
			}
		}
		next, err := Mark(text, a.ID, ans.Agree, o.Line)
		if err != nil {
			return out, err
		}
		text = next
		if err := gitfile.WriteAtomic(env.RetroPath, []byte(text), 0o644); err != nil {
			return out, fmt.Errorf("%s applied (%s) but the retro could not be written: %w", a.ID, o.Line, err)
		}
		out = append(out, o)
	}
	return out, nil
}

// apply applies one agreed action and returns what it made, as the
// Applied: line says it.
func (env Env) apply(a Action) (string, error) {
	switch a.Place {
	case PlaceMemory:
		store := memory.ProjectStore(env.IssueDir)
		d := memory.Draft{
			Text: strings.Join(strings.Fields(a.Text), " "), Kind: a.Arg, Scope: memory.ScopeProject,
			Project: env.Project, System: env.System, Author: env.Owner,
			Source: memory.Source{Issue: env.Issue, Retro: env.RetroRef},
		}
		res, err := store.Add(d, memory.AddOptions{}, env.Now)
		var sim *memory.SimilarError
		if errors.As(err, &sim) && len(sim.Similar) > 0 {
			res, err = store.Add(d, memory.AddOptions{Mode: memory.ModeUpdates, Target: sim.Similar[0].ID}, env.Now)
		}
		if err != nil {
			return "", err
		}
		switch res.Op {
		case memory.OpNoop:
			return "memory " + res.Memory.ID + " (already remembered)", nil
		case memory.OpUpdate:
			return "memory " + res.Memory.ID + " (updated)", nil
		}
		return "memory " + res.Memory.ID, nil
	case PlaceFile, PlaceSkill:
		rel := a.Target()
		path := filepath.Join(env.Repo, filepath.FromSlash(rel))
		old, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		next, err := Splice(string(old), a.Under, a.Text)
		if err != nil {
			return "", fmt.Errorf("%s: %w", rel, err)
		}
		subject := "docs(postmortem): " + oneLine(a.Title, 60)
		body := fmt.Sprintf("Owner: agreed on the post-mortem of %s (%s, %s).\n\nRefs: %s", env.Issue, env.RetroRef, a.ID, env.Issue)
		sha, err := gitfile.Commit(path, old, []byte(next), true, subject, body)
		if err != nil {
			return "", err
		}
		return "commit " + sha + " (" + rel + ")", nil
	case PlaceWorkflow:
		if env.NewExperiment == nil {
			return "", errors.New("no way to file an experiment proposal here")
		}
		slug, err := env.NewExperiment(a)
		if err != nil {
			return "", err
		}
		return "experiment " + slug, nil
	case PlaceNone:
		return "none (" + a.Arg + "): the docs already say it", nil
	}
	return "", fmt.Errorf("unknown place %q", a.Place)
}

// Splice adds text to a markdown file's content: at the end of the section
// under heading (before the next heading of the same or a higher level),
// or at the end of the file when heading is "". A heading that isn't there
// is an error.
func Splice(content, heading, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("nothing to add")
	}
	if heading == "" {
		c := strings.TrimRight(content, "\n")
		if c == "" {
			return text + "\n", nil
		}
		return c + "\n\n" + text + "\n", nil
	}
	lines := strings.Split(content, "\n")
	at, level := -1, 0
	for i, l := range lines {
		if m := headingRe.FindStringSubmatch(l); m != nil && strings.EqualFold(strings.TrimSpace(m[1]), strings.TrimSpace(heading)) {
			at, level = i, len(l)-len(strings.TrimLeft(l, "#"))
			break
		}
	}
	if at < 0 {
		return "", fmt.Errorf("no heading %q in the file any more", heading)
	}
	end := len(lines)
	for j := at + 1; j < len(lines); j++ {
		if headingRe.MatchString(lines[j]) && len(lines[j])-len(strings.TrimLeft(lines[j], "#")) <= level {
			end = j
			break
		}
	}
	ins := end
	for ins > at+1 && strings.TrimSpace(lines[ins-1]) == "" {
		ins--
	}
	out := append([]string{}, lines[:ins]...)
	out = append(out, "", text)
	if ins < len(lines) {
		out = append(out, "")
	}
	rest := lines[ins:]
	for len(rest) > 0 && strings.TrimSpace(rest[0]) == "" && end < len(lines) {
		rest = rest[1:]
	}
	out = append(out, rest...)
	res := strings.Join(out, "\n")
	if !strings.HasSuffix(res, "\n") {
		res += "\n"
	}
	return res, nil
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
