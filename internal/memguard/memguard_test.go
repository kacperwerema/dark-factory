package memguard

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/standin"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// standIn puts a mem-guard on PATH that prints testdata's files: a change
// of format in mem-guard is a change here and a red test. Its body is the
// same for every call (the fixture and exit code come from the
// environment), so macOS's first-exec check is paid once (internal/standin).
func standIn(t *testing.T, check string, checkExit int) {
	t.Helper()
	abs, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	standin.Write(t, filepath.Join(dir, "mem-guard"), "case \"$1\" in\n  used) /bin/cat \"$MEMGUARD_TESTDATA/used.txt\" ;;\n  check) /bin/cat \"$MEMGUARD_TESTDATA/$MEMGUARD_CHECK\"; exit \"$MEMGUARD_CHECK_EXIT\" ;;\nesac\n")
	t.Setenv("MEMGUARD_TESTDATA", abs)
	t.Setenv("MEMGUARD_CHECK", check)
	t.Setenv("MEMGUARD_CHECK_EXIT", strconv.Itoa(checkExit))
	t.Setenv("PATH", dir)
}

func TestRead(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if m := Read(); m.Found {
		t.Fatalf("no mem-guard on PATH: %+v", m)
	}
	if _, err := Used(); err == nil || err.Error() != "mem-guard not found" {
		t.Fatalf("Used without mem-guard: %v", err)
	}
	if found, _, _ := Check(); found {
		t.Fatal("Check found a mem-guard")
	}

	standIn(t, "check-ok.txt", 0)
	if m := Read(); !m.Found || m.Used != 44 || !m.CanStart || m.Note != "" {
		t.Fatalf("check ok: %+v", m)
	}
	standIn(t, "check-blocked.txt", 1)
	if m := Read(); !m.Found || m.Used != 44 || m.CanStart || !strings.HasPrefix(m.Note, "BLOCKED: memory used 80%") {
		t.Fatalf("check blocked: %+v", m)
	}
	// A BLOCKED line blocks whatever the exit status.
	standIn(t, "check-blocked.txt", 0)
	if _, blocked, _ := Check(); !blocked {
		t.Fatal("BLOCKED with exit 0 did not block")
	}
}

// The three formats mem-guard prints, pinned from a real run.
func TestFormats(t *testing.T) {
	if n, err := ParseUsed(fixture(t, "used.txt")); err != nil || n != 44 {
		t.Errorf("used: %d %v", n, err)
	}
	if n, err := ParseUsed("44%\n"); err != nil || n != 44 {
		t.Errorf("used with a percent sign: %d %v", n, err)
	}
	if _, err := ParseUsed("lots"); err == nil || !strings.Contains(err.Error(), `said "lots"`) {
		t.Errorf("used garbage: %v", err)
	}
	if n, ok := CheckUsed(fixture(t, "check-ok.txt")); !ok || n != 44 {
		t.Errorf("check ok: %d %v", n, ok)
	}
	if n, ok := CheckUsed(fixture(t, "check-blocked.txt")); !ok || n != 80 {
		t.Errorf("check blocked: %d %v", n, ok)
	}
	if _, ok := CheckUsed("BLOCKED"); ok {
		t.Error("a line without a number gave one")
	}
	st, err := ParseStatus(fixture(t, "status.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if want := (Status{Used: 44, Start: StartMax, Pause: PauseAt, Resume: ResumeAt, Hard: HardLimit}); st != want {
		t.Errorf("status = %+v, want %+v", st, want)
	}
	if _, err := ParseStatus("up"); err == nil {
		t.Error("status garbage parsed")
	}
}

// The constants are a copy of mem-guard's numbers, so the real one is asked
// where it is installed; its env overrides are left out.
func TestLimitsMatchMemGuard(t *testing.T) {
	path, err := exec.LookPath("mem-guard")
	if err != nil {
		t.Skip("no mem-guard on PATH")
	}
	cmd := exec.Command(path, "status")
	for _, kv := range os.Environ() {
		switch strings.SplitN(kv, "=", 2)[0] {
		case "START_MAX", "PAUSE_AT", "RESUME_AT", "HARD_LIMIT":
		default:
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("mem-guard status: %v", err)
	}
	st, err := ParseStatus(string(out))
	if err != nil {
		t.Fatalf("the format moved, update testdata and ParseStatus: %v", err)
	}
	st.Used = 0
	if want := (Status{Start: StartMax, Pause: PauseAt, Resume: ResumeAt, Hard: HardLimit}); st != want {
		t.Errorf("mem-guard says %+v, the package says %+v", st, want)
	}
}

// repoRoot walks up to go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test")
		}
		dir = parent
	}
}

// Every Go caller goes through this package, and every copy of a line or a
// load comparison is gone: a new copy turns this red.
func TestEveryCallerGoesThroughThePackage(t *testing.T) {
	root := repoRoot(t)
	banned := []struct {
		re   *regexp.Regexp
		why  string
		only string // the one file allowed
	}{
		{regexp.MustCompile(`\(\s*(\w+,\s*)?"mem-guard"`), `runs mem-guard itself: use internal/memguard`, "internal/memguard/memguard.go"},
		{regexp.MustCompile(`memory_pressure`), `measures memory itself: mem-guard owns the numbers`, ""},
		{regexp.MustCompile(`Avg1\s*(>|>=|<|<=)`), `compares the load itself: use autostart.MachineHold or LoadHold`, "internal/autostart/decide.go"},
		{regexp.MustCompile(`DefaultBelow\s*=\s*\d`), `declares the 50% line again: alias autostart.DefaultBelow`, "internal/autostart/autostart.go"},
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == ".worktrees" || name == "issues" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		for _, b := range banned {
			if rel != b.only && b.re.Match(data) {
				t.Errorf("%s %s", rel, b.why)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// under-load.sh can't import the package: it reads `mem-guard used`, the
// pinned format, and its default stop is ResumeAt.
func TestUnderLoadScriptReadsTheSameLine(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "under-load.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if want := "max_pct=" + strconv.Itoa(ResumeAt) + "\n"; !strings.Contains(s, want) {
		t.Errorf("under-load.sh lost %q", want)
	}
	if !strings.Contains(s, "mem-guard used") || strings.Contains(s, "mem-guard status") {
		t.Error("under-load.sh must read `mem-guard used`, never parse status")
	}
}
