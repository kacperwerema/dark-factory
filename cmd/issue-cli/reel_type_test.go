package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/cmd/issue-cli/templates"
	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var reelPath = []string{"brief", "script", "render", "review", "publish", "done"}

// installVideo installs Video from its template in a sandbox project.
func installVideo(t *testing.T) *tracker.Project {
	t.Helper()
	asLead(t)
	proj, cfg := deptTemplateProject(t)
	ctx, _ := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "video", "--partner-path", filepath.Join(t.TempDir(), "studio")}); err != nil {
		t.Fatal(err)
	}
	return reloadDeptProject(t, cfg)
}

// replaceLine swaps one line of the issue file (a declaration).
func replaceIssueLine(t *testing.T, path, old, new string) {
	t.Helper()
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), old) {
		t.Fatalf("%s has no line %q", path, old)
	}
	writeDeptFile(t, path, strings.Replace(string(body), old, new, 1))
}

// TestReelWalksWithOneOwnerReview is AC2 and AC3 (and AC5's install): a
// reel walks brief → script → render → review → publish → done with YOLO
// off. brief → script, script → render and render → review need no
// approval; the legal check holds script → render until the claim is
// authorised, the track licensed and the AI content labelled; review →
// publish waits for the shelf approval and the owner's publish stamp
// (owner_only); publish → done asks for no third approval.
func TestReelWalksWithOneOwnerReview(t *testing.T) {
	proj := installVideo(t)
	bundled, _ := templates.FS.ReadFile("departments/video/reel-legal.yaml")
	installed, err := os.ReadFile(filepath.Join(proj.Root(), "departments", "video", "reel-legal.yaml"))
	if err != nil || string(installed) != string(bundled) {
		t.Fatalf("department add video didn't install the bundled reel-legal.yaml (%v)", err)
	}

	ctx, out, _ := newTestContext(proj, false)
	if err := runCreate(ctx, []string{"--department", "video", "--type", "reel", "--title", "Dinner plan reel"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(proj.IssueDir, "dinner-plan-reel.md")
	issue := loadIssueByPath(t, proj.IssueDir, path)
	if issue.Type != "reel" || issue.Status != "brief" {
		t.Fatalf("new reel: type %q status %q\n%s", issue.Type, issue.Status, out)
	}
	slug := issue.Slug

	if err := runTransition(ctx, []string{slug, "--to", "script"}); err == nil || !strings.Contains(err.Error(), "Reel brief") {
		t.Fatalf("brief → script with no brief: err = %v", err)
	}
	appendBody(t, path, "## Reel brief\n\nOne week of dinners planned in two minutes; parents; 20 s; Instagram Reels; app demo.\n")
	if err := runTransition(ctx, []string{slug, "--to", "script"}); err != nil {
		t.Fatalf("brief → script: %v\n%s", err, out)
	}
	assertContains(t, loadIssueByPath(t, proj.IssueDir, path).BodyRaw, "## Legal check\n")

	appendBody(t, path, "\n## Script\n\nHigh protein dinners, planned in two minutes. Download the app.\n")
	replaceIssueLine(t, path, "- music: <none, or the track and where it is from>", "- music: Summer Vibes")
	replaceIssueLine(t, path, "- ai_content: <yes when any person, voice or footage is AI-made, else no>", "- ai_content: yes")
	replaceIssueLine(t, path, "- face: <none, avatar or real>", "- face: none")

	err = runTransition(ctx, []string{slug, "--to", "render"})
	if err == nil {
		t.Fatal("script → render passed a reel with an unauthorised claim, an unlicensed track and no AI label")
	}
	for _, want := range []string{"claims: \"high protein\"", "music: the track \"Summer Vibes\" has no licence", "ai-label: AI-generated content with no label", "issue-cli reel-legal " + slug} {
		assertContains(t, err.Error(), want)
	}
	ctx2, out2, _ := newTestContext(proj, false)
	err = runReelLegal(ctx2, []string{slug})
	var code *exitCodeError
	if !errors.As(err, &code) || code.Code != 1 {
		t.Fatalf("reel-legal on a held reel: err = %v", err)
	}
	assertContains(t, out2.String(), "reel-legal: HOLD (3 finding(s))")

	replaceIssueLine(t, path, "- music_licence: <the licence that covers ads on Instagram and TikTok, when there is music>", "- music_licence: Epidemic Sound, commercial social licence")
	replaceIssueLine(t, path, "- ai_label: <yes: the platform's AI label when it goes out, and a note on screen or in the caption>", "- ai_label: yes")
	replaceIssueLine(t, path, "- claim: <phrase> = <its authorisation, one line per authorised claim>", "- claim: high protein = Annex to 1924/2006, at least 20% of energy from protein")
	ctx2, out2, _ = newTestContext(proj, false)
	if err := runReelLegal(ctx2, []string{slug}); err != nil {
		t.Fatalf("reel-legal on the fixed reel: %v\n%s", err, out2)
	}
	assertContains(t, out2.String(), "reel-legal: PASS")
	if err := runTransition(ctx, []string{slug, "--to", "render"}); err != nil {
		t.Fatalf("script → render on the fixed reel: %v", err)
	}

	if err := runTransition(ctx, []string{slug, "--to", "review"}); err == nil || !strings.Contains(err.Error(), "Render") {
		t.Fatalf("render → review with ## Render open: err = %v", err)
	}
	tickSections(t, path, "Render")
	if err := runTransition(ctx, []string{slug, "--to", "review"}); err == nil {
		t.Fatal("render → review with no video on the shelf passed")
	}
	mp4 := filepath.Join(t.TempDir(), "reel.mp4")
	mustWrite(t, mp4, "not really an mp4")
	o, err := outputs.Add(proj, outputs.AddInput{Title: "Dinner plan reel", Kind: "video", File: mp4, Issue: slug, Department: "video", By: "agent"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := runTransition(ctx, []string{slug, "--to", "review"}); err != nil {
		t.Fatalf("render → review: %v", err)
	}

	if err := runTransition(ctx, []string{slug, "--to", "publish"}); err == nil || !strings.Contains(err.Error(), "shelf") {
		t.Fatalf("review → publish before the owner approved the reel on the shelf: err = %v", err)
	}
	if _, err := outputs.Review(proj, o.ID, outputs.StateApproved, "good", "", "owner", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := runTransition(ctx, []string{slug, "--to", "publish"}); err == nil || !strings.Contains(err.Error(), "publish") {
		t.Fatalf("review → publish with the shelf approval but no owner stamp: err = %v", err)
	}
	body, _ := os.ReadFile(path)
	writeDeptFile(t, path, strings.Replace(string(body), "status: \"review\"", "status: \"review\"\nhuman_approval: \"publish\"", 1))
	if err := runTransition(ctx, []string{slug, "--to", "publish"}); err != nil {
		t.Fatalf("review → publish with the shelf approval and the owner's stamp: %v", err)
	}

	if err := runTransition(ctx, []string{slug, "--to", "done"}); err == nil || !strings.Contains(err.Error(), "Published") {
		t.Fatalf("publish → done with ## Published open: err = %v", err)
	}
	tickAll(t, path)
	if err := runTransition(ctx, []string{slug, "--to", "done"}); err != nil {
		t.Fatalf("publish → done with no third stamp: %v", err)
	}
}

// TestReelLegalWithoutItsRulesFile is AC5's other half: a project that
// deleted its reel-legal.yaml gets a refusal naming department update,
// never a pass.
func TestReelLegalWithoutItsRulesFile(t *testing.T) {
	proj := installVideo(t)
	ctx, _, _ := newTestContext(proj, false)
	if err := runCreate(ctx, []string{"--department", "video", "--type", "reel", "--title", "No rules reel"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(proj.IssueDir, "no-rules-reel.md")
	slug := loadIssueByPath(t, proj.IssueDir, path).Slug
	appendBody(t, path, "## Script\n\nPlan your dinners.\n\n## Legal check\n- music: none\n- ai_content: no\n- face: none\n")
	if err := os.Remove(filepath.Join(proj.Root(), "departments", "video", "reel-legal.yaml")); err != nil {
		t.Fatal(err)
	}
	err := runReelLegal(ctx, []string{slug})
	if err == nil || !strings.Contains(err.Error(), "reel-legal.yaml") || !strings.Contains(err.Error(), "department update") {
		t.Fatalf("reel-legal with no rules file: err = %v", err)
	}
}

// TestVideoTypesKeepTheStudioGates is AC4 and D-2: the studio type keeps
// every base status but render and its six owner_only gates; the reel's
// only owner_only gate is publish → done, and its review → publish waits
// on the shelf approval instead of a stamp.
func TestVideoTypesKeepTheStudioGates(t *testing.T) {
	proj := installVideo(t)
	wf, err := proj.WorkflowFor("video")
	if err != nil {
		t.Fatal(err)
	}
	if wf.DefaultType != "studio" {
		t.Errorf("default_type %q, want studio", wf.DefaultType)
	}
	owners := func(typ string) map[string]bool {
		eff := wf.ForIssue(&tracker.Issue{Type: typ, Status: "brief"})
		got := map[string]bool{}
		for _, tr := range eff.Transitions {
			for _, a := range tr.Actions {
				if a.Type == "require_human_approval" && a.OwnerOnly {
					got[tr.From+" → "+tr.To] = true
				}
			}
		}
		return got
	}
	studio := owners("studio")
	for _, g := range []string{"proposal → script", "script → scene plan", "scene plan → assets", "assets → compose", "review → publish", "publish → done"} {
		if !studio[g] {
			t.Errorf("studio: %s is not owner_only", g)
		}
		delete(studio, g)
	}
	if len(studio) > 0 {
		t.Errorf("studio: owner_only gates the design doesn't name: %v", studio)
	}
	if p := wf.Types["studio"].Path; strings.Contains(strings.Join(p, ","), "render") {
		t.Errorf("studio path %v walks render", p)
	}
	reel := owners("reel")
	if len(reel) != 2 || !reel["review → publish"] || !reel["review → done"] {
		t.Errorf("reel owner_only gates %v, want review → publish and review → done (shared privately)", reel)
	}
	if strings.Join(wf.Types["reel"].Path, ",") != strings.Join(reelPath, ",") {
		t.Errorf("reel path %v, want %v", wf.Types["reel"].Path, reelPath)
	}
	eff := wf.ForIssue(&tracker.Issue{Type: "reel", Status: "review"})
	shelf := map[string]bool{}
	approvals := 0
	for _, tr := range eff.Transitions {
		for _, a := range tr.Actions {
			if a.Type == "require_human_approval" {
				approvals++
			}
			if tr.From == "review" && a.Rule == "outputs_approved: video" {
				shelf[tr.To] = true
			}
		}
	}
	if !shelf["publish"] || !shelf["done"] {
		t.Errorf("reel review → publish and review → done must both wait for outputs_approved: video, got %v", shelf)
	}
	if approvals != 2 {
		t.Errorf("reel has %d stamps, want 2 (publish, or done without publishing) besides the shelf review", approvals)
	}
}

// TestVideoDocsDescribeTheReelType is AC6: docs/departments.md describes
// the reel type, its legal check and how a project changes its own copy.
func TestVideoDocsDescribeTheReelType(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "departments.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	i := strings.Index(doc, "\n#### Reels\n")
	if i < 0 {
		t.Fatal("docs/departments.md has no #### Reels section")
	}
	sec := doc[i:]
	if j := strings.Index(sec[1:], "\n### "); j > 0 {
		sec = sec[:j+1]
	}
	for _, want := range []string{
		"brief → script → render → review → publish → done",
		"reel_legal_check", "issue-cli reel-legal", "departments/video/reel-legal.yaml",
		"1924/2006", "Omnibus", "art. 50", "consent", "trademark",
		"outputs_approved: video", "owner_only", "no third stamp",
		"workflows/video.yaml", "issue-cli workflow upgrade --department video",
		"6 owner_only gates",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("#### Reels doesn't say %q", want)
		}
	}
}

// newReel installs Video and creates one reel issue in it.
func newReel(t *testing.T, title string) (*tracker.Project, string, string) {
	t.Helper()
	proj := installVideo(t)
	ctx, out, _ := newTestContext(proj, false)
	if err := runCreate(ctx, []string{"--department", "video", "--type", "reel", "--title", title}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	path := filepath.Join(proj.IssueDir, tracker.Slugify(title)+".md")
	return proj, path, loadIssueByPath(t, proj.IssueDir, path).Slug
}

// TestReelBriefCarriesSourceMediaAndPronunciation is AC1: the briefing a
// reel's worker gets at brief names where the media come from (copied onto
// the work branch, never a gitignored media/ folder or an unmerged branch),
// and at render the brand pronunciation per voice.
func TestReelBriefCarriesSourceMediaAndPronunciation(t *testing.T) {
	proj, path, slug := newReel(t, "Source media reel")
	brief := func() string {
		t.Helper()
		ctx, out, _ := newTestContext(proj, false)
		if err := runDispatchPrompt(ctx, []string{slug}); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	at := brief()
	for _, want := range []string{"where the reel's media come from", "copy each onto your work branch", "a gitignored media/ folder", "an unmerged branch is not a source"} {
		assertContains(t, at, want)
	}
	body, _ := os.ReadFile(path)
	writeDeptFile(t, path, strings.Replace(string(body), "status: \"brief\"", "status: \"render\"", 1))
	at = brief()
	for _, want := range []string{"Brand pronunciation: listen to the brand and product names in the first take of each voice", "reels/pronunciation.md, one line per voice placement", "only into the TTS text"} {
		assertContains(t, at, want)
	}
}

// TestReelDoneWithoutPublishing is AC2: a reel the owner shares privately
// goes review → done without publish, once the newest version is approved
// on the shelf and the owner's words for done are recorded (owner_only:
// YOLO never passes it; a lead relays them with approve --quote).
func TestReelDoneWithoutPublishing(t *testing.T) {
	proj, path, slug := newReel(t, "Private reel")
	body, _ := os.ReadFile(path)
	writeDeptFile(t, path, strings.Replace(string(body), "status: \"brief\"", "status: \"review\"", 1))
	mp4 := filepath.Join(t.TempDir(), "reel.mp4")
	mustWrite(t, mp4, "not really an mp4")
	o, err := outputs.Add(proj, outputs.AddInput{Title: "Private reel", Kind: "video", File: mp4, Issue: slug, Department: "video", By: "agent"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ctx, out, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{slug, "--to", "done"}); err == nil || !strings.Contains(err.Error(), "shelf") {
		t.Fatalf("review → done before the owner approved the reel on the shelf: err = %v", err)
	}
	if _, err := outputs.Review(proj, o.ID, outputs.StateApproved, "good", "", "owner", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := runTransition(ctx, []string{slug, "--to", "done"}); err == nil || !strings.Contains(err.Error(), "approv") {
		t.Fatalf("review → done with the shelf approval but no owner's word for done: err = %v", err)
	}
	lctx, lout, _ := newTestContext(proj, false)
	if err := runApprove(lctx, []string{slug, "--to", "done", "--quote", "I sent it to my sister, don't post it"}); err != nil {
		t.Fatalf("lead relays the owner's done: %v\n%s", err, lout)
	}
	if err := runTransition(ctx, []string{slug, "--to", "done"}); err != nil {
		t.Fatalf("review → done with the shelf approval and the owner's words: %v\n%s", err, out)
	}
	issue := loadIssueByPath(t, proj.IssueDir, path)
	if issue.Status != "done" {
		t.Fatalf("status %q, want done", issue.Status)
	}
	raw, _ := os.ReadFile(path)
	assertContains(t, string(raw), "I sent it to my sister, don't post it")
	if strings.Contains(issue.BodyRaw, "## Published") {
		t.Error("a reel done without publishing got a ## Published section")
	}
}
