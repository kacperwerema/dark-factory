package tracker

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker/validations"
)

// WorkflowValidationRules is the one list of rules. These tests hold the
// copies that can't read it (the switches that run and describe a rule,
// validations.Registry, the docs) to it.

// packageSource parses this package's non-test files, and maps its
// top-level string constants to their values.
func packageSource(t *testing.T) (map[string]*ast.File, map[string]string) {
	t.Helper()
	fset := token.NewFileSet()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]*ast.File{}
	consts := map[string]string{}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files[path] = f
		for _, d := range f.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || g.Tok != token.CONST {
				continue
			}
			for _, s := range g.Specs {
				vs := s.(*ast.ValueSpec)
				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if v, err := strconv.Unquote(lit.Value); err == nil {
							consts[name.Name] = v
						}
					}
				}
			}
		}
	}
	return files, consts
}

// stringValue is x as a string: a literal or a constant of the package.
func stringValue(t *testing.T, x ast.Expr, consts map[string]string) string {
	t.Helper()
	switch x := x.(type) {
	case *ast.BasicLit:
		v, err := strconv.Unquote(x.Value)
		if err != nil {
			t.Fatal(err)
		}
		return v
	case *ast.Ident:
		if v, ok := consts[x.Name]; ok {
			return v
		}
		t.Fatalf("%s is not a string constant of the package", x.Name)
	}
	t.Fatalf("%T is neither a string literal nor a constant", x)
	return ""
}

// checkRuleCases is every rule name checkRule's switch has a case for.
func checkRuleCases(t *testing.T) []string {
	t.Helper()
	files, consts := packageSource(t)
	var out []string
	for _, f := range files {
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "checkRule" {
				continue
			}
			for _, stmt := range fn.Body.List {
				sw, ok := stmt.(*ast.SwitchStmt)
				if !ok {
					continue
				}
				for _, c := range sw.Body.List {
					for _, x := range c.(*ast.CaseClause).List {
						out = append(out, stringValue(t, x, consts))
					}
				}
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("found no case in checkRule's switch")
	}
	return out
}

// TestRuleRegistryIsComplete: every rule the code runs is in
// WorkflowValidationRules, and every rule there is run by something.
func TestRuleRegistryIsComplete(t *testing.T) {
	ran := map[string]bool{HandoffAnsweredRule: true} // checkValidateAction's own branch
	for name := range validations.Registry {
		ran[name] = true
	}
	for _, name := range checkRuleCases(t) {
		ran[name] = true
	}
	for name := range ran {
		if !KnownRule(name) {
			t.Errorf("rule %s runs but WorkflowValidationRules doesn't list it: add it to workflow_schema.go, with its description", name)
		}
	}
	for _, r := range WorkflowValidationRules {
		name, _ := SplitRule(r.Name)
		if !ran[name] {
			t.Errorf("WorkflowValidationRules lists %s, but neither checkRule nor validations.Registry runs it", name)
		}
	}
}

// TestEveryRuleHasASummary: process transitions and the previews say what a
// rule checks; the fallback prints the raw rule.
func TestEveryRuleHasASummary(t *testing.T) {
	for _, r := range WorkflowValidationRules {
		name, _ := SplitRule(r.Name)
		rule := r.Name
		if validations.Has(name) {
			rule = name
		}
		a := WorkflowAction{Type: "validate", Rule: rule}
		if got := DescribeAction(a, "done"); got == "Validate "+rule {
			t.Errorf("rule %s has no summary: add a case to validationSummary or structuredSummary", name)
		}
	}
}

// TestEveryRuleIsInTheDocs: docs/workflow.md names every rule.
func TestEveryRuleIsInTheDocs(t *testing.T) {
	raw, err := os.ReadFile("../../docs/workflow.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range WorkflowValidationRules {
		name, _ := SplitRule(r.Name)
		if !strings.Contains(string(raw), "`"+name) {
			t.Errorf("rule %s is not in docs/workflow.md", name)
		}
	}
}

// TestRulesTakingSince: the registry says which rules take a since date,
// and that is every rule the code reads one for.
func TestRulesTakingSince(t *testing.T) {
	files, consts := packageSource(t)
	read := map[string]bool{criteriaProvenRule: true} // parseProofSince, for the same words
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if fn, ok := call.Fun.(*ast.Ident); ok && fn.Name == "parseRuleSince" {
				if id, ok := call.Args[0].(*ast.Ident); ok && consts[id.Name] != "" {
					read[consts[id.Name]] = true
				}
			}
			return true
		})
	}
	var listed []string
	for _, r := range WorkflowValidationRules {
		name, _ := SplitRule(r.Name)
		if ruleTakesSince(name) {
			listed = append(listed, name)
			if !read[name] {
				t.Errorf("WorkflowValidationRules gives %s a since date, but nothing reads one", name)
			}
		}
	}
	for name := range read {
		if !ruleTakesSince(name) {
			t.Errorf("%s reads a since date, but its WorkflowValidationRules entry doesn't say \"%s: since <YYYY-MM-DD>\"", name, name)
		}
	}
	sort.Strings(listed)
	wf := &WorkflowConfig{}
	for _, name := range listed {
		wf.Transitions = append(wf.Transitions, WorkflowTransition{From: "a", To: "b", Actions: []WorkflowAction{{Type: "validate", Rule: name + ": since yesterday"}}})
	}
	if got := wf.lintRules(); len(got) != len(listed) {
		t.Errorf("lint on %d bad dates: %q", len(listed), got)
	}
}

func TestSplitRule(t *testing.T) {
	for _, tc := range []struct{ rule, name, arg string }{
		{"has_comment_prefix: tests:", "has_comment_prefix", "tests:"},
		{"body_not_empty", "body_not_empty", ""},
		{"  approved_for:done ", "approved_for", "done"},
		{"criteria_proven: since <YYYY-MM-DD>", "criteria_proven", "since <YYYY-MM-DD>"},
		{"", "", ""},
	} {
		if name, arg := SplitRule(tc.rule); name != tc.name || arg != tc.arg {
			t.Errorf("SplitRule(%q) = (%q, %q), want (%q, %q)", tc.rule, name, arg, tc.name, tc.arg)
		}
	}
}

func TestKnownRule(t *testing.T) {
	for rule, want := range map[string]bool{
		"children_closed": true, "approved_for: done": true, "approved_for:done": true,
		"has_section": true, "has_vibes": false, "has_vibes: arg": false, "": false,
	} {
		if got := KnownRule(rule); got != want {
			t.Errorf("KnownRule(%q) = %v, want %v", rule, got, want)
		}
	}
}

// A rule written without the space after its colon means the same to the
// transition as to lint and the station editor.
func TestCheckRuleReadsARuleWithoutTheSpace(t *testing.T) {
	wf := &WorkflowConfig{}
	issue := &Issue{Slug: "x", HumanApproval: "done"}
	if err := wf.checkRule("approved_for:done", issue, nil); err != nil {
		t.Errorf("approved_for:done: %v", err)
	}
}

// ruleSplitRe finds a hand split of a rule string, the kind SplitRule
// replaced: a Cut, Index or SplitN on a value named rule, on a colon.
var ruleSplitRe = regexp.MustCompile(`strings\.(Cut|Index|SplitN|HasPrefix|TrimPrefix)\([^,\n]*[Rr]ule[^,\n]*, *"[^"]*:`)

// TestRuleSplitHasOneSource fails on a new hand split of a rule string.
func TestRuleSplitHasOneSource(t *testing.T) {
	for _, root := range []string{"../../internal", "../../cmd"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			if filepath.Base(path) == "rule_name.go" {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(raw), "\n") {
				if ruleSplitRe.MatchString(line) {
					t.Errorf("%s:%d splits a rule by hand, use tracker.SplitRule: %s", path, i+1, strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(fmt.Errorf("walk %s: %w", root, err))
		}
	}
}
