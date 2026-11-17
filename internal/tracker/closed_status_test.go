package tracker

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func statuses(names ...string) *WorkflowConfig {
	w := &WorkflowConfig{}
	for _, n := range names {
		st := WorkflowStatus{Name: n}
		if strings.HasSuffix(n, "?") {
			st.Name, st.Optional = strings.TrimSuffix(n, "?"), true
		}
		w.Statuses = append(w.Statuses, st)
	}
	return w
}

func TestIsClosedStatus(t *testing.T) {
	dev := statuses("idea", "shipping", "done", "parked?")
	obsolete := statuses("idea", "done", "obsolete")
	published := statuses("brief", "published", "parked?")
	archived := statuses("inbox", "approved", "archived")
	sideInLine := statuses("idea", "blocked?", "shipping", "done")
	tests := []struct {
		w      *WorkflowConfig
		status string
		want   bool
	}{
		{dev, "done", true},
		{dev, " Done ", true},
		{dev, "parked", true},
		{dev, "Parked", true},
		{dev, "shipping", false},
		{dev, "", false},
		{dev, "whatever", false},
		// Not listed: only done and parked are known.
		{dev, "archived", false},
		// A workflow with no parked still knows it.
		{statuses("idea", "done"), "parked", true},
		// Side exits after done close.
		{obsolete, "obsolete", true},
		// A file ending elsewhere: its end closes, optional ones after it too.
		{published, "published", true},
		{published, "brief", false},
		{archived, "archived", true},
		{archived, "approved", false},
		// An optional status inside the line is not a side exit.
		{sideInLine, "blocked", false},
		{nil, "done", true},
		{nil, "parked", true},
		{nil, "in progress", false},
		{&WorkflowConfig{}, "done", true},
		{&WorkflowConfig{}, "idea", false},
	}
	for _, tt := range tests {
		if got := IsClosedStatus(tt.w, tt.status); got != tt.want {
			t.Errorf("IsClosedStatus(%+v, %q) = %v, want %v", tt.w, tt.status, got, tt.want)
		}
	}
}

func TestIsLastStatusIsTheEndNotASideExit(t *testing.T) {
	published := statuses("brief", "published", "parked?")
	if !published.IsLastStatus("published") {
		t.Error("published ends the file even with parked after it")
	}
	if published.IsLastStatus("parked") {
		t.Error("parked is closed, not finished")
	}
	dev := statuses("idea", "done", "parked?")
	if !dev.IsLastStatus("Done") || dev.IsLastStatus("parked") || dev.IsLastStatus("idea") {
		t.Errorf("done must be the only end of %v", dev.GetStatusOrder())
	}
	var none *WorkflowConfig
	if none.IsLastStatus("idea") || !none.IsLastStatus("done") {
		t.Error("nil workflow knows only done")
	}
}

// MergeStatus is the last non-optional status before the end.
func TestMergeStatusIsTheStepBeforeTheEnd(t *testing.T) {
	for _, c := range []struct {
		w    *WorkflowConfig
		want string
	}{
		{statuses("idea", "testing", "shipping", "done", "parked?"), "shipping"},
		{statuses("idea", "shipping", "documentation?", "done"), "shipping"},
		{statuses("brief", "review", "published", "parked?"), "review"},
		{statuses("done"), ""},
		{nil, ""},
	} {
		if got := c.w.MergeStatus(); got != c.want {
			t.Errorf("%v: MergeStatus = %q, want %q", c.w.GetStatusOrder(), got, c.want)
		}
	}
}

// closedLiterals are the words a closed-status list was written with.
var closedLiterals = map[string]bool{"done": true, "parked": true, "closed": true, "archived": true, "obsolete": true}

// notIssueStatuses are comparisons with "done" or "parked" that aren't about
// an issue's status. file|expression, with why.
var notIssueStatuses = map[string]string{
	"cmd/issue-cli/cmd_decision.go|cm.Status":      "a comment's state",
	"cmd/issue-cli/cmd_show.go|c.Status":           "a comment's state",
	"cmd/issue-cli/cmd_comments.go|c.Status":       "a comment's state",
	"internal/redrounds/redrounds.go|c.Status":     "a comment's state",
	"internal/viewer/station.go|st.State":          "a belt step's state",
	"internal/viewer/systems_wb.go|c.State":        "a crate's state",
	"internal/viewer/connections_view.go|it.State": "a handoff's state",
}

// TestNoClosedStatusLiteralsOutsideTracker keeps closed statuses in one
// place: outside this package nobody compares a status with "done" or
// "parked", or lists closing words in a case, whatever the case folding.
// Ask IsClosedStatus, IsLastStatus, IsParkedStatus, Project.IssueDone or
// Project.IssueClosed. The test parses the sources rather than grepping them,
// so a rewrapped line can't slip by.
func TestNoClosedStatusLiteralsOutsideTracker(t *testing.T) {
	root := filepath.Join("..", "..")
	var scanned int
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path == filepath.Join(root, "internal", "tracker") || d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			scanned++
			for _, bad := range closedStatusFindings(t, path) {
				if _, ok := notIssueStatuses[filepath.ToSlash(rel)+"|"+bad.expr]; ok {
					continue
				}
				t.Errorf("%s:%d: %s: ask tracker.IsClosedStatus, IsLastStatus, IsParkedStatus or Project.IssueDone instead (not an issue status? add it to notIssueStatuses with why)", filepath.ToSlash(rel), bad.line, bad.expr)
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
}

// TestClosedStatusGuardCatches proves the guard sees each way of writing it.
func TestClosedStatusGuardCatches(t *testing.T) {
	src := `package x
import "strings"
func f(is struct{Status string}, to string) {
	switch is.Status { case "done", "parked": }
	switch is.Status { case "parked": }
	_ = is.Status == "done"
	_ = "parked" != to
	_ = strings.EqualFold(is.Status, "done")
	switch to { case "done": }
}`
	path := filepath.Join(t.TempDir(), "x.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	got := closedStatusFindings(t, path)
	if len(got) != 5 {
		t.Fatalf("findings = %+v, want 5 (a lone case \"done\" is a subcommand's)", got)
	}
}

type closedFinding struct {
	line int
	expr string
}

func closedStatusFindings(t *testing.T, path string) []closedFinding {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	literal := func(e ast.Expr) string {
		lit, ok := e.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return ""
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			return ""
		}
		return s
	}
	text := func(e ast.Expr) string {
		var b strings.Builder
		_ = printer.Fprint(&b, fset, e)
		return b.String()
	}
	status := func(s string) bool { return s == "done" || s == "parked" }
	var out []closedFinding
	report := func(at token.Pos, expr string) {
		out = append(out, closedFinding{fset.Position(at).Line, expr})
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BinaryExpr:
			if n.Op != token.EQL && n.Op != token.NEQ {
				return true
			}
			if status(literal(n.Y)) {
				report(n.Pos(), text(n.X))
			} else if status(literal(n.X)) {
				report(n.Pos(), text(n.Y))
			}
		case *ast.CallExpr:
			sel, ok := n.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "EqualFold" || len(n.Args) != 2 {
				return true
			}
			if status(literal(n.Args[1])) {
				report(n.Pos(), text(n.Args[0]))
			} else if status(literal(n.Args[0])) {
				report(n.Pos(), text(n.Args[1]))
			}
		case *ast.CaseClause:
			// A lone "done" is a subcommand or a filter word; "parked", or
			// "done" beside another closing word, is a closed list.
			words := 0
			parked := false
			for _, e := range n.List {
				if s := literal(e); closedLiterals[s] {
					words++
					parked = parked || s == "parked"
				}
			}
			if parked || words > 1 {
				report(n.Pos(), "case list")
			}
		}
		return true
	})
	return out
}
