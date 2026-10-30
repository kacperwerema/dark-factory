package main

import (
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/agentrun"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The Docs page (L2): the project's binder of documents. On the left the
// binder (every Markdown file under the docs dir), in the middle the selected
// document rendered from its file, on the right what the document guards,
// how fresh it is, which issues and decisions it ties to, and the ways to
// edit it or ask the CEO about it. Everything comes from the files: the
// frontmatter keys kind, system, guard, guards and as_of are optional, and a
// key a doc leaves out is left out of the page too.
// Design: issues/Factory/designs/workshop-board/L2-docs.

// docsFreshDays is the age (days) the freshness bar is full at.
const docsFreshDays = 180

// docsCiters is how many citing issues the side lists before "+N more".
const docsCiters = 5

// DocsWbData is the whole Docs page.
type DocsWbData struct {
	// Title is the browser tab's title.
	Title      string
	Header     WbHeader
	Binder     WbBinder
	Doc        *WbDoc
	Side       *WbDocSide
	Message    WbMessageBox
	Empty      WbPlanCard
	ClassicURL string
}

// WbBinder is the list of documents: groups by folder, the selected one
// marked, and the tile for a new document.
type WbBinder struct {
	Count  string
	Groups []WbBinderGroup
	New    WbNewTile
}

// WbBinderGroup is one docs folder ("" for the docs dir itself).
type WbBinderGroup struct {
	Name  string
	Items []WbBinderItem
}

// WbBinderItem is one document in the binder.
type WbBinderItem struct {
	Slug, Title, URL string
	Chips            []WbChip
	Current          bool
}

// WbDoc is the selected document.
type WbDoc struct {
	Slug, Title, Path string
	Chips             []WbChip
	// AsOf is the frontmatter's as_of date, as a label.
	AsOf string
	// Lead is what stands before the first "## " section.
	Lead template.HTML
	// Guard is the frontmatter's guard rule.
	Guard    string
	Sections []WbDocSection
	Body     template.HTML
}

// WbDocSection is a "## " section: a chip linking to its heading.
type WbDocSection struct {
	Title, Href string
}

// WbDocSide is the right panel.
type WbDocSide struct {
	Guards    []WbGuardPath
	System    string
	Fresh     WbFreshness
	Citers    []WbDocCiter
	More      int
	Decisions []WbDocCiter
	// SystemDecisions links the system's decisions; Count is how many.
	SystemDecisions *WbDocLink
	EditURL         string
	Path            string
}

// WbGuardPath is a code path a document guards; Missing when the project
// root has no such path.
type WbGuardPath struct {
	Path    string
	Missing bool
}

// WbFreshness is how long ago a document changed: a bar that fills over
// docsFreshDays and turns from green to yellow to red.
type WbFreshness struct {
	Label, Note, Tone string
	Pct               int
}

// WbDocCiter is an issue (or a decision) tied to the document.
type WbDocCiter struct {
	Slug, Text, URL string
	Chip            *WbChip
}

// WbDocLink is a labelled link.
type WbDocLink struct {
	Text, URL string
}

// docMeta is the doc frontmatter the page reads. title and order belong to
// tracker.DocPage.
type docMeta struct {
	Kind   string   `yaml:"kind"`
	System string   `yaml:"system"`
	Guard  string   `yaml:"guard"`
	Guards []string `yaml:"guards"`
	AsOf   string   `yaml:"as_of"`
}

func (s *Server) handleDocsWb(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix, slug string) {
	data, ok := s.buildDocsWb(proj, prefix, slug, time.Now())
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "docs_wb.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// buildDocsWb builds the page with slug selected (the first document when
// empty); ok is false when slug names no document.
func (s *Server) buildDocsWb(p *tracker.Project, prefix, slug string, now time.Time) (DocsWbData, bool) {
	pages, _ := tracker.LoadDocs(p.DocsDir)
	d := DocsWbData{Title: "Docs", ClassicURL: prefix + "/docs?classic=1"}
	if slug != "" {
		d.ClassicURL = prefix + "/docs/" + slug + "?classic=1"
	}

	color := p.Color
	if color == "" {
		for i := range s.projects {
			if s.projects[i].Slug == p.Slug {
				color = mapProjectColors[i%len(mapProjectColors)]
			}
		}
	}
	lot, queue := s.buildLot(p, color, now)
	d.Header = WbHeader{WaitingURL: prefix + "/needs-you", Crumbs: wbCrumbs(2, p.Name, prefix+"/company"), Tabs: wbHallTabs(prefix, "docs"), Working: lot.Working, Idle: lot.Idle, AgentWait: lot.AgentWait, Waiting: len(queue), HideDone: true}
	d.Header.Crumbs[1].Color = lot.Color
	d.Header.YOLO = headerYOLO(lot.YOLO, prefix)
	d.Header.Crumbs[2] = WbCrumb{Label: "Product hall", URL: prefix + "/hall", Color: companyProductColor}
	d.Message = WbMessageBox{Session: lot.CEO.Session, SendURL: prefix + "/lead/send", SpawnURL: prefix + "/lead/spawn", Live: lot.CEO.Live}

	var selected *tracker.DocPage
	for _, page := range pages {
		if page.Slug == slug || slug == "" && selected == nil {
			selected = page
		}
	}
	if slug != "" && selected == nil {
		return d, false
	}

	wf := p.LoadWorkflow()
	root := projectRoot(p)
	metas := map[string]docMeta{}
	raws := map[string]string{}
	for _, page := range pages {
		raw, _ := os.ReadFile(filepath.Join(p.DocsDir, page.RelPath))
		var m docMeta
		body, err := tracker.ParseFrontmatter(string(raw), &m)
		if err != nil {
			body = string(raw)
		}
		if m.System == "" {
			m.System = designDocSystem(wf.Workbench, root, filepath.Join(p.DocsDir, page.RelPath))
			if m.System != "" && m.Kind == "" {
				m.Kind = "design"
			}
		}
		metas[page.Slug], raws[page.Slug] = m, body
	}

	d.Binder = buildBinder(pages, metas, prefix, selected)
	if selected == nil {
		docsDir := p.DocsDir
		if rel, err := filepath.Rel(root, docsDir); err == nil && !strings.HasPrefix(rel, "..") {
			docsDir = filepath.ToSlash(rel)
		}
		d.Empty = WbPlanCard{Label: "BINDER", Title: "No documents yet", Text: fmt.Sprintf("Add a Markdown file to %s and it shows up here as a document. Frontmatter is optional: kind, system, guard, guards and as_of add to its page.", docsDir), TestID: "docs_empty"}
		return d, true
	}

	meta := metas[selected.Slug]
	abs := filepath.Join(p.DocsDir, selected.RelPath)
	relPath := filepath.ToSlash(selected.RelPath)
	if rel, ok := relWithinRoot(root, abs); ok {
		relPath = rel
	}
	known := map[string]bool{}
	for _, page := range pages {
		known[page.Slug] = true
	}
	doc := buildDoc(selected, meta, raws[selected.Slug], relPath, now)
	body := rewriteRelativeImages(string(doc.Body), prefix, filepath.Dir(abs), root)
	body = rewriteDocLinks(body, prefix, path.Dir(filepath.ToSlash(selected.RelPath)), known)
	lead, rest := splitLead(body)
	doc.Lead, doc.Body = template.HTML(lead), template.HTML(rest)
	d.Doc = doc
	d.Title = doc.Title + " · Docs"

	issues, _ := tracker.LoadIssues(p.IssueDir)
	d.Side = buildDocSide(p, prefix, issues, meta, raws[selected.Slug], abs, relPath, now)
	d.Side.EditURL = prefix + "/docs/" + selected.Slug + "/edit-in-nvim"
	d.Message.Prefill = "About " + relPath + ": "
	return d, true
}

// designDocSystem is the workflow.yaml system (or "System › Family") whose
// design_doc is the file at abs.
func designDocSystem(wb tracker.WorkbenchConfig, root, abs string) string {
	same := func(doc string) bool {
		return doc != "" && filepath.Clean(filepath.Join(root, doc)) == filepath.Clean(abs)
	}
	names := make([]string, 0, len(wb.Systems))
	for name := range wb.Systems {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sys := wb.Systems[name]
		if same(sys.DesignDoc) {
			return name
		}
		for _, fam := range sys.FamilyNames() {
			if same(sys.Families[fam].DesignDoc) {
				return name
			}
		}
	}
	return ""
}

// docKindChip is a kind as a chip: rule in ink, knowledge and design in
// sand, the rest (log, review, …) on paper.
func docKindChip(kind string) WbChip {
	kind = strings.ToLower(strings.TrimSpace(kind))
	tone := "paper"
	switch kind {
	case "rule":
		tone = "ink"
	case "knowledge", "design":
		tone = "sand"
	}
	return WbChip{Tone: tone, Text: kind, TestID: "docs_kind"}
}

func docChips(m docMeta, systemPrefix string) []WbChip {
	var out []WbChip
	if strings.TrimSpace(m.Kind) != "" {
		out = append(out, docKindChip(m.Kind))
	}
	if m.System != "" {
		out = append(out, WbChip{Tone: "outline", Text: systemPrefix + m.System, TestID: "docs_system"})
	}
	return out
}

func buildBinder(pages []*tracker.DocPage, metas map[string]docMeta, prefix string, selected *tracker.DocPage) WbBinder {
	b := WbBinder{
		Count: plural(len(pages), "document", "documents"),
		New:   WbNewTile{Title: "New document", Note: "Add a Markdown file to the docs folder", TestID: "new_document"},
	}
	for _, page := range pages {
		if len(b.Groups) == 0 || b.Groups[len(b.Groups)-1].Name != page.Section {
			b.Groups = append(b.Groups, WbBinderGroup{Name: page.Section})
		}
		g := &b.Groups[len(b.Groups)-1]
		g.Items = append(g.Items, WbBinderItem{
			Slug:    page.Slug,
			Title:   page.Title,
			URL:     prefix + "/docs/" + page.Slug,
			Chips:   docChips(metas[page.Slug], ""),
			Current: page == selected,
		})
	}
	return b
}

// buildDoc renders the document. Its leading "# " heading, when it has one,
// is the page's title and leaves the body.
func buildDoc(page *tracker.DocPage, m docMeta, body, relPath string, now time.Time) *WbDoc {
	doc := &WbDoc{Slug: page.Slug, Title: page.Title, Path: relPath, Guard: strings.TrimSpace(m.Guard), Chips: docChips(m, "system: ")}
	if t, rest, ok := leadingH1(body); ok {
		doc.Title, body = t, rest
	}
	if at, ok := parseDocDate(m.AsOf); ok {
		doc.AsOf = "as of " + docDateLabel(at, now)
	}
	sections := tracker.SplitSections(body)
	anchors := tracker.SectionAnchors(body)
	for i, sec := range sections {
		v := WbDocSection{Title: sec.Title}
		if len(anchors) == len(sections) && anchors[i] != "" {
			v.Href = "#" + anchors[i]
		}
		doc.Sections = append(doc.Sections, v)
	}
	rendered, err := tracker.RenderDocMarkdown(body)
	if err != nil {
		rendered = "<pre>" + html.EscapeString(body) + "</pre>"
	}
	doc.Body = template.HTML(rendered)
	return doc
}

// leadingH1 splits a body that opens with a "# " heading into its text and
// the rest.
func leadingH1(body string) (title, rest string, ok bool) {
	trimmed := strings.TrimLeft(body, " \t\r\n")
	line, after, _ := strings.Cut(trimmed, "\n")
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "# ") {
		return "", body, false
	}
	title = strings.TrimSpace(strings.TrimRight(strings.TrimPrefix(line, "# "), "# "))
	if title == "" {
		return "", body, false
	}
	return title, after, true
}

// splitLead cuts rendered HTML at its first <h2>: the lead before it, the
// sections after. A doc without sections is all body.
func splitLead(body string) (lead, rest string) {
	i := strings.Index(body, "<h2")
	if i <= 0 {
		return "", body
	}
	lead = strings.TrimSpace(body[:i])
	return lead, body[i:]
}

var docLinkRe = regexp.MustCompile(`(<a\s+href=")([^"]+)(")`)

// rewriteDocLinks points relative links to another doc (`other.md`,
// `../ui/x.md#part`) at that doc's page.
func rewriteDocLinks(body, prefix, dir string, known map[string]bool) string {
	return docLinkRe.ReplaceAllStringFunc(body, func(match string) string {
		m := docLinkRe.FindStringSubmatch(match)
		href := html.UnescapeString(m[2])
		if strings.HasPrefix(href, "/") || strings.HasPrefix(href, "#") || strings.Contains(href, ":") {
			return match
		}
		target, frag, _ := strings.Cut(href, "#")
		if !strings.HasSuffix(target, ".md") {
			return match
		}
		decoded, err := url.PathUnescape(target)
		if err != nil {
			return match
		}
		slug := strings.TrimSuffix(path.Clean(path.Join(dir, decoded)), ".md")
		if !known[slug] {
			return match
		}
		out := prefix + "/docs/" + slug
		if frag != "" {
			out += "#" + frag
		}
		return m[1] + html.EscapeString(out) + m[3]
	})
}

func parseDocDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02", "2006-01-02T15:04:05Z07:00", "02.01.2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func docDateLabel(t, now time.Time) string {
	if t.Year() == now.Year() {
		return t.Format("2 Jan")
	}
	return t.Format("2 Jan 2006")
}

// docChangedAt is when the doc last changed: its last commit, or the file's
// modification time outside git. source says which.
var docChangedAt = func(abs string) (at time.Time, source string) {
	if out, ok := gitOutput(filepath.Dir(abs), "log", "-1", "--format=%cI", "--", filepath.Base(abs)); ok {
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(out)); err == nil {
			return t, "commit"
		}
	}
	if info, err := os.Stat(abs); err == nil {
		return info.ModTime(), "file"
	}
	return time.Time{}, ""
}

// docFreshness is the freshness block: from as_of when the doc declares it,
// else from its last change.
func docFreshness(m docMeta, abs string, now time.Time) WbFreshness {
	at, ok := parseDocDate(m.AsOf)
	what := "Research as of"
	if !ok {
		var source string
		at, source = docChangedAt(abs)
		switch source {
		case "commit":
			what = "Last commit"
		case "file":
			what = "File saved"
		default:
			return WbFreshness{Label: "No date", Note: "Neither git nor the file says when it changed.", Tone: "idle"}
		}
	}
	days := int(now.Sub(at).Hours() / 24)
	if days < 0 {
		days = 0
	}
	f := WbFreshness{Label: fmt.Sprintf("%s %s · %s", what, docDateLabel(at, now), docAgo(days))}
	f.Pct = days * 100 / docsFreshDays
	if f.Pct > 100 {
		f.Pct = 100
	}
	if f.Pct < 3 {
		f.Pct = 3
	}
	switch {
	case days <= 30:
		f.Tone, f.Note = "green", "Changed within the last month."
	case days <= 90:
		f.Tone, f.Note = "yellow", "Unchanged for over a month: check it still matches the code."
	default:
		f.Tone, f.Note = "red", "Unchanged for over three months: check it still matches the code."
	}
	return f
}

func docAgo(days int) string {
	switch days {
	case 0:
		return "today"
	case 1:
		return "yesterday"
	}
	return fmt.Sprintf("%d days ago", days)
}

// buildDocSide is the right panel: guarded paths, freshness, the open issues
// that cite the doc (and whether an agent works them), the decisions it
// cites and the system's decisions.
func buildDocSide(p *tracker.Project, prefix string, issues []*tracker.Issue, m docMeta, body, abs, relPath string, now time.Time) *WbDocSide {
	root := projectRoot(p)
	side := &WbDocSide{System: m.System, Path: relPath, Fresh: docFreshness(m, abs, now)}
	for _, g := range m.Guards {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(g)))
		side.Guards = append(side.Guards, WbGuardPath{Path: g, Missing: err != nil})
	}

	open := openOnly(issues)
	sort.Slice(open, func(i, j int) bool { return open[i].Slug < open[j].Slug })
	sessions, _ := sessionsByIssueSlug(p, open)
	base := filepath.Base(abs)
	for _, issue := range open {
		if !strings.Contains(issue.BodyRaw, base) {
			continue
		}
		if len(side.Citers) == docsCiters {
			side.More++
			continue
		}
		c := WbDocCiter{Slug: issue.Slug, Text: issue.Title, URL: prefix + "/issue/" + issue.Slug}
		if len(sessions[issue.Slug]) > 0 {
			c.Chip = &WbChip{Tone: "green", Text: "agent on it", Dot: true}
		} else if issue.Status != "" {
			c.Chip = &WbChip{Tone: "idle", Text: issue.Status}
		}
		side.Citers = append(side.Citers, c)
	}

	index := tracker.BuildDecisionIndex(issues, tracker.ClosedIssueFunc(p))
	for _, e := range index.CitedIn(body) {
		c := WbDocCiter{Slug: e.Issue.Slug + "#" + e.ID, Text: shortText(e.Text, 120), URL: prefix + "/issue/" + e.Issue.Slug}
		if e.ReplacedBy != nil {
			c.Chip = &WbChip{Tone: "red", Text: "replaced by " + e.ReplacedBy.ID}
		}
		side.Decisions = append(side.Decisions, c)
	}
	if m.System != "" {
		n := 0
		for _, e := range index.Entries {
			if strings.EqualFold(e.Issue.System, m.System) {
				n++
			}
		}
		side.SystemDecisions = &WbDocLink{Text: fmt.Sprintf("Decisions of system %s · %d", m.System, n), URL: prefix + "/decisions?system=" + url.QueryEscape(m.System)}
	}
	return side
}

// --- Edit in nvim ---

// launchDocEditor opens a doc in nvim; tests stub it.
var launchDocEditor = startDocEditor

// startDocEditor opens the doc file itself in nvim in a tmux session of its
// own: the file is plain Markdown in the repo, so there is nothing to sync
// back. A session still open from an earlier edit is reattached.
func startDocEditor(proj *tracker.Project, slug, abs string) (BodyEditResponse, error) {
	session := "doc-edit-" + tracker.Slugify(strings.ReplaceAll(slug, "/", "-"))
	target := agentTmuxTarget(proj, session)
	reach := func() string {
		hint, err := openEditorTerminal(proj, session)
		if err != nil {
			return "Reach it " + editorReachHint(proj, session)
		}
		if hint != "" {
			return "Reach it " + hint
		}
		return "A terminal window is open on it."
	}
	if agentrun.HasSession(target) {
		return BodyEditResponse{Status: "reattached", Session: agentDisplayName(proj, session), Reattached: true,
			Message: "This document is already open in nvim. " + reach()}, nil
	}
	_, createCmd := createAgentTmux(proj, session, filepath.Dir(abs))
	if err := createCmd.Run(); err != nil {
		return BodyEditResponse{}, fmt.Errorf("create tmux session: %w", err)
	}
	editCmd := fmt.Sprintf("nvim %q; exit", abs)
	if err := tmuxSendKeys(target, []string{editCmd}); err != nil {
		killAgentTmux(proj, session)
		return BodyEditResponse{}, fmt.Errorf("start nvim: %w", err)
	}
	return BodyEditResponse{Status: "launched", Session: agentDisplayName(proj, session),
		Message: "Opened in nvim; saving writes the file. " + reach()}, nil
}

func (s *Server) handleDocEditInNvim(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	slug := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix+"/docs/"), "/edit-in-nvim")
	pages, _ := tracker.LoadDocs(proj.DocsDir)
	var found *tracker.DocPage
	for _, page := range pages {
		if page.Slug == slug {
			found = page
		}
	}
	if found == nil {
		http.NotFound(w, r)
		return
	}
	resp, err := launchDocEditor(proj, slug, filepath.Join(proj.DocsDir, found.RelPath))
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"status": "error", "error": err.Error()})
		return
	}
	writeJSONStatus(w, http.StatusOK, resp)
}
