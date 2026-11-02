package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/connections"
	"github.com/kacperwerema/dark-factory/internal/connectors"
	"github.com/kacperwerema/dark-factory/internal/goals"
	"github.com/kacperwerema/dark-factory/internal/spend"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/weekplan"
)

// goalsConfigSnippet is what issue-cli goals prints when no goal is
// configured: where they go and their fields.
const goalsConfigSnippet = `No goals configured. Add them in projects.yaml, under the project:

  company:                          # HQ: the business goals
    mission: "What the business is for"
    prompt: departments/hq/role.md  # the CEO's role prompt (optional)
    goals:
      - {id: clients, name: Paying clients, target: 20, period: quarter}
  departments:
    - name: Marketing
      mission: "What the department is for"
      prompt: departments/marketing/role.md
      goals:
        - id: pages
          name: Client pages refreshed
          target: 40
          period: week                         # week | month | quarter | none
          source: file:docs/seo/run.json#pages  # or manual (issue-cli goal set)
          rolls_up: clients                     # the HQ goal it counts towards

See docs/departments.md.`

var goalsCommand = &Command{
	Name:      "goals",
	ShortHelp: "List HQ's and the departments' goals, rolled up into HQ",
	LongHelp: `Print the project's goals / KPIs: each HQ (business) goal with the
department goals that roll up into it, then each department's goals.
A goal shows its value, target, progress, period and source; a value from
before the current period is marked stale, a source that can't be read
shows why. An HQ goal with no value of its own shows the mean progress of
the goals rolling up into it.

Pass a department (marketing, product, hq) to list only its goals.

Goals live in projects.yaml (company: and departments:); manual values are
set with issue-cli goal set. See docs/departments.md.

Examples:
  issue-cli goals
  issue-cli goals marketing
  issue-cli goals --json`,
	Run: runGoals,
}

var goalCommand = &Command{
	Name:      "goal",
	ShortHelp: "Record a goal's value (goal set <dept>/<id> <value>)",
	LongHelp: `Record a value for a goal whose source is manual. The value is
appended to <issues>/.goals/values.jsonl with today's date (or --date);
issue-cli goals and the company page show the newest one.

<dept> is hq, product or the department's name in lower case; issue-cli
goals lists every key.

Examples:
  issue-cli goal set marketing/pages 38
  issue-cli goal set hq/clients 12 --note "two signed this week"
  issue-cli goal set hq/revenue 4200 --date 2026-09-30`,
	Run:         runGoal,
	Subcommands: []string{"set"},
}

var departmentCommand = &Command{
	Name:      "department",
	ShortHelp: "Departments: list the templates, add or remove one, show one's role and goals",
	LongHelp: `Work with the project's departments (projects.yaml departments:).

  department list               HQ, Product and the bundled department
                                templates, and which this project has
  department add <template>     install a template: its workflow file
                                (workflows/<key>.yaml), role prompt
                                (departments/<key>/role.md) and an entry with
                                its goals in projects.yaml. A file already
                                there is never overwritten. A template
                                installs once, under its own name: one the
                                project has is refused. --parent <group>
                                puts it under a group, --dry-run prints the
                                plan only. A group template (gtm) installs
                                the group and its departments, and moves one
                                you already have under it. It ends with the
                                files git does not track yet and the commit
                                to make; --commit commits exactly those
  department update <name>      bring an installed department up to its
                                template's newer text: a file (role.md, a
                                job's brief) or a role, mission or goal
                                name in projects.yaml is replaced only when
                                it still holds an earlier template's text;
                                what you edited is kept and named.
                                --dry-run says what would change, --side
                                writes the template's text next to each
                                edited file (role.template.md) to compare.
                                The workflow file is replaced only from a
                                text that differs in wording alone; its
                                features move with workflow upgrade
  department remove <name>      take the entry out of projects.yaml (its
                                files stay); refused while it has open
                                issues, or departments under it (a group)
  department show <name>        mission, role, the role prompt (path and
                                content), goals, the open connections
                                in and out (issue-cli connections) and its
                                spend this week (issue-cli spend); <name> is hq, product or
                                a department from projects.yaml
  department week <name>        the week of a group (and its departments)
                                or a department: goals and how far each
                                moved in the last 7 days, outputs waiting
                                on review / reviewed / shipped, jobs,
                                handoffs in and out, what is blocked and on
                                whom, and the run issue's ## Week plan.
                                --write writes the week report to
                                <issues>/.reports/<key>/<ISO week>.md
                                (replacing this week's), shown in the
                                group's room on the company page. It only
                                reads and writes that file: nothing starts

add, update and remove edit the owner's projects.yaml: workers are refused.

Examples:
  issue-cli department list
  issue-cli department add marketing --dry-run
  issue-cli department add marketing
  issue-cli department add marketing --commit
  issue-cli department update marketing --dry-run
  issue-cli department remove marketing
  issue-cli department show marketing
  issue-cli department show hq --json
  issue-cli department week gtm
  issue-cli department week gtm --write`,
	Run:         runDepartment,
	Subcommands: []string{"list", "add", "update", "remove", "show", "week"},
}

func init() {
	registerCommand(goalsCommand)
	registerCommand(goalCommand)
	registerCommand(departmentCommand)
}

func runGoals(ctx *Context, args []string) error {
	pos, err := parsePositionals(ctx, "goals", args, 1)
	if err != nil {
		return err
	}
	rep := goals.Resolve(ctx.Project, ctx.now())
	owners := rep.Owners()
	if len(pos) == 1 {
		o := rep.Find(pos[0])
		if o == nil {
			return fmt.Errorf("no department %q (departments: %s)", pos[0], strings.Join(ownerKeys(rep), ", "))
		}
		owners = []*goals.Owner{o}
	}
	if ctx.JSONOutput {
		if len(pos) == 1 {
			return writeJSON(ctx.Stdout, owners[0])
		}
		return writeJSON(ctx.Stdout, rep)
	}
	if rep.Empty() {
		fmt.Fprintln(ctx.Stdout, goalsConfigSnippet)
		return nil
	}
	for i, o := range owners {
		if i > 0 {
			fmt.Fprintln(ctx.Stdout)
		}
		printOwnerGoals(ctx.Stdout, o)
	}
	return nil
}

// printOwnerGoals prints an owner's heading (with its group) and goals; an
// HQ or group goal lists the goals rolling up into it underneath.
func printOwnerGoals(w io.Writer, o *goals.Owner) {
	if o.Parent != "" {
		fmt.Fprintf(w, "== %s (in group %s) ==\n", o.Name, o.Parent)
	} else {
		fmt.Fprintf(w, "== %s ==\n", o.Name)
	}
	if o.Mission != "" {
		fmt.Fprintf(w, "Mission: %s\n", o.Mission)
	}
	if len(o.Goals) == 0 {
		fmt.Fprintln(w, "  (no goals)")
		return
	}
	for _, g := range o.Goals {
		printGoal(w, g, "  ")
		for _, c := range g.Children {
			printGoal(w, c, "      ↳ "+c.Owner+": ")
		}
	}
}

// printGoal is one goal on a line, then a line for its error or the
// command that sets its first value.
func printGoal(w io.Writer, g *goals.Goal, indent string) {
	parts := []string{g.ValueText()}
	if t := g.TargetText(); t != "" {
		parts[0] += " / " + t
	}
	if g.Progress != nil {
		p := fmt.Sprintf("%d%%", *g.Progress)
		if g.RolledUp {
			unit := "goals"
			if g.RolledFrom == 1 {
				unit = "goal"
			}
			p += fmt.Sprintf(" (mean of %d department %s)", g.RolledFrom, unit)
		}
		parts = append(parts, p)
	}
	if g.Period != "" && g.Period != "none" {
		parts = append(parts, "per "+g.Period)
	}
	if g.Date != "" {
		d := g.Date
		if g.Stale {
			d = "stale, from " + d
		}
		parts = append(parts, d)
	}
	parts = append(parts, g.Source)
	fmt.Fprintf(w, "%s%s [%s]  %s\n", indent, g.Label(), g.Key, strings.Join(parts, " · "))
	pad := strings.Repeat(" ", len([]rune(indent))+2)
	switch {
	case g.Err != "":
		fmt.Fprintf(w, "%s! %s\n", pad, g.Err)
	case g.Value == nil && g.Manual() && !g.RolledUp:
		fmt.Fprintf(w, "%sno value yet: %s\n", pad, g.SetCommand())
	}
}

func ownerKeys(rep *goals.Report) []string {
	var out []string
	for _, o := range rep.Owners() {
		out = append(out, o.Key)
	}
	if len(out) == 0 {
		return []string{"none configured"}
	}
	return out
}

func runGoal(ctx *Context, args []string) error {
	if len(args) == 0 || args[0] != "set" {
		return &usageError{msg: "usage: issue-cli goal set <dept>/<id> <value> [--note \"...\"] [--date YYYY-MM-DD]"}
	}
	fs := newFlagSet("goal set", ctx)
	note := fs.String("note", "", "what the value is about")
	date := fs.String("date", "", "the day the value is for (YYYY-MM-DD; default today)")
	if err := parseFlagsArgs(ctx, fs, args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return &usageError{msg: "usage: issue-cli goal set <dept>/<id> <value> [--note \"...\"] [--date YYYY-MM-DD]"}
	}
	key, raw := fs.Arg(0), fs.Arg(1)
	rep := goals.Resolve(ctx.Project, ctx.now())
	g := rep.Goal(key)
	if g == nil {
		keys := rep.Keys()
		if len(keys) == 0 {
			return fmt.Errorf("no goal %q: no goals configured (issue-cli goals shows how to add them)", key)
		}
		return fmt.Errorf("no goal %q; goals: %s", key, strings.Join(keys, ", "))
	}
	if !g.Manual() {
		return fmt.Errorf("goal %s reads its value from %s, not by hand", g.Key, g.Source)
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return fmt.Errorf("value %q is not a number", raw)
	}
	day := ctx.now().Format("2006-01-02")
	if *date != "" {
		if _, err := time.Parse("2006-01-02", *date); err != nil {
			return fmt.Errorf("--date %q: want YYYY-MM-DD", *date)
		}
		day = *date
	}
	if err := goals.Append(ctx.Project, goals.Entry{Goal: g.Key, Value: v, Date: day, Note: *note}); err != nil {
		return fmt.Errorf("recording %s: %w", g.Key, err)
	}
	g = goals.Resolve(ctx.Project, ctx.now()).Goal(g.Key)
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, g)
	}
	fmt.Fprintf(ctx.Stdout, "✓ %s = %s (%s)\n", g.Key, strconv.FormatFloat(v, 'f', -1, 64), day)
	printGoal(ctx.Stdout, g, "  ")
	fmt.Fprintf(ctx.Stdout, "file: %s\n", goals.ValuesPath(ctx.Project))
	return nil
}

// departmentShow is department show's JSON: the owner and its prompt.
type departmentShow struct {
	*goals.Owner
	PromptPath    string `json:"prompt_path,omitempty"`
	PromptContent string `json:"prompt_content,omitempty"`
	PromptError   string `json:"prompt_error,omitempty"`
	// Connectors are a department's (issue-cli connectors).
	Connectors []connectors.Status `json:"connectors,omitempty"`
	// Connections are the open items on lines into and out of it (issue-cli
	// connections); nil without departments.
	Connections *connections.Counts `json:"connections,omitempty"`
	// Spend is what it spent by week (issue-cli spend).
	Spend *spend.Box `json:"spend,omitempty"`
}

func runDepartment(ctx *Context, args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "list":
		return runDepartmentList(ctx, args[1:])
	case "add":
		return runDepartmentAdd(ctx, args[1:])
	case "update":
		return runDepartmentUpdate(ctx, args[1:])
	case "remove":
		return runDepartmentRemove(ctx, args[1:])
	case "week":
		return runDepartmentWeek(ctx, args[1:])
	case "show":
	default:
		return &usageError{msg: "usage: issue-cli department list|add <template>|update <name>|remove <name>|show <name>|week <name>"}
	}
	pos, err := parsePositionals(ctx, "department show", args[1:], 1)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return &usageError{msg: "usage: issue-cli department show <name>"}
	}
	rep := goals.Resolve(ctx.Project, ctx.now())
	o := rep.Find(pos[0])
	if o == nil {
		return fmt.Errorf("no department %q (departments: %s)\nTemplates you can add: %s", pos[0], strings.Join(ownerKeys(rep), ", "), tracker.InjectProject(ctx.Project, "issue-cli department list"))
	}
	out := departmentShow{Owner: o}
	if o.Prompt != "" {
		out.PromptPath = promptPath(ctx.Project, o.Prompt)
		if b, err := os.ReadFile(out.PromptPath); err != nil {
			out.PromptError = err.Error()
		} else {
			out.PromptContent = string(b)
		}
	}
	if d := ctx.Project.DepartmentByKey(pos[0]); d != nil {
		out.Connectors = connectors.List(ctx.Project, d.Name)
	}
	if len(ctx.Project.Departments) > 0 {
		issues, _ := tracker.LoadIssues(ctx.Project.IssueDir)
		c := connections.CountsByBox(connections.Load(ctx.Project, issues, ctx.now()))[o.Key]
		out.Connections = &c
	}
	sp := loadSpend(ctx)
	out.Spend = sp.Find(o.Key)
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, out)
	}
	w := ctx.Stdout
	printOwnerGoals(w, o)
	if o.Role != "" {
		fmt.Fprintf(w, "Role: %s\n", o.Role)
	}
	if c := out.Connections; c != nil {
		fmt.Fprintf(w, "Connections: in %d · out %d open (issue-cli connections --to %s, --from %s)\n", c.In, c.Out, o.Key, o.Key)
	}
	if line := spendLine(sp, o.Key); line != "" {
		fmt.Fprintln(w, line)
	}
	if d := ctx.Project.DepartmentByKey(o.Key); d != nil && ctx.Project.IsGroup(d.Name) {
		week := "none yet"
		if h := weekplan.Latest(ctx.Project, o.Key); h != nil {
			week = h.Week + ": " + h.Counts.Line() + " (" + h.Path + ")"
		}
		fmt.Fprintf(w, "Week report: %s. The week: issue-cli department week %s [--write]\n", week, o.Key)
	}
	if len(out.Connectors) > 0 {
		fmt.Fprintln(w, "Connectors:")
		printConnectors(w, out.Connectors, ctx.now(), "  ")
	}
	switch {
	case o.Prompt == "":
		fmt.Fprintln(w, "Role prompt: none (add prompt: <file.md> to the department in projects.yaml)")
	case out.PromptError != "":
		fmt.Fprintf(w, "Role prompt: %s\n  ! %s\n", out.PromptPath, out.PromptError)
	default:
		fmt.Fprintf(w, "Role prompt: %s\n\n%s\n", out.PromptPath, strings.TrimRight(out.PromptContent, "\n"))
	}
	printDepartmentJobs(ctx, w, o.Key)
	return nil
}

// promptPath is a prompt: file as a path: relative to the project root.
func promptPath(p *tracker.Project, prompt string) string {
	if filepath.IsAbs(prompt) {
		return prompt
	}
	return filepath.Join(p.Root(), prompt)
}

// departmentLine is the one line start and show print for an issue whose
// department (or Product) has a role prompt or goals; "" otherwise.
func departmentLine(p *tracker.Project, issue *tracker.Issue) string {
	name := p.DepartmentOf(issue)
	var mission, prompt string
	var n int
	key := goals.ProductKey
	if name == "" {
		if p.Product == nil {
			return ""
		}
		name, prompt, mission, n = tracker.ProductName, p.Product.Prompt, p.Product.Mission, len(p.Product.Goals)
	} else {
		for _, d := range p.Departments {
			if d.Name == name {
				prompt, mission, n = d.Prompt, d.Mission, len(d.Goals)
			}
		}
		key = goals.DeptKey(name)
	}
	if prompt == "" && n == 0 {
		return ""
	}
	parts := []string{}
	if prompt != "" {
		parts = append(parts, "role: "+prompt)
	}
	if n > 0 {
		parts = append(parts, "goals: issue-cli goals "+key)
	}
	line := "Department: " + name
	if mission != "" {
		line += " (" + mission + ")"
	}
	return line + " — " + strings.Join(parts, " · ")
}
