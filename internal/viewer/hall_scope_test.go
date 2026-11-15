package viewer

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// hallDeptProject is hallPartProject with Marketing on the bundled
// marketing workflow (workflows/marketing.yaml) and one issue of its own.
func hallDeptProject(t *testing.T) tracker.Project {
	t.Helper()
	p := hallPartProject(t)
	raw, err := os.ReadFile(filepath.Join("..", "..", "cmd", "issue-cli", "templates", "workflow", "marketing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(p.WorkDir, "workflows", "marketing.yaml")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	issue := "---\ntitle: \"Spring post\"\nstatus: \"draft\"\ndepartment: Marketing\ntype: piece\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(p.IssueDir, "spring-post.md"), []byte(issue), 0o644); err != nil {
		t.Fatal(err)
	}
	p.Departments = []tracker.Department{{Name: "Marketing", Color: "#E2559B", Workflow: file}}
	return p
}

func TestHallDepartmentShowsTypePaths(t *testing.T) {
	p := hallDeptProject(t)
	base := hallEditServer(t, p)

	code, page := getBody(t, base+"/p/agents/hall?department=marketing")
	if code != http.StatusOK {
		t.Fatalf("department hall %d:\n%s", code, page)
	}
	for _, want := range []string{
		`<h1 class="wb-h1" data-testid="wb.title">Marketing hall</h1>`,
		`data-testid="hall.switch"`, `href="/p/agents/hall">`, // back to Product's
		`href="/p/agents/hall/station/owner-review?department=marketing"`,
		`href="/p/agents/hall/gate/publish?department=marketing"`, // the gate before publish
		`data-testid="hall.types"`,
		`data-type="piece"`, `data-type="seo-pass"`, `data-type="foundation"`, `data-type="keyword-research"`, `data-type="market-size"`,
		`data-testid="hall.type.gate"`, "only you, even in YOLO",
		"Spring post", // its own issue
	} {
		if !strings.Contains(page, want) {
			t.Errorf("department hall lacks %q", want)
		}
	}
	// Product's issues stay in Product's hall; no CI scanner or Retro card.
	for _, not := range []string{"Asker", `data-testid="hall.ci"`, "Retro and feedback"} {
		if strings.Contains(page, not) {
			t.Errorf("department hall shows %q", not)
		}
	}
	// piece's path is brief → draft → owner review → gate → publish → done,
	// seo-pass adds measure.
	piece := hallTypeRow(page, "piece")
	for _, st := range []string{">brief<", ">draft<", ">owner review<", ">publish<", ">done<"} {
		if !strings.Contains(piece, st) {
			t.Errorf("piece's path lacks %s:\n%s", st, piece)
		}
	}
	if strings.Contains(piece, ">measure<") || strings.Contains(piece, ">research<") {
		t.Errorf("piece's path has a status off its path:\n%s", piece)
	}
	if !strings.Contains(hallTypeRow(page, "seo-pass"), ">measure<") {
		t.Error("seo-pass's path lacks measure")
	}

	// Product's hall keeps its own line and its issues, with the switcher.
	_, prod := getBody(t, base+"/p/agents/hall")
	for _, want := range []string{`<h1 class="wb-h1" data-testid="wb.title">Product hall</h1>`, `data-testid="hall.switch"`, `href="/p/agents/hall?department=marketing"`, "Retro and feedback"} {
		if !strings.Contains(prod, want) {
			t.Errorf("Product hall lacks %q", want)
		}
	}
	if strings.Contains(prod, "Spring post") {
		t.Error("Product hall shows Marketing's issue")
	}

	// The station page links within Marketing's hall.
	_, part := getBody(t, base+"/p/agents/hall/station/owner-review?department=marketing")
	for _, want := range []string{"← Marketing hall", `href="/p/agents/hall?department=marketing"`, `href="/p/agents/hall/station/owner-review/edit?department=marketing"`} {
		if !strings.Contains(part, want) {
			t.Errorf("Marketing station page lacks %q", want)
		}
	}

	// Unknown departments, and one on Product's workflow, have no hall.
	if code, _ := getBody(t, base+"/p/agents/hall?department=sales"); code != http.StatusNotFound {
		t.Errorf("unknown department: %d", code)
	}
	if code, _ := getBody(t, base+"/p/agents/hall/ci?department=marketing"); code != http.StatusNotFound {
		t.Errorf("a department hall's CI: %d", code)
	}
}

func TestHallWithoutDepartmentFilesHasNoSwitch(t *testing.T) {
	p := hallPartProject(t)
	base := hallEditServer(t, p)
	_, page := getBody(t, base+"/p/agents/hall")
	if strings.Contains(page, `data-testid="hall.switch"`) {
		t.Error("a project with no department workflow shows the switcher")
	}
	// Product's types still show their paths: tweak skips in progress.
	tweak := hallTypeRow(page, "tweak")
	if !strings.Contains(tweak, ">backlog<") || strings.Contains(tweak, ">in progress<") {
		t.Errorf("tweak's path:\n%s", tweak)
	}
}

// git runs git in dir and returns its output.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestHallEditDepartmentWritesInPlace(t *testing.T) {
	p := hallDeptProject(t)
	dir := hallEditRepo(t, p) // workflow.yaml committed, workflows/ untracked
	gitIn(t, dir, "config", "maintenance.auto", "false")
	gitIn(t, dir, "config", "gc.auto", "0")
	base := hallEditServer(t, p)
	file := p.Departments[0].Workflow
	head := gitIn(t, dir, "rev-parse", "HEAD")

	code, page := getBody(t, base+"/p/agents/hall/station/draft/edit?department=marketing")
	if code != http.StatusOK {
		t.Fatalf("edit form %d:\n%s", code, page)
	}
	for _, want := range []string{`data-testid="hall.edit.untracked"`, "commits nothing", `action="/p/agents/hall/station/draft/edit/preview?department=marketing"`, "workflows/marketing.yaml"} {
		if !strings.Contains(page, want) {
			t.Errorf("edit form lacks %q", want)
		}
	}
	if strings.Contains(page, `data-testid="hall.edit.reason"`) {
		t.Error("the in-place form asks for a reason no commit quotes")
	}

	v := formValues(page)
	v.Set("description", "Making the piece, in the brand's voice")
	code, pv, _ := hallPost(t, base+"/p/agents/hall/station/draft/edit/preview?department=marketing", v)
	if code != http.StatusOK || !strings.Contains(pv, `data-testid="hall.edit.diff"`) || !strings.Contains(pv, "in the brand&#39;s voice") ||
		!strings.Contains(pv, `formaction="/p/agents/hall/station/draft/edit/save?department=marketing"`) || !strings.Contains(pv, "write in place, no commit") {
		t.Fatalf("preview %d:\n%s", code, pv)
	}

	code, _, resp := hallPost(t, base+"/p/agents/hall/station/draft/edit/save?department=marketing", v)
	if code != http.StatusSeeOther {
		t.Fatalf("save %d", code)
	}
	loc := resp.Header.Get("Location")
	if loc != "/p/agents/hall/station/draft?department=marketing&saved=in-place" {
		t.Fatalf("redirect %q", loc)
	}
	wf, err := tracker.LoadWorkflow(file)
	if err != nil || wf.GetStatus("draft").Description != "Making the piece, in the brand's voice" {
		t.Fatalf("the file was not written: %v", err)
	}
	// Nothing committed, nothing staged: workflows/ is still untracked.
	if now := gitIn(t, dir, "rev-parse", "HEAD"); now != head {
		t.Error("the save made a commit")
	}
	if st := gitIn(t, dir, "status", "--porcelain"); !strings.Contains(st, "?? workflows/") || strings.Contains(st, "A ") {
		t.Errorf("git status after the save:\n%s", st)
	}
	_, part := getBody(t, base+loc)
	if !strings.Contains(part, "Saved in place") {
		t.Error("the station page doesn't say it was saved in place")
	}
}

func TestHallEditKeepsOwnerOnlyGate(t *testing.T) {
	p := hallDeptProject(t)
	base := hallEditServer(t, p)
	file := p.Departments[0].Workflow

	_, page := getBody(t, base+"/p/agents/hall/station/publish/edit?department=marketing")
	if !strings.Contains(page, `data-testid="hall.edit.owner_only"`) {
		t.Fatalf("the owner_only gate into publish has no mark:\n%s", page)
	}
	// The form as it came, gate kept on: the edit changes nothing, so
	// owner_only: stays in the file.
	_, pv, _ := hallPost(t, base+"/p/agents/hall/station/publish/edit/preview?department=marketing", formValues(page))
	if !strings.Contains(pv, "The edit changes nothing") {
		t.Fatalf("an untouched form changes the file:\n%s", pv)
	}
	// A real edit beside the gate keeps owner_only too.
	v := formValues(page)
	v.Set("description", "Putting the approved piece out, today")
	_, pv, _ = hallPost(t, base+"/p/agents/hall/station/publish/edit/preview?department=marketing", v)
	if strings.Contains(pv, `wb-diff-del">-        owner_only`) || strings.Contains(pv, `wb-diff-del">-      - type: &#34;require_human_approval&#34;`) {
		t.Fatalf("the preview drops the gate's owner_only:\n%s", pv)
	}
	if _, _, resp := hallPost(t, base+"/p/agents/hall/station/publish/edit/save?department=marketing", v); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save %d", resp.StatusCode)
	}
	wf, _ := tracker.LoadWorkflow(file)
	if !wf.OwnerOnlyGate("owner review", "publish") {
		t.Error("owner review → publish lost owner_only")
	}
}

// hallTypeRow is the type's row in the hall's Paths by type.
func hallTypeRow(page, name string) string {
	i := strings.Index(page, `data-type="`+name+`"`)
	if i < 0 {
		return ""
	}
	rest := page[i+1:]
	if j := strings.Index(rest, `data-testid="hall.type"`); j >= 0 {
		return rest[:j]
	}
	if j := strings.Index(rest, "</section>"); j >= 0 {
		return rest[:j]
	}
	return rest
}
