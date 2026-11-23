package lint

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/connectors"
	"github.com/kacperwerema/dark-factory/internal/depttemplate"
	"github.com/kacperwerema/dark-factory/internal/goals"
	"github.com/kacperwerema/dark-factory/internal/jobs"
	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
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
// outputs shelf's (checkOutputs); a budget: that doesn't parse; a partner:
// block's (partnerFindings); a connector or partner the partners catalog
// knows as AGPL with no exception: in force (licenceFindings); a partner's approval with no owner decision
// on its issue (partnerApprovalFindings); a handoff_answered rule naming
// no department (handoffGateFindings); an open issue whose system: names a
// department it isn't in (unfiledFindings); a mission or goal still the
// bundled template's sample (depttemplate.SampleFields).
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
		missing := map[string][]string{}
		missingJob := map[string]string{}
		var missingOrder []string
		defer func() {
			for _, path := range missingOrder {
				ids := missing[path]
				label := "goal " + ids[0]
				if len(ids) > 1 {
					label = "goals " + strings.Join(ids, ", ")
				}
				msg := fmt.Sprintf("%s: source file %s does not exist", label, path)
				if r := missingJob[path]; r != "" {
					msg += fmt.Sprintf(" (job %s writes it on its first run: issue-cli goal write %s <id>=<value>...)", r, r)
				}
				add(owner, msg, "")
			}
		}()
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
				source := path
				if key, ok := goals.JobKeyOf(source); ok && goals.JobOf(p, source) == nil {
					add(owner, fmt.Sprintf("%s: %s names no job %s (the job writing it decides when its value is stale: name the file after a job of this department's jobs:)", label, source, key), "")
				}
				path, err = goals.ResolvePath(p, path)
				if err != nil {
					add(owner, label+": "+err.Error(), "")
					break
				}
				if _, err := os.Stat(path); err != nil {
					// One finding per file: a job's metric file feeds
					// several goals.
					if missing[path] == nil {
						missingOrder = append(missingOrder, path)
						if r := goals.JobOf(p, source); r != nil {
							missingJob[path] = r.Key
						}
					}
					missing[path] = append(missing[path], g.ID)
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
		for _, f := range publishRouteFindings(p, d) {
			add(d.Name, f.Message, f.Fix)
		}
		for _, f := range restWideFindings(d) {
			add(d.Name, f.Message, f.Fix)
		}
		for _, f := range atFindings(p, d) {
			add(d.Name, f.Message, f.Fix)
		}
		for _, f := range checkFileGates(p, d.Name, env.Issues) {
			add(d.Name, f.Message, f.Fix)
		}
		for _, f := range partnerFindings(p, d) {
			add(d.Name, f.Message, f.Fix)
		}
		for _, f := range licenceFindings(env, d) {
			add(d.Name, f.Message, f.Fix)
		}
		out = append(out, partnerApprovalFindings(env, d)...)
		out = append(out, subredditFindings(p, d)...)
	}
	for _, sm := range depttemplate.SampleFields(p) {
		fix := tracker.InjectProject(p, sm.UpdateCommand())
		if sm.Mission != "" {
			add(sm.Name, fmt.Sprintf("mission is still the %s template's sample (%q): set this project's own", sm.Template.Key, sm.Mission), fix)
		}
		if n := len(sm.Goals); n > 0 {
			what := "goal " + sm.Goals[0] + " is"
			if n > 1 {
				what = "goals " + strings.Join(sm.Goals, ", ") + " are"
			}
			add(sm.Name, fmt.Sprintf("%s still the %s template's sample (name, target and period as bundled): set this project's own, drop one with --drop-goal <id>, or keep one as it is by setting it to the same value", what, sm.Template.Key), fix)
		}
	}
	for _, f := range connectionFindings(p) {
		add("connections", f.Message, f.Fix)
	}
	if p.Company != nil && len(p.Company.Jobs) > 0 {
		add("company", "jobs: on HQ are not run: a recurring job belongs to a department", "move the jobs under a department in departments:")
	}
	if p.Product != nil {
		// Product's jobs run too (factory/nightly-self-improvement-loop-…#Q1).
		for _, f := range checkJobs(p, tracker.ProductName, p.Product.Jobs) {
			add(tracker.ProductName, f.Message, f.Fix)
		}
	}
	for _, f := range handoffGateFindings(p) {
		add(f.Where, f.Message, f.Fix)
	}
	out = append(out, checkJobLog(env)...)
	out = append(out, unfiledFindings(env)...)
	return append(out, checkOutputs(env)...)
}

// unfiledFindings flags open issues whose system: names a department while
// nothing puts them in it: they are Product's, on Product's workflow. A
// closed issue is left alone, there is nothing left for it to walk.
func unfiledFindings(env *Env) []Finding {
	var out []Finding
	for _, issue := range env.Issues {
		name := env.Project.UnfiledDepartment(issue)
		if name == "" || env.Project.IssueClosed(issue) {
			continue
		}
		out = append(out, Finding{Where: issue.Slug,
			Message: fmt.Sprintf("system: %s names a department, but the issue has no department: field and %s's systems: don't list it, so it is Product's", issue.System, name),
			Fix:     fmt.Sprintf("issue-cli set-meta %s --key department --value %s, or list %s under %s's systems: in projects.yaml", issue.Slug, name, issue.System, name)})
	}
	return out
}

// handoffGateFindings flags a handoff_answered rule, in Product's workflow
// or a department's, that names no department of the project: no issue
// could ever answer it, so the edge would hold for good.
func handoffGateFindings(p *tracker.Project) []Finding {
	var out []Finding
	look := func(owner string, wf *tracker.WorkflowConfig) {
		for _, r := range wf.HandoffAnsweredRules() {
			if r[0] != "" && p.DepartmentByKey(r[0]) != nil {
				continue
			}
			name := r[0]
			if name == "" {
				name = "(none)"
			}
			out = append(out, Finding{
				Where:   owner,
				Message: fmt.Sprintf("%s: handoff_answered: %s names no department of this project: the edge holds until one answers", r[1], name),
				Fix:     "issue-cli department add <template> (issue-cli department list), or fix the rule's department key",
			})
		}
	}
	look(tracker.ProductName, p.LoadWorkflow())
	for _, d := range p.Departments {
		if p.DepartmentWorkflowPath(d.Name) == "" {
			continue // Product's file: looked at above
		}
		if wf, err := p.WorkflowFor(d.Name); err == nil {
			look(d.Name, wf)
		}
	}
	return out
}

// checkFileGates flags the files department dept's workflow gates on
// (file_exists) that aren't there yet, so the owner hears that, say,
// Marketing's channel briefs can't move to draft before the brand
// foundation is written. The base transitions always count. A type's own
// edge checks a file that type's issues write themselves (keyword-research's
// keywords.md, competitor-scan's competitors.md): it counts only once an
// issue of that type in dept has run to its owner review (typeFileGates),
// so a department that never ran one hears nothing. Paths that name the
// issue ({{slug}}, …) are skipped: they differ per issue.
func checkFileGates(p *tracker.Project, dept string, issues []*tracker.Issue) []Finding {
	wf, err := p.WorkflowFor(dept)
	if err != nil {
		return nil // the department's workflow file has its own finding
	}
	key := tracker.DepartmentKey(dept)
	fill := func(s string) string { return strings.ReplaceAll(s, "{{department}}", key) }
	var out []Finding
	seen := map[string]bool{}
	// missing is the department-relative path a's file_exists names, when
	// it is a fixed path not seen yet and its file isn't written.
	missing := func(a tracker.WorkflowAction) (string, bool) {
		if a.Type != "validate" || a.Rule != "file_exists" {
			return "", false
		}
		rel := fill(strings.TrimSpace(a.Path))
		if rel == "" || strings.Contains(rel, "{{") || seen[rel] {
			return "", false
		}
		seen[rel] = true
		abs := filepath.Join(p.Root(), filepath.FromSlash(rel))
		if rest, ok := strings.CutPrefix(rel, goals.IssuesPrefix); ok {
			abs = filepath.Join(p.IssueDir, filepath.FromSlash(rest))
		}
		info, err := os.Stat(abs)
		return rel, err != nil || !info.Mode().IsRegular() || info.Size() == 0
	}
	for _, tr := range wf.Transitions {
		for _, a := range tr.Actions {
			rel, gone := missing(a)
			if !gone {
				continue
			}
			fix := fill(strings.TrimSpace(a.Hint))
			if strings.Contains(fix, "{{") {
				fix = ""
			}
			out = append(out, Finding{Message: fmt.Sprintf("%s is not written yet: %s → %s refuses until it is (file_exists)", rel, tr.From, tr.To), Fix: fix})
		}
	}
	for _, g := range typeFileGates(p, wf, dept, issues) {
		for _, a := range g.edge.Actions {
			rel, gone := missing(a)
			if !gone {
				continue
			}
			out = append(out, Finding{
				Message: fmt.Sprintf("%s is not written yet: %s (%s) has run to %s, and its publish writes it once the owner approves (%s → %s, file_exists)", rel, g.issue.Slug, g.typ, g.issue.Status, g.edge.From, g.edge.To),
				Fix:     fmt.Sprintf("the owner approves %s's newest shelf version and its publish (issue-cli outputs --issue %s); a revise: comment sends it back", g.issue.Slug, g.issue.Slug),
			})
		}
	}
	return out
}

// typeFileGate is one of a type's own edges, with the issue that has run
// far enough on that type's path for the edge's files to count.
type typeFileGate struct {
	typ   string
	issue *tracker.Issue
	edge  tracker.WorkflowTransition
}

// typeFileGates are the edges of each type in wf whose files an issue of
// dept has reached: the issue is on its type's path at or after the from
// of the path's first human-approval edge (owner review, for the research
// types). Types are walked in name order and issues in slug order, so the
// first issue to reach a file names it.
func typeFileGates(p *tracker.Project, wf *tracker.WorkflowConfig, dept string, issues []*tracker.Issue) []typeFileGate {
	if !wf.HasTypes() {
		return nil
	}
	var mine []*tracker.Issue
	for _, is := range issues {
		if is != nil && strings.EqualFold(p.DepartmentOf(is), dept) {
			mine = append(mine, is)
		}
	}
	sort.Slice(mine, func(i, j int) bool { return mine[i].Slug < mine[j].Slug })
	var out []typeFileGate
	for _, name := range wf.TypeNames() {
		t := wf.Types[name]
		var edges []tracker.WorkflowTransition
		for _, tr := range t.Transitions {
			for _, a := range tr.Actions {
				if a.Type == "validate" && a.Rule == "file_exists" {
					edges = append(edges, tr)
					break
				}
			}
		}
		if len(edges) == 0 {
			continue
		}
		scoped := wf.ForType(name)
		review := -1
		for _, s := range scoped.Statuses {
			for _, tr := range scoped.Transitions {
				if tr.From != s.Name {
					continue
				}
				for _, a := range tr.Actions {
					if a.Type == "require_human_approval" && review < 0 {
						review = scoped.GetStatusIndex(s.Name)
					}
				}
			}
			if review >= 0 {
				break
			}
		}
		if review < 0 {
			continue
		}
		for _, is := range mine {
			if typ, _ := wf.ResolveType(is.Type); typ != name {
				continue
			}
			if at := scoped.GetStatusIndex(is.Status); at < review {
				continue // not on the path (-1), or not at owner review yet
			}
			for _, e := range edges {
				out = append(out, typeFileGate{typ: name, issue: is, edge: e})
			}
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
// one), deny_tools: that aren't tool names or sit on a tool connector, and
// a known server's tools that send or spend left on without sends: true.
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
			out = append(out, fmt.Sprintf("%s: kind %q is not mcp, tool or rest", label, c.Kind))
		}
		hasURL := strings.TrimSpace(c.URL) != ""
		isRest := c.Kind == tracker.ConnectorRest
		// A meter: elevenlabs tool is called by issue-cli itself, at url:.
		isTTS := c.Kind == tracker.ConnectorTool && c.Spend != nil && c.Spend.Meter == tracker.MeterElevenLabs
		// A publish: connector too: url: only points it at a stand-in.
		isPublish := strings.TrimSpace(c.Publish) != ""
		out = append(out, spendFindings(label, c)...)
		out = append(out, publishFindings(label, c)...)
		switch {
		case isPublish:
		case isTTS && strings.TrimSpace(c.Command) != "":
			out = append(out, label+": a meter: elevenlabs connector runs no command: (issue-cli makes its text-to-speech calls itself)")
		case isTTS:
		case c.Kind == tracker.ConnectorMCP && strings.TrimSpace(c.Command) == "" && !hasURL:
			out = append(out, label+": an mcp connector needs command: or url:")
		case isRest && !hasURL:
			out = append(out, label+": a rest connector needs url: (the API's base its get: paths are read under)")
		case isRest && strings.TrimSpace(c.Command) != "":
			out = append(out, label+": a rest connector runs no command: (issue-cli makes its GETs itself)")
		case hasURL && c.Kind != tracker.ConnectorMCP && !isRest:
			out = append(out, label+": url: is only for kind: mcp or rest")
		case hasURL && strings.TrimSpace(c.Command) != "":
			out = append(out, label+": give command: or url:, not both")
		}
		out = append(out, restFindings(label, c)...)
		if hasURL {
			if err := tracker.CheckConnectorURL(c.URL); err != nil {
				out = append(out, label+": "+err.Error())
			}
			if len(c.Secrets) > 0 && !isPublish {
				out = append(out, label+": secrets: are unused with url: (no child process gets them): send them as headers:")
			}
		} else if len(c.Headers) > 0 {
			out = append(out, label+": headers: are only for a connector with url:")
		}
		for _, h := range slices.Sorted(maps.Keys(c.Headers)) {
			if _, err := tracker.ParseHeaderValue(c.Headers[h]); err != nil {
				out = append(out, fmt.Sprintf("%s: header %s: %s", label, h, err))
			}
		}
		for _, v := range slices.Sorted(maps.Keys(c.Secrets)) {
			if _, err := tracker.ParseSecretRef(c.Secrets[v]); err != nil {
				out = append(out, fmt.Sprintf("%s: secret %s: %s", label, v, err))
			}
		}
		for _, v := range c.Optional {
			if _, ok := c.Secrets[v]; !ok {
				out = append(out, fmt.Sprintf("%s: optional %s is not one of its secrets:", label, v))
			}
		}
		if c.PerRun != nil {
			if miss := c.PerRun.MissingSteps(); len(miss) > 0 {
				out = append(out, fmt.Sprintf("%s: per_run: needs %s: connector exec refuses it until then", label, strings.Join(miss, ", ")))
			}
			if c.PerRun.Timeout < 0 {
				out = append(out, fmt.Sprintf("%s: per_run: timeout: is seconds, above 0", label))
			}
			if why := c.PerRun.ToolProblem(); why != "" {
				out = append(out, fmt.Sprintf("%s: per_run: tool: %s: connector exec refuses it until then", label, why))
			}
		}
		if len(c.DenyTools) > 0 && c.Kind != tracker.ConnectorMCP && slices.Contains(tracker.ConnectorKinds, c.Kind) {
			out = append(out, label+": deny_tools: does nothing on a kind: "+c.Kind+" connector (only an mcp server's tools are switched off at launch)")
		}
		denied := map[string]bool{}
		for _, t := range c.DenyTools {
			t = strings.TrimSpace(t)
			switch {
			case !tracker.ToolNameRe.MatchString(t):
				out = append(out, fmt.Sprintf("%s: deny_tools: %q is not a tool name (letters, digits, _ . -); it is skipped at launch", label, t))
			case denied[t]:
				out = append(out, fmt.Sprintf("%s: deny_tools: %s appears twice", label, t))
			}
			denied[t] = true
		}
		out = append(out, holdFindings(label, c, hasURL)...)
		if on := connectors.RiskyAllowed(c); len(on) > 0 {
			out = append(out, fmt.Sprintf("%s: tools that send or spend are on: %s; add them to deny_tools: [%s], or mark the connector sends: true (an owner_only gate before the step that uses it)",
				label, strings.Join(on, ", "), strings.Join(on, ", ")))
		}
	}
	return out
}

// spendFindings are a connector's spend: and voices: mistakes: an unknown
// meter, a cap that isn't a number above 0 (every billing call is refused
// until the owner writes one), meter: json without field: or command:,
// a meter on the wrong kind, and voices without a placement, a voice id,
// once each, or a real person's voice with no consent noted.
func spendFindings(label string, c tracker.Connector) []string {
	var out []string
	if sp := c.Spend; sp != nil {
		switch {
		case !slices.Contains(tracker.SpendMeters, sp.Meter):
			out = append(out, fmt.Sprintf("%s: spend: meter: %q is not %s: connector exec refuses it", label, sp.Meter, strings.Join(tracker.SpendMeters, " or ")))
		case c.Kind != tracker.ConnectorTool:
			out = append(out, fmt.Sprintf("%s: spend: is only for kind: tool (connector exec meters its calls)", label))
		case sp.Meter == tracker.MeterJSON && strings.TrimSpace(sp.Field) == "":
			out = append(out, label+": spend: meter: json needs field: (the key the cost is under in the command's JSON output)")
		case sp.Meter == tracker.MeterJSON && strings.TrimSpace(c.Command) == "":
			out = append(out, label+": spend: meter: json needs command: (a metered connector runs only its own command)")
		}
		if sp.Cap < 0 {
			out = append(out, label+": spend: cap: is an amount a month, above 0")
		}
		if sp.Cap == 0 {
			out = append(out, label+": spend: has no cap: every billing call is refused until the owner writes one (spend: cap:, "+sp.UnitWords()+" a month)")
		}
		if c.PerRun != nil {
			out = append(out, label+": spend: and per_run: together are not metered: connector exec meters the call and skips per_run:")
		}
	}
	if len(c.Voices) > 0 && (c.Spend == nil || c.Spend.Meter != tracker.MeterElevenLabs) {
		out = append(out, label+": voices: are only read by a spend: meter: elevenlabs connector")
	}
	seen := map[string]bool{}
	for _, v := range c.Voices {
		key := strings.ToLower(strings.TrimSpace(v.Placement))
		switch {
		case key == "":
			out = append(out, label+": a voice has no placement:")
		case seen[key]:
			out = append(out, fmt.Sprintf("%s: voice placement %s appears twice (the first one is used)", label, v.Placement))
		}
		seen[key] = true
		if strings.TrimSpace(v.VoiceID) == "" {
			out = append(out, fmt.Sprintf("%s: voice %s has no voice_id:", label, v.Placement))
		}
		if v.NeedsConsent() {
			out = append(out, fmt.Sprintf("%s: voice %s is %s's, a real person's, with no consent: noted: connector exec refuses it until then", label, v.Placement, v.RealPerson))
		}
	}
	return out
}

// atFindings are the mistakes in d's connectors' at: blocks: on a
// connector without url: (only the bridge sees the calls), a status the
// department's workflow doesn't have (that entry never applies, and the
// status the owner meant allows nothing), an entry that limits nothing,
// names in allow: or deny_args: that aren't tool names, and a deny_args:
// argument path that doesn't parse.
func atFindings(p *tracker.Project, d tracker.Department) []Finding {
	var out []Finding
	var statuses []string
	if wf, err := p.WorkflowFor(d.Name); err == nil {
		for _, st := range wf.Statuses {
			statuses = append(statuses, st.Name)
		}
	}
	has := func(s string) bool {
		for _, st := range statuses {
			if strings.EqualFold(st, strings.TrimSpace(s)) {
				return true
			}
		}
		return false
	}
	for _, c := range d.Connectors {
		if len(c.At) == 0 {
			continue
		}
		label := "connector " + c.Name
		switch {
		case c.Kind == tracker.ConnectorRest:
			out = append(out, Finding{Message: label + ": at: does nothing on a rest connector (it only reads, at every status)",
				Fix: "drop at:, or narrow get: to what every status may read"})
		case strings.TrimSpace(c.URL) == "":
			out = append(out, Finding{Message: label + ": at: does nothing without url: (only connector exec's bridge sees a server's calls)",
				Fix: "give the connector url:, or switch tools off with deny_tools:"})
		}
		for _, st := range c.AtStatuses() {
			at := c.At[st]
			if len(statuses) > 0 && !has(st) {
				out = append(out, Finding{Message: fmt.Sprintf("%s: at: %s is not a status of %s's workflow (%s): that entry never applies", label, st, d.Name, strings.Join(statuses, ", ")),
					Fix: "name a status of the department's workflow"})
			}
			if len(at.Allow) == 0 && len(at.DenyArgs) == 0 {
				out = append(out, Finding{Message: fmt.Sprintf("%s: at: %s has neither allow: nor deny_args: (every tool passes there)", label, st)})
			}
			for _, t := range at.Allow {
				if !tracker.ToolNameRe.MatchString(strings.TrimSpace(t)) {
					out = append(out, Finding{Message: fmt.Sprintf("%s: at: %s: allow: %q is not a tool name (letters, digits, _ . -)", label, st, t)})
				}
			}
			for _, t := range slices.Sorted(maps.Keys(at.DenyArgs)) {
				switch {
				case !tracker.ToolNameRe.MatchString(t):
					out = append(out, Finding{Message: fmt.Sprintf("%s: at: %s: deny_args: %q is not a tool name (letters, digits, _ . -)", label, st, t)})
				case len(at.Allow) > 0 && !slices.Contains(at.Allow, t):
					out = append(out, Finding{Message: fmt.Sprintf("%s: at: %s: deny_args: %s is not in allow: (it is refused there anyway)", label, st, t)})
				}
				for _, arg := range slices.Sorted(maps.Keys(at.DenyArgs[t])) {
					if err := tracker.ArgPathError(arg); err != nil {
						out = append(out, Finding{Message: fmt.Sprintf("%s: at: %s: deny_args: %s: %v; until it is fixed the bridge refuses every call of %s", label, st, t, err, t),
							Fix: "write the argument as a name, or a path such as socialPost[].type (\".\" into an object, \"[]\" over every item of an array)"})
					}
				}
			}
		}
	}
	return out
}

// holdFindings are a connector's needs_approved_output: and read_tools:
// mistakes: the hold on a command: mcp server, which the bridge can't see
// into (connector exec then holds the whole server, its read tools too),
// and read_tools: that do nothing or aren't tool names.
func holdFindings(label string, c tracker.Connector, hasURL bool) []string {
	var out []string
	if c.NeedsApprovedOutput && c.Kind == tracker.ConnectorMCP && !hasURL {
		out = append(out, label+": needs_approved_output: on a command: mcp server holds the whole server until the output is approved (the bridge sees only a url: server's calls): give it url: with read_tools:, or switch the sending tools off with deny_tools:")
	}
	if len(c.ReadTools) > 0 {
		switch {
		case !c.NeedsApprovedOutput:
			out = append(out, label+": read_tools: does nothing without needs_approved_output: true")
		case !hasURL:
			out = append(out, label+": read_tools: does nothing without url: (only the bridge lets single tools through)")
		case c.Kind == tracker.ConnectorRest:
			out = append(out, label+": read_tools: does nothing on a rest connector (it has no tools; the hold holds it whole)")
		}
	}
	seen := map[string]bool{}
	for _, t := range c.ReadTools {
		t = strings.TrimSpace(t)
		switch {
		case !tracker.ToolNameRe.MatchString(t):
			out = append(out, fmt.Sprintf("%s: read_tools: %q is not a tool name (letters, digits, _ . -); it stays held", label, t))
		case seen[t]:
			out = append(out, fmt.Sprintf("%s: read_tools: %s appears twice", label, t))
		}
		seen[t] = true
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
		case jobs.IsBuiltin(brief):
			if _, err := jobs.BriefText(p, j); err != nil {
				out = append(out, Finding{Message: label + ": " + err.Error(), Fix: "name a bundled brief or a file under the project root"})
			}
		default:
			path := filepath.Join(p.Root(), filepath.FromSlash(brief))
			if _, err := os.Stat(path); err != nil {
				out = append(out, Finding{Message: fmt.Sprintf("%s: brief %s does not exist", label, brief), Fix: "create " + path + " or fix brief:"})
			}
		}
		if strings.TrimSpace(j.Type) == "" && jobs.BuiltinType(j.Brief) == "" {
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
		if key == "" || known[key] || jobs.IsImplicit(key) || p.IssueClosed(is) {
			continue
		}
		out = append(out, Finding{Where: is.Slug, Message: fmt.Sprintf("open run of job %q, which projects.yaml no longer has", is.Field("job")),
			Fix: "add the job back, or finish or park the run"})
	}
	return out
}

// checkOutputs flags the outputs shelf's problems: log lines Load (and
// LoadComments) skip,
// outputs of a department no longer configured (history, not a warning:
// the department was removed, its outputs stay as its record), a file or
// path that is gone, an output waiting for review longer than outputs.StaleReview.
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
			Fix: "nothing to do; add the department back under departments: to count them again", History: true})
	}
	return out
}

// restWideFindings warn on each get: entry of d's rest connectors whose
// first segment is a wildcard: it reaches every route at the API's first
// level, so a route that changes state on GET would be read too. The fix
// names the first segment.
func restWideFindings(d tracker.Department) []Finding {
	var out []Finding
	for _, c := range d.Connectors {
		if c.Kind != tracker.ConnectorRest {
			continue
		}
		for _, g := range connectors.RestPatterns(&c) {
			if !connectors.RestPatternWide(g) {
				continue
			}
			out = append(out, Finding{
				Message: fmt.Sprintf("connector %s: get: %s opens every route at the API's first level, including any that changes state on GET", c.Name, g),
				Fix:     fmt.Sprintf("name the first segment: get: %s (the paths it reads, e.g. /public/v1/analytics/*)", connectors.RestPatternNarrower(g)),
			})
		}
	}
	return out
}

// restFindings are a connector's get: mistakes: a rest connector with no
// get: (it reads nothing), an entry that isn't a clean path pattern (it is
// skipped at exec) or appears twice, and get: on another kind.
func restFindings(label string, c tracker.Connector) []string {
	var out []string
	if len(c.Get) > 0 && c.Kind != tracker.ConnectorRest {
		return append(out, label+": get: is only for kind: rest (it does nothing here)")
	}
	if c.Kind != tracker.ConnectorRest {
		return nil
	}
	if len(c.Get) == 0 {
		out = append(out, label+": a rest connector needs get: (the paths it may read); without it connector exec reads nothing")
	}
	seen := map[string]bool{}
	for _, g := range c.Get {
		t := strings.TrimSpace(g)
		if why := connectors.RestPatternProblem(t); why != "" {
			out = append(out, fmt.Sprintf("%s: get: %q %s; it is skipped at exec", label, g, why))
		} else if seen[t] {
			out = append(out, fmt.Sprintf("%s: get: %s appears twice", label, t))
		}
		seen[t] = true
	}
	return out
}
