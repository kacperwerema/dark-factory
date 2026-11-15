package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// ------------------- comment -------------------

func TestRunCommentAddsComment(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runComment(ctx, []string{slug, "--text", "tests: 3 added"}); err != nil {
		t.Fatalf("runComment: %v", err)
	}
	assertContains(t, stdout.String(), "✓ Comment added to cli/sample")

	comments, err := tracker.LoadComments(issuePath)
	if err != nil {
		t.Fatalf("LoadComments: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("comments len = %d, want 1", len(comments))
	}
	if !strings.Contains(comments[0].Text, "tests: 3 added") {
		t.Fatalf("comment text = %q", comments[0].Text)
	}
}

func TestRunCommentRequiresText(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runComment(ctx, []string{slug}); err == nil {
		t.Fatal("expected text-required error")
	}
}

func TestRunCommentBodyFile(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)

	noteFile := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(noteFile, []byte("tests: `go test` (all pass)"), 0644); err != nil {
		t.Fatalf("write note file: %v", err)
	}
	if err := runComment(ctx, []string{slug, "--body-file", noteFile}); err != nil {
		t.Fatalf("runComment: %v", err)
	}

	comments, err := tracker.LoadComments(issuePath)
	if err != nil {
		t.Fatalf("LoadComments: %v", err)
	}
	if len(comments) != 1 || !strings.Contains(comments[0].Text, "tests: `go test` (all pass)") {
		t.Fatalf("comment not stored verbatim: %+v", comments)
	}
}

func TestRunCommentBodyFileStdin(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	ctx.Stdin = strings.NewReader("docs: updated (CLI overview)")

	if err := runComment(ctx, []string{slug, "--body-file", "-"}); err != nil {
		t.Fatalf("runComment: %v", err)
	}

	comments, err := tracker.LoadComments(issuePath)
	if err != nil {
		t.Fatalf("LoadComments: %v", err)
	}
	if len(comments) != 1 || !strings.Contains(comments[0].Text, "docs: updated (CLI overview)") {
		t.Fatalf("stdin comment not stored verbatim: %+v", comments)
	}
}

func TestRunCommentBodyFileConflict(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)

	noteFile := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(noteFile, []byte("x"), 0644); err != nil {
		t.Fatalf("write note file: %v", err)
	}
	err := runComment(ctx, []string{slug, "--text", "inline", "--body-file", noteFile})
	if err == nil || !strings.Contains(err.Error(), "--body-file") {
		t.Fatalf("expected conflict error, got %v", err)
	}
}

// ------------------- replace -------------------

func TestRunReplaceUpdatesSection(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runReplace(ctx, []string{slug, "--section", "Design", "--body", "rewritten content"}); err != nil {
		t.Fatalf("runReplace: %v", err)
	}
	assertContains(t, stdout.String(), `✓ Replaced section "Design" in cli/sample`)

	got := loadIssueByPath(t, proj.IssueDir, issuePath)
	if !strings.Contains(got.BodyRaw, "rewritten content") {
		t.Fatalf("section not replaced:\n%s", got.BodyRaw)
	}
}

func TestRunReplaceRequiresSection(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runReplace(ctx, []string{slug, "--body", "x"}); err == nil || !strings.Contains(err.Error(), "--section") {
		t.Fatalf("expected --section error, got %v", err)
	}
}

func TestRunReplaceRequiresBody(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runReplace(ctx, []string{slug, "--section", "Design"}); err == nil || !strings.Contains(err.Error(), "--body") {
		t.Fatalf("expected --body error, got %v", err)
	}
}

// ------------------- append -------------------

func TestRunAppendAppendsToBody(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runAppend(ctx, []string{slug, "--body", "appended note"}); err != nil {
		t.Fatalf("runAppend: %v", err)
	}
	assertContains(t, stdout.String(), "✓ Appended to cli/sample")

	got := loadIssueByPath(t, proj.IssueDir, issuePath)
	if !strings.Contains(got.BodyRaw, "appended note") {
		t.Fatalf("appended note missing:\n%s", got.BodyRaw)
	}
}

func TestRunAppendRequiresBody(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runAppend(ctx, []string{slug}); err == nil || !strings.Contains(err.Error(), "--body") {
		t.Fatalf("expected --body error, got %v", err)
	}
}

// shellHazard is body content that a shell would mangle (backticks trigger
// command substitution, parens are subshell syntax). Reading it from a file or
// stdin must preserve it byte-for-byte.
const shellHazard = "- Uses `Asteroid.NetRate` (workers × yield)"

func TestRunAppendBodyFile(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)

	bodyFile := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(bodyFile, []byte(shellHazard), 0644); err != nil {
		t.Fatalf("write body file: %v", err)
	}
	if err := runAppend(ctx, []string{slug, "--body-file", bodyFile}); err != nil {
		t.Fatalf("runAppend: %v", err)
	}
	assertContains(t, stdout.String(), "✓ Appended to cli/sample")

	got := loadIssueByPath(t, proj.IssueDir, issuePath)
	if !strings.Contains(got.BodyRaw, shellHazard) {
		t.Fatalf("body-file content missing/mangled:\n%s", got.BodyRaw)
	}
}

func TestRunAppendBodyFileIntoSection(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)

	bodyFile := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(bodyFile, []byte(shellHazard), 0644); err != nil {
		t.Fatalf("write body file: %v", err)
	}
	if err := runAppend(ctx, []string{slug, "--section", "Design", "--body-file", bodyFile}); err != nil {
		t.Fatalf("runAppend: %v", err)
	}

	got := loadIssueByPath(t, proj.IssueDir, issuePath)
	designIdx := strings.Index(got.BodyRaw, "## Design")
	testPlanIdx := strings.Index(got.BodyRaw, "## Test Plan")
	hazardIdx := strings.Index(got.BodyRaw, shellHazard)
	if hazardIdx < designIdx || hazardIdx > testPlanIdx {
		t.Fatalf("content not routed into Design section:\n%s", got.BodyRaw)
	}
}

func TestRunAppendBodyFileStdin(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	ctx.Stdin = strings.NewReader(shellHazard)

	if err := runAppend(ctx, []string{slug, "--body-file", "-"}); err != nil {
		t.Fatalf("runAppend: %v", err)
	}

	got := loadIssueByPath(t, proj.IssueDir, issuePath)
	if !strings.Contains(got.BodyRaw, shellHazard) {
		t.Fatalf("stdin content missing/mangled:\n%s", got.BodyRaw)
	}
}

func TestRunAppendBodyFileConflict(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)

	bodyFile := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(bodyFile, []byte("x"), 0644); err != nil {
		t.Fatalf("write body file: %v", err)
	}
	err := runAppend(ctx, []string{slug, "--body", "inline", "--body-file", bodyFile})
	if err == nil || !strings.Contains(err.Error(), "--body-file") {
		t.Fatalf("expected conflict error, got %v", err)
	}
}

func TestRunAppendBodyFileMissing(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)

	missing := filepath.Join(t.TempDir(), "nope.md")
	err := runAppend(ctx, []string{slug, "--body-file", missing})
	if err == nil || !strings.Contains(err.Error(), "failed to read body file") {
		t.Fatalf("expected read error, got %v", err)
	}
}

func TestRunAppendBodyFileEmpty(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)

	bodyFile := filepath.Join(t.TempDir(), "empty.md")
	if err := os.WriteFile(bodyFile, []byte(""), 0644); err != nil {
		t.Fatalf("write body file: %v", err)
	}
	err := runAppend(ctx, []string{slug, "--body-file", bodyFile})
	if err == nil || !strings.Contains(err.Error(), "--body") {
		t.Fatalf("expected requires-body error, got %v", err)
	}
}
