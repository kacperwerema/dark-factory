package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A command that takes no stdin input leaves stdin alone: in a
// `while read s; do issue-cli …; done < slugs` loop every line still reaches
// the loop (retro 20261031-210327). Only --body-file -, workflow init,
// ci gotest, privacy pre-push and the commands that hand stdin to a child
// (check-script, connector exec) read it.
func TestCommandsWithoutStdinInputLeaveStdinUnread(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg := writeProjectsConfig(t, dir, "acme")
	issue := filepath.Join(dir, "acme-issues", "Core", "first.md")
	if err := os.MkdirAll(filepath.Dir(issue), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(issue, []byte("---\ntitle: \"First\"\nstatus: \"idea\"\nsystem: \"Core\"\n---\n\n- [ ] A box\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lines := filepath.Join(dir, "lines.txt")
	if err := os.WriteFile(lines, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"show", "core/first"},
		{"checklist", "core/first"},
		{"list"},
		{"version"},
		{"comment", "core/first", "progress: x"},
		{"check", "core/first", "A box"},
		{"decide", "core/first", "--provisional", "--text", "x"},
		{"append", "core/first", "--body", "## Notes\n\nx"},
		{"transition", "core/first", "--to", "in progress", "--dry-run"},
		{"create", "--title", "Second", "--system", "Core"},
		{"next"},
		{"memory", "search", "x"},
	} {
		// The child runs issue-cli on the shared stdin, then cat prints
		// what it left.
		script := `"$CLI" --config "$CFG" "$@" >/dev/null 2>&1; cat`
		cmd := exec.Command("sh", append([]string{"-c", script, "sh"}, args...)...)
		cmd.Env = append(os.Environ(), "ISSUE_CLI_TEST_AS_CLI=1", "CLI="+self, "CFG="+cfg)
		in, err := os.Open(lines)
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stdin = in
		out, err := cmd.Output()
		in.Close()
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if got := strings.TrimSpace(string(out)); got != "one\ntwo" {
			t.Errorf("issue-cli %s read stdin: cat saw %q, want both lines", strings.Join(args, " "), got)
		}
	}
}
