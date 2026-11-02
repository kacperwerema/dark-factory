package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/routing"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var agentsCommand = &Command{
	Name:      "agents",
	ShortHelp: "Agent CLIs and their subscriptions: which is working or resting after a usage limit, the routing: rules (sub: status|rest|clear)",
	LongHelp: `Which of the owner's agent CLIs (claude, codex) starts the next worker,
lead or job run, and whether each subscription is resting after its CLI
showed a usage-limit message. Dispatch, lead spawn, job run and auto-start
pick the agent from the workflow's routing: rules (docs/agent-dispatch.md,
"Routing between Claude and Codex"); --agent on those commands overrides it.

A rest is recorded when a live agent's pane shows its CLI's own limit
message (Claude Code: "You've hit your 5-hour limit · resets 5pm (…)";
Codex: "You’ve hit your usage limit … try again at 6:30 PM."), until the
reset time it names (one hour when it names none). It ends by itself.
Routing only picks which installed CLI starts next: nothing is retried,
moved or killed, and no account, key or login is touched.

Subcommands:
  status [--json]
      Each agent: installed or not, working / resting until HH:MM /
      unknown, the agents showing a limit now, and the routing: rules.
  rest <agent> --until HH:MM
      Mark a subscription resting by hand (you saw the limit yourself).
  clear <agent>
      End a rest early (a wrong match, or the reset came sooner).
rest and clear are the owner's or a lead's; a worker can't run them.`,
	Run:         runAgents,
	Subcommands: []string{"status", "rest", "clear"},
}

func init() {
	registerCommand(agentsCommand)
}

func runAgents(ctx *Context, args []string) error {
	sub := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "status":
		return runAgentsStatus(ctx, args)
	case "rest", "clear":
		return runAgentsRest(ctx, sub, args)
	}
	return &usageError{msg: fmt.Sprintf("unknown agents subcommand %q (status, rest, clear)", sub)}
}

// AgentsReport is issue-cli agents status --json.
type AgentsReport struct {
	Agents  []AgentLine      `json:"agents"`
	Limited []AgentLimitLine `json:"limited,omitempty"`
	Rules   []string         `json:"rules,omitempty"`
	Default string           `json:"default"`
	Fallbk  []string         `json:"fallback,omitempty"`
	Rests   []routing.Rest   `json:"rests,omitempty"`
	cfg     *tracker.RoutingConfig
}

// AgentLine is one agent CLI's state.
type AgentLine struct {
	Agent     string `json:"agent"`
	Installed bool   `json:"installed"`
	Live      int    `json:"live"`
	State     string `json:"state"`
}

// AgentLimitLine is a live agent whose pane shows a limit now.
type AgentLimitLine struct {
	Session string `json:"session"`
	Agent   string `json:"agent"`
	Line    string `json:"line"`
	Until   string `json:"until"`
}

func runAgentsStatus(ctx *Context, args []string) error {
	fs := newFlagSet("agents status", ctx)
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	rep := agentsReport(ctx)
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, rep)
	}
	fmt.Fprintln(ctx.Stdout, "== Agents ==")
	for _, a := range rep.Agents {
		inst := ""
		if !a.Installed {
			inst = " (not installed)"
		}
		live := ""
		if a.Live > 0 {
			live = fmt.Sprintf(" · %d live", a.Live)
		}
		fmt.Fprintf(ctx.Stdout, "  %-7s %s%s%s\n", a.Agent, a.State, live, inst)
	}
	if len(rep.Limited) > 0 {
		fmt.Fprintln(ctx.Stdout, "\n== Showing a limit now (left running; never killed) ==")
		for _, l := range rep.Limited {
			fmt.Fprintf(ctx.Stdout, "  %s (%s): %s — resets %s\n", l.Session, l.Agent, l.Line, l.Until)
		}
	}
	fmt.Fprintln(ctx.Stdout, "\n== Routing ==")
	if rep.cfg == nil {
		fmt.Fprintf(ctx.Stdout, "  no routing: in the workflow file: every start runs %s unless --agent says otherwise\n", tracker.DefaultAgent)
	} else {
		for _, r := range rep.Rules {
			fmt.Fprintf(ctx.Stdout, "  %s\n", r)
		}
		fmt.Fprintf(ctx.Stdout, "  default → %s", rep.Default)
		if len(rep.Fallbk) > 0 {
			fmt.Fprintf(ctx.Stdout, " · fallback %s", strings.Join(rep.Fallbk, " → "))
		}
		fmt.Fprintln(ctx.Stdout)
	}
	fmt.Fprintf(ctx.Stdout, "\nA wrong rest: %s\n", tracker.InjectProject(ctx.Project, "issue-cli agents clear <agent>"))
	return nil
}

func agentsReport(ctx *Context) AgentsReport {
	now := ctx.now()
	var rep AgentsReport
	live := map[string]int{}
	if p := ctx.Project; p != nil {
		root := projectRoot(p)
		for _, l := range agentrun.ScanLimits(p, root, now) {
			rep.Limited = append(rep.Limited, AgentLimitLine{Session: l.Session, Agent: l.Limit.Agent, Line: l.Limit.Line,
				Until: routing.RestUntilText(routing.Rest{Until: l.Limit.Until, KnownReset: l.Limit.KnownReset}, now)})
		}
		for _, s := range agentrun.ForProject(p, root, agentrun.ListSessions()) {
			name := agentrun.BareName(s.Name)
			if strings.HasPrefix(name, "agent-") || strings.HasPrefix(name, "lead-") {
				if a := agentrun.SessionAgent(p, name); a != "" {
					live[a]++
				}
			}
		}
		if wf := p.LoadWorkflow(); wf != nil {
			rep.cfg = wf.Routing
		}
	}
	st := agentrun.Subscriptions()
	rep.Rests = st.Active(now)
	for _, a := range routing.KnownAgents {
		rep.Agents = append(rep.Agents, AgentLine{Agent: a, Installed: routing.Installed(a), Live: live[a],
			State: routing.StateWords(a, st, live[a] > 0, now)})
	}
	rep.Default = rep.cfg.DefaultAgentName()
	if rep.cfg != nil {
		rep.Fallbk = rep.cfg.Fallback
		for _, r := range rep.cfg.Rules {
			rep.Rules = append(rep.Rules, r.Describe()+" → "+r.Agent)
		}
	}
	return rep
}

func runAgentsRest(ctx *Context, sub string, args []string) error {
	fs := newFlagSet("agents "+sub, ctx)
	until := fs.String("until", "", "when the subscription resets (HH:MM, local time; past means tomorrow)")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return &usageError{msg: fmt.Sprintf("usage: issue-cli agents %s <agent>%s", sub, map[string]string{"rest": " --until HH:MM", "clear": ""}[sub])}
	}
	agent := strings.TrimSpace(fs.Arg(0))
	if !agentrun.ValidAgentType(agent) {
		return &usageError{msg: fmt.Sprintf("%q is not an agent command name", agent)}
	}
	if ctx.Project != nil {
		issues, _ := tracker.LoadIssues(ctx.Project.IssueDir)
		if me := whoAmI(ctx.Project, issues); me.Role == tracker.RoleWorker {
			return fmt.Errorf("workers can't change a subscription's rest (you are %s); post a question: comment and the CEO decides", valueOr(me.Name, "a worker"))
		}
	}
	dir := agentrun.SubscriptionsDir()
	now := ctx.now()
	if sub == "clear" {
		found, err := routing.Clear(dir, agent, now)
		if err != nil {
			return err
		}
		if !found {
			fmt.Fprintf(ctx.Stdout, "%s was not resting.\n", agent)
			return nil
		}
		fmt.Fprintf(ctx.Stdout, "✓ %s is no longer resting: new starts may use it again.\n", agent)
		return nil
	}
	t, err := time.ParseInLocation("15:04", strings.TrimSpace(*until), now.Location())
	if err != nil {
		return &usageError{msg: "agents rest needs --until HH:MM (local time)"}
	}
	end := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
	if !end.After(now) {
		end = end.AddDate(0, 0, 1)
	}
	if err := routing.SetRest(dir, agent, end, now); err != nil {
		return err
	}
	fmt.Fprintf(ctx.Stdout, "✓ %s rests until %s: new starts go to the fallback (routing:), auto-start holds if none is free.\n", agent, routing.RestUntilText(routing.Rest{Until: end, KnownReset: true}, now))
	return nil
}
