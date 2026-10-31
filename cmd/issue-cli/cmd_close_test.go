package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// makeCloseProject has a dup at idea, the issue it duplicates, a third one,
// a done one, and a workflow whose parked status every status can move to
// with a required reason, as the bundled template has it.
func makeCloseProject(t *testing.T) *tracker.Project {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	issue := func(title, status string) string {
		return "---\ntitle: \"" + title + "\"\nstatus: \"" + status + "\"\n---\n\n## Acceptance criteria\n- [ ] owner box of " + title + "\n"
	}
	write("issues/CLI/dup.md", issue("Dup", "idea"))
	write("issues/CLI/main.md", issue("Main", "in progress"))
	write("issues/CLI/other.md", issue("Other", "in progress"))
	write("issues/CLI/shipped.md", issue("Shipped", "done"))
	write("workflow.yaml", `statuses:
  - name: "idea"
  - name: "in progress"
  - name: "done"
  - name: "parked"
    optional: true
    global: true
transitions:
  - from: "idea"
    to: "in progress"
  - from: "in progress"
    to: "done"
  - from: "*"
    to: "parked"
    fields:
      - name: "parked_reason"
        prompt: "Why?"
        target: "section:Parked Record"
        type: "multiline"
        required: true
`)
	return &tracker.Project{Slug: "proj", Name: "Proj", IssueDir: filepath.Join(root, "issues"), WorkDir: root, WorkflowFile: filepath.Join(root, "workflow.yaml")}
}

func closeComments(t *testing.T, issue *tracker.Issue) string {
	t.Helper()
	comments, err := tracker.LoadComments(issue.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, c := range comments {
		b.WriteString(c.Text + " [" + c.Author + "]\n")
	}
	return b.String()
}

// frontmatterLine reads key's value from the issue file, unquoted.
func frontmatterLine(t *testing.T, issue *tracker.Issue, key string) string {
	t.Helper()
	data, err := os.ReadFile(issue.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, key+":"); ok {
			return strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return ""
}

// AC1, AC4: the worker of cli/main parks its idea-stage duplicate in one
// step; both issues link the other and no checkbox changes.
func TestClose_WorkerParksDuplicateOfItsIssue(t *testing.T) {
	proj := makeCloseProject(t)
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "cli/main")

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runClose(ctx, []string{"cli/dup", "--duplicate-of", "cli/main"}); err != nil {
		t.Fatalf("close: %v", err)
	}
	dup := loadLeadIssue(t, proj, "cli/dup")
	if dup.Status != "parked" {
		t.Fatalf("status = %q, want parked", dup.Status)
	}
	if got := frontmatterLine(t, dup, "duplicate_of"); got != "cli/main" {
		t.Errorf("duplicate_of = %q", got)
	}
	if !strings.Contains(dup.BodyRaw, "## Parked Record") || !strings.Contains(dup.BodyRaw, "Duplicate of cli/main.") {
		t.Errorf("parked reason missing:\n%s", dup.BodyRaw)
	}
	if !strings.Contains(dup.BodyRaw, "- [ ] owner box of Dup") {
		t.Errorf("dup's checkbox changed:\n%s", dup.BodyRaw)
	}
	if got := closeComments(t, dup); !strings.Contains(got, "closed: duplicate of cli/main (was idea) [agent-cli-main]") {
		t.Errorf("dup comments = %q", got)
	}
	main := loadLeadIssue(t, proj, "cli/main")
	if got := closeComments(t, main); !strings.Contains(got, "closed: cli/dup as duplicate of this issue [agent-cli-main]") {
		t.Errorf("main comments = %q", got)
	}
	if main.Status != "in progress" || !strings.Contains(main.BodyRaw, "- [ ] owner box of Main") {
		t.Errorf("main changed: %s\n%s", main.Status, main.BodyRaw)
	}
	if out := stdout.String(); !strings.Contains(out, "cli/dup: idea → parked (duplicate of cli/main)") ||
		!strings.Contains(out, `Undo: issue-cli --project proj transition cli/dup --to "idea"`) {
		t.Errorf("stdout = %q", out)
	}
}

// AC2: any other worker is refused with the rule, and nothing changes.
func TestClose_OtherWorkerRefused(t *testing.T) {
	proj := makeCloseProject(t)
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	before, _ := os.ReadFile(loadLeadIssue(t, proj, "cli/dup").FilePath)

	for _, me := range []string{"cli/other", "cli/dup"} {
		t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", me)
		ctx, _, _ := newTestContext(proj, false)
		err := runClose(ctx, []string{"cli/dup", "--duplicate-of", "cli/main"})
		if err == nil || !strings.Contains(err.Error(), "close cli/dup changed nothing: "+tracker.CloseRule) ||
			!strings.Contains(err.Error(), `issue-cli --project proj comment `+me+` "question: ..."`) {
			t.Errorf("worker of %s: err = %v", me, err)
		}
	}
	if after, _ := os.ReadFile(loadLeadIssue(t, proj, "cli/dup").FilePath); string(after) != string(before) {
		t.Fatalf("a refused close changed dup:\n%s", after)
	}
	if got := closeComments(t, loadLeadIssue(t, proj, "cli/main")); got != "" {
		t.Errorf("a refused close commented on main: %q", got)
	}
}

// The lead closes any issue; --fixed-by works like --duplicate-of.
func TestClose_LeadFixedBy(t *testing.T) {
	proj := makeCloseProject(t)
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "project")

	ctx, _, _ := newTestContext(proj, false)
	if err := runClose(ctx, []string{"cli/other", "--fixed-by", "cli/main"}); err != nil {
		t.Fatalf("close: %v", err)
	}
	other := loadLeadIssue(t, proj, "cli/other")
	if other.Status != "parked" || frontmatterLine(t, other, "fixed_by") != "cli/main" {
		t.Fatalf("status = %q fixed_by = %q", other.Status, frontmatterLine(t, other, "fixed_by"))
	}
	if got := closeComments(t, loadLeadIssue(t, proj, "cli/main")); !strings.Contains(got, "closed: cli/other as fixed by this issue") {
		t.Errorf("main comments = %q", got)
	}
}

// AC5: refusals change nothing.
func TestClose_Refusals(t *testing.T) {
	proj := makeCloseProject(t)
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "project")
	snapshot := func() string {
		var b strings.Builder
		for _, s := range []string{"cli/dup", "cli/main", "cli/other", "cli/shipped"} {
			data, _ := os.ReadFile(loadLeadIssue(t, proj, s).FilePath)
			b.Write(data)
		}
		return b.String()
	}
	before := snapshot()

	cases := []struct {
		args []string
		want string
	}{
		{[]string{"cli/dup"}, "close needs --duplicate-of <slug> or --fixed-by <slug>"},
		{[]string{"cli/dup", "--duplicate-of", "cli/main", "--fixed-by", "cli/other"}, "not both"},
		{[]string{"cli/dup", "--duplicate-of", "cli/dup"}, "can't be a duplicate of itself"},
		{[]string{"cli/dup", "--duplicate-of", "cli/nope"}, "cli/nope"},
		{[]string{"cli/shipped", "--duplicate-of", "cli/main"}, "close cli/shipped changed nothing: it is already done"},
	}
	for _, c := range cases {
		ctx, _, _ := newTestContext(proj, false)
		err := runClose(ctx, c.args)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: err = %v, want %q", c.args, err, c.want)
		}
	}
	if snapshot() != before {
		t.Fatal("a refused close changed an issue")
	}

	ctx, _, _ := newTestContext(proj, false)
	if err := runClose(ctx, []string{"cli/dup", "--duplicate-of", "cli/main"}); err != nil {
		t.Fatal(err)
	}
	ctx, _, _ = newTestContext(proj, false)
	if err := runClose(ctx, []string{"cli/dup", "--duplicate-of", "cli/main"}); err == nil || !strings.Contains(err.Error(), "it is already parked") {
		t.Errorf("second close: err = %v", err)
	}
}

// A worker refused a transition to parked on another issue is pointed at close.
func TestTransition_WorkerParkingAnotherIssuePointsAtClose(t *testing.T) {
	proj := makeCloseProject(t)
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "cli/main")
	ctx, _, _ := newTestContext(proj, false)
	err := runTransition(ctx, []string{"cli/dup", "--to", "parked", "--field", "parked_reason=dup"})
	if err == nil || !strings.Contains(err.Error(), "issue-cli --project proj close cli/dup --duplicate-of <your slug>") {
		t.Fatalf("err = %v", err)
	}
}
