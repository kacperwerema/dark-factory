package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// setupDocsProject is setupTestProject plus a rule doc in a folder, with
// every optional frontmatter key, and an open issue that cites it.
func setupDocsProject(t *testing.T) (tracker.Project, string) {
	t.Helper()
	proj, root := setupTestProject(t)
	if err := os.MkdirAll(filepath.Join(proj.DocsDir, "guide"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src", "core"), 0755); err != nil {
		t.Fatal(err)
	}
	doc := `---
title: "Macro Rules"
kind: rule
system: Macro
guard: "a change to the maths follows this document, or changes it first."
guards: [src/core, src/gone]
as_of: 2026-10-11
---

# How we count macros

The main instruction for the calorie logic.

## First step

1. Profile
2. Energy

## Second step

See [getting started](../getting-started.md#install) and [the web](https://example.com/x.md).
`
	if err := os.WriteFile(filepath.Join(proj.DocsDir, "guide", "rules.md"), []byte(doc), 0644); err != nil {
		t.Fatal(err)
	}
	issue := `---
title: "Fix the energy step"
status: "backlog"
system: "Macro"
created: "2026-10-20"
---

Follow docs/guide/rules.md for the energy step.
`
	if err := os.WriteFile(filepath.Join(proj.IssueDir, "fix-energy.md"), []byte(issue), 0644); err != nil {
		t.Fatal(err)
	}
	return proj, root
}

func TestDocsWbFirstDocSelected(t *testing.T) {
	proj, _ := setupDocsProject(t)
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	page := pageGet(t, ts.URL+"/p/test-project/docs")
	for _, want := range []string{
		`data-testid="docs.binder"`,
		`2 documents`,
		`<div class="wb-binder-group">guide</div>`,
		`href="/p/test-project/docs/getting-started" aria-current="page" data-testid="docs.item.getting-started"`,
		`data-testid="docs.item.guide/rules"`,
		`<h1 class="wb-doc-title" data-testid="docs.title">Getting Started</h1>`,
		`Welcome to the project.`,
		`href="/p/test-project/docs?classic=1" data-testid="docs.classic"`,
		`data-testid="tile.new_document"`,
		`Product hall`,
		`data-testid="docs.edit"`,
		`data-doc-edit="/p/test-project/docs/getting-started/edit-in-nvim"`,
		`value="About docs/getting-started.md: "`,
		`This document names no code.`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("docs page missing %q", want)
		}
	}
	// The plain doc declares no kind, system or guard: none is invented.
	for _, absent := range []string{`data-testid="docs.guard_rule"`, `data-testid="docs.system_decisions"`, `docs-nav-link`} {
		if strings.Contains(page, absent) {
			t.Errorf("docs page has %q", absent)
		}
	}
}

func TestDocsWbSelectedDoc(t *testing.T) {
	proj, _ := setupDocsProject(t)
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	page := pageGet(t, ts.URL+"/p/test-project/docs/guide/rules")
	for _, want := range []string{
		`href="/p/test-project/docs/guide/rules" aria-current="page" data-testid="docs.item.guide/rules"`,
		`data-testid="chip.docs_kind">rule</span>`,
		`data-testid="chip.docs_system">system: Macro</span>`,
		`as of 11 Oct`,
		`<h1 class="wb-doc-title" data-testid="docs.title">How we count macros</h1>`,
		`<div class="wb-doc-lead wb-md"><p>The main instruction for the calorie logic.</p></div>`,
		`<b>Guard rule:</b> a change to the maths follows this document, or changes it first.`,
		`<a class="wb-doc-section" href="#first-step">First step</a>`,
		`<a class="wb-doc-section" href="#second-step">Second step</a>`,
		`<h2 id="first-step">First step</h2>`,
		`<ol>`,
		`href="/p/test-project/docs/getting-started#install"`,
		`href="https://example.com/x.md"`,
		`<div class="wb-guard-path"><span>src/core</span></div>`,
		`<span>src/gone</span><span class="wb-guard-path-note">not in the repo</span>`,
		`Adds to the code map: system <b>Macro</b>`,
		`Research as of 11 Oct`,
		`href="/p/test-project/issue/fix-the-energy-step"`,
		`Fix the energy step`,
		`href="/p/test-project/decisions?system=Macro" data-testid="docs.system_decisions">Decisions of system Macro · 0</a>`,
		`value="About docs/guide/rules.md: "`,
		`href="/p/test-project/docs/guide/rules?classic=1" data-testid="docs.classic"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("selected doc missing %q", want)
		}
	}
	// The leading "# " heading is the title, not a second heading in the body.
	if strings.Count(page, "How we count macros") != 2 { // <title> and <h1>
		t.Errorf("the doc's H1 is repeated in the body")
	}
	if strings.Contains(page, "ZgotmplZ") {
		t.Error("html/template rejected a value (ZgotmplZ)")
	}
}

func TestDocsWbUnknownDoc(t *testing.T) {
	proj, _ := setupDocsProject(t)
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	if code, _ := getBody(t, ts.URL+"/p/test-project/docs/nope"); code != http.StatusNotFound {
		t.Errorf("unknown doc = %d, want 404", code)
	}
}

func TestDocsWbClassic(t *testing.T) {
	proj, _ := setupDocsProject(t)
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	// /docs?classic=1 redirects to the first doc, still classic.
	page := pageGet(t, ts.URL+"/p/test-project/docs?classic=1")
	for _, want := range []string{`data-testid="nav.sidebar"`, `class="docs-title">Getting Started</h1>`, `href="/p/test-project/docs/guide/rules?classic=1"`} {
		if !strings.Contains(page, want) {
			t.Errorf("classic docs missing %q", want)
		}
	}
	if strings.Contains(page, "docs.binder") {
		t.Error("classic docs renders the new binder")
	}
	page = pageGet(t, ts.URL+"/p/test-project/docs/guide/rules?classic=1")
	if !strings.Contains(page, `class="docs-title">Macro Rules</h1>`) {
		t.Error("classic doc page not rendered")
	}
}

func TestDocsWbEmpty(t *testing.T) {
	proj, _ := setupTestProject(t)
	proj.DocsDir = filepath.Join(t.TempDir(), "none")
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	page := pageGet(t, ts.URL+"/p/test-project/docs")
	for _, want := range []string{`data-testid="docs.empty"`, `data-testid="plan.docs_empty"`, `No documents yet`, `0 documents`} {
		if !strings.Contains(page, want) {
			t.Errorf("empty docs missing %q", want)
		}
	}
	if strings.Contains(page, `data-testid="docs.edit"`) {
		t.Error("empty docs offers Edit in nvim")
	}
}

func TestDocsWbEditInNvim(t *testing.T) {
	proj, _ := setupDocsProject(t)
	withMockTmuxSessions(t, nil)
	var gotSlug, gotPath string
	orig := launchDocEditor
	launchDocEditor = func(p *tracker.Project, slug, abs string) (BodyEditResponse, error) {
		gotSlug, gotPath = slug, abs
		return BodyEditResponse{Status: "launched", Session: "doc-edit-guide-rules", Message: "Opened in nvim."}, nil
	}
	t.Cleanup(func() { launchDocEditor = orig })
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp := pagePost(t, ts.URL+"/p/test-project/docs/guide/rules/edit-in-nvim", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("edit = %d", resp.StatusCode)
	}
	var out BodyEditResponse
	json.NewDecoder(resp.Body).Decode(&out)
	if out.Status != "launched" || gotSlug != "guide/rules" || gotPath != filepath.Join(proj.DocsDir, "guide", "rules.md") {
		t.Errorf("edit: resp %+v, slug %q, path %q", out, gotSlug, gotPath)
	}
	if resp := pagePost(t, ts.URL+"/p/test-project/docs/nope/edit-in-nvim", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("edit of an unknown doc = %d, want 404", resp.StatusCode)
	}
}

func TestDocFreshness(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	orig := docChangedAt
	t.Cleanup(func() { docChangedAt = orig })

	docChangedAt = func(string) (time.Time, string) { return now.AddDate(0, 0, -18), "commit" }
	f := docFreshness(docMeta{}, "x.md", now)
	if f.Label != "Last commit 11 Oct · 18 days ago" || f.Tone != "green" || f.Pct != 10 {
		t.Errorf("recent commit: %+v", f)
	}
	docChangedAt = func(string) (time.Time, string) { return now.AddDate(0, 0, -60), "file" }
	if f := docFreshness(docMeta{}, "x.md", now); f.Tone != "yellow" || !strings.HasPrefix(f.Label, "File saved") {
		t.Errorf("file mtime: %+v", f)
	}
	docChangedAt = func(string) (time.Time, string) { return now.AddDate(-1, 0, 0), "commit" }
	if f := docFreshness(docMeta{}, "x.md", now); f.Tone != "red" || f.Pct != 100 || !strings.Contains(f.Label, "2025") {
		t.Errorf("old commit: %+v", f)
	}
	// as_of wins over git.
	if f := docFreshness(docMeta{AsOf: "2026-10-28"}, "x.md", now); f.Label != "Research as of 28 Oct · yesterday" {
		t.Errorf("as_of: %+v", f)
	}
	docChangedAt = func(string) (time.Time, string) { return time.Time{}, "" }
	if f := docFreshness(docMeta{}, "x.md", now); f.Label != "No date" || f.Pct != 0 {
		t.Errorf("no date: %+v", f)
	}
}

func TestDocsWbHelpers(t *testing.T) {
	if title, rest, ok := leadingH1("\n# Title #\n\nBody"); !ok || title != "Title" || strings.TrimSpace(rest) != "Body" {
		t.Errorf("leadingH1 = %q %q %v", title, rest, ok)
	}
	if _, _, ok := leadingH1("## Not a title\n"); ok {
		t.Error("leadingH1 took an h2")
	}
	if lead, rest := splitLead("<p>a</p>\n<h2 id=\"x\">X</h2>"); lead != "<p>a</p>" || rest != `<h2 id="x">X</h2>` {
		t.Errorf("splitLead = %q %q", lead, rest)
	}
	if lead, _ := splitLead("<p>only</p>"); lead != "" {
		t.Errorf("a doc without sections has a lead: %q", lead)
	}
	known := map[string]bool{"a/b": true, "top": true}
	got := rewriteDocLinks(`<a href="b.md#s">x</a> <a href="../top.md">y</a> <a href="missing.md">z</a> <a href="#local">w</a>`, "/p/x", "a", known)
	for _, want := range []string{`href="/p/x/docs/a/b#s"`, `href="/p/x/docs/top"`, `href="missing.md"`, `href="#local"`} {
		if !strings.Contains(got, want) {
			t.Errorf("rewriteDocLinks: missing %q in %s", want, got)
		}
	}
	if c := docKindChip("Rule"); c.Tone != "ink" || c.Text != "rule" {
		t.Errorf("rule chip = %+v", c)
	}
	if c := docKindChip("log"); c.Tone != "paper" {
		t.Errorf("log chip = %+v", c)
	}
}

// TestDocsWbComponents renders each component on its own.
func TestDocsWbComponents(t *testing.T) {
	srv, err := NewServer(nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		data any
		want []string
	}{
		{"wb/binder", WbBinder{Count: "1 document", Groups: []WbBinderGroup{{Name: "rules", Items: []WbBinderItem{{Slug: "rules/a", Title: "A", URL: "/p/x/docs/rules/a", Current: true, Chips: []WbChip{docKindChip("rule")}}}}}, New: WbNewTile{Title: "New document", TestID: "new_document"}},
			[]string{`data-testid="docs.binder"`, `1 document`, `wb-binder-item--on`, `aria-current="page"`, `data-testid="docs.item.rules/a"`, `wb-chip--ink`, `data-testid="tile.new_document"`}},
		{"wb/binder-item", WbBinderItem{Slug: "a", Title: "A", URL: "/p/x/docs/a"}, []string{`href="/p/x/docs/a"`, `<span class="wb-binder-name">A</span>`}},
		{"wb/doc-head", &WbDoc{Title: "T", AsOf: "as of 1 Oct", Lead: "<p>lead</p>", Chips: []WbChip{docKindChip("knowledge")}}, []string{`docs.title">T</h1>`, `as of 1 Oct`, `<p>lead</p>`, `wb-chip--sand`}},
		{"wb/guard-rule", "keep it true", []string{`data-testid="docs.guard_rule"`, `keep it true`}},
		{"wb/doc-sections", []WbDocSection{{Title: "One", Href: "#one"}, {Title: "Two"}}, []string{`href="#one">One</a>`, `<span class="wb-doc-section">Two</span>`}},
		{"wb/doc-body", WbDoc{}.Body, []string{`data-testid="docs.body"`}},
		{"wb/doc-guards", &WbDocSide{System: "S", Guards: []WbGuardPath{{Path: "src"}}}, []string{`<span>src</span>`, `system <b>S</b>`}},
		{"wb/freshness", WbFreshness{Label: "Last commit 1 Oct · 3 days ago", Note: "n", Tone: "green", Pct: 10}, []string{`data-testid="docs.freshness"`, `wb-fresh-bar--green`, `width:10%`}},
		{"wb/doc-citer", WbDocCiter{Slug: "s", Text: "t", URL: "/p/x/issue/s", Chip: &WbChip{Tone: "green", Text: "agent on it"}}, []string{`href="/p/x/issue/s"`, `agent on it`}},
		{"wb/doc-agents", &WbDocSide{Path: "docs/a.md", SystemDecisions: &WbDocLink{Text: "Decisions of system S · 2", URL: "/p/x/decisions?system=S"}}, []string{`No open issue cites`, `Decisions of system S · 2`}},
		{"wb/doc-edit", "/p/x/docs/a/edit-in-nvim", []string{`data-doc-edit="/p/x/docs/a/edit-in-nvim"`, `Edit in nvim`}},
		{"wb/side-label", "Freshness", []string{`<div class="wb-side-label">Freshness</div>`}},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		if err := srv.tmpl.ExecuteTemplate(&buf, c.name, c.data); err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		for _, want := range c.want {
			if !strings.Contains(buf.String(), want) {
				t.Errorf("%s: missing %q in %s", c.name, want, buf.String())
			}
		}
	}
}
