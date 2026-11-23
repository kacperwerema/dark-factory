package briefing

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"gopkg.in/yaml.v3"
)

// dfLeadBrief renders this repo's lead brief as its lead gets it: the repo's
// workflow.yaml (CI, release candidates, privacy) with auto-start on. It
// reads nothing of the machine it runs on (isolateHost): its only
// memories are leadPins, lead-scope pins in the project's own store.
func dfLeadBrief(t *testing.T, leadPins ...string) string {
	t.Helper()
	isolateHost(t)
	raw, err := os.ReadFile("../../workflow.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var wf tracker.WorkflowConfig
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "issues")
	store := memory.ProjectStore(dir)
	now := time.Now()
	for _, text := range leadPins {
		r, err := store.Add(memory.Draft{Text: text, Scope: memory.ScopeLead}, memory.AddOptions{Mode: memory.ModeNew}, now)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Pin(r.Memory.ID, "lead-dark-factory", true, now); err != nil {
			t.Fatal(err)
		}
	}
	return LeadBrief(&wf, LeadBriefInput{
		Project:   &tracker.Project{Slug: "dark-factory", Name: "Dark Factory", IssueDir: dir},
		Root:      "/r",
		Now:       time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC),
		AutoStart: &AutoStartBrief{Words: "start under 60%", Below: 60, MaxWorkers: 4},
	})
}

// isolateHost points HOME and the XDG dirs at empty temp dirs, so a test
// reads no memories, rules, state or config of the machine it runs on.
func isolateHost(t *testing.T) {
	t.Helper()
	for _, k := range []string{"HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(k, t.TempDir())
	}
}

// The Pins line comes with the lead's memories: none on a fresh machine
// leaves it out, one lead pin puts it in, on any host.
func TestLeadBriefPinsLineFollowsTheLeadMemories(t *testing.T) {
	const line = "Pins are titles here, with their Never rules in full"
	if got := dfLeadBrief(t); strings.Contains(got, line) || strings.Contains(got, "Standing rules (lead scope):") {
		t.Errorf("a lead with no memories gets the pins line:\n%s", got)
	}
	got := dfLeadBrief(t, "never dispatch without the owner's words")
	for _, want := range []string{line, "Standing rules (lead scope):", "never dispatch without the owner's words"} {
		if !strings.Contains(got, want) {
			t.Errorf("a lead with a lead pin misses %q", want)
		}
	}
}

// The hold conditions are listed once in the lead brief: the auto-start
// and queue lines point at the one Holds line instead of repeating it.
func TestLeadBriefListsTheHoldsOnce(t *testing.T) {
	got := dfLeadBrief(t)
	for _, hold := range []string{
		"memory is at or above",
		"the 1- and 5-minute load are both at or above",
		"workers are live",
		"another project just started one",
		"it is another project's turn",
		"local CI is red on main",
	} {
		if n := strings.Count(got, hold); n != 1 {
			t.Errorf("hold %q appears %d times in the lead brief, want 1", hold, n)
		}
	}
	if n := strings.Count(got, "- Holds:"); n != 1 {
		t.Errorf("%d Holds lines, want 1", n)
	}
	// The dispatch rule is the Lead rules' line alone.
	if n := strings.Count(got, "you dispatch exactly those") + strings.Count(got, "Dispatch only when the human tells you"); n != 1 {
		t.Errorf("the dispatch rule is said %d times, want 1", n)
	}
}

// The release and private-development lines are one line each, pointing at
// their docs page, and every Never rule of the brief before the cut is
// still in the brief.
func TestLeadBriefRulesMovedToDocsAreThere(t *testing.T) {
	got := dfLeadBrief(t, "never dispatch without the owner's words")
	for prefix, doc := range map[string]string{"- Releases:": ReleaseDoc, "- Private development": PrivacyDoc} {
		var lines []string
		for _, l := range strings.Split(got, "\n") {
			if strings.HasPrefix(l, prefix) {
				lines = append(lines, l)
			}
		}
		if len(lines) != 1 || !strings.Contains(lines[0], doc) {
			t.Errorf("%s: want one line naming %s, got %q", prefix, doc, lines)
		}
		checkDocAnchor(t, doc)
	}
	// Every Never line of the brief at 3e19223 (old wording, then what
	// carries it now).
	for _, c := range []struct{ old, now string }{
		{"Never on your own: not when planning finishes, not the next issue, not more workers than named", "Never on your own: not when planning finishes, not the next issue, not more workers than named"},
		{`Never write "the lead merges" or "don't merge" into Lead rules`, `Never write "the lead merges" or "don't merge" into Lead rules`},
		{"never Human Testing (the human approves it in the viewer)", "never Human Testing (the human approves it in the viewer)"},
		{"workers never tick", "workers never tick"},
		{"Never call the viewer over HTTP: issue-cli dispatch and issue-cli status cover it", "Never call the viewer over HTTP"},
		{"Never turn auto-start on, off or pause it without the human's words", "Never turn auto-start on, off or pause it without the human's words"},
		{"Never a /loop cron, a ScheduleWakeup poll or a Monitor", "Never a /loop cron, a ScheduleWakeup poll or a Monitor"},
		{"Pins are titles here, with their Never rules in full", "Pins are titles here, with their Never rules in full"},
		{"CI never messages you", "CI never messages you"},
		{"Never push a release while it is red", "Never push a release while it is red"},
		{"never push a candidate tag", "Never push a candidate tag"},
		{"Never publish the release without the human's explicit ok on a soaked candidate", "never publish without the human's explicit ok on a soaked candidate"},
		{"never push anything else to public (the pre-push guard refuses dev commits)", "never push anything else to public"},
		{"never add work files to the code repo (the pre-push guard refuses them)", "never add work files to the code repo"},
	} {
		if !strings.Contains(got, c.now) {
			t.Errorf("old rule %q is lost: the brief misses %q", c.old, c.now)
		}
	}
}

// checkDocAnchor fails unless ref (path#anchor) names a heading of its page.
func checkDocAnchor(t *testing.T, ref string) {
	t.Helper()
	path, anchor, _ := strings.Cut(ref, "#")
	raw, err := os.ReadFile("../../" + path)
	if err != nil {
		t.Fatal(err)
	}
	drop := regexp.MustCompile("[^a-z0-9 _-]")
	for _, l := range strings.Split(string(raw), "\n") {
		h, ok := strings.CutPrefix(strings.TrimLeft(l, "#"), " ")
		if !ok || !strings.HasPrefix(l, "#") {
			continue
		}
		if strings.ReplaceAll(drop.ReplaceAllString(strings.ToLower(h), ""), " ", "-") == anchor {
			return
		}
	}
	t.Errorf("%s has no heading for #%s", path, anchor)
}
