package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// ref reads the repo's own docs: the CLI page's outline is a small part of
// the page, and one section comes back whole.
func TestRefOutlineAndSectionOfTheCLIPage(t *testing.T) {
	withCwd(t, filepath.Join("..", ".."))
	page, err := os.ReadFile(filepath.Join("docs", "CLI", "overview.md"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, stdout, _ := newTestContext(nil, false)
	if err := runRef(ctx, []string{"cli"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "### Adding a command  [L") || !strings.Contains(out, "issue-cli ref cli <heading words>") {
		t.Fatalf("outline:\n%s", out[:min(len(out), 2000)])
	}
	if len(out)*10 > len(page) {
		t.Fatalf("outline %d bytes for a %d byte page: not compact", len(out), len(page))
	}
	ctx, stdout, _ = newTestContext(nil, false)
	if err := runRef(ctx, []string{"cli", "adding", "a", "command"}); err != nil {
		t.Fatal(err)
	}
	if s := stdout.String(); !strings.HasPrefix(s, "docs/CLI/overview.md lines ") || !strings.Contains(s, "registerCommand") || strings.Contains(s, "### Adding a lint check") {
		t.Fatalf("section:\n%s", s)
	}
	ctx, stdout, _ = newTestContext(nil, false)
	if err := runRef(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if s := stdout.String(); !strings.Contains(s, "cli              docs/CLI/overview.md") || !strings.Contains(s, "lead-session") {
		t.Fatalf("list:\n%s", s)
	}
}

// The worker briefing names codemap, affected and ref in its own short
// section, with --project injected.
func TestWorkerBriefingNamesTheShortForms(t *testing.T) {
	issue := &tracker.Issue{Slug: "factory/x", Title: "T", Status: "idea", System: "Factory", BodyRaw: "Body."}
	prompt := tracker.BuildAgentPromptYOLO(&tracker.Project{Slug: "dark-factory"}, issue, nil, "", "", false)
	for _, want := range []string{"## Reading code and docs", "issue-cli --project dark-factory codemap", `issue-cli --project dark-factory affected "$ISSUE_VIEWER_ISSUE_SLUG"`, "issue-cli --project dark-factory ref <page>"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("briefing misses %q", want)
		}
	}
}
