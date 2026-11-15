package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// realTempDir is t.TempDir() with symlinks resolved. On macOS the temp dir
// sits under /var, a link to /private/var; git and os.Getwd print the
// resolved path, so a test that compares printed paths needs this one.
func realTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// newTestContext builds a Context whose Stdout/Stderr are captured into the
// returned buffer. Tests use this in place of the old captureStdout helper so
// per-test output stays isolated from os.Stdout (and from any concurrent
// tests).
func newTestContext(proj *tracker.Project, jsonOut bool) (*Context, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	ctx := &Context{
		JSONOutput: jsonOut,
		Stdout:     &stdout,
		Stderr:     &stderr,
		Stdin:      strings.NewReader(""),
		Project:    proj,
		Now:        time.Now,
	}
	return ctx, &stdout, &stderr
}

func TestCollectChecklist(t *testing.T) {
	t.Parallel()

	body := strings.Join([]string{
		"## Header",
		"- [x] done item",
		"- [ ] pending item",
		"not a checkbox",
		"  - [X] nested done",
	}, "\n")

	got := collectChecklist(body)
	if len(got) != 3 {
		t.Fatalf("collectChecklist len = %d, want 3", len(got))
	}
	if !got[0].Checked || got[0].Text != "done item" {
		t.Fatalf("first checklist item = %+v", got[0])
	}
	if got[1].Checked || got[1].Text != "pending item" {
		t.Fatalf("second checklist item = %+v", got[1])
	}
	if !got[2].Checked || got[2].Text != "nested done" {
		t.Fatalf("third checklist item = %+v", got[2])
	}
}

func TestTransitionSideEffects(t *testing.T) {
	t.Parallel()

	empty := ""
	result := tracker.TransitionResult{
		Update: tracker.IssueUpdate{
			Assignee: &empty,
		},
		BodyAppended:    true,
		ClearedApproval: true,
		InjectedPrompts: []string{"prompt one", "prompt two"},
	}

	got := transitionSideEffects(result)
	want := []string{
		"assignee cleared",
		"approval consumed",
		"workflow content appended to issue body",
		"2 entry guidance prompt(s) injected",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("transitionSideEffects = %#v, want %#v", got, want)
	}

	// Gate sections are named instead of the generic line: created, topped
	// up with missing boxes, or (already complete) not mentioned.
	result = tracker.TransitionResult{
		BodyAppended: true,
		Sections: []tracker.SectionAppend{
			{Title: "Testing", Created: true, BoxesAdded: 3},
			{Title: "Design", BoxesAdded: 1},
			{Title: "Test Plan", BoxesAdded: 2},
			{Title: "Idea"},
		},
	}
	got = transitionSideEffects(result)
	want = []string{
		"## Testing appended",
		"## Design existed — added 1 missing box",
		"## Test Plan existed — added 2 missing boxes",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("transitionSideEffects(sections) = %#v, want %#v", got, want)
	}
}

func TestRunTransitionPrintsPostTransitionState(t *testing.T) {
	proj, issuePath := makeTransitionFixture(t)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"cli/sample", "--to", "in progress"}); err != nil {
		t.Fatalf("runTransition: %v", err)
	}
	output := stdout.String()

	assertContains(t, output, "✓ backlog → in progress")
	assertContains(t, output, "Status: in progress")
	assertContains(t, output, "✓ Assignee cleared")
	assertContains(t, output, "✓ Approval consumed")
	assertContains(t, output, "✓ ## Implementation appended")
	assertContains(t, output, "✓ 1 entry guidance prompt(s) injected")
	assertContains(t, output, "== Checklist (1/3) ==")
	assertContains(t, output, "## (no section)\n  1. [x] already done")
	assertContains(t, output, "## Implementation\n  I1 [ ] Code changes complete")
	assertContains(t, output, "  I2 [ ] Tests written or updated")
	assertContains(t, output, "Tick done boxes by id, several at once: issue-cli check cli/sample <id> [<id>...]")
	assertContains(t, output, "== Guidance ==")
	assertContains(t, output, "- Implement the accepted design.")
	assertContains(t, output, "- Run tests before entering testing.")
	assertContains(t, output, "- Verify the implementation.")
	assertContains(t, output, "issue-cli transition cli/sample --to testing")

	issue := loadIssueByPath(t, proj.IssueDir, issuePath)
	if issue.Status != "in progress" {
		t.Fatalf("issue status = %q, want in progress", issue.Status)
	}
	if issue.Assignee != "" {
		t.Fatalf("issue assignee = %q, want empty", issue.Assignee)
	}
	if issue.HumanApproval != "" {
		t.Fatalf("issue human approval = %q, want empty", issue.HumanApproval)
	}
	if !strings.Contains(issue.BodyRaw, "## Implementation") {
		t.Fatalf("issue body missing appended Implementation section:\n%s", issue.BodyRaw)
	}
}

func TestRunTransitionJSONIncludesPostTransitionFields(t *testing.T) {
	proj, _ := makeTransitionFixture(t)
	ctx, stdout, _ := newTestContext(proj, true)
	if err := runTransition(ctx, []string{"cli/sample", "--to", "in progress"}); err != nil {
		t.Fatalf("runTransition: %v", err)
	}
	output := stdout.String()

	var got transitionOutput
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatalf("unmarshal transition output: %v\noutput:\n%s", err, output)
	}

	if got.From != "backlog" || got.To != "in progress" {
		t.Fatalf("transition = %q -> %q, want backlog -> in progress", got.From, got.To)
	}
	if got.Status != "in progress" {
		t.Fatalf("status = %q, want in progress", got.Status)
	}
	if got.Slug != "cli/sample" {
		t.Fatalf("slug = %q, want cli/sample", got.Slug)
	}
	if got.NextStatus != "testing" {
		t.Fatalf("next_status = %q, want testing", got.NextStatus)
	}
	if !got.BodyChanged {
		t.Fatal("body_changed = false, want true")
	}
	if got.CommentsChanged {
		t.Fatal("comments_changed = true, want false")
	}
	if len(got.Checklist) != 3 {
		t.Fatalf("checklist len = %d, want 3", len(got.Checklist))
	}
	if len(got.SideEffects) != 4 {
		t.Fatalf("side_effects len = %d, want 4", len(got.SideEffects))
	}
	if len(got.Guidance) != 3 {
		t.Fatalf("guidance len = %d, want 3", len(got.Guidance))
	}
}

func TestNormalizeEscapedText(t *testing.T) {
	got := normalizeEscapedText(`line1\nline2\r\nline3\tend`)
	want := "line1\nline2\nline3\tend"
	if got != want {
		t.Fatalf("normalizeEscapedText = %q, want %q", got, want)
	}
}

func TestParseFieldFlags(t *testing.T) {
	got, err := parseFieldFlags([]string{"--to", "testing", "--field", "waiting=design review", "--field", "deferred_to=alice"})
	if err != nil {
		t.Fatalf("parseFieldFlags: %v", err)
	}
	if got["waiting"] != "design review" {
		t.Errorf("waiting = %q, want \"design review\"", got["waiting"])
	}
	if got["deferred_to"] != "alice" {
		t.Errorf("deferred_to = %q, want alice", got["deferred_to"])
	}
}

func TestParseFieldFlags_RejectsMalformed(t *testing.T) {
	if _, err := parseFieldFlags([]string{"--field", "no-equals-sign"}); err == nil {
		t.Fatal("expected error for missing =")
	}
	if _, err := parseFieldFlags([]string{"--field", "=value"}); err == nil {
		t.Fatal("expected error for empty key")
	}
	if _, err := parseFieldFlags([]string{"--field"}); err == nil {
		t.Fatal("expected error for trailing --field with no value")
	}
}

func TestParseFieldFlags_NoFieldsReturnsEmpty(t *testing.T) {
	got, err := parseFieldFlags([]string{"--to", "testing"})
	if err != nil {
		t.Fatalf("parseFieldFlags: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestRunAppendAutoRoutesEscapedDuplicateHeading(t *testing.T) {
	dir := t.TempDir()
	issuesDir := filepath.Join(dir, "issues")
	systemDir := filepath.Join(issuesDir, "CLI")
	if err := os.MkdirAll(systemDir, 0755); err != nil {
		t.Fatalf("mkdir issue dir: %v", err)
	}

	issuePath := filepath.Join(systemDir, "sample.md")
	issue := strings.TrimSpace(`
---
title: "sample"
status: "in progress"
system: "CLI"
---

## Design
Existing note
`)
	if err := os.WriteFile(issuePath, []byte(issue), 0644); err != nil {
		t.Fatalf("write issue: %v", err)
	}

	body := normalizeEscapedText(`## Design\n- [ ] auto routed`)
	_, changed, err := tracker.UpdateIssueBody(issuePath, func(existing string) (string, bool, error) {
		return tracker.AppendIssueBody(existing, body)
	})
	if err != nil {
		t.Fatalf("AppendIssueBody returned error: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}

	finalIssue := loadIssueByPath(t, issuesDir, issuePath)
	if !strings.Contains(finalIssue.BodyRaw, "auto routed") {
		t.Fatalf("issue body missing auto-routed content:\n%s", finalIssue.BodyRaw)
	}
	if strings.Count(finalIssue.BodyRaw, "## Design") != 1 {
		t.Fatalf("expected exactly one ## Design heading, got:\n%s", finalIssue.BodyRaw)
	}
}

func TestConcurrentCheckboxUpdatesPreserveAllChecks(t *testing.T) {
	dir := t.TempDir()
	issuesDir := filepath.Join(dir, "issues")
	systemDir := filepath.Join(issuesDir, "CLI")
	if err := os.MkdirAll(systemDir, 0755); err != nil {
		t.Fatalf("mkdir issue dir: %v", err)
	}

	issuePath := filepath.Join(systemDir, "sample.md")
	issue := strings.TrimSpace(`
---
title: "sample"
status: "in progress"
system: "CLI"
---

- [ ] first task
- [ ] second task
- [ ] third task
`)
	if err := os.WriteFile(issuePath, []byte(issue), 0644); err != nil {
		t.Fatalf("write issue: %v", err)
	}

	var wg sync.WaitGroup
	for _, query := range []string{"first task", "second task"} {
		wg.Add(1)
		go func(query string) {
			defer wg.Done()

			_, changed, err := tracker.UpdateIssueBody(issuePath, func(body string) (string, bool, error) {
				updated, found := tracker.CheckCheckbox(body, query)
				return updated, found, nil
			})
			if err != nil {
				t.Errorf("update %q failed: %v", query, err)
				return
			}
			if !changed {
				t.Errorf("update %q did not change the issue body", query)
			}
		}(query)
	}
	wg.Wait()

	finalIssue := loadIssueByPath(t, issuesDir, issuePath)
	if !strings.Contains(finalIssue.BodyRaw, "- [x] first task") {
		t.Fatalf("first task was not preserved:\n%s", finalIssue.BodyRaw)
	}
	if !strings.Contains(finalIssue.BodyRaw, "- [x] second task") {
		t.Fatalf("second task was not preserved:\n%s", finalIssue.BodyRaw)
	}
	if !strings.Contains(finalIssue.BodyRaw, "- [ ] third task") {
		t.Fatalf("unexpected third task state:\n%s", finalIssue.BodyRaw)
	}
}

func TestRunSetMetaCreatedOnlyWhenMissing(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	slug := issues[0].Slug
	if issues[0].Created != "" {
		t.Fatalf("fixture is already dated: %q", issues[0].Created)
	}
	if err := runSetMeta(ctx, []string{slug, "--key", "created", "--clear"}); err == nil {
		t.Error("--clear on created was accepted")
	}
	if err := runSetMeta(ctx, []string{slug, "--key", "created", "--value", "2026-04-02"}); err != nil {
		t.Fatalf("dating an undated issue: %v", err)
	}
	assertContains(t, stdout.String(), `✓ Set created = "2026-04-02"`)
	err := runSetMeta(ctx, []string{slug, "--key", "created", "--value", "2026-05-01"})
	if err == nil || !strings.Contains(err.Error(), "never changed") {
		t.Errorf("re-dating: err = %v, want a refusal", err)
	}
	issues, _ = tracker.LoadIssues(proj.IssueDir)
	if issues[0].Created != "2026-04-02" {
		t.Errorf("created = %q, want 2026-04-02", issues[0].Created)
	}
}

func TestRunSetMetaSetsAndClears(t *testing.T) {
	dir := t.TempDir()
	issuesDir := filepath.Join(dir, "issues")
	systemDir := filepath.Join(issuesDir, "CLI")
	if err := os.MkdirAll(systemDir, 0755); err != nil {
		t.Fatalf("mkdir issue dir: %v", err)
	}

	issuePath := filepath.Join(systemDir, "sample.md")
	issue := strings.TrimSpace(`
---
title: "sample"
status: "in progress"
system: "CLI"
---

Body
`)
	if err := os.WriteFile(issuePath, []byte(issue), 0644); err != nil {
		t.Fatalf("write issue: %v", err)
	}

	proj := &tracker.Project{Name: "test", Slug: "test", IssueDir: issuesDir}
	ctx, stdout, _ := newTestContext(proj, false)

	if err := runSetMeta(ctx, []string{"cli/sample", "--key", "waiting", "--value", "design review"}); err != nil {
		t.Fatalf("runSetMeta set: %v", err)
	}
	output := stdout.String()
	assertContains(t, output, `✓ Set waiting = "design review"`)
	assertContains(t, output, "file: "+issuePath)

	got := loadIssueByPath(t, issuesDir, issuePath)
	var waiting string
	for _, ef := range got.ExtraFields {
		if ef.Key == "waiting" {
			waiting = ef.Value
		}
	}
	if waiting != "design review" {
		t.Fatalf("waiting = %q, want %q", waiting, "design review")
	}

	stdout.Reset()
	if err := runSetMeta(ctx, []string{"cli/sample", "--key", "waiting", "--clear"}); err != nil {
		t.Fatalf("runSetMeta clear: %v", err)
	}
	assertContains(t, stdout.String(), "✓ Cleared waiting")

	got = loadIssueByPath(t, issuesDir, issuePath)
	for _, ef := range got.ExtraFields {
		if ef.Key == "waiting" {
			t.Fatalf("waiting field still present after clear: %+v", ef)
		}
	}
}

func makeTransitionFixture(t *testing.T) (*tracker.Project, string) {
	t.Helper()

	dir := t.TempDir()
	issuesDir := filepath.Join(dir, "issues")
	systemDir := filepath.Join(issuesDir, "CLI")
	if err := os.MkdirAll(systemDir, 0755); err != nil {
		t.Fatalf("mkdir issue dir: %v", err)
	}

	workflowPath := filepath.Join(dir, "workflow.yaml")
	workflow := strings.TrimSpace(`
statuses:
  - name: "backlog"
    prompt: "Queued for implementation."
  - name: "in progress"
    prompt: "Implement the accepted design."
  - name: "testing"
    prompt: "Verify the implementation."
transitions:
  - from: "backlog"
    to: "in progress"
    actions:
      - type: require_human_approval
        status: "in progress"
      - type: validate
        rule: has_assignee
      - type: append_section
        title: "Implementation"
        body: |
          - [ ] Code changes complete
          - [ ] Tests written or updated
      - type: inject_prompt
        prompt: "Run tests before entering testing."
      - type: set_fields
        field: "assignee"
        value: ""
`)
	if err := os.WriteFile(workflowPath, []byte(workflow), 0644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	issuePath := filepath.Join(systemDir, "sample.md")
	issue := strings.TrimSpace(`
---
title: "sample"
status: "backlog"
system: "CLI"
assignee: "agent-transtion-improvement"
human_approval: "in progress"
---

- [x] already done
`)
	if err := os.WriteFile(issuePath, []byte(issue), 0644); err != nil {
		t.Fatalf("write issue: %v", err)
	}

	return &tracker.Project{
		Name:         "test",
		Slug:         "test",
		IssueDir:     issuesDir,
		WorkflowFile: workflowPath,
	}, issuePath
}

func loadIssueByPath(t *testing.T, issuesDir, issuePath string) *tracker.Issue {
	t.Helper()

	issues, err := tracker.LoadIssues(issuesDir)
	if err != nil {
		t.Fatalf("load issues: %v", err)
	}
	for _, issue := range issues {
		if issue.FilePath == issuePath {
			return issue
		}
	}
	t.Fatalf("issue %s not found", issuePath)
	return nil
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("output missing %q\noutput:\n%s", want, got)
	}
}

func assertNotContains(t *testing.T, got, unwanted string) {
	t.Helper()
	if strings.Contains(got, unwanted) {
		t.Fatalf("output unexpectedly contains %q\noutput:\n%s", unwanted, got)
	}
}

func makeListScoringFixture(t *testing.T, scoringEnabled bool) *tracker.Project {
	t.Helper()

	dir := t.TempDir()
	issuesDir := filepath.Join(dir, "issues")
	if err := os.MkdirAll(issuesDir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", issuesDir, err)
	}

	enabled := "false"
	if scoringEnabled {
		enabled = "true"
	}
	workflowPath := filepath.Join(dir, "workflow.yaml")
	workflow := strings.TrimSpace(fmt.Sprintf(`
statuses:
  - name: "backlog"
  - name: "in progress"
  - name: "done"
scoring:
  enabled: %s
  default_sort: score_desc
  formula:
    priority:
      p0: 100
      p1: 75
      p2: 50
      p3: 25
`, enabled))
	if err := os.WriteFile(workflowPath, []byte(workflow), 0644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	issues := []struct {
		name string
		body string
	}{
		{
			name: "high.md",
			body: strings.TrimSpace(`
---
title: "high priority"
status: "backlog"
priority: "P0"
---
body
`),
		},
		{
			name: "mid.md",
			body: strings.TrimSpace(`
---
title: "mid priority"
status: "backlog"
priority: "P2"
---
body
`),
		},
	}
	for _, iss := range issues {
		if err := os.WriteFile(filepath.Join(issuesDir, iss.name), []byte(iss.body), 0644); err != nil {
			t.Fatalf("write %s: %v", iss.name, err)
		}
	}

	return &tracker.Project{
		Name:         "test",
		Slug:         "test",
		IssueDir:     issuesDir,
		WorkflowFile: workflowPath,
	}
}

func TestRunListJSONIncludesScoreWhenScoringEnabled(t *testing.T) {
	proj := makeListScoringFixture(t, true)
	ctx, stdout, _ := newTestContext(proj, true)
	if err := runList(ctx, nil); err != nil {
		t.Fatalf("runList: %v", err)
	}

	var got []listJSONIssue
	if err := json.Unmarshal([]byte(stdout.String()), &got); err != nil {
		t.Fatalf("unmarshal list output: %v\noutput:\n%s", err, stdout.String())
	}
	if len(got) != 2 {
		t.Fatalf("got %d issues, want 2", len(got))
	}

	if got[0].Slug != "high-priority" {
		t.Fatalf("first slug = %q, want high-priority (sorted by score desc)", got[0].Slug)
	}
	if got[0].Score == nil {
		t.Fatal("first issue Score is nil, want populated float")
	}
	if *got[0].Score != 100 {
		t.Fatalf("first issue Score = %v, want 100", *got[0].Score)
	}
	if got[0].ScoreBreakdown == nil {
		t.Fatal("first issue ScoreBreakdown is nil, want populated breakdown")
	}
	if got[0].ScoreBreakdown.Total != 100 {
		t.Fatalf("first ScoreBreakdown.Total = %v, want 100", got[0].ScoreBreakdown.Total)
	}
	if len(got[0].ScoreBreakdown.Components) == 0 {
		t.Fatal("ScoreBreakdown.Components empty, want priority component")
	}
	if got[0].ScoreBreakdown.Components[0].Name != "priority" {
		t.Fatalf("first component Name = %q, want priority", got[0].ScoreBreakdown.Components[0].Name)
	}

	if got[1].Score == nil || *got[1].Score != 50 {
		t.Fatalf("second issue Score = %v, want 50", got[1].Score)
	}
}

func TestRunListJSONOmitsScoreWhenScoringDisabled(t *testing.T) {
	proj := makeListScoringFixture(t, false)
	ctx, stdout, _ := newTestContext(proj, true)
	if err := runList(ctx, nil); err != nil {
		t.Fatalf("runList: %v", err)
	}

	var got []listJSONIssue
	if err := json.Unmarshal([]byte(stdout.String()), &got); err != nil {
		t.Fatalf("unmarshal list output: %v\noutput:\n%s", err, stdout.String())
	}
	if len(got) != 2 {
		t.Fatalf("got %d issues, want 2", len(got))
	}
	for i, entry := range got {
		if entry.Score != nil {
			t.Errorf("issue[%d].Score = %v, want nil when scoring disabled", i, *entry.Score)
		}
		if entry.ScoreBreakdown != nil {
			t.Errorf("issue[%d].ScoreBreakdown = %+v, want nil when scoring disabled", i, entry.ScoreBreakdown)
		}
	}
}

// resolveAgentClilog must derive the per-agent clilog path from --project +
// slug even when ISSUE_CLI_LOG is unset — that's the case for agents that
// run `issue-cli` outside the tmux session the viewer dispatched (Claude
// Code's Bash tool, IDE terminals, etc.). The match must be on parsed slug,
// not filename, because projects like demo-1 name files `<number>.md`.
func TestResolveAgentClilog_DerivesFromProjectAndSlug(t *testing.T) {
	dir := t.TempDir()
	issuesDir := filepath.Join(dir, "issues")
	if err := os.MkdirAll(issuesDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	issue := strings.TrimSpace(`
---
title: "Scaffold Hello"
status: "dev"
number: 1
assignee: "agent-scaffold-hello"
---

body
`)
	if err := os.WriteFile(filepath.Join(issuesDir, "1.md"), []byte(issue), 0644); err != nil {
		t.Fatalf("write issue: %v", err)
	}
	projectsYAML := fmt.Sprintf("projects:\n  - name: Demo\n    slug: demo\n    workdir: %q\n    issues: %q\n", dir, issuesDir)
	configPath := filepath.Join(dir, "projects.yaml")
	if err := os.WriteFile(configPath, []byte(projectsYAML), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("ISSUE_VIEWER_CONFIG", configPath)

	got := resolveAgentClilog([]string{"--project", "demo", "transition", "scaffold-hello", "--to", "test"})
	want := filepath.Join(dir, ".agent-logs", "agent-scaffold-hello", "agent-scaffold-hello.clilog")
	if got != want {
		t.Fatalf("resolveAgentClilog = %q, want %q", got, want)
	}
}

func TestResolveAgentClilog_ReturnsEmptyWithoutAssignee(t *testing.T) {
	dir := t.TempDir()
	issuesDir := filepath.Join(dir, "issues")
	if err := os.MkdirAll(issuesDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(issuesDir, "hello.md"), []byte("---\ntitle: \"Hello\"\nstatus: dev\n---\nbody\n"), 0644); err != nil {
		t.Fatalf("write issue: %v", err)
	}
	projectsYAML := fmt.Sprintf("projects:\n  - name: Demo\n    slug: demo\n    workdir: %q\n    issues: %q\n", dir, issuesDir)
	configPath := filepath.Join(dir, "projects.yaml")
	if err := os.WriteFile(configPath, []byte(projectsYAML), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("ISSUE_VIEWER_CONFIG", configPath)

	if got := resolveAgentClilog([]string{"--project", "demo", "transition", "hello", "--to", "test"}); got != "" {
		t.Fatalf("expected empty (no assignee), got %q", got)
	}
}

func TestResolveAgentClilog_ReturnsEmptyForNonIssueCommand(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "projects.yaml"), []byte("projects:\n  - name: Demo\n    slug: demo\n"), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("ISSUE_VIEWER_CONFIG", filepath.Join(dir, "projects.yaml"))

	if got := resolveAgentClilog([]string{"--project", "demo", "process", "workflow"}); got != "" {
		t.Fatalf("`process workflow` has no slug; expected empty, got %q", got)
	}
}
