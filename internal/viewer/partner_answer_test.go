package viewer

import (
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// partnerIssue loads the partner fixture's station issue.
// partnerDecisions are the issue's owner answers to partner gates.
func partnerDecisions(issue *tracker.Issue) []tracker.Decision {
	var out []tracker.Decision
	for _, d := range tracker.ParseDecisions(issue.Slug, issue.BodyRaw) {
		if len(d.Refs) > 0 && strings.HasPrefix(d.Refs[0], "partner-") {
			out = append(out, d)
		}
	}
	return out
}

func partnerIssue(t *testing.T, p tracker.Project) *tracker.Issue {
	t.Helper()
	issues, err := tracker.LoadIssues(p.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	return tracker.FindIssue(issues, "ui/station")
}

// AC1: Approve on the partner's row records a D-n with the owner's words,
// logged as the owner's browser, whose ref names the stage and the ask it
// answers, and nudges the issue's worker to write the partner's approval
// quoting it. The row then leaves Needs you (it waits on the worker), and
// a second answer to the same ask is refused.
func TestPartnerApproveRecordsDecisionAndNudgesWorker(t *testing.T) {
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, []AgentSession{{Name: "agent-ui-station"}})
	var gotTarget string
	var gotLines []string
	withMockTmuxSendKeys(t, func(target string, lines []string) error {
		gotTarget, gotLines = target, lines
		return nil
	})
	p, _ := setupPartnerProject(t, "waiting", omPartner(t))
	ts := newTestServer(t, []tracker.Project{p})
	defer ts.Close()

	code, out := postJSON(t, ts.URL+"/p/agents/issue/ui/station/partner", `{"action":"approve","stage":"pitch","words":"go with concept B","from":"needs-you"}`)
	if code != 200 || out["ok"] != true || out["notified_session"] != "agent-ui-station" {
		t.Fatalf("approve: %d %v", code, out)
	}
	ref, _ := out["decision"].(string)

	issue := partnerIssue(t, p)
	ds := tracker.ParseDecisions(issue.Slug, issue.BodyRaw)
	if len(ds) == 0 {
		t.Fatal("no decision recorded")
	}
	d := ds[len(ds)-1]
	if d.Ref != ref || d.How != "owner on Needs you" || d.Quote != "go with concept B" || d.Text != "OpenMontage pitch approved" {
		t.Fatalf("decision %+v (reply ref %q)", d, ref)
	}
	if len(d.Refs) != 1 || d.Refs[0] != "partner-approved:pitch@20261101T102000Z" {
		t.Fatalf("refs %v, want the stage and the ask's checkpoint time", d.Refs)
	}
	evs, err := tracker.ReadDecisionLog(p.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	logged := false
	for _, e := range evs {
		logged = logged || (e.Ref == ref && e.Event == tracker.DecisionRecorded && e.Role == tracker.ActorOwnerBrowser)
	}
	if !logged {
		t.Fatalf("no recorded event by the owner's browser for %s in %+v", ref, evs)
	}

	if gotTarget != "agent-ui-station" || len(gotLines) != 1 {
		t.Fatalf("nudge to %q: %q", gotTarget, gotLines)
	}
	for _, want := range []string{"OpenMontage: the owner approved pitch (" + ref + ")", `"go with concept B"`, "checkpoint_pitch.json with human_approved=True", "decision_log entry for stage pitch quoting " + ref} {
		if !strings.Contains(gotLines[0], want) {
			t.Errorf("nudge %q lacks %q", gotLines[0], want)
		}
	}

	// Answered: the partner no longer waits on the owner (the fixture's
	// own engine gate still does, without the partner's words).
	issues, _ := tracker.LoadIssues(p.IssueDir)
	for _, r := range rowsFor(needsYouRows(&p, "", "/p/agents", openOnly(&p, issues), time.Now()), "ui/station") {
		if r.Kind == "partner" || r.Partner != "" || r.PartnerURL != "" {
			t.Fatalf("the partner still waits on the owner after the answer: %+v", r)
		}
	}
	html := stationHTML(t, p)
	assertContains(t, html, `data-testid="station.partner.answered">You approved pitch (`+ref+`): the worker writes it at OpenMontage`, `OpenMontage: pitch · approved by you`)
	if strings.Contains(html, `data-testid="station.partner.form"`) {
		t.Error("the Station still offers Approve after the answer")
	}

	code, out = postJSON(t, ts.URL+"/p/agents/issue/ui/station/partner", `{"action":"approve","stage":"pitch"}`)
	if code != 409 || !strings.Contains(out["error"].(string), "already answered ("+ref+")") {
		t.Fatalf("second answer: %d %v", code, out)
	}
}

func TestPartnerSendBackAndRefusals(t *testing.T) {
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	withMockTmuxSendKeys(t, func(string, []string) error { t.Error("nudged with no worker live"); return nil })
	p, _ := setupPartnerProject(t, "waiting", omPartner(t))
	ts := newTestServer(t, []tracker.Project{p})
	defer ts.Close()
	url := ts.URL + "/p/agents/issue/ui/station/partner"

	// The Station offers the form while the stage awaits the owner.
	assertContains(t, stationHTML(t, p), `data-partner-answer="/p/agents/issue/ui/station/partner" data-stage="pitch"`)

	for _, tc := range []struct {
		body, want string
		code       int
	}{
		{`{"action":"send-back","stage":"pitch"}`, "say what to revise", 400},
		{`{"action":"maybe","stage":"pitch"}`, "approve or send-back", 400},
		{`{"action":"approve","stage":"storyboard"}`, "storyboard is no longer awaiting you (pitch awaits you)", 409},
	} {
		code, out := postJSON(t, url, tc.body)
		if code != tc.code || !strings.Contains(out["error"].(string), tc.want) {
			t.Errorf("%s: %d %v, want %d %q", tc.body, code, out, tc.code, tc.want)
		}
	}
	if ds := partnerDecisions(partnerIssue(t, p)); len(ds) != 0 {
		t.Fatalf("a refused answer recorded %v", ds)
	}

	code, out := postJSON(t, url, `{"action":"send-back","stage":"pitch","words":"too dark"}`)
	if code != 200 || !strings.Contains(out["notification_error"].(string), "no worker is live") {
		t.Fatalf("send back: %d %v", code, out)
	}
	issue := partnerIssue(t, p)
	ds := partnerDecisions(issue)
	if len(ds) != 1 || ds[0].Refs[0] != "partner-sent-back:pitch@20261101T102000Z" || ds[0].Quote != "too dark" {
		t.Fatalf("send-back decision %+v", ds)
	}
	if issue.Status != "in progress" {
		t.Errorf("a partner send-back moved the issue to %q", issue.Status)
	}
	if line := partnerNudge(omPartner(t), "ui/station", "pitch", ds[0].Ref, false, "too dark"); !strings.Contains(line, "sent pitch back") || !strings.Contains(line, "never approve it yourself") {
		t.Errorf("send-back nudge %q", line)
	}
}

// Approving an engine gate whose row carries the partner answers the
// partner too: one click, one decision on each.
func TestPartnerApprovedAlongWithTheGate(t *testing.T) {
	withMockTmuxSessions(t, nil)
	p, _ := setupPartnerProject(t, "waiting", omPartner(t))
	issue := partnerIssue(t, p)
	line := partnerAlongGate(&p, issue, "ship it", "Needs you", time.Now())
	if !strings.Contains(line, "the owner approved pitch") {
		t.Fatalf("nudge line %q", line)
	}
	issue = partnerIssue(t, p)
	ds := partnerDecisions(issue)
	if len(ds) != 1 || ds[0].Refs[0] != "partner-approved:pitch@20261101T102000Z" || ds[0].Quote != "ship it" {
		t.Fatalf("decision %+v", ds)
	}
	if again := partnerAlongGate(&p, issue, "", "Needs you", time.Now()); again != "" {
		t.Fatalf("an answered stage answered again: %q", again)
	}
}

// AC2 on the Station: approvals at the partner with no owner decision show
// as red lines.
func TestPartnerUnpairedApprovalOnStation(t *testing.T) {
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	p, _ := setupPartnerProject(t, "all-done", omPartner(t))
	assertContains(t, stationHTML(t, p),
		`data-testid="station.partner.unpaired">OpenMontage: pitch approved at the partner with no owner decision (partner-approved:pitch)`,
		`data-testid="station.partner.unpaired">OpenMontage: storyboard approved at the partner with no owner decision (partner-approved:storyboard)`)
}
