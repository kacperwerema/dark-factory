package tracker

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// statusNames are the bundled lifecycle's names that mean something. done
// and parked are the closed guard's (TestNoClosedStatusLiteralsOutsideTracker).
var statusNames = map[string]bool{
	"idea": true, "in design": true, "backlog": true, "in progress": true,
	"testing": true, "human-testing": true, "documentation": true, "shipping": true,
}

// statusLiteralFindings is each string literal in src equal to a status
// name, as "name" with its line. Imports are not literals here.
func statusLiteralFindings(t *testing.T, path string, src any) map[string][]int {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]int{}
	ast.Inspect(file, func(n ast.Node) bool {
		if _, ok := n.(*ast.ImportSpec); ok {
			return false
		}
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if s, err := strconv.Unquote(lit.Value); err == nil && statusNames[s] {
			out[s] = append(out[s], fset.Position(lit.Pos()).Line)
		}
		return true
	})
	return out
}

func TestNoStatusNameLiterals(t *testing.T) {
	root := filepath.Join("..", "..")
	var scanned int
	var bad []string
	stale := map[string]bool{}
	for k := range notAStatusMeaning {
		stale[k] = true
	}
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			if rel == "internal/tracker/status_role.go" {
				return nil
			}
			scanned++
			for name, lines := range statusLiteralFindings(t, path, nil) {
				key := rel + "|" + name
				if _, ok := notAStatusMeaning[key]; ok {
					delete(stale, key)
					continue
				}
				for _, line := range lines {
					bad = append(bad, rel+":"+strconv.Itoa(line)+": "+strconv.Quote(name))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if scanned < 100 {
		t.Fatalf("scanned only %d files: the walk is broken", scanned)
	}
	sort.Strings(bad)
	for _, b := range bad {
		t.Errorf("%s: code that knows a status by its name loses that when it's renamed: ask WorkflowConfig.HasRole or RoleStatus (status_role.go); not a status, or display only? add it to notAStatusMeaning with why", b)
	}
	for k := range stale {
		t.Errorf("notAStatusMeaning: %s no longer occurs: remove it", k)
	}
}

// notAStatusMeaning is every literal the guard lets stand, "<file>|<name>",
// with why: it isn't a status at all, or it only picks a word to show and
// the status's own name is the fallback.
var notAStatusMeaning = map[string]string{
	"cmd/issue-cli/cmd_create.go|idea":                "the first status's stand-in, only when the workflow lists none",
	"internal/health/recommend.go|idea":               "the first status's stand-in, only when the workflow lists none",
	"internal/tracker/issue.go|idea":                  "CreateIssueFileOpts's default for a caller with no workflow",
	"internal/viewer/github.go|idea":                  "the first status's stand-in, only when the workflow lists none",
	"internal/bench/task.go|in progress":              "DefaultStart: the name --start's help gives; NewTask asks the work role first",
	"cmd/issue-cli/cmd_help.go|testing":               "a help topic, not a status",
	"cmd/issue-cli/cmd_process.go|testing":            "a process topic, not a status",
	"cmd/issue-cli/cmd_process.go|documentation":      "an alias of the docs topic, not a status",
	"cmd/issue-cli/cmd_queue.go|idea":                 "the --idea flag, not a status",
	"cmd/issue-cli/cmd_next.go|backlog":               "a key of next --json, a stable output contract",
	"cmd/issue-cli/cmd_next.go|testing":               "a key of next --json, a stable output contract",
	"internal/viewer/report.go|testing":               "a data-testid, not a status",
	"internal/viewer/stale_text.go|testing":           "an issue section heading, not a status",
	"internal/viewer/stale_text.go|documentation":     "an issue section heading, not a status",
	"internal/viewer/stale_text.go|shipping":          "an issue section heading, not a status",
	"internal/viewer/company_floor.go|documentation":  "display: a shorter word for the strip, the name is the fallback",
	"internal/viewer/company_floor.go|shipping":       "display: a shorter word for the strip, the name is the fallback",
	"internal/viewer/company_floor.go|in design":      "display: a shorter word for the strip, the name is the fallback",
	"internal/viewer/issue_tab.go|idea":               "display: gateNoun's word, the name is the fallback",
	"internal/viewer/issue_tab.go|in design":          "display: gateNoun's word, the name is the fallback",
	"internal/viewer/issue_tab.go|backlog":            "display: gateNoun's word, the name is the fallback",
	"internal/viewer/issue_tab.go|in progress":        "display: gateNoun's word, the name is the fallback",
	"internal/viewer/issue_tab.go|testing":            "display: gateNoun's word, the name is the fallback",
	"internal/viewer/issue_tab.go|human-testing":      "display: gateNoun's word, the name is the fallback",
	"internal/viewer/issue_tab.go|documentation":      "display: gateNoun's word, the name is the fallback",
	"internal/viewer/issue_tab.go|shipping":           "display: gateNoun's word, the name is the fallback",
	"internal/viewer/template_funcs.go|idea":          "display: statusColor's palette, a default colour is the fallback",
	"internal/viewer/template_funcs.go|backlog":       "display: statusColor's palette, a default colour is the fallback",
	"internal/viewer/template_funcs.go|testing":       "display: statusColor's palette, a default colour is the fallback",
	"internal/viewer/template_funcs.go|human-testing": "display: statusColor's palette, a default colour is the fallback",
	"internal/viewer/template_funcs.go|documentation": "display: statusColor's palette, a default colour is the fallback",
	"internal/viewer/template_funcs.go|shipping":      "display: statusColor's palette, a default colour is the fallback",
}

// TestStatusLiteralGuardCatches proves the guard sees each way of writing a
// status name, and leaves imports and other strings alone.
func TestStatusLiteralGuardCatches(t *testing.T) {
	src := `package x
import "testing"
var _ testing.T
var m = map[string]int{"backlog": 1}
const c = "in design"
func f(s string, wf interface{ GetStatusIndex(string) int }) bool {
	switch s {
	case "testing":
	}
	return s == "human-testing" || wf.GetStatusIndex("shipping") > 0 || s == "not-a-status"
}`
	got := statusLiteralFindings(t, "x.go", src)
	for _, name := range []string{"backlog", "in design", "human-testing", "shipping"} {
		if len(got[name]) != 1 {
			t.Errorf("%s: found on lines %v, want one line", name, got[name])
		}
	}
	if len(got["testing"]) != 1 || got["testing"][0] != 8 {
		t.Errorf("testing: lines %v, want only the case on line 8: an import is not a finding", got["testing"])
	}
	if len(got) != 5 {
		t.Errorf("findings = %v, want 5 names", got)
	}
}
