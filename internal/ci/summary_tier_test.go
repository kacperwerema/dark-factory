package ci

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// tierFixture is a git repo on main with a ci: block and the given state.
func tierFixture(t *testing.T, st *State) (string, *tracker.WorkflowConfig) {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"config", "maintenance.auto", "false"}, {"config", "gc.auto", "0"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := WriteState(root, st); err != nil {
		t.Fatal(err)
	}
	wf := &tracker.WorkflowConfig{CI: &tracker.CIConfig{Run: []tracker.CICommand{{Cmd: "true"}, {Cmd: "make ux-test", Tier: "slow"}}}}
	return root, wf
}

func greenState(now time.Time) *State {
	return &State{Branch: "main", State: Green, GreenSha: "64be5a9000000", GreenAt: now.Add(-time.Minute), LastSha: "64be5a9000000"}
}

func TestAgentLineNamesARedSlowTier(t *testing.T) {
	now := time.Now()
	st := greenState(now)
	st.Tiers = map[string]TierState{"slow": {Sha: "fa50934000000", State: Red, Failed: "make ux-test", Failures: []string{"crew-all/TP5"}, Finished: now.Add(-3 * time.Hour)}}
	root, wf := tierFixture(t, st)
	line := AgentLine(root, wf)
	if !strings.HasPrefix(line, "Local CI: main green at 64be5a9") {
		t.Errorf("default line changed:\n%s", line)
	}
	for _, want := range []string{"tier slow: red at fa50934 (3h ago): make ux-test: crew-all/TP5", "inherited from main, not caused by your branch"} {
		if !strings.Contains(line, want) {
			t.Errorf("want %q in:\n%s", want, line)
		}
	}
}

func TestAgentLineGreenTiersUnchanged(t *testing.T) {
	now := time.Now()
	st := greenState(now)
	st.Tiers = map[string]TierState{"slow": {Sha: "fa50934000000", State: Green, Finished: now.Add(-3 * time.Hour)}}
	root, wf := tierFixture(t, st)
	line := AgentLine(root, wf)
	if strings.Contains(line, "\n") || strings.Contains(line, "tier slow") {
		t.Errorf("green tiers should stay one line:\n%s", line)
	}
	if !strings.HasPrefix(line, "Local CI: main green at 64be5a9 (1m ago)") {
		t.Errorf("line: %s", line)
	}
}
