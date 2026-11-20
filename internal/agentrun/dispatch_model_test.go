package agentrun

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Every path that starts an issue's worker builds its spec with DispatchSpec
// (dispatch, queue start, auto-start, jobs) or TakeoverSpec (a takeover at a
// usage limit), and Start launches it on StartModel. Under this repo's own
// workflow.yaml a docs or report worker gets claude-sonnet-5-5 on each of
// them; a feature keeps the project's model. Report 1f67 read Opus tokens on
// docs and report workers: they were started before the routing change, or
// were the lead (factory/docs-and-report-workers-still-run-on-opus-although-workflow-yaml-routes-them-to-sonnet).
func TestDocsAndReportWorkersStartOnSonnetOnEveryPath(t *testing.T) {
	isolateTmux(t)
	subs := t.TempDir()
	orig := SubscriptionsDir
	SubscriptionsDir = func() string { return subs }
	t.Cleanup(func() { SubscriptionsDir = orig })

	wfFile, err := filepath.Abs(filepath.Join("..", "..", "workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	issues := filepath.Join(root, "issues")
	if err := os.MkdirAll(filepath.Join(issues, "Factory"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{"docs", "report", "feature"} {
		body := "---\ntitle: \"" + typ + " work\"\nstatus: \"idea\"\nsystem: \"Factory\"\ntype: \"" + typ + "\"\n---\n\nBody.\n"
		if err := os.WriteFile(filepath.Join(issues, "Factory", typ+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	proj := &tracker.Project{Slug: "acme", IssueDir: issues, WorkDir: root, WorkflowFile: wfFile, Terminal: "none"}
	all := mustLoad(t, issues)

	want := map[string]string{"docs": "claude-sonnet-5-5", "report": "claude-sonnet-5-5", "feature": ""}
	for typ, model := range want {
		issue := tracker.FindIssue(all, "factory/"+typ+"-work")
		if issue == nil {
			t.Fatalf("no %s issue", typ)
		}
		routed, refused := DispatchSpec(proj, issue, "", root, true)
		if refused != nil {
			t.Fatalf("%s: refused %+v", typ, refused)
		}
		taken := TakeoverSpec(proj, issue, Takeover{Slug: issue.Slug, From: "codex", To: "claude"}, root)
		for path, spec := range map[string]Spec{"dispatch": routed, "takeover": taken} {
			if spec.AgentType != "claude" || spec.Route == nil {
				t.Fatalf("%s %s: agent %q route %+v", typ, path, spec.AgentType, spec.Route)
			}
			if got := StartModel(proj, spec); got != model {
				t.Errorf("%s worker via %s starts on %q, want %q (route %+v)", typ, path, got, model, *spec.Route)
			}
		}
	}
}
