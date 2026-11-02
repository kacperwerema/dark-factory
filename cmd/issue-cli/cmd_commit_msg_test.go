package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

// knownCatalogIDs is this checkout's catalog entry ids.
func knownCatalogIDs(t *testing.T) map[string]bool {
	t.Helper()
	known, err := workflowupgrade.EntryIDs(repoAt(filepath.Join("..", "..")))
	if err != nil {
		t.Fatal(err)
	}
	if !known["parked-status"] {
		t.Fatal("catalog has no parked-status entry: pick another known id for these tests")
	}
	return known
}

const commitSubject = "fix(workflow): something\n\nBody text.\n\n"

func TestCommitMsgProblems(t *testing.T) {
	known := knownCatalogIDs(t)
	for _, tc := range []struct {
		name, lines string
		want        string // "" = no problem
	}{
		{"colon after the id", "Catalog: n/a parked-status: only a refactor", `a colon after the entry id "parked-status"`},
		{"bare n/a", "Catalog: n/a", "names no entry"},
		{"colon after n/a", "Catalog: n/a: parked-status why", "a colon after n/a"},
		{"unknown id (wrapped prose)", "Catalog: n/a line per entry in the docs", `no catalog entry "line"`},
		{"an id without n/a", "Catalog: parked-status", "a bumped entry needs no line"},
		{"package path", "Proves: AC1 internal/memory TestX", `"internal/memory" is a path`},
		{"path with flags", "Proves: AC1 TestX, cmd/issue-cli --project dark-factory TestY", `"cmd/issue-cli" is a path`},
		{"slug#AC", "Proves: workflow/some-slug#AC2 TestX", "the issue slug doesn't go in a Proves: line"},
		{"session/step", "Proves: AC3 lead-tab/TP4", `"lead-tab/TP4" is a <session>/<step>`},
		{"bare step id", "Proves: AC1 TP4", `"TP4" is a box or playtest step id`},
		{"no criterion", "Proves: TestX", "names no criterion"},
		{"well formed", "Catalog: n/a parked-status the change is a refactor\nProves: AC1 TestX", ""},
		{"several and a subtest", "Proves: AC1, AC3 TestA TestB/sub_case TestFoo2", ""},
		{"commit link", "Proves: AC2", ""},
		{"comment lines are git's", "# Proves: AC1 internal/memory TestX", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(CommitMsgProblems(commitSubject+tc.lines+"\n", known), "\n")
			if tc.want == "" {
				if got != "" {
					t.Fatalf("want no problem, got:\n%s", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("want %q in:\n%s", tc.want, got)
			}
		})
	}
}

// commitThroughHook installs the commit-msg check in a fresh repo (with
// this checkout's catalog), puts issue-cli on PATH as this test binary,
// and commits msg through git: the hook git runs, not a call of ours.
func commitThroughHook(t *testing.T, msg string) (string, error) {
	t.Helper()
	repo := t.TempDir()
	git := func(args ...string) (string, error) {
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=T", "-c", "user.email=t@example.com"}, args...)...)
		// The machine's global config (a core.hooksPath) stays out.
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := git("init", "-q"); err != nil {
		t.Fatal(out)
	}
	srcDir := filepath.Join("..", "..", filepath.FromSlash(workflowupgrade.CatalogPath))
	files, err := os.ReadDir(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(repo, filepath.FromSlash(workflowupgrade.CatalogPath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, e := range files {
		b, err := os.ReadFile(filepath.Join(srcDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// An existing hook is kept and runs first.
	hooks := filepath.Join(repo, ".git", "hooks")
	os.MkdirAll(hooks, 0o755)
	if err := os.WriteFile(filepath.Join(hooks, "commit-msg"), []byte("#!/bin/sh\necho earlier-hook-ran >&2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path, kept, err := installCommitMsgHook(repo)
	if err != nil || !kept || !strings.HasSuffix(path, filepath.Join(".git", "hooks", "commit-msg")) {
		t.Fatalf("install: %s kept=%v %v", path, kept, err)
	}
	if _, _, err := installCommitMsgHook(repo); err != nil {
		t.Fatalf("a second install replaces our own hook: %v", err)
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	stub := "#!/bin/sh\nISSUE_CLI_TEST_AS_CLI=1 exec '" + self + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "issue-cli"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	if out, err := git("add", "-A"); err != nil {
		t.Fatal(out)
	}
	msgFile := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(msgFile, []byte(msg), 0o644); err != nil {
		t.Fatal(err)
	}
	return git("commit", "-q", "-F", msgFile)
}

func TestCommitMsgHookRefusesAColonAfterTheCatalogID(t *testing.T) {
	out, err := commitThroughHook(t, commitSubject+"Catalog: n/a parked-status: why\n")
	if err == nil {
		t.Fatalf("commit went through:\n%s", out)
	}
	for _, want := range []string{"earlier-hook-ran", "commit refused", `a colon after the entry id "parked-status"`, `write "Catalog: n/a parked-status <why>", no colon`} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
}

func TestCommitMsgHookRefusesAPathInProves(t *testing.T) {
	out, err := commitThroughHook(t, commitSubject+"Proves: AC1 internal/memory TestX\n")
	if err == nil {
		t.Fatalf("commit went through:\n%s", out)
	}
	if !strings.Contains(out, `"internal/memory" is a path`) {
		t.Errorf("want the path named in:\n%s", out)
	}
}

func TestCommitMsgHookPassesWellFormedLines(t *testing.T) {
	out, err := commitThroughHook(t, commitSubject+"Catalog: n/a parked-status a refactor, nothing projects adopt\nProves: AC1 TestX\n")
	if err != nil {
		t.Fatalf("commit refused: %v\n%s", err, out)
	}
	if strings.Contains(out, "commit refused") {
		t.Fatalf("hook complained:\n%s", out)
	}
}
