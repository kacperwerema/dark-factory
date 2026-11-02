package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The expiry fixture's day is 2026-10-31 (decideAt's clock).
const expiryRules = "---\ntitle: \"rules\"\nstatus: \"in progress\"\nsystem: \"Sim\"\n---\n\n## Decisions\n\n" +
	"- **D-1** · 2026-10-01 · chat: Freeze the map, \"freeze it\" {scope: project; hard; expires: 2026-10-30}\n" +
	"- **D-2** · 2026-10-02 · chat: Two lanes only, \"two lanes\" {scope: project; hard; expires: 2026-10-31}\n" +
	"- **D-3** · 2026-10-03 · chat: Soft trial {scope: project; expires: 2026-10-20}\n" +
	"- **D-4** · 2026-10-04 · chat: No expiry, \"keep\" {scope: project; hard}\n"

func expiryFixture(t *testing.T) (*tracker.Project, string) {
	t.Helper()
	proj, _, _ := makeDecisionFixture(t)
	path := filepath.Join(proj.IssueDir, "Sim", "rules.md")
	if err := os.WriteFile(path, []byte(expiryRules), 0o644); err != nil {
		t.Fatal(err)
	}
	return proj, path
}

func decisionsAt(t *testing.T, proj *tracker.Project, args ...string) string {
	t.Helper()
	ctx, stdout, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return time.Date(2026, 10, 31, 9, 0, 0, 0, time.Local) }
	if err := runDecisions(ctx, args); err != nil {
		t.Fatalf("decisions %v: %v", args, err)
	}
	return stdout.String()
}

// TestDecisionsExpiredState: expires: yesterday is out of force and listed
// by --state expired; expires: today is still in force (AC1).
func TestDecisionsExpiredState(t *testing.T) {
	proj, _ := expiryFixture(t)
	asOwner(t)
	inForce := decisionsAt(t, proj, "--in-force", "sim/creeps")
	assertContains(t, inForce, "sim/rules#D-2 · hard")
	if strings.Contains(inForce, "sim/rules#D-1 ") || strings.Contains(inForce, "sim/rules#D-3 ") {
		t.Fatalf("an expired decision is in force:\n%s", inForce)
	}
	assertContains(t, inForce, "2 more decisions are out of force by its expires: date: issue-cli")

	expired := decisionsAt(t, proj, "--state", "expired")
	assertContains(t, expired, "== Decisions (2) ==")
	assertContains(t, expired, "sim/rules#D-1 · 2026-10-01 · expired after 2026-10-30 · hard · waits for the owner")
	assertContains(t, expired, "renew: issue-cli")
	assertContains(t, expired, "decide sim/rules --let-go D-1")
	assertContains(t, expired, "sim/rules#D-3 · 2026-10-03 · expired after 2026-10-20 · needs owner's words · scope project")
	if strings.Contains(expired, "sim/rules#D-2 ") {
		t.Fatalf("expires: today listed as expired:\n%s", expired)
	}
	active := decisionsAt(t, proj, "--state", "active")
	if strings.Contains(active, "sim/rules#D-1 ") || !strings.Contains(active, "sim/rules#D-2 ") {
		t.Fatalf("--state active:\n%s", active)
	}
}

// expiryActs are renew and let go of the expired hard D-1.
var expiryActs = []struct {
	name string
	args []string
}{
	{"renew", []string{"sim/rules", "--renew", "D-1", "--expires", "2027-01-31"}},
	{"let go", []string{"sim/rules", "--let-go", "D-1"}},
}

// TestRenewLetGoOwnerRule: a worker is refused renewing or letting go of a
// hard decision, even with a --quote; the lead only with --quote, posting
// the relay comment; the owner's shell may, its words a decision: comment
// (AC3).
func TestRenewLetGoOwnerRule(t *testing.T) {
	for _, act := range expiryActs {
		t.Run(act.name, func(t *testing.T) {
			proj, path := expiryFixture(t)
			asWorkerOn(t, "sim/rules")
			for _, args := range [][]string{act.args, withQuote(act.args)} {
				_, err := decideAt(proj, args...)
				if err == nil || !strings.Contains(err.Error(), "a hard decision is the owner's act: a worker can't") {
					t.Fatalf("worker %v: err = %v", args, err)
				}
			}
			if readFile(t, path) != expiryRules {
				t.Fatal("a refused act changed the file")
			}
			if _, err := os.Stat(tracker.DecisionLogPath(proj.IssueDir)); err == nil {
				t.Fatal("a refused act wrote the log")
			}

			asOwner(t)
			asLead(t)
			if _, err := decideAt(proj, act.args...); err == nil || !strings.Contains(err.Error(), "--quote") {
				t.Fatalf("lead without --quote: err = %v", err)
			}
			if readFile(t, path) != expiryRules {
				t.Fatal("refused, yet the file changed")
			}
			out, err := decideAt(proj, withQuote(act.args)...)
			if err != nil {
				t.Fatalf("lead with --quote: %v", err)
			}
			assertContains(t, out, "Relayed the owner's words in a decision: comment on sim/rules")
			cs, _ := tracker.LoadComments(path)
			if len(cs) != 1 || !strings.Contains(cs[0].Text, "decision: sim/rules#D-1 ") || !strings.Contains(cs[0].Text, "— relayed by ") {
				t.Fatalf("comments = %+v", cs)
			}
			evs, _ := tracker.ReadDecisionLog(proj.IssueDir)
			want := tracker.DecisionRenewedEvent
			if act.name == "let go" {
				want = tracker.DecisionExpiredEvent
			}
			if len(evs) != 1 || evs[0].Event != want || evs[0].Role != tracker.ActorLeadRelay || evs[0].Ref != "sim/rules#D-1" {
				t.Fatalf("log = %+v", evs)
			}
		})
	}
}

// TestRenewLetGoOwnerShellWords: the owner's own shell runs both without a
// relay; --quote becomes a decision: comment by the owner.
func TestRenewLetGoOwnerShellWords(t *testing.T) {
	proj, path := expiryFixture(t)
	asOwner(t)
	out, err := decideAt(proj, "sim/rules", "--renew", "D-1", "--expires", "2027-01-31", "--quote", "keep it frozen")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, out, "✓ Renewed sim/rules#D-1 until 2027-01-31 (was expired, expires: 2026-10-30)")
	assertContains(t, out, "Your words are in a decision: comment on sim/rules")
	cs, _ := tracker.LoadComments(path)
	if len(cs) != 1 || cs[0].Text != `decision: sim/rules#D-1 renewed until 2027-01-31, "keep it frozen" — the owner` || cs[0].Author != tracker.OwnerAuthor {
		t.Fatalf("comments = %+v", cs)
	}
	evs, _ := tracker.ReadDecisionLog(proj.IssueDir)
	if len(evs) != 1 || evs[0].Role != tracker.ActorOwnerShell || evs[0].Old != "{scope: project; hard; expires: 2026-10-30}" || evs[0].New != "{scope: project; hard; expires: 2027-01-31}" {
		t.Fatalf("log = %+v", evs)
	}
	// No words: no comment.
	if _, err := decideAt(proj, "sim/rules", "--let-go", "D-3"); err != nil {
		t.Fatal(err)
	}
	if cs, _ := tracker.LoadComments(path); len(cs) != 1 {
		t.Fatalf("let go without words posted a comment: %+v", cs)
	}
}

// TestRenewLetGoKeepLineBytes: only the attribute group changes.
func TestRenewLetGoKeepLineBytes(t *testing.T) {
	proj, path := expiryFixture(t)
	asOwner(t)
	if _, err := decideAt(proj, "sim/rules", "--renew", "D-1", "--expires", "2027-01-31"); err != nil {
		t.Fatal(err)
	}
	if _, err := decideAt(proj, "sim/rules", "--let-go", "D-3"); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(expiryRules, "{scope: project; hard; expires: 2026-10-30}", "{scope: project; hard; expires: 2027-01-31}", 1)
	want = strings.Replace(want, "{scope: project; expires: 2026-10-20}", "{scope: project; expires: 2026-10-20; ended: 2026-10-31}", 1)
	if got, w := decisionLines(readFile(t, path)), decisionLines(want); got != w {
		t.Fatalf("decision lines:\n%s\nwant:\n%s", got, w)
	}
	expired := decisionsAt(t, proj, "--state", "expired")
	assertContains(t, expired, "sim/rules#D-3 · 2026-10-03 · expired after 2026-10-20 · let go 2026-10-31")
	if strings.Contains(expired, "sim/rules#D-1 ") {
		t.Fatalf("renewed decision still expired:\n%s", expired)
	}
}

// decisionLines is the D-n lines of a body.
func decisionLines(body string) string {
	var out []string
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, "- **D-") {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// TestRenewLetGoRefusals: each refusal names the fix and writes nothing.
func TestRenewLetGoRefusals(t *testing.T) {
	proj, path := expiryFixture(t)
	asOwner(t)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"sim/rules", "--renew", "D-4", "--expires", "2027-01-01"}, "has no expires: date: nothing to renew"},
		{[]string{"sim/rules", "--let-go", "D-2"}, "has not expired"},
		{[]string{"sim/rules", "--renew", "D-1"}, "needs --expires YYYY-MM-DD"},
		{[]string{"sim/rules", "--renew", "D-1", "--expires", "2026-10-30"}, "is in the past"},
		{[]string{"sim/rules", "--renew", "D-1", "--let-go", "D-1"}, "pick one"},
		{[]string{"sim/rules", "--let-go", "D-1", "--expires", "2027-01-01"}, "--let-go only --quote"},
		{[]string{"sim/rules", "--amend", "D-1", "--expires", "2027-01-01"}, "renew it or let it go"},
		{[]string{"sim/rules", "--renew", "sim/phases#D-1", "--expires", "2027-01-01"}, "on its own issue"},
	}
	for _, c := range cases {
		_, err := decideAt(proj, c.args...)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%v: err = %v, want %q", c.args, err, c.want)
		}
	}
	if readFile(t, path) != expiryRules {
		t.Fatal("a refusal changed the file")
	}
	if _, err := decideAt(proj, "sim/rules", "--let-go", "D-1"); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"sim/rules", "--renew", "D-1", "--expires", "2027-01-01"}, {"sim/rules", "--let-go", "D-1"}} {
		if _, err := decideAt(proj, args...); err == nil || !strings.Contains(err.Error(), "was let go: that is final") {
			t.Fatalf("%v after let go: err = %v", args, err)
		}
	}
}

// TestRenewLetGoReadClockOnce: a clock that crosses midnight between reads
// still gives one day to the check, the line, the comment and the log
// (AC4).
func TestRenewLetGoReadClockOnce(t *testing.T) {
	proj, path := expiryFixture(t)
	asOwner(t)
	ctx, _, _ := newTestContext(proj, false)
	calls := 0
	ctx.Now = func() time.Time {
		calls++
		// The first read is a moment before midnight; any later read is the
		// next day.
		return time.Date(2026, 10, 31, 23, 59, 59, 0, time.Local).Add(time.Duration(calls-1) * time.Hour)
	}
	if err := runDecide(ctx, []string{"sim/rules", "--let-go", "D-1", "--quote", "let it go"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("the clock was read %d times, want once", calls)
	}
	assertContains(t, readFile(t, path), "{scope: project; hard; expires: 2026-10-30; ended: 2026-10-31}")
	cs, _ := tracker.LoadComments(path)
	if len(cs) != 1 || !strings.HasPrefix(cs[0].Date, "2026-10-31T23:59:59") {
		t.Fatalf("comment = %+v", cs)
	}
	evs, _ := tracker.ReadDecisionLogAll(proj.IssueDir)
	for _, ev := range evs {
		if !strings.HasPrefix(ev.At, "2026-10-31T23:59:59") {
			t.Fatalf("event at %s", ev.At)
		}
	}
}

// TestExpiredReadsWriteNothing: listing expired decisions and the in-force
// view never write the log or the issue.
func TestExpiredReadsWriteNothing(t *testing.T) {
	proj, path := expiryFixture(t)
	asOwner(t)
	decisionsAt(t, proj, "--state", "expired")
	decisionsAt(t, proj, "--in-force", "sim/rules")
	decisionsAt(t, proj, "--state", "expired", "--json")
	if _, err := os.Stat(tracker.DecisionLogPath(proj.IssueDir)); err == nil {
		t.Fatal("a read wrote the decisions log")
	}
	if readFile(t, path) != expiryRules {
		t.Fatal("a read changed the issue")
	}
}
