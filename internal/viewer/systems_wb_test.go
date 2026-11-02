package viewer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/health"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestSystemsWbPage(t *testing.T) {
	proj, root := setupTestProject(t)
	withMockTmuxSessions(t, nil)
	// An issue naming a file that exists, and one naming a file that does not.
	os.MkdirAll(filepath.Join(root, "web", "src", "screens"), 0755)
	os.WriteFile(filepath.Join(root, "web", "src", "screens", "Login.tsx"), []byte("x\n"), 0644)
	os.WriteFile(filepath.Join(proj.IssueDir, "login-copy.md"), []byte("---\ntitle: \"Login copy\"\nstatus: \"backlog\"\nsystem: \"Auth\"\n---\n\nChange `web/src/screens/Login.tsx` and web/src/Missing.tsx.\n"), 0644)
	// A workflow without workbench.systems and without gates.
	proj.WorkflowFile = filepath.Join(root, "workflow.yaml")
	os.WriteFile(proj.WorkflowFile, []byte("statuses:\n  - name: \"backlog\"\n  - name: \"in progress\"\n  - name: \"done\"\n"), 0644)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	code, html := getBody(t, ts.URL+"/p/test-project/systems")
	if code != 200 {
		t.Fatalf("status %d: %s", code, html)
	}
	assertContains(t, html,
		`data-testid="systems.wb.page"`, `>Systems</h1>`,
		// 3 systems, 3 open (Auth 2, UI 1), nothing waits on the owner.
		`<div class="wb-figure-num">3</div><div class="wb-figure-label">systems</div>`,
		`<div class="wb-figure-num">3</div><div class="wb-figure-label">open</div>`,
		`<div class="wb-figure-num">0</div><div class="wb-figure-label">gates for you</div>`,
		`data-testid="systems.wb.legend"`, `at your gate`, `agent working`, `waiting in the queue`,
		`data-testid="systems.wb.card.auth"`, `data-testid="systems.wb.card.ui"`, `data-testid="systems.wb.card.docs"`,
		`2 open · 0 done`, `0 open · 1 done`,
		`href="/p/test-project/decisions?system=Auth"`, `Decisions · 0`, `href="/p/test-project/hall"`, `On the line`,
		`href="/p/test-project/systems/Auth"`, `href="/p/test-project/systems?classic=1"`,
		// The file the issue names is on Auth's CODE row and the code map; the
		// missing one is not.
		`title="web/src/screens/Login.tsx">screens/Login.tsx</span>`, `<div class="wb-code-pkg-name">web/src</div>`,
		`no file named in its issues`, `>Draft</b>`, `No health snapshot yet`,
		// No workbench.systems and no CEO: the unlock card says how.
		`data-testid="systems.wb.unlock"`, `Unlock: workbench.systems`, `Auth, Docs and UI are not there yet`, `Give the draft to an agent`, `disabled`, `Start the CEO on the Company page first`,
	)
	if strings.Contains(html, "Missing.tsx") {
		t.Error("a file that is not in the repo is not on the map")
	}
	if strings.Index(html, "systems.wb.card.auth") > strings.Index(html, "systems.wb.card.docs") {
		t.Error("most open first: Auth (2) before Docs (0)")
	}
	if n := strings.Count(html, `class="wb-cdot wb-cdot--queue" href=`); n != 3 {
		t.Errorf("queue crates = %d, want 3", n)
	}
	if n := strings.Count(html, `class="wb-cdot wb-cdot--done" href=`); n != 1 {
		t.Errorf("done crates = %d, want 1", n)
	}
	if strings.Contains(html, `data-testid="systems.wb.shared_hot"`) {
		t.Error("no hot files without a snapshot")
	}
}

func TestSystemsWbCrateStates(t *testing.T) {
	a := setupAgentsProject(t)
	// A worker on an issue with no gate ahead, and a done issue.
	os.WriteFile(filepath.Join(a.IssueDir, "UI", "runner.md"), []byte("---\ntitle: \"Runner\"\nstatus: \"backlog\"\n---\n\nBody.\n"), 0644)
	os.WriteFile(filepath.Join(a.IssueDir, "UI", "shipped.md"), []byte("---\ntitle: \"Shipped\"\nstatus: \"done\"\n---\n\nBody.\n"), 0644)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, []AgentSession{{Name: "lead-agents"}, {Name: "agent-ui-child"}, {Name: "agent-ui-asker"}, {Name: "agent-ui-runner"}})
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()

	_, html := getBody(t, ts.URL+"/p/agents/systems")
	// The four issues in progress wait at the testing gate (ui/asker also
	// asked a question); ui/runner's worker has no gate ahead; ui/shipped is
	// done. The owner's crates come first.
	counts := map[string]int{}
	for _, st := range []string{"you", "working", "queue", "done"} {
		counts[st] = strings.Count(html, `class="wb-cdot wb-cdot--`+st+`" href=`)
	}
	if counts["you"] != 4 || counts["working"] != 1 || counts["queue"] != 0 || counts["done"] != 1 {
		t.Errorf("crates = %v, want you 4, working 1, done 1", counts)
	}
	if strings.Index(html, "wb-cdot--you\" href") > strings.Index(html, "wb-cdot--working\" href") {
		t.Error("crates at the owner's gate come first")
	}
	assertContains(t, html,
		`data-testid="systems.wb.card.ui"`, `5 open · 1 done`,
		`title="Waiting on you: 4 gates, 1 question" data-testid="systems.wb.gates">5 gates</span>`,
		`<div class="wb-figure-num wb-figure-num--you">5</div><div class="wb-figure-label">gates for you</div>`,
		`5 gates on 4 issues`,
		`title="ui/runner · backlog · working"`,
		`No system: 1 open, 0 done`,
		`href="/p/agents/issue/ui/child"`,
		// The CEO runs: the draft can go to it.
		`data-send="/p/agents/lead/send"`, `value="Draft workbench.systems in workflow.yaml for UI:`,
	)
	if strings.Contains(html, `Start the CEO on the Company page first`) {
		t.Error("the CEO is live")
	}
}

func TestSystemsWbClassicAndSystemPage(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, classic := getBody(t, ts.URL+"/p/test-project/systems?classic=1")
	assertContains(t, classic, `data-testid="systems.index"`, `data-testid="systems.card.auth"`)
	if strings.Contains(classic, "systems.wb.page") {
		t.Error("?classic=1 is the old index")
	}
	code, sys := getBody(t, ts.URL+"/p/test-project/systems/Auth")
	if code != 200 || !strings.Contains(sys, `data-testid="system.wb.page"`) {
		t.Errorf("/systems/<name> is the Workshop page: %d", code)
	}
	code, sys = getBody(t, ts.URL+"/p/test-project/systems/Auth?classic=1")
	if code != 200 || !strings.Contains(sys, `data-testid="system.header"`) {
		t.Errorf("/systems/<name>?classic=1 stays the classic system page: %d", code)
	}
}

func TestCodeIndex(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"packages/web/src/lib/store.ts", "packages/web/src/screens/Plan.tsx", "packages/web/src/styles.css", "internal/health/shape.go", "main.go"} {
		os.MkdirAll(filepath.Join(root, filepath.Dir(p)), 0755)
		os.WriteFile(filepath.Join(root, p), []byte("x\n"), 0644)
	}
	snap := &health.Snapshot{Metrics: []health.Row{
		{Metric: "shape.hotspot", Labels: map[string]string{"path": "packages/web/src/styles.css"}, Value: 900},
		{Metric: "shape.hotspot", Labels: map[string]string{"path": "packages/web/src/lib/store.ts"}, Value: 500},
		{Metric: "shape.long_func.lines", Labels: map[string]string{"path": "packages/web/src/screens/Plan.tsx", "func": "Plan"}, Value: 982},
	}}
	c := newCodeIndex(root, snap)
	issue := func(body string) *tracker.Issue { return &tracker.Issue{BodyRaw: body} }
	plan := c.chipsFor("Plan", nil, []*tracker.Issue{issue("see packages/web/src/screens/Plan.tsx and packages/web/src/lib/store.ts:42"), issue("packages/web/src/screens/Plan.tsx again")})
	shop := c.chipsFor("Shopping", nil, []*tracker.Issue{issue("`packages/web/src/lib/store.ts` and https://x.io/a.ts and ../main.go")})
	c.chipsFor("Engine", nil, []*tracker.Issue{issue("main.go and internal/health/shape.go")})

	if len(plan) != 2 || plan[0].Name != "screens/Plan.tsx" || plan[0].Hot || plan[1].Name != "lib/store.ts" || !plan[1].Hot {
		t.Errorf("Plan chips = %+v", plan)
	}
	if len(shop) != 1 || shop[0].Path != "packages/web/src/lib/store.ts" {
		t.Errorf("Shopping chips = %+v", shop)
	}
	if got := c.longFuncNote("Plan"); got != "Plan() has 982 lines" {
		t.Errorf("note = %q", got)
	}
	if got := c.longFuncNote("Shopping"); got != "" {
		t.Errorf("Shopping names no long function: %q", got)
	}

	m := c.codeMap(true)
	if m.Empty != "" || len(m.Packages) != 3 || m.Packages[0].Name != "packages/web/src" {
		t.Fatalf("packages = %+v", m.Packages)
	}
	files := map[string]WbCodeFile{}
	for _, f := range m.Packages[0].Files {
		files[f.Name] = f
	}
	if f := files["lib/store.ts"]; !f.Hot || !f.Shared || f.Systems != "Plan, Shopping" {
		t.Errorf("store.ts = %+v", f)
	}
	if f := files["styles.css"]; !f.Hot || f.Systems != "?" {
		t.Errorf("a hotspot nobody names = %+v", f)
	}
	if m.Packages[0].Files[0].Name != "lib/store.ts" && m.Packages[0].Files[0].Name != "styles.css" {
		t.Errorf("hot files first: %+v", m.Packages[0].Files)
	}
	w := c.sharedHot()
	if w == nil || len(w.Files) != 1 || w.Files[0] != "store.ts" || !strings.HasPrefix(w.Text, "belongs") {
		t.Errorf("shared hot = %+v", w)
	}

	if m := newCodeIndex(root, nil).codeMap(false); m.Empty == "" || !m.Draft {
		t.Errorf("nothing named, no snapshot: %+v", m)
	}
}

func TestCodePackage(t *testing.T) {
	for p, want := range map[string][2]string{
		"packages/web/src/screens/Plan.tsx": {"packages/web/src", "screens/Plan.tsx"},
		"internal/health/shape.go":          {"internal/health", "shape.go"},
		"main.go":                           {"(repo root)", "main.go"},
		"a/b/c/d/e.go":                      {"a/b/c", "d/e.go"},
	} {
		pkg, name := codePackage(p)
		if pkg != want[0] || name != want[1] {
			t.Errorf("codePackage(%q) = %q, %q; want %q", p, pkg, name, want)
		}
	}
}

// The components render on their own, outside the page.
func TestSystemsWbComponents(t *testing.T) {
	srv, err := NewServer(nil)
	if err != nil {
		t.Fatal(err)
	}
	render := func(name string, data any) string {
		t.Helper()
		var b strings.Builder
		if err := srv.tmpl.ExecuteTemplate(&b, name, data); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return b.String()
	}
	ws := WbWorkshop{Name: "Plan", URL: "/p/x/systems/Plan", Open: 2, Done: 1, Gates: 3, GatesTitle: "Waiting on you: 3 gate",
		Crates: []WbCrateDot{{State: "you", URL: "/p/x/issue/a", Title: "a"}, {State: "done"}}, More: 4,
		Code: []WbCodeChip{{Name: "week.rs", Path: "solver/src/week.rs", Hot: true}}, Decisions: 11, DecisionsURL: "/d", LineURL: "/h", Note: "Plan() has 982 lines"}
	assertContains(t, render("wb/workshop", ws),
		`data-testid="systems.wb.card.plan"`, `3 gates</span>`, `2 open · 1 done`, `aria-label="Plan: 2 open, 1 done"`,
		`<a class="wb-cdot wb-cdot--you" href="/p/x/issue/a"`, `<span class="wb-cdot wb-cdot--done"></span>`, `+4`,
		`week.rs<i class="wb-hot-dot"`, `Decisions · 11`, `Plan() has 982 lines`)
	assertContains(t, render("wb/workshop", WbWorkshop{Name: "Docs", CodeNone: "no file matches its paths"}), `wb-code-chip--none">no file matches its paths`)
	if strings.Contains(render("wb/workshop", WbWorkshop{Name: "Docs"}), "systems.wb.gates") {
		t.Error("no gates pill without gates")
	}
	assertContains(t, render("wb/crate-legend", wbCrateKeys()), `wb-cdot--you`, `wb-cdot--working`, `wb-cdot--queue`, `wb-cdot--done`)
	assertContains(t, render("wb/figures", []WbFigure{{Value: 7, Label: "systems"}, {Value: 54, Label: "gates for you", Tone: "you"}}), `wb-figure-num--you">54`)
	assertContains(t, render("wb/code-map", WbCodeMap{Draft: true, Empty: "No code map yet"}), `data-testid="systems.wb.codemap.empty"`, `No code map yet`)
	assertContains(t, render("wb/warning", &WbWarning{Title: "Shared hot files.", Files: []string{"store.ts", "styles.css"}, Text: "belong to several systems at once."}),
		`<span class="wb-mono-inline">store.ts</span> and <span class="wb-mono-inline">styles.css</span>`)
	if got := render("wb/warning", (*WbWarning)(nil)); strings.TrimSpace(got) != "" {
		t.Errorf("no warning renders nothing: %q", got)
	}
	if got := render("wb/unlock-card", (*WbUnlockCard)(nil)); strings.TrimSpace(got) != "" {
		t.Errorf("no unlock card renders nothing: %q", got)
	}
	assertContains(t, render("wb/unlock-card", &WbUnlockCard{Title: "Unlock: workbench.systems", Button: "Give the draft to an agent", SendURL: "/p/x/lead/send", Message: "Draft it", Live: true}),
		`data-send="/p/x/lead/send"`, `name="text" value="Draft it"`, `Give the draft to an agent`)
}

func TestCodeIndexPaths(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"web/src/screens/Plan.tsx", "web/src/screens/Week.tsx", "web/src/lib/store.ts", "web/src/styles.css", "main.go"} {
		os.MkdirAll(filepath.Join(root, filepath.Dir(p)), 0755)
		os.WriteFile(filepath.Join(root, p), []byte("x\n"), 0644)
	}
	snap := &health.Snapshot{Metrics: []health.Row{
		{Metric: "shape.hotspot", Labels: map[string]string{"path": "web/src/styles.css"}, Value: 900},
		{Metric: "shape.hotspot", Labels: map[string]string{"path": "web/src/screens/Week.tsx"}, Value: 500},
	}}
	issue := func(body string) *tracker.Issue { return &tracker.Issue{BodyRaw: body} }

	// Plan and Shopping have paths, Engine doesn't: a draft naming Engine.
	c := newCodeIndex(root, snap)
	plan := c.chipsFor("Plan", []string{"web/src/screens/", "**/store.ts"}, []*tracker.Issue{issue("web/src/lib/store.ts")})
	c.chipsFor("Shopping", []string{"web/src/lib/*.ts"}, nil)
	c.chipsFor("Engine", nil, []*tracker.Issue{issue("main.go")})
	if len(plan) != 3 || plan[0].Path != "web/src/lib/store.ts" || plan[1].Path != "web/src/screens/Week.tsx" || plan[2].Path != "web/src/screens/Plan.tsx" {
		t.Errorf("Plan chips: its globs' files, the ones its issues name first, then hot: %+v", plan)
	}
	m := c.codeMap(true)
	if !m.Draft || !strings.HasPrefix(m.Note, "Engine: read from the files their issues name") {
		t.Errorf("mixed: a draft naming the system without paths: %+v", m)
	}
	files := map[string]WbCodeFile{}
	for _, p := range m.Packages {
		for _, f := range p.Files {
			files[f.Path] = f
		}
	}
	if f := files["web/src/lib/store.ts"]; !f.Shared || f.Systems != "Plan, Shopping" {
		t.Errorf("store.ts is owned by two systems' globs: %+v", f)
	}
	if f := files["main.go"]; f.Systems != "Engine" {
		t.Errorf("Engine keeps its issue-named file: %+v", f)
	}
	if f := files["web/src/styles.css"]; f.Systems != "?" || f.SystemsTitle != "no system's paths: or issues claim it" {
		t.Errorf("a hotspot nobody owns: %+v", f)
	}

	// Every system has paths: no draft.
	c = newCodeIndex(root, snap)
	c.chipsFor("Plan", []string{"web/src/screens/"}, nil)
	c.chipsFor("Engine", []string{"*.go"}, nil)
	m = c.codeMap(true)
	if m.Draft || !strings.Contains(m.Note, "from paths: in workbench.systems") {
		t.Errorf("all paths: confirmed: %+v", m)
	}
	if got := c.chipsFor("Empty", []string{"nothing/**"}, nil); len(got) != 0 {
		t.Errorf("a glob matching nothing gives no chips: %+v", got)
	}
}

func TestSystemsWbPagePaths(t *testing.T) {
	proj, root := setupTestProject(t)
	withMockTmuxSessions(t, nil)
	os.MkdirAll(filepath.Join(root, "web", "src", "screens"), 0755)
	os.WriteFile(filepath.Join(root, "web", "src", "screens", "Login.tsx"), []byte("x\n"), 0644)
	os.WriteFile(filepath.Join(proj.IssueDir, "login-copy.md"), []byte("---\ntitle: \"Login copy\"\nstatus: \"backlog\"\nsystem: \"Auth\"\n---\n\nNo file named.\n"), 0644)
	proj.WorkflowFile = filepath.Join(root, "workflow.yaml")
	base := "statuses:\n  - name: \"backlog\"\n  - name: \"in progress\"\n  - name: \"done\"\nworkbench:\n  systems:\n    Auth:\n      paths: [\"web/src/screens/\"]\n    UI:\n      summary: x\n"
	os.WriteFile(proj.WorkflowFile, []byte(base+"    Docs:\n      summary: x\n"), 0644)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, html := getBody(t, ts.URL+"/p/test-project/systems")
	assertContains(t, html, `title="web/src/screens/Login.tsx">screens/Login.tsx</span>`, `data-testid="systems.wb.codemap.draft"`,
		`Docs and UI: read from the files their issues name`, `Unlock: a confirmed code map`, `Give Docs and UI paths: in workbench.systems`)

	os.WriteFile(proj.WorkflowFile, []byte(base+"      paths: [\"web/\"]\n    Docs:\n      paths: [\"*.md\"]\n"), 0644)
	_, html = getBody(t, ts.URL+"/p/test-project/systems")
	assertContains(t, html, `Which packages belong to which systems, from paths: in workbench.systems.`)
	for _, s := range []string{`systems.wb.codemap.draft`, `Unlock: a confirmed code map`, `systems.wb.unlock`} {
		if strings.Contains(html, s) {
			t.Errorf("every system has paths: no %s", s)
		}
	}
}

// Proves: AC3 — a workshop card carries what the classic index card had:
// the summary, the design doc and the families from workbench.systems
// (classic-page-gaps G55).
func TestSystemsWbCardSummaryDocFamilies(t *testing.T) {
	proj, root := setupTestProject(t)
	withMockTmuxSessions(t, nil)
	os.WriteFile(filepath.Join(proj.IssueDir, "login-copy.md"), []byte("---\ntitle: \"Login copy\"\nstatus: \"backlog\"\nsystem: \"Auth\"\n---\n\nx\n"), 0644)
	proj.WorkflowFile = filepath.Join(root, "workflow.yaml")
	os.WriteFile(proj.WorkflowFile, []byte("statuses:\n  - name: \"backlog\"\n  - name: \"done\"\nworkbench:\n  systems:\n    Auth:\n      summary: Who may sign in.\n      design_doc: docs/auth.md\n      families:\n        Sessions:\n          umbrella: nope/missing\n"), 0644)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, html := getBody(t, ts.URL+"/p/test-project/systems")
	assertContains(t, html, `data-testid="systems.wb.summary">Who may sign in.</p>`, `data-testid="systems.wb.doc">docs/auth.md</span>`,
		`data-testid="systems.wb.families"><a class="wb-tag-pill" href="/p/test-project/systems/Auth/Sessions">Sessions</a>`)
}
