package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/leadstate"
	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// A read-only tick starts nothing and closes nothing, even with a
// startable queue item and a finished worker the CEO's tick would close;
// it says a status move, a comment and an ended worker once each.
func TestLeadTickObserveStartsAndClosesNothing(t *testing.T) {
	proj, finished, closed := tickProject(t)
	started := stubStart(t)
	mustQ(t, proj, "add", "high", "--quote", queueWords)
	*finished = []agentrun.Finished{{Name: "agent-base", Slug: "base", Why: "done"}}
	live := map[string]bool{"agent-base": true}
	orig := liveSessionNames
	liveSessionNames = func(*tracker.Project) map[string]bool { return live }
	t.Cleanup(func() { liveSessionNames = orig })

	// The first tick takes the baseline: nothing to say.
	if out := tick(t, proj, "--observe"); out != "" {
		t.Fatalf("baseline tick printed:\n%s", out)
	}
	writeIssue(t, proj, "base", "done")
	delete(live, "agent-base")
	low := loadLeadIssue(t, proj, "low")
	if err := tracker.AddCommentBy(low.FilePath, 0, "shelf copy looks right", "viewer", "owner"); err != nil {
		t.Fatal(err)
	}
	out := tick(t, proj, "--observe")
	for _, want := range []string{"status: base in progress → done", "ended: agent-base (base at done)", "comment: low owner: shelf copy looks right"} {
		assertContains(t, out, want)
	}
	if out := tick(t, proj, "--observe"); out != "" {
		t.Fatalf("said twice:\n%s", out)
	}
	if len(*started) != 0 || len(*closed) != 0 {
		t.Fatalf("observe started %v, closed %v", *started, *closed)
	}
	// The CEO's tick on the same project does both: the stubs are live.
	tick(t, proj)
	if len(*started) != 1 || len(*closed) != 1 {
		t.Fatalf("plain tick started %v, closed %v", *started, *closed)
	}
}

// --department scopes it: another department's move is not said.
func TestLeadTickObserveDepartmentScope(t *testing.T) {
	proj, _, _ := tickProject(t)
	stubStart(t)
	proj.Departments = []tracker.Department{{Name: "Marketing", Systems: []string{"SEO"}}}
	seo := func(status string) {
		body := "---\ntitle: \"Seo\"\nstatus: \"" + status + "\"\nsystem: \"SEO\"\n---\n\nBody.\n"
		os.WriteFile(filepath.Join(proj.IssueDir, "seo.md"), []byte(body), 0o644)
	}
	seo("backlog")
	tick(t, proj, "--observe", "--department", "marketing")
	seo("in progress")
	writeIssue(t, proj, "low", "done")
	if _, err := outputs.Add(proj, outputs.AddInput{Department: "marketing", Title: "Launch post", Kind: "post", URL: "https://example.com/p"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := outputs.Add(proj, outputs.AddInput{Department: "product", Title: "Spec", Kind: "doc", URL: "https://example.com/s"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	out := tick(t, proj, "--observe", "--department", "marketing")
	assertContains(t, out, "status: seo backlog → in progress")
	assertContains(t, out, `shelf: Marketing "Launch post"`)
	assertNotContains(t, out, "low")
	assertNotContains(t, out, "Spec")
	if out := tick(t, proj, "--observe", "--department", "marketing"); out != "" {
		t.Fatalf("said twice:\n%s", out)
	}

	ctx, _, _ := newTestContext(proj, false)
	if err := runLead(ctx, []string{"tick", "--observe", "--department", "nope"}); err == nil {
		t.Fatal("unknown department: no error")
	}
	if err := runLead(ctx, []string{"tick", "--department", "marketing"}); err == nil {
		t.Fatal("--department without --observe: no error")
	}
}

// writeLeadTranscript puts a transcript named lead under the project root's
// Claude Code folder in dir, its last turn context tokens.
func writeLeadTranscript(t *testing.T, dir, root, lead string, context int64) {
	t.Helper()
	folder := filepath.Join(dir, regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(root, "-"))
	if fs := bench.TranscriptFolders(dir, root); len(fs) == 1 {
		folder = fs[0]
	}
	os.MkdirAll(folder, 0o755)
	lines := fmt.Sprintf(`{"type":"agent-name","agentName":%q}`+"\n"+
		`{"type":"assistant","timestamp":"2026-11-15T09:00:00Z","message":{"id":"m1","model":"claude-test","usage":{"input_tokens":5,"cache_read_input_tokens":%d,"cache_creation_input_tokens":0,"output_tokens":9}}}`+"\n", lead, context)
	if err := os.WriteFile(filepath.Join(folder, lead+".jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Above 300k the tick says rotate once a local day; under it, nothing.
func TestLeadTickContextNudgeOnceADay(t *testing.T) {
	proj, _, _ := tickProject(t)
	stubStart(t)
	dir := t.TempDir()
	orig := leadContextDir
	leadContextDir = func() string { return dir }
	t.Cleanup(func() { leadContextDir = orig })
	root := projectRoot(proj)
	clock := time.Now()
	tickAt := func(args ...string) string {
		ctx, stdout, _ := newTestContext(proj, false)
		ctx.Now = func() time.Time { return clock }
		if err := runLead(ctx, append([]string{"tick"}, args...)); err != nil {
			t.Fatal(err)
		}
		return stdout.String()
	}

	writeLeadTranscript(t, dir, root, tracker.LeadSessionName(proj), 250_000)
	tickAt()
	if out := tickAt(); strings.Contains(out, "lead context") {
		t.Fatalf("under the line:\n%s", out)
	}
	writeLeadTranscript(t, dir, root, tracker.LeadSessionName(proj), 412_000)
	assertContains(t, tickAt(), "lead context 412k: rotate ("+tracker.LeadSessionName(proj)+"): issue-cli --project "+proj.Slug+" lead rotate")
	if _, err := os.Stat(leadstate.Path(tracker.LeadSessionName(proj))); err != nil {
		t.Fatalf("the tick didn't render the state file: %v", err)
	}
	if out := tickAt(); strings.Contains(out, "lead context") {
		t.Fatalf("said twice in a day:\n%s", out)
	}
	clock = clock.AddDate(0, 0, 1)
	assertContains(t, tickAt(), "lead context 412k")

	// A department lead's observe tick reads its own transcript.
	writeLeadTranscript(t, dir, root, tracker.DepartmentLeadName(proj, "marketing"), 333_000)
	proj.Departments = []tracker.Department{{Name: "Marketing"}}
	assertContains(t, tickAt("--observe", "--department", "marketing"), "lead context 333k: rotate ("+tracker.DepartmentLeadName(proj, "marketing"))
}

// With --exit-on-news a status move nobody waits on and an ended worker are
// no news: the watch goes on, and a move the owner must act on (parked)
// exits with them printed above it.
func TestLeadTickObserveExitOnNewsHoldsPlainMoves(t *testing.T) {
	proj, _, _ := tickProject(t)
	stubStart(t)
	writeIssue(t, proj, "base", "backlog")
	writeIssue(t, proj, "other", "backlog")
	live := map[string]bool{"agent-base": true}
	origLive := liveSessionNames
	liveSessionNames = func(*tracker.Project) map[string]bool { return live }
	t.Cleanup(func() { liveSessionNames = origLive })
	tick(t, proj, "--observe") // the baseline

	ticks := 0
	orig := leadTickSleep
	leadTickSleep = func(time.Duration) bool {
		ticks++
		switch ticks {
		case 1:
			delete(live, "agent-base")
		case 2:
			writeIssue(t, proj, "other", "parked")
		default:
			t.Errorf("tick %d: should have exited on the parked move", ticks)
			return false
		}
		return true
	}
	t.Cleanup(func() { leadTickSleep = orig })
	writeIssue(t, proj, "base", "in progress")
	out := tick(t, proj, "--watch", "--exit-on-news", "--observe")
	if ticks != 2 {
		t.Fatalf("%d sleeps, output:\n%s", ticks, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	want := []string{"status: base backlog → in progress", "ended: agent-base (base at in progress)", "status: other backlog → parked (parked)"}
	if len(lines) != len(want) {
		t.Fatalf("got:\n%s", out)
	}
	for i := range want {
		assertContains(t, lines[i], want[i])
	}
}
