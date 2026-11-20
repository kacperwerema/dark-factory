package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/timing"
)

// fakePrivateName is a made-up project name this test puts on its own
// private-names list; the owner's real list never holds it.
const fakePrivateName = "Quillbrook"

// privateNamesList points the private-names list at a temp file holding
// names, one per line.
func privateNamesList(t *testing.T, names ...string) {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	p := filepath.Join(cfg, "ai-workflow-engine", "private-names.txt")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("# test list\n"+strings.Join(names, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A staged line naming a project from the list gets a warning with its
// file:line and the stand-in to use, and the commit still goes through
// git's real hook. The warning names the list's line, never the name.
func TestCommitMsgWarnsOnStagedPrivateName(t *testing.T) {
	privateNamesList(t, "Another Game", fakePrivateName)
	out, err := commitFilesThroughHook(t, commitSubject+"A note on the "+strings.ToLower(fakePrivateName)+" board\n", map[string]string{
		"internal/x/x.go":  "package x\n\n// Seen first on the " + fakePrivateName + " board.\nvar A = 1\n",
		"docs/clean.md":    "# Clean\n\nA project without worktree: true.\n",
		"internal/x/ok.go": "package x\n\n// " + fakePrivateName + "ish is another word.\n",
	})
	if err != nil {
		t.Fatalf("the commit was refused, it must only warn: %v\n%s", err, out)
	}
	for _, want := range []string{
		"internal/x/x.go:3: a private name (line 2 of the private-names list)",
		"commit message:5: a private name (line 2 of the private-names list)",
		"would fail make leak-check",
		"use a stand-in",
		"Orchard",
		"a project without worktree: true",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
	for _, not := range []string{"docs/clean.md", "internal/x/ok.go", "commit refused"} {
		if strings.Contains(out, not) {
			t.Errorf("%q should not show:\n%s", not, out)
		}
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "warning:") && strings.Contains(strings.ToLower(l), strings.ToLower(fakePrivateName)) {
			t.Errorf("the warning echoes the private name: %s", l)
		}
	}
}

// A clean staged diff gets no warning.
func TestCommitMsgNoLeakWarningOnACleanDiff(t *testing.T) {
	privateNamesList(t, fakePrivateName)
	out, err := commitFilesThroughHook(t, commitSubject, map[string]string{"a.go": "package a\n"})
	if err != nil {
		t.Fatalf("refused: %v\n%s", err, out)
	}
	if strings.Contains(out, "leak-check") {
		t.Errorf("a clean diff warned:\n%s", out)
	}
}

// The scan of a 50-file staged diff stays under 1 s. On a loaded machine
// it may take as long as three plain git diff --cached calls timed in
// turns with it instead (timing.Check: best of each over the rounds).
func TestCommitMsgLeakWarnIsFast(t *testing.T) {
	privateNamesList(t, "Another Game", fakePrivateName, "third-name")
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=T", "-c", "user.email=t@example.com"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	var body strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&body, "// line %d of a staged file, some prose about /home/someone/work and more\n", i)
	}
	for i := 0; i < 50; i++ {
		text := body.String()
		if i == 49 {
			text += "// " + fakePrivateName + "\n"
		}
		if err := os.WriteFile(filepath.Join(repo, fmt.Sprintf("f%02d.go", i)), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("add", "-A")

	diff := func() {
		if err := exec.Command("git", "-C", repo, "diff", "--cached", "-U0").Run(); err != nil {
			t.Fatal(err)
		}
	}
	var w bytes.Buffer
	scan := func() {
		w.Reset()
		warnStagedLeaks(&w, repo, commitSubject)
	}
	check := timing.Check{Limit: time.Second, Ratio: 3}
	r := check.Run(timing.Timed(scan), timing.Timed(diff))
	if !strings.Contains(w.String(), "f49.go:301:") {
		t.Fatalf("the hit in the 50th file wasn't found:\n%s", w.String())
	}
	if r.Slow {
		t.Errorf("the staged scan of 50 files: %s (baseline: a plain git diff --cached)", r)
	}
}
