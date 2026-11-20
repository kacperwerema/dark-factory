package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// A new command joins several name-keyed lists. Each list used to have its
// own test naming only its own gap, and readSamples' test stayed quiet until
// commandAccess had the entry, so a worker found them one red run at a time.
// TestEveryCommandJoinsEveryRegistry names them all in one error per command.

// commandWideTests are the tests that run every registered command and fail
// only when one misbehaves (no list to join). The checklist's error names them
// in a -run line; TestCommandWideTestsListsEveryTestOverAllCommands keeps the
// list complete.
var commandWideTests = []string{
	"TestEveryCommandHelpNeedsNoProject",
	"TestEveryCommandRejectsUnknownFlags",
	"TestEveryHelpHintNamesRealFlags",
	"TestIntrospectFlagsEveryCommandWithoutSideEffects",
	"TestReadCommandsLeaveAnotherProjectUnchanged",
}

// notCommandWide loop over every command but are the list checks themselves,
// not behaviour a command must pass.
var notCommandWide = map[string]string{
	"TestEveryCommandJoinsEveryRegistry":                "the checklist itself",
	"TestCommandAccessNamesOnlyCommands":                "stale commandAccess entries",
	"TestReadSamplesCoverEveryReadCommand":              "checks the samples that exist",
	"TestCommandsTableNamesOnlyCommands":                "stale Commands-table rows",
	"TestCommandWideTestsListsEveryTestOverAllCommands": "this list's own check",
}

// registryLists is what a command is checked against: the lists keyed by
// command name.
type registryLists struct {
	access    map[string]readOnly
	samples   map[string][]string
	tableRows map[string]bool
}

// registryGaps names every list cmd is missing from, each with its file and
// what to add.
func registryGaps(cmd *Command, l registryLists) []string {
	var gaps []string
	rule, hasAccess := l.access[cmd.Name]
	if !hasAccess || rule == nil {
		gaps = append(gaps, "commandAccess in cmd/issue-cli/readonly.go: reads, writes, or readWithFlag(\"<flag>\") / a func over its subcommands; writes when unsure")
	}
	if _, ok := l.samples[cmd.Name]; !ok {
		switch {
		case !hasAccess || rule == nil:
			gaps = append(gaps, fmt.Sprintf("readSamples in cmd/issue-cli/readonly_test.go: one read-only call, e.g. %q: {%q} (leave it out if you mark %s writes)", cmd.Name, cmd.Name, cmd.Name))
		case !isWrites(rule):
			gaps = append(gaps, fmt.Sprintf("readSamples in cmd/issue-cli/readonly_test.go: one read-only call, e.g. %q: {%q}: commandAccess gives %s a read mode", cmd.Name, cmd.Name, cmd.Name))
		}
	}
	if !l.tableRows[cmd.Name] {
		gaps = append(gaps, fmt.Sprintf("a row in docs/CLI/overview.md's Commands table, worded from its --help short line (%q)", cmd.ShortHelp))
	}
	return gaps
}

// registryChecklist is the one error for a command with gaps.
func registryChecklist(cmd *Command, gaps []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "issue-cli %s is missing from %d list(s); add them all now:\n", cmd.Name, len(gaps))
	for _, g := range gaps {
		b.WriteString("  - " + g + "\n")
	}
	b.WriteString("  if it guards a worker verb (guardWho/guardAgent): that verb in guardVerbs (cmd/issue-cli/sandbox_pass.go); its sandbox rule is the owner's call, ask in Clarify\n")
	fmt.Fprintf(&b, "then run the tests every command must pass: go test ./cmd/issue-cli -run '^(%s)$'", strings.Join(commandWideTests, "|"))
	return b.String()
}

func TestEveryCommandJoinsEveryRegistry(t *testing.T) {
	l := registryLists{access: commandAccess, samples: readSamples, tableRows: commandsTableNames(t)}
	for _, name := range commandNames() {
		cmd := commandRegistry[name]
		if gaps := registryGaps(cmd, l); len(gaps) > 0 {
			t.Error(registryChecklist(cmd, gaps))
		}
	}
}

func TestRegistryChecklistNamesEveryGapAtOnce(t *testing.T) {
	cmd := &Command{Name: "frob", ShortHelp: "Frob an issue"}
	empty := registryLists{access: map[string]readOnly{}, samples: map[string][]string{}, tableRows: map[string]bool{}}
	gaps := registryGaps(cmd, empty)
	msg := registryChecklist(cmd, gaps)
	for _, want := range []string{
		"issue-cli frob is missing from 3 list(s)",
		"commandAccess in cmd/issue-cli/readonly.go",
		"readSamples in cmd/issue-cli/readonly_test.go",
		"leave it out if you mark frob writes",
		"docs/CLI/overview.md's Commands table",
		`"Frob an issue"`,
		"guardVerbs (cmd/issue-cli/sandbox_pass.go)",
		"go test ./cmd/issue-cli -run '^(TestEveryCommandHelpNeedsNoProject|",
		"TestEveryCommandRejectsUnknownFlags",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("checklist misses %q:\n%s", want, msg)
		}
	}

	written := registryLists{access: map[string]readOnly{"frob": writes}, samples: map[string][]string{}, tableRows: map[string]bool{"frob": true}}
	if gaps := registryGaps(cmd, written); len(gaps) != 0 {
		t.Errorf("a writes command with a table row needs no sample, got %q", gaps)
	}

	read := registryLists{access: map[string]readOnly{"frob": reads}, samples: map[string][]string{}, tableRows: map[string]bool{"frob": true}}
	gaps = registryGaps(cmd, read)
	if len(gaps) != 1 || !strings.HasPrefix(gaps[0], "readSamples in") || !strings.Contains(gaps[0], "gives frob a read mode") {
		t.Errorf("a reads command without a sample: want one readSamples gap, got %q", gaps)
	}
	read.samples["frob"] = []string{"frob"}
	if gaps := registryGaps(cmd, read); len(gaps) != 0 {
		t.Errorf("a command on every list: want no gaps, got %q", gaps)
	}
}

// commandWideLoops returns, from Go test sources, each Test func that loops
// over every command: calls commandNames() or registryCases(), or ranges
// over readSamples or commandAccess.
func commandWideLoops(t *testing.T, sources map[string]string) []string {
	t.Helper()
	fset := token.NewFileSet()
	found := map[string]bool{}
	for name, src := range sources {
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Body == nil || !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CallExpr:
					if id, ok := x.Fun.(*ast.Ident); ok && (id.Name == "commandNames" || id.Name == "registryCases") {
						found[fn.Name.Name] = true
					}
				case *ast.RangeStmt:
					if id, ok := x.X.(*ast.Ident); ok && (id.Name == "readSamples" || id.Name == "commandAccess") {
						found[fn.Name.Name] = true
					}
				}
				return true
			})
		}
	}
	var names []string
	for n := range found {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// commandWideProblems compares the loops found with commandWideTests.
func commandWideProblems(loops, listed []string, exist map[string]bool) []string {
	on := map[string]bool{}
	for _, n := range listed {
		on[n] = true
	}
	var problems []string
	for _, n := range loops {
		if !on[n] && notCommandWide[n] == "" {
			problems = append(problems, fmt.Sprintf("%s runs over every command but is not in commandWideTests (command_checklist_test.go): add it, so the checklist's -run line names it", n))
		}
	}
	for _, n := range listed {
		if !exist[n] {
			problems = append(problems, fmt.Sprintf("commandWideTests names %s, which is no test in cmd/issue-cli", n))
		}
	}
	return problems
}

func TestCommandWideTestsListsEveryTestOverAllCommands(t *testing.T) {
	files, _ := filepath.Glob("*_test.go")
	sources := map[string]string{}
	exist := map[string]bool{}
	fset := token.NewFileSet()
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		sources[f] = string(b)
		parsed, err := parser.ParseFile(fset, f, b, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range parsed.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil {
				exist[fn.Name.Name] = true
			}
		}
	}
	for _, p := range commandWideProblems(commandWideLoops(t, sources), commandWideTests, exist) {
		t.Error(p)
	}
}

func TestCommandWideProblemsNameANewLoopAndAGoneName(t *testing.T) {
	src := `package main
import "testing"
func TestAllCommandsFrob(t *testing.T) { for _, n := range commandNames() { _ = n } }
func TestAllSamplesBlink(t *testing.T) { for n := range readSamples { _ = n } }
func TestEveryCommandJoinsEveryRegistry(t *testing.T) { _ = commandNames() }
func TestOther(t *testing.T) { _ = lookupCommand("x") }
`
	loops := commandWideLoops(t, map[string]string{"x_test.go": src})
	if strings.Join(loops, ",") != "TestAllCommandsFrob,TestAllSamplesBlink,TestEveryCommandJoinsEveryRegistry" {
		t.Fatalf("loops = %q", loops)
	}
	exist := map[string]bool{"TestAllCommandsFrob": true, "TestAllSamplesBlink": true}
	problems := commandWideProblems(loops, []string{"TestAllSamplesBlink", "TestGone"}, exist)
	if len(problems) != 2 ||
		!strings.HasPrefix(problems[0], "TestAllCommandsFrob runs over every command but is not in commandWideTests") ||
		!strings.HasPrefix(problems[1], "commandWideTests names TestGone, which is no test") {
		t.Errorf("problems = %q", problems)
	}
}
