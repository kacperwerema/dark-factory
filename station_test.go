package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		`data-testid="station.body"`, `href="/p/agents/issue/ui/station?classic=1#implementation"`, `data-nvim="/p/agents/issue/ui/station/edit-in-nvim"`,
		`data-testid="station.fields"`, `20 Oct 2026`,
		`<script src="/static/station.js"></script>`,
	)
	if strings.Contains(html, `class="detail-`) {
		t.Error("the station must not render the classic page")
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

// TestStationOwnerSteps: guided checks due lock the stamp until they are
// done (stationMove).
func TestStationOwnerSteps(t *testing.T) {
	issue := &tracker.Issue{Slug: "ui/x", Status: "human-testing"}
	att := Attention{ChecksDue: true, Checks: []string{"x-checks"}, Gate: &GateInfo{Target: "documentation"}}
	m := stationMove("agents", issue, "/p/agents/issue/ui/x", needsYou{Amber: true, Wait: "2d"}, att, []string{"documentation"}, nil, time.Now())
	if !m.On || len(m.Tasks) != 1 || m.Tasks[0].Kind != "checks" || m.Tasks[0].RunURL != "/p/agents/issue/ui/x/check-script" {
		t.Fatalf("tasks = %+v", m.Tasks)
	}
	if m.Stamp == nil || !m.Stamp.Locked || m.Stamp.N != 2 || m.Stamp.Targets[0] != "documentation" {
		t.Fatalf("stamp = %+v", m.Stamp)
	}
	if m.Title != "Your move: guided checks and the stamp" {
		t.Errorf("title = %q", m.Title)
	}
	// Nothing due and no gated exit: a quiet panel, no stamp.
	q := stationMove("agents", issue, "/u", needsYou{Label: "With the agent", Title: "Nothing needs you."}, Attention{}, nil, nil, time.Now())
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
		{"wb/fields", WbFields{Title: "FIELDS", Rows: []WbFact{{Label: "Status", Value: "done", Mono: true}}, LinkText: "Edit", LinkURL: "/e"},
			[]string{`<dt>Status</dt><dd class="wb-mono-inline">done</dd>`, `href="/e">Edit</a>`}},
		{"wb/stamp", &WbStamp{N: 1, Targets: []string{"backlog", "in progress"}, Approved: "backlog", ApproveURL: "/a"},
			[]string{`type="radio" name="status" value="backlog" checked`, `id="approve-in-progress"`, `Take the stamp back`}},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		if err := srv.tmpl.ExecuteTemplate(&buf, c.name, c.data); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		assertContains(t, buf.String(), c.want...)
	}
}
