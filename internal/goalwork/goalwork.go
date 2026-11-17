// Package goalwork finds the project's goals and open epics with no work
// behind them: no open issue, active queue item or job. The goal-without-work
// lint check prints one finding per gap, and the queue's out-of-work line
// (queue list, queue next, the lead tick) names them as the places to look
// first. See docs/departments.md, "Work behind a goal".
package goalwork

import (
	"fmt"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/goals"
	"github.com/kacperwerema/dark-factory/internal/queue"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Field is the issue frontmatter key that names the goal an issue serves.
const Field = "goal"

// Gap kinds.
const (
	KindGoal = "goal"
	KindEpic = "epic"
)

// Gap is one goal or open epic with nothing behind it.
type Gap struct {
	Kind string `json:"kind"`
	// Key is the goal key (hq/clients) or the epic's slug.
	Key   string `json:"key"`
	Label string `json:"label"`
	// Fix is the command that adds work for it.
	Fix string `json:"fix"`
}

// Name is the gap as the out-of-work line names it.
func (g Gap) Name() string {
	if g.Kind == KindEpic {
		return "epic " + g.Key
	}
	if g.Label != "" && !strings.EqualFold(g.Label, g.Key) {
		return fmt.Sprintf("%s (%s)", g.Key, g.Label)
	}
	return g.Key
}

// Report is the coverage of a project's goals and epics.
type Report struct {
	// NoGoals: projects.yaml sets no goal anywhere.
	NoGoals bool  `json:"no_goals,omitempty"`
	Gaps    []Gap `json:"gaps"`
}

// Find reads the coverage. items is the project's queue (nil when it has
// none); only active items count.
func Find(proj *tracker.Project, issues []*tracker.Issue, items []queue.Item, now time.Time) *Report {
	rep := &Report{Gaps: []Gap{}}
	gr := goals.Resolve(proj, now)
	rep.NoGoals = gr.Empty()

	open := map[string]*tracker.Issue{}
	for _, issue := range issues {
		if isOpen(proj, issue) {
			open[strings.ToLower(issue.Slug)] = issue
		}
	}
	var active []queue.Item
	for _, it := range items {
		if it.Active() {
			active = append(active, it)
		}
	}

	if !rep.NoGoals {
		covered := map[string]bool{}
		mark := func(ref string) {
			if g := Resolve(gr, ref); g != nil {
				covered[strings.ToLower(g.Key)] = true
			}
		}
		for _, issue := range open {
			mark(issue.Field(Field))
		}
		for _, it := range active {
			mark(it.Goal)
			if issue := open[strings.ToLower(it.Slug)]; issue != nil {
				mark(issue.Field(Field))
			}
		}
		var has func(g *goals.Goal) bool
		has = func(g *goals.Goal) bool {
			if g.Job != "" || covered[strings.ToLower(g.Key)] {
				return true
			}
			for _, c := range g.Children {
				if has(c) {
					return true
				}
			}
			return false
		}
		for _, o := range gr.Owners() {
			for _, g := range o.Goals {
				if has(g) {
					continue
				}
				rep.Gaps = append(rep.Gaps, Gap{
					Kind: KindGoal, Key: g.Key, Label: g.Label(),
					Fix: tracker.InjectProject(proj, fmt.Sprintf(`issue-cli create --title "…" --goal %s`, g.Key)) +
						" (or " + tracker.InjectProject(proj, fmt.Sprintf(`issue-cli queue add --idea "…" --goal %s`, g.Key)) + ")",
				})
			}
		}
	}

	queued := map[string]bool{}
	for _, it := range active {
		if it.Slug != "" {
			queued[strings.ToLower(it.Slug)] = true
		}
	}
	for _, epic := range issues {
		if open[strings.ToLower(epic.Slug)] == nil || !tracker.IsEpic(epic, issues) {
			continue
		}
		if queued[strings.ToLower(epic.Slug)] {
			continue
		}
		busy := false
		for _, c := range tracker.EpicChildren(issues, epic.Slug) {
			if open[strings.ToLower(c.Slug)] != nil || queued[strings.ToLower(c.Slug)] {
				busy = true
				break
			}
		}
		if busy {
			continue
		}
		rep.Gaps = append(rep.Gaps, Gap{
			Kind: KindEpic, Key: epic.Slug, Label: epic.Title,
			Fix: tracker.InjectProject(proj, fmt.Sprintf(`issue-cli create --title "…" --parent %s`, epic.Slug)) + ", or close the epic",
		})
	}
	return rep
}

// isOpen: work in hand, not closed and not parked.
func isOpen(proj *tracker.Project, issue *tracker.Issue) bool {
	return !proj.IssueClosed(issue) && !tracker.IsParkedStatus(issue.Status)
}

// Resolve finds the goal a goal: value names: its key (hq/clients), or a
// bare id that exactly one owner has. nil when it names none or is
// ambiguous.
func Resolve(rep *goals.Report, ref string) *goals.Goal {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	if g := rep.Goal(ref); g != nil {
		return g
	}
	if strings.Contains(ref, "/") {
		return nil
	}
	var found *goals.Goal
	for _, o := range rep.Owners() {
		for _, g := range o.Goals {
			if strings.EqualFold(g.ID, ref) {
				if found != nil {
					return nil
				}
				found = g
			}
		}
	}
	return found
}

// Check is what create --goal and queue add --goal run: the key the value
// stands for, or an error naming the keys the project has.
func Check(proj *tracker.Project, ref string, now time.Time) (string, error) {
	rep := goals.Resolve(proj, now)
	if g := Resolve(rep, ref); g != nil {
		return g.Key, nil
	}
	keys := rep.Keys()
	if len(keys) == 0 {
		return "", fmt.Errorf("--goal %q: the project has no goals in projects.yaml (the owner sets them: issue-cli department update hq --goal \"<id>=<name>/<target>/<period>\")", ref)
	}
	return "", fmt.Errorf("--goal %q names no goal of this project (or more than one): use a key, one of %s", ref, strings.Join(keys, ", "))
}

// LookFirst is the clause the out-of-work line adds: the goals and epics
// with no work, at most max of them, or "" when every one has work.
func (r *Report) LookFirst(proj *tracker.Project, max int) string {
	if r == nil {
		return ""
	}
	var parts []string
	if r.NoGoals {
		parts = append(parts, "no goals set (the owner's: issue-cli goals)")
	}
	var names []string
	for _, g := range r.Gaps {
		names = append(names, g.Name())
	}
	if len(names) > max {
		more := len(names) - max
		names = append(names[:max], fmt.Sprintf("+%d more: %s", more, tracker.InjectProject(proj, "issue-cli health lint --check goal-without-work")))
	}
	if len(names) > 0 {
		parts = append(parts, "look first at: "+strings.Join(names, ", "))
	}
	return strings.Join(parts, "; ")
}
