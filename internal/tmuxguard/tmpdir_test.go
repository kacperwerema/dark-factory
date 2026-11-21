package tmuxguard_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
)

// tempRunVar makes this test binary a run of its own: Isolate, a stand-in
// for m.Run that leaves TEMP_LEAVE (a name, "" for nothing) in os.TempDir,
// the cleanup. TEMP_SEEN gets the private folder's path.
const tempRunVar = "TMUXGUARD_TEST_TEMP"

func tempRun() {
	cleanup := tmuxguard.Isolate()
	dir := os.TempDir()
	_ = os.WriteFile(os.Getenv("TEMP_SEEN"), []byte(dir), 0o600)
	if name := os.Getenv("TEMP_LEAVE"); name != "" {
		_ = os.MkdirAll(filepath.Join(dir, name), 0o700)
	}
	// What t.TempDir makes is gone by the time m.Run returns.
	made, _ := os.MkdirTemp("", "TestSomething")
	_ = os.RemoveAll(made)
	cleanup()
	os.Exit(0)
}

// runTemp runs the binary as tempRun with leave left behind, on a shared
// TMPDIR of its own; it returns the exit code, stderr and the private
// folder the run used.
func runTemp(t *testing.T, leave string) (int, string, string) {
	t.Helper()
	shared := t.TempDir()
	seen := filepath.Join(t.TempDir(), "seen")
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), tempRunVar+"=1", "TEMP_LEAVE="+leave, "TEMP_SEEN="+seen, "TMPDIR="+shared)
	out, err := cmd.CombinedOutput()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	private, _ := os.ReadFile(seen)
	if !strings.HasPrefix(string(private), filepath.Join(shared, tmuxguard.PrivateTempPrefix)) {
		t.Fatalf("the run's TMPDIR = %q, want a %s folder under %s", private, tmuxguard.PrivateTempPrefix, shared)
	}
	if left, _ := os.ReadDir(shared); len(left) != 0 {
		t.Errorf("the shared TMPDIR still holds %v after the run: the private folder must go", left)
	}
	return code, string(out), string(private)
}

// TestTestBinaryLeavingATempEntryFails: a test that makes a folder in
// TMPDIR and never removes it fails its binary, the folder named, and the
// shared TMPDIR is left as it was.
func TestTestBinaryLeavingATempEntryFails(t *testing.T) {
	code, out, _ := runTemp(t, "pt-bins-123")
	if code != 1 || !strings.Contains(out, "left pt-bins-123 in TMPDIR") {
		t.Fatalf("exit %d, output %q: want exit 1 naming pt-bins-123", code, out)
	}
}

// TestTestBinaryCleanRunPasses: nothing left, or only what Chrome and go
// leave by themselves, passes, and the private folder still goes.
func TestTestBinaryCleanRunPasses(t *testing.T) {
	for _, leave := range []string{"", "com.google.chrome.for.testing.abc123", "go-build4242"} {
		if code, out, _ := runTemp(t, leave); code != 0 {
			t.Errorf("leaving %q: exit %d, output %q; want 0", leave, code, out)
		}
	}
}

func TestTempLeftSkipsToolLeftovers(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"b-left", "com.google.chrome.for.testing.x", "go-build1", "dft-9", "a-left"} {
		if err := os.Mkdir(filepath.Join(dir, n), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(tmuxguard.TempLeft(dir), ","); got != "a-left,b-left" {
		t.Fatalf("TempLeft = %s, want a-left,b-left", got)
	}
}
