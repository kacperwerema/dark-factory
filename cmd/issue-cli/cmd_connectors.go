package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/connectors"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

const connectorsSnippet = `No connectors configured. A department lists the outside tools its agents
may use in projects.yaml, with secrets referenced, never written:

  departments:
    - name: Marketing
      connectors:
        - name: search-console
          kind: mcp                       # mcp (a server the agent loads) or tool
          command: npx
          args: ["-y", "@example/gsc-mcp"]
          secrets:
            GSC_TOKEN: keychain:acme-gsc  # or env:ACME_GSC_TOKEN
          check: "gsc-cli whoami"         # read-only; exit 0 = ok
          sends: false                    # true: it can publish or spend

See docs/departments.md#connectors.`

var connectorsCommand = &Command{
	Name:      "connectors",
	ShortHelp: "List the departments' connectors, their secrets (set / missing) and last check",
	LongHelp: `Print each department's connectors: kind, each secret's reference as set
or missing on this machine (decided without reading the value), whether it
sends, and the last health check with its age.

Pass a department (marketing) to list only its connectors.

Examples:
  issue-cli connectors
  issue-cli connectors marketing --json`,
	Run: runConnectors,
}

var connectorCommand = &Command{
	Name:      "connector",
	ShortHelp: "Run a department's connector, or its health check (connector exec|check)",
	LongHelp: `Run an outside tool of a department with its secrets, for that
department's agents only.

  connector exec <dept>/<name> [--as-owner] [-- <cmd> ...]
      An mcp connector runs its server (command, args) on stdin/stdout; the
      agent's MCP config points here. A tool connector runs <cmd> after --
      (else its command:). Its secrets are read now and put into the
      child's environment only. Refused for another department's agents,
      Product's, the CEO and a shell outside any agent session; the owner
      adds --as-owner in their own shell.
  connector check [<dept>[/<name>]]
      Runs each check: (read-only) with its secrets, a 20 s timeout and its
      output thrown away, and records ok / failing / missing / no-check in
      <issues>/.connectors/checks.jsonl. The owner or the department's lead.

It guards against mistakes; it is not a security boundary: anything
running as the owner can read their environment and Keychain.

Examples:
  issue-cli connector exec marketing/wordpress -- wp post list
  issue-cli connector check marketing`,
	Run:         runConnector,
	Subcommands: []string{"exec", "check"},
}

func init() {
	registerCommand(connectorsCommand)
	registerCommand(connectorCommand)
}

func runConnectors(ctx *Context, args []string) error {
	pos, err := parsePositionals(ctx, "connectors", args, 1)
	if err != nil {
		return err
	}
	p := ctx.Project
	dept := ""
	if len(pos) == 1 {
		d := p.DepartmentByKey(pos[0])
		if d == nil {
			return fmt.Errorf("no department %q (departments: %s)", pos[0], strings.Join(p.DepartmentKeys(), ", "))
		}
		dept = d.Name
	}
	list := connectors.List(p, dept)
	if ctx.JSONOutput {
		if list == nil {
			list = []connectors.Status{}
		}
		return writeJSON(ctx.Stdout, list)
	}
	if len(list) == 0 {
		fmt.Fprintln(ctx.Stdout, connectorsSnippet)
		return nil
	}
	printConnectors(ctx.Stdout, list, ctx.now(), "")
	return nil
}

// printConnectors prints list grouped by department.
func printConnectors(w io.Writer, list []connectors.Status, now time.Time, indent string) {
	last := ""
	for _, s := range list {
		if s.Department != last {
			fmt.Fprintf(w, "%s== %s ==\n", indent, s.Department)
			last = s.Department
		}
		line := fmt.Sprintf("%s%s (%s)", indent, s.Address, s.Kind)
		if s.Sends {
			line += " · sends"
		}
		line += " · " + checkWords(s, now)
		fmt.Fprintln(w, line)
		for _, sec := range s.Secrets {
			state := "missing"
			if sec.Set {
				state = "set"
			}
			if sec.Err != "" {
				state = "invalid: " + sec.Err
			}
			fmt.Fprintf(w, "%s  %s %s %s\n", indent, sec.Var, sec.Ref, state)
		}
		if s.Note != "" {
			fmt.Fprintf(w, "%s  %s\n", indent, s.Note)
		}
	}
}

// checkWords is a connector's last check as words: "check ok 3h ago".
func checkWords(s connectors.Status, now time.Time) string {
	switch {
	case !s.HasCheck:
		return "no check:"
	case s.Last == nil:
		return "never checked (issue-cli connector check " + s.Address + ")"
	}
	w := "check " + s.Last.Result
	if s.Last.Result == connectors.ResultFailing {
		w += fmt.Sprintf(" (exit %d)", s.Last.Exit)
	}
	return w + " " + ageWords(now.Sub(s.Last.Date))
}

func ageWords(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func runConnector(ctx *Context, args []string) error {
	if len(args) == 0 {
		return &usageError{msg: "usage: issue-cli connector exec <dept>/<name> [-- <cmd> ...] | check [<dept>[/<name>]] (issue-cli help connector)"}
	}
	switch args[0] {
	case "exec":
		return runConnectorExec(ctx, args[1:])
	case "check":
		return runConnectorCheck(ctx, args[1:])
	}
	return &usageError{msg: fmt.Sprintf("unknown connector command %q: exec or check", args[0])}
}

// connectorCaller is who runs this command, from the session's signals.
func connectorCaller(p *tracker.Project, asOwner bool) connectors.Caller {
	issues, _ := tracker.LoadIssues(p.IssueDir)
	me := whoAmI(p, issues)
	switch me.Role {
	case tracker.RoleDepartmentLead:
		return connectors.Caller{Department: me.Department, Lead: true, Agent: true}
	case tracker.RoleProjectLead:
		return connectors.Caller{Lead: true, Agent: true}
	case tracker.RoleWorker:
		c := connectors.Caller{Agent: true}
		if issue := tracker.FindIssue(issues, me.Slug); issue != nil {
			c.Department = p.DepartmentOf(issue)
		}
		return c
	}
	return connectors.Caller{Owner: asOwner}
}

func runConnectorExec(ctx *Context, args []string) error {
	var child []string
	for i, a := range args {
		if a == "--" {
			args, child = args[:i], args[i+1:]
			break
		}
	}
	fs := newFlagSet("connector exec", ctx)
	asOwner := fs.Bool("as-owner", false, "the owner's own shell, outside any agent session")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return &usageError{msg: "usage: issue-cli connector exec <dept>/<name> [--as-owner] [-- <cmd> ...]"}
	}
	p := ctx.Project
	dept, c, err := connectors.Find(p, fs.Arg(0))
	if err != nil {
		return err
	}
	caller := connectorCaller(p, *asOwner)
	if *asOwner && caller.Agent {
		return fmt.Errorf("--as-owner is for the owner's own shell, not an agent session")
	}
	if err := connectors.Allowed(p, dept, caller); err != nil {
		return err
	}
	argv, err := connectors.Command(c, child)
	if err != nil {
		return err
	}
	env, err := connectors.Env(c, os.Environ())
	if err != nil {
		return err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Dir = p.Root()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = ctx.Stdin, ctx.Stdout, ctx.Stderr
	err = cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &exitCodeError{Code: exitErr.ExitCode()}
	}
	return err
}

func runConnectorCheck(ctx *Context, args []string) error {
	pos, err := parsePositionals(ctx, "connector check", args, 1)
	if err != nil {
		return err
	}
	p := ctx.Project
	caller := connectorCaller(p, true)
	type target struct {
		dept *tracker.Department
		c    *tracker.Connector
	}
	var targets []target
	switch {
	case len(pos) == 1 && strings.Contains(pos[0], "/"):
		d, c, err := connectors.Find(p, pos[0])
		if err != nil {
			return err
		}
		targets = append(targets, target{d, c})
	default:
		for i := range p.Departments {
			d := &p.Departments[i]
			if len(pos) == 1 && tracker.DepartmentKey(d.Name) != tracker.DepartmentKey(pos[0]) {
				continue
			}
			for j := range d.Connectors {
				targets = append(targets, target{d, &d.Connectors[j]})
			}
		}
		if len(pos) == 1 && p.DepartmentByKey(pos[0]) == nil {
			return fmt.Errorf("no department %q (departments: %s)", pos[0], strings.Join(p.DepartmentKeys(), ", "))
		}
	}
	if len(targets) == 0 {
		fmt.Fprintln(ctx.Stdout, connectorsSnippet)
		return nil
	}
	var recs []connectors.Record
	for _, t := range targets {
		// The CEO and Product's workers don't check: a lead checks its own
		// department's connectors, the owner any.
		if caller.Agent && !(caller.Lead && strings.EqualFold(caller.Department, t.dept.Name)) {
			return fmt.Errorf("connector checks are for the owner or %s's lead (%s)", t.dept.Name, tracker.DepartmentLeadName(p, t.dept.Name))
		}
		recs = append(recs, connectors.RunCheck(context.Background(), t.dept.Name, t.c, ctx.now))
	}
	if err := connectors.Append(p, recs...); err != nil {
		return err
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, recs)
	}
	failing := 0
	for _, r := range recs {
		mark := "✓"
		switch r.Result {
		case connectors.ResultFailing, connectors.ResultMissing:
			mark = "✗"
			failing++
		case connectors.ResultNoCheck:
			mark = "·"
		}
		line := fmt.Sprintf("%s %s %s", mark, r.Connector, r.Result)
		if r.Result == connectors.ResultFailing {
			line += fmt.Sprintf(" (exit %d, %d ms)", r.Exit, r.MS)
		}
		if r.Result == connectors.ResultMissing {
			line += " (a secret is missing: issue-cli connectors " + strings.SplitN(r.Connector, "/", 2)[0] + ")"
		}
		fmt.Fprintln(ctx.Stdout, line)
	}
	fmt.Fprintf(ctx.Stdout, "file: %s\n", connectors.LogPath(p))
	if failing > 0 {
		return &exitCodeError{Code: 1}
	}
	return nil
}
