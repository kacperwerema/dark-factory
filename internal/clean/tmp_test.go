package clean

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// setDirTime sets path's own mtime (a folder's, after its files went in).
func setDirTime(t *testing.T, path string, mod time.Time) {
	t.Helper()
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

// TestTmpTakesOnlyOldUnheldFactoryEntries: an old pt-bins-* folder and an
// old Chrome entry go; a young one, one a live process holds, another
// program's, another project's without clean.tmp_prefixes and a name that
// only starts like the factory's stay.
func TestTmpTakesOnlyOldUnheldFactoryEntries(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 21, 4, 0, 0, 0, time.Local)
	old, young := now.Add(-48*time.Hour), now.Add(-2*time.Hour)
	mk := func(name string, size int, mod time.Time) string {
		p := filepath.Join(dir, name)
		writeFile(t, filepath.Join(p, "f"), size, mod)
		setDirTime(t, p, mod)
		return p
	}
	gone := []string{
		mk("pt-bins-1234", 2000, old),
		mk("com.google.chrome.for.testing.0aB9xZ", 10, old),
		mk("go-build998877", 100, old),
		mk("dft-5551", 1, old),
	}
	cover := filepath.Join(dir, "dark-factory-cover-42.guard.out")
	writeFile(t, cover, 500, old)
	gone = append(gone, cover)
	stay := []string{
		mk("pt-bins-5678", 2000, young),   // younger than 24 h
		mk("issue-cli-help-42", 10, old),  // held by a live process
		mk("fma-agent-777", 10, old),      // another project's: not listed
		mk("pt-notrandom", 10, old),       // pt- without the random part
		mk("talagent", 10, old),           // another program's
		mk("node-compile-cache", 10, old), // another program's
	}
	// A folder whose own mtime is old but a file inside is young stays.
	deep := mk("pt-serve-31", 10, old)
	writeFile(t, filepath.Join(deep, "live.log"), 10, young)
	stay = append(stay, deep)

	inUse := map[string]bool{"issue-cli-help-42": true}
	dry := Tmp(dir, nil, 24*time.Hour, inUse, now, true)
	for _, p := range append(gone, stay...) {
		if !exists(p) {
			t.Fatalf("dry run removed %s", p)
		}
	}
	a := Tmp(dir, nil, 24*time.Hour, inUse, now, false)
	if a.FreedBytes != dry.FreedBytes || a.FreedBytes != 2000+10+100+1+500 {
		t.Errorf("freed %d (dry %d), want %d", a.FreedBytes, dry.FreedBytes, 2611)
	}
	for _, p := range gone {
		if exists(p) {
			t.Errorf("%s stayed; want it gone", filepath.Base(p))
		}
	}
	for _, p := range stay {
		if !exists(p) {
			t.Errorf("%s went; want it kept", filepath.Base(p))
		}
	}
	if !strings.Contains(a.Line(), "5 entries removed, 3 kept (in use or younger than 24 h)") {
		t.Errorf("line %q", a.Line())
	}
}

// TestTmpOtherProjectsOnlyWhenListed: clean.tmp_prefixes lets the area
// take another project's names, nothing else of theirs.
func TestTmpOtherProjectsOnlyWhenListed(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old := now.Add(-72 * time.Hour)
	for _, n := range []string{"fma-agent-1", "fma-", "orchard-foods-2"} {
		writeFile(t, filepath.Join(dir, n, "x"), 1, old)
		setDirTime(t, filepath.Join(dir, n), old)
	}
	Tmp(dir, []string{"fma-"}, 24*time.Hour, map[string]bool{}, now, false)
	if exists(filepath.Join(dir, "fma-agent-1")) {
		t.Error("fma-agent-1 stayed with fma- listed")
	}
	if !exists(filepath.Join(dir, "fma-")) || !exists(filepath.Join(dir, "orchard-foods-2")) {
		t.Error("a bare prefix or an unlisted project's entry went")
	}
}

// TestTmpUnknownInUseTakesNothing: lsof failing (nil) leaves every entry.
func TestTmpUnknownInUseTakesNothing(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeFile(t, filepath.Join(dir, "pt-bins-1", "x"), 1, now.Add(-72*time.Hour))
	setDirTime(t, filepath.Join(dir, "pt-bins-1"), now.Add(-72*time.Hour))
	a := Tmp(dir, nil, 24*time.Hour, nil, now, false)
	if !exists(filepath.Join(dir, "pt-bins-1")) || !strings.Contains(a.Note, "left alone") {
		t.Fatalf("an unknown in-use set took entries: %s", a.Line())
	}
}

// tempCall is os.MkdirTemp("", "<pattern>") or os.CreateTemp("", "<pattern>").
var tempCall = regexp.MustCompile(`os\.(?:MkdirTemp|CreateTemp)\("", (?:fmt\.Sprintf\()?"([^"]+)"`)

// TestFactoryTmpNamesEveryTempPattern: every temp name the module's code
// makes in $TMPDIR is one the tmp area takes, so a new one can't leak past
// the nightly clean. Patterns built at run time (Sprintf) count by their
// literal head; one with none (browser.go's profile) is the prefix it is
// built from. A test's temp names land in its binary's private TMPDIR
// (internal/tmuxguard), whose dft- folder the area takes when a killed
// binary leaves it, so only TestMain files count among the tests: they make
// theirs before the guard moves TMPDIR.
func TestFactoryTmpNamesEveryTempPattern(t *testing.T) {
	root := filepath.Join("..", "..")
	var missed []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".worktrees" || d.Name() == "node_modules" || d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) && p != root {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.HasSuffix(p, "_test.go") && !strings.Contains(string(src), "func TestMain(") {
			return nil
		}
		var calls [][]string
		for _, line := range strings.Split(string(src), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			calls = append(calls, tempCall.FindAllStringSubmatch(line, -1)...)
		}
		for _, m := range calls {
			pattern := m[1]
			pattern, _, _ = strings.Cut(pattern, "%") // a Sprintf'd pattern: its literal head
			if pattern == "" {
				continue
			}
			name := pattern + "123"
			if strings.Contains(pattern, "*") {
				name = strings.Replace(pattern, "*", "123", 1)
			}
			if !tmpTakes(name, nil) && !tmpTakes(name+"-1", nil) {
				missed = append(missed, p+": "+m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(missed) > 0 {
		t.Fatalf("temp names the nightly clean's tmp area doesn't take; add each to FactoryTmp (internal/clean/tmp.go):\n  %s", strings.Join(missed, "\n  "))
	}
}
