package lint

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/goals"
	"github.com/michal-franc/issue-viewer/internal/jobs"
	"github.com/michal-franc/issue-viewer/internal/outputs"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// checkDepartments flags mistakes in projects.yaml's company: and
// departments: blocks that would otherwise only show as a missing goal or
// an empty role prompt: a prompt file that isn't there, rolls_up: naming
// no HQ goal, duplicate goal ids, unknown period:/direction:/source:, a
// file source whose file is missing, systems: on the Product entry; the
// recurring jobs' (checkJobs); connector mistakes and connectors: on
// company: or Product; and the outputs shelf's (checkOutputs).
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
	if p.Company != nil && len(p.Company.Connectors) > 0 {
		add("company", "connectors: on company: are ignored: connectors belong to a department", "move them under the department that uses them")
	}
	if d := p.Product; d != nil && len(d.Connectors) > 0 {
		add(tracker.ProductName, "connectors: on the Product entry are ignored: connectors belong to a department other than Product", "move them under the department that uses them")
	}
	for _, d := range p.Departments {
		check(d.Name, d.Prompt, d.Goals, false)
		for _, f := range checkJobs(p, d.Jobs) {
			add(d.Name, f.Message, f.Fix)
		}
		for _, f := range connectorFindings(d) {
			add(d.Name, f, "")
		}
	}
	if p.Company != nil && len(p.Company.Jobs) > 0 {
		add("company", "jobs: on HQ are not run: a recurring job belongs to a department", "move the jobs under a department in departments:")
	}
	if p.Product != nil && len(p.Product.Jobs) > 0 {
		add(tracker.ProductName, "jobs: on the Product entry are not run: a recurring job belongs to a department", "move the jobs under a department in departments:")
	}
	out = append(out, checkJobLog(env)...)
	return append(out, checkOutputs(env)...)
}

// connectorFindings are department d's connector mistakes: an unknown
// kind, a bad or duplicate name, mcp without command:, a secret reference
// that is not env:/keychain: (which never quotes the raw value: it may be
// one).
func connectorFindings(d tracker.Department) []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range d.Connectors {
		label := "connector " + c.Name
		switch {
		case c.Name == "":
			out = append(out, "a connector has no name:")
			label = "a connector"
		case !tracker.ConnectorNameRe.MatchString(c.Name):
			out = append(out, fmt.Sprintf("connector name %q is not lower-case letters, digits and -", c.Name))
		case seen[c.Name]:
			out = append(out, fmt.Sprintf("connector %q appears twice", c.Name))
		}
		seen[c.Name] = true
		if !slices.Contains(tracker.ConnectorKinds, c.Kind) {
			out = append(out, fmt.Sprintf("%s: kind %q is not mcp or tool", label, c.Kind))
		}
		if c.Kind == tracker.ConnectorMCP && strings.TrimSpace(c.Command) == "" {
			out = append(out, label+": an mcp connector needs command:")
		}
		for _, v := range slices.Sorted(maps.Keys(c.Secrets)) {
			if _, err := tracker.ParseSecretRef(c.Secrets[v]); err != nil {
				out = append(out, fmt.Sprintf("%s: secret %s: %s", label, v, err))
			}
		}
	}
	return out
}

// checkJobs flags a department's jobs that issue-cli job due would skip or
// a run would fail on: no id or a duplicate, a schedule that doesn't parse,
// no brief or a brief file that isn't there.
func checkJobs(p *tracker.Project, js []tracker.Job) []Finding {
	var out []Finding
	seen := map[string]bool{}
	for _, j := range js {
		id := strings.ToLower(strings.TrimSpace(j.ID))
		label := "job " + j.ID
		switch {
		case id == "":
			out = append(out, Finding{Message: "a job has no id:"})
			continue
		case seen[id]:
			out = append(out, Finding{Message: fmt.Sprintf("job id %q appears twice", j.ID)})
		}
		seen[id] = true
		if _, err := jobs.Parse(j); err != nil {
			out = append(out, Finding{Message: label + ": " + err.Error()})
		}
		switch brief := strings.TrimSpace(j.Brief); {
		case brief == "":
			out = append(out, Finding{Message: label + ": no brief: (the markdown file each run starts from)"})
		default:
			path := filepath.Join(p.Root(), filepath.FromSlash(brief))
			if _, err := os.Stat(path); err != nil {
				out = append(out, Finding{Message: fmt.Sprintf("%s: brief %s does not exist", label, brief), Fix: "create " + path + " or fix brief:"})
			}
		}
	}
	return out
}

// checkJobLog flags runs of jobs projects.yaml no longer has: run issues
// still open whose job: names no configured job.
func checkJobLog(env *Env) []Finding {
	p := env.Project
	known := map[string]bool{}
	for _, r := range jobs.List(p) {
		known[strings.ToLower(r.Key)] = true
	}
	var out []Finding
	for _, is := range env.Issues {
		key := strings.ToLower(is.Field("job"))
		if key == "" || known[key] || p.IssueDone(is) || strings.EqualFold(is.Status, "parked") {
			continue
		}
		out = append(out, Finding{Where: is.Slug, Message: fmt.Sprintf("open run of job %q, which projects.yaml no longer has", is.Field("job")),
			Fix: "add the job back, or finish or park the run"})
	}
	return out
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
