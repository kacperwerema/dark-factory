package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/outputs"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// setupStationProject is the agents project plus an issue with a body
// worth a station: decisions (yours and the agent's), checkboxes in two
// sections and a pending gate (in progress → testing).
func setupStationProject(t *testing.T) tracker.Project {
	t.Helper()
	p := setupAgentsProject(t)
	body := "---\ntitle: \"Station fixture\"\nslug: \"station\"\nstatus: \"in progress\"\nsystem: \"UI\"\npriority: \"high\"\ncreated: \"2026-10-20\"\n---\n\n" +
		"What the issue is about.\n\n" +
		"## Decisions\n\n" +
		"- **D-1** · 2026-10-20 · lead chat: Keep the UI simple, \"Keep it simple and consistent\"\n" +
		"- **D-2** · 2026-10-21 · recorded by agent: A separate limit of 12 (also considered: raise the shared limit)\n\n" +
		"## Implementation\n\n- [x] I1 wire it\n- [x] I2 test it\n\n" +
		"## Human Testing\n\n- [ ] HT1 play it\n"
	path := filepath.Join(p.IssueDir, "UI", "station.md")
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestStationPage(t *testing.T) {
	p := setupStationProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, []AgentSession{{Name: "lead-agents"}, {Name: "agent-ui-station", StartTime: "2026-10-21 09:30:00"}})
	ts := newTestServer(t, []tracker.Project{p})
	defer ts.Close()

	code, html := getBody(t, ts.URL+"/p/agents/issue/ui/station")
	if code != 200 {
		t.Fatalf("status %d: %s", code, html)
	}
	assertContains(t, html,
		// Map › project › Hall › Station.
		`href="/p/agents/company">Agents</a>`, `href="/p/agents/hall?agent=ui%2Fstation#agent">Hall</a>`, `aria-current="page">Station</span>`,
		`data-testid="station.title"`, `Station fixture`, `>priority: high</span>`, `>system: UI</span>`,
		`href="/p/agents/issue/ui/station?classic=1" data-testid="station.classic"`,
		// The belt: the path with the gate into testing still the owner's.
		`data-testid="station.belt"`, `<span class="wb-ibelt-key">in progress</span>`, `gate: you`,
		`data-testid="station.time"`,
		// The stamp posts the approval with the owner's words.
		`data-testid="station.move"`, `data-stamp="/p/agents/issue/ui/station/approve"`, `value="testing"`, `I approve the move to testing`,
		`data-testid="approve_words.station"`, `data-testid="station.stamp.back"`,
		// Quality check and the decisions: yours with your words, the agent's with what it set aside.
		`data-testid="station.quality.decisions"`, `data-testid="station.quality.checklist"`, `>2/3</span>`,
		`data-testid="station.decisions"`, `<span class="wb-dec-who">YOURS</span>`, `“Keep it simple and consistent”`,
		`wb-dec-who--agent">AGENT'S`, `raise the shared limit`,
		`data-testid="station.change"`, `data-testid="station.tests"`, `HT1`, `wb-group wb-group--you`,
		`data-testid="station.timeline"`, `data-testid="station.timeline.filter.all"`,
		// The side column: the agent, its session, the body and the fields.
		`data-testid="station.agent"`, `data-open-session="agent-ui-station"`, `data-reprompt="/p/agents/issue/ui/station/dispatch/reprompt"`,
		`data-testid="station.sessions"`, `agent-ui-station`, `21 Oct 09:30`,
		`data-testid="station.body"`, `href="#body-implementation"`, `data-nvim="/p/agents/issue/ui/station/edit-in-nvim"`,
		`data-testid="station.fields"`, `20 Oct 2026`,
		`<script src="/static/station.js"></script>`,
	)
	if strings.Contains(html, `class="detail-`) {
		t.Error("the station must not render the classic page")
	}
}

// TestStationTakesOverTheClassicPage: what only the classic issue page did
// is on the station, posting to the same endpoints: rename and delete,
// the fields edited in place (status after the transition preview), the
// body folded by section with live boxes and an editor guarded by the
// body's hash, dispatch, the brief and relations; no link sends the owner
// to ?classic=1 for the fields or a body section.
func TestStationTakesOverTheClassicPage(t *testing.T) {
	p := setupStationProject(t)
	dep := "---\ntitle: \"Base work\"\nslug: \"base\"\nstatus: \"done\"\nsystem: \"UI\"\n---\n\nBase.\n"
	if err := os.WriteFile(filepath.Join(p.IssueDir, "UI", "base.md"), []byte(dep), 0644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(p.IssueDir, "UI", "station.md")
	raw, _ := os.ReadFile(path)
	raw = bytes.Replace(raw, []byte("created: \"2026-10-20\"\n"), []byte("created: \"2026-10-20\"\nlabels:\n  - ui\ndepends_on:\n  - ui/base\nrepo: \"o/r\"\nnumber: 7\nowner_note: \"keep\"\n"), 1)
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{p})
	defer ts.Close()

	code, html := getBody(t, ts.URL+"/p/agents/issue/ui/station")
	if code != 200 {
		t.Fatalf("status %d: %s", code, html)
	}
	assertContains(t, html,
		// G59 rename and delete.
		`data-testid="station.rename"`, `data-st-rename-form="/p/agents/issue/ui/station"`, `data-st-delete="/p/agents/issue/ui/station/delete"`, `data-store="/p/agents/"`,
		// G60 the fields, edited in place; G61 the rows under them.
		`data-st-fields="/p/agents/issue/ui/station"`, `data-preview-url="/p/agents/issue/ui/station/transition"`, `data-approve-url="/p/agents/issue/ui/station/approve"`,
		`<option value="in progress" selected>in progress</option>`, `<option value="high" selected>high</option>`, `data-label="ui">ui`,
		`data-testid="station.field.assignee"`, `data-testid="station.field.version"`, `data-testid="station.move_preview"`,
		`href="https://github.com/o/r/issues/7" target="_blank"`, `<dt>Owner Note</dt>`, `<dt>CEO</dt>`,
		// G62 dispatch, custom actions and the prompt; G63 Send the prompt always on.
		`data-st-dispatch="/p/agents/issue/ui/station/dispatch"`, `data-testid="station.dispatch.claude"`, `href="/p/agents/issue/ui/station/dispatch-prompt"`,
		// G66 brief; G68 approving; G70 relations.
		`data-testid="station.brief"`, `What the issue is about.`, `data-testid="station.approving"`,
		`data-testid="station.relations.depends_on"`, `href="/p/agents/issue/ui/base">ui/base</a>`,
		// G73–G75 the body: sections, live boxes, the editor with the hash.
		`data-testid="station.fullbody"`, `data-checkbox-url="/p/agents/issue/ui/station/checkbox"`, `data-body-hash="`+tracker.BodyHash(issueBody(t, path))+`"`,
		`id="body-implementation"`, `data-testid="station.fullbody.section.human_testing"`, `type="checkbox" data-line=`, `data-testid="station.fullbody.textarea"`,
	)
	for _, bad := range []string{`?classic=1#`, `Edit the fields`, `from the classic view`} {
		if strings.Contains(html, bad) {
			t.Errorf("the station still sends the owner to the classic page: %q", bad)
		}
	}
	if strings.Contains(html, `data-testid="station.agent.prompt" disabled`) {
		t.Error("Send the prompt should stay usable when the session is not running")
	}
}

func issueBody(t *testing.T, path string) string {
	t.Helper()
	raw, _ := os.ReadFile(path)
	is, err := tracker.ParseIssue(path, raw)
	if err != nil {
		t.Fatal(err)
	}
	return is.BodyRaw
}

// TestStationEpicChildren: an epic's station lists its children by version.
func TestStationEpicChildren(t *testing.T) {
	wf := &tracker.WorkflowConfig{}
	epic := &tracker.Issue{Slug: "e/epic", Title: "Epic"}
	issues := []*tracker.Issue{epic,
		(&tracker.Issue{Slug: "e/a", Title: "A", Status: "done", Version: "0.1"}).WithField("parent", "e/epic"),
		(&tracker.Issue{Slug: "e/b", Title: "B", Status: "idea", Version: "0.1"}).WithField("parent", "e/epic"),
	}
	got := stationEpic(buildEpicTab(wf, epic, issues), epic.Slug, "/p/x", wf)
	if got == nil || got.Total != 2 || len(got.Versions) != 1 || len(got.Versions[0].Children) != 2 {
		t.Fatalf("epic = %+v", got)
	}
	if stationEpic(nil, "x", "/p/x", wf) != nil {
		t.Error("an issue that is not an epic has no children panel")
	}
}

func TestStationClassicStillServed(t *testing.T) {
	p := setupStationProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{p})
	defer ts.Close()

	for _, q := range []string{"?classic=1", "?tab=timeline", "?tab=evidence"} {
		code, html := getBody(t, ts.URL+"/p/agents/issue/ui/station"+q)
		if code != 200 {
			t.Fatalf("%s: status %d", q, code)
		}
		if strings.Contains(html, `data-testid="station.title"`) || !strings.Contains(html, `/static/approve-words.js`) {
			t.Errorf("%s: want the classic page", q)
		}
	}
	if code, _ := getBody(t, ts.URL+"/p/agents/issue/ui/nope"); code != 404 {
		t.Errorf("unknown issue: status %d, want 404", code)
	}
}

// TestStationOwnerSteps: guided checks due come first, and the stamp after
// them stays usable with a note that they are open (stationMove).
func TestStationOwnerSteps(t *testing.T) {
	issue := &tracker.Issue{Slug: "ui/x", Status: "human-testing"}
	att := Attention{ChecksDue: true, Checks: []string{"x-checks"}, Gate: &GateInfo{Target: "documentation"}}
	m := stationMove("agents", nil, issue, "/p/agents/issue/ui/x", needsYou{Amber: true, Wait: "2d"}, att, []string{"documentation"}, nil, time.Now())
	if !m.On || len(m.Tasks) != 1 || m.Tasks[0].Kind != "checks" || m.Tasks[0].RunURL != "/p/agents/issue/ui/x/check-script" {
		t.Fatalf("tasks = %+v", m.Tasks)
	}
	if m.Stamp == nil || m.Stamp.N != 2 || m.Stamp.Targets[0] != "documentation" || m.Stamp.Note != "Step 1 is still open: you can stamp ahead." {
		t.Fatalf("stamp = %+v", m.Stamp)
	}
	// Playtest and checks due: both steps named in the note.
	two := stationMove("agents", nil, issue, "/u", needsYou{Amber: true, Wait: "2d"}, Attention{ChecksDue: true, Checks: []string{"x"}, PlaytestDue: true, Playtests: []string{"y"}, Gate: &GateInfo{Target: "documentation"}}, []string{"documentation"}, nil, time.Now())
	if two.Stamp == nil || two.Stamp.N != 3 || two.Stamp.Note != "Steps 1–2 are still open: you can stamp ahead." {
		t.Fatalf("stamp with two steps = %+v", two.Stamp)
	}
	if m.Title != "Your move: guided checks and the stamp" {
		t.Errorf("title = %q", m.Title)
	}
	// Nothing due and no gated exit: a quiet panel, no stamp.
	q := stationMove("agents", nil, issue, "/u", needsYou{Label: "With the agent", Title: "Nothing needs you."}, Attention{}, nil, nil, time.Now())
	if q.On || q.Stamp != nil || q.Title != "With the agent: Nothing needs you" {
		t.Errorf("quiet = %+v", q)
	}
}

// TestStationComponents renders each component on its own.
func TestStationComponents(t *testing.T) {
	srv, err := NewServer([]tracker.Project{{Name: "P", Slug: "p", IssueDir: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		data any
		want []string
	}{
		{"wb/belt", WbBelt{Label: "BELT", Steps: []WbBeltStep{{Status: "idea", State: "done", Note: "gate ✓"}, {Status: "testing", State: "now", You: true, Note: "your checks", NoteYou: true}}},
			[]string{`wb-ibelt-mark--done">✓`, `wb-ibelt-mark--you wb-ping">!`, `wb-ibelt-note--you">your checks`}},
		{"wb/time-split", WbTimeSplit{Total: "2d 0h", Label: "so far", Known: true, Parts: []WbTimePart{{Label: "agent", Value: "8m", Tone: "agent", Pct: 1}, {Label: "waiting on you", Value: "2d", Tone: "you", Pct: 99}}},
			[]string{`2d 0h <span>so far</span>`, `wb-tp--you" style="flex-grow:99.00"`}},
		{"wb/quality-card", WbQualityCard{Key: "tests", Title: "Tests", Big: "3/3", Tone: "green", Verdict: "checked by the machine", Href: "#tests"},
			[]string{`data-testid="station.quality.tests"`, `wb-lamp--green`, `>3/3<`}},
		{"wb/change-card", WbChangeCard{Head: "abc1234 · fix", Range: "work/x", Files: 1, Commits: 1, Add: 6, Del: 1, PerFile: []WbFileStat{{Path: "a/b.go", Add: 6, Del: 1}}, Areas: []WbFileStat{{Path: "a", Files: 1, Add: 6, Del: 1}}},
			[]string{`abc1234 · fix · work/x`, `title="a/b.go"`, `+6`, `−1`}},
		{"wb/change-card", WbChangeCard{Unreadable: "No work branch."}, []string{`No work branch.`}},
		{"wb/timeline-list", WbTimelineList{FullURL: "/full", Rows: []WbTimelineRow{{At: "03:21", Text: "→ testing", Kind: "status", Tone: "status"}}},
			[]string{`wb-tl-dot--status`, `→ testing`, `href="/full"`}},
		{"wb/sessions", WbSessionList{}, []string{`No tmux session names this issue.`}},
		{"wb/fields", WbFields{Title: "FIELDS", Rows: []WbFieldRow{{Label: "Epic", Value: "e/x", URL: "/p/p/issue/e/x", Mono: true}, {Label: "GitHub", Value: "o/r#4", URL: "https://github.com/o/r/issues/4", External: true}}},
			[]string{`<dt>Epic</dt><dd class="wb-mono-inline"><a href="/p/p/issue/e/x">e/x</a>`, `href="https://github.com/o/r/issues/4" target="_blank"`}},
		{"wb/fields", WbFields{Title: "FIELDS", Edit: &WbFieldEdit{URL: "/i", Status: "testing", Statuses: []string{"in progress", "testing"}, Priorities: stationPriorities, Priority: "high", Labels: []string{"ui"}, Types: []WorkTypeOption{{Name: "feature"}, {Name: "bugfix"}}, Type: "bugfix"}},
			[]string{`data-st-fields="/i"`, `<option value="testing" selected>testing</option>`, `<option value="high" selected>high</option>`, `<option value="bugfix" title=" — " selected>bugfix</option>`, `data-label="ui">ui<button`, `data-testid="station.move_preview"`}},
		{"wb/brief", &WbBrief{Text: "Moves **gaps**.", Words: &briefWords{Quote: "do it", Who: "You", Ref: "D-1"}, Epic: &CardProgress{Title: "Epic", Href: "/e", Done: 1, Total: 4}},
			[]string{`data-testid="station.brief"`, `<strong>gaps</strong>`, `“do it”`, `width:25%`, `1 of 4 done`}},
		{"wb/brief", (*WbBrief)(nil), nil},
		{"wb/approving", WbApproving{From: "dispatch", Empty: "No recorded events since the issue was made."}, []string{`since dispatch`, `No recorded events`}},
		{"wb/relations", &WbRelations{Groups: []WbRelGroup{{Kind: "depends on", Rows: []WbRelRow{{Target: "a/b", URL: "/p/p/issue/a/b", State: "done", Tone: "green"}}}}},
			[]string{`data-testid="station.relations.depends_on"`, `href="/p/p/issue/a/b">a/b</a>`, `wb-chip--green">done`}},
		{"wb/epic-children", &WbEpicChildren{Done: 1, Total: 2, Versions: []WbEpicVersion{{Version: "0.1", Done: 1, Total: 2, Pct: 50, Children: []WbEpicChild{{Title: "Kid", URL: "/k", Status: "done", Closed: true}}}}},
			[]string{`Children · 1/2 closed`, `data-testid="station.epic.version.0_1"`, `href="/k">Kid</a>`, `wb-chip--green">done`}},
		{"wb/mockups", &WbMockups{Shots: []mockupThumb{{Title: "Board", Src: "/a.png"}}, URL: "https://claude.ai/x"}, []string{`src="/a.png" alt="Board"`, `Open the canvas`}},
		{"wb/full-body", WbBody{URL: "/i", Hash: "h1", Parts: []WbBodyPart{{ID: "acceptance_criteria", Title: "Acceptance criteria", Count: "0/1", Tone: "idle", HTML: "<ul><li>x</li></ul>"}}},
			[]string{`data-body-hash="h1"`, `id="body-acceptance_criteria"`, `wb-chip--idle">0/1`, `<ul><li>x</li></ul>`}},
		{"wb/full-body", WbBody{Empty: true}, []string{`The body is empty.`}},
		{"wb/dispatch", WbDispatch{URL: "/i/dispatch", ActionURL: "/i/action/", PromptURL: "/i/dispatch-prompt", Actions: []tracker.CustomAction{{ID: "review", Label: "Review"}}},
			[]string{`data-st-dispatch="/i/dispatch"`, `data-st-action="/i/action/review"`, `data-testid="station.dispatch.action.review"`, `href="/i/dispatch-prompt"`}},
		{"wb/station-missing", []string{"CLI log: not found"}, []string{`Could not read 1 source`, `CLI log: not found`}},
		{"wb/station-missing", []string(nil), nil},
		{"wb/stamp", &WbStamp{N: 1, Targets: []string{"backlog", "in progress"}, Approved: "backlog", ApproveURL: "/a"},
			[]string{`type="radio" name="status" value="backlog" checked`, `id="approve-in-progress"`, `Take the stamp back`}},
		{"wb/stamp", &WbStamp{N: 3, Targets: []string{"documentation"}, ApproveURL: "/a", Note: "Steps 1–2 are still open: you can stamp ahead."},
			[]string{`value="documentation">I approve`, `data-testid="station.stamp.go">Stamp it</button>`, `Steps 1–2 are still open: you can stamp ahead.`}},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		if err := srv.tmpl.ExecuteTemplate(&buf, c.name, c.data); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		assertContains(t, buf.String(), c.want...)
	}
}

// A report issue's report is on the shelf with the issue, and its page
// lists it with a link to the file (the report type, docs/Workflow/types.md).
func TestStationShowsTheIssuesReport(t *testing.T) {
	p := setupStationProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	src := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(src, []byte("# Findings\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o, err := outputs.Add(&p, outputs.AddInput{Department: "product", Title: "Benchmark pack findings", Kind: "report", File: src, Issue: "ui/station"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, []tracker.Project{p})
	defer ts.Close()
	code, html := getBody(t, ts.URL+"/p/agents/issue/ui/station")
	if code != 200 {
		t.Fatalf("status %d: %s", code, html)
	}
	assertContains(t, html, `data-testid="station.outputs"`, `Outputs · 1`, `1 to review`, `Benchmark pack findings`, `href="/p/agents/shelf/read/`+o.ID+`"`)
	code, file := getBody(t, ts.URL+"/p/agents/shelf/file/"+o.File)
	if code != 200 || !strings.Contains(file, "Findings") {
		t.Fatalf("the report file: %d %s", code, file)
	}
}
