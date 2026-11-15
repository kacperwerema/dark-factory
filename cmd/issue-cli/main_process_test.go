package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestProcessSchemaIncludesEveryTaggedField(t *testing.T) {
	ctx, stdout, _ := newTestContext(nil, false)
	if err := runProcessSchema(ctx); err != nil {
		t.Fatalf("runProcessSchema: %v", err)
	}
	out := stdout.String()

	for _, section := range tracker.WorkflowSchemaSections() {
		for _, f := range section.Fields {
			if !strings.Contains(out, f.Name) {
				t.Errorf("schema output missing field %q from %s", f.Name, section.Path)
			}
			if f.Description == "" {
				t.Errorf("field %s.%s missing desc:\"...\" tag", section.Path, f.Name)
			}
		}
	}

	for _, a := range tracker.WorkflowActionTypes {
		if !strings.Contains(out, a.Name) {
			t.Errorf("schema output missing action type %q", a.Name)
		}
	}

	for _, r := range tracker.WorkflowValidationRules {
		if !strings.Contains(out, r.Name) {
			t.Errorf("schema output missing validation rule %q", r.Name)
		}
	}

	assertContains(t, out, "workflow.yaml schema")
	assertContains(t, out, "Action types")
	assertContains(t, out, "Validation rules")
}

func TestProcessSchemaMarksOptionalFields(t *testing.T) {
	ctx, stdout, _ := newTestContext(nil, false)
	if err := runProcessSchema(ctx); err != nil {
		t.Fatalf("runProcessSchema: %v", err)
	}
	out := stdout.String()

	assertContains(t, out, "optional?")
	assertContains(t, out, "prompt?")
	if strings.Contains(out, "name?") {
		t.Errorf("required field `name` should not be suffixed with ?")
	}
}

func TestProcessChangesEmbedsChangelog(t *testing.T) {
	orig := fetchReleases
	fetchReleases = func(string) ([]githubRelease, error) {
		return nil, fmt.Errorf("test: offline")
	}
	t.Cleanup(func() { fetchReleases = orig })

	ctx, stdout, _ := newTestContext(nil, false)
	if err := runProcessChanges(ctx); err != nil {
		t.Fatalf("runProcessChanges: %v", err)
	}
	out := stdout.String()

	assertContains(t, out, "release history")
	assertContains(t, out, "# Changelog")

	// The offline fallback caps output at the 20 most recent version sections
	// (trimChangelogToVersions). Assert those appear, and that older ones are
	// omitted with the documented notice when the changelog exceeds the cap.
	var versions []string
	for _, line := range strings.Split(changelogMD, "\n") {
		if strings.HasPrefix(line, "## v") {
			versions = append(versions, line)
		}
	}
	const cap = 20
	kept := versions
	if len(kept) > cap {
		kept = versions[:cap]
	}
	for _, line := range kept {
		if !strings.Contains(out, line) {
			t.Errorf("process changes output missing recent version line %q", line)
		}
	}
	if len(versions) > cap {
		assertContains(t, out, "older version entries omitted")
		if strings.Contains(out, versions[cap]) {
			t.Errorf("version line %q is beyond the %d-entry cap and should be omitted", versions[cap], cap)
		}
	}
}

func TestProcessChangesPrefersGitHubReleases(t *testing.T) {
	orig := fetchReleases
	fetchReleases = func(repo string) ([]githubRelease, error) {
		if repo != releasesRepo {
			t.Errorf("fetchReleases called with repo %q, want %q", repo, releasesRepo)
		}
		return []githubRelease{
			{
				TagName:     "v9.9.9",
				Name:        "v9.9.9 — test release",
				Body:        "- first test change\n- second test change",
				PublishedAt: "2026-04-24T10:00:00Z",
			},
		}, nil
	}
	t.Cleanup(func() { fetchReleases = orig })

	ctx, stdout, _ := newTestContext(nil, false)
	if err := runProcessChanges(ctx); err != nil {
		t.Fatalf("runProcessChanges: %v", err)
	}
	out := stdout.String()

	assertContains(t, out, "release history")
	assertContains(t, out, "v9.9.9 — test release")
	assertContains(t, out, "2026-04-24")
	assertContains(t, out, "first test change")
	if strings.Contains(out, "# Changelog") {
		t.Error("releases path should not print embedded CHANGELOG heading")
	}
	if strings.Contains(out, "(offline)") {
		t.Error("releases path should not mark output as offline")
	}
}

func TestTrimChangelogToVersions(t *testing.T) {
	t.Parallel()

	md := "# Changelog\n\npreamble here\n\n"
	for i := 25; i >= 1; i-- {
		md += "## v0.0." + itoaLocal(i) + " — 2026-01-01\n\n- change\n\n"
	}

	trimmed, omitted := trimChangelogToVersions(md, 20)
	if omitted != 5 {
		t.Fatalf("omitted = %d, want 5", omitted)
	}
	assertContains(t, trimmed, "# Changelog")
	assertContains(t, trimmed, "preamble here")
	assertContains(t, trimmed, "## v0.0.25")
	assertContains(t, trimmed, "## v0.0.6")
	if strings.Contains(trimmed, "## v0.0.5 ") {
		t.Errorf("v0.0.5 should have been trimmed")
	}
	if strings.Contains(trimmed, "## v0.0.1 ") {
		t.Errorf("v0.0.1 should have been trimmed")
	}
}

func TestTrimChangelogToVersions_UnderCapKeepsAll(t *testing.T) {
	t.Parallel()

	md := "# Changelog\n\n## v0.1.1 — 2026-04-23\n\n- change\n\n## v0.1.0 — 2026-04-23\n\n- change\n"
	trimmed, omitted := trimChangelogToVersions(md, 20)
	if omitted != 0 {
		t.Fatalf("omitted = %d, want 0", omitted)
	}
	assertContains(t, trimmed, "## v0.1.1")
	assertContains(t, trimmed, "## v0.1.0")
}

func itoaLocal(n int) string {
	if n == 0 {
		return "0"
	}
	var buf []byte
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}

func makeProcessTransitionsFixture(t *testing.T) *tracker.Project {
	t.Helper()

	dir := t.TempDir()
	issuesDir := filepath.Join(dir, "issues")
	cliDir := filepath.Join(issuesDir, "CLI")
	if err := os.MkdirAll(cliDir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", cliDir, err)
	}

	workflowPath := filepath.Join(dir, "workflow.yaml")
	workflow := strings.TrimSpace(`
statuses:
  - name: "idea"
  - name: "in design"
  - name: "backlog"
  - name: "in progress"
  - name: "blocked"
    global: true
  - name: "deferred"
    optional: true
  - name: "done"
transitions:
  - from: "idea"
    to: "in design"
    actions:
      - type: validate
        rule: body_not_empty
  - from: "in design"
    to: "backlog"
    actions:
      - type: require_human_approval
        status: "backlog"
      - type: set_fields
        field: "assignee"
        value: ""
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
systems:
  CLI:
    transitions:
      - from: "backlog"
        to: "in progress"
        actions:
          - type: require_human_approval
            status: "in progress"
          - type: validate
            rule: has_assignee
          - type: inject_prompt
            prompt: "CLI-specific guidance for new work."
`)
	if err := os.WriteFile(workflowPath, []byte(workflow), 0644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	cliIssue := strings.TrimSpace(`
---
title: "cli sample"
status: "backlog"
system: "CLI"
---

body
`)
	if err := os.WriteFile(filepath.Join(cliDir, "cli-sample.md"), []byte(cliIssue), 0644); err != nil {
		t.Fatalf("write cli issue: %v", err)
	}

	plainIssue := strings.TrimSpace(`
---
title: "plain sample"
status: "backlog"
---

body
`)
	if err := os.WriteFile(filepath.Join(issuesDir, "plain-sample.md"), []byte(plainIssue), 0644); err != nil {
		t.Fatalf("write plain issue: %v", err)
	}

	return &tracker.Project{
		Name:         "test",
		Slug:         "test",
		IssueDir:     issuesDir,
		WorkflowFile: workflowPath,
	}
}

func TestProcessTransitionsRendersFromActiveWorkflow(t *testing.T) {
	proj := makeProcessTransitionsFixture(t)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runProcessTransitions(ctx, nil); err != nil {
		t.Fatalf("runProcessTransitions: %v", err)
	}
	out := stdout.String()

	assertContains(t, out, "== Transition Rules ==")
	assertContains(t, out, "→ idea")
	assertContains(t, out, "idea → in design")
	assertContains(t, out, "in design → backlog")
	assertContains(t, out, "backlog → in progress")
	assertContains(t, out, "Validate issue body is not empty")
	assertContains(t, out, "Validate issue has assignee")
	assertContains(t, out, `Must be human-approved for "backlog" in the issue viewer`)
	assertContains(t, out, "Side-effect: clears assignee")
	assertContains(t, out, "Side-effect: appends ## Implementation section")
	assertContains(t, out, "Optional statuses (skippable on forward transitions): deferred")
	assertContains(t, out, "Global statuses (transitions from them to any status are allowed): blocked")
	assertContains(t, out, "Per-system overlays are configured for:")
	assertContains(t, out, "CLI")
	// The owner sees each status in the Product hall.
	assertContains(t, out, "In the viewer, the Product hall opens each status: "+viewerBaseURL(proj)+"/p/"+proj.Slug+"/hall/station/<status>")
}

func TestProcessTransitionsScopedBySystemFlag(t *testing.T) {
	proj := makeProcessTransitionsFixture(t)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runProcessTransitions(ctx, []string{"--system", "CLI"}); err != nil {
		t.Fatalf("runProcessTransitions: %v", err)
	}
	out := stdout.String()

	assertContains(t, out, `== Transition Rules — system "CLI"`)
	assertContains(t, out, "Side-effect: injects entry guidance prompt")
	if strings.Contains(out, "Per-system overlays are configured for:") {
		t.Errorf("scoped output should not list per-system overlay hint:\n%s", out)
	}
}

func TestProcessTransitionsScopedByIssueSlug(t *testing.T) {
	proj := makeProcessTransitionsFixture(t)
	ctx, cliStdout, _ := newTestContext(proj, false)
	if err := runProcessTransitions(ctx, []string{"cli-sample"}); err != nil {
		t.Fatalf("runProcessTransitions cli-sample: %v", err)
	}
	cliOut := cliStdout.String()
	assertContains(t, cliOut, `== Transition Rules — system "CLI" (issue cli/cli-sample) ==`)
	assertContains(t, cliOut, "Side-effect: injects entry guidance prompt")

	ctx2, plainStdout, _ := newTestContext(proj, false)
	if err := runProcessTransitions(ctx2, []string{"plain-sample"}); err != nil {
		t.Fatalf("runProcessTransitions plain-sample: %v", err)
	}
	plainOut := plainStdout.String()
	assertContains(t, plainOut, "== Transition Rules — issue plain-sample (no system overlay; project default) ==")
}

func TestChangelogEmbeddedAndHasEntries(t *testing.T) {
	t.Parallel()

	if strings.TrimSpace(changelogMD) == "" {
		t.Fatal("changelogMD is empty — //go:embed CHANGELOG.md failed")
	}
	versionCount := 0
	for _, line := range strings.Split(changelogMD, "\n") {
		if strings.HasPrefix(line, "## v") {
			versionCount++
		}
	}
	if versionCount == 0 {
		t.Fatal("CHANGELOG.md has no '## v...' entries")
	}
}
