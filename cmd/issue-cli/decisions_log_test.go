package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func heldEvents(t *testing.T, proj *tracker.Project) []tracker.DecisionEvent {
	t.Helper()
	evs, err := tracker.ReadDecisionLog(proj.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	var out []tracker.DecisionEvent
	for _, ev := range evs {
		if ev.Event == tracker.DecisionHeld {
			out = append(out, ev)
		}
	}
	return out
}

// AC6: a real transition a ledger rule refuses logs one held event per
// rule and ref (passed false); under YOLO the pass logs them with passed
// true; a dry run logs nothing.
func TestTransitionLogsDecisionHolds(t *testing.T) {
	now := time.Now()
	proj := makeLedgerProject(t, true, false, "We build it; stores per rules#D-2.")

	if _, _, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog", "--dry-run"); err == nil {
		t.Fatal("dry run passed")
	}
	if got := heldEvents(t, proj); len(got) != 0 {
		t.Fatalf("dry run logged %+v", got)
	}

	_, errOut, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog")
	if err == nil {
		t.Fatal("transition passed an uncited hard decision")
	}
	got := heldEvents(t, proj)
	if len(got) != 1 {
		// decisions_cited fails first; citations_current runs only when it
		// passes, so one hold is logged.
		t.Fatalf("held events = %+v", got)
	}
	ev := got[0]
	if ev.Ref != "rules#D-1" || ev.Rule != tracker.DecisionsCitedRule || ev.Issue != "design" || ev.From != "in design" || ev.To != "backlog" || ev.Passed {
		t.Fatalf("held = %+v", ev)
	}
	assertContains(t, errOut, "decisions log --ref rules#D-1")

	yoloOn(t, proj, now, time.Hour)
	if _, _, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog"); err != nil {
		t.Fatalf("YOLO: %v", err)
	}
	got = heldEvents(t, proj)[1:]
	var refs []string
	for _, ev := range got {
		if !ev.Passed {
			t.Errorf("YOLO hold not marked passed: %+v", ev)
		}
		refs = append(refs, ev.Rule+" "+ev.Ref)
	}
	if strings.Join(refs, ",") != "decisions_cited rules#D-1,citations_current rules#D-2" {
		t.Fatalf("YOLO held = %v", refs)
	}
}

func runDecisionsCmd(t *testing.T, proj *tracker.Project, jsonOut bool, args ...string) (string, error) {
	t.Helper()
	ctx, out, _ := newTestContext(proj, jsonOut)
	err := runDecisions(ctx, args)
	return out.String(), err
}

// AC1: decisions log --ref lists that decision's events in time order
// (injected events that carried it too), only done ones; --since filters by
// day; reading writes nothing.
func TestDecisionsLogByRefAndSince(t *testing.T) {
	proj := makeLedgerProject(t, false, false, "x")
	at := func(s string) string {
		tm, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
		if err != nil {
			t.Fatal(err)
		}
		return tm.Format(time.RFC3339)
	}
	evs := []tracker.DecisionEvent{
		{Write: "w1", Op: tracker.DecisionOpPending, Event: tracker.DecisionRecorded, Ref: "rules#D-1", At: at("2026-10-01 10:00")},
		{Write: "w1", Op: tracker.DecisionOpDone, Event: tracker.DecisionRecorded, Ref: "rules#D-1", At: at("2026-10-01 10:00"), By: "owner", Role: tracker.ActorOwnerShell},
		{Write: "w2", Op: tracker.DecisionOpDone, Event: tracker.DecisionRecorded, Ref: "rules#D-2", At: at("2026-10-02 10:00"), By: "owner"},
		// A slow writer: logged after, happened before the amend.
		{Write: "w4", Op: tracker.DecisionOpDone, Event: tracker.DecisionInjected, At: at("2026-10-20 09:00"), By: "agent-design", Role: "worker", Briefing: tracker.BriefingStart, Issue: "design", Refs: []string{"rules#D-1", "rules#D-3"}},
		{Write: "w3", Op: tracker.DecisionOpDone, Event: tracker.DecisionAmended, Ref: "rules#D-1", At: at("2026-10-10 12:00"), Old: "{scope: project}", New: "{scope: project; hard}", By: "lead-x", Role: tracker.ActorLeadRelay, Comment: 4},
		{Write: "w5", Op: tracker.DecisionOpPending, Event: tracker.DecisionAmended, Ref: "rules#D-1", At: at("2026-10-21 12:00")},
		{Write: "w5", Op: tracker.DecisionOpFailed, Event: tracker.DecisionAmended, Ref: "rules#D-1", At: at("2026-10-21 12:00"), Error: "disk"},
		{Write: "w6", Op: tracker.DecisionOpDone, Event: tracker.DecisionHeld, Ref: "rules#D-1", At: at("2026-10-22 08:00"), Rule: "decisions_cited", Issue: "design", From: "in design", To: "backlog", Passed: true, By: "agent-design", Role: "worker"},
	}
	if err := tracker.AppendDecisionEvents(proj.IssueDir, evs); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(tracker.DecisionLogPath(proj.IssueDir))

	out, err := runDecisionsCmd(t, proj, false, "log", "--ref", "rules#D-1")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"== Decisions log (4 events) ==",
		"2026-10-01 10:00 · recorded · rules#D-1 · owner (owner-shell)",
		"2026-10-10 12:00 · amended · rules#D-1 · {scope: project} → {scope: project; hard} · comment #4 · lead-x (lead-relay)",
		"2026-10-20 09:00 · injected · start briefing for design · 2 refs · agent-design (worker)",
		"2026-10-22 08:00 · held · rules#D-1 · decisions_cited on design (in design → backlog) · passed (YOLO) · agent-design (worker)",
	}
	if got := strings.TrimSpace(out); got != strings.Join(want, "\n") {
		t.Fatalf("log --ref:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}

	out, err = runDecisionsCmd(t, proj, false, "log", "--ref", "rules#D-1", "--since", "2026-10-10")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "2026-10-01") || !strings.Contains(out, "2026-10-10 12:00 · amended") || !strings.Contains(out, "(3 events)") {
		t.Fatalf("--since:\n%s", out)
	}

	out, err = runDecisionsCmd(t, proj, true, "log", "--since", "2026-10-02")
	if err != nil {
		t.Fatal(err)
	}
	var got []tracker.DecisionEvent
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	var order []string
	for _, ev := range got {
		order = append(order, ev.Write)
	}
	if strings.Join(order, ",") != "w2,w3,w4,w6" {
		t.Fatalf("json order = %v", order)
	}

	if _, err := runDecisionsCmd(t, proj, false, "log", "--ref", "D-1"); err == nil || !strings.Contains(err.Error(), "<slug>#D-n") {
		t.Fatalf("bare D-1: %v", err)
	}
	if _, err := runDecisionsCmd(t, proj, false, "log", "--since", "yesterday"); err == nil {
		t.Fatal("--since yesterday accepted")
	}
	out, _ = runDecisionsCmd(t, proj, false, "log", "--ref", "rules#D-9")
	assertContains(t, out, "No events match these filters (5 in the log).")

	after, _ := os.ReadFile(tracker.DecisionLogPath(proj.IssueDir))
	if string(before) != string(after) {
		t.Fatal("decisions log wrote to the log")
	}
}

// An empty log says so and names decide.
func TestDecisionsLogEmpty(t *testing.T) {
	proj := makeLedgerProject(t, false, false, "x")
	out, err := runDecisionsCmd(t, proj, false, "log")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, out, "The decisions log is empty")
	assertContains(t, out, `decide <slug> --text "..."`)
	out, _ = runDecisionsCmd(t, proj, true, "log")
	if strings.TrimSpace(out) != "[]" {
		t.Fatalf("json = %q", out)
	}
}
