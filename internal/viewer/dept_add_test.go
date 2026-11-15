package viewer

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/depttemplate"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// deptSandbox is a project "Acme" in its own projects.yaml under a temp
// dir: never the owner's config.
func deptSandbox(t *testing.T) (config string, proj tracker.Project) {
	t.Helper()
	withMockTmuxSessions(t, nil)
	dir := t.TempDir()
	root := filepath.Join(dir, "acme")
	if err := os.MkdirAll(filepath.Join(root, "issues"), 0o755); err != nil {
		t.Fatal(err)
	}
	config = filepath.Join(dir, "projects.yaml")
	body := "projects:\n  # the sandbox\n  - name: Acme\n    issues: " + filepath.Join(root, "issues") + "\n    workdir: " + root + "\n"
	if err := os.WriteFile(config, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	projects, err := tracker.LoadProjects(config)
	if err != nil {
		t.Fatal(err)
	}
	proj = projects[0]
	proj.WorkflowFile = repoWorkflow(t)
	return config, proj
}

// plannedFiles counts the files adding template key under parent would
// write and keep, from the plan Add makes, so a files: entry added to a
// bundled template changes no literal here. Call it before the Add.
func plannedFiles(t *testing.T, proj *tracker.Project, key, parent string) (wrote, kept int) {
	t.Helper()
	_, plan, err := planDepartment(proj, key, parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, pl := range append([]*depttemplate.Plan{plan}, plan.Children...) {
		for _, f := range pl.Files {
			switch f.State {
			case depttemplate.FileWrite:
				wrote++
			case depttemplate.FileKeep:
				kept++
			}
		}
	}
	return wrote, kept
}

// postAdd posts the Add form as the owner's browser does, with headers.
func postAdd(t *testing.T, base string, form url.Values, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/p/acme/departments/add", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

var sameOrigin = map[string]string{"Sec-Fetch-Site": "same-origin"}

func TestCompanyNewDeptTileIsAFormOverTheTemplates(t *testing.T) {
	_, proj := deptSandbox(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	code, body := getBody(t, ts.URL+"/p/acme/company?classic=1")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	for _, want := range []string{
		`data-testid="company.new_dept"`,
		`data-testid="tile.new_department"`,
		`From a template: Legal · Marketing · Sales · Video · GTM`,
		`<form class="wb-newlot-how wb-newdept-form" method="get" action="/p/acme/departments/new" data-testid="company.new_dept.form">`,
		`data-testid="company.new_dept.template.marketing"`,
		`<input type="radio" name="template" value="legal" checked>`,
		`<input type="radio" name="template" value="marketing">`,
		`workflow: marketing · 3 goals · 1 job`,
		`data-testid="company.new_dept.preview"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("company page missing %q", want)
		}
	}
	for _, gone := range []string{"Add one under departments: in projects.yaml", "Custom", "A department of your own", `name="name"`} {
		if strings.Contains(body, gone) {
			t.Errorf("the tile offers more than the templates: %q", gone)
		}
	}
}

func TestDeptPreviewShowsThePlanAndWritesNothing(t *testing.T) {
	config, proj := deptSandbox(t)
	before, _ := os.ReadFile(config)
	// An edited role prompt is there already: the plan keeps it.
	role := filepath.Join(proj.Root(), "departments", "marketing", "role.md")
	if err := os.MkdirAll(filepath.Dir(role), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(role, []byte("my own words\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	code, body := getBody(t, ts.URL+"/p/acme/departments/new?template=marketing")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	for _, want := range []string{
		"Add Marketing to Acme",
		`data-testid="dept_new.file.write"`, `workflows/marketing.yaml`,
		`departments/marketing/jobs/weekly-seo-pass.md`,
		`data-testid="dept_new.file.keep"`, `your edited file: kept, not overwritten`,
		`data-testid="dept_new.entry">- name: Marketing`,
		`prompt: departments/marketing/role.md`,
		`<form class="wb-deptnew-actions" method="post" action="/p/acme/departments/add">`,
		`<input type="hidden" name="template" value="marketing">`,
		`data-testid="dept_new.add">Add Marketing</button>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("preview missing %q", want)
		}
	}
	after, _ := os.ReadFile(config)
	if string(after) != string(before) {
		t.Error("preview changed projects.yaml")
	}
	if _, err := os.Stat(filepath.Join(proj.Root(), "workflows", "marketing.yaml")); !os.IsNotExist(err) {
		t.Error("preview wrote the workflow file")
	}
}

func TestDeptAddInstallsAndTheRoomShows(t *testing.T) {
	config, proj := deptSandbox(t)
	role := filepath.Join(proj.Root(), "departments", "marketing", "role.md")
	if err := os.MkdirAll(filepath.Dir(role), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(role, []byte("my own words\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	wrote, kept := plannedFiles(t, &proj, "marketing", "")
	if kept != 1 {
		t.Fatalf("the plan keeps %d files, want the edited role.md", kept)
	}
	resp := postAdd(t, ts.URL, url.Values{"template": {"marketing"}}, sameOrigin)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/p/acme/company?") || !strings.Contains(loc, "added=Marketing") {
		t.Fatalf("redirect %q", loc)
	}
	cfg, _ := os.ReadFile(config)
	for _, want := range []string{"# the sandbox", "departments:", "- name: Marketing", "workflow: workflows/marketing.yaml"} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf("projects.yaml missing %q:\n%s", want, cfg)
		}
	}
	if _, err := os.Stat(filepath.Join(proj.Root(), "workflows", "marketing.yaml")); err != nil {
		t.Errorf("workflow not written: %v", err)
	}
	if got, _ := os.ReadFile(role); string(got) != "my own words\n" {
		t.Errorf("edited role.md overwritten: %q", got)
	}

	code, body := getBody(t, ts.URL+loc)
	if code != http.StatusOK {
		t.Fatalf("company status %d", code)
	}
	for _, want := range []string{
		`data-testid="company.added">✓ Added Marketing: ` + strconv.Itoa(wrote) + ` files written, 1 edited file of yours kept. Start its lead from the room.</p>`,
		// The floor opens the added room's tile and inspector.
		`wb-fl-tile wb-fl-tile--added" style="left:`,
		`id="room-marketing" data-room="marketing" data-title="Marketing" data-testid="floor.inspector.marketing"`,
		// A template installs once: Legal, Sales, Video and GTM are left.
		`4 templates left`,
		`<input type="radio" name="template" value="legal" checked>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("company page after Add missing %q", want)
		}
	}
	if strings.Contains(body, `data-testid="company.new_dept.template.marketing"`) {
		t.Error("Marketing is added, the tile still offers it")
	}
}

func TestDeptAddRefusesAnInstalledTemplate(t *testing.T) {
	config, proj := deptSandbox(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	if resp := postAdd(t, ts.URL, url.Values{"template": {"marketing"}}, sameOrigin); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("first add: status %d", resp.StatusCode)
	}
	before, _ := os.ReadFile(config)

	// The same message as `department add marketing` (cmd/issue-cli).
	want := `data-testid="dept_new.error">Marketing is already a department of Acme (` + config + `): a template installs once. To add it again, remove it first: issue-cli --project acme department remove marketing</p>`
	code, body := getBody(t, ts.URL+"/p/acme/departments/new?template=marketing")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if !strings.Contains(body, want) {
		t.Errorf("preview missing the refusal %q", want)
	}
	if strings.Contains(body, `data-testid="dept_new.add"`) || strings.Contains(body, `name="name"`) {
		t.Error("a refused preview still offers Add or another name")
	}

	// A name in the form changes nothing: a template installs once.
	for _, name := range []string{"", "SEO"} {
		resp := postAdd(t, ts.URL, url.Values{"template": {"marketing"}, "name": {name}}, sameOrigin)
		if resp.StatusCode != http.StatusConflict {
			t.Errorf("second add (name %q): status %d, want 409", name, resp.StatusCode)
		}
		b, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(b), want) {
			t.Errorf("second add (name %q) missing the refusal:\n%s", name, b)
		}
	}
	if after, _ := os.ReadFile(config); string(after) != string(before) {
		t.Error("a refused Add changed projects.yaml")
	}
	if _, err := os.Stat(filepath.Join(proj.Root(), "workflows", "seo.yaml")); !os.IsNotExist(err) {
		t.Error("a refused Add wrote a file")
	}
}

func TestDeptAddOnlyFromTheOwnersBrowser(t *testing.T) {
	config, proj := deptSandbox(t)
	before, _ := os.ReadFile(config)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	for name, headers := range map[string]map[string]string{
		"cross-site":        {"Sec-Fetch-Site": "cross-site"},
		"other origin":      {"Origin": "http://evil.example"},
		"no headers (curl)": {strangerHeader: "1"},
		// A browser's headers without the owner's session: forged.
		"forged same-origin": {strangerHeader: "1", "Sec-Fetch-Site": "same-origin"},
	} {
		if resp := postAdd(t, ts.URL, url.Values{"template": {"marketing"}}, headers); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", name, resp.StatusCode)
		}
	}
	if after, _ := os.ReadFile(config); string(after) != string(before) {
		t.Error("a refused POST changed projects.yaml")
	}
	// The owner's signed-in browser (newTestServer adds the session).
	host := strings.TrimPrefix(ts.URL, "http://")
	if resp := postAdd(t, ts.URL, url.Values{"template": {"marketing"}}, map[string]string{"Origin": "http://" + host}); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("owner: status %d, want 303", resp.StatusCode)
	}
}

func TestUpdatesDepartmentCardsHaveAdd(t *testing.T) {
	_, proj := deptSandbox(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	_, body := getBody(t, ts.URL+"/p/acme/updates")
	want := `<a class="upd-btn upd-btn--ink" href="/p/acme/departments/new?template=marketing" data-testid="upd.dept.marketing.add">Add…</a>`
	if !strings.Contains(body, want) {
		t.Errorf("Updates missing %q", want)
	}
}

// The video template's preview shows the partner's install steps beside
// the plan; previewing (and the steps) never touch the partner's path.
func TestDeptPreviewShowsPartnerInstallSteps(t *testing.T) {
	_, proj := deptSandbox(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	code, body := getBody(t, ts.URL+"/p/acme/departments/new?template=video")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	for _, want := range []string{
		`data-testid="dept_new.entry">- name: Video`,
		"adapter: openmontage",
		"commit: 195eab24478dc9363326aec437aefc3701635ddf",
		"Install its partner yourself",
		`data-testid="dept_new.install">OpenMontage is a separate program under the AGPL-3.0`,
		"git clone https://github.com/calesthio/openmontage ",
		"telemetryEnabled",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("preview missing %q", want)
		}
	}
	code, body = getBody(t, ts.URL+"/p/acme/departments/new?template=marketing")
	if code != http.StatusOK || strings.Contains(body, `data-testid="dept_new.install"`) {
		t.Errorf("a template without a partner shows no install steps (status %d)", code)
	}
}

// The sales-b2c template's preview shows the handoffs to declare beside
// the plan; Add never writes them.
func TestDeptPreviewShowsHandoffsToDeclare(t *testing.T) {
	_, proj := deptSandbox(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	code, body := getBody(t, ts.URL+"/p/acme/departments/new?template=sales-b2c")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	for _, want := range []string{
		`data-testid="dept_new.entry">- name: Sales`,
		"Handoffs to declare",
		`data-testid="dept_new.handoffs">connections:`,
		"from: marketing",
		"via: hq",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("preview missing %q", want)
		}
	}
	code, body = getBody(t, ts.URL+"/p/acme/departments/new?template=marketing")
	if code != http.StatusOK || strings.Contains(body, `data-testid="dept_new.handoffs"`) {
		t.Errorf("a template without handoffs shows none (status %d)", code)
	}
}
