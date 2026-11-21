package tmuxguard

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A test binary's private temp folder. Isolate points TMPDIR at a fresh
// $TMPDIR/dft-<n> before any test runs, so os.MkdirTemp("", …),
// os.CreateTemp("", …), t.TempDir, and every child a test starts (go build,
// a built issue-cli, a playtest Chrome) write there. After m.Run whatever is
// still in it was left by a test: t.TempDir's own folders are gone by then.
// On 2026-11-21 the owner's $TMPDIR held 6.8 GB of such leftovers, 4.5 GB of
// them cmd/playtest's pt-bins-* (one per test binary run, never removed)
// (factory/no-disk-or-memory-leaks-over-a-247-run-…).

// PrivateTempPrefix names a test binary's private temp folder; the nightly
// clean's tmp area takes one a killed binary left behind.
const PrivateTempPrefix = "dft-"

// SharedTempEnv names the machine's temp folder for what test binaries
// share on purpose (internal/standin's warmed scripts): set once, by the
// outermost binary, before TMPDIR moves.
const SharedTempEnv = "DF_SHARED_TMPDIR"

// toolLeftovers are names outside tools leave in their temp folder however
// they are stopped: removed with the folder, never a test's failure.
var toolLeftovers = []string{
	"com.google.chrome.for.testing.", // Chrome for Testing's per-launch files
	"go-build",                       // a go build killed with its test
	PrivateTempPrefix,                // a child test binary's own (its guard reports it)
}

// privateTemp makes the private folder under the current TMPDIR and points
// TMPDIR at it; restore puts the old value back. "" when it can't be made:
// the binary then runs on the shared TMPDIR as before.
func privateTemp() (dir string, restore func()) {
	old, had := os.LookupEnv("TMPDIR")
	if os.Getenv(SharedTempEnv) == "" {
		os.Setenv(SharedTempEnv, os.TempDir())
	}
	dir, err := os.MkdirTemp("", PrivateTempPrefix)
	if err != nil {
		return "", func() {}
	}
	os.Setenv("TMPDIR", dir)
	return dir, func() {
		if had {
			os.Setenv("TMPDIR", old)
		} else {
			os.Unsetenv("TMPDIR")
		}
	}
}

// TempLeft names what a test left in dir, sorted, without the entries
// outside tools leave (toolLeftovers).
func TempLeft(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var left []string
	for _, e := range entries {
		if toolLeftover(e.Name()) {
			continue
		}
		left = append(left, e.Name())
	}
	sort.Strings(left)
	return left
}

func toolLeftover(name string) bool {
	for _, p := range toolLeftovers {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// tempLeftMessage is the failure for what a binary left in its temp folder.
func tempLeftMessage(left []string) string {
	shown := left
	more := ""
	if len(shown) > 5 {
		shown, more = shown[:5], fmt.Sprintf(" and %d more", len(left)-5)
	}
	return fmt.Sprintf("tmuxguard: %s left %s%s in TMPDIR: make it with t.TempDir, or remove it in t.Cleanup or TestMain "+
		"(a package-level helper's folder goes in TestMain after m.Run)\n", filepath.Base(os.Args[0]), strings.Join(shown, ", "), more)
}
