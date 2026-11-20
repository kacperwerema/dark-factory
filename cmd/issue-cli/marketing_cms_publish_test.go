package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var (
	piecePath   = []string{"brief", "draft", "owner review", "publish", "done"}
	seoPassPath = []string{"brief", "draft", "owner review", "publish", "measure", "done"}
)

// effectivePrompts is each status's prompt as an issue of type typ reads
// it: the type's own statuses: over the base ones.
func effectivePrompts(t *testing.T, wf *tracker.WorkflowConfig, typ string) map[string]string {
	t.Helper()
	eff := wf.ForIssue(&tracker.Issue{Type: typ, Status: "brief"})
	prompts := map[string]string{}
	for _, s := range eff.Statuses {
		prompts[s.Name] = s.Prompt
	}
	return prompts
}

func assertPromptSays(t *testing.T, typ, status, prompt string, words ...string) {
	t.Helper()
	for _, w := range words {
		if !strings.Contains(prompt, w) {
			t.Errorf("%s %s prompt doesn't say %q:\n%s", typ, status, w, prompt)
		}
	}
}

// TestPieceAndSeoPassPublishThroughCms is AC1-AC4 on the bundled
// template: piece's publish sends the approved file through the
// department's cms connector and records the URL it prints, another place
// uses its own connector; seo-pass reads an existing page before updating
// it and publishes a new post, one output publish per output; with no cms
// connector both stop and ask the owner for the link; piece's brief names
// site (cms).
func TestPieceAndSeoPassPublishThroughCms(t *testing.T) {
	wf := loadMarketingWorkflow(t)
	exec := "issue-cli connector exec <department>/cms -- sh departments/<department>/cms-ghost.sh"
	piece := effectivePrompts(t, wf, "piece")
	assertPromptSays(t, "piece", "publish", piece["publish"],
		"issue-cli outputs --issue <slug> --state approved --json", "`file`",
		exec+" publish --file <the approved file> --title \"<title>\" --kind post|page",
		`{"id","status","url"}`, "`scheduled`",
		"issue-cli output publish <id> --url <url>", "## Published",
		"Another place the brief names", "use that place's connector instead",
		"never retry with a changed file")
	seo := effectivePrompts(t, wf, "seo-pass")
	assertPromptSays(t, "seo-pass", "publish", seo["publish"],
		"issue-cli outputs --issue <slug> --state approved --json",
		exec+" get --slug <its slug>", exec+" update --id <the id it printed> --file <the approved file>",
		exec+" publish --file <the approved file> --title \"<title>\" --kind post",
		"One `issue-cli output publish <id> --url <url>` per output", "## Published", "move to measure")
	if g, u := strings.Index(seo["publish"], " get --slug"), strings.Index(seo["publish"], " update --id"); g < 0 || u < g {
		t.Errorf("seo-pass publish doesn't read the page (get --slug) before update --id")
	}
	for typ, p := range map[string]string{"piece": piece["publish"], "seo-pass": seo["publish"]} {
		assertPromptSays(t, typ, "publish", p, "No `cms` connector", "publish nothing yourself", "`answer:` comment", "end your turn")
	}
	assertPromptSays(t, "piece", "brief", piece["brief"], "`site (cms)`", "`issue-cli connectors`", "post or page")
	// The other types keep their own publish: the base prompt names no cms.
	if strings.Contains(effectivePrompts(t, wf, "social")["publish"], "/cms") {
		t.Error("social's publish names the cms connector")
	}
}

// TestBriefAndDraftMakeNoCmsCall is AC5: piece's and seo-pass's brief and
// draft say no cms call and hold no connector exec line (the drafts stay
// on the shelf), and owner review → publish stays owner_only on both
// paths, the only owner_only step.
func TestBriefAndDraftMakeNoCmsCall(t *testing.T) {
	wf := loadMarketingWorkflow(t)
	for typ, path := range map[string][]string{"piece": piecePath, "seo-pass": seoPassPath} {
		prompts := effectivePrompts(t, wf, typ)
		for _, status := range []string{"brief", "draft"} {
			p := prompts[status]
			if strings.Contains(p, "connector exec") || strings.Contains(p, "cms-ghost.sh") {
				t.Errorf("%s %s prompt calls a connector:\n%s", typ, status, p)
			}
		}
		assertPromptSays(t, typ, "draft", prompts["draft"], "Make no `cms` call while drafting")
		assertChannelPath(t, wf, typ, path)
	}
	assertPromptSays(t, "piece", "brief", effectivePrompts(t, wf, "piece")["brief"], "Make no `cms` call")
	proj := installMarketingWith(t, marketingWorkflow(t))
	assertMarketingLintClean(t, proj, "with the cms publish prompts")
}

// TestDepartmentsDocNotesTheCmsPublishSplice is AC6: docs/departments.md
// says what publish calls and names the splice a live department makes.
func TestDepartmentsDocNotesTheCmsPublishSplice(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "departments.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	for _, want := range []string{
		"#### Publishing to the site", "cms-ghost.sh publish", "get --slug", "update --id", "`answer:`",
		"**piece and seo-pass publish through `cms`", "types.piece.statuses", "types.seo-pass.statuses", "cms-publish",
	} {
		assertContains(t, doc, want)
	}
}
