package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/cmd/issue-cli/templates"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// designPreviewProject is one issue at status on criteriaGateWorkflow, its
// criteria and Must not written well, so an edit that breaks one shows.
func designPreviewProject(t *testing.T, status string) (*tracker.Project, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "issues"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflow.yaml"), []byte(criteriaGateWorkflow), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "issues", "edit.md")
	body := "---\ntitle: \"Edit\"\nstatus: \"" + status + "\"\n---\n\n## Design\n\nPlan.\n\n## Acceptance Criteria\n\n" +
		"- [ ] `issue-cli status` names it (test: TestStatusNamesIt)\n\n" +
		"## Must not\n\n- Drop a line → AC1\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return &tracker.Project{Slug: "proj", Name: "Proj", IssueDir: filepath.Join(root, "issues"), WorkDir: root, WorkflowFile: filepath.Join(root, "workflow.yaml")}, path
}

// The three shapes the backlog gate refused or warned about in the
// post-mortems show at the edit that wrote them; a clean edit, an edit to
// another section and an issue past design print nothing.
func TestDesignEditPrintsTheBacklogGatesRefusals(t *testing.T) {
	const refuse, warn = "⚠ the backlog gate would refuse: criteria_checkboxes:", "⚠ the backlog gate would warn:"
	run := func(t *testing.T, status string, cmd func(*Context, []string) error, args ...string) string {
		t.Helper()
		proj, _ := designPreviewProject(t, status)
		ctx, stdout, _ := newTestContext(proj, false)
		if err := cmd(ctx, append([]string{"edit"}, args...)); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, stdout)
		}
		return stdout.String()
	}

	t.Run("sub-bullets refuse", func(t *testing.T) {
		out := run(t, "in design", runReplace, "--section", "Acceptance Criteria", "--body",
			"- [ ] `issue-cli status` names it (test: TestStatusNamesIt)\n  - also in --json\n- the page loads")
		for _, want := range []string{refuse, "plain bullet, not a checkbox: also in --json", "plain bullet, not a checkbox: the page loads", "issue-cli replace edit --section"} {
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q:\n%s", want, out)
			}
		}
	})
	t.Run("template box refuses", func(t *testing.T) {
		out := run(t, "in design", runAppend, "--section", "Acceptance Criteria", "--body", "- [ ] First observable requirement")
		if !strings.Contains(out, refuse) || !strings.Contains(out, "AC2 still has the template text") {
			t.Errorf("output lacks the template-box refusal:\n%s", out)
		}
	})
	t.Run("unlinked Must not warns", func(t *testing.T) {
		out := run(t, "in design", runAppend, "--section", "Must not", "--body", "- Touch the owner's viewer")
		if !strings.Contains(out, warn) || !strings.Contains(out, "names no criterion that fails when it is broken") || !strings.Contains(out, "→ AC<n>") {
			t.Errorf("output lacks the Must not warning:\n%s", out)
		}
		if strings.Contains(out, refuse) {
			t.Errorf("a Must not line is a warning, not a refusal (D-1):\n%s", out)
		}
	})
	t.Run("a Must not line by --match warns", func(t *testing.T) {
		out := run(t, "in design", runReplace, "--section", "Must not", "--match", "Drop", "--text", "- Drop a line")
		if !strings.Contains(out, warn) {
			t.Errorf("replace --match output lacks the warning:\n%s", out)
		}
	})
	quiet := map[string]struct {
		status string
		cmd    func(*Context, []string) error
		args   []string
	}{
		"clean criteria":  {"in design", runAppend, []string{"--section", "Acceptance Criteria", "--body", "- [ ] `issue-cli list` names it (test: TestListNamesIt)"}},
		"linked Must not": {"in design", runAppend, []string{"--section", "Must not", "--body", "- Touch the owner's viewer → AC1"}},
		// D-2: an edit elsewhere doesn't repeat what the criteria hold.
		"other section": {"in design", runReplace, []string{"--section", "Design", "--body", "- the plan\n  - a sub-bullet"}},
		"past design":   {"backlog", runAppend, []string{"--section", "Acceptance Criteria", "--body", "- plain"}},
	}
	for name, c := range quiet {
		t.Run(name+" says nothing", func(t *testing.T) {
			if out := run(t, c.status, c.cmd, c.args...); strings.Contains(out, "⚠") {
				t.Errorf("output warns:\n%s", out)
			}
		})
	}
}

// The Must not placeholder every bundled source writes shows the link the
// lint asks for, and still reads as the placeholder: neither it nor the old
// text counts as a written line, so issues written before need nothing.
func TestMustNotPlaceholderShowsTheLink(t *testing.T) {
	if !strings.Contains(tracker.MustNotPlaceholder, "→ AC<n>") || !strings.Contains(tracker.MustNotPlaceholder, "→ none: <why>") {
		t.Fatalf("MustNotPlaceholder = %q, want the → AC<n> and → none: <why> forms", tracker.MustNotPlaceholder)
	}
	data, err := templates.FS.ReadFile("workflow/development.yaml")
	if err != nil {
		t.Fatal(err)
	}
	wfPath := filepath.Join(t.TempDir(), "workflow.yaml")
	if err := os.WriteFile(wfPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	root, _ := filepath.Abs("../..")
	for _, path := range []string{wfPath, filepath.Join(root, "workflow.yaml")} {
		wf, err := tracker.LoadWorkflow(path)
		if err != nil {
			t.Fatal(err)
		}
		var body string
		for _, tr := range wf.Transitions {
			for _, a := range tr.Actions {
				if a.Type == "append_section" && strings.EqualFold(a.Title, tracker.MustNotSection) {
					body = a.Body
				}
			}
		}
		if !strings.Contains(body, "- "+tracker.MustNotPlaceholder) {
			t.Errorf("%s: the Must not append_section writes %q, want the placeholder %q", path, body, tracker.MustNotPlaceholder)
		}
	}
	feature, err := templates.FS.ReadFile("workflow-features/must-not-section.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(feature), tracker.MustNotPlaceholder) {
		t.Errorf("workflow-features/must-not-section.yaml snippet lacks the placeholder %q", tracker.MustNotPlaceholder)
	}
	old := "<what this change must not do; name a test or a command in backticks when one can catch it>"
	for _, p := range []string{tracker.MustNotPlaceholder, old} {
		if got := tracker.MustNot("## Must not\n\n- " + p + "\n"); len(got) != 0 {
			t.Errorf("MustNot reads placeholder %q as a line: %q", p, got)
		}
	}
}

const checkSectionWorkflow = `statuses:
  - name: "in progress"
    description: "build"
  - name: "testing"
    description: "test"
transitions:
  - from: "in progress"
    to: "testing"
    actions:
      - type: "validate"
        rule: "section_checkboxes_checked: Implementation"
`

// The lead-context run: at in progress --check I2 I3 named Idea's boxes
// (Idea holds I, Implementation Im), check said "Already checked" and the
// gate refused with nothing saying why. The note names each id's section
// and what the move waits on; ids in that section say nothing.
func TestTransitionCheckNamesTheSectionOfEachId(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "issues"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflow.yaml"), []byte(checkSectionWorkflow), 0o644); err != nil {
		t.Fatal(err)
	}
	body := "---\ntitle: \"Two\"\nstatus: \"in progress\"\n---\n\n## Idea\n\n- [x] Described\n- [x] Edges\n- [ ] Scope\n\n" +
		"## Implementation\n\n- [ ] Code\n- [ ] Tests\n"
	if err := os.WriteFile(filepath.Join(root, "issues", "two.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "two")
	proj := &tracker.Project{Slug: "proj", Name: "Proj", IssueDir: filepath.Join(root, "issues"), WorkDir: root, WorkflowFile: filepath.Join(root, "workflow.yaml")}

	const note = "⚠ I2, I3 ticked in ## Idea; this move waits on ## Implementation (open: Im1 Im2)"
	dry, _, _ := runYOLOTransition(t, proj, time.Now(), "two", "--to", "testing", "--check", "I2", "I3", "--dry-run")
	if !strings.Contains(dry, note) {
		t.Errorf("dry run lacks %q:\n%s", note, dry)
	}
	out, _, err := runYOLOTransition(t, proj, time.Now(), "two", "--to", "testing", "--check", "I2", "I3")
	if err == nil {
		t.Fatalf("moved with Implementation open:\n%s", out)
	}
	if !strings.Contains(out, note) {
		t.Errorf("output lacks %q:\n%s", note, out)
	}
	// A box for a later gate beside the last gated ones: the move's
	// sections are done, so no note.
	out, _, err = runYOLOTransition(t, proj, time.Now(), "two", "--to", "testing", "--check", "I3", "Im1", "Im2")
	if err != nil {
		t.Fatalf("Im1 Im2: %v\n%s", err, out)
	}
	if strings.Contains(out, "ticked in ##") {
		t.Errorf("ids in the gated section print the note:\n%s", out)
	}
}
