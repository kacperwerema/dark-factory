package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// A dispatched worker leaves its slug out: check D1 ticks its own issue's
// D1, and the first default says once, on stderr, where the slug came from.
func TestOwnSlugDefaultsCheck(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir()) // the note's marker starts fresh
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	asWorkerOn(t, slug)
	ctx, stdout, stderr := newTestContext(proj, false)
	if err := runCheck(ctx, []string{"D1"}); err != nil {
		t.Fatalf("check D1: %v", err)
	}
	assertContains(t, stdout.String(), "✓ Checked: D1")
	assertContains(t, readFile(t, issuePath), "- [x] first task\n- [ ] second task\n")
	note := "issue-cli: no slug given: using your issue cli/sample (ISSUE_VIEWER_ISSUE_SLUG)\n"
	if stderr.String() != note {
		t.Fatalf("stderr = %q, want the one note %q", stderr.String(), note)
	}

	// The note is shown once per issue.
	ctx, _, stderr = newTestContext(proj, false)
	if err := runCheck(ctx, []string{"D2"}); err != nil {
		t.Fatalf("check D2: %v", err)
	}
	if strings.Contains(stderr.String(), "no slug given") {
		t.Errorf("the note came back: %q", stderr.String())
	}

	// Commands without positionals take it when nothing or a flag comes first.
	for _, c := range []struct {
		name string
		run  func(*Context, []string) error
		args []string
		want string
	}{
		{"checklist", runChecklist, nil, "D2 [x] second task"},
		{"show", runShow, nil, "sample"},
		{"comment", runComment, []string{"progress: half done"}, ""},
		{"comment", runComment, []string{"tests: 5/5 passed in ./internal/x"}, ""},
		{"append", runAppend, []string{"--section", "Design", "--body", "- [ ] third task"}, ""},
	} {
		ctx, stdout, _ := newTestContext(proj, false)
		if err := c.run(ctx, c.args); err != nil {
			t.Fatalf("%s %v: %v", c.name, c.args, err)
		}
		if c.want != "" {
			assertContains(t, stdout.String(), c.want)
		}
	}
	body := readFile(t, issuePath)
	assertContains(t, body, "- [ ] third task")
	comments, err := tracker.LoadComments(issuePath)
	if err != nil || len(comments) != 2 || !strings.Contains(comments[0].Text, "progress: half done") || !strings.Contains(comments[1].Text, "tests: 5/5 passed") {
		t.Errorf("comment without a slug did not land on the worker's issue: %+v %v", comments, err)
	}
}

// Without ISSUE_VIEWER_ISSUE_SLUG (the owner's shell, a lead) nothing is
// defaulted: the command still asks for the slug.
func TestOwnSlugUnsetStillAsks(t *testing.T) {
	proj, _, issuePath := makeSimpleProject(t, "in progress")
	asOwner(t)
	before := readFile(t, issuePath)
	ctx, _, stderr := newTestContext(proj, false)
	err := runCheck(ctx, nil)
	if err == nil || !strings.Contains(err.Error(), "check requires <slug>") {
		t.Fatalf("check with nothing: %v", err)
	}
	if err := runCheck(ctx, []string{"D1"}); err == nil || !strings.Contains(err.Error(), "D1") {
		t.Fatalf("check D1 with no env: want a not-found for D1, got %v", err)
	}
	if err := runChecklist(ctx, nil); err == nil || !strings.Contains(err.Error(), "checklist requires <slug>") {
		t.Fatalf("checklist with nothing: %v", err)
	}
	if strings.Contains(stderr.String(), "no slug given") {
		t.Errorf("the note printed without the env: %q", stderr.String())
	}
	if readFile(t, issuePath) != before {
		t.Error("a command with no slug and no env changed the issue")
	}
}

// A slug on the command line always wins over the env, in full or as the
// issue's unique last part.
func TestOwnSlugCommandLineWins(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	asWorkerOn(t, "cli/elsewhere")
	for _, first := range []string{slug, "sample"} {
		ctx, _, stderr := newTestContext(proj, false)
		if err := runCheck(ctx, []string{first, "D1"}); err != nil {
			t.Fatalf("check %s D1: %v", first, err)
		}
		if strings.Contains(stderr.String(), "no slug given") {
			t.Errorf("check %s D1 used the env: %q", first, stderr.String())
		}
		if err := runUncheck(ctx, []string{first, "D1"}); err != nil {
			t.Fatalf("uncheck %s D1: %v", first, err)
		}
	}
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runChecklist(ctx, []string{slug}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "D1 [ ] first task")
	_ = issuePath
}

// A first word with "/" that names no issue is a mistyped slug: it fails as
// not found and never lands on the worker's own issue.
func TestOwnSlugMistypedSlugStaysNotFound(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	asWorkerOn(t, slug)
	before := readFile(t, issuePath)
	ctx, _, _ := newTestContext(proj, false)
	err := runCheck(ctx, []string{"cli/sampel", "D1"})
	if err == nil || !strings.Contains(err.Error(), "cli/sampel") {
		t.Fatalf("check cli/sampel D1: want not found, got %v", err)
	}
	if readFile(t, issuePath) != before {
		t.Error("a mistyped slug ticked the worker's own issue")
	}
}

// output add with no --issue, --department or --job is the worker's issue's.
func TestOwnSlugOutputAdd(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	asWorkerOn(t, slug)
	ctx, stdout, _ := newTestContext(proj, true)
	if err := runOutputAdd(ctx, []string{"--title", "Report", "--kind", "report", "--url", "https://example.com/r"}); err != nil {
		t.Fatalf("output add: %v", err)
	}
	assertContains(t, stdout.String(), `"issue": "cli/sample"`)
}

// The note's marker lives in issue-cli's state dir, one per issue.
func TestOwnSlugNoteMarker(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := ownSlugMarker("cli/sample")
	if filepath.Dir(m) != filepath.Join(os.Getenv("XDG_STATE_HOME"), "issue-cli", "own-slug") || m == ownSlugMarker("cli/other") {
		t.Fatalf("marker = %q", m)
	}
}
