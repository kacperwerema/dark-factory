package lint

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/goals"
	"github.com/michal-franc/issue-viewer/internal/outputs"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// checkDepartments flags mistakes in projects.yaml's company: and
// departments: blocks that would otherwise only show as a missing goal or
// an empty role prompt: a prompt file that isn't there, rolls_up: naming
// no HQ goal, duplicate goal ids, unknown period:/direction:/source:, a
// file source whose file is missing, systems: on the Product entry; and
// the outputs shelf's (checkOutputs).
func checkDepartments(env *Env) []Finding {
	p := env.Project
	var out []Finding
	add := func(owner, msg, fix string) {
		out = append(out, Finding{Where: "projects.yaml (" + owner + ")", Message: msg, Fix: fix})
	}
	hqGoals := map[string]bool{}
	if p.Company != nil {
		for _, g := range p.Company.Goals {
			hqGoals[strings.ToLower(g.ID)] = true
		}
	}
	check := func(owner, prompt string, gs []tracker.Goal, hq bool) {
		if prompt != "" {
			path := prompt
			if !filepath.IsAbs(path) {
				path = filepath.Join(p.Root(), path)
			}
			if _, err := os.Stat(path); err != nil {
				add(owner, fmt.Sprintf("prompt: %s does not exist", prompt), "create "+path+" or fix prompt:")
			}
		}
		seen := map[string]bool{}
		for _, g := range gs {
			id := strings.ToLower(strings.TrimSpace(g.ID))
			label := "goal " + g.ID
			switch {
			case id == "":
				add(owner, "a goal has no id:", "")
				continue
			case seen[id]:
				add(owner, fmt.Sprintf("goal id %q appears twice", g.ID), "")
			}
			seen[id] = true
			if !slices.Contains(goals.Periods, strings.ToLower(g.Period)) {
				add(owner, fmt.Sprintf("%s: period %q is not week, month, quarter or none", label, g.Period), "")
			}
			if !slices.Contains(goals.Directions, strings.ToLower(g.Direction)) {
				add(owner, fmt.Sprintf("%s: direction %q is not up or down", label, g.Direction), "")
			}
			switch src := g.Source; {
			case src == "" || strings.EqualFold(src, "manual"):
			case strings.HasPrefix(src, "file:"):
				path, _, err := goals.ParseFileSource(strings.TrimPrefix(src, "file:"))
				if err != nil {
					add(owner, label+": "+err.Error(), "")
					break
				}
				if !filepath.IsAbs(path) {
					path = filepath.Join(p.Root(), path)
				}
				if _, err := os.Stat(path); err != nil {
					add(owner, fmt.Sprintf("%s: source file %s does not exist", label, path), "")
				}
			default:
				add(owner, fmt.Sprintf("%s: source %q is not manual or file:<path>#<metric id>", label, src), "")
			}
			switch {
			case g.RollsUp == "":
			case hq:
				add(owner, label+": rolls_up: on an HQ goal (HQ goals are what department goals roll up into)", "")
			case !hqGoals[strings.ToLower(g.RollsUp)]:
				add(owner, fmt.Sprintf("%s: rolls_up: %q names no HQ goal (company.goals)", label, g.RollsUp), "")
			}
		}
	}
	if p.Company != nil {
		check("company", p.Company.Prompt, p.Company.Goals, true)
	}
	if d := p.Product; d != nil {
		check(tracker.ProductName, d.Prompt, d.Goals, false)
		if len(d.Systems) > 0 {
			add(tracker.ProductName, "systems: on the Product entry is ignored: Product holds every issue no other department claims", "remove systems: from the Product entry")
		}
	}
	for _, d := range p.Departments {
		check(d.Name, d.Prompt, d.Goals, false)
	}
	return append(out, checkOutputs(env)...)
}

// checkOutputs flags the outputs shelf's problems: log lines Load skips,
// outputs of a department no longer configured, a file or path that is
// gone, an output waiting for review longer than outputs.StaleReview.
func checkOutputs(env *Env) []Finding {
	p := env.Project
	list, bad := outputs.Load(p)
	where := filepath.ToSlash(filepath.Join(filepath.Base(p.IssueDir), ".outputs", "outputs.jsonl"))
	var out []Finding
	for _, b := range bad {
		out = append(out, Finding{Where: fmt.Sprintf("%s:%d", where, b.Line), Message: "line skipped: " + b.Err})
	}
	gone := map[string]int{}
	for _, o := range list {
		if !outputs.Configured(p, o.Department) {
			gone[o.Department]++
		}
		if o.Missing {
			out = append(out, Finding{Where: where, Message: fmt.Sprintf("output %s (%s): %s is gone", o.ID, o.Title, o.Where())})
		}
		if o.State == outputs.StateReview && env.Now.Sub(o.Added) > outputs.StaleReview {
			out = append(out, Finding{Where: where, Message: fmt.Sprintf("output %s (%s) has waited for review since %s", o.ID, o.Title, o.Added.Format("2006-01-02")),
				Fix: fmt.Sprintf("the owner approves or rejects it on the shelf, or: issue-cli output review %s approve|reject --quote \"...\"", o.ID)})
		}
	}
	for _, key := range slices.Sorted(maps.Keys(gone)) {
		out = append(out, Finding{Where: where, Message: fmt.Sprintf("%d output(s) of department %q, which projects.yaml no longer has", gone[key], key),
			Fix: "add the department back under departments:, or leave them as history"})
	}
	return out
}
