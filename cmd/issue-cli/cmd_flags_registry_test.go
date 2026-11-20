package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// registryCases lists every command, and every subcommand of commands that
// dispatch on one, as the args that select it. New commands are covered
// without editing this test.
func registryCases() [][]string {
	var cases [][]string
	for _, name := range commandNames() {
		cmd := commandRegistry[name]
		if hasFlagSubcommands(cmd) {
			for _, sub := range cmd.Subcommands {
				cases = append(cases, []string{name, sub})
			}
			continue
		}
		cases = append(cases, []string{name})
	}
	return cases
}

// registryProject writes a one-project config around makeSimpleProject's
// issue and moves into its directory, so no command can reach real data.
func registryProject(t *testing.T) (cfg, issuePath string) {
	t.Helper()
	proj, _, issuePath := makeSimpleProject(t, "in progress")
	root := filepath.Dir(proj.IssueDir)
	cfg = filepath.Join(root, "projects.yaml")
	mustWrite(t, cfg, "projects:\n  - name: test\n    slug: test\n    workdir: "+root+"\n    issues: "+proj.IssueDir+"\n")
	withCwd(t, root)
	return cfg, issuePath
}

func TestEveryCommandHelpNeedsNoProject(t *testing.T) {
	withCwd(t, t.TempDir())
	for _, c := range registryCases() {
		if c[0] == "help" {
			continue
		}
		for _, h := range []string{"--help", "-h"} {
			args := append(append([]string{"--config", "missing.yaml"}, c...), "cli/sample", h)
			var stdout, stderr bytes.Buffer
			if err := run(args, strings.NewReader(""), &stdout, &stderr); err != nil {
				t.Errorf("%v: want help, got error %v", args, err)
				continue
			}
			if !strings.Contains(stdout.String(), "== issue-cli "+c[0]) {
				t.Errorf("%v: help missing header, got %q", args, stdout.String())
			}
		}
	}
}

func TestEveryCommandRejectsUnknownFlags(t *testing.T) {
	cfg, issuePath := registryProject(t)
	before, _ := os.ReadFile(issuePath)
	for _, c := range registryCases() {
		// The flag first, then after positionals: Go's flag package stops at
		// the first positional, which is how `list done --all` used to pass.
		for _, tail := range [][]string{
			{"--no-such-flag"},
			{"cli/sample", "1", "x", "--no-such-flag"},
		} {
			args := append(append([]string{"--config", cfg, "--project", "test"}, c...), tail...)
			var stdout, stderr bytes.Buffer
			err := run(args, strings.NewReader(""), &stdout, &stderr)
			if err == nil {
				t.Errorf("%v: unknown flag accepted (stdout %q)", args, stdout.String())
				continue
			}
			if code := processExitCode(err); code != 2 {
				t.Errorf("%v: exit %d, want 2 (%v)", args, code, err)
			}
			if !strings.Contains(err.Error(), "--no-such-flag") {
				t.Errorf("%v: error does not name the flag: %v", args, err)
			}
		}
	}
	if after, _ := os.ReadFile(issuePath); !bytes.Equal(before, after) {
		t.Errorf("a rejected command changed the issue file")
	}
}

func TestFlagsAfterPositionalsParse(t *testing.T) {
	cfg, _ := registryProject(t)
	var stdout, stderr bytes.Buffer
	// A stray positional before a flag is rejected rather than silently
	// dropped together with the flag.
	err := run([]string{"--config", cfg, "--project", "test", "list", "done", "--status", "done"}, strings.NewReader(""), &stdout, &stderr)
	if err == nil || processExitCode(err) != 2 || !strings.Contains(err.Error(), `unexpected argument "done"`) {
		t.Fatalf("stray positional: want exit 2 naming it, got %v", err)
	}
	stdout.Reset()
	if err := run([]string{"--config", cfg, "--project", "test", "comment", "cli/sample", "hello", "--text", "world"}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("flag after text positional: %v", err)
	}
}

func TestGlobalJSONAfterCommand(t *testing.T) {
	cfg, _ := registryProject(t)
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--config", cfg, "--project", "test", "list", "--all", "--json"}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("list --json: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(stdout.String()), "[") {
		t.Fatalf("want JSON, got %q", stdout.String())
	}
	err := run([]string{"--config", cfg, "list", "--status", "done", "--config", cfg}, strings.NewReader(""), &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "--config is a global flag: put it before the command") {
		t.Fatalf("want global-flag hint, got %v", err)
	}
}

func TestFlagsFirst(t *testing.T) {
	ctx := &Context{}
	fs := newFlagSet("x", ctx)
	fs.String("to", "", "")
	fs.Bool("dry-run", false, "")
	got := flagsFirst(fs, []string{"a", "--to", "b", "-5", "--dry-run", "- [ ] c", "--to=d", "--force is text", "--", "--to"})
	want := []string{"--to", "b", "--dry-run", "--to=d", "--", "a", "-5", "- [ ] c", "--force is text", "--to"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("flagsFirst = %q, want %q", got, want)
	}
}

// A value flag written last, before "--", or before another flag of the
// command has no value: the parse says so instead of taking the "--"
// flagsFirst adds (or the next flag) as its value.
func TestFlagsFirstLeavesADanglingFlagWithoutAValue(t *testing.T) {
	for _, args := range [][]string{
		{"--new"},
		{"slug", "--new"},
		{"--new", "--", "slug"},
		{"--new", "--yes", "slug"},
		{"--new", "-yes"},
	} {
		ctx := &Context{}
		fs := newFlagSet("check-script", ctx)
		name := fs.String("new", "", "")
		fs.Bool("yes", false, "")
		err := parseFlagsArgs(ctx, fs, args)
		if err == nil || !strings.Contains(err.Error(), "flag needs an argument: --new") || processExitCode(err) != 2 {
			t.Errorf("%q: err = %v (--new = %q), want flag needs an argument: --new", args, err, *name)
		}
	}
	// A value that only looks like a flag, or names none of the command's, stays a value.
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--new", "-5"}, "-5"},
		{[]string{"--new", "--other"}, "--other"},
		{[]string{"--new=", "slug"}, ""},
		{[]string{"slug", "--new", "x"}, "x"},
	} {
		ctx := &Context{}
		fs := newFlagSet("check-script", ctx)
		name := fs.String("new", "", "")
		fs.Bool("yes", false, "")
		if err := parseFlagsArgs(ctx, fs, c.args); err != nil || *name != c.want {
			t.Errorf("%q: --new = %q, err %v; want %q", c.args, *name, err, c.want)
		}
	}
}

func TestCheckScriptNewWithoutANameNeedsAnArgument(t *testing.T) {
	ctx := &Context{}
	err := runCheckScript(ctx, []string{"some/slug", "--new"})
	if err == nil || !strings.Contains(err.Error(), "flag needs an argument: --new") {
		t.Fatalf("check-script --new: err = %v, want flag needs an argument: --new", err)
	}
}

func TestRequireSlugRejectsFlag(t *testing.T) {
	_, _, err := requireSlug([]string{"--text", "x"}, "comment")
	if err == nil || processExitCode(err) != 2 || !strings.Contains(err.Error(), "comment takes <slug> first") {
		t.Fatalf("want usage error, got %v", err)
	}
}
