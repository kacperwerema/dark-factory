package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// makeSimpleProject builds a temp project with a single issue at the given
// status. Returns the project, the slug used for findIssueOrErr lookups, and
// the issue file path. Tests that mutate the issue should reload via
// loadIssueByPath after invoking the command.
func makeSimpleProject(t *testing.T, status string) (proj *tracker.Project, slug string, issuePath string) {
	t.Helper()

	dir := t.TempDir()
	issuesDir := filepath.Join(dir, "issues")
	systemDir := filepath.Join(issuesDir, "CLI")
	if err := os.MkdirAll(systemDir, 0755); err != nil {
		t.Fatalf("mkdir issue dir: %v", err)
	}

	issuePath = filepath.Join(systemDir, "sample.md")
	body := strings.TrimSpace(fmt.Sprintf(`
---
title: "sample"
status: %q
system: "CLI"
version: "0.1"
priority: "high"
---

## Design
- [ ] first task
- [ ] second task

## Test Plan

### Automated
- thing one

### Manual
- thing two
`, status))
	if err := os.WriteFile(issuePath, []byte(body), 0644); err != nil {
		t.Fatalf("write issue: %v", err)
	}

	proj = &tracker.Project{
		Name:     "test",
		Slug:     "test",
		IssueDir: issuesDir,
		Version:  "0.1",
	}
	return proj, "cli/sample", issuePath
}

// ------------------- claim / unclaim -------------------

func TestRunClaimSetsAssignee(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "backlog")
	ctx, stdout, _ := newTestContext(proj, false)

	if err := runClaim(ctx, []string{slug, "--assignee", "alice"}); err != nil {
		t.Fatalf("runClaim: %v", err)
	}
	assertContains(t, stdout.String(), "✓ Claimed: cli/sample (assignee: alice)")

	got := loadIssueByPath(t, proj.IssueDir, issuePath)
	if got.Assignee != "alice" {
		t.Fatalf("assignee = %q, want alice", got.Assignee)
	}
}

func TestRunClaimRefusesAlreadyClaimedWithoutForce(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "backlog")
	if err := os.WriteFile(issuePath, []byte(strings.Replace(string(mustRead(t, issuePath)), `system: "CLI"`, "system: \"CLI\"\nassignee: \"bob\"", 1)), 0644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	ctx, _, _ := newTestContext(proj, false)

	err := runClaim(ctx, []string{slug, "--assignee", "alice"})
	if err == nil {
		t.Fatal("expected already-claimed error")
	}
	if !strings.Contains(err.Error(), `already claimed by "bob"`) {
		t.Fatalf("error = %q, want already-claimed message", err.Error())
	}
}

func TestRunClaimRequiresSlug(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "backlog")
	ctx, _, _ := newTestContext(proj, false)
	if err := runClaim(ctx, nil); err == nil || !strings.Contains(err.Error(), "claim requires <slug>") {
		t.Fatalf("expected slug-required error, got %v", err)
	}
}

func TestRunUnclaimClearsAssignee(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "backlog")
	ctx, _, _ := newTestContext(proj, false)
	if err := runClaim(ctx, []string{slug, "--assignee", "alice"}); err != nil {
		t.Fatalf("runClaim: %v", err)
	}
	if err := runUnclaim(ctx, []string{slug}); err != nil {
		t.Fatalf("runUnclaim: %v", err)
	}
	got := loadIssueByPath(t, proj.IssueDir, issuePath)
	if got.Assignee != "" {
		t.Fatalf("assignee = %q, want empty", got.Assignee)
	}
}

func TestRunUnclaimUnknownSlug(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "backlog")
	ctx, _, _ := newTestContext(proj, false)
	if err := runUnclaim(ctx, []string{"does-not-exist"}); err == nil {
		t.Fatal("expected not-found error")
	}
}

// ------------------- done -------------------

func TestRunDoneRejectsWrongStatus(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "backlog")
	ctx, _, _ := newTestContext(proj, false)
	err := runDone(ctx, []string{slug})
	if err == nil {
		t.Fatal("expected done error from wrong status")
	}
}

func TestRunDoneUnknownSlug(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "backlog")
	ctx, _, _ := newTestContext(proj, false)
	if err := runDone(ctx, []string{"missing"}); err == nil {
		t.Fatal("expected not-found error")
	}
}

// ------------------- update -------------------

func TestRunUpdateChangesTitle(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runUpdate(ctx, []string{slug, "--title", "new title"}); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	assertContains(t, stdout.String(), "✓ Retitled: cli/sample (slug kept")
	assertContains(t, stdout.String(), `issue-cli update cli/sample --title 'new title' --rename`)

	got := loadIssueByPath(t, proj.IssueDir, issuePath)
	if got.Title != "new title" || got.Slug != "cli/sample" || got.SlugPin != "sample" {
		t.Fatalf("title %q slug %q pin %q, want new title / cli/sample / sample", got.Title, got.Slug, got.SlugPin)
	}

	// A second retitle keeps the existing pin.
	stdout.Reset()
	if err := runUpdate(ctx, []string{slug, "--title", "newer title"}); err != nil {
		t.Fatalf("second retitle: %v", err)
	}
	if got := loadIssueByPath(t, proj.IssueDir, issuePath); got.Slug != "cli/sample" {
		t.Fatalf("second retitle moved the slug to %q", got.Slug)
	}
}

func TestRunUpdateRenameFollowsTitle(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	// Another issue that references the old slug.
	other := filepath.Join(filepath.Dir(issuePath), "child.md")
	mustWrite(t, other, "---\ntitle: \"child\"\nstatus: \"idea\"\nparent: \"cli/sample\"\n---\n")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runUpdate(ctx, []string{slug, "--title", "Fresh Name"}); err != nil {
		t.Fatal(err)
	}
	if err := runUpdate(ctx, []string{slug, "--title", "Fresh Name", "--rename"}); err != nil {
		t.Fatalf("--rename: %v", err)
	}
	out := stdout.String()
	assertContains(t, out, "✓ Renamed: cli/sample → cli/fresh-name")
	assertContains(t, out, "1 issue(s) still reference cli/sample in parent:/depends_on: — update them:\n  cli/child")
	newPath := filepath.Join(filepath.Dir(issuePath), "fresh-name.md")
	got := loadIssueByPath(t, proj.IssueDir, newPath)
	if got.Slug != "cli/fresh-name" || got.SlugPin != "" {
		t.Fatalf("slug %q pin %q after --rename", got.Slug, got.SlugPin)
	}

	// --rename onto an existing file is refused before anything is written.
	before, _ := os.ReadFile(newPath)
	if err := runUpdate(ctx, []string{"cli/fresh-name", "--title", "child", "--rename"}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("want already-exists error, got %v", err)
	}
	if after, _ := os.ReadFile(newPath); string(after) != string(before) {
		t.Error("refused rename changed the file")
	}
}

func TestRunUpdateRequiresAtLeastOneFlag(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runUpdate(ctx, []string{slug}); err == nil || !strings.Contains(err.Error(), "requires --title, --body, --body-file or --parent") {
		t.Fatalf("expected title-or-body error, got %v", err)
	}
}

// ------------------- retrospective -------------------

func TestRunRetrospectiveWritesFile(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runRetrospective(ctx, []string{slug, "--body", "tooling friction: x"}); err != nil {
		t.Fatalf("runRetrospective: %v", err)
	}
	out := stdout.String()
	assertContains(t, out, "✓ Retrospective saved for cli/sample")

	retroDir := filepath.Join(projectRoot(proj), "retros")
	entries, err := os.ReadDir(retroDir)
	if err != nil {
		t.Fatalf("read retros dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("retros dir has %d entries, want 1", len(entries))
	}
	body, err := os.ReadFile(filepath.Join(retroDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read retro file: %v", err)
	}
	if !strings.Contains(string(body), "tooling friction: x") {
		t.Fatalf("retro body missing input: %s", body)
	}
	if !strings.Contains(string(body), "Issue: cli/sample") {
		t.Fatalf("retro body missing issue header: %s", body)
	}
}

func TestRunRetrospectiveRequiresBody(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runRetrospective(ctx, []string{slug}); err == nil || !strings.Contains(err.Error(), "--body") {
		t.Fatalf("expected --body error, got %v", err)
	}
}

// ------------------- search -------------------

func TestRunSearchMatchesTitle(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runSearch(ctx, []string{"sample"}); err != nil {
		t.Fatalf("runSearch: %v", err)
	}
	out := stdout.String()
	assertContains(t, out, `Search: "sample"`)
	assertContains(t, out, "cli/sample")
}

func TestRunSearchJSON(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, true)
	if err := runSearch(ctx, []string{"sample"}); err != nil {
		t.Fatalf("runSearch: %v", err)
	}
	var got []map[string]interface{}
	if err := json.Unmarshal([]byte(stdout.String()), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, stdout.String())
	}
	if len(got) != 1 {
		t.Fatalf("results = %d, want 1", len(got))
	}
}

func TestRunSearchRequiresQuery(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runSearch(ctx, nil); err == nil || !strings.Contains(err.Error(), "requires <query>") {
		t.Fatalf("expected query-required error, got %v", err)
	}
}

// ------------------- next -------------------

func TestRunNextListsBacklogForVersion(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "backlog")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runNext(ctx, []string{"--version", "0.1"}); err != nil {
		t.Fatalf("runNext: %v", err)
	}
	out := stdout.String()
	assertContains(t, out, "== Work for v0.1 ==")
	assertContains(t, out, "Backlog — unclaimed")
	assertContains(t, out, "cli/sample")
}

func TestRunNextDesignFlagListsIdeas(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "idea")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runNext(ctx, []string{"--design", "--version", "0.1"}); err != nil {
		t.Fatalf("runNext: %v", err)
	}
	out := stdout.String()
	assertContains(t, out, "Issues needing design work")
	assertContains(t, out, "cli/sample")
}

// TestRunNextWithoutVersionListsEveryVersion: a fresh project has no
// version: and no --version; next lists the work of every version instead
// of asking for one.
func TestRunNextWithoutVersionListsEveryVersion(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "backlog")
	proj.Version = ""
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runNext(ctx, nil); err != nil {
		t.Fatalf("runNext: %v", err)
	}
	out := stdout.String()
	assertContains(t, out, "== Work for every version ==")
	assertContains(t, out, "cli/sample")
	if strings.Contains(out, "project.yaml") {
		t.Errorf("next names a project.yaml that does not exist:\n%s", out)
	}
}

// ------------------- show / context -------------------

func TestRunShowText(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runShow(ctx, []string{slug}); err != nil {
		t.Fatalf("runShow: %v", err)
	}
	out := stdout.String()
	assertContains(t, out, "== sample ==")
	assertContains(t, out, "Status: in progress")
	assertContains(t, out, "== Body ==")
	assertContains(t, out, "first task")
	assertContains(t, out, "== Test Plan ==")
}

func TestRunShowJSON(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, true)
	if err := runShow(ctx, []string{slug}); err != nil {
		t.Fatalf("runShow: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(stdout.String()), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, stdout.String())
	}
	if got["issue"] == nil {
		t.Fatalf("missing issue field: %v", got)
	}
}

func TestRunShowUnknownSlug(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runShow(ctx, []string{"missing"}); err == nil {
		t.Fatal("expected not-found error")
	}
}

// ------------------- create -------------------

func TestRunCreateNewIssue(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runCreate(ctx, []string{"--title", "another idea", "--system", "CLI", "--status", "idea"}); err != nil {
		t.Fatalf("runCreate: %v", err)
	}
	out := stdout.String()
	assertContains(t, out, "✓ Created:")
	assertContains(t, out, "Slug: cli/another-idea")
	if strings.Contains(out, "Thank you") {
		t.Errorf("create thanks the caller:\n%s", out)
	}

	if _, err := os.Stat(filepath.Join(proj.IssueDir, "CLI", "another-idea.md")); err != nil {
		t.Fatalf("created file missing: %v", err)
	}
}

func TestRunCreateWritesCreatedDate(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return time.Date(2026, 10, 24, 23, 30, 0, 0, time.Local) }
	if err := runCreate(ctx, []string{"--title", "dated idea"}); err != nil {
		t.Fatalf("runCreate: %v", err)
	}
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	issue := tracker.FindIssue(issues, "dated-idea")
	if issue == nil || issue.Created != "2026-10-24" {
		t.Fatalf("created = %v, want 2026-10-24", issue)
	}
}

func TestRunCreateRefusesUnparsableFrontmatter(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	err := runCreate(ctx, []string{"--title", "broken one", "--priority", `high" oops: "x`})
	if err == nil || !strings.Contains(err.Error(), "frontmatter would not parse") {
		t.Fatalf("expected a refused create, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(proj.IssueDir, "broken-one.md")); !os.IsNotExist(err) {
		t.Fatalf("issue file written despite the refusal")
	}
}

func TestRunCreateRequiresTitle(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runCreate(ctx, nil); err == nil || !strings.Contains(err.Error(), "--title is required") {
		t.Fatalf("expected title-required error, got %v", err)
	}
}

func TestRunCreateRejectsBacklogStatus(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	err := runCreate(ctx, []string{"--title", "x", "--status", "backlog"})
	if err == nil || !strings.Contains(err.Error(), `with status "backlog"`) {
		t.Fatalf("expected status-rejected error, got %v", err)
	}
}

// A workflow whose first status is backlog has nowhere create may start an
// issue: the refusal says a status before backlog is needed instead of
// listing an empty "allowed:" (retro 20261031-051750).
func TestRunCreateOnAWorkflowStartingAtBacklogSaysWhatIsMissing(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "backlog")
	wf := filepath.Join(t.TempDir(), "workflow.yaml")
	if err := os.WriteFile(wf, []byte("statuses:\n  - name: \"backlog\"\n  - name: \"in progress\"\n  - name: \"done\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	proj.WorkflowFile = wf
	ctx, _, _ := newTestContext(proj, false)
	for _, args := range [][]string{{"--title", "x"}, {"--title", "y", "--status", "in progress"}} {
		err := runCreate(ctx, args)
		if err == nil || !strings.Contains(err.Error(), "needs a status before backlog") || strings.Contains(err.Error(), "allowed: ") {
			t.Errorf("create %v: %v", args, err)
		}
	}
}

// ------------------- stats -------------------

func TestRunStatsText(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runStats(ctx, nil); err != nil {
		t.Fatalf("runStats: %v", err)
	}
	out := stdout.String()
	assertContains(t, out, "Project Stats")
	assertContains(t, out, "By status:")
	assertContains(t, out, "By system:")
	assertContains(t, out, "CLI")
}

func TestRunStatsJSON(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, true)
	if err := runStats(ctx, nil); err != nil {
		t.Fatalf("runStats: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(stdout.String()), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, stdout.String())
	}
	if got["total"] == nil || got["by_status"] == nil || got["by_system"] == nil {
		t.Fatalf("missing top-level keys: %v", got)
	}
	if int(got["total"].(float64)) != 1 {
		t.Fatalf("total = %v, want 1", got["total"])
	}
}

// ------------------- helpers -------------------

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}
