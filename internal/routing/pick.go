package routing

import (
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// KnownAgents are the agent CLIs whose limit messages FindLimit reads and
// whose subscriptions the pages show.
var KnownAgents = []string{"claude", "codex"}

// Subject is what a start is for: the issue's type, labels, current status
// and department (box key, "" for Product), or a lead.
type Subject struct {
	Type       string
	Labels     []string
	Status     string
	Department string
	Lead       bool
}

// IssueSubject is issue's subject in proj.
func IssueSubject(proj *tracker.Project, issue *tracker.Issue) Subject {
	s := Subject{Type: issue.Type, Labels: issue.Labels, Status: issue.Status}
	if proj != nil {
		if d := proj.DepartmentOf(issue); d != "" {
			s.Department = tracker.DepartmentKey(d)
		}
	}
	return s
}

// Choice is the agent a start runs and why.
type Choice struct {
	Agent string
	// Why is the rule in words: "type docs → codex", "default → claude",
	// "--agent codex".
	Why string
	// Fallback is set when the routed agent was resting and Agent is the
	// next in fallback:; Skipped are the resting ones passed over.
	Fallback bool
	Skipped  []Rest
	// Hold is set when every agent to try is resting or not installed:
	// nothing should start (auto-start holds; a manual start warns).
	Hold string
	// Warning is set when an explicit agent is resting: the start goes on.
	Warning string
}

// Installed reports whether agent's command is on PATH. A var so tests
// need no real CLI.
var Installed = func(agent string) bool {
	_, err := exec.LookPath(agent)
	return err == nil
}

// Match reports whether rule r matches s.
func Match(r tracker.RoutingRule, s Subject) bool {
	if r.Type != "" && !strings.EqualFold(r.Type, s.Type) {
		return false
	}
	if r.Status != "" && !strings.EqualFold(r.Status, s.Status) {
		return false
	}
	if r.Department != "" && tracker.DepartmentKey(r.Department) != s.Department {
		return false
	}
	if r.Lead != nil && *r.Lead != s.Lead {
		return false
	}
	if r.Label != "" && !slices.ContainsFunc(s.Labels, func(l string) bool { return strings.EqualFold(l, r.Label) }) {
		return false
	}
	return true
}

// Route is the agent cfg's rules give s, before rests: the first matching
// rule's agent, else the default.
func Route(cfg *tracker.RoutingConfig, s Subject) (agent, why string) {
	if cfg != nil {
		for _, r := range cfg.Rules {
			if strings.TrimSpace(r.Agent) != "" && Match(r, s) {
				return strings.TrimSpace(r.Agent), r.Describe() + " → " + strings.TrimSpace(r.Agent)
			}
		}
	}
	a := cfg.DefaultAgentName()
	if cfg == nil {
		return a, "no routing: → " + a
	}
	return a, "default → " + a
}

// Pick is the agent the next start for s runs. explicit (an --agent flag, an
// action's agent:) is the owner's choice and wins; when its subscription is
// resting the choice carries a Warning and still starts. Otherwise the routed
// agent, and when it is resting the first agent of fallback: that is neither
// resting nor missing from PATH; Hold when there is none.
func Pick(cfg *tracker.RoutingConfig, s Subject, explicit string, st *State, now time.Time) Choice {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		c := Choice{Agent: explicit, Why: "--agent " + explicit}
		if r, ok := st.Resting(explicit, now); ok {
			c.Warning = fmt.Sprintf("%s is resting until %s (%s); starting it anyway, as asked", explicit, RestUntilText(r, now), restSource(r))
		}
		return c
	}
	agent, why := Route(cfg, s)
	c := Choice{Agent: agent, Why: why}
	r, ok := st.Resting(agent, now)
	if !ok {
		return c
	}
	c.Skipped = append(c.Skipped, r)
	var order []string
	if cfg != nil {
		order = cfg.Fallback
	}
	for _, a := range order {
		a = strings.TrimSpace(a)
		if a == "" || a == agent {
			continue
		}
		if r2, resting := st.Resting(a, now); resting {
			c.Skipped = append(c.Skipped, r2)
			continue
		}
		if !Installed(a) {
			continue
		}
		c.Agent, c.Fallback = a, true
		c.Why = fmt.Sprintf("%s resting until %s; fallback → %s", agent, RestUntilText(r, now), a)
		return c
	}
	var parts []string
	for _, sk := range c.Skipped {
		parts = append(parts, sk.Agent+" until "+RestUntilText(sk, now))
	}
	c.Hold = "every agent to try is resting (" + strings.Join(parts, ", ") + ")"
	if len(order) == 0 {
		c.Hold = fmt.Sprintf("%s is resting until %s and routing: has no fallback", agent, RestUntilText(r, now))
	}
	return c
}

// RestUntilText is the rest's end as the pages say it: "17:00", "Mon 09:00"
// when not today, with "?" when the reset time was a guess.
func RestUntilText(r Rest, now time.Time) string {
	u := r.Until.In(now.Location())
	t := u.Format("15:04")
	if y1, d1 := u.YearDay(), u.Year(); y1 != now.YearDay() || d1 != now.Year() {
		t = u.Format("Mon 15:04")
	}
	if !r.KnownReset {
		t += " (reset time unknown)"
	}
	return t
}

func restSource(r Rest) string {
	if r.By == "owner" {
		return "set by the owner"
	}
	if r.Session != "" {
		return "limit seen in " + r.Session
	}
	return "limit seen"
}

// StateWords is a subscription's state for pages: "resting until 17:00
// (limit seen in agent-x)", "working" when a live session runs it, or
// "unknown".
func StateWords(agent string, st *State, live bool, now time.Time) string {
	if r, ok := st.Resting(agent, now); ok {
		return "resting until " + RestUntilText(r, now) + " (" + restSource(r) + ")"
	}
	if live {
		return "working"
	}
	return "unknown"
}
