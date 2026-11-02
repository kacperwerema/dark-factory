package viewer

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// TestUpdatesOverviewWorkshopBoard: /updates is the Workshop Board page, one
// wb/upd-project card per project with its template, state, to-decide count,
// record and a link to its Updates, a built-in project with the note and no
// link, the tally, the Map crumb and the link to the classic table.
func TestUpdatesOverviewWorkshopBoard(t *testing.T) {
	t.Chdir(t.TempDir())
	a := updatesProject(t)
	b, _ := setupTestProject(t)
	b.Name, b.Slug = "Plain", "plain"
	ts := newTestServer(t, []tracker.Project{a, b})
	defer ts.Close()

	_, body := getBody(t, ts.URL+"/updates")
	for _, want := range []string{
		`data-testid="updates_overview.page"`,
		`<a class="wb-crumb wb-crumb--link" href="/?map=1">Map</a>`,
		`>Workflow updates</`,
		`href="/updates?classic=1" data-testid="updates_overview.classic">Classic view</a>`,
		`data-testid="updates_overview.tally.projects"><b>2</b><span>projects</span>`,
		`data-testid="updates_overview.card.test-project.template">development@v0.36.0<`,
		`class="wb-chip wb-chip--you" data-testid="updates_overview.card.test-project.state">behind<`,
		`1 installed · 1 declined`,
		`href="/p/test-project/updates" data-testid="updates_overview.card.test-project.open">Open its updates</a>`,
		`data-testid="updates_overview.card.plain.state">no template<`,
		`Built-in or custom workflow: no catalog to compare.`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/updates missing %q", want)
		}
	}
	if strings.Contains(body, `data-testid="updates_overview.card.plain.open"`) {
		t.Error("a built-in project has no Updates to open")
	}
	if strings.Index(body, "updates_overview.card.test-project") > strings.Index(body, "updates_overview.card.plain") {
		t.Error("a behind project should sort before a built-in one")
	}
	if strings.Contains(body, `data-testid="updates.overview"`) {
		t.Error("the classic table belongs to ?classic=1")
	}
}

// TestUpdatesOverviewNeverScanned: a stamped project with no features:
// record says never scanned, and the note explains it.
func TestUpdatesOverviewNeverScanned(t *testing.T) {
	t.Chdir(t.TempDir())
	proj := updatesProject(t)
	wf := regexp.MustCompile(`\nfeatures:\n(  .*\n)*`).ReplaceAllString(updatesWorkflow, "\n")
	if err := os.WriteFile(proj.WorkflowFile, []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, body := getBody(t, ts.URL+"/updates")
	if !strings.Contains(body, `data-testid="updates_overview.tally.never_scanned"><b>1</b>`) {
		t.Error("the tally should count the never-scanned project")
	}
	if !strings.Contains(body, "none yet") || !strings.Contains(body, "Never scanned: the project has no") {
		t.Error("a never-scanned project should say it has no record yet")
	}
}

// TestUpdatesOverviewNoProjects: with no project the page says so.
func TestUpdatesOverviewNoProjects(t *testing.T) {
	ts := newTestServer(t, nil)
	defer ts.Close()
	_, body := getBody(t, ts.URL+"/updates")
	if !strings.Contains(body, `data-testid="updates_overview.empty">No project is configured in projects.yaml.<`) {
		t.Error("the empty state is missing")
	}
	if strings.Contains(body, "updates_overview.tally") {
		t.Error("no tally without projects")
	}
}

// TestGlobalPagesWorkshopBoardOnly: the pages outside a project carry only
// Workshop Board classes (from the wb/* components) and English text.
func TestGlobalPagesWorkshopBoardOnly(t *testing.T) {
	t.Chdir(t.TempDir())
	a := updatesProject(t)
	b, _ := setupTestProject(t)
	b.Name, b.Slug = "Plain", "plain"
	ts := newTestServer(t, []tracker.Project{a, b})
	defer ts.Close()

	for _, path := range []string{"/updates", "/leads", "/needs-you"} {
		_, html := getBody(t, ts.URL+path)
		for _, attr := range regexp.MustCompile(`class="([^"]*)"`).FindAllStringSubmatch(html, -1) {
			for _, c := range strings.Fields(attr[1]) {
				if c != "wb" && !strings.HasPrefix(c, "wb-") {
					t.Errorf("%s: class %q is not a Workshop Board class", path, c)
				}
			}
		}
		if strings.ContainsAny(html, "ąćęłńóśźżĄĆĘŁŃÓŚŹŻ") {
			t.Errorf("%s carries Polish text", path)
		}
	}
}
