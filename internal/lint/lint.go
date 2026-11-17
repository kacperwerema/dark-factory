// Package lint checks a project's workflow.yaml and its board for problems
// the engine would otherwise meet silently: undated issues, unknown keys,
// an old template stamp, placeholders nothing fills, gates on sections no
// template writes. Every finding is a warning, except one marked History:
// a record of something the project removed on purpose (a decision scoped
// to, or an output of, a department projects.yaml no longer has), which
// the report lists apart and no count includes. issue-cli health lint prints
// them, the health family lint records their count (the lint score
// component) and /health lists them live. See docs/workbench-files.md,
// "The lint family".
package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Finding is one problem a check found. Every finding is a warning: lint
// lowers the health score, it never fails a run.
type Finding struct {
	Check string `json:"check"`
	// Where is "workflow.yaml:<line>", "workflow.yaml" or an issue slug.
	Where   string `json:"where"`
	Message string `json:"message"`
	// Fix is the command to run or the edit to make; empty when the
	// message says it.
	Fix string `json:"fix,omitempty"`
	// History marks a record of something removed on purpose: Run puts it
	// in Report.History, not Findings, and counts no warning for it.
	History bool `json:"-"`
	// Note marks a line worth reading that is no fault of the config: a
	// new project with no goals yet (goal-without-work). Run puts it in
	// Report.Notes and counts no warning, so a fresh project stays clean.
	Note bool `json:"-"`
}

// Check is one named check.
type Check struct {
	Name string
	Help string
	Run  func(env *Env) []Finding
}

// checks is every check, in the order lint runs and prints them. A new
// check is one entry here.
var checks = []Check{
	{"undated", "issues without a created: date (the criteria_proven gate counts them as new)", checkUndated},
	{"workflow", "types:, workbench:, ci: and health: problems (the warnings process workflow prints)", checkWorkflow},
	{"schema", "workflow.yaml keys the parser doesn't know, missing statuses:/transitions:, deprecated keys", checkSchema},
	{"template", "the template: stamp against the template bundled in this issue-cli", checkTemplate},
	{"placeholders", "{{name}} placeholders the field never fills, so the agent sees them as written", checkPlaceholders},
	{"sections", "checkbox and test-plan gates on sections no template writes the way the gate needs", checkSections},
	{"departments", "projects.yaml company:/departments: problems: missing prompt files, unknown rolls_up:, bad goal fields; connections: routes; outputs shelf lines, gone files, outputs waiting for review; open issues whose system: names a department they aren't in; a mission or goals still the template's sample", checkDepartments},
	{"untracked", "department files (role prompt, workflow file, job briefs, departments/<key>/) git does not track, so they can be lost or slip into an unrelated commit", checkUntracked},
	{"paths", "workbench.systems paths: globs that match no file in the repository or leave it", checkPaths},
	{"contexts", "the code map (workbench.contexts): a Go package no context lists, an import up a layer that leaks: doesn't list, a leak that no longer happens, a context name used twice, a package in two contexts, a layer: or packages: entry naming nothing", checkContexts},
	{"roles", "status role: keys: an unknown role, a role on two statuses or a closing status, and a file filling some roles but not all, which is how a renamed status looks", checkRoles},
	{"rules", "rules.md lines that reach nobody, repeated keys, a layer over the briefing's cap; project rules: with no text, a repeated id, narrows: naming no machine rule, or contradicting a machine rule without narrows:", checkRules},
	{"owner-only", "steps that spend or publish (publish/launch/send statuses, sends: connectors) behind a gate YOLO may pass: no owner_only", checkOwnerOnly},
	{"na-boxes", "ticked N/A boxes on open issues whose reason is not in a named form (issue-cli check --help), or that sit on the owner's box", checkNABoxes},
	{"owner-marker", "open acceptance criteria that read like the owner's check (\"the owner confirms …\") without the (owner) marker", checkOwnerMarker},
	{"show-footer", "issue sections holding issue-cli show's own output (== Checklist ==, the tick hints, == Comments ==), pasted when a section is rebuilt from show; quotes in code spans and fences don't count", checkShowFooter},
	{"criteria", "open acceptance criteria no test, command or playtest step could fail (\"it works\", \"is clear\"), and a ## Must not left as the template's placeholder; owner boxes and types whose path skips testing (report, docs, chore) are exempt from the test ask", checkCriteria},
	{"types", "open docs-type issues whose ## Wanted or open acceptance criteria name a test, a package or a .go path: work the docs path has no Test Plan for", checkTypes},
	{"reports", "issues past a has_output gate (the report type) whose output is gone from the shelf: never added, file deleted, or rejected and not replaced; done reports whose Follow-ups no issue or queue item quotes", checkReports},
	{"routing", "routing: rules that pick the agent CLI (claude, codex): agents not installed or not command names, unknown types, statuses or departments, rules after a catch-all, a fallback that repeats or leaves out the default", checkRouting},
	{"workers", "workers: settings a worker launch can't use: setup: other than lean or full, an mcp: item with no server or a server the owner's Claude Code config does not define, a type the file does not define, a plugin that is not name@marketplace", checkWorkers},
	{"memory", "memory: embedder: settings search can't use: both or neither of url and command, a URL that leaves this machine (hosted is the owner's gate), no model, a bad timeout or min_similarity; memories whose --at names a status no workflow has; a cluster of 3+ memories retelling one fact; a pitfall or howto that names a step with no --at", checkMemory},
	{"evals", "evals: entries the factory benchmark can't use: no or repeated id, unknown score, pass outside 0-1, a type the file doesn't define", checkEvals},
	{"department", "department workflow files (projects.yaml departments: workflow:) that don't load, project-wide keys in them, issues whose status their department's file lacks", checkDepartment},
	{"critique", "the fresh critic's settings: a critique: timeout that doesn't parse, an agent with no read-only command, a critique: block no edge uses, a critique gate out of the first status", checkCritique},
	{"hints", "validate actions whose hint: the rule never shows (criteria_*, approved_for, decisions_*, critique, has_output and other rules that keep their own fix text)", checkHints},
	{"decisions", "D-n attribute groups the decision ledger can't use: a {…} naming a decision key that doesn't parse or a scope naming no system, department or type; hard without the owner's words; paths: matching no file; at: naming no status", checkDecisions},
	{"testids", "playtest session steps (playtest/sessions) whose selector names a data-testid internal/testids does not list, so the step fails only in Chrome", checkTestIDs},
	{"shas", "commit refs in a done issue's ## Implementation that main does not reach (the SHA from before an amend or rebase)", checkSHAs},
	{"goal-without-work", "goals in projects.yaml (HQ, Product, departments) and open epics with no open issue, active queue item or job behind them; a project with no goals at all, once", checkGoalWithoutWork},
	{"roadmap-behind", "docs/roadmap.md's version table stopping before the running milestone (the highest version: an open issue carries): names each milestone it lacks", checkRoadmapBehind},
	{"worktrees", "a workflow with no worktree: true whose open issues have worktrees under .worktrees/ all the same, made by hand (worktree: true would make work/<slug> for them)", checkWorktrees},
}

// boardChecks read the board or the repository, not only a workflow file:
// they run once. The other checks run on Product's file and again on each
// department's own (workbench: is project-wide, so paths runs on Product's).
var boardChecks = map[string]bool{"rules": true, "undated": true, "na-boxes": true, "owner-marker": true, "show-footer": true, "criteria": true, "types": true, "reports": true, "department": true, "departments": true, "untracked": true, "paths": true, "contexts": true, "testids": true, "decisions": true, "shas": true, "worktrees": true, "goal-without-work": true, "roadmap-behind": true}

// Checks lists every check in run order.
func Checks() []Check { return append([]Check(nil), checks...) }

// Env is what the checks read.
type Env struct {
	Project  *tracker.Project
	Workflow *tracker.WorkflowConfig
	// File is the workflow.yaml the project loads, "" for the built-in
	// default; Raw is its content.
	File   string
	Raw    []byte
	Issues []*tracker.Issue
	Now    time.Time
	// Department is the department whose own workflow file this is; ""
	// for Product's.
	Department string
}

// Load reads the project's workflow.yaml and issues.
func Load(proj *tracker.Project, now time.Time) (*Env, error) {
	issues, err := proj.LoadIssues()
	if err != nil {
		return nil, fmt.Errorf("loading issues: %w", err)
	}
	return NewEnv(proj, issues, now), nil
}

// NewEnv is Load for a caller that already has the issues (the Status page).
func NewEnv(proj *tracker.Project, issues []*tracker.Issue, now time.Time) *Env {
	env := &Env{Project: proj, Workflow: proj.LoadWorkflow(), Issues: issues, Now: now}
	for _, f := range []string{proj.WorkflowFile, "workflow.yaml"} {
		if f == "" {
			continue
		}
		if raw, err := os.ReadFile(f); err == nil {
			env.File, env.Raw = f, raw
			break
		}
	}
	return env
}

// Report is a lint run's findings and their counts.
type Report struct {
	Project  string    `json:"project"`
	Checks   []string  `json:"checks"`
	Findings []Finding `json:"findings"`
	Warnings int       `json:"warnings"`
	// History is the info lines: records of what the project removed on
	// purpose (Finding.History). They lower no score.
	History []Finding `json:"history,omitempty"`
	// Notes are info lines that lower no score (Finding.Note).
	Notes []Finding `json:"notes,omitempty"`
}

// Run runs the named checks (every check when names is empty).
func Run(env *Env, names ...string) (*Report, error) {
	run := checks
	if len(names) > 0 {
		run = nil
		for _, n := range names {
			c, ok := find(n)
			if !ok {
				return nil, fmt.Errorf("unknown check %q (checks: %s)", n, strings.Join(Names(), ", "))
			}
			run = append(run, c)
		}
	}
	rep := &Report{Project: env.Project.Slug, Findings: []Finding{}}
	depts := env.departmentEnvs()
	for _, c := range run {
		rep.Checks = append(rep.Checks, c.Name)
		envs := []*Env{env}
		if !boardChecks[c.Name] {
			envs = append(envs, depts...)
		}
		for _, e := range envs {
			for _, f := range c.Run(e) {
				f.Check = c.Name
				if f.History {
					rep.History = append(rep.History, f)
					continue
				}
				if f.Note {
					rep.Notes = append(rep.Notes, f)
					continue
				}
				rep.Findings = append(rep.Findings, f)
				rep.Warnings++
			}
		}
	}
	return rep, nil
}

// File runs the checks that read only a workflow file, not the board, on
// raw as the content of path (which need not exist yet): the workflow
// editor's preview of an edit before it is written. An error means raw
// does not parse.
func File(path string, raw []byte, now time.Time) ([]Finding, error) {
	var wf tracker.WorkflowConfig
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		return nil, err
	}
	env := &Env{Project: &tracker.Project{}, Workflow: &wf, File: path, Raw: raw, Now: now}
	var out []Finding
	for _, c := range checks {
		if boardChecks[c.Name] {
			continue
		}
		for _, f := range c.Run(env) {
			f.Check = c.Name
			out = append(out, f)
		}
	}
	return out, nil
}

// EditCheck is File for the station editor (wfedit.Apply's check): the
// file's findings without the unknown-rule ones, which the editor names
// per action itself.
func EditCheck(path string, raw []byte, now time.Time) ([]Finding, error) {
	fs, err := File(path, raw, now)
	out := fs[:0]
	for _, f := range fs {
		if !UnknownRule(f) {
			out = append(out, f)
		}
	}
	return out, err
}

// departmentEnvs is an Env per department workflow file that loads: its
// own file as written (not merged with Product's), and the issues that
// walk it. Files that don't load are the department check's to report.
func (env *Env) departmentEnvs() []*Env {
	var out []*Env
	for _, d := range env.Project.WorkflowDepartments() {
		path := env.Project.DepartmentWorkflowPath(d.Name)
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		wf, err := tracker.LoadWorkflow(path)
		if err != nil {
			continue
		}
		var issues []*tracker.Issue
		for _, issue := range env.Issues {
			if env.Project.WorkflowDepartment(issue) == d.Name {
				issues = append(issues, issue)
			}
		}
		out = append(out, &Env{Project: env.Project, Workflow: wf, File: path, Raw: raw, Issues: issues, Now: env.Now, Department: d.Name})
	}
	return out
}

// Names lists every check name in run order.
func Names() []string {
	var out []string
	for _, c := range checks {
		out = append(out, c.Name)
	}
	return out
}

func find(name string) (Check, bool) {
	for _, c := range checks {
		if c.Name == name {
			return c, true
		}
	}
	return Check{}, false
}

// where names a workflow.yaml line, or the file alone for line 0.
func (env *Env) where(line int) string {
	name := "workflow.yaml"
	if env.File != "" {
		name = filepath.Base(env.File)
	}
	if line <= 0 {
		return name
	}
	return fmt.Sprintf("%s:%d", name, line)
}

// lineOf is the first non-comment line of workflow.yaml containing needle,
// or 0.
func (env *Env) lineOf(needle string) int {
	if needle == "" {
		return 0
	}
	for i, line := range strings.Split(string(env.Raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if strings.Contains(line, needle) {
			return i + 1
		}
	}
	return 0
}
