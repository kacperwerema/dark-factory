package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kacperwerema/dark-factory/internal/connectors"
	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/socials"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

const connectorsSnippet = `No connectors configured. A department lists the outside tools its agents
may use in projects.yaml, with secrets referenced, never written:

  departments:
    - name: Marketing
      connectors:
        - name: search-console
          kind: mcp                       # mcp (a server the agent loads), tool or rest
          command: npx
          args: ["-y", "@example/gsc-mcp"]
          secrets:
            GSC_TOKEN: keychain:acme-gsc  # or env:ACME_GSC_TOKEN
            GSC_QUOTA_KEY: keychain:acme-gsc-quota
          optional: [GSC_QUOTA_KEY]       # runs without it when not set
          check: "gsc-cli whoami"         # read-only; exit 0 = ok
          sends: false                    # true: it can publish or spend
        - name: openseo                   # a remote MCP server (Streamable HTTP)
          kind: mcp
          url: http://localhost:3011/mcp  # https, or http on this machine only
          headers:
            Authorization: "Bearer keychain:openseo"  # prefix + secret reference
          deny_tools: [submit_indexnow_urls, set_report_sharing, set_ai_visibility_schedule]  # switched off at launch
        - name: postiz                    # drafts and posts with one key
          kind: mcp
          url: https://postiz.example.com/mcp
          headers:
            Authorization: "keychain:postiz"
          sends: true
          needs_approved_output: true     # tools/call held until the issue's output is approved
          read_tools: [list_integrations] # these pass before
          at:                             # tools by the issue's status, read at each call
            draft: {allow: [list_integrations, create_post], deny_args: {create_post: {type: [now]}}}
            publish: {allow: [create_post], deny_args: {create_post: {"posts[].type": [now]}}}  # a path into every item
          per_run:                        # a heavy tool: up only while a run needs it
            start: "docker compose -p openseo start"   # returns once launched
            ready: "curl -fsS http://localhost:3011/health"  # polled until exit 0
            stop: "docker compose -p openseo stop"
            timeout: 60                   # seconds, the default
            tool: openseo                 # optional: connectors naming one tool share its runs
        - name: social-analytics          # a read-only REST API: GET only, made by issue-cli
          kind: rest
          url: http://127.0.0.1:4007/api  # the base the paths are read under
          get: [/public/v1/analytics/post/*, /public/v1/posts]  # what it may read ("*" one segment)
          headers:
            Authorization: "keychain:postiz"

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
      agent's MCP config points here. One with url: is bridged in this
      process to its Streamable HTTP server, its headers: secrets added to
      each request (https, or http on localhost; no redirects followed). A tool connector runs <cmd> after --
      (else its command:). Its secrets are read now and put into the
      child's environment only. Refused for another department's agents,
      Product's, the CEO and a shell outside any agent session; the owner
      adds --as-owner in their own shell. With per_run: the tool starts
      first, after mem-guard check, and stops when the last run ends.
  connector exec <dept>/<name> -- <path> [<path>...]
      A rest connector (kind: rest) makes one GET per path under its url:,
      in this process with its headers: secrets, and prints one JSON line
      each: {"path", "status", "body"}. GET is the only method; a path its
      get: patterns don't match is refused before anything starts. Exit 1
      when any answer isn't a 2xx.
  connector check [<dept>[/<name>]]
      Runs each check: (read-only) with its secrets, a 20 s timeout and its
      output thrown away, and records ok / failing / missing / no-check in
      <issues>/.connectors/checks.jsonl. The owner or the department's lead.
      A per_run: tool no run holds records stopped, and isn't checked.
  connector sweep [<dept>]
      Stops per_run: tools the engine started and no run holds any more (a
      killed run). The viewer does this every minute. Never starts one. The
      owner or the department's lead.

It guards against mistakes; it is not a security boundary: anything
running as the owner can read their environment and Keychain.

Examples:
  issue-cli connector exec marketing/wordpress -- wp post list
  issue-cli connector exec marketing/social-analytics -- /public/v1/analytics/post/<id>?date=7
  issue-cli connector check marketing`,
	Run:         runConnector,
	Subcommands: []string{"exec", "check", "sweep"},
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
	// A social connector whose tool the viewer's Connect socials page drives:
	// where the owner connects networks (agents never do).
	for _, d := range p.Departments {
		if dept != "" && d.Name != dept {
			continue
		}
		for i := range d.Connectors {
			if c := &d.Connectors[i]; c.Name == "social" && socials.For(c) != nil {
				fmt.Fprintf(ctx.Stdout, "connect networks (the owner, in the browser): %s/p/%s/socials/%s\n", viewerBaseURL(p), p.Slug, tracker.DepartmentKey(d.Name))
			}
		}
	}
	return nil
}

// atStatuses are an at: block's statuses, sorted.
func atStatuses(at map[string]tracker.ConnectorAt) []string {
	return tracker.Connector{At: at}.AtStatuses()
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
		if len(s.DenyTools) > 0 {
			line += " · tools off: " + strings.Join(s.DenyTools, ", ")
		}
		if len(s.Get) > 0 {
			line += " · reads GET " + strings.Join(s.Get, ", ") + " (connector exec " + s.Address + " -- <path>...)"
		}
		if s.Sends {
			line += " · sends"
		}
		if s.NeedsApprovedOutput {
			line += " · needs an approved output"
			if len(s.ReadTools) > 0 {
				line += " (open before: " + strings.Join(s.ReadTools, ", ") + ")"
			}
		}
		if len(s.At) > 0 {
			line += " · tools by status (at: " + strings.Join(atStatuses(s.At), ", ") + ")"
		}
		if w := s.RunWords(); w != "" {
			line += " · " + w
		}
		line += " · " + checkWords(s, now)
		fmt.Fprintln(w, line)
		for _, sec := range s.Secrets {
			state := "missing"
			if sec.Set {
				state = "set"
			}
			if sec.Optional {
				if !sec.Set {
					state = "not set"
				}
				state += " (optional)"
			}
			if sec.Err != "" {
				state = "invalid: " + sec.Err
			}
			name := sec.Var
			if sec.Header {
				name = "header " + name + ":"
			}
			fmt.Fprintf(w, "%s  %s %s %s\n", indent, name, sec.Ref, state)
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
		return &usageError{msg: "usage: issue-cli connector exec <dept>/<name> [-- <cmd> ...] | check [<dept>[/<name>]] | sweep [<dept>] (issue-cli help connector)"}
	}
	switch args[0] {
	case "exec":
		return runConnectorExec(ctx, args[1:])
	case "check":
		return runConnectorCheck(ctx, args[1:])
	case "sweep":
		return runConnectorSweep(ctx, args[1:])
	}
	return &usageError{msg: fmt.Sprintf("unknown connector command %q: exec, check or sweep", args[0])}
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
			c.Issue = issue.Slug
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
		return &usageError{msg: "usage: issue-cli connector exec <dept>/<name> [--as-owner] [-- <cmd> ... | -- <path> ... for a rest connector]"}
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
	addr := tracker.ConnectorAddress(dept.Name, c.Name)
	checks := callChecks{hold: connectorHold(p, addr, c, caller)}
	if c.Kind == tracker.ConnectorRest {
		return runConnectorRest(ctx, dept, addr, c, child, checks.hold)
	}
	// A tool or a stdio server is held whole: nothing starts, no secret is
	// read. A url: server is held per tools/call, in the bridge, which is
	// also the only place at: is asked (lint warns on it elsewhere).
	if checks.hold != nil && c.URL == "" {
		if err := checks.hold(); err != nil {
			return err
		}
	}
	if c.URL != "" {
		checks.gate = connectorAt(p, addr, c, caller)
	}
	if c.PerRun != nil {
		return runConnectorPerRun(ctx, dept, c, child, checks)
	}
	return runConnectorChild(context.Background(), ctx, c, child, checks)
}

// callChecks are the bridge's per-call checks: the needs_approved_output:
// hold and the at: gate, each nil when the connector has none.
type callChecks struct {
	hold func() error
	gate func(tool string, args json.RawMessage) error
	// lazy and wake keep a per_run: tool down until a request its saved
	// copy can't answer; save keeps the copy current. All nil for any other
	// connector.
	lazy *connectors.Listing
	wake func(context.Context) error
	save func(key string, result json.RawMessage)
}

// connectorAt is c's at: gate for caller: nil when c has no at: or the
// owner runs it. A caller with no issue (a lead, the CEO) is refused at
// each call; a worker's issue status is read again at each call, so a
// transition during the session changes what passes.
func connectorAt(p *tracker.Project, addr string, c *tracker.Connector, caller connectors.Caller) func(string, json.RawMessage) error {
	if len(c.At) == 0 || caller.Owner {
		return nil
	}
	return func(tool string, args json.RawMessage) error {
		if err := connectors.AtCallerError(addr, caller); err != nil {
			return err
		}
		issues, err := tracker.LoadIssues(p.IssueDir)
		if err != nil {
			return fmt.Errorf("connector %s allows its tools per issue status (at:) and the issues could not be read", addr)
		}
		issue := tracker.FindIssue(issues, caller.Issue)
		if issue == nil {
			return fmt.Errorf("connector %s allows its tools per issue status (at:) and issue %s is not on the board", addr, caller.Issue)
		}
		return connectors.AtError(addr, c, issue.Status, tool, args)
	}
}

// connectorHold is c's needs_approved_output: hold for caller, read from
// the shelf each time it is asked; nil when c has none or the owner runs it.
func connectorHold(p *tracker.Project, addr string, c *tracker.Connector, caller connectors.Caller) func() error {
	if !c.NeedsApprovedOutput || caller.Owner {
		return nil
	}
	return func() error {
		var shelf error
		if caller.Issue != "" {
			// A bad line is skipped, as the outputs_approved gate does.
			list, _ := outputs.Load(p)
			shelf = outputs.ApprovedError(list, caller.Issue, "")
		}
		return connectors.HoldError(addr, caller, shelf)
	}
}

// runConnectorPerRun is exec for a connector with per_run:: its tool is up
// for the child's or the bridge's life, and a signal ends the run the same
// way an exit does, so the tool stops either way.
func runConnectorPerRun(ctx *Context, dept *tracker.Department, c *tracker.Connector, child []string, checks callChecks) error {
	// A wrong call starts nothing.
	if c.URL == "" {
		if _, err := connectors.Command(c, child); err != nil {
			return err
		}
	} else if len(child) > 0 {
		_, err := connectors.Command(c, child)
		return err
	}
	run, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	if c.URL != "" {
		p := ctx.Project
		checks.save = func(key string, result json.RawMessage) {
			if err := connectors.SaveAnswer(p, dept.Name, c, key, result, ctx.now()); err != nil {
				fmt.Fprintf(ctx.Stderr, "connector %s: the tool list was not saved: %v\n", c.Name, err)
			}
		}
		// With a saved copy the tool starts at the first request the copy
		// can't answer, and only then; with none it starts now, as below.
		if saved := connectors.LoadListing(p, dept.Name, c); saved != nil {
			h := &lazyHold{}
			defer h.release(ctx)
			checks.lazy = saved
			checks.wake = func(context.Context) error {
				return h.take(func() (*connectors.Lease, error) { return connectors.Hold(run, p, dept.Name, c, ctx.now) })
			}
			return runConnectorChild(run, ctx, c, child, checks)
		}
	}
	lease, err := connectors.Hold(run, ctx.Project, dept.Name, c, ctx.now)
	if err != nil {
		return err
	}
	defer func() {
		if err := lease.Release(); err != nil {
			fmt.Fprintln(ctx.Stderr, err)
		}
	}()
	return runConnectorChild(run, ctx, c, child, checks)
}

// lazyHold is a down session's lease, taken at its first wake and released
// when exec ends; a wake after the release takes nothing.
type lazyHold struct {
	mu     sync.Mutex
	lease  *connectors.Lease
	closed bool
}

func (h *lazyHold) take(hold func() (*connectors.Lease, error)) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return fmt.Errorf("the run is ending")
	}
	if h.lease != nil {
		return nil
	}
	l, err := hold()
	if err != nil {
		return err
	}
	h.lease = l
	return nil
}

func (h *lazyHold) release(ctx *Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	if h.lease != nil {
		if err := h.lease.Release(); err != nil {
			fmt.Fprintln(ctx.Stderr, err)
		}
	}
}

// runConnectorChild runs the bridge or the child until it ends or run does.
func runConnectorChild(run context.Context, ctx *Context, c *tracker.Connector, child []string, checks callChecks) error {
	if c.URL != "" {
		return runConnectorBridge(run, ctx, c, child, checks)
	}
	argv, err := connectors.Command(c, child)
	if err != nil {
		return err
	}
	env, err := connectors.Env(c, os.Environ())
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(run, argv[0], argv[1:]...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = env
	cmd.Dir = ctx.Project.Root()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = ctx.Stdin, ctx.Stdout, ctx.Stderr
	err = cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &exitCodeError{Code: exitErr.ExitCode()}
	}
	return err
}

// runConnectorBridge is exec for a remote MCP server (url:): the bridge
// runs in this process, so its headers' secrets reach only the requests.
func runConnectorBridge(run context.Context, ctx *Context, c *tracker.Connector, child []string, checks callChecks) error {
	if len(child) > 0 {
		_, err := connectors.Command(c, child)
		return err
	}
	if err := tracker.CheckConnectorURL(c.URL); err != nil {
		return fmt.Errorf("connector %s: %w", c.Name, err)
	}
	h, err := connectors.Headers(c)
	if err != nil {
		return err
	}
	b := &connectors.Bridge{URL: c.URL, Header: h, Stderr: ctx.Stderr, Hold: checks.hold, ReadTools: c.ReadToolNames(), Gate: checks.gate,
		Lazy: checks.lazy, Wake: checks.wake, Save: checks.save}
	// Bridge.Run waits on stdin, which a signal doesn't end.
	done := make(chan error, 1)
	go func() { done <- b.Run(run, ctx.Stdin, ctx.Stdout) }()
	select {
	case err := <-done:
		return err
	case <-run.Done():
		return nil
	}
}

func runConnectorSweep(ctx *Context, args []string) error {
	pos, err := parsePositionals(ctx, "connector sweep", args, 1)
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
	caller := connectorCaller(p, true)
	if caller.Agent && !(caller.Lead && dept != "" && strings.EqualFold(caller.Department, dept)) {
		return fmt.Errorf("connector sweep is for the owner or a department's lead on its own department")
	}
	errs := connectors.Sweep(p, dept, ctx.now)
	for _, e := range errs {
		fmt.Fprintln(ctx.Stderr, e)
	}
	if len(errs) > 0 {
		return &exitCodeError{Code: 1}
	}
	fmt.Fprintln(ctx.Stdout, "✓ no per-run tool left up without a run")
	return nil
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
		recs = append(recs, connectors.Check(context.Background(), p, t.dept.Name, t.c, ctx.now))
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
		case connectors.ResultNoCheck, connectors.ResultStopped:
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
		for _, o := range r.Optional {
			state := "not set"
			if o.Set {
				state = "set"
			}
			fmt.Fprintf(ctx.Stdout, "  %s optional: %s\n", o.Var, state)
		}
	}
	fmt.Fprintf(ctx.Stdout, "file: %s\n", connectors.LogPath(p))
	if failing > 0 {
		return &exitCodeError{Code: 1}
	}
	return nil
}

// runConnectorRest is exec for a kind: rest connector: one GET per path,
// made here with its headers: secrets, one JSON line each. Every path is
// checked, the hold asked and the secrets read before a per_run: tool
// starts, so a refused call starts nothing. Exit 1 when any answer isn't
// a 2xx, after every line is out.
func runConnectorRest(ctx *Context, dept *tracker.Department, addr string, c *tracker.Connector, paths []string, hold func() error) error {
	pats := connectors.RestPatterns(c)
	if len(paths) == 0 {
		return &usageError{msg: fmt.Sprintf("usage: issue-cli connector exec %s -- <path> [<path>...] (it reads GET %s; one JSON line per path)", addr, strings.Join(pats, ", "))}
	}
	if len(pats) == 0 {
		return fmt.Errorf("connector %s is kind: rest with no get: paths in projects.yaml: it reads nothing", addr)
	}
	var want []string
	for _, raw := range paths {
		p, err := connectors.RestPath(addr, c, raw)
		if err != nil {
			return err
		}
		want = append(want, p)
	}
	if hold != nil {
		if err := hold(); err != nil {
			return err
		}
	}
	if err := tracker.CheckConnectorURL(c.URL); err != nil {
		return fmt.Errorf("connector %s: %w", addr, err)
	}
	h, err := connectors.Headers(c)
	if err != nil {
		return fmt.Errorf("connector %s: %w", addr, err)
	}
	run, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	if c.PerRun != nil {
		lease, err := connectors.Hold(run, ctx.Project, dept.Name, c, ctx.now)
		if err != nil {
			return err
		}
		defer func() {
			if err := lease.Release(); err != nil {
				fmt.Fprintln(ctx.Stderr, err)
			}
		}()
	}
	hc := connectors.RestClient()
	failed := 0
	enc := json.NewEncoder(ctx.Stdout)
	for _, p := range want {
		if run.Err() != nil {
			break
		}
		a := connectors.RestRead(run, hc, c.URL, h, p)
		if !a.OK() {
			failed++
			if a.Status == 401 || a.Status == 403 {
				fmt.Fprintf(ctx.Stderr, "connector %s: %s refused the key in %s\n", addr, p, restKeyRefs(c))
			}
		}
		if err := enc.Encode(a); err != nil {
			return err
		}
	}
	if failed > 0 || run.Err() != nil {
		return &exitCodeError{Code: 1}
	}
	return nil
}

// restKeyRefs names c's header secrets by reference: "header Authorization:
// keychain:postiz", never a value.
func restKeyRefs(c *tracker.Connector) string {
	var out []string
	for _, s := range c.SecretStates() {
		if s.Header {
			out = append(out, "header "+s.Var+": "+s.Ref)
		}
	}
	if len(out) == 0 {
		return "no header (the connector sends none)"
	}
	return strings.Join(out, ", ")
}
