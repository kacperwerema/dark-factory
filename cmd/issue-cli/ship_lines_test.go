package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

const shipWorkflow = `
statuses:
  - name: idea
  - name: in progress
  - name: shipping
    prompt: "Fast-forward the main checkout."
  - name: done
transitions:
  - from: "in progress"
    to: "shipping"
  - from: "shipping"
    to: "done"
`

// shipProject is a project whose root is a git repository on main with one
// commit, so the ship lines can read the main checkout.
func shipProject(t *testing.T, issues map[string]string) (*tracker.Project, func(args ...string) string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
	root := realTempDir(t)
	issuesDir := filepath.Join(root, "issues")
	if err := os.MkdirAll(issuesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wfPath := filepath.Join(root, "workflow.yaml")
	if err := os.WriteFile(wfPath, []byte(shipWorkflow), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, content := range issues {
		if err := os.WriteFile(filepath.Join(issuesDir, name+".md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	g := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	g("init", "-q", "-b", "main")
	g("add", "workflow.yaml")
	g("commit", "-q", "-m", "init")
	return &tracker.Project{Name: "test", Slug: "test", IssueDir: issuesDir, WorkflowFile: wfPath, WorkDir: root}, g
}

func depIssue(title, status, deps string) string {
	s := "---\ntitle: \"" + title + "\"\nstatus: \"" + status + "\"\n"
	if deps != "" {
		s += "depends_on: \"" + deps + "\"\n"
	}
	return s + "---\n\nbody\n"
}

func writeDispatches(t *testing.T, proj *tracker.Project, slug string, rows ...tracker.Dispatch) {
	t.Helper()
	if err := tracker.SaveStats(filepath.Join(proj.IssueDir, slug+".md"), tracker.StatsStore{Dispatches: rows}); err != nil {
		t.Fatal(err)
	}
}

func TestShowAndStartPrintTheDispatchBase(t *testing.T) {
	proj, g := shipProject(t, map[string]string{
		"alpha": depIssue("alpha", "in progress", ""),
		"old":   depIssue("old", "in progress", ""),
	})
	sha := g("rev-parse", "HEAD")
	at := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	writeDispatches(t, proj, "alpha",
		tracker.Dispatch{At: at.Add(-time.Hour), Session: "agent-alpha", Agent: "claude"},
		tracker.Dispatch{At: at, Session: "agent-alpha", Agent: "claude", BaseBranch: "main", BaseSHA: sha},
		tracker.Dispatch{At: at.Add(time.Hour), Session: "agent-alpha", Agent: "claude", BaseBranch: "other", BaseSHA: "f00"},
	)
	writeDispatches(t, proj, "old", tracker.Dispatch{At: at, Session: "agent-old", Agent: "claude"})
	want := "Base: main at " + sha[:7] + " (cut by the dispatch on 2026-10-30)"

	ctx, out, _ := newTestContext(proj, false)
	if err := runShow(ctx, []string{"alpha"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), want)

	ctx, out, _ = newTestContext(proj, true)
	if err := runShow(ctx, []string{"alpha"}); err != nil {
		t.Fatal(err)
	}
	var shown struct {
		Base map[string]string `json:"base"`
	}
	if err := json.Unmarshal(out.Bytes(), &shown); err != nil {
		t.Fatal(err)
	}
	if shown.Base["branch"] != "main" || shown.Base["sha"] != sha {
		t.Errorf("show --json base = %v", shown.Base)
	}

	ctx, out, _ = newTestContext(proj, false)
	if err := runStart(ctx, []string{"alpha"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), want)

	// Dispatched before bases were recorded: no Base line, no guess.
	ctx, out, _ = newTestContext(proj, false)
	if err := runShow(ctx, []string{"old"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Base:") {
		t.Errorf("show printed a Base line with none recorded:\n%s", out)
	}
}

func TestStartListsDependsOnWithStatusAndBranch(t *testing.T) {
	proj, g := shipProject(t, map[string]string{
		"alpha": depIssue("alpha", "in progress", "beta, gamma, nowhere"),
		"beta":  depIssue("beta", "in progress", ""),
		"gamma": depIssue("gamma", "done", ""),
	})
	g("branch", "work/beta")
	g("branch", "work/gamma")

	ctx, out, _ := newTestContext(proj, false)
	if err := runStart(ctx, []string{"alpha"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Depends on: beta (in progress, branch work/beta), gamma (done), nowhere (unknown issue)\n")
	if strings.Contains(out.String(), "== Shipping ==") {
		t.Errorf("start printed the shipping block before shipping:\n%s", out)
	}
}

func TestTransitionToShippingDryRunWarnsWhileADependencyIsOpen(t *testing.T) {
	proj, _ := shipProject(t, map[string]string{
		"alpha": depIssue("alpha", "in progress", "beta"),
		"beta":  depIssue("beta", "testing", ""),
		"solo":  depIssue("solo", "in progress", "gamma"),
		"gamma": depIssue("gamma", "done", ""),
	})
	warning := "⚠ depends_on beta is testing, not done: this issue ships after it."

	ctx, out, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"alpha", "--to", "shipping", "--dry-run"}); err != nil {
		t.Fatalf("a warning must not make the dry run fail: %v\n%s", err, out)
	}
	assertContains(t, out.String(), "✓ Ready: in progress → shipping")
	assertContains(t, out.String(), warning)

	ctx, out, _ = newTestContext(proj, true)
	if err := runTransition(ctx, []string{"alpha", "--to", "shipping", "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	var dry dryRunOutput
	if err := json.Unmarshal(out.Bytes(), &dry); err != nil {
		t.Fatal(err)
	}
	if !dry.Ready || len(dry.Warnings) != 1 || !strings.Contains(dry.Warnings[0], "beta is testing") {
		t.Errorf("dry run json = %+v", dry)
	}

	ctx, out, _ = newTestContext(proj, false)
	if err := runTransition(ctx, []string{"solo", "--to", "shipping", "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "⚠") {
		t.Errorf("warned with every dependency done:\n%s", out)
	}
}

func TestShippingGuidanceNamesTheMainCheckoutBranch(t *testing.T) {
	proj, g := shipProject(t, map[string]string{
		"alpha":  depIssue("alpha", "in progress", "beta"),
		"beta":   depIssue("beta", "in progress", ""),
		"nobase": depIssue("nobase", "shipping", ""),
	})
	root := proj.WorkDir
	writeDispatches(t, proj, "alpha", tracker.Dispatch{At: time.Now(), Session: "s", Agent: "claude", BaseBranch: "main", BaseSHA: g("rev-parse", "HEAD")})

	ctx, out, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"alpha", "--to", "shipping"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"== Shipping ==\nMain checkout: " + root + " on main\n",
		"Ships into main: fast-forward it in the main checkout.",
		"Ships after beta (in progress)\n⚠ beta is not done: fast-forward only after it ships, or ask the lead.",
	} {
		assertContains(t, out.String(), want)
	}

	// The main checkout moved to another branch: stop and ask the lead.
	g("checkout", "-q", "-b", "integration")
	ctx, out, _ = newTestContext(proj, false)
	if err := runStart(ctx, []string{"alpha"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Main checkout: "+root+" on integration\n⚠ The main checkout has integration checked out, not main (this issue's base). Stop and ask the lead")

	// No recorded base: anything but main or master is the odd one out.
	ctx, out, _ = newTestContext(proj, false)
	if err := runStart(ctx, []string{"nobase"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "⚠ The main checkout has integration checked out, not main. Stop and ask the lead")
	g("checkout", "-q", "main")
	ctx, out, _ = newTestContext(proj, false)
	if err := runStart(ctx, []string{"nobase"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Ships into main")
	if strings.Contains(out.String(), "Ships after") {
		t.Errorf("printed dependencies for an issue with none:\n%s", out)
	}
}
