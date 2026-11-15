package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/standin"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// TestCheckboxFixPastesBackUnchanged: a box text with backticks and $HOME,
// printed as a fix hint (with --project added) and pasted into sh, reaches
// issue-cli as the same text: nothing in it runs or expands.
func TestCheckboxFixPastesBackUnchanged(t *testing.T) {
	text := "Run `touch pwned` then echo $HOME and $(id) — it's \"done\""
	body := "- [ ] " + text + "\n" // no section: no id, so the fix names the text
	fixes := checkboxFixes("cli/x", body, "all_checkboxes_checked")
	if len(fixes) != 1 {
		t.Fatalf("fixes = %q, want one by text", fixes)
	}
	fix := tracker.InjectProject(&tracker.Project{Slug: "dark-factory"}, fixes[0])

	dir := t.TempDir()
	out := filepath.Join(dir, "args")
	standin.Write(t, filepath.Join(dir, "issue-cli"), "for a in \"$@\"; do printf '%s\\n' \"$a\"; done > \"${0%/*}/args\"\n")
	cmd := exec.Command("sh", "-c", fix)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"))
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sh -c %s: %v: %s", fix, err, b)
	}
	if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
		t.Errorf("the backticked command ran: %s", fix)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{"--project", "dark-factory", "check", "cli/x", text}, "\n") + "\n"
	if string(got) != want {
		t.Errorf("sh -c %s\npassed %q\nwant   %q", fix, got, want)
	}
}

// TestInjectProjectSkipsShellQuotedValues: a value written by shell.Quote,
// even one holding "issue-cli " and the '\” escape, keeps its text.
func TestInjectProjectSkipsShellQuotedValues(t *testing.T) {
	proj := &tracker.Project{Slug: "p"}
	for in, want := range map[string]string{
		"issue-cli check s 'run issue-cli show'":                  "issue-cli --project p check s 'run issue-cli show'",
		"issue-cli check s 'it'\\''s issue-cli x' && issue-cli y": "issue-cli --project p check s 'it'\\''s issue-cli x' && issue-cli --project p y",
		"issue-cli transition s --to 'in progress'":               "issue-cli --project p transition s --to 'in progress'",
		"Use 'issue-cli append --section X'":                      "Use 'issue-cli --project p append --section X'",
		"run issue-cli show's footer then issue-cli check":        "run issue-cli --project p show's footer then issue-cli --project p check",
	} {
		if got := tracker.InjectProject(proj, in); got != want {
			t.Errorf("InjectProject(%q)\n = %q\nwant %q", in, got, want)
		}
	}
}
