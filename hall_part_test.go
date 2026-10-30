package main

import (
	"os"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/ci"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// hallPartProject is the agents fixture with a workflow that has something
// in each part: a prompt, a gated edge with checks, an any-status edge, a
// type and a system overlay, parking and CI.
func hallPartProject(t *testing.T) tracker.Project {
	t.Helper()
	p := setupAgentsProject(t)
	wf := `worktree: true
ci:
  branch: main
  run:
    - make validate
    - cmd: make e2e
      tier: slow
statuses:
  - name: "backlog"
  - name: "in progress"
    description: "An agent builds it"
    prompt: "Write the code on the issue branch.\nCommit as you go."
  - name: "testing"
  - name: "done"
  - name: "parked"
    global: true
transitions:
  - from: "in progress"
    to: "testing"
    actions:
      - type: validate
        rule: "section_checkboxes_checked Implementation"
      - type: require_human_approval
      - type: append_section
        title: "Testing"
  - from: "*"
    to: "parked"
    actions:
      - type: set_fields
        field: assignee
        value: ""
types:
  tweak:
    path: ["backlog", "testing", "done"]
  feature:
    statuses:
      - name: "in progress"
        prompt: "Features also update the docs."
systems:
  UI:
    transitions:
      - from: "in progress"
        to: "testing"
        replace: true
        actions:
          - type: require_human_approval
`
	if err := os.WriteFile(p.WorkflowFile, []byte(wf), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHallPartStation(t *testing.T) {
	a := hallPartProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, []AgentSession{{Name: "lead-agents"}, {Name: "agent-ui-child"}})
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()

	// The hall's parts are links.
	_, hall := getBody(t, ts.URL+"/p/agents/hall")
	assertContains(t, hall,
		`href="/p/agents/hall/station/in-progress" title="Open the in progress station" data-testid="hall.station.open"`,
		`href="/p/agents/hall/gate/testing"`,
		`href="/p/agents/hall/station/done"`, `href="/p/agents/hall/station/parked"`,
	)

	code, html := getBody(t, ts.URL+"/p/agents/hall/station/in-progress")
	if code != 200 {
		t.Fatalf("status %d: %s", code, html)
	}
	assertContains(t, html,
		// Crumbs: Map › project › Product hall › the station.
		`href="/p/agents/hall"`, `aria-current="page">In progress</span>`, `data-testid="hall.part.back"`,
		// Every issue there, links to its page, the worker on its crate.
		`data-testid="hall.part.issues"`, `href="/p/agents/issue/ui/child"`, `href="/p/agents/issue/ui/asker"`, `href="/p/agents/issue/ui/idle"`, `href="/p/agents/issue/ui/epic"`,
		// The prompt.
		`An agent builds it`, "Write the code on the issue branch.\nCommit as you go.",
		// In: implied from backlog. Out: the gated edge in words, any → parked.
		`data-testid="hall.part.in"`, `the next step of the lifecycle`,
		`checks: section_checkboxes_checked Implementation`, `needs your approval`, `adds the section ## Testing`,
		`any status</span> → <span class="wb-mono-inline">parked`, `clears assignee`,
		`href="/p/agents/hall/station/testing"`,
		// Overlays: the feature prompt, the tweak path that skips it, UI's replacing edge.
		`data-testid="hall.part.overlays"`, `Features also update the docs.`, `its path skips this status`, `replaces the base edge`,
	)
	if n := strings.Count(html, `data-testid="hall.crate"`); n != 4 {
		t.Errorf("crates = %d, want all 4 in-progress issues", n)
	}

	// Done lists done issues, though the hall's line holds only open ones.
	_, html = getBody(t, ts.URL+"/p/agents/hall/station/done")
	assertContains(t, html, `aria-current="page">Done</span>`, `No issue is in <span class="wb-mono-inline">done</span>`)

	for _, path := range []string{"/hall/station/nope", "/hall/gate/backlog", "/hall/gate/nope", "/hall/bogus/testing"} {
		if code, _ := getBody(t, ts.URL+"/p/agents"+path); code != 404 {
			t.Errorf("%s = %d, want 404", path, code)
		}
	}
}

func TestHallPartGate(t *testing.T) {
	a := hallPartProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	// ui/idle waits at the gate into testing: every box is ticked.
	idle := a.IssueDir + "/UI/idle.md"
	if err := os.WriteFile(idle, []byte("---\ntitle: \"Idle\"\nstatus: \"in progress\"\n---\n\n## Implementation\n- [x] done\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()

	code, html := getBody(t, ts.URL+"/p/agents/hall/gate/testing")
	if code != 200 {
		t.Fatalf("status %d: %s", code, html)
	}
	assertContains(t, html,
		`aria-current="page">Gate before testing</span>`,
		`data-testid="hall.part.waiting"`, `data-testid="hall.part.in"`,
		`checks: section_checkboxes_checked Implementation`, `wb-part-edge wb-part-edge--gate`,
		// UI's overlay gates it too.
		`data-testid="hall.part.overlays"`, `replaces the base edge`,
	)
	if strings.Contains(html, `data-testid="hall.part.raised"`) {
		t.Error("barrier up while YOLO is off")
	}
	waiting := waitingOnOwner(&a, mustLoad(t, a))
	want := 0
	for _, it := range waiting {
		if it.Kind == "gate" && statusKey(it.Target) == "testing" {
			want++
			if !strings.Contains(html, `href="/p/agents/issue/`+it.Issue.Slug+`"`) {
				t.Errorf("%s waits at the gate but is not listed", it.Issue.Slug)
			}
		}
	}
	if want == 0 {
		t.Fatal("fixture: nothing waits at the gate")
	}
}

func mustLoad(t *testing.T, p tracker.Project) []*tracker.Issue {
	t.Helper()
	issues, err := tracker.LoadIssues(p.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	return openOnly(issues)
}

func TestHallPartCI(t *testing.T) {
	a := hallPartProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	// Before the first run: the commands, no state.
	ts0 := newTestServer(t, []tracker.Project{a})
	if code, html := getBody(t, ts0.URL+"/p/agents/hall/ci"); code != 200 || !strings.Contains(html, "no run yet") {
		t.Errorf("/hall/ci before a run = %d, want the page with no run yet", code)
	}
	ts0.Close()
	st := &ci.State{Branch: "main", State: ci.Red, GreenSha: "aaaaaaa1111", RedSince: "bbbbbbb2222", Failed: "make validate", Failures: []string{"TestHallPage"},
		Merged: []ci.Merged{{Sha: "bbbbbbb2222", Slug: "ui/child", Subject: "feat: child"}}, Blamed: []string{"ui/child"}, LogTail: []string{"--- FAIL: TestHallPage"}}
	if err := ci.WriteState(ciRootFor(&a), st); err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()

	_, hall := getBody(t, ts.URL+"/p/agents/hall")
	assertContains(t, hall, `href="/p/agents/hall/ci" title="Open the CI scanner" data-testid="hall.ci"`)
	code, html := getBody(t, ts.URL+"/p/agents/hall/ci")
	if code != 200 {
		t.Fatalf("status %d: %s", code, html)
	}
	assertContains(t, html,
		`aria-current="page">CI scanner</span>`, `wb-part-panel--red`, `bbbbbbb`, `TestHallPage`,
		`<dt>default</dt><dd class="wb-mono-inline">make validate</dd>`, `<dt>slow</dt><dd class="wb-mono-inline">make e2e</dd>`,
		`ui/child feat: child`, `href="/p/agents/issue/ui/child"`, `--- FAIL: TestHallPage`, `href="/p/agents/status"`,
	)

	// Without a ci: block there is no scanner to open.
	b := setupAgentsProject(t)
	ts2 := newTestServer(t, []tracker.Project{b})
	defer ts2.Close()
	if code, _ := getBody(t, ts2.URL+"/p/agents/hall/ci"); code != 404 {
		t.Errorf("/hall/ci without ci: = %d, want 404", code)
	}
}

func TestActionWords(t *testing.T) {
	for _, c := range []struct {
		a    tracker.WorkflowAction
		want string
	}{
		{tracker.WorkflowAction{Type: "require_human_approval", Status: "backlog"}, "needs your approval for backlog"},
		{tracker.WorkflowAction{Type: "set_fields", Field: "assignee", Value: "x"}, "sets assignee to x"},
		{tracker.WorkflowAction{Type: "inject_prompt", Prompt: "Do\n  this"}, "gives the agent: Do this"},
		{tracker.WorkflowAction{Type: "validate", Rule: "has_section", Field: "Design"}, "checks: has_section Design"},
		{tracker.WorkflowAction{Type: "something_new"}, "something_new"},
	} {
		if got := actionWords(c.a); got != c.want {
			t.Errorf("actionWords(%+v) = %q, want %q", c.a, got, c.want)
		}
	}
}
