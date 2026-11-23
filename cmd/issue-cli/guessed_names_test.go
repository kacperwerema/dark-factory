package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestGuessedNamesAnswerWithTheRealCommand is the table of names agents
// guessed in their post-mortems (the queue sweep of 2026-11-21, cause C3):
// each is refused (a non-zero exit) and the refusal names the command that
// does the job.
func TestGuessedNamesAnswerWithTheRealCommand(t *testing.T) {
	isolateTelemetryEnv(t)
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	cfg, work := telemetryFixture(t, "")
	files := map[string]string{
		"docs/testing.md":      "# Testing\n\n## make validate\n\nIt runs the steps.\n",
		"docs/CLI/overview.md": "# CLI\n\n## Commands\n\n### `workflow catalog-check`\n\nIt checks the catalog.\n",
		"issues/x.md":          "---\ntitle: X\nstatus: idea\n---\n\n## Acceptance criteria\n\n- [ ] a box\n",
	}
	for rel, body := range files {
		p := filepath.Join(work, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"ref", "testing", "catalog"}, []string{`no heading in docs/testing.md holds "catalog"`, "issue-cli ref cli catalog", "workflow catalog-check"}},
		{[]string{"workflow", "catalog"}, []string{"unknown workflow subcommand: catalog", "Did you mean: issue-cli workflow catalog-check"}},
		{[]string{"decision", "show"}, []string{`unknown decision subcommand "show"`, "Did you mean: issue-cli decisions --issue <slug>"}},
		{[]string{"decision", "log"}, []string{"Did you mean: issue-cli decisions log"}},
		{[]string{"job", "list"}, []string{`unknown job subcommand "list"`, "Did you mean: issue-cli jobs"}},
		{[]string{"spend", "--context", "--since", "2026-11-01"}, []string{"issue-cli spend --context --days <n>"}},
		{[]string{"spend", "--since", "2026-11-01"}, []string{"issue-cli spend --kinds --since <date>", "--days <n>"}},
		{[]string{"issue-viewer", "--help"}, []string{"unknown command: issue-viewer", "issue-viewer --help"}},
		{[]string{"viewer", "start"}, []string{"unknown viewer subcommand: start", "its own binary: issue-viewer --help"}},
		{[]string{"prove", "--value", "3"}, []string{"prove"}},
	}
	for _, c := range cases {
		args := append([]string{"--config", cfg, "--project", "alpha"}, c.args...)
		_, err := invoke(t, args...)
		if err == nil {
			t.Errorf("%s: succeeded, want a refusal", strings.Join(c.args, " "))
			continue
		}
		if code := processExitCode(err); code == 0 {
			t.Errorf("%s: exit 0, want non-zero", strings.Join(c.args, " "))
		}
		for _, w := range c.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: error lacks %q:\n%s", strings.Join(c.args, " "), w, err)
			}
		}
	}
}

// --project=<x> and --config=<path> work like --project <x> and
// --config <path>.
func TestGlobalFlagsTakeTheEqualsForm(t *testing.T) {
	isolateTelemetryEnv(t)
	cfg, _ := telemetryFixture(t, "")
	if err := os.WriteFile(filepath.Join(filepath.Dir(cfg), "alpha", "issues", "x.md"), []byte("---\ntitle: The X issue\nstatus: idea\n---\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--config=" + cfg, "--project=alpha", "show", "x"},
		{"--config", cfg, "--project=alpha", "show", "x"},
		{"--json", "--config=" + cfg, "--project", "alpha", "show", "x"},
	} {
		out, err := invoke(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(out, "The X issue") {
			t.Fatalf("%v: output lacks the issue:\n%s", args, out)
		}
	}
	if _, err := invoke(t, "--config="+cfg, "--project=nope", "show", "x"); err == nil {
		t.Fatal("--project=nope: want the unknown-project error")
	}
}

func TestExpandGlobalFlagEquals(t *testing.T) {
	cases := []struct{ in, want []string }{
		{[]string{"--project=a", "show"}, []string{"--project", "a", "show"}},
		{[]string{"--json", "--config=/p.yaml", "list"}, []string{"--json", "--config", "/p.yaml", "list"}},
		{[]string{"--project", "a=b", "show"}, []string{"--project", "a=b", "show"}},
		// A command's own --flag=value is left to its flag set.
		{[]string{"spend", "--days=3"}, []string{"spend", "--days=3"}},
		{[]string{"show", "--project=a"}, []string{"show", "--project=a"}},
		{[]string{"--project=", "show"}, []string{"--project", "", "show"}},
	}
	for _, c := range cases {
		if got := expandGlobalFlagEquals(c.in); !slices.Equal(got, c.want) {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
}

// Every subcommand hint names a command that exists.
func TestSubcommandHintsNameRealCommands(t *testing.T) {
	for typed, to := range subcommandHints {
		cmd, _, _ := strings.Cut(typed, " ")
		if lookupCommand(cmd) == nil {
			t.Errorf("%q: %s is no command", typed, cmd)
		}
		first, _, _ := strings.Cut(to, " ")
		if lookupCommand(first) == nil {
			t.Errorf("%q hints %q: %s is no command", typed, to, first)
		}
	}
}
