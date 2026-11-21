package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/ownerstep"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var ownerStepNow = time.Date(2026, 11, 16, 9, 0, 0, 0, time.Local)

func ownerStepAt(proj *tracker.Project, now time.Time, args ...string) (string, error) {
	ctx, stdout, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return now }
	err := runOwnerStep(ctx, args)
	return stdout.String(), err
}

// addStepAs adds a step and returns its id.
func addStepAs(t *testing.T, proj *tracker.Project, args ...string) string {
	t.Helper()
	ctx, stdout, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return ownerStepNow }
	ctx.JSONOutput = true
	if err := runOwnerStep(ctx, append([]string{"add"}, args...)); err != nil {
		t.Fatalf("add %v: %v", args, err)
	}
	var out struct{ ID string }
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil || out.ID == "" {
		t.Fatalf("add output %q: %v", stdout.String(), err)
	}
	return out.ID
}

// A worker's step goes on its own issue and keeps the body byte for byte;
// list shows it with its due line and body.
func TestOwnerStepAddAndList(t *testing.T) {
	proj, _ := ledgerFixture(t)
	asWorkerOn(t, "sim/rules")
	body := "Open:\n  https://example.test/a\n\tthen Request indexing  "
	id := addStepAs(t, proj, "--title", "Request indexing", "--due", "+3d", "--repeat", "1w", "--body", body)
	evs, _ := ownerstep.Read(proj.IssueDir)
	if len(evs) != 1 || evs[0].Body != body || evs[0].Issue != "sim/rules" || evs[0].Due != "2026-11-19" || evs[0].Repeat != 7 || evs[0].By == "" {
		t.Fatalf("log %+v", evs)
	}
	out, err := ownerStepAt(proj, ownerStepNow, "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{id + " · open · due 2026-11-19 · repeats 7d · sim/rules", "  Request indexing", "    https://example.test/a", "/needs-you"} {
		assertContains(t, out, want)
	}
}

// Proves: AC5 — add refuses a body that looks like a secret, an empty
// title or body, a due day in the past and a body over the cap, and writes
// nothing.
func TestOwnerStepAddRefuses(t *testing.T) {
	proj, _ := ledgerFixture(t)
	asOwner(t)
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"secret":   {[]string{"--title", "Set the key", "--due", "today", "--body", "export TOKEN=ghp_" + "A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8"}, "seems to hold a secret"},
		"no title": {[]string{"--due", "today", "--body", "x"}, "needs --title"},
		"no body":  {[]string{"--title", "x", "--due", "today"}, "needs --body"},
		"past":     {[]string{"--title", "x", "--due", "2026-11-15", "--body", "x"}, "before today"},
		"no due":   {[]string{"--title", "x", "--body", "x"}, "--due is required"},
		"too long": {[]string{"--title", "x", "--due", "today", "--body", strings.Repeat("a ", ownerstep.MaxBody)}, "the cap is"},
		"repeat":   {[]string{"--title", "x", "--due", "today", "--body", "x", "--repeat", "monthly"}, "want Nd or Nw"},
		"issue":    {[]string{"--title", "x", "--due", "today", "--body", "x", "--issue", "no/such"}, "no such issue"},
	} {
		_, err := ownerStepAt(proj, ownerStepNow, append([]string{"add"}, c.args...)...)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err %v, want %q", name, err, c.want)
		}
	}
	if _, err := os.Stat(ownerstep.LogPath(proj.IssueDir)); err == nil {
		t.Fatal("a refused add wrote the log")
	}
}

// Proves: AC2 — a worker can't tick a step (even with a --quote it typed)
// and the refusal names the owner's row and the question: path; a lead
// needs the owner's --quote, recorded as a relay; the owner's shell may.
func TestOwnerStepDoneRefusesWorker(t *testing.T) {
	proj, _ := ledgerFixture(t)
	asOwner(t)
	id := addStepAs(t, proj, "--title", "Reinstall the build", "--due", "today", "--body", "Xcode, Run")
	other := addStepAs(t, proj, "--title", "Upload the logo", "--due", "today", "--body", "logo.png")

	asWorkerOn(t, "sim/rules")
	for _, args := range [][]string{{"done", id}, {"done", id, "--quote", "the owner did it"}} {
		_, err := ownerStepAt(proj, ownerStepNow, args...)
		if err == nil || !strings.Contains(err.Error(), "the owner's act: a worker can't") {
			t.Fatalf("%v: err %v", args, err)
		}
		assertContains(t, err.Error(), "/needs-you")
		assertContains(t, err.Error(), `comment sim/rules "question: did you do owner step `+id)
	}
	// A worker can't drop a step it didn't add either.
	if _, err := ownerStepAt(proj, ownerStepNow, "drop", id, "--why", "x"); err == nil || !strings.Contains(err.Error(), "a worker drops only a step it added") {
		t.Fatalf("worker drop: %v", err)
	}
	if evs, _ := ownerstep.Read(proj.IssueDir); len(evs) != 2 {
		t.Fatalf("refused acts wrote the log: %+v", evs)
	}

	asOwner(t)
	asLead(t)
	if _, err := ownerStepAt(proj, ownerStepNow, "done", id); err == nil || !strings.Contains(err.Error(), "--quote") {
		t.Fatalf("lead without --quote: %v", err)
	}
	if _, err := ownerStepAt(proj, ownerStepNow, "done", id, "--quote", "reinstalled it"); err != nil {
		t.Fatal(err)
	}
	evs, _ := ownerstep.Read(proj.IssueDir)
	if last := evs[len(evs)-1]; last.Op != ownerstep.OpDone || last.Role != ownerstep.RoleLeadRelay || last.Words != "reinstalled it" || last.By != tracker.LeadSessionName(proj) {
		t.Fatalf("lead relay %+v", last)
	}

	asOwner(t)
	out, err := ownerStepAt(proj, ownerStepNow, "done", other)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, out, "✓ "+other+" done")
	evs, _ = ownerstep.Read(proj.IssueDir)
	if last := evs[len(evs)-1]; last.Role != ownerstep.RoleOwnerShell {
		t.Fatalf("owner shell %+v", last)
	}
	if _, err := ownerStepAt(proj, ownerStepNow, "done", other); err == nil || !strings.Contains(err.Error(), "not open") {
		t.Fatalf("done twice: %v", err)
	}
	if _, err := ownerStepAt(proj, ownerStepNow, "done", "st-ffffff"); err == nil || !strings.Contains(err.Error(), "no step st-ffffff") {
		t.Fatalf("unknown id: %v", err)
	}
}

// A repeating step ticked from the CLI says when it opens again, and list
// shows it again on that day.
func TestOwnerStepDoneRepeatReopens(t *testing.T) {
	proj, _ := ledgerFixture(t)
	asOwner(t)
	id := addStepAs(t, proj, "--title", "Reinstall the build", "--due", "today", "--repeat", "7d", "--body", "Xcode, Run")
	out, err := ownerStepAt(proj, ownerStepNow, "done", id)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, out, "It opens again on 2026-11-23.")
	out, _ = ownerStepAt(proj, ownerStepNow.AddDate(0, 0, 6), "list")
	assertContains(t, out, "No open owner step")
	out, _ = ownerStepAt(proj, ownerStepNow.AddDate(0, 0, 7), "list")
	assertContains(t, out, id+" · open · due today")
	// A worker drops only its own unticked step.
	asWorkerOn(t, "sim/rules")
	mine := addStepAs(t, proj, "--title", "Typo", "--due", "today", "--body", "x")
	if _, err := ownerStepAt(proj, ownerStepNow, "drop", mine, "--why", "filed by mistake"); err != nil {
		t.Fatal(err)
	}
}
