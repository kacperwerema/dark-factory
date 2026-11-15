package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// writeFoundationDocs writes the files a settled foundation leaves in the
// Marketing department.
func writeFoundationDocs(t *testing.T, proj *tracker.Project, names ...string) {
	t.Helper()
	for _, n := range names {
		writeDeptFile(t, filepath.Join(proj.Root(), "departments", "marketing", n), "# "+n+"\n\nSettled with the owner.\n")
	}
}

// tickAll ticks every box in the issue at path.
func tickAll(t *testing.T, path string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeDeptFile(t, path, strings.ReplaceAll(string(body), "- [ ]", "- [x]"))
}

// Basic first: a channel brief can't move to draft before the brand and
// the ICP are written, and the refusal says how to start them.
func TestChannelBriefNeedsTheBrandFoundation(t *testing.T) {
	proj := makeMarketingProject(t)
	seoIssueAt(t, proj, "seo-pass", "brief")
	ctx, _, _ := newTestContext(proj, false)
	err := runTransition(ctx, []string{"seo/pass", "--to", "draft"})
	if err == nil {
		t.Fatal("brief → draft without brand.md should fail")
	}
	for _, want := range []string{"departments/marketing/brand.md is missing", "issue-cli create --department marketing --type foundation"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("want %q in:\n%v", want, err)
		}
	}

	writeFoundationDocs(t, proj, "brand.md")
	if err := runTransition(ctx, []string{"seo/pass", "--to", "draft"}); err == nil || !strings.Contains(err.Error(), "icp.md is missing") {
		t.Fatalf("brand.md alone: err = %v, want icp.md missing", err)
	}
	writeFoundationDocs(t, proj, "icp.md")
	if err := runTransition(ctx, []string{"seo/pass", "--to", "draft"}); err != nil {
		t.Fatalf("with brand.md and icp.md: %v", err)
	}
}

// The foundation walks research → brand → icp → owner review → publish →
// done in research mode, and can't finish before its docs are written.
func TestFoundationWalksResearchToPublishedDocs(t *testing.T) {
	proj := makeMarketingProject(t)
	ctx, out, _ := newTestContext(proj, false)
	if err := runCreate(ctx, []string{"--department", "marketing", "--type", "foundation", "--title", "Brand foundation: plumbing"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(proj.IssueDir, "brand-foundation-plumbing.md")
	issue := loadIssueByPath(t, proj.IssueDir, path)
	if issue.Type != "foundation" || issue.Status != "research" {
		t.Fatalf("new foundation: type %q status %q\n%s", issue.Type, issue.Status, out)
	}
	slug := issue.Slug

	if err := runTransition(ctx, []string{slug, "--to", "brand"}); err == nil || !strings.Contains(err.Error(), "Research") {
		t.Fatalf("research → brand with no ## Research: err = %v", err)
	}
	body, _ := os.ReadFile(path)
	writeDeptFile(t, path, string(body)+"\n## Research\n\n"+researchLinks(5))
	if err := runTransition(ctx, []string{slug, "--to", "brand"}); err != nil {
		t.Fatalf("research → brand: %v", err)
	}
	assertContains(t, loadIssueByPath(t, proj.IssueDir, path).BodyRaw, "## Brand")
	for _, to := range []string{"icp", "owner review"} {
		if err := runTransition(ctx, []string{slug, "--to", to}); err == nil {
			t.Fatalf("→ %s with the last section's boxes open should fail", to)
		}
		tickAll(t, path)
		if err := runTransition(ctx, []string{slug, "--to", to}); err != nil {
			t.Fatalf("→ %s: %v", to, err)
		}
	}
	if err := runTransition(ctx, []string{slug, "--to", "publish"}); err == nil || !strings.Contains(err.Error(), "no report on the shelf") {
		t.Fatalf("owner review → publish with no report on the shelf: err = %v", err)
	}
	rep := shelveReport(t, proj, slug, "v1")
	if _, err := outputs.Review(proj, rep.ID, outputs.StateApproved, "settled", "", "owner", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := runTransition(ctx, []string{slug, "--to", "publish"}); err == nil || !strings.Contains(err.Error(), "publish") {
		t.Fatalf("owner review → publish without approval: err = %v", err)
	}

	body, _ = os.ReadFile(path)
	writeDeptFile(t, path, strings.Replace(string(body), "status: \"owner review\"", "status: \"owner review\"\nhuman_approval: \"publish\"", 1))
	if err := runTransition(ctx, []string{slug, "--to", "publish"}); err != nil {
		t.Fatalf("approved owner review → publish: %v", err)
	}
	issue = loadIssueByPath(t, proj.IssueDir, path)
	assertContains(t, issue.BodyRaw, "brand.md and icp.md hold the approved text")
	tickAll(t, path)
	if err := runTransition(ctx, []string{slug, "--to", "done"}); err == nil || !strings.Contains(err.Error(), "brand.md is missing") {
		t.Fatalf("publish → done before the docs are written: err = %v", err)
	}
	writeFoundationDocs(t, proj, "brand.md", "icp.md")
	if err := runTransition(ctx, []string{slug, "--to", "done"}); err != nil {
		t.Fatalf("publish → done: %v", err)
	}
}

// researchLinks is a ## Research body with n distinct sources.
func researchLinks(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "- https://competitor%d.example/drains: what they say about drains and boilers\n", i)
	}
	return b.String()
}

// shelveReport puts a foundation report for slug on the shelf.
func shelveReport(t *testing.T, proj *tracker.Project, slug, text string) *outputs.Output {
	t.Helper()
	src := filepath.Join(t.TempDir(), "brand-icp.md")
	writeDeptFile(t, src, "# Brand and ICP\n\n"+text+"\n")
	o, err := outputs.Add(proj, outputs.AddInput{Title: "Brand and ICP: plumbing", Kind: "report", File: src, Issue: slug, Department: "marketing", By: "agent"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// foundationAt writes a foundation issue at status with the given body.
func foundationAt(t *testing.T, proj *tracker.Project, status, body string) string {
	t.Helper()
	path := filepath.Join(proj.IssueDir, "foundation.md")
	writeDeptFile(t, path, "---\ntitle: \"Brand foundation: plumbing\"\nstatus: \""+status+"\"\ntype: \"foundation\"\ndepartment: \"Marketing\"\n---\n\nResearch plumbing.\n\n"+body)
	return path
}

// research → brand counts the sources: 4 distinct links are refused, 5
// pass; the same link written twice (or with a trailing slash) counts once.
func TestFoundationResearchNeedsFiveLinks(t *testing.T) {
	proj := makeMarketingProject(t)
	foundationAt(t, proj, "research", "## Research\n\n"+researchLinks(4)+"- https://competitor1.example/drains/ again, with a slash.\n"+strings.Repeat("Long notes on the market without a link. ", 20)+"\n")
	ctx, _, _ := newTestContext(proj, false)
	slug := loadIssueByPath(t, proj.IssueDir, filepath.Join(proj.IssueDir, "foundation.md")).Slug
	err := runTransition(ctx, []string{slug, "--to", "brand"})
	if err == nil || !strings.Contains(err.Error(), "has 4 distinct http(s) links, need ≥5") {
		t.Fatalf("research → brand with 4 links: err = %v", err)
	}
	foundationAt(t, proj, "research", "## Research\n\n"+researchLinks(5))
	if err := runTransition(ctx, []string{slug, "--to", "brand"}); err != nil {
		t.Fatalf("research → brand with 5 links: %v", err)
	}
}

// The publish gate and the shelf agree: with an older report approved and
// a newer one (or a newer version of it) still in review, publish is
// refused even with the issue's publish approval.
func TestFoundationPublishWaitsForTheNewestShelfVersion(t *testing.T) {
	proj := makeMarketingProject(t)
	path := foundationAt(t, proj, "owner review", "## Brand\n\n- [x] done\n\n## ICP\n\n- [x] done\n")
	body, _ := os.ReadFile(path)
	writeDeptFile(t, path, strings.Replace(string(body), "status: \"owner review\"", "status: \"owner review\"\nhuman_approval: \"publish\"", 1))
	slug := loadIssueByPath(t, proj.IssueDir, path).Slug
	ctx, _, _ := newTestContext(proj, false)

	old := shelveReport(t, proj, slug, "first draft")
	if _, err := outputs.Review(proj, old.ID, outputs.StateApproved, "fine", "", "owner", time.Now()); err != nil {
		t.Fatal(err)
	}
	newer := shelveReport(t, proj, slug, "second draft")
	err := runTransition(ctx, []string{slug, "--to", "publish"})
	if err == nil || !strings.Contains(err.Error(), newer.ID+" v1 still in review") {
		t.Fatalf("publish with the newest output in review: err = %v", err)
	}
	if _, err := outputs.Review(proj, newer.ID, outputs.StateApproved, "yes", "", "owner", time.Now()); err != nil {
		t.Fatal(err)
	}

	// A revision of the approved report puts it back in review.
	src := filepath.Join(t.TempDir(), "v2.md")
	writeDeptFile(t, src, "# Brand and ICP\n\nwith the owner's answers\n")
	if _, err := outputs.Revise(proj, newer.ID, src, "answers", "agent", time.Now()); err != nil {
		t.Fatal(err)
	}
	err = runTransition(ctx, []string{slug, "--to", "publish"})
	if err == nil || !strings.Contains(err.Error(), newer.ID+" v2 still in review") {
		t.Fatalf("publish with v2 in review and v1 approved: err = %v", err)
	}
	if _, err := outputs.Review(proj, newer.ID, outputs.StateApproved, "yes", "", "owner", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := runTransition(ctx, []string{slug, "--to", "publish"}); err != nil {
		t.Fatalf("publish with the newest version approved: %v", err)
	}
}

// Market size runs on its own path and gates nothing: channel work doesn't
// wait for market.md.
func TestMarketSizeIsItsOwnPath(t *testing.T) {
	proj := makeMarketingProject(t)
	ctx, out, _ := newTestContext(proj, false)
	if err := runProcess(ctx, []string{"workflow", "--department", "marketing"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "research → market size → owner review → publish → done")
	assertContains(t, out.String(), "research → brand → icp → owner review → publish → done")

	writeFoundationDocs(t, proj, "brand.md", "icp.md")
	seoIssueAt(t, proj, "piece", "brief")
	if err := runTransition(ctx, []string{"seo/pass", "--to", "draft"}); err != nil {
		t.Fatalf("a piece needs no market.md: %v", err)
	}
}
