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
// recurring jobs' (checkJobs); parent: mistakes (parentFindings); files a
// department's gates need that aren't there yet (checkFileGates); connector
// mistakes and connectors: on
// company: or Product; declared connections: (connectionFindings); and the
// outputs shelf's (checkOutputs); a budget: that doesn't parse.
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
	// group is the group owner sits under (nil: none); its goals are what
	// owner's goals may roll up into besides HQ's.
	check := func(owner, prompt string, gs []tracker.Goal, hq bool, group *tracker.Department) {
		groupGoals := map[string]bool{}
		if group != nil {
			for _, g := range group.Goals {
				groupGoals[strings.ToLower(g.ID)] = true
			}
		}
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
			case group != nil && !hqGoals[strings.ToLower(g.RollsUp)] && !groupGoals[strings.ToLower(g.RollsUp)]:
				add(owner, fmt.Sprintf("%s: rolls_up: %q names no %s goal and no HQ goal (company.goals)", label, g.RollsUp, group.Name), "")
			case group == nil && !hqGoals[strings.ToLower(g.RollsUp)]:
				add(owner, fmt.Sprintf("%s: rolls_up: %q names no HQ goal (company.goals)", label, g.RollsUp), "")
			}
		}
	}
	if p.Company != nil {
		check("company", p.Company.Prompt, p.Company.Goals, true, nil)
		if b := p.Company.Budget; b != nil && b.Err != "" {
			add("company", b.Err, tracker.BudgetShape)
		}
	}
	if d := p.Product; d != nil {
		check(tracker.ProductName, d.Prompt, d.Goals, false, nil)
		if b := d.Budget; b != nil && b.Err != "" {
			add(tracker.ProductName, b.Err, tracker.BudgetShape)
		}
		if strings.TrimSpace(d.Parent) != "" {
			add(tracker.ProductName, "parent: on the Product entry is ignored: Product is never in a group", "remove parent: from the Product entry")
		}
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
		check(d.Name, d.Prompt, d.Goals, false, p.GroupOf(d.Name))
		if b := d.Budget; b != nil && b.Err != "" {
			add(d.Name, b.Err, tracker.BudgetShape)
		}
		for _, f := range parentFindings(p, d) {
			add(d.Name, f.Message, f.Fix)
		}
		for _, f := range checkJobs(p, d.Name, d.Jobs) {
			add(d.Name, f.Message, f.Fix)
		}
		for _, f := range connectorFindings(d) {
			add(d.Name, f, "")
		}
		for _, f := range checkFileGates(p, d.Name) {
			add(d.Name, f.Message, f.Fix)
		}
	}
	for _, f := range connectionFindings(p) {
		add("connections", f.Message, f.Fix)
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

// checkFileGates flags the files department dept's workflow gates on
// (file_exists) that aren't there yet, so the owner hears that, say,
// Marketing's channel briefs can't move to draft before the brand
// foundation is written. Only the base transitions count: a type's own
// edge checks a file that type's issues write themselves (the foundation's
// publish → done). Paths that name the issue ({{slug}}, …) are skipped:
// they differ per issue.
func checkFileGates(p *tracker.Project, dept string) []Finding {
	wf, err := p.WorkflowFor(dept)
	if err != nil {
		return nil // the department's workflow file has its own finding
	}
	key := tracker.DepartmentKey(dept)
	fill := func(s string) string { return strings.ReplaceAll(s, "{{department}}", key) }
	var out []Finding
	seen := map[string]bool{}
	for _, tr := range wf.Transitions {
		for _, a := range tr.Actions {
			if a.Type != "validate" || a.Rule != "file_exists" {
				continue
			}
			rel := fill(strings.TrimSpace(a.Path))
			if rel == "" || strings.Contains(rel, "{{") || seen[rel] {
				continue
			}
			seen[rel] = true
			info, err := os.Stat(filepath.Join(p.Root(), filepath.FromSlash(rel)))
			if err == nil && info.Mode().IsRegular() && info.Size() > 0 {
				continue
			}
			fix := fill(strings.TrimSpace(a.Hint))
			if strings.Contains(fix, "{{") {
				fix = ""
			}
			out = append(out, Finding{Message: fmt.Sprintf("%s is not written yet: %s → %s refuses until it is (file_exists)", rel, tr.From, tr.To), Fix: fix})
		}
	}
	return out
}

// parentFindings are department d's parent: mistakes: a parent that names
// no department (or HQ, Product, d itself), and a chain of groups above d
// that loops or is deeper than tracker.MaxGroupDepth.
func parentFindings(p *tracker.Project, d tracker.Department) []Finding {
	parent := strings.TrimSpace(d.Parent)
	if parent == "" {
		return nil
	}
	key := tracker.DepartmentKey(parent)
	g := p.DepartmentByKey(parent)
	switch {
	case key == "hq" || key == tracker.DepartmentKey(tracker.ProductName):
		return []Finding{{Message: fmt.Sprintf("parent: %s is built in and never a group: a group is a department in departments:", parent), Fix: "remove parent:, or name a group department"}}
	case g == nil:
		return []Finding{{Message: fmt.Sprintf("parent: %q names no department", parent), Fix: fmt.Sprintf("add the group (issue-cli department add %s, if a template has it) or remove parent:", key)}}
	case tracker.DepartmentKey(g.Name) == tracker.DepartmentKey(d.Name):
		return []Finding{{Message: "parent: names the department itself", Fix: "remove parent:"}}
	}
	// A group may sit in a group; the chain may not loop or run deeper
	// than tracker.MaxGroupDepth
	// (factory/nested-groups-a-group-inside-a-group-with-goals-rolling-up-and-the-map-drawing-it#D-1).
	switch _, problem := p.ParentChain(d.Name); problem {
	case "loop":
		return []Finding{{Message: fmt.Sprintf("parent: chain loops (%s): %s is treated as in no group", chainText(p, d), d.Name), Fix: "remove one parent: in the loop"}}
	case "deep":
		return []Finding{{Message: fmt.Sprintf("parent: chain is too deep (%s): groups nest at most %d deep, so %s is treated as in no group", chainText(p, d), tracker.MaxGroupDepth, d.Name), Fix: fmt.Sprintf("put %s under a group higher up the chain", d.Name)}}
	}
	return nil
}

// chainText is d's parent: chain as "a → b → c", stopping at the first
// name seen twice (shown again, so a loop reads a → b → a).
func chainText(p *tracker.Project, d tracker.Department) string {
	names := []string{d.Name}
	seen := map[string]bool{tracker.DepartmentKey(d.Name): true}
	for cur := p.DepartmentByKey(d.Name); cur != nil && strings.TrimSpace(cur.Parent) != ""; {
		next := p.DepartmentByKey(strings.TrimSpace(cur.Parent))
		if next == nil {
			break
		}
		names = append(names, next.Name)
		if seen[tracker.DepartmentKey(next.Name)] {
			break
		}
		seen[tracker.DepartmentKey(next.Name)] = true
		cur = next
	}
	return strings.Join(names, " → ")
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
// no brief or a brief file that isn't there, a type: the department's
// workflow doesn't define.
func checkJobs(p *tracker.Project, dept string, js []tracker.Job) []Finding {
	var out []Finding
	var wf *tracker.WorkflowConfig
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
		if strings.TrimSpace(j.Type) == "" {
			continue
		}
		if wf == nil {
			var err error
			if wf, err = p.WorkflowFor(dept); err != nil {
				continue // the department's workflow file has its own finding
			}
		}
		if _, err := jobs.RunType(wf, j); err != nil {
			out = append(out, Finding{Message: label + ": " + err.Error(), Fix: "fix type: or add the type to the " + dept + " workflow's types:"})
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

// checkOutputs flags the outputs shelf's problems: log lines Load (and
// LoadComments) skip,
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
	_, badComments := outputs.LoadComments(p)
	cwhere := filepath.ToSlash(filepath.Join(filepath.Base(p.IssueDir), ".outputs", "comments.jsonl"))
	for _, b := range badComments {
		out = append(out, Finding{Where: fmt.Sprintf("%s:%d", cwhere, b.Line), Message: "comment line skipped: " + b.Err})
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
