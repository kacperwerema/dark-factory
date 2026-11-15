package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// leadCommentsProject is the YOLO test project with one issue, "work", in
// progress, moved there at moved, run as its worker. comments are written
// as given, oldest first.
func leadCommentsProject(t *testing.T, moved time.Time, comments ...tracker.Comment) *tracker.Project {
	t.Helper()
	proj := makeYOLOProject(t)
	path := filepath.Join(proj.IssueDir, "work.md")
	body := "---\ntitle: \"Work\"\nstatus: \"in progress\"\nassignee: \"agent-work\"\n---\n\nBody of Work.\n"
	for i := range comments {
		comments[i].ID = i + 1
		if comments[i].Status == "" {
			comments[i].Status = "open"
		}
	}
	if err := os.WriteFile(path, []byte(body+tracker.SerializeComments(comments)), 0o644); err != nil {
		t.Fatal(err)
	}
	if !moved.IsZero() {
		if err := tracker.SaveStats(path, tracker.StatsStore{Transitions: []tracker.TransitionStat{{From: "backlog", To: "in progress", TS: moved}}}); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "work")
	return proj
}

func rfcAt(t time.Time) string { return t.Format(time.RFC3339) }

func TestTransitionShowsNewLeadComments(t *testing.T) {
	moved := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	proj := leadCommentsProject(t, moved,
		tracker.Comment{Date: rfcAt(moved.Add(time.Hour)), Text: "lead: also cover the --json path", Author: "lead-proj"},
		tracker.Comment{Date: rfcAt(moved.Add(2 * time.Hour)), Text: "progress: noted", Author: "agent-work"},
		tracker.Comment{Date: rfcAt(moved.Add(3 * time.Hour)), Text: "Lead: and the docs page\nsecond line", Author: "lead-proj"},
	)

	out, _, err := runYOLOTransition(t, proj, moved.Add(4*time.Hour), "work", "--to", "human-testing", "--dry-run")
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	head, _, found := strings.Cut(out, "✓ Ready")
	if !found {
		t.Fatalf("no ready line:\n%s", out)
	}
	for _, want := range []string{leadCommentsHeading, "#1 2026-11-14 lead-proj: lead: also cover the --json path", "#3 2026-11-14 lead-proj: Lead: and the docs page", "      second line"} {
		if !strings.Contains(head, want) {
			t.Fatalf("output before the result lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "progress: noted") {
		t.Fatalf("a non-lead comment was printed:\n%s", out)
	}

	// The real move prints the same block, warns on the unanswered one and
	// still moves: nothing refuses over a lead comment.
	out, _, err = runYOLOTransition(t, proj, moved.Add(4*time.Hour), "work", "--to", "human-testing")
	if err != nil {
		t.Fatalf("real transition refused: %v\n%s", err, out)
	}
	if !strings.HasPrefix(out, leadCommentsHeading) {
		t.Fatalf("real transition doesn't start with the lead block:\n%s", out)
	}
	if !strings.Contains(out, "⚠ lead comment #3 has no reply after it") || strings.Contains(out, "lead comment #1 has no reply") {
		t.Fatalf("want a warning for #3 only:\n%s", out)
	}
	if issue := loadLeadIssue(t, proj, "work"); issue.Status != "human-testing" {
		t.Fatalf("status = %q", issue.Status)
	}

	// The doc says where a comment after dispatch reaches the worker.
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "patterns", "lead-session.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "reaches the worker at its next transition") || !strings.Contains(string(doc), strings.Trim(leadCommentsHeading, "= ")) {
		t.Fatal("docs/patterns/lead-session.md doesn't say a comment after dispatch reaches the worker at its next transition, under the block's heading")
	}
}

func TestTransitionSkipsSeenLeadComments(t *testing.T) {
	moved := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	proj := leadCommentsProject(t, moved,
		tracker.Comment{Date: rfcAt(moved.Add(-time.Hour)), Text: "lead: seen before the move", Author: "lead-proj"},
		tracker.Comment{Date: "2026-11-14", Text: "lead: day-only, same day as the move", Author: "lead-proj"},
	)
	out, _, err := runYOLOTransition(t, proj, moved.Add(time.Hour), "work", "--to", "human-testing", "--dry-run")
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if strings.Contains(out, leadCommentsHeading) || strings.Contains(out, "seen before") || strings.Contains(out, "day-only") {
		t.Fatalf("comments older than the last move printed again:\n%s", out)
	}

	// After a real move the window starts again: a comment printed at that
	// move is not printed at the next one.
	fresh := moved.Add(30 * time.Minute)
	proj = leadCommentsProject(t, moved, tracker.Comment{Date: rfcAt(fresh), Text: "lead: new scope", Author: "lead-proj"})
	if out, _, err := runYOLOTransition(t, proj, moved.Add(time.Hour), "work", "--to", "human-testing"); err != nil || !strings.Contains(out, "lead: new scope") {
		t.Fatalf("first move: err %v\n%s", err, out)
	}
	out, _, _ = runYOLOTransition(t, proj, time.Now(), "work", "--to", "documentation", "--dry-run")
	if strings.Contains(out, "lead: new scope") {
		t.Fatalf("a comment shown at the last move printed again:\n%s", out)
	}
}

func TestTransitionLeadCommentsWindow(t *testing.T) {
	// No move recorded: every lead comment counts (the dispatch prompt
	// carries no comments). The caller's own comments never print.
	proj := leadCommentsProject(t, time.Time{},
		tracker.Comment{Date: "2026-11-01", Text: "lead: before dispatch", Author: "lead-proj"},
		tracker.Comment{Date: rfcAt(time.Now()), Text: "lead: my own note", Author: "agent-work"},
	)
	out, _, err := runYOLOTransition(t, proj, time.Now(), "work", "--to", "human-testing", "--dry-run")
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "lead: before dispatch") || strings.Contains(out, "my own note") {
		t.Fatalf("want every lead comment but the caller's own:\n%s", out)
	}
}

func TestTransitionJSONLeadComments(t *testing.T) {
	moved := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	proj := leadCommentsProject(t, moved,
		tracker.Comment{Date: rfcAt(moved.Add(time.Hour)), Text: "lead: json scope", Author: "lead-proj"},
	)
	for _, args := range [][]string{{"--dry-run"}, {}} {
		ctx, out, _ := newTestContext(proj, true)
		ctx.Now = func() time.Time { return moved.Add(2 * time.Hour) }
		if err := runTransition(ctx, append([]string{"work", "--to", "human-testing"}, args...)); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		var got struct {
			LeadComments []leadComment `json:"lead_comments"`
		}
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("%v: stdout is not one JSON value: %v\n%s", args, err, out)
		}
		if len(got.LeadComments) != 1 || got.LeadComments[0].Text != "lead: json scope" || got.LeadComments[0].Author != "lead-proj" || got.LeadComments[0].Answered {
			t.Fatalf("%v: lead_comments = %+v", args, got.LeadComments)
		}
	}
}
