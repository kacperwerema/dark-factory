package routing

import (
	"fmt"
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

// The lines as the CLIs print them: a real pane footer, Claude's other form
// and Codex's warning (see usage.go for each one's source).
func TestFindUsageReadsTheWeeklyLineOfEachCLI(t *testing.T) {
	footer := "  ⏵⏵ auto mode on (shift+tab to cycle) · esc to interrupt\n  You've used 92% of your weekly limit · resets Nov 5 at 8am (Europe/Warsaw) …\n"
	u, ok := FindUsage("", footer, now)
	want := time.Date(2026, 11, 5, 8, 0, 0, 0, warsaw)
	if !ok || u.Agent != "claude" || u.Percent != 92 || !u.Until.Equal(want) || !u.KnownReset {
		t.Fatalf("footer: %+v ok=%v, want claude 92%% until %v", u, ok, want)
	}
	u, ok = FindUsage("", "Over 98% of your weekly limit used · resets 5pm (Europe/Warsaw)", now)
	if !ok || u.Percent != 98 || u.Agent != "claude" {
		t.Fatalf("over form: %+v ok=%v", u, ok)
	}
	u, ok = FindUsage("", "⚠ Heads up, you have less than 5% of your weekly limit left. Run /status for a breakdown.", now)
	if !ok || u.Agent != "codex" || u.Percent != 95 || u.KnownReset || !u.Until.Equal(now.Add(UnknownRest)) {
		t.Fatalf("codex: %+v ok=%v", u, ok)
	}
	if u, ok := FindUsage("codex", footer, now); ok {
		t.Errorf("a claude line read as codex: %+v", u)
	}
}

func TestFindUsageLeavesOutWhatIsNotTheWeekNow(t *testing.T) {
	cases := map[string]string{
		"session limit": "You've used 99% of your session limit · resets 5pm (Europe/Warsaw)",
		"model limit":   "You've used 99% of your Opus limit · resets 5pm (Europe/Warsaw)",
		"codex 5h":      "Heads up, you have less than 5% of your 5h limit left. Run /status for a breakdown.",
		"a diff":        "+ You've used 98% of your weekly limit · resets 5pm (Europe/Warsaw)",
		"a quote":       `  {"claude", "You've used 98% of your weekly limit · resets 5pm"},`,
		"prose":         "the line says You've used 98% of your weekly limit · resets 5pm",
		"past reset":    "You've used 99% of your weekly limit · resets Oct 29 at 8am (Europe/Warsaw)",
		"scrolled away": "You've used 98% of your weekly limit · resets 5pm (Europe/Warsaw)\n" + strings.Repeat("output\n", scanTail),
	}
	for name, pane := range cases {
		t.Run(name, func(t *testing.T) {
			if u, ok := FindUsage("", pane, now); ok {
				t.Errorf("read %+v from %q", u, pane)
			}
		})
	}
}

// A real weekly limit names its date with "at", and a stale date stays in
// the past instead of becoming next year's.
func TestClaudeResetDates(t *testing.T) {
	l, ok := FindLimit("", "You've hit your weekly limit · resets Nov 5 at 8am (Europe/Warsaw)", now)
	if !ok || !l.KnownReset || !l.Until.Equal(time.Date(2026, 11, 5, 8, 0, 0, 0, warsaw)) {
		t.Errorf("at form: %+v", l)
	}
	l, _ = FindLimit("", "You've hit your weekly limit · resets Oct 29 at 8am (Europe/Warsaw)", now)
	if l.Until.After(now) {
		t.Errorf("a stale date became %v", l.Until)
	}
	dec := time.Date(2026, 12, 30, 12, 0, 0, 0, warsaw)
	l, _ = FindLimit("", "You've hit your weekly limit · resets Jan 2 at 8am (Europe/Warsaw)", dec)
	if !l.Until.Equal(time.Date(2027, 1, 2, 8, 0, 0, 0, warsaw)) {
		t.Errorf("over new year: %v", l.Until)
	}
}

func usageAt(pct int, until time.Time) Usage {
	return Usage{Agent: "claude", Percent: pct, Until: until, KnownReset: true, Line: fmt.Sprintf("You've used %d%%", pct)}
}

func TestSeeUsageRestsAtTheStopPointAndKeepsTheWeeksHighest(t *testing.T) {
	dir := t.TempDir()
	reset := now.Add(72 * time.Hour)
	if rested, err := SeeUsage(dir, usageAt(97, reset), 98, "p", "agent-a", now); rested || err != nil {
		t.Fatalf("97 of 98: rested=%v err=%v", rested, err)
	}
	st, _ := Load(dir)
	if _, ok := st.Resting("claude", now); ok {
		t.Fatal("under the stop point and resting")
	}
	if u, ok := st.UsageOf("claude", now); !ok || u.Percent != 97 || u.Session != "agent-a" {
		t.Fatalf("reading = %+v", u)
	}
	if rested, _ := SeeUsage(dir, usageAt(98, reset), 98, "p", "agent-b", now); !rested {
		t.Fatal("98 of 98 did not rest")
	}
	SeeUsage(dir, usageAt(92, reset), 98, "p", "agent-old", now)
	st, _ = Load(dir)
	r, ok := st.Resting("claude", now)
	if !ok || !r.Until.Equal(reset) || r.By != "stop" || r.Percent != 98 || r.StopAt != 98 || r.Session != "agent-b" {
		t.Fatalf("rest = %+v ok=%v", r, ok)
	}
	if u, _ := st.UsageOf("claude", now); u.Percent != 98 {
		t.Errorf("a lower pane undid the week's percent: %+v", u)
	}
	if _, ok := st.UsageOf("claude", reset); ok {
		t.Error("the reading outlived its reset")
	}
	if _, ok := st.Resting("claude", reset); ok {
		t.Error("the rest outlived its reset")
	}
}

func TestStopPointHundredNeverRests(t *testing.T) {
	dir := t.TempDir()
	if rested, _ := SeeUsage(dir, usageAt(99, now.Add(time.Hour)), 100, "p", "s", now); rested {
		t.Error("stop_at 100 rested at 99, the most Claude prints")
	}
}

func TestClearHoldsUntilAHigherPercentShows(t *testing.T) {
	dir := t.TempDir()
	reset := now.Add(72 * time.Hour)
	SeeUsage(dir, usageAt(98, reset), 98, "p", "agent-a", now)
	if found, err := Clear(dir, "claude", now); !found || err != nil {
		t.Fatalf("clear: %v %v", found, err)
	}
	for _, session := range []string{"agent-a", "agent-b"} {
		if rested, _ := SeeUsage(dir, usageAt(98, reset), 98, "p", session, now.Add(time.Minute)); rested {
			t.Fatalf("the cleared percent rested again from %s", session)
		}
	}
	if rested, _ := SeeUsage(dir, usageAt(99, reset), 98, "p", "agent-a", now.Add(time.Minute)); !rested {
		t.Fatal("a higher percent did not rest")
	}
}

// A line with no reset ends after UnknownRest even while it stays on the pane.
func TestAReadingWithNoResetEndsWhileItsLineStaysOnThePane(t *testing.T) {
	dir := t.TempDir()
	u, _ := FindUsage("", "Heads up, you have less than 5% of your weekly limit left.", now)
	if rested, _ := SeeUsage(dir, u, 90, "p", "agent-a", now); !rested {
		t.Fatal("codex at 95 of 90 did not rest")
	}
	later := now.Add(UnknownRest + time.Minute)
	u, _ = FindUsage("", "Heads up, you have less than 5% of your weekly limit left.", later)
	if rested, _ := SeeUsage(dir, u, 90, "p", "agent-a", later); rested {
		t.Fatal("the same line started another rest")
	}
	st, _ := Load(dir)
	if _, ok := st.UsageOf("codex", later); ok {
		t.Error("the reading did not end")
	}
}

func TestAgentAtItsStopPointFallsBackAndBothHoldWithTheReason(t *testing.T) {
	old := Installed
	Installed = func(string) bool { return true }
	t.Cleanup(func() { Installed = old })
	dir := t.TempDir()
	cfg := &tracker.RoutingConfig{Default: "claude", Fallback: []string{"claude", "codex"}, Rules: []tracker.RoutingRule{{Type: "feature", Agent: "claude"}}}
	subj := Subject{Type: "feature"}
	reset := time.Date(2026, 11, 5, 8, 0, 0, 0, warsaw)
	SeeUsage(dir, usageAt(98, reset), cfg.StopPoint("claude"), "p", "agent-a", now)
	st, _ := Load(dir)
	c := Pick(cfg, subj, "", st, now)
	if c.Agent != "codex" || !c.Fallback || c.Hold != "" || !strings.Contains(c.Why, "at its stop point 98%") {
		t.Fatalf("one at its stop point: %+v", c)
	}
	if c := Pick(cfg, subj, "claude", st, now); c.Agent != "claude" || c.Warning == "" {
		t.Errorf("--agent claude: %+v", c)
	}
	SeeUsage(dir, Usage{Agent: "codex", Percent: 97, Until: reset, KnownReset: true, Line: "x"}, 96, "p", "agent-b", now)
	st, _ = Load(dir)
	c = Pick(cfg, subj, "", st, now)
	want := "every agent to try is resting (claude until Thu 08:00 at its stop point 98%, codex until Thu 08:00 at its stop point 96%)"
	if c.Hold != want {
		t.Fatalf("hold = %q, want %q", c.Hold, want)
	}
}

func TestStateWordsCarryThePercent(t *testing.T) {
	dir := t.TempDir()
	reset := now.Add(72 * time.Hour)
	if got := StateWords("claude", &State{}, true, now); got != "working" {
		t.Errorf("no reading: %q", got)
	}
	SeeUsage(dir, usageAt(92, reset), 98, "p", "agent-a", now)
	st, _ := Load(dir)
	if got, want := StateWords("claude", st, true, now), "working · 92% of the weekly limit"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	SeeUsage(dir, usageAt(98, reset), 98, "p", "agent-b", now)
	st, _ = Load(dir)
	if got := StateWords("claude", st, true, now); !strings.Contains(got, "used 98%, stop point 98%, seen in agent-b") {
		t.Errorf("at the stop point: %q", got)
	}
}
