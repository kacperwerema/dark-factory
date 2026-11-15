package viewer

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/postmortem"
	"github.com/kacperwerema/dark-factory/internal/retro"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func postMark(t *testing.T, url, body string, headers map[string]string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url+"/p/acme/postmortems/mark", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func markBody(act, file string, ids ...string) string {
	type item struct {
		File string `json:"file"`
		ID   string `json:"id"`
	}
	var items []item
	for _, id := range ids {
		items = append(items, item{file, id})
	}
	b, _ := json.Marshal(map[string]any{"act": act, "items": items, "note": "dup"})
	return string(b)
}

// AC2: the page lists every open action of every post-mortem, archive
// included, by place, with copies together and all but the newest ticked.
func TestPostmortemsPageGroupsByPlaceAndCopy(t *testing.T) {
	proj, url, _ := postmortemSandbox(t)
	root := proj.Root()
	os.WriteFile(filepath.Join(proj.IssueDir, "till.md"), []byte("---\ntitle: \"Till\"\nslug: \"till\"\nstatus: \"done\"\n---\n\nBody.\n"), 0o644)
	dir := filepath.Join(root, "retros")
	p, err := postmortem.Save(dir, postmortem.Meta{Issue: "till", Title: "Till", Status: "done"}, pmRetroBody, time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(dir, retro.ArchiveDir), 0o755)
	if err := os.Rename(p, filepath.Join(dir, retro.ArchiveDir, filepath.Base(p))); err != nil {
		t.Fatal(err)
	}
	resp, body := getResp(t, url+"/p/acme/postmortems")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d:\n%s", resp.StatusCode, body)
	}
	for _, want := range []string{
		`data-testid="postmortems.count">8</span>`, `every open action of 2 post-mortems`,
		`data-testid="postmortems.place.file"`, `data-testid="postmortems.place.workflow"`,
		`data-testid="postmortems.joined">8 in 4 groups`,
		`data-testid="postmortems.row.till.a1"`, `data-testid="postmortems.row.cart.a1"`,
		`data-testid="postmortems.warn">Each agreed copy files its own experiment`,
		`data-mark-url="/p/acme/postmortems/mark"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if n := strings.Count(body, `data-testid="postmortems.kept"`); n != 4 {
		t.Errorf("%d rows kept, want the newest of each of 4 groups", n)
	}
	if strings.Contains(body, "/decide") {
		t.Error("the queue page offers the round's Agree")
	}
	// The ways in.
	if _, ny := getResp(t, url+"/p/acme/needs-you"); !strings.Contains(ny, `href="/p/acme/postmortems" data-testid="needs_you.postmortems"`) {
		t.Error("Needs you doesn't link the page")
	}
	if _, pm := getResp(t, url+"/p/acme/postmortem/cart"); !strings.Contains(pm, `data-testid="postmortem.queue"`) {
		t.Error("the post-mortem page doesn't link the queue")
	}
}

func TestPostmortemsPageEmpty(t *testing.T) {
	_, proj := deptSandbox(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	if _, body := getResp(t, ts.URL+"/p/"+proj.Slug+"/postmortems"); !strings.Contains(body, `data-testid="postmortems.empty"`) {
		t.Fatalf("no empty state:\n%s", body)
	}
}

// AC2: a group's Refuse refuses the rows posted and no others; the issue's
// trail says so, once.
func TestPostmortemsMarkRefusesOnlyTheTicked(t *testing.T) {
	proj, url, path := postmortemSandbox(t)
	code, out := postMark(t, url, markBody("refuse", filepath.Base(path), "A1", "A3"), nil)
	if code != 200 || out["ok"] != true {
		t.Fatalf("mark: %d %v", code, out)
	}
	raw, _ := os.ReadFile(path)
	acts := postmortem.Actions(string(raw))
	if p := postmortem.Pending(acts); len(p) != 2 || p[0].ID != "A2" || p[1].ID != "A4" {
		t.Fatalf("still open: %+v", p)
	}
	if !strings.HasSuffix(acts[0].Refused, ": dup") {
		t.Errorf("A1's line: %q", acts[0].Refused)
	}
	comments, _ := tracker.LoadComments(filepath.Join(proj.IssueDir, "cart.md"))
	if len(comments) != 1 || !strings.Contains(comments[0].Text, "A1, A3 refused on the actions page (retros/") {
		t.Fatalf("trail: %+v", comments)
	}
	rows := needsYouRows(&proj, "", "/p/acme", nil, time.Now())
	for _, r := range rows {
		if r.Kind == "postmortem" && !strings.Contains(r.Note, "2 actions wait") {
			t.Errorf("Needs you: %q", r.Note)
		}
	}
}

// AC3: overtaken needs a commit on main; marked, the action is off the
// queue and Needs you's count, and the retro names the commit.
func TestPostmortemsMarkOvertaken(t *testing.T) {
	proj, url, path := postmortemSandbox(t)
	mark := func(sha string) (int, map[string]any) {
		b, _ := json.Marshal(map[string]any{"act": "overtaken", "sha": sha, "items": []map[string]string{{"file": filepath.Base(path), "id": "A2"}}})
		return postMark(t, url, string(b), nil)
	}
	before, _ := os.ReadFile(path)
	for _, sha := range []string{"", "deadbeef", "--help"} {
		if code, out := mark(sha); code != http.StatusBadRequest {
			t.Errorf("sha %q: %d %v", sha, code, out)
		}
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("a refused sha changed the retro")
	}
	head, err := exec.Command("git", "-C", proj.Root(), "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(string(head))
	if code, out := mark(sha); code != 200 || out["ok"] != true {
		t.Fatalf("mark: %d %v", code, out)
	}
	raw, _ := os.ReadFile(path)
	acts := postmortem.Actions(string(raw))
	if !strings.HasPrefix(acts[1].Overtaken, gitdir.ShortSHA(sha)+" · ") {
		t.Fatalf("A2: %+v", acts[1])
	}
	if _, body := getResp(t, url+"/p/acme/postmortem/cart"); !strings.Contains(body, `outcome">Overtaken: `+gitdir.ShortSHA(sha)) {
		t.Error("the post-mortem page doesn't show the outcome")
	}
	if _, body := getResp(t, url+"/p/acme/postmortems"); strings.Contains(body, `postmortems.row.cart.a2`) {
		t.Error("the queue still lists A2")
	}
}

// AC4: only the owner's browser passes.
func TestPostmortemsMarkIsTheOwners(t *testing.T) {
	_, url, path := postmortemSandbox(t)
	before, _ := os.ReadFile(path)
	body := markBody("refuse", filepath.Base(path), "A1")
	for name, headers := range map[string]map[string]string{
		"curl or an agent": {strangerHeader: "1"},
		"forged origin":    {strangerHeader: "1", "Sec-Fetch-Site": "same-origin"},
		"other site":       {"Origin": "http://evil.example"},
	} {
		if code, out := postMark(t, url, body, headers); code != http.StatusForbidden {
			t.Errorf("%s: %d %v, want 403", name, code, out)
		}
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("a refused mark changed the retro")
	}
}

// AC4: refusing or marking makes no memory, commit or experiment.
func TestPostmortemsMarkAppliesNothing(t *testing.T) {
	proj, url, path := postmortemSandbox(t)
	headOf := func() string {
		out, err := exec.Command("git", "-C", proj.Root(), "rev-parse", "HEAD").Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	head := headOf()
	issues, _ := proj.LoadIssues()
	if code, out := postMark(t, url, markBody("refuse", filepath.Base(path), "A1", "A2", "A3", "A4"), nil); code != 200 || out["ok"] != true {
		t.Fatalf("mark: %d %v", code, out)
	}
	if headOf() != head {
		t.Error("a mark made a commit")
	}
	set, _ := memory.ProjectStore(proj.IssueDir).Load()
	if mems := set.Active(time.Now()); len(mems) != 0 {
		t.Errorf("a mark made a memory: %+v", mems)
	}
	if after, _ := proj.LoadIssues(); len(after) != len(issues) {
		t.Errorf("a mark filed an issue: %d, was %d", len(after), len(issues))
	}
}

// A file is looked up in the queue, never read as a path; a decided row is
// that row's error and the others in the post still go through.
func TestPostmortemsMarkRefusesAFileOutsideTheQueue(t *testing.T) {
	proj, url, path := postmortemSandbox(t)
	outside := filepath.Join(proj.Root(), "CLAUDE.md")
	before, _ := os.ReadFile(outside)
	for _, file := range []string{outside, "../CLAUDE.md", "CLAUDE.md"} {
		code, out := postMark(t, url, markBody("refuse", file, "A1"), nil)
		if code != 200 || out["ok"] != false {
			t.Fatalf("%s: %d %v", file, code, out)
		}
	}
	if after, _ := os.ReadFile(outside); string(after) != string(before) {
		t.Fatal("a mark wrote outside the queue")
	}
	name := filepath.Base(path)
	if _, err := postmortem.Refuse(path, "A1", "owner", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	code, out := postMark(t, url, markBody("refuse", name, "A1", "A2"), nil)
	rows, _ := out["outcomes"].([]any)
	if code != 200 || out["ok"] != false || len(rows) != 2 {
		t.Fatalf("stale page: %d %v", code, out)
	}
	if e, _ := rows[0].(map[string]any)["error"].(string); !strings.Contains(e, "not open") {
		t.Errorf("A1: %v", rows[0])
	}
	if e, _ := rows[1].(map[string]any)["error"].(string); e != "" {
		t.Errorf("A2: %v", rows[1])
	}
}

func TestPostmortemsMarkRefusesABadBody(t *testing.T) {
	_, url, path := postmortemSandbox(t)
	for _, b := range []string{`{`, `{"act":"refuse"}`, markBody("agree", filepath.Base(path), "A1")} {
		if code, out := postMark(t, url, b, nil); code != http.StatusBadRequest {
			t.Errorf("%s: %d %v", b, code, out)
		}
	}
}

func postUndo(t *testing.T, url, file string, headers map[string]string, ids ...string) (int, map[string]any) {
	t.Helper()
	var items []map[string]string
	for _, id := range ids {
		items = append(items, map[string]string{"file": file, "id": id})
	}
	b, _ := json.Marshal(map[string]any{"items": items})
	req, _ := http.NewRequest(http.MethodPost, url+"/p/acme/postmortems/undo", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// AC1: the owner undoes a Refuse: the action is back in the queue, off
// Marked lately, the trail says so and it can be answered again. The last
// mark on the page is still undoable when the queue is empty.
func TestPostmortemsUndoReopensARefuse(t *testing.T) {
	proj, url, path := postmortemSandbox(t)
	file := filepath.Base(path)
	if code, out := postMark(t, url, markBody("refuse", file, "A1", "A2", "A3", "A4"), nil); code != 200 || out["ok"] != true {
		t.Fatalf("mark: %d %v", code, out)
	}
	_, body := getResp(t, url+"/p/acme/postmortems")
	for _, want := range []string{
		`data-testid="postmortems.empty"`, `data-testid="postmortems.marked"`,
		`data-testid="postmortems.marked.row.cart.a1"`, `data-undo-url="/p/acme/postmortems/undo"`,
		"Refused: " + time.Now().Format("2006-01-02") + " (owner): dup",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	code, out := postUndo(t, url, file, nil, "A1")
	if code != 200 || out["ok"] != true {
		t.Fatalf("undo: %d %v", code, out)
	}
	_, body = getResp(t, url+"/p/acme/postmortems")
	if !strings.Contains(body, `data-testid="postmortems.row.cart.a1"`) {
		t.Error("the queue doesn't list A1 again")
	}
	if strings.Contains(body, `postmortems.marked.row.cart.a1"`) {
		t.Error("Marked lately still lists A1")
	}
	comments, _ := tracker.LoadComments(filepath.Join(proj.IssueDir, "cart.md"))
	if last := comments[len(comments)-1]; !strings.Contains(last.Text, "A1 reopened on the actions page (retros/") {
		t.Errorf("trail: %+v", last)
	}
	if _, pm := getResp(t, url+"/p/acme/postmortem/cart"); !strings.Contains(pm, `data-testid="postmortem.action.a1.reopened">Reopened: `) {
		t.Error("the post-mortem page doesn't show the reopened line")
	}
	if code, out := postMark(t, url, markBody("refuse", file, "A1"), nil); code != 200 || out["ok"] != true {
		t.Fatalf("mark again: %d %v", code, out)
	}
}

// AC2: only the owner's browser passes, and the retro stays as it was.
func TestPostmortemsUndoIsTheOwners(t *testing.T) {
	_, url, path := postmortemSandbox(t)
	file := filepath.Base(path)
	if code, out := postMark(t, url, markBody("refuse", file, "A1"), nil); code != 200 {
		t.Fatalf("mark: %d %v", code, out)
	}
	before, _ := os.ReadFile(path)
	for name, headers := range map[string]map[string]string{
		"curl or an agent": {strangerHeader: "1"},
		"forged origin":    {strangerHeader: "1", "Sec-Fetch-Site": "same-origin"},
		"other site":       {"Origin": "http://evil.example"},
	} {
		if code, out := postUndo(t, url, file, headers, "A1"); code != http.StatusForbidden {
			t.Errorf("%s: %d %v, want 403", name, code, out)
		}
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("a refused undo changed the retro")
	}
}

// A row is looked up, never read as a path: what isn't a recent mark fails
// alone and the others go through.
func TestPostmortemsUndoRefusesWhatIsNotMarked(t *testing.T) {
	_, url, path := postmortemSandbox(t)
	file := filepath.Base(path)
	if code, out := postMark(t, url, markBody("refuse", file, "A1"), nil); code != 200 {
		t.Fatalf("mark: %d %v", code, out)
	}
	code, out := postUndo(t, url, file, nil, "A2", "A1")
	if code != 200 || out["ok"] != false {
		t.Fatalf("undo: %d %v", code, out)
	}
	rows := out["outcomes"].([]any)
	if rows[0].(map[string]any)["error"] == nil || rows[1].(map[string]any)["error"] != nil {
		t.Fatalf("rows: %v", rows)
	}
	for _, file := range []string{"../../etc/passwd", "../" + file} {
		before, _ := os.ReadFile(path)
		code, out := postUndo(t, url, file, nil, "A1")
		if rows, _ := out["outcomes"].([]any); code != 200 || len(rows) != 1 || rows[0].(map[string]any)["error"] == nil {
			t.Errorf("file %q: %d %v", file, code, out)
		}
		if after, _ := os.ReadFile(path); string(after) != string(before) {
			t.Fatalf("file %q changed the retro", file)
		}
	}
	if code, out := postUndo(t, url, file, nil); code != http.StatusBadRequest {
		t.Errorf("no rows: %d %v", code, out)
	}
}
