package main

import (
	"testing"
)

// TestUnknownCommandNamesTheCommandThatDoesTheJob pins the error's wording:
// the name, the command that does the job with its short help, and help.
func TestUnknownCommandNamesTheCommandThatDoesTheJob(t *testing.T) {
	isolateTelemetryEnv(t)
	cfg, _ := telemetryFixture(t, "")
	cases := []struct{ typed, want string }{
		// The hint table: words that name a command called otherwise.
		{"body", "unknown command: body\n\nDid you mean: issue-cli show — Full context dump for an issue (alias: show)\n\nRun: issue-cli help"},
		{"add-check", "unknown command: add-check\n\nDid you mean: issue-cli check-add — " + checkAddCommand.ShortHelp + "\n\nRun: issue-cli help"},
		{"checklist-add", "unknown command: checklist-add\n\nDid you mean: issue-cli check-add — " + checkAddCommand.ShortHelp + "\n\nRun: issue-cli help"},
		{"list-comments", "unknown command: list-comments\n\nDid you mean: issue-cli comments — " + commentsCommand.ShortHelp + "\n\nRun: issue-cli help"},
		// A typo, within two edits.
		{"transiton", "unknown command: transiton\n\nDid you mean: issue-cli transition — " + commandRegistry["transition"].ShortHelp + "\n\nRun: issue-cli help"},
		// The one command a name starts.
		{"trans", "unknown command: trans\n\nDid you mean: issue-cli transition — " + commandRegistry["transition"].ShortHelp + "\n\nRun: issue-cli help"},
		// A known command in another case.
		{"Comments", "unknown command: Comments\n\nDid you mean: issue-cli comments — " + commentsCommand.ShortHelp + "\n\nRun: issue-cli help"},
		// Nothing near: no guess.
		{"foo", "unknown command: foo\n\nRun: issue-cli help"},
		{"frob", "unknown command: frob\n\nRun: issue-cli help"},
	}
	for _, c := range cases {
		_, err := invoke(t, "--config", cfg, "--project", "alpha", c.typed)
		if err == nil {
			t.Fatalf("%s: expected an unknown command error", c.typed)
		}
		if err.Error() != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.typed, err.Error(), c.want)
		}
	}
}

// TestCommandHintsNameRealCommands keeps the hint table honest: a hint is
// never a command or alias itself (it would never be reached), and always
// names one that exists.
func TestCommandHintsNameRealCommands(t *testing.T) {
	for word, to := range commandHints {
		if lookupCommand(word) != nil {
			t.Errorf("hint %q is a command or alias now: drop it from commandHints", word)
		}
		if lookupCommand(to) == nil {
			t.Errorf("hint %q names %q, which is no command", word, to)
		}
	}
}

func TestSuggestCommand(t *testing.T) {
	cases := map[string]string{
		"chec":      "check", // several start it: the nearest
		"checklst":  "checklist",
		"comment":   "comment",
		"ls":        "list",
		"xy":        "",
		"aaaaaaaaa": "",
	}
	for typed, want := range cases {
		if got := suggestCommand(typed); got != want {
			t.Errorf("suggestCommand(%q) = %q, want %q", typed, got, want)
		}
	}
}
