package tmuxguard_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
)

func TestMain(m *testing.M) {
	if os.Getenv("TMUXGUARD_TEST_SLEEP") != "" {
		time.Sleep(2 * time.Minute) // the stand-in daemon, see actsReal
		os.Exit(0)
	}
	if os.Getenv(guardRunVar) != "" {
		guardedRun() // a run of its own, see agent_cli_test.go
		return
	}
	if os.Getenv(tempRunVar) != "" {
		tempRun() // a run of its own, see tmpdir_test.go
		return
	}
	tmuxguard.Main(m)
}

// TestIsolateDropsWorkerEnv: a test run from a worker's shell does not carry
// its clilog or issue slug into the code under test.
func TestIsolateDropsWorkerEnv(t *testing.T) {
	for _, k := range []string{"ISSUE_CLI_LOG", "ISSUE_VIEWER_ISSUE_SLUG", "ISSUE_VIEWER_LEAD_SCOPE", "TMUX"} {
		t.Setenv(k, "from-the-worker")
	}
	t.Setenv("ISSUE_CLI_TELEMETRY", "off")
	cleanup := tmuxguard.Isolate()
	defer cleanup()
	for _, k := range []string{"ISSUE_CLI_LOG", "ISSUE_VIEWER_ISSUE_SLUG", "ISSUE_VIEWER_LEAD_SCOPE", "TMUX"} {
		if v, ok := os.LookupEnv(k); ok {
			t.Errorf("%s still set (%q) after Isolate", k, v)
		}
	}
	if os.Getenv("ISSUE_CLI_TELEMETRY") != "off" {
		t.Error("ISSUE_CLI_TELEMETRY is not worker env and should stay")
	}
	if !tmuxguard.WorkerEnv("ISSUE_VIEWER_URL") || tmuxguard.WorkerEnv("ISSUE_CLI_TEST_AS_CLI") {
		t.Error("WorkerEnv: ISSUE_VIEWER_URL yes, ISSUE_CLI_TEST_AS_CLI no")
	}
}

// A run that was killed can't clean up after itself, so the next one does:
// SweepStale removes the helpers' private tmux folders once they are old,
// and leaves young ones, other names and an unmarked check folder alone.
func TestSweepStaleRemovesOldPrivateFolders(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	mk := func(name string, age time.Duration, files ...string) string {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Join(dir, "tmux-1"), 0o700); err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if err := os.WriteFile(filepath.Join(dir, f), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Chtimes(dir, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	oldGuard := mk("tmg-old", 3*time.Hour)
	oldTest := mk("tmx-old", 9*24*time.Hour)
	oldCheck := mk("ckt-old", 2*24*time.Hour, ".checks-private-tmux")
	young := []string{mk("tmg-young", time.Minute), mk("ckt-young", 3*time.Hour, ".checks-private-tmux")}
	other := []string{mk("ckt-unmarked", 9*24*time.Hour), mk("pt-scenario", 9*24*time.Hour), mk("someone-else", 9*24*time.Hour)}

	got := tmuxguard.SweepStale(root, now)
	slices.Sort(got)
	want := []string{oldCheck, oldGuard, oldTest}
	if !slices.Equal(got, want) {
		t.Fatalf("removed %v, want %v", got, want)
	}
	for _, dir := range append(young, other...) {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("%s removed: %v", dir, err)
		}
	}
}

// TestChildGitRunsQuiet: a git started from a test binary, through
// exec.Command with a nil Env as most helpers do, reads maintenance.auto
// false and gc.auto 0, whatever the owner's own git config says.
func TestChildGitRunsQuiet(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	// A repo config that says the opposite: the environment's entries win.
	for _, kv := range [][2]string{{"maintenance.auto", "true"}, {"gc.auto", "6700"}} {
		if out, err := exec.Command("git", "-C", repo, "config", kv[0], kv[1]).CombinedOutput(); err != nil {
			t.Fatalf("git config %s: %v\n%s", kv[0], err, out)
		}
	}
	for key, want := range map[string]string{"maintenance.auto": "false", "gc.auto": "0"} {
		out, err := exec.Command("git", "-C", repo, "config", key).Output()
		if got := strings.TrimSpace(string(out)); err != nil || got != want {
			t.Errorf("git config %s = %q (%v), want %q", key, got, err, want)
		}
	}
}

// TestQuietGitKeepsCallersEntries: Isolate adds its two entries after the
// GIT_CONFIG_* entries the caller already had, instead of replacing them.
func TestQuietGitKeepsCallersEntries(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "user.name")
	t.Setenv("GIT_CONFIG_VALUE_0", "caller")
	for _, k := range []string{"GIT_CONFIG_KEY_1", "GIT_CONFIG_VALUE_1", "GIT_CONFIG_KEY_2", "GIT_CONFIG_VALUE_2"} {
		t.Setenv(k, "")
	}
	cleanup := tmuxguard.Isolate()
	defer cleanup()
	want := map[string]string{
		"GIT_CONFIG_COUNT": "3",
		"GIT_CONFIG_KEY_0": "user.name", "GIT_CONFIG_VALUE_0": "caller",
		"GIT_CONFIG_KEY_1": "maintenance.auto", "GIT_CONFIG_VALUE_1": "false",
		"GIT_CONFIG_KEY_2": "gc.auto", "GIT_CONFIG_VALUE_2": "0",
	}
	for k, v := range want {
		if got := os.Getenv(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}
