package main

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// helpHintRe finds an issue-cli command a help text tells the reader to run,
// with its arguments up to the end of the code span, parenthesis, sentence
// or line it sits in.
var helpHintRe = regexp.MustCompile("issue-cli((?: [^`();\n]*)?)")

// helpUsageRe finds a usage line: an indented line that starts with a word
// (the command's own name, checked by the caller) and its arguments.
var helpUsageRe = regexp.MustCompile("(?m)^[ \t]*([a-z][a-z-]*)((?: [^`();\n]*)?)$")

// Every --flag an issue-cli hint in a registered command's help names is one
// the hinted command (and subcommand) accepts. A hint is `issue-cli <cmd>
// [<sub> [<sub>]] …` anywhere in ShortHelp or LongHelp, or a usage line
// starting with the command's own name (`  bench task add … [--merged <sha>]`).
// TestExperimentHintsNameRealFlags did this for cmd_experiment.go alone; a
// help text that drifted from its FlagSet sends agents to a call that fails
// with "flag provided but not defined". Flags named in prose about another
// tool (`git rm --cached`, `dispatch --autostart` without issue-cli) are not
// hints and are not checked.
func TestEveryHelpHintNamesRealFlags(t *testing.T) {
	isolateTelemetryEnv(t)
	withCwd(t, t.TempDir())
	issueDir := t.TempDir()
	global := append([]string{"help"}, globalFlagNames...)
	cache := map[string][]string{}

	check := func(owner string, cmd *Command, hint string, words []string) {
		path := hintPath(cmd, words)
		// A plain first word the command dispatches on without declaring it
		// in Subcommands (`decisions log`): the flags reached through it
		// count as well as the bare command's.
		undeclared := ""
		if path == nil && !hasFlagSubcommands(cmd) && len(words) > 0 && plainWordRe.MatchString(words[0]) {
			undeclared = words[0]
		}
		key := strings.Join(append([]string{cmd.Name}, path...), " ") + "|" + undeclared
		valid, seen := cache[key]
		if !seen {
			valid = append(hintFlags(cmd, path, issueDir), cmd.ExtraFlags...)
			if undeclared != "" {
				valid = append(valid, hintFlags(cmd, []string{undeclared}, issueDir)...)
			}
			cache[key] = valid
		}
		for _, f := range templateFlagRe.FindAllStringSubmatch(strings.Join(words, " "), -1) {
			if !slices.Contains(valid, f[1]) && !slices.Contains(global, f[1]) {
				t.Errorf("issue-cli %s help: %q names --%s; %s accepts %v", owner, strings.TrimSpace(hint), f[1], key, valid)
			}
		}
	}

	hints := 0
	for _, name := range commandNames() {
		owner := commandRegistry[name]
		help := owner.ShortHelp + "\n" + owner.LongHelp
		for _, m := range helpHintRe.FindAllStringSubmatch(help, -1) {
			words := strings.Fields(m[1])
			for len(words) >= 2 && words[0] == "--project" {
				words = words[2:]
			}
			if len(words) == 0 {
				continue
			}
			cmd := lookupCommand(words[0])
			if cmd == nil {
				continue // prose ("issue-cli is …"), not a command
			}
			hints++
			check(name, cmd, m[0], words[1:])
		}
		for _, m := range helpUsageRe.FindAllStringSubmatch(help, -1) {
			if lookupCommand(m[1]) != owner {
				continue
			}
			hints++
			check(name, owner, m[0], strings.Fields(m[2]))
		}
	}
	if hints < 50 {
		t.Fatalf("found only %d hints: the scan is broken", hints)
	}
}

// plainWordRe is a subcommand-shaped word: not a slug, placeholder or flag.
var plainWordRe = regexp.MustCompile(`^[a-z][a-z-]*$`)

// hintPath is the subcommand words a hint selects: the first word when it is
// one of cmd's Subcommands (or a SubAlias), then one more plain word for a
// nested one (`bench task add`, `job auto on`).
func hintPath(cmd *Command, words []string) []string {
	if len(words) == 0 {
		return nil
	}
	sub := words[0]
	if canon, ok := cmd.SubAliases[sub]; ok {
		sub = canon
	}
	if !slices.Contains(cmd.Subcommands, sub) {
		return nil
	}
	path := []string{sub}
	if len(words) > 1 && plainWordRe.MatchString(words[1]) {
		path = append(path, words[1])
	}
	return path
}

// hintFlags are the flags cmd defines on path, read in introspection mode
// like introspectFlags. With a nested word in path it tries the subcommand
// alone first and the nested one when that parses no flags.
func hintFlags(cmd *Command, path []string, issueDir string) []string {
	var flags []string
	if len(path) == 0 && hasFlagSubcommands(cmd) {
		// A usage line or hint without a subcommand: any of them.
		for _, s := range append([]string{""}, cmd.Subcommands...) {
			got, _ := introspectFlags(cmd, s, issueDir)
			flags = append(flags, got...)
		}
		return flags
	}
	for n := min(1, len(path)); n <= len(path); n++ {
		fs := introspectPath(cmd, path[:n], issueDir)
		if fs == nil {
			continue
		}
		fs.VisitAll(func(f *flag.Flag) { flags = append(flags, f.Name) })
		if len(flags) > 0 {
			break
		}
	}
	return flags
}

// introspectPath is introspectFlagSet with the subcommand words given.
func introspectPath(cmd *Command, path []string, issueDir string) (fs *flag.FlagSet) {
	ctx := &Context{
		Stdout:     io.Discard,
		Stderr:     io.Discard,
		Stdin:      strings.NewReader(""),
		Project:    &tracker.Project{Name: "introspect", Slug: "introspect", IssueDir: issueDir},
		Now:        time.Now,
		introspect: true,
	}
	defer func() {
		if got, ok := recover().(introspectedFlags); ok {
			fs = got.fs
		}
	}()
	_ = cmd.Run(ctx, append(slices.Clone(path), introspectArgs...))
	return nil
}

// docs/CLI/overview.md's "Adding a command" names every registry and guard
// test a new command must satisfy, and each name it gives still exists in
// this package, so the checklist can't point at a renamed registry.
func TestAddingACommandDocNamesTheRegistries(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI", "overview.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(doc)
	start := strings.Index(text, "\n### Adding a command\n")
	if start < 0 {
		t.Fatal(`docs/CLI/overview.md has no "### Adding a command" section`)
	}
	section := text[start+1:]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	var src strings.Builder
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src.Write(b)
	}
	for _, name := range []string{
		"commandAccess", "readSamples", "TestEveryCommandRejectsUnknownFlags",
		"TestEveryCommandHasAccess", "TestEveryCommandHelpNeedsNoProject",
		"TestEveryHelpHintNamesRealFlags", "parseFlags", "ExtraFlags",
		"runTraced", "resolveBootstrap", "WorkflowFile",
	} {
		if !strings.Contains(section, "`"+name) {
			t.Errorf(`"Adding a command" in docs/CLI/overview.md does not name %s`, name)
		}
		if !strings.Contains(src.String(), name) {
			t.Errorf("%s, named in the doc's checklist, is not in cmd/issue-cli any more", name)
		}
	}
}
