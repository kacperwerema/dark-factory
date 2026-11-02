package viewer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func init() {
	ciSpawn = func(*tracker.Project) error { panic("test started a real CI run; stub ciSpawn") }
	ciSpawnTier = func(*tracker.Project, string) error { panic("test started a real nightly CI run; stub ciSpawnTier") }
	ciMemory = func() (bool, string) { return true, "" }
}

// TestCIWatcherStartsTheNightlyTier: with ci.nightly set and the head
// already tested, the watcher starts the nightly tier once (no run of it
// yet), and waits while mem-guard says no.
func TestCIWatcherStartsTheNightlyTier(t *testing.T) {
	proj, root := ciRepo(t, "p", "ci:\n  run:\n    - make check\n    - cmd: make ux-test ALL=1\n      tier: slow\n  nightly:\n    tier: slow\n")
	head := runGitT(t, root, "rev-parse", "HEAD")
	if err := os.MkdirAll(filepath.Join(root, ".ci"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ci", "state.json"), []byte(`{"branch":"main","last_sha":"`+head+`","state":"green"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	origSpawn, origTier, origMem := ciSpawn, ciSpawnTier, ciMemory
	defer func() { ciSpawn, ciSpawnTier, ciMemory = origSpawn, origTier, origMem }()
	ciSpawn = func(*tracker.Project) error { t.Error("tested head must not start a default run"); return nil }
	var started []string
	ciSpawnTier = func(p *tracker.Project, tier string) error { started = append(started, p.Slug+":"+tier); return nil }
	ciMemory = func() (bool, string) { return false, "mem-guard says no" }

	w := newCIWatcher()
	w.check([]tracker.Project{proj})
	if len(started) != 0 {
		t.Fatalf("started while mem-guard said no: %v", started)
	}
	ciMemory = func() (bool, string) { return true, "" }
	w.check([]tracker.Project{proj})
	w.check([]tracker.Project{proj})
	if strings.Join(started, ",") != "p:slow" {
		t.Errorf("started = %v, want one p:slow", started)
	}
}

// ciRepo is a throwaway git repo on main holding a workflow.yaml with the
// given ci: block (none when ""), an issues/ dir and a project pointing at it.
func ciRepo(t *testing.T, slug, ciBlock string) (tracker.Project, string) {
	t.Helper()
	root := t.TempDir()
	runGitT(t, root, "init", "-q", "-b", "main")
	runGitT(t, root, "config", "user.email", "ci@test")
	runGitT(t, root, "config", "user.name", "ci")
	runGitT(t, root, "config", "commit.gpgsign", "false")
	if err := os.MkdirAll(filepath.Join(root, "issues"), 0o755); err != nil {
		t.Fatal(err)
	}
	wf := "statuses:\n  - name: \"idea\"\n  - name: \"done\"\ntransitions: []\n" + ciBlock
	if err := os.WriteFile(filepath.Join(root, "workflow.yaml"), []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitT(t, root, "add", "workflow.yaml")
	runGitT(t, root, "commit", "-q", "-m", "initial")
	return tracker.Project{Name: slug, Slug: slug, IssueDir: filepath.Join(root, "issues"), WorkflowFile: filepath.Join(root, "workflow.yaml"), WorkDir: root}, root
}

func runGitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestCIWatcherSpawnsOncePerUntestedHead(t *testing.T) {
	on, root := ciRepo(t, "on", "ci:\n  run:\n    - make check\n")
	off, _ := ciRepo(t, "off", "")
	// A second project on the same repo must not start a second run.
	twin := on
	twin.Slug = "twin"

	var spawned []string
	orig := ciSpawn
	defer func() { ciSpawn = orig }()
	ciSpawn = func(p *tracker.Project) error { spawned = append(spawned, p.Slug); return nil }

	w := newCIWatcher()
	projects := []tracker.Project{off, on, twin}
	w.check(projects)
	if strings.Join(spawned, ",") != "on" {
		t.Fatalf("spawned = %v, want [on]", spawned)
	}
	w.check(projects)
	if len(spawned) != 1 {
		t.Errorf("same head spawned again: %v", spawned)
	}
	runGitT(t, root, "commit", "-q", "--allow-empty", "-m", "merge lands")
	w.check(projects)
	if len(spawned) != 2 {
		t.Errorf("new head did not spawn: %v", spawned)
	}
}

func TestCIWatcherLogsBranchErrorsOnce(t *testing.T) {
	proj, _ := ciRepo(t, "p", "ci:\n  branch: nope\n  run:\n    - make check\n")
	orig := ciSpawn
	defer func() { ciSpawn = orig }()
	ciSpawn = func(*tracker.Project) error { t.Error("spawned for a missing branch"); return nil }
	w := newCIWatcher()
	w.check([]tracker.Project{proj})
	w.check([]tracker.Project{proj})
	if len(w.lastErr) != 1 {
		t.Errorf("lastErr = %v", w.lastErr)
	}
}
