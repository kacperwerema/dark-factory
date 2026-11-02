package viewer

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// openTestChallenge records challenger's challenge on ref as its worker.
func openTestChallenge(t *testing.T, proj tracker.Project, ref, challenger, why string) {
	t.Helper()
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	w := tracker.ChallengeWrite{
		IssueDir: proj.IssueDir, Actor: tracker.DecisionActor{Name: "agent-x", Role: tracker.ActorAgent}, Author: "agent-x",
		File: func(slug string) string {
			if is := tracker.FindIssue(issues, slug); is != nil {
				return is.FilePath
			}
			return ""
		},
	}
	if err := tracker.OpenChallenge(w, tracker.Challenge{Ref: ref, Issue: challenger, Why: why, Propose: "a new rule"}, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func challengeRows(rows []WbQueueCard) []WbQueueCard {
	var out []WbQueueCard
	for _, r := range rows {
		if r.Kind == "challenge" {
			out = append(out, r)
		}
	}
	return out
}

func projectRows(t *testing.T, proj tracker.Project) []WbQueueCard {
	t.Helper()
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	var open []*tracker.Issue
	closed := tracker.ClosedIssueFunc(&proj)
	for _, is := range issues {
		if !closed(is) {
			open = append(open, is)
		}
	}
	return needsYouRows(&proj, "", "/p/"+proj.Slug, open, time.Now())
}

// AC2, AC3, AC6: an open challenge is one Needs you row (the count grows by
// exactly one), names the open issue that cites the decision without
// holding it; Keep it without the owner's sign-in is 403 and writes
// nothing; the owner's Keep it closes it and the row goes.
func TestChallengeIsOneNeedsYouRow(t *testing.T) {
	proj := setupDecisionsProject(t)
	withMockTmuxSessions(t, nil)
	before := len(projectRows(t, proj))

	openTestChallenge(t, proj, "sim/pivot#D-1", "shop", "the raid needs a timer")
	rows := projectRows(t, proj)
	if len(rows) != before+1 {
		t.Fatalf("needsYouRows %d → %d, want +1", before, len(rows))
	}
	ch := challengeRows(rows)
	if len(ch) != 1 {
		t.Fatalf("challenge rows = %+v", ch)
	}
	r := ch[0]
	if r.Slug != "shop" || r.ResolveRef != "sim/pivot#D-1" || r.ResolveURL != "/p/wb/decisions/challenge/resolve" ||
		!strings.Contains(r.Question, "the raid needs a timer") || !strings.Contains(r.Decision, "ET-style") {
		t.Fatalf("row = %+v", r)
	}
	if len(r.Also) != 1 || r.Also[0] != "sim/creeps (cites it)" {
		t.Fatalf("also = %q", r.Also)
	}

	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	_, html := getBody(t, ts.URL+"/p/wb/needs-you")
	assertContains(t, html, `data-kind="challenge" data-slug="shop"`, `data-testid="needs_you.challenge.keep"`, "Also on it, not held: sim/creeps (cites it)")

	snap := snapshotIssues(t, proj.IssueDir)
	logBefore, _ := os.ReadFile(tracker.DecisionLogPath(proj.IssueDir))
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/p/wb/decisions/challenge/resolve", strings.NewReader(`{"ref":"sim/pivot#D-1","issue":"shop","text":"keep"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(strangerHeader, "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("Keep it without sign-in: %d, want 403", resp.StatusCode)
	}
	logAfter, _ := os.ReadFile(tracker.DecisionLogPath(proj.IssueDir))
	if !bytes.Equal(snap, snapshotIssues(t, proj.IssueDir)) || !bytes.Equal(logBefore, logAfter) {
		t.Fatal("a refused Keep it wrote something")
	}

	code, out := postJSON(t, ts.URL+"/p/wb/decisions/challenge/resolve", `{"ref":"sim/pivot#D-1","issue":"shop","text":"keep the raid"}`)
	if code != 200 || out["resolved"] != float64(1) {
		t.Fatalf("Keep it: %d %v", code, out)
	}
	if got := len(projectRows(t, proj)); got != before {
		t.Fatalf("rows after Keep it = %d, want %d", got, before)
	}
	cs, _ := tracker.LoadComments(filepath.Join(proj.IssueDir, "shop.md"))
	if last := cs[len(cs)-1]; last.Text != `challenge-resolved: sim/pivot#D-1 kept, "keep the raid" — owner` || last.Author != tracker.OwnerAuthor {
		t.Fatalf("last comment = %+v", last)
	}
	evs, _ := tracker.ReadDecisionLog(proj.IssueDir)
	if last := evs[len(evs)-1]; last.Event != tracker.DecisionResolved || last.Role != tracker.ActorOwnerBrowser {
		t.Fatalf("last event = %+v", last)
	}
	code, _ = postJSON(t, ts.URL+"/p/wb/decisions/challenge/resolve", `{"ref":"sim/pivot#D-1"}`)
	if code != http.StatusConflict {
		t.Fatalf("Keep it with none open: %d", code)
	}
}

// AC6: an open issue whose work branch touches the challenged decision's
// paths: is listed on the row (and not held: the rule reads only the
// challenger); the merge-base diff ignores what main changed.
func TestChallengeRowListsBranchesTouchingThePaths(t *testing.T) {
	proj := setupDecisionsProject(t)
	withMockTmuxSessions(t, nil)
	root := filepath.Dir(proj.IssueDir)
	proj.WorkDir = root
	pivot := filepath.Join(proj.IssueDir, "Sim", "pivot.md")
	data, _ := os.ReadFile(pivot)
	if err := os.WriteFile(pivot, []byte(strings.Replace(string(data), "(replaces sim/phases#D-1)", "(replaces sim/phases#D-1) {hard; paths: src/raid/**}", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWorkbenchIssue(t, proj.IssueDir, "raid", "in progress", "", "Raid timer work.")
	writeWorkbenchIssue(t, proj.IssueDir, "other", "in progress", "", "Unrelated.")

	gitRun(t, root, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(root, "src", "raid", "timer.go"), "package raid\n")
	writeFile(t, filepath.Join(root, "README"), "x\n")
	gitRun(t, root, "add", "-A")
	gitRun(t, root, "commit", "-q", "-m", "base")
	gitRun(t, root, "checkout", "-q", "-b", "work/raid")
	writeFile(t, filepath.Join(root, "src", "raid", "timer.go"), "package raid\n// changed\n")
	gitRun(t, root, "commit", "-q", "-am", "raid change")
	gitRun(t, root, "checkout", "-q", "main")
	gitRun(t, root, "checkout", "-q", "-b", "work/other")
	writeFile(t, filepath.Join(root, "README"), "y\n")
	gitRun(t, root, "commit", "-q", "-am", "readme")
	gitRun(t, root, "checkout", "-q", "main")
	// main moves on under src/raid: the other branch's merge-base diff
	// still doesn't touch it.
	writeFile(t, filepath.Join(root, "src", "raid", "boss.go"), "package raid\n")
	gitRun(t, root, "add", "-A")
	gitRun(t, root, "commit", "-q", "-m", "main moves")

	openTestChallenge(t, proj, "sim/pivot#D-1", "shop", "the raid needs a timer")
	ch := challengeRows(projectRows(t, proj))
	if len(ch) != 1 {
		t.Fatalf("challenge rows = %+v", ch)
	}
	got := strings.Join(ch[0].Also, " | ")
	if got != "raid (branch touches src/raid/timer.go) | sim/creeps (cites it)" {
		t.Fatalf("also = %q", got)
	}
}
