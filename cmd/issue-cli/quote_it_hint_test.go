package main

import (
	"bytes"
	"flag"
	"strings"
	"testing"
)

// An unquoted two-word value (append --section Test Plan) names the quoted
// form; every other stray word keeps today's text.
func TestUnquotedFlagValueGetsAQuoteItHint(t *testing.T) {
	cfg, _ := registryProject(t)
	var stdout, stderr bytes.Buffer
	err := run([]string{"--config", cfg, "--project", "test", "append", "cli/sample", "--section", "Test", "Plan", "--body", "x"}, strings.NewReader(""), &stdout, &stderr)
	if err == nil || processExitCode(err) != 2 {
		t.Fatalf("want exit 2, got %v", err)
	}
	for _, want := range []string{`unexpected argument "Plan"`, `--section "Test Plan"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%s", want, err)
		}
	}

	// A stray word not after a string flag's value: today's text, no hint.
	err = run([]string{"--config", cfg, "--project", "test", "list", "done", "--status", "done"}, strings.NewReader(""), &stdout, &stderr)
	if err == nil || strings.Contains(err.Error(), "needs quotes") {
		t.Fatalf("stray positional got a quote hint: %v", err)
	}
	if want := "unexpected argument \"done\" for 'list'\n\nRun: issue-cli list --help"; err.Error() != want {
		t.Errorf("stray positional text changed:\n got %q\nwant %q", err.Error(), want)
	}
}

func TestQuoteItHintForms(t *testing.T) {
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	fs.String("section", "", "")
	fs.Int("limit", 0, "")
	fs.Bool("all", false, "")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--section", "Test", "Plan"}, `--section "Test Plan"`},
		{[]string{"--all", "--section", "A", "B", "C", "--limit", "2"}, `--section "A B C"`},
		{[]string{"--section", "it's", "$HOME"}, `--section 'it'\''s $HOME'`},
		{[]string{"--limit", "5", "extra"}, ""},
		{[]string{"--all", "extra"}, ""},
		{[]string{"--section=Test", "Plan"}, ""},
		{[]string{"word", "--section", "A", "B"}, ""},
		{[]string{"--section", "A", "--", "B"}, ""},
	} {
		if err := fs.Parse(flagsFirst(fs, c.args)); err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		got := quoteItHint(fs, c.args)
		if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%v: hint %q, want %q", c.args, got, c.want)
		}
	}
}
