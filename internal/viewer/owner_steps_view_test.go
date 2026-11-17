package viewer

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/ownerstep"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// addTestStep records an owner step the way issue-cli owner-step add does.
func addTestStep(t *testing.T, proj tracker.Project, id, title, body, due string, repeat int, added time.Time) {
	t.Helper()
	ev := ownerstep.Event{Op: ownerstep.OpAdd, ID: id, At: added.Format(time.RFC3339), By: "lead-wb", Title: title, Body: body, Due: due, Repeat: repeat}
	if err := ownerstep.Append(proj.IssueDir, ev); err != nil {
		t.Fatal(err)
	}
}

func stepRows(rows []WbQueueCard) []WbQueueCard {
	var out []WbQueueCard
	for _, r := range rows {
		if r.Kind == "step" {
			out = append(out, r)
		}
	}
	return out
}

const stepBody = "Search Console → URL inspection → Request indexing:\n  https://example.test/a\n  https://example.test/b"

// Proves: AC1 — a step added with a due date is one Needs you row holding
// its due day and its exact body, on the JSON and on the page.
func TestOwnerStepShowsOnWaitingOnYou(t *testing.T) {
	now := time.Now()
	proj := setupStatusProject(t, now)
	withMockTmuxSessions(t, nil)
	before := len(projectRows(t, proj))
	due := ownerstep.AddDays(ownerstep.DayOf(now), 3)
	addTestStep(t, proj, "st-0a0a0a", "Request indexing of two articles", stepBody, due, 0, now)

	rows := projectRows(t, proj)
	if len(rows) != before+1 {
		t.Fatalf("needsYouRows %d → %d, want +1", before, len(rows))
	}
	got := stepRows(rows)
	if len(got) != 1 {
		t.Fatalf("step rows %+v", got)
	}
	r := got[0]
	if r.Slug != "st-0a0a0a" || r.Due != due || r.Body != stepBody || r.Overdue || r.Age != "due "+due ||
		!strings.Contains(r.Note, "Request indexing of two articles") || r.StepURL != "/p/"+proj.Slug+"/owner-steps/st-0a0a0a/done" {
		t.Fatalf("row %+v", r)
	}

	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	var ny NeedsYouJSON
	_, body := getResp(t, ts.URL+"/p/"+proj.Slug+"/needs-you.json")
	if err := json.Unmarshal([]byte(body), &ny); err != nil {
		t.Fatal(err)
	}
	js := stepRows(ny.Rows)
	if len(js) != 1 || js[0].Body != stepBody || js[0].Due != due {
		t.Fatalf("needs-you.json step rows %+v", js)
	}
	_, html := getBody(t, ts.URL+"/p/"+proj.Slug+"/needs-you")
	assertContains(t, html, `data-kind="step" data-slug="st-0a0a0a" id="st-0a0a0a" data-due="`+due+`"`,
		`data-testid="needs_you.step.body">Search Console → URL inspection → Request indexing:`+"\n  https://example.test/a",
		`data-testid="needs_you.step.done"`, `due `+due, `data-section="steps"`)
	// The Status page lists it with the rest of the waiting rows.
	_, status := getBody(t, ts.URL+"/p/"+proj.Slug+"/status?classic=1")
	assertContains(t, status, `data-testid="status.waiting.other.step.`)
}

// Proves: AC2 — Done without the owner's sign-in is 403, names the CLI way
// around and writes nothing; the owner's Done ticks the step and its row
// goes. (The CLI refusal of a worker is TestOwnerStepDoneRefusesWorker.)
func TestOwnerStepTickIsOwnerOnly(t *testing.T) {
	now := time.Now()
	proj := setupStatusProject(t, now)
	withMockTmuxSessions(t, nil)
	addTestStep(t, proj, "st-0b0b0b", "Upload the 512 px logo", "logo.png to the directory form", ownerstep.DayOf(now), 0, now)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	url := ts.URL + "/p/" + proj.Slug + "/owner-steps/st-0b0b0b/done"

	logBefore, _ := os.ReadFile(ownerstep.LogPath(proj.IssueDir))
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(strangerHeader, "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var refusal bytes.Buffer
	refusal.ReadFrom(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(refusal.String(), "owner-step done") {
		t.Fatalf("Done without sign-in: %d %s", resp.StatusCode, refusal.String())
	}
	if logAfter, _ := os.ReadFile(ownerstep.LogPath(proj.IssueDir)); !bytes.Equal(logBefore, logAfter) {
		t.Fatal("a refused Done wrote the log")
	}
	if len(stepRows(projectRows(t, proj))) != 1 {
		t.Fatal("the step left before the owner ticked it")
	}

	code, out := postJSON(t, url, `{}`)
	if code != 200 || out["id"] != "st-0b0b0b" {
		t.Fatalf("owner's Done: %d %v", code, out)
	}
	if n := len(stepRows(projectRows(t, proj))); n != 0 {
		t.Fatalf("%d step rows after Done", n)
	}
	evs, _ := ownerstep.Read(proj.IssueDir)
	if last := evs[len(evs)-1]; last.Op != ownerstep.OpDone || last.Role != ownerstep.RoleOwnerBrowser || last.By != tracker.OwnerAuthor {
		t.Fatalf("last event %+v", last)
	}
	if code, _ := postJSON(t, url, `{}`); code != http.StatusConflict {
		t.Fatalf("Done twice: %d, want 409", code)
	}
}

// Proves: AC4 — a step past its due day is marked overdue: red, its
// label and line say so, data-overdue on the page; it sorts before a
// step that is not.
func TestOwnerStepOverdueRow(t *testing.T) {
	now := time.Now()
	proj := setupStatusProject(t, now)
	withMockTmuxSessions(t, nil)
	past := ownerstep.AddDays(ownerstep.DayOf(now), -2)
	addTestStep(t, proj, "st-0c0c0c", "Later step", "b", ownerstep.AddDays(ownerstep.DayOf(now), 5), 0, now)
	addTestStep(t, proj, "st-0d0d0d", "Reinstall the phone build", "a", past, 7, now.AddDate(0, 0, -9))
	got := stepRows(projectRows(t, proj))
	if len(got) != 2 || got[0].Slug != "st-0d0d0d" {
		t.Fatalf("rows %+v", got)
	}
	r := got[0]
	if !r.Overdue || r.Tone != "red" || !strings.Contains(r.Label, "overdue") || r.Age != "overdue: was due "+past+" (2 days ago)" || !strings.Contains(r.Note, "repeats 7d") {
		t.Fatalf("overdue row %+v", r)
	}
	if got[1].Overdue || got[1].Tone != "you" {
		t.Fatalf("on-time row %+v", got[1])
	}
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	_, html := getBody(t, ts.URL+"/p/"+proj.Slug+"/needs-you")
	assertContains(t, html, `data-slug="st-0d0d0d" id="st-0d0d0d" data-due="`+past+`" data-overdue="1"`, "wb-ny-row--red")
}
