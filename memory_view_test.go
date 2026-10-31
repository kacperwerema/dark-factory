package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/memory"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

type memSandbox struct {
	proj                   tracker.Project
	base                   string
	own, dept, old, shared memory.Memory
}

// memorySandbox is project Acme with a project memory (from issue epic), a
// department one, a superseded one and a machine one, the machine store
// under a temp XDG_DATA_HOME.
func memorySandbox(t *testing.T) memSandbox {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	proj, base := ownerChecksSandbox(t)
	now := time.Now().Add(-time.Hour)
	s := memory.ProjectStore(proj.IssueDir)
	addM := func(st memory.Store, d memory.Draft, opt memory.AddOptions) memory.Memory {
		t.Helper()
		r, err := st.Add(d, opt, now)
		if err != nil {
			t.Fatal(err)
		}
		return r.Memory
	}
	sb := memSandbox{proj: proj, base: base}
	sb.old = addM(s, memory.Draft{Text: "the epic's pages draw cards by hand", Kind: memory.KindConvention}, memory.AddOptions{})
	sb.own = addM(s, memory.Draft{Text: "the epic's pages use wb components", Kind: memory.KindConvention, Project: "acme",
		Source: memory.Source{Issue: "epic", Commit: "abc1234"}, Author: "agent-epic", SuggestPin: true}, memory.AddOptions{Mode: memory.ModeSupersedes, Target: sb.old.ID})
	sb.dept = addM(s, memory.Draft{Text: "marketing posts go out on Tuesdays", Kind: memory.KindFact, Scope: "department:marketing"}, memory.AddOptions{})
	sb.shared = addM(memory.MachineStore(), memory.Draft{Text: "make shot takes one screenshot", Kind: memory.KindHowto, Project: "other"}, memory.AddOptions{})
	return sb
}

func postMemory(t *testing.T, base, action string, form url.Values, browser bool) (int, string, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+"/p/acme/memory/"+action, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// browser is the owner's signed-in browser (newTestServer's session);
	// otherwise a script forging the browser's headers without it.
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if !browser {
		req.Header.Set(strangerHeader, "1")
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Location"), string(b)
}

// TestMemoryPageShowsEveryMemoryWithHistory (AC3): the owner sees every
// scope, the source, kind, links and history, and the actions.
func TestMemoryPageShowsEveryMemoryWithHistory(t *testing.T) {
	sb := memorySandbox(t)
	resp, body := getResp(t, sb.base+"/p/acme/memory")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	for _, want := range []string{
		`data-testid="memory.page"`,
		`data-testid="memory.row.` + sb.own.ID + `"`,
		`the epic&#39;s pages use wb components`,
		`href="/p/acme/issue/epic" data-testid="memory.row.` + sb.own.ID + `.source"`,
		`commit abc1234`,
		`data-testid="memory.row.` + sb.own.ID + `.suggested"`,
		`supersedes ` + sb.old.ID,
		`data-testid="memory.row.` + sb.own.ID + `.history"`,
		`data-testid="memory.row.` + sb.dept.ID + `"`,
		`department:marketing`,
		`data-testid="memory.row.` + sb.shared.ID + `"`,
		`from other`,
		`action="/p/acme/memory/edit"`,
		`formaction="/p/acme/memory/delete"`,
		`formaction="/p/acme/memory/pin"`,
		`data-testid="memory.recent"`,
	} {
		assertContains(t, body, want)
	}
	if strings.Contains(body, `data-testid="memory.row.`+sb.old.ID+`"`) {
		t.Error("a superseded memory shows without the filter")
	}

	_, body = getResp(t, sb.base+"/p/acme/memory?state=all&kind=convention")
	assertContains(t, body, `data-testid="memory.row.`+sb.old.ID+`"`)
	assertContains(t, body, `superseded`)
	if strings.Contains(body, `data-testid="memory.row.`+sb.dept.ID+`"`) {
		t.Error("the kind filter let a fact through")
	}
	_, body = getResp(t, sb.base+"/p/acme/memory?scope=machine")
	if strings.Contains(body, `data-testid="memory.row.`+sb.own.ID+`"`) || !strings.Contains(body, sb.shared.ID) {
		t.Error("the scope filter")
	}

	_, body = getResp(t, sb.base+"/p/acme/memory.json")
	var got struct{ Project, Machine []memory.Memory }
	if err := json.Unmarshal([]byte(body), &got); err != nil || len(got.Project) != 3 || len(got.Machine) != 1 {
		t.Fatalf("memory.json %s (%v)", body, err)
	}
}

// TestMemoryPageOwnerWrites (AC3, AC4): the owner's browser edits, pins
// and deletes; a script is refused; a secret is refused; the delete
// scrubs the text from the log.
func TestMemoryPageOwnerWrites(t *testing.T) {
	sb := memorySandbox(t)
	if code, _, body := postMemory(t, sb.base, "delete", url.Values{"id": {sb.own.ID}}, false); code != http.StatusForbidden || !strings.Contains(body, "only the owner") {
		t.Fatalf("a script's delete: %d %s", code, body)
	}
	code, loc, _ := postMemory(t, sb.base, "edit", url.Values{"id": {sb.own.ID}, "text": {"the epic's pages use wb/* components only"}}, true)
	if code != http.StatusSeeOther || !strings.Contains(loc, "done=") {
		t.Fatalf("edit: %d %s", code, loc)
	}
	set, _ := memory.ProjectStore(sb.proj.IssueDir).Load()
	if m := set.Get(sb.own.ID); m.Text != "the epic's pages use wb/* components only" || set.History(sb.own.ID)[1].Op != memory.OpOwnerEdit {
		t.Fatalf("after edit %+v", m)
	}
	code, loc, _ = postMemory(t, sb.base, "edit", url.Values{"id": {sb.own.ID}, "text": {"login with password=hunter22"}}, true)
	if code != http.StatusSeeOther || !strings.Contains(loc, "error=") || strings.Contains(loc, "hunter22") {
		t.Fatalf("secret edit: %d %s", code, loc)
	}
	if code, _, _ := postMemory(t, sb.base, "pin", url.Values{"id": {sb.own.ID}}, true); code != http.StatusSeeOther {
		t.Fatalf("pin: %d", code)
	}
	set, _ = memory.ProjectStore(sb.proj.IssueDir).Load()
	if m := set.Get(sb.own.ID); !m.Pinned || m.PinnedBy != "owner" {
		t.Fatalf("after pin %+v", m)
	}
	if code, _, _ := postMemory(t, sb.base, "delete", url.Values{"id": {sb.own.ID}}, true); code != http.StatusSeeOther {
		t.Fatalf("delete: %d", code)
	}
	data, _ := os.ReadFile(memory.ProjectPath(sb.proj.IssueDir))
	if strings.Contains(string(data), "wb/* components") || strings.Contains(string(data), "hunter22") {
		t.Errorf("the deleted text is still in the log:\n%s", data)
	}
	if code, _, _ := postMemory(t, sb.base, "delete", url.Values{"id": {sb.shared.ID}}, true); code != http.StatusSeeOther {
		t.Fatalf("delete machine: %d", code)
	}
	if set, _ := memory.MachineStore().Load(); len(set.All()) != 0 {
		t.Error("machine memory not deleted")
	}
	if code, _, _ := postMemory(t, sb.base, "delete", url.Values{"id": {"bogus"}}, true); code != http.StatusBadRequest {
		t.Errorf("bad id: %d", code)
	}
}

func TestMemoryPageEmptyStateNamesTheCommand(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	_, base := ownerChecksSandbox(t)
	_, body := getResp(t, base+"/p/acme/memory")
	assertContains(t, body, `data-testid="memory.empty"`)
	assertContains(t, body, `issue-cli memory add &#34;&lt;fact&gt;&#34;`)
}

// Phase 4 (AC2): the page lists memories by entity: the index with
// counts and who knows what, ?entity= filters, ?group=entity groups a
// memory under each of its entities; memory.json carries the index.
func TestMemoryPageByEntity(t *testing.T) {
	sb := memorySandbox(t)
	s := memory.ProjectStore(sb.proj.IssueDir)
	now := time.Now().Add(-time.Minute)
	who, err := s.Add(memory.Draft{Text: "@lead-acme knows the #playtest runner", Kind: memory.KindWho}, memory.AddOptions{}, now)
	if err != nil {
		t.Fatal(err)
	}
	fact, err := s.Add(memory.Draft{Text: "playtest shots go in the issue", Kind: memory.KindFact, Tags: []string{"playtest", "@owner"}}, memory.AddOptions{}, now)
	if err != nil {
		t.Fatal(err)
	}
	_, body := getResp(t, sb.base+"/p/acme/memory")
	for _, want := range []string{
		`data-testid="memory.entities"`,
		`data-testid="memory.entity.topic-playtest"`,
		`data-testid="memory.entity.agent-lead-acme"`,
		`known by @lead-acme`,
		`href="/p/acme/memory?entity=%23playtest"`,
	} {
		assertContains(t, body, want)
	}

	_, body = getResp(t, sb.base+"/p/acme/memory?entity=%23playtest")
	assertContains(t, body, `data-testid="memory.entity_filter"`)
	if !strings.Contains(body, `memory.row.`+who.Memory.ID+`"`) || !strings.Contains(body, `memory.row.`+fact.Memory.ID+`"`) || strings.Contains(body, `memory.row.`+sb.own.ID+`"`) {
		t.Fatal("?entity=#playtest kept the wrong rows")
	}

	_, body = getResp(t, sb.base+"/p/acme/memory?group=entity")
	for _, want := range []string{`data-testid="memory.group.topic-playtest"`, `data-testid="memory.group.agent-owner"`, `data-testid="memory.group.none"`} {
		assertContains(t, body, want)
	}
	if strings.Index(body, `memory.group.topic-playtest`) > strings.Index(body, `memory.group.none`) {
		t.Error("the no-entity group is not last")
	}
	if n := strings.Count(body, `id="`+fact.Memory.ID+`"`); n != 1 {
		t.Errorf("a memory under two entities anchors %d times, want 1", n)
	}
	if n := strings.Count(body, `data-testid="memory.row.`+fact.Memory.ID+`"`); n != 2 {
		t.Errorf("a memory with two entities shows %d times grouped, want 2", n)
	}

	_, body = getResp(t, sb.base+"/p/acme/memory.json")
	var got struct{ Entities []memory.Entity }
	if err := json.Unmarshal([]byte(body), &got); err != nil || len(got.Entities) == 0 || got.Entities[0].Name != "#playtest" {
		t.Fatalf("memory.json entities %s (%v)", body, err)
	}
}
