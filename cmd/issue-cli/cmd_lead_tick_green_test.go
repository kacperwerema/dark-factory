package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/trace"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// greenProject is a tick project with a shipping status, an issue
// "shipped" at it and work/shipped one commit ahead of main; it returns
// the main checkout and the branch head's commit time.
func greenProject(t *testing.T, extraWorkflow string) (*tracker.Project, string, time.Time) {
	t.Helper()
	proj, _, _ := tickProject(t)
	stubStart(t)
	root := filepath.Dir(proj.IssueDir)
	wf := "statuses:\n  - name: \"backlog\"\n  - name: \"in progress\"\n  - name: \"shipping\"\n  - name: \"done\"\n" + extraWorkflow
	if err := os.WriteFile(proj.WorkflowFile, []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	writeIssue(t, proj, "shipped", "shipping")
	_, tip := queueShipRepo(t, root, "main", "shipped")
	sec, err := strconv.ParseInt(queueGit(t, root, "log", "-1", "--format=%ct", tip), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return proj, root, time.Unix(sec, 0)
}

// tickAt runs one lead tick with the clock at now.
func tickAt(t *testing.T, proj *tracker.Project, now time.Time) string {
	t.Helper()
	ctx, stdout, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return now }
	if err := runLead(ctx, []string{"tick"}); err != nil {
		t.Fatalf("lead tick: %v\n%s", err, stdout.String())
	}
	return stdout.String()
}

// mergeLines is out's merge: lines.
func mergeLines(out string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "merge: ") {
			lines = append(lines, line)
		}
	}
	return lines
}

// A branch whose issue is at shipping, not on main, its head 40 minutes
// old: one news line naming the slug, the age and the merge command; the
// tick merges nothing.
func TestLeadTickNamesAGreenBranchWaitingToMerge(t *testing.T) {
	proj, root, head := greenProject(t, "")
	before := queueGit(t, root, "rev-parse", "main")
	lines := mergeLines(tickAt(t, proj, head.Add(40*time.Minute)))
	if len(lines) != 1 {
		t.Fatalf("want one merge: line, got %q", lines)
	}
	want := "merge: shipped is shipping and work/shipped has waited 40m unmerged (since " + head.Local().Format("15:04") + "): git -C "
	if !strings.HasPrefix(lines[0], want) || !strings.HasSuffix(lines[0], " merge --ff-only work/shipped") {
		t.Fatalf("line = %q\nwant %q… merge --ff-only work/shipped", lines[0], want)
	}
	if after := queueGit(t, root, "rev-parse", "main"); after != before {
		t.Fatalf("the tick moved main %s → %s: the merge is the lead's", before, after)
	}

	// Main moved on: the command is the conventional merge, not a fast-forward.
	os.WriteFile(filepath.Join(root, "c.txt"), []byte("c"), 0o644)
	queueGit(t, root, "add", "c.txt")
	queueGit(t, root, "commit", "-q", "-m", "feat: other")
	cmd := greenMergeCommand(root, "main", "work/shipped")
	if !strings.HasSuffix(cmd, `merge --no-ff work/shipped -m "chore(merge): work/shipped into main"`) {
		t.Fatalf("diverged command = %q", cmd)
	}
}

// Ten minutes old, on main already, at another status, or just moved into
// shipping (its trace): nothing. leads.green_branch_minutes moves the line.
func TestLeadTickQuietForYoungOrMergedBranch(t *testing.T) {
	proj, root, head := greenProject(t, "")
	if lines := mergeLines(tickAt(t, proj, head.Add(10*time.Minute))); len(lines) != 0 {
		t.Fatalf("10 minutes old: %q", lines)
	}

	// Moved into shipping 5 minutes ago: it waits from there.
	path := trace.Path(proj.IssueDir, "shipped")
	if err := trace.Write(path, []trace.Span{{Status: "shipping", From: head.Add(55 * time.Minute), Open: true}}); err != nil {
		t.Fatal(err)
	}
	if lines := mergeLines(tickAt(t, proj, head.Add(time.Hour))); len(lines) != 0 {
		t.Fatalf("5 minutes at shipping: %q", lines)
	}
	os.Remove(path)

	// Still in progress: not done at its work.
	writeIssue(t, proj, "shipped", "in progress")
	if lines := mergeLines(tickAt(t, proj, head.Add(2*time.Hour))); len(lines) != 0 {
		t.Fatalf("in progress: %q", lines)
	}
	writeIssue(t, proj, "shipped", "shipping")

	// On main: nothing.
	queueGit(t, root, "merge", "-q", "--ff-only", "work/shipped")
	if lines := mergeLines(tickAt(t, proj, head.Add(2*time.Hour))); len(lines) != 0 {
		t.Fatalf("on main: %q", lines)
	}

	// A longer threshold keeps a 40-minute branch quiet.
	proj, _, head = greenProject(t, "leads:\n  green_branch_minutes: 60\n")
	if lines := mergeLines(tickAt(t, proj, head.Add(40*time.Minute))); len(lines) != 0 {
		t.Fatalf("40m under a 60m threshold: %q", lines)
	}
	if lines := mergeLines(tickAt(t, proj, head.Add(61*time.Minute))); len(lines) != 1 {
		t.Fatalf("61m over a 60m threshold: %q", lines)
	}
}

// The line is news once an hour per branch, not every tick.
func TestLeadTickGreenBranchLineOncePerHour(t *testing.T) {
	proj, _, head := greenProject(t, "")
	start := head.Add(40 * time.Minute)
	if lines := mergeLines(tickAt(t, proj, start)); len(lines) != 1 {
		t.Fatalf("first tick: %q", lines)
	}
	for _, d := range []time.Duration{5 * time.Minute, 30 * time.Minute, 59 * time.Minute} {
		if lines := mergeLines(tickAt(t, proj, start.Add(d))); len(lines) != 0 {
			t.Fatalf("again after %s: %q", d, lines)
		}
	}
	lines := mergeLines(tickAt(t, proj, start.Add(time.Hour)))
	if len(lines) != 1 || !strings.Contains(lines[0], "has waited 1h40m unmerged") {
		t.Fatalf("after an hour: %q", lines)
	}
	if lines := mergeLines(tickAt(t, proj, start.Add(time.Hour+5*time.Minute))); len(lines) != 0 {
		t.Fatalf("right after the repeat: %q", lines)
	}
}
