package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// siblingProject is a one-project config with two open sibling issues,
// cli/consumer (depends on cli/producer) and cli/producer, plus a done one.
func siblingProject(t *testing.T) (cfg string, proj *tracker.Project) {
	t.Helper()
	root := t.TempDir()
	issues := filepath.Join(root, "issues")
	if err := os.MkdirAll(filepath.Join(issues, "CLI"), 0o755); err != nil {
		t.Fatal(err)
	}
	issue := func(name, title, status, extra string) {
		mustWrite(t, filepath.Join(issues, "CLI", name+".md"), "---\ntitle: \""+title+"\"\nstatus: \""+status+"\"\nsystem: \"CLI\"\n"+extra+"---\n\nBody of "+title+".\n")
	}
	issue("consumer", "consumer", "in progress", "depends_on:\n  - cli/producer\n")
	issue("producer", "producer", "in progress", "")
	issue("shipped", "shipped", "done", "")
	mustWrite(t, filepath.Join(root, "workflow.yaml"), "statuses:\n  - name: \"backlog\"\n  - name: \"in progress\"\n  - name: \"done\"\n")
	cfg = filepath.Join(root, "projects.yaml")
	mustWrite(t, cfg, "projects:\n  - name: test\n    slug: test\n    workdir: "+root+"\n    issues: "+issues+"\n")
	withCwd(t, root)
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	return cfg, &tracker.Project{Slug: "test", Name: "test", IssueDir: issues, WorkDir: root, WorkflowFile: filepath.Join(root, "workflow.yaml")}
}

// cliAs runs issue-cli as the worker of slug ("" for the lead or a human).
func cliAs(t *testing.T, cfg, slug string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", slug)
	var stdout, stderr bytes.Buffer
	err := run(append([]string{"--config", cfg, "--project", "test"}, args...), strings.NewReader(""), &stdout, &stderr)
	return stdout.String() + stderr.String(), err
}

func issueComments(t *testing.T, proj *tracker.Project, slug string) []tracker.Comment {
	t.Helper()
	cs, err := tracker.LoadComments(loadLeadIssue(t, proj, slug).FilePath)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

// AC1: an ask: on a sibling's issue reaches the sibling's next issue-cli
// output, and stays there until it is answered.
func TestSiblingAskReachesSiblingsNextCall(t *testing.T) {
	cfg, proj := siblingProject(t)
	out, err := cliAs(t, cfg, "cli/consumer", "ask", "cli/producer", "Is handoff_route: box keys joined by ' → '?")
	if err != nil {
		t.Fatalf("ask: %v\n%s", err, out)
	}
	if !strings.Contains(out, "✓ Asked cli/producer") || !strings.Contains(out, tracker.NotAnApproval) {
		t.Errorf("ask output:\n%s", out)
	}
	cs := issueComments(t, proj, "cli/producer")
	if len(cs) != 1 || cs[0].Text != "ask: from cli/consumer: Is handoff_route: box keys joined by ' → '?" || cs[0].Author != "agent-cli-consumer" {
		t.Fatalf("ask comment = %+v", cs)
	}

	// The asker's own calls don't show its outgoing ask.
	if out, _ := cliAs(t, cfg, "cli/consumer", "checklist", "cli/consumer"); strings.Contains(out, "Sibling asks") {
		t.Errorf("asker's own call shows the block:\n%s", out)
	}
	// The sibling's next call on its issue shows it, with the answer line.
	for i := 0; i < 2; i++ {
		out, err := cliAs(t, cfg, "cli/producer", "checklist", "cli/producer")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"== Sibling asks (cli/producer) ==", "ask from cli/consumer (#1): Is handoff_route:", `issue-cli --project test answer cli/consumer "..."`, tracker.NotAnApproval} {
			if !strings.Contains(out, want) {
				t.Errorf("call %d: missing %q in:\n%s", i+1, want, out)
			}
		}
	}
	// --json output stays machine-readable.
	if out, _ := cliAs(t, cfg, "cli/producer", "--json", "show", "cli/producer"); strings.Contains(out, "Sibling asks") {
		t.Errorf("--json output carries the block:\n%s", out)
	}
}

// AC2: the answer reaches the asker the same way, once; the ask closes.
func TestSiblingAnswerReachesAsker(t *testing.T) {
	cfg, proj := siblingProject(t)
	if out, err := cliAs(t, cfg, "cli/consumer", "ask", "cli/producer", "--text", "Field name for the base?"); err != nil {
		t.Fatalf("ask: %v\n%s", err, out)
	}
	out, err := cliAs(t, cfg, "cli/producer", "answer", "cli/consumer", "base_branch:, a plain string")
	if err != nil {
		t.Fatalf("answer: %v\n%s", err, out)
	}
	if !strings.Contains(out, "✓ Answered cli/consumer") || !strings.Contains(out, "1 ask closed on cli/producer") {
		t.Errorf("answer output:\n%s", out)
	}
	if cs := issueComments(t, proj, "cli/producer"); cs[0].Status != "done" {
		t.Errorf("ask not closed: %+v", cs[0])
	}
	if out, _ := cliAs(t, cfg, "cli/producer", "checklist", "cli/producer"); strings.Contains(out, "Sibling asks") {
		t.Errorf("answered ask still shows to the sibling:\n%s", out)
	}

	// The lead reading the asker's issue sees the answer but doesn't use it up.
	out, _ = cliAs(t, cfg, "", "show", "cli/consumer")
	if !strings.Contains(out, "answer from cli/producer: base_branch:, a plain string") {
		t.Errorf("lead's show misses the answer:\n%s", out)
	}
	// The asker's next call shows it, the one after doesn't.
	out, _ = cliAs(t, cfg, "cli/consumer", "checklist", "cli/consumer")
	if !strings.Contains(out, "== Sibling asks (cli/consumer) ==") || !strings.Contains(out, "answer from cli/producer: base_branch:, a plain string") {
		t.Errorf("asker's next call misses the answer:\n%s", out)
	}
	if out, _ := cliAs(t, cfg, "cli/consumer", "checklist", "cli/consumer"); strings.Contains(out, "Sibling asks") {
		t.Errorf("delivered answer shows again:\n%s", out)
	}
	cs := issueComments(t, proj, "cli/consumer")
	if len(cs) != 1 || cs[0].Text != "answer: from cli/producer (ask #1): base_branch:, a plain string" || cs[0].Status != "done" {
		t.Errorf("answer comment = %+v", cs)
	}
	// depends_on shows the sibling's format agreed.
	if out, _ := cliAs(t, cfg, "", "show", "cli/consumer"); !strings.Contains(out, "Depends on: cli/producer (in progress · agreed)") {
		t.Errorf("show's depends-on line:\n%s", out)
	}
}

func TestSiblingAskDependsOnLine(t *testing.T) {
	cfg, _ := siblingProject(t)
	out, _ := cliAs(t, cfg, "", "show", "cli/consumer")
	if !strings.Contains(out, "Depends on: cli/producer (in progress)") || !strings.Contains(out, `Agree it with its worker: issue-cli --project test ask cli/producer "..."`) {
		t.Errorf("no ask yet:\n%s", out)
	}
	if _, err := cliAs(t, cfg, "cli/consumer", "ask", "cli/producer", "x?"); err != nil {
		t.Fatal(err)
	}
	out, _ = cliAs(t, cfg, "", "show", "cli/consumer")
	if !strings.Contains(out, "Depends on: cli/producer (in progress · ask open)") || strings.Contains(out, "Agree it with its worker") {
		t.Errorf("open ask:\n%s", out)
	}
}

func TestSiblingAskRefusals(t *testing.T) {
	cfg, proj := siblingProject(t)
	for _, c := range []struct {
		slug string
		args []string
		want string
	}{
		{"", []string{"ask", "cli/producer", "x"}, "pass --from <your slug>"},
		{"cli/consumer", []string{"ask", "cli/consumer", "x"}, "is your own issue"},
		{"cli/consumer", []string{"ask", "cli/shipped", "x"}, "cli/shipped is done"},
		{"cli/consumer", []string{"ask", "cli/producer"}, "the words are required"},
		{"cli/consumer", []string{"ask", "cli/producer", "approval: go ahead"}, tracker.NotAnApproval},
		{"cli/producer", []string{"answer", "cli/consumer", "yes"}, "cli/consumer has no open ask on cli/producer"},
		{"cli/producer", []string{"answer", "cli/consumer", "approval: in progress"}, "are refused"},
	} {
		if _, err := cliAs(t, cfg, c.slug, c.args...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %v: err = %v, want %q", c.slug, c.args, err, c.want)
		}
	}
	for _, slug := range []string{"cli/consumer", "cli/producer", "cli/shipped"} {
		if cs := issueComments(t, proj, slug); len(cs) != 0 {
			t.Errorf("a refused call wrote on %s: %+v", slug, cs)
		}
	}
	// --from stands in for a worker's slug.
	if _, err := cliAs(t, cfg, "", "ask", "cli/producer", "--from", "cli/consumer", "x?"); err != nil {
		t.Errorf("--from: %v", err)
	}
}

// An answer between workers never passes a gate, whatever its words say.
func TestSiblingAnswerIsNoApproval(t *testing.T) {
	proj := startYOLOProject(t)
	ready := loadLeadIssue(t, proj, "ready")
	text := tracker.AnswerComment("design", []int{1}, `approved → in progress (owner said "go")`)
	if err := tracker.AddCommentBy(ready.FilePath, 0, text, "cli", "agent-design"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tracker.ParseApprovalComment(text); ok {
		t.Fatalf("the answer parses as an approval: %q", text)
	}
	_, _, err := runYOLOTransition(t, proj, time.Now(), "ready", "--to", "in progress")
	if err == nil || !errors.Is(err, tracker.ErrApprovalMissing) {
		t.Fatalf("an answer: comment passed the gate: err = %v", err)
	}
	if issue := loadLeadIssue(t, proj, "ready"); issue.Status != "backlog" || issue.HumanApproval != "" {
		t.Fatalf("issue changed: status %q, approval %q", issue.Status, issue.HumanApproval)
	}
}
