package routing

import (
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var warsaw = mustLoc("Europe/Warsaw")

func mustLoc(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

// now is 2026-10-31 14:02 in Warsaw for every limit test.
var now = time.Date(2026, 10, 31, 14, 2, 0, 0, warsaw)

func TestFindLimitReadsEachCLIsOwnMessageAndItsReset(t *testing.T) {
	cases := []struct {
		name, pane, agent string
		want              time.Time
		known             bool
	}{
		// Claude Code 2.1.316's banner: "You've hit your " + name + " · resets " + time.
		{"claude 5-hour", "❯ go on\n  ⎿  You've hit your 5-hour limit · resets 5pm (Europe/Warsaw)\n", "claude",
			time.Date(2026, 10, 31, 17, 0, 0, 0, warsaw), true},
		{"claude minutes", "You've hit your session limit · resets 3:45pm (Europe/Warsaw)", "claude",
			time.Date(2026, 10, 31, 15, 45, 0, 0, warsaw), true},
		{"claude weekly, a date", "You've hit your weekly limit · resets Nov 3, 9am (Europe/Warsaw)", "claude",
			time.Date(2026, 11, 3, 9, 0, 0, 0, warsaw), true},
		{"claude, other zone", "You've hit your Opus limit · resets 1pm (America/New_York)", "claude",
			time.Date(2026, 10, 31, 13, 0, 0, 0, mustLoc("America/New_York")), true},
		{"claude past today means tomorrow", "You've hit your 5-hour limit · resets 9am (Europe/Warsaw)", "claude",
			time.Date(2026, 11, 1, 9, 0, 0, 0, warsaw), true},
		{"claude old form", "Claude usage limit reached. Your limit will reset at 5pm (Europe/Warsaw).", "claude",
			time.Date(2026, 10, 31, 17, 0, 0, 0, warsaw), true},
		{"claude epoch", "Claude AI usage limit reached|1793451600", "claude", time.Unix(1793451600, 0), true},
		{"claude no time", "Usage limit reached · continuing automatically", "claude", now.Add(UnknownRest), false},
		// Codex: codex-rs/protocol/src/error.rs, UsageLimitReachedError.
		{"codex today", "■ You’ve hit your usage limit. Upgrade to Plus to continue using Codex (https://chatgpt.com/explore/plus), or try again at 6:30 PM.", "codex",
			time.Date(2026, 10, 31, 18, 30, 0, 0, warsaw), true},
		{"codex date", "■ You've hit your usage limit. Try again at Nov 2nd, 2026 3:05 PM.", "codex",
			time.Date(2026, 11, 2, 15, 5, 0, 0, warsaw), true},
		{"codex later", "■ You’ve hit your usage limit. Try again later.", "codex", now.Add(UnknownRest), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l, ok := FindLimit("", c.pane, now)
			if !ok {
				t.Fatalf("no limit found in %q", c.pane)
			}
			if l.Agent != c.agent || !l.Until.Equal(c.want) || l.KnownReset != c.known {
				t.Errorf("got %s until %v known=%v, want %s until %v known=%v", l.Agent, l.Until, l.KnownReset, c.agent, c.want, c.known)
			}
		})
	}
}

func TestFindLimitIgnoresAMessageQuotedFarUpThePaneOrPaidExtras(t *testing.T) {
	quoted := "the issue says: You've hit your 5-hour limit · resets 5pm\n" + strings.Repeat("work line\n", 30) + "❯ "
	if l, ok := FindLimit("", quoted, now); ok {
		t.Errorf("a message 30 lines up counted: %+v", l)
	}
	for _, pane := range []string{"You've hit your monthly spend limit · raise it at x", "You're out of usage credits · resets 5pm", "all good\n❯ "} {
		if l, ok := FindLimit("", pane, now); ok {
			t.Errorf("%q counted as a plan limit: %+v", pane, l)
		}
	}
	if _, ok := FindLimit("codex", "You've hit your 5-hour limit · resets 5pm", now); ok {
		t.Error("asked for codex, got claude's message")
	}
}

func routingCfg() *tracker.RoutingConfig {
	yes := true
	return &tracker.RoutingConfig{
		Default:  "claude",
		Fallback: []string{"claude", "codex"},
		Rules: []tracker.RoutingRule{
			{Type: "docs", Agent: "codex"},
			{Label: "design", Agent: "claude"},
			{Department: "Marketing", Agent: "codex"},
			{Lead: &yes, Agent: "claude"},
		},
	}
}

func withInstalled(t *testing.T, agents ...string) {
	t.Helper()
	old := Installed
	Installed = func(a string) bool {
		for _, x := range agents {
			if x == a {
				return true
			}
		}
		return false
	}
	t.Cleanup(func() { Installed = old })
}

func TestRouteFollowsTheFirstMatchingRuleElseTheDefault(t *testing.T) {
	cfg := routingCfg()
	cases := []struct {
		s    Subject
		want string
		why  string
	}{
		{Subject{Type: "docs"}, "codex", "type docs → codex"},
		{Subject{Type: "feature", Labels: []string{"Design"}}, "claude", "label design → claude"},
		{Subject{Type: "feature", Department: "marketing"}, "codex", "department Marketing → codex"},
		{Subject{Lead: true}, "claude", "leads → claude"},
		{Subject{Type: "feature"}, "claude", "default → claude"},
	}
	for _, c := range cases {
		if got, why := Route(cfg, c.s); got != c.want || why != c.why {
			t.Errorf("Route(%+v) = %s (%s), want %s (%s)", c.s, got, why, c.want, c.why)
		}
	}
	if got, why := Route(nil, Subject{Type: "docs"}); got != "claude" || !strings.Contains(why, "no routing:") {
		t.Errorf("no routing: block = %s (%s), want claude as today", got, why)
	}
}

func TestPickFallsBackWhenTheRoutedSubscriptionRestsAndHoldsWhenAllDo(t *testing.T) {
	withInstalled(t, "claude", "codex")
	cfg := routingCfg()
	st := &State{Rests: map[string]Rest{}}
	if c := Pick(cfg, Subject{Type: "docs"}, "", st, now); c.Agent != "codex" || c.Fallback || c.Hold != "" {
		t.Fatalf("nothing resting: %+v", c)
	}
	st.Rests["codex"] = Rest{Agent: "codex", Until: now.Add(2 * time.Hour), KnownReset: true, Session: "agent-x", By: "scan"}
	c := Pick(cfg, Subject{Type: "docs"}, "", st, now)
	if c.Agent != "claude" || !c.Fallback || !strings.Contains(c.Why, "codex resting until 16:02; fallback → claude") {
		t.Fatalf("codex resting: %+v", c)
	}
	st.Rests["claude"] = Rest{Agent: "claude", Until: now.Add(time.Hour), KnownReset: true, By: "scan"}
	c = Pick(cfg, Subject{Type: "docs"}, "", st, now)
	if c.Hold == "" || !strings.Contains(c.Hold, "codex until 16:02") || !strings.Contains(c.Hold, "claude until 15:02") {
		t.Fatalf("both resting should hold: %+v", c)
	}
	// A rest ends by itself at its reset.
	if c := Pick(cfg, Subject{Type: "docs"}, "", st, now.Add(3*time.Hour)); c.Agent != "codex" || c.Hold != "" {
		t.Fatalf("after both resets: %+v", c)
	}
}

func TestPickSkipsAFallbackThatIsNotInstalled(t *testing.T) {
	withInstalled(t, "claude")
	st := &State{Rests: map[string]Rest{"claude": {Agent: "claude", Until: now.Add(time.Hour), KnownReset: true}}}
	c := Pick(routingCfg(), Subject{Type: "feature"}, "", st, now)
	if c.Hold == "" {
		t.Fatalf("codex isn't installed, claude rests: want a hold, got %+v", c)
	}
}

func TestAnExplicitAgentWinsAndWarnsWhenResting(t *testing.T) {
	st := &State{Rests: map[string]Rest{"codex": {Agent: "codex", Until: now.Add(time.Hour), KnownReset: true, Session: "agent-y", By: "scan"}}}
	c := Pick(routingCfg(), Subject{Type: "feature"}, "codex", st, now)
	if c.Agent != "codex" || c.Hold != "" || !strings.Contains(c.Warning, "codex is resting until 15:02 (limit seen in agent-y)") {
		t.Fatalf("explicit codex: %+v", c)
	}
}

func TestMarkRecordsARestOnceAndTheOwnerCanClearIt(t *testing.T) {
	dir := t.TempDir()
	l, _ := FindLimit("", "You've hit your 5-hour limit · resets 5pm (Europe/Warsaw)", now)
	changed, err := Mark(dir, l, "proj", "agent-a", now)
	if err != nil || !changed {
		t.Fatalf("first mark: changed=%v err=%v", changed, err)
	}
	// The same message still on the pane after the reset starts no new rest.
	later := time.Date(2026, 10, 31, 17, 5, 0, 0, warsaw)
	l2, _ := FindLimit("", "You've hit your 5-hour limit · resets 5pm (Europe/Warsaw)", later)
	if changed, _ := Mark(dir, l2, "proj", "agent-a", later); changed {
		t.Error("the same pane message started a second rest")
	}
	st, _ := Load(dir)
	if _, ok := st.Resting("claude", later); ok {
		t.Error("the rest should have ended at 17:00")
	}
	if r, ok := st.Resting("claude", now); !ok || r.Session != "agent-a" || r.Project != "proj" || r.By != "scan" {
		t.Errorf("rest at 14:02: %+v ok=%v", r, ok)
	}
	if got := StateWords("claude", st, true, now); got != "resting until 17:00 (limit seen in agent-a)" {
		t.Errorf("StateWords = %q", got)
	}
	if found, err := Clear(dir, "claude", now); err != nil || !found {
		t.Fatalf("clear: found=%v err=%v", found, err)
	}
	st, _ = Load(dir)
	if got := StateWords("claude", st, false, now); got != "unknown" {
		t.Errorf("after clear: %q", got)
	}
	if err := SetRest(dir, "codex", now.Add(30*time.Minute), now); err != nil {
		t.Fatal(err)
	}
	st, _ = Load(dir)
	if got := StateWords("codex", st, false, now); got != "resting until 14:32 (set by the owner)" {
		t.Errorf("owner's rest: %q", got)
	}
}
