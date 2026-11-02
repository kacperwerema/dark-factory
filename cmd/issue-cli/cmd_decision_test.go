package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/yolo"
)

func decisionAt(proj *tracker.Project, args ...string) (string, error) {
	ctx, stdout, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return time.Date(2026, 11, 1, 9, 0, 0, 0, time.Local) }
	err := runDecision(ctx, args)
	return stdout.String(), err
}

func openChallenges(t *testing.T, proj *tracker.Project) []tracker.Challenge {
	t.Helper()
	open, err := tracker.LoadOpenChallenges(proj.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	return open
}

func commentsWith(t *testing.T, path, prefix string) []string {
	t.Helper()
	cs, err := tracker.LoadComments(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, c := range cs {
		if strings.HasPrefix(c.Text, prefix) {
			out = append(out, c.Text)
		}
	}
	return out
}

// challengeFixture is ledgerFixture with sim/creeps's worker challenging
// sim/rules#D-1.
func challengeFixture(t *testing.T) (*tracker.Project, string, string) {
	t.Helper()
	proj, rulesPath := ledgerFixture(t)
	creeps := filepath.Join(proj.IssueDir, "Sim", "creeps.md")
	asWorkerOn(t, "sim/creeps")
	if _, err := decisionAt(proj, "challenge", "sim/rules#D-1", "--why", "camps need their own rule", "--propose", "split it"); err != nil {
		t.Fatalf("challenge: %v", err)
	}
	return proj, rulesPath, creeps
}

// AC1: an unknown ref fails and writes nothing; a known one writes the
// challenge: comment on both issues and one challenged event.
func TestDecisionChallengeWritesBothCommentsAndAnEvent(t *testing.T) {
	proj, rulesPath := ledgerFixture(t)
	creeps := filepath.Join(proj.IssueDir, "Sim", "creeps.md")
	asWorkerOn(t, "sim/creeps")

	for _, args := range [][]string{
		{"challenge", "sim/rules#D-9", "--why", "x"},
		{"challenge", "sim/nope#D-1", "--why", "x"},
		{"challenge", "sim/rules#D-1"},
		{"challenge", "sim/rules#D-1", "--why", "x", "--for", "sim/phases"},
	} {
		if _, err := decisionAt(proj, args...); err == nil {
			t.Fatalf("%v: want a refusal", args)
		}
	}
	if _, err := os.Stat(tracker.DecisionLogPath(proj.IssueDir)); err == nil {
		t.Fatal("a refused challenge wrote the log")
	}
	if got := commentsWith(t, creeps, "challenge:"); len(got) != 0 {
		t.Fatalf("a refused challenge commented: %v", got)
	}

	out, err := decisionAt(proj, "challenge", "sim/rules#D-1", "--why", "camps need their own rule", "--propose", "split it")
	if err != nil {
		t.Fatalf("challenge: %v", err)
	}
	assertContains(t, out, "✓ Challenged sim/rules#D-1 for sim/creeps")
	assertContains(t, out, "no other issue is held")
	want := "challenge: sim/rules#D-1 — camps need their own rule · propose: split it (for sim/creeps, by " + tracker.AgentSessionName("sim/creeps") + ")"
	for _, path := range []string{creeps, rulesPath} {
		if got := commentsWith(t, path, "challenge:"); len(got) != 1 || got[0] != want {
			t.Fatalf("%s: comments = %q, want %q", path, got, want)
		}
	}
	evs, _ := tracker.ReadDecisionLog(proj.IssueDir)
	if len(evs) != 1 || evs[0].Event != tracker.DecisionChallenged || evs[0].Ref != "sim/rules#D-1" || evs[0].Issue != "sim/creeps" || evs[0].Why != "camps need their own rule" {
		t.Fatalf("log = %+v", evs)
	}
	if _, err := decisionAt(proj, "challenge", "sim/rules#D-1", "--why", "again"); err == nil || !strings.Contains(err.Error(), "already challenged") {
		t.Fatalf("second challenge: %v", err)
	}
	if open := openChallenges(t, proj); len(open) != 1 {
		t.Fatalf("open = %+v", open)
	}
}

// AC3: a worker's resolve is refused, a lead's without --quote is refused,
// both writing nothing; the lead with the owner's --quote, or the owner's
// shell, closes it.
func TestDecisionResolveOwnerRule(t *testing.T) {
	proj, rulesPath, creeps := challengeFixture(t)
	before := readFile(t, rulesPath)
	logBefore := readFile(t, tracker.DecisionLogPath(proj.IssueDir))

	for _, args := range [][]string{
		{"resolve", "sim/rules#D-1", "--keep"},
		{"resolve", "sim/rules#D-1", "--keep", "--quote", "I say keep it"},
	} {
		_, err := decisionAt(proj, args...)
		if err == nil || !strings.Contains(err.Error(), "answering a challenge is the owner's") {
			t.Fatalf("worker %v: err = %v", args, err)
		}
	}
	// A worker can't clear its hold by replacing or confirming either.
	if _, err := decideAt(proj, "sim/creeps", "--text", "Camps get their own rule.", "--replaces", "sim/rules#D-1"); err == nil ||
		!strings.Contains(err.Error(), "answering a challenge is the owner's") {
		t.Fatalf("worker --replaces: %v", err)
	}
	asLead(t)
	if _, err := decisionAt(proj, "resolve", "sim/rules#D-1", "--keep"); err == nil || !strings.Contains(err.Error(), "--quote") {
		t.Fatalf("lead without --quote: %v", err)
	}
	if readFile(t, rulesPath) != before || readFile(t, tracker.DecisionLogPath(proj.IssueDir)) != logBefore {
		t.Fatal("a refused resolve wrote something")
	}
	if len(openChallenges(t, proj)) != 1 {
		t.Fatal("a refused resolve closed the challenge")
	}

	out, err := decisionAt(proj, "resolve", "sim/rules#D-1", "--keep", "--quote", "keep it as it is")
	if err != nil {
		t.Fatalf("lead with --quote: %v", err)
	}
	assertContains(t, out, "✓ Kept sim/rules#D-1")
	assertContains(t, out, "Relayed the owner's words in a decision: comment on sim/rules")
	if open := openChallenges(t, proj); len(open) != 0 {
		t.Fatalf("still open: %+v", open)
	}
	if got := commentsWith(t, creeps, "challenge-resolved:"); len(got) != 1 || !strings.Contains(got[0], `sim/rules#D-1 kept, "keep it as it is" — the owner, relayed by`) {
		t.Fatalf("challenger comments = %q", got)
	}
	if got := commentsWith(t, rulesPath, "decision:"); len(got) != 1 || !strings.Contains(got[0], "kept (challenge resolved)") {
		t.Fatalf("relay comments = %q", got)
	}
	if _, err := decisionAt(proj, "resolve", "sim/rules#D-1", "--keep", "--quote", "x"); err == nil || !strings.Contains(err.Error(), "no open challenge") {
		t.Fatalf("resolve with none open: %v", err)
	}

	// The owner's shell needs no --quote.
	asWorkerOn(t, "sim/creeps")
	if _, err := decisionAt(proj, "challenge", "sim/rules#D-1", "--why", "still wrong"); err != nil {
		t.Fatal(err)
	}
	asOwner(t)
	if _, err := decisionAt(proj, "resolve", "sim/rules#D-1", "--keep"); err != nil {
		t.Fatalf("owner's shell: %v", err)
	}
	if open := openChallenges(t, proj); len(open) != 0 {
		t.Fatalf("still open after the owner: %+v", open)
	}
	evs, _ := tracker.ReadDecisionLog(proj.IssueDir)
	last := evs[len(evs)-1]
	if last.Event != tracker.DecisionResolved || last.Role != tracker.ActorOwnerShell || last.New != tracker.ChallengeKept {
		t.Fatalf("last event = %+v", last)
	}
}

// AC4: the owner's decide --replaces closes the open challenge on the ref
// it replaces; confirming a challenged provisional decision keeps it.
func TestDecideReplacesClosesTheChallenge(t *testing.T) {
	proj, _, creeps := challengeFixture(t)
	asOwner(t)
	out, err := decideAt(proj, "sim/rules", "--text", "Camps get their own rule.", "--replaces", "D-1", "--quote", "fine, split it")
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	assertContains(t, out, "Resolved sim/creeps's challenge on sim/rules#D-1 (replaced by sim/rules#D-5)")
	if open := openChallenges(t, proj); len(open) != 0 {
		t.Fatalf("still open: %+v", open)
	}
	if got := commentsWith(t, creeps, "challenge-resolved:"); len(got) != 1 || !strings.Contains(got[0], "sim/rules#D-1 replaced by sim/rules#D-5") {
		t.Fatalf("challenger comments = %q", got)
	}
	evs, _ := tracker.ReadDecisionLog(proj.IssueDir)
	last := evs[len(evs)-1]
	if last.Event != tracker.DecisionResolved || last.New != tracker.ChallengeReplaced || last.ByRef != "sim/rules#D-5" {
		t.Fatalf("last event = %+v", last)
	}

	// Confirm: a challenged provisional decision, kept with the owner's words.
	asWorkerOn(t, "sim/creeps")
	if _, err := decisionAt(proj, "challenge", "sim/rules#D-3", "--why", "too vague"); err != nil {
		t.Fatal(err)
	}
	if _, err := decideAt(proj, "sim/rules", "--confirm", "D-3", "--quote", "yes"); err == nil || !strings.Contains(err.Error(), "owner's") {
		t.Fatalf("worker confirm: %v", err)
	}
	asOwner(t)
	if _, err := decideAt(proj, "sim/rules", "--confirm", "D-3", "--quote", "yes, keep it"); err != nil {
		t.Fatalf("owner confirm: %v", err)
	}
	if open := openChallenges(t, proj); len(open) != 0 {
		t.Fatalf("confirm left it open: %+v", open)
	}
}

// Must not: typing the comments opens and closes nothing.
func TestTypedChallengeCommentOpensNothing(t *testing.T) {
	proj, _, creeps := challengeFixture(t)
	if err := tracker.AddCommentBy(creeps, 0, "challenge-resolved: sim/rules#D-1 kept — owner", "cli", "agent-x"); err != nil {
		t.Fatal(err)
	}
	if err := tracker.AddCommentBy(creeps, 0, "challenge: sim/rules#D-2 — typed (for sim/creeps, by agent-x)", "cli", "agent-x"); err != nil {
		t.Fatal(err)
	}
	open := openChallenges(t, proj)
	if len(open) != 1 || open[0].Ref != "sim/rules#D-1" {
		t.Fatalf("open = %+v", open)
	}
}

// The lead sees each new challenge once, on its next call; the log is not
// written by showing it.
func TestLeadSeesEachChallengeOnce(t *testing.T) {
	proj, _, _ := challengeFixture(t)
	logBefore := readFile(t, tracker.DecisionLogPath(proj.IssueDir))
	asLead(t)
	show := func() string {
		ctx, out, _ := newTestContext(proj, false)
		printLeadChallenges(ctx, "show")
		return out.String()
	}
	first := show()
	assertContains(t, first, "== Decision challenges (1 new) ==")
	assertContains(t, first, "sim/rules#D-1 challenged by sim/creeps: camps need their own rule")
	assertContains(t, first, "decision resolve sim/rules#D-1 --keep --quote")
	if again := show(); again != "" {
		t.Fatalf("shown twice:\n%s", again)
	}
	asWorkerOn(t, "sim/creeps")
	ctx, out, _ := newTestContext(proj, false)
	printLeadChallenges(ctx, "show")
	if out.Len() != 0 {
		t.Fatalf("a worker got the lead's block:\n%s", out.String())
	}
	if readFile(t, tracker.DecisionLogPath(proj.IssueDir)) != logBefore {
		t.Fatal("showing the block wrote the log")
	}
}

// unchallengedProject is the ledger project with decision_unchallenged on
// in design → backlog, the design issue's worker as the caller, and a
// second issue (peer) that cites rules#D-1.
func unchallengedProject(t *testing.T, approval bool) *tracker.Project {
	t.Helper()
	proj := makeLedgerProject(t, true, approval, "Follows rules#D-1: merges into local main.")
	wf := readFile(t, proj.WorkflowFile)
	wf = strings.Replace(wf, "        rule: \"citations_current\"\n", "        rule: \"citations_current\"\n      - type: \"validate\"\n        rule: \"decision_unchallenged\"\n", 1)
	if err := os.WriteFile(proj.WorkflowFile, []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	peer := "---\ntitle: \"Peer\"\nstatus: \"in design\"\ncreated: \"2026-10-31\"\n---\n\nBody of Peer.\n\n## Design\n\nFollows rules#D-1: merges into local main.\n"
	if err := os.WriteFile(filepath.Join(proj.IssueDir, "peer.md"), []byte(peer), 0o644); err != nil {
		t.Fatal(err)
	}
	return proj
}

// AC5, AC6: the challenger's transition refuses while its challenge is
// open and passes once the owner resolves it; a peer that cites the same
// decision is never held.
func TestChallengeHoldsOnlyTheChallenger(t *testing.T) {
	now := time.Now()
	proj := unchallengedProject(t, false)
	if _, err := decisionAt(proj, "challenge", "rules#D-1", "--why", "PRs give a review step"); err != nil {
		t.Fatalf("challenge: %v", err)
	}
	_, _, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog")
	if err == nil {
		t.Fatal("the challenger passed with its challenge open")
	}
	for _, w := range []string{"decision_unchallenged: this issue's challenge waits for the owner's answer", "rules#D-1", "PRs give a review step", "decision resolve rules#D-1 --keep --quote"} {
		assertContains(t, err.Error(), w)
	}
	if s := loadLeadIssue(t, proj, "design").Status; s != "in design" {
		t.Fatalf("status moved to %q", s)
	}
	held := false
	evs, _ := tracker.ReadDecisionLog(proj.IssueDir)
	for _, ev := range evs {
		held = held || (ev.Event == tracker.DecisionHeld && ev.Rule == tracker.DecisionUnchallengedRule && ev.Ref == "rules#D-1" && ev.Issue == "design")
	}
	if !held {
		t.Fatalf("no held event for the refusal: %+v", evs)
	}

	// The peer, citing the challenged decision, goes on.
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "peer")
	if _, _, err := runYOLOTransition(t, proj, now, "peer", "--to", "backlog"); err != nil {
		t.Fatalf("the peer was held: %v", err)
	}

	asOwner(t)
	if _, err := decisionAt(proj, "resolve", "rules#D-1", "--keep"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "design")
	if _, _, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog"); err != nil {
		t.Fatalf("after the owner resolved: %v", err)
	}
}

// AC7: with YOLO on, the challenger's held gate passes, posts the
// yolo-hold: comment naming the challenge, and yolo queue lists it.
func TestYOLOPassesChallengeHoldIntoTheQueue(t *testing.T) {
	now := time.Now()
	proj := unchallengedProject(t, true)
	yoloOn(t, proj, now, time.Hour)
	if _, err := decisionAt(proj, "challenge", "rules#D-1", "--why", "PRs give a review step"); err != nil {
		t.Fatalf("challenge: %v", err)
	}
	out, _, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog")
	if err != nil {
		t.Fatalf("YOLO transition: %v", err)
	}
	assertContains(t, out, "YOLO: decision_unchallenged: rules#D-1 challenged by this issue: PRs give a review step — passed, in the after-the-fact queue")
	issue := loadLeadIssue(t, proj, "design")
	if issue.Status != "backlog" {
		t.Fatalf("status = %q", issue.Status)
	}
	if got := commentsWith(t, issue.FilePath, "yolo-hold:"); len(got) != 1 || got[0] != "yolo-hold: decision_unchallenged rules#D-1 from in design: challenged by this issue: PRs give a review step" {
		t.Fatalf("holds = %q", got)
	}
	var found bool
	for _, e := range yolo.QueueEntries([]*tracker.Issue{issue}) {
		for _, d := range e.Decisions {
			if e.Slug == "design" && d.Decision == "rules#D-1" && d.Hold == tracker.DecisionUnchallengedRule {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("yolo queue has no entry naming the challenge")
	}
	ctx, qout, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return now }
	if err := runYOLO(ctx, []string{"queue"}); err != nil {
		t.Fatalf("yolo queue: %v", err)
	}
	assertContains(t, qout.String(), "rules#D-1: decision_unchallenged passed in YOLO (challenged by this issue: PRs give a review step)  from in design")
	// A challenge never clears a gate: it is still open after the pass.
	if len(openChallenges(t, proj)) != 1 {
		t.Fatal("the YOLO pass closed the challenge")
	}
}
