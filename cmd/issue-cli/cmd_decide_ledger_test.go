package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/yolo"
)

const ledgerRules = "---\ntitle: \"rules\"\nstatus: \"in progress\"\nsystem: \"Sim\"\n---\n\n## Decisions\n\n" +
	"- **D-1** · 2026-10-01 · chat: Soft rule, \"yes\"\n" +
	"- **D-2** · 2026-10-02 · chat: Merge into main only, \"only main\" {scope: project; hard; paths: cmd/**}\n" +
	"- **D-3** · 2026-10-03 · YOLO, provisional: Maybe rule\n" +
	"- **D-4** · 2026-10-04 · chat: Timed rule, \"ok\" {at: in progress}\n"

// ledgerFixture is makeDecisionFixture plus sim/rules with a soft, a
// hard, a provisional and an at:-scoped decision.
func ledgerFixture(t *testing.T) (*tracker.Project, string) {
	t.Helper()
	proj, _, _ := makeDecisionFixture(t)
	path := filepath.Join(proj.IssueDir, "Sim", "rules.md")
	if err := os.WriteFile(path, []byte(ledgerRules), 0o644); err != nil {
		t.Fatal(err)
	}
	return proj, path
}

// asWorkerOn makes issue-cli see the worker dispatched on slug.
func asWorkerOn(t *testing.T, slug string) {
	t.Helper()
	asOwner(t)
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", slug)
}

func decideAt(proj *tracker.Project, args ...string) (string, error) {
	ctx, stdout, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return time.Date(2026, 10, 31, 9, 0, 0, 0, time.Local) }
	err := runDecide(ctx, args)
	return stdout.String(), err
}

// ownerActRows are the owner acts AC3 and AC4 name, each without --quote.
var ownerActRows = []struct {
	name string
	args []string
}{
	{"decide --hard", []string{"sim/rules", "--text", "Never ship on Fridays.", "--hard"}},
	{"amend drops hard", []string{"sim/rules", "--amend", "D-2", "--soft", "--paths", ""}},
	{"amend empties paths", []string{"sim/rules", "--amend", "D-2", "--paths", ""}},
	{"amend adds expires", []string{"sim/rules", "--amend", "D-2", "--expires", "2027-01-31"}},
	{"amend changes scope", []string{"sim/rules", "--amend", "D-2", "--scope", "system:Sim"}},
	{"replaces a hard one", []string{"sim/rules", "--text", "Merge via PRs.", "--replaces", "D-2"}},
}

func withQuote(args []string) []string {
	return append(append([]string(nil), args...), "--quote", "the owner said so")
}

// TestDecideOwnerActsRefuseWorker: from a worker, every change to a hard
// decision exits with the owner-act refusal and writes nothing, even with
// a --quote it typed (AC3).
func TestDecideOwnerActsRefuseWorker(t *testing.T) {
	for _, row := range ownerActRows {
		t.Run(row.name, func(t *testing.T) {
			proj, path := ledgerFixture(t)
			asWorkerOn(t, "sim/rules")
			for _, args := range [][]string{row.args, withQuote(row.args)} {
				_, err := decideAt(proj, args...)
				if err == nil || !strings.Contains(err.Error(), "changes to a hard decision are the owner's") {
					t.Fatalf("%v: err = %v, want the owner-act refusal", args, err)
				}
				assertContains(t, err.Error(), "comment sim/rules \"question:")
			}
			if readFile(t, path) != ledgerRules {
				t.Fatal("the issue file changed")
			}
			if _, err := os.Stat(tracker.DecisionLogPath(proj.IssueDir)); err == nil {
				t.Fatal("a refused act wrote the log")
			}
		})
	}
}

// TestDecideOwnerActsLeadNeedsQuote: the lead without --quote is refused;
// with it the act runs and the log records the lead as relaying (AC4).
func TestDecideOwnerActsLeadNeedsQuote(t *testing.T) {
	for _, row := range ownerActRows {
		t.Run(row.name, func(t *testing.T) {
			proj, path := ledgerFixture(t)
			asOwner(t)
			asLead(t)
			_, err := decideAt(proj, row.args...)
			if err == nil || !strings.Contains(err.Error(), "relay the owner's own words with --quote") {
				t.Fatalf("err = %v, want the --quote refusal", err)
			}
			if readFile(t, path) != ledgerRules {
				t.Fatal("refused, yet the file changed")
			}
			out, err := decideAt(proj, withQuote(row.args)...)
			if err != nil {
				t.Fatalf("with --quote: %v", err)
			}
			assertContains(t, out, "Relayed the owner's words in a decision: comment on sim/rules")
			evs, _ := tracker.ReadDecisionLog(proj.IssueDir)
			if len(evs) == 0 || evs[0].Role != tracker.ActorLeadRelay || evs[0].By != tracker.LeadSessionName(proj) || evs[0].Comment == 0 {
				t.Fatalf("log = %+v", evs)
			}
			cs, _ := tracker.LoadComments(path)
			if len(cs) != 1 || !strings.Contains(cs[0].Text, `"the owner said so" — relayed by `+tracker.LeadSessionName(proj)) {
				t.Fatalf("comments = %+v", cs)
			}
		})
	}
}

// TestDecideReplacesHardInherits: a decision replacing a hard one is hard
// with the same scope and paths; --soft drops hard (AC5).
func TestDecideReplacesHardInherits(t *testing.T) {
	proj, path := ledgerFixture(t)
	asOwner(t)
	if _, err := decideAt(proj, "sim/rules", "--text", "Merge via the queue.", "--quote", "use the queue", "--replaces", "D-2"); err != nil {
		t.Fatal(err)
	}
	d := tracker.ParseDecisions("sim/rules", readFile(t, path))[4]
	if d.ID != "D-5" || !d.Hard || d.Scope != "project" || strings.Join(d.Paths, ",") != "cmd/**" {
		t.Fatalf("D-5 = %+v", d.DecisionAttrs)
	}
	if _, err := decideAt(proj, "sim/rules", "--text", "Anything goes.", "--quote", "relax", "--replaces", "D-5", "--soft"); err != nil {
		t.Fatal(err)
	}
	d = tracker.ParseDecisions("sim/rules", readFile(t, path))[5]
	if d.Hard || d.Scope != "project" || len(d.Paths) != 0 {
		t.Fatalf("D-6 = %+v", d.DecisionAttrs)
	}
	// A hard decision needs the owner's words on its line.
	if _, err := decideAt(proj, "sim/rules", "--text", "No words.", "--hard"); err == nil || !strings.Contains(err.Error(), "needs the owner's words") {
		t.Fatalf("err = %v", err)
	}
	if _, err := decideAt(proj, "sim/rules", "--amend", "D-3", "--hard"); err == nil || !strings.Contains(err.Error(), "--confirm D-3") {
		t.Fatalf("err = %v", err)
	}
}

// TestDecideSoftAmendByWorker: agents may scope soft decisions of their own
// issue (AC8).
func TestDecideSoftAmendByWorker(t *testing.T) {
	proj, path := ledgerFixture(t)
	asWorkerOn(t, "sim/rules")
	out, err := decideAt(proj, "sim/rules", "--amend", "D-1", "--scope", "system:Sim", "--at", "in progress, backlog")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, out, "✓ Amended sim/rules#D-1: {} (scope issue, soft) → {scope: system:Sim; at: in progress, backlog}")
	assertContains(t, readFile(t, path), "- **D-1** · 2026-10-01 · chat: Soft rule, \"yes\" {scope: system:Sim; at: in progress, backlog}\n")
	evs, _ := tracker.ReadDecisionLog(proj.IssueDir)
	if len(evs) != 1 || evs[0].Role != tracker.ActorAgent || evs[0].By != tracker.AgentSessionName("sim/rules") {
		t.Fatalf("log = %+v", evs)
	}
	if _, err := decideAt(proj, "sim/rules", "--amend", "D-1", "--at", "shipping"); err == nil || !strings.Contains(err.Error(), "statuses: in design, backlog, in progress, done") {
		t.Fatalf("unknown status: %v", err)
	}
	if _, err := decideAt(proj, "sim/rules", "--amend", "D-1", "--expires", "2026-10-01"); err == nil || !strings.Contains(err.Error(), "in the past") {
		t.Fatalf("past expires: %v", err)
	}
	if _, err := decideAt(proj, "sim/rules", "--amend", "D-1", "--paths", "x/**"); err == nil || !strings.Contains(err.Error(), "paths: guard only hard decisions") {
		t.Fatalf("paths on soft: %v", err)
	}
}

// TestDecideWorkerScopeLimits: a worker can't scope to the project or a
// department, nor amend another issue's decision.
func TestDecideWorkerScopeLimits(t *testing.T) {
	proj, path := ledgerFixture(t)
	asWorkerOn(t, "sim/rules")
	for _, scope := range []string{"project", "department:product"} {
		if _, err := decideAt(proj, "sim/rules", "--text", "Wide rule.", "--scope", scope); err == nil || !strings.Contains(err.Error(), "ask the lead to scope it") {
			t.Fatalf("--scope %s: %v", scope, err)
		}
	}
	if _, err := decideAt(proj, "sim/phases", "--amend", "D-1", "--scope", "system:Sim"); err == nil || !strings.Contains(err.Error(), "its own issue only") {
		t.Fatalf("other issue: %v", err)
	}
	if readFile(t, path) != ledgerRules {
		t.Fatal("file changed")
	}
	// The lead may.
	asLead(t)
	if _, err := decideAt(proj, "sim/rules", "--amend", "D-1", "--scope", "project"); err != nil {
		t.Fatal(err)
	}
}

// TestDecideAmendScopeKeepsBytes: --amend D-4 --scope system:Workflow
// changes only that line's group; the ref resolves in decisions; the log
// has an amended event with the old group (AC12).
func TestDecideAmendScopeKeepsBytes(t *testing.T) {
	proj, path := ledgerFixture(t)
	asOwner(t)
	wf := filepath.Join(proj.IssueDir, "Workflow")
	os.MkdirAll(wf, 0o755)
	os.WriteFile(filepath.Join(wf, "w.md"), []byte("---\ntitle: \"w\"\nstatus: \"backlog\"\nsystem: \"Workflow\"\n---\n\nBody.\n"), 0o644)
	if _, err := decideAt(proj, "sim/rules", "--amend", "D-4", "--scope", "system:Workflow"); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(ledgerRules, "- **D-4** · 2026-10-04 · chat: Timed rule, \"ok\" {at: in progress}",
		"- **D-4** · 2026-10-04 · chat: Timed rule, \"ok\" {scope: system:Workflow; at: in progress}", 1)
	if got := readFile(t, path); got != strings.Replace(want, "---\n\n## Decisions", "---\n## Decisions", 1) {
		t.Fatalf("file =\n%s", got)
	}
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runDecisions(ctx, []string{"--issue", "sim/rules"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "sim/rules#D-4 · 2026-10-04 · active · scope system:Workflow")
	assertContains(t, stdout.String(), "sim/rules#D-2 · 2026-10-02 · active · hard · scope project")
	evs, _ := tracker.ReadDecisionLog(proj.IssueDir)
	if len(evs) != 1 || evs[0].Event != tracker.DecisionAmended || evs[0].Old != "{at: in progress}" || evs[0].New != "{scope: system:Workflow; at: in progress}" || evs[0].Role != tracker.ActorOwnerShell {
		t.Fatalf("log = %+v", evs)
	}
}

// TestDecideAmendTextRefused: --amend with --text exits non-zero, writes
// nothing and names decide --replaces (AC13).
func TestDecideAmendTextRefused(t *testing.T) {
	proj, path := ledgerFixture(t)
	asOwner(t)
	_, err := decideAt(proj, "sim/rules", "--amend", "D-2", "--text", "Merge anywhere.")
	if err == nil || !strings.Contains(err.Error(), "decide sim/rules --replaces D-2 --text") {
		t.Fatalf("err = %v", err)
	}
	if readFile(t, path) != ledgerRules {
		t.Fatal("file changed")
	}
	if _, err := os.Stat(tracker.DecisionLogPath(proj.IssueDir)); err == nil {
		t.Fatal("log written")
	}
}

// TestDecideLeadRelayHardUnderYOLO: a lead-relayed --hard under YOLO is an
// entry in issue-cli yolo queue until the owner agrees (AC10).
func TestDecideLeadRelayHardUnderYOLO(t *testing.T) {
	proj, _ := ledgerFixture(t)
	asOwner(t)
	asLead(t)
	now := time.Date(2026, 10, 31, 9, 0, 0, 0, time.Local)
	if err := yolo.TurnOn(yoloRoot(proj), proj.Slug, yolo.State{Until: now.Add(48 * time.Hour), By: "owner", Words: "go", How: "viewer"}, now); err != nil {
		t.Fatal(err)
	}
	out, err := decideAt(proj, "sim/rules", "--amend", "D-1", "--hard", "--quote", "make it a rule")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, out, "YOLO is on, so it waits in the after-the-fact review")
	ctx, stdout, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return now }
	if err := runYOLOQueue(ctx, nil); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "sim/rules  (1 relayed decision)")
	ctx, _, _ = newTestContext(proj, false)
	if err := runYOLOReview(ctx, []string{"sim/rules", "--agree", "--quote", "fine"}); err != nil {
		t.Fatal(err)
	}
	ctx, stdout, _ = newTestContext(proj, false)
	ctx.Now = func() time.Time { return now }
	runYOLOQueue(ctx, nil)
	if strings.Contains(stdout.String(), "relayed decision") {
		t.Fatalf("still queued after the review:\n%s", stdout.String())
	}
}

// TestDecisionsInForceCommand: decisions --in-force lists what reaches an
// issue and leaves out what doesn't; it writes no log (AC14).
func TestDecisionsInForceCommand(t *testing.T) {
	proj, _ := ledgerFixture(t)
	write := func(rel, content string) {
		p := filepath.Join(proj.IssueDir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Sim/epic.md", "---\ntitle: \"epic\"\nstatus: \"in progress\"\nsystem: \"Sim\"\n---\n\n## Decisions\n\n- **D-1** · 2026-10-05 · chat: Epic rule, \"e\"\n")
	write("UI/ui.md", "---\ntitle: \"ui\"\nstatus: \"backlog\"\nsystem: \"UI\"\n---\n\n## Decisions\n\n"+
		"- **D-1** · 2026-10-06 · chat: Sim system rule {scope: system:Sim}\n"+
		"- **D-2** · 2026-10-06 · chat: UI system rule {scope: system:UI}\n"+
		"- **D-3** · 2026-10-06 · chat: Old project rule {scope: project}\n"+
		"- **D-4** · 2026-10-06 · chat: New project rule (replaces D-3) {scope: project}\n"+
		"- **D-5** · 2026-10-06 · deferred · chat: Later {scope: project}\n"+
		"- **D-6** · 2026-10-06 · chat: Design only {scope: project; at: in design}\n")
	write("Sim/child.md", "---\ntitle: \"child\"\nstatus: \"in progress\"\nsystem: \"Sim\"\nparent: \"sim/epic\"\n---\n\nFollows sim/phases#D-1.\n")
	asOwner(t)
	ctx, stdout, _ := newTestContext(proj, true)
	ctx.Now = func() time.Time { return time.Date(2026, 10, 31, 9, 0, 0, 0, time.Local) }
	if err := runDecisions(ctx, []string{"--in-force", "sim/child"}); err != nil {
		t.Fatal(err)
	}
	var rows []inForceRow
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r.Ref] = r.Why
	}
	for ref, why := range map[string]string{
		"sim/rules#D-2": "project", "ui/ui#D-4": "project", "ui/ui#D-1": "system:Sim",
		"sim/phases#D-1": "cites", "sim/epic#D-1": "parent sim/epic",
	} {
		if got[ref] != why {
			t.Errorf("%s: %q, want %q (all: %v)", ref, got[ref], why, got)
		}
	}
	for _, ref := range []string{"ui/ui#D-2", "ui/ui#D-3", "ui/ui#D-5", "ui/ui#D-6", "sim/rules#D-1", "sim/rules#D-4"} {
		if _, ok := got[ref]; ok {
			t.Errorf("%s should not be in force", ref)
		}
	}
	if rows[0].Ref != "sim/rules#D-2" || !rows[0].Hard {
		t.Errorf("hard first: %+v", rows[0])
	}
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runDecisions(ctx, []string{"--in-force", "sim/child", "--status", "in design"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "== Decisions in force for sim/child at in design (6: 1 hard) ==")
	assertContains(t, stdout.String(), "ui/ui#D-6 · soft · project · project")
	if _, err := os.Stat(tracker.DecisionLogPath(proj.IssueDir)); err == nil {
		t.Fatal("a read wrote the decisions log")
	}
}
