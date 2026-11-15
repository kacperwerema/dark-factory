package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// ------------------- check -------------------

func TestRunCheckMatchesByText(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runCheck(ctx, []string{slug, "first task"}); err != nil {
		t.Fatalf("runCheck: %v", err)
	}
	assertContains(t, stdout.String(), `✓ Checked: D1 [Design #1] first task`)

	got := loadIssueByPath(t, proj.IssueDir, issuePath)
	if !strings.Contains(got.BodyRaw, "- [x] first task") {
		t.Fatalf("first task not checked:\n%s", got.BodyRaw)
	}
}

func TestRunCheckRejectsMissingMatch(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	err := runCheck(ctx, []string{slug, "no such item exists"})
	if err == nil {
		t.Fatal("expected no-match error")
	}
	if !strings.Contains(err.Error(), "no unchecked item matched") {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestRunCheckRequiresQuery(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runCheck(ctx, []string{slug}); err == nil {
		t.Fatal("expected query-required error")
	}
}

func TestRunCheckByIndex(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runCheck(ctx, []string{slug, "--section", "Design", "--index", "2"}); err != nil {
		t.Fatalf("runCheck: %v", err)
	}
	assertContains(t, stdout.String(), "✓ Checked: D2 [Design #2] second task")

	got := loadIssueByPath(t, proj.IssueDir, issuePath)
	if !strings.Contains(got.BodyRaw, "- [x] second task") || strings.Contains(got.BodyRaw, "- [x] first task") {
		t.Fatalf("wrong box checked:\n%s", got.BodyRaw)
	}
}

func TestRunCheckByIndexAlreadyChecked(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runCheck(ctx, []string{slug, "--section", "Design", "--index", "1"}); err != nil {
		t.Fatalf("first check: %v", err)
	}
	stdout.Reset()
	// Checking it again is a no-op success, not an error.
	if err := runCheck(ctx, []string{slug, "--section", "Design", "--index", "1"}); err != nil {
		t.Fatalf("re-check should not error: %v", err)
	}
	assertContains(t, stdout.String(), "Already checked: D1 [Design #1] first task")
}

func TestRunCheckByIndexOutOfRange(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	err := runCheck(ctx, []string{slug, "--section", "Design", "--index", "9"})
	if err == nil {
		t.Fatal("expected out-of-range error")
	}
	if !strings.Contains(err.Error(), "no checkbox at index 9") {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestRunCheckAmbiguousErrors(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	// "task" matches both "first task" and "second task" (both unchecked).
	err := runCheck(ctx, []string{slug, "task"})
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("error = %q", err.Error())
	}
	out := stdout.String()
	assertContains(t, out, "D1 [Design #1] first task")
	assertContains(t, out, "D2 [Design #2] second task")
	assertContains(t, out, "issue-cli check cli/sample D1")
}

func TestRunCheckIndexAndQueryConflict(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runCheck(ctx, []string{slug, "first", "--index", "1"}); err == nil {
		t.Fatal("expected error when both query and --index are given")
	}
}

func TestRunCheckSeveralIDs(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runCheck(ctx, []string{slug, "D1", "d2"}); err != nil {
		t.Fatalf("runCheck: %v", err)
	}
	out := stdout.String()
	assertContains(t, out, "✓ Checked: D1 [Design #1] first task")
	assertContains(t, out, "✓ Checked: D2 [Design #2] second task")
	assertContains(t, out, "Progress: 2/2 (Design 2/2)")

	got := loadIssueByPath(t, proj.IssueDir, issuePath)
	if !strings.Contains(got.BodyRaw, "- [x] first task") || !strings.Contains(got.BodyRaw, "- [x] second task") {
		t.Fatalf("boxes not checked:\n%s", got.BodyRaw)
	}
}

func TestRunCheckIDsAlreadyCheckedAndDuplicates(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runCheck(ctx, []string{slug, "D1"}); err != nil {
		t.Fatalf("first check: %v", err)
	}
	stdout.Reset()
	if err := runCheck(ctx, []string{slug, "D1", "D2", "Design#2"}); err != nil {
		t.Fatalf("runCheck: %v", err)
	}
	out := stdout.String()
	assertContains(t, out, "✓ Checked: D2 [Design #2] second task")
	assertContains(t, out, "Already checked: D1 [Design #1] first task")
	if strings.Count(out, "second task") != 1 {
		t.Fatalf("duplicate ref reported twice:\n%s", out)
	}
}

func TestRunCheckUnknownIDTicksNothing(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	err := runCheck(ctx, []string{slug, "D1", "D9"})
	if err == nil || !strings.Contains(err.Error(), "D9") {
		t.Fatalf("expected unknown-id error naming D9, got %v", err)
	}
	assertContains(t, stdout.String(), "nothing was ticked")
	assertContains(t, stdout.String(), "D1 [ ] first task")

	got := loadIssueByPath(t, proj.IssueDir, issuePath)
	if strings.Contains(got.BodyRaw, "- [x]") {
		t.Fatalf("a box was ticked despite the unknown id:\n%s", got.BodyRaw)
	}
}

func TestRunCheckIDShapedWordFallsBackToText(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	// "x1" is shaped like an id but no section abbreviates to X, so it is a
	// text query — which matches nothing.
	err := runCheck(ctx, []string{slug, "x1"})
	if err == nil || !strings.Contains(err.Error(), "no unchecked item matched") {
		t.Fatalf("expected text no-match error, got %v", err)
	}
}

func TestRunCheckUnquotedMultiWordText(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runCheck(ctx, []string{slug, "second", "task"}); err != nil {
		t.Fatalf("runCheck: %v", err)
	}
	got := loadIssueByPath(t, proj.IssueDir, issuePath)
	if !strings.Contains(got.BodyRaw, "- [x] second task") {
		t.Fatalf("second task not checked:\n%s", got.BodyRaw)
	}
}

func TestRunCheckSectionAll(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runCheck(ctx, []string{slug, "--section", "design", "--all"}); err != nil {
		t.Fatalf("runCheck: %v", err)
	}
	out := stdout.String()
	assertContains(t, out, "✓ Checked: D1 [Design #1] first task")
	assertContains(t, out, "✓ Checked: D2 [Design #2] second task")
	got := loadIssueByPath(t, proj.IssueDir, issuePath)
	if strings.Contains(got.BodyRaw, "- [ ]") {
		t.Fatalf("open box left after --all:\n%s", got.BodyRaw)
	}

	stdout.Reset()
	if err := runCheck(ctx, []string{slug, "--section", "Design", "--all"}); err != nil {
		t.Fatalf("--all on a complete section should not error: %v", err)
	}
	assertContains(t, stdout.String(), `Nothing to check: section "Design" already complete (2/2)`)
}

func TestRunCheckSectionAllErrors(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runCheck(ctx, []string{slug, "--all"}); err == nil || !strings.Contains(err.Error(), "--all requires --section") {
		t.Fatalf("expected --section requirement, got %v", err)
	}
	if err := runCheck(ctx, []string{slug, "--section", "Design", "--all", "D1"}); err == nil {
		t.Fatal("expected error combining --all with ids")
	}
	if err := runCheck(ctx, []string{slug, "--section", "Nope", "--all"}); err == nil || !strings.Contains(err.Error(), "no checkboxes in section") {
		t.Fatalf("expected missing-section error, got %v", err)
	}
}

func TestRunChecklistShowsIDsAndHint(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runChecklist(ctx, []string{slug}); err != nil {
		t.Fatalf("runChecklist: %v", err)
	}
	out := stdout.String()
	assertContains(t, out, "## Design\n  D1 [ ] first task\n  D2 [ ] second task")
	assertContains(t, out, "Tick done boxes by id, several at once: issue-cli check cli/sample <id> [<id>...]")
}

func TestRunChecklistDoesNotRepeatIDInText(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	data, _ := os.ReadFile(issuePath)
	body := strings.Replace(string(data), "- [ ] second task", "- [ ] D2: second task", 1)
	if err := os.WriteFile(issuePath, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runChecklist(ctx, []string{slug}); err != nil {
		t.Fatalf("runChecklist: %v", err)
	}
	assertContains(t, stdout.String(), "  D2 [ ] second task\n")
	stdout.Reset()
	if err := runCheck(ctx, []string{slug, "D2"}); err != nil {
		t.Fatalf("runCheck: %v", err)
	}
	assertContains(t, stdout.String(), "D2 [Design #2] second task")
}

func TestRunChecklistJSONIncludesID(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, true)
	if err := runChecklist(ctx, []string{slug}); err != nil {
		t.Fatalf("runChecklist: %v", err)
	}
	assertContains(t, stdout.String(), `"id": "D2"`)
}

// ------------------- checklist -------------------

func TestRunChecklistText(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runChecklist(ctx, []string{slug}); err != nil {
		t.Fatalf("runChecklist: %v", err)
	}
	out := stdout.String()
	assertContains(t, out, "== Checklist (")
	assertContains(t, out, "first task")
}

func TestRunChecklistJSON(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, true)
	if err := runChecklist(ctx, []string{slug}); err != nil {
		t.Fatalf("runChecklist: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(stdout.String()), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, stdout.String())
	}
	if got["total"] == nil || got["checked"] == nil {
		t.Fatalf("missing total/checked: %v", got)
	}
}

func TestRunChecklistUnknownSlug(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runChecklist(ctx, []string{"missing"}); err == nil {
		t.Fatal("expected not-found error")
	}
}
