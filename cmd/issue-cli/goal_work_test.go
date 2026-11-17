package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// AC2: when the queue is out of work, queue list, queue next, the lead's
// Queue: line and the tick's queue-low line name the goals with no work
// behind them, and leave out the goal an open issue serves.
func TestQueueOutOfWorkNamesGoalsWithNoWork(t *testing.T) {
	proj, _, _ := tickProject(t)
	stubStart(t)
	proj.Company = &tracker.Company{Goals: []tracker.Goal{{ID: "clients", Name: "Paying clients"}, {ID: "revenue", Name: "Revenue"}}}
	body := "---\ntitle: \"case-studies\"\nstatus: \"idea\"\ngoal: \"hq/clients\"\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(proj.IssueDir, "case-studies.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	mustQ(t, proj, "add", "--idea", "a pricing page", "--quote", queueWords)
	mustQ(t, proj, "drop", "1")

	const look = "look first at: hq/revenue (Revenue)"
	list := mustQ(t, proj, "list")
	assertContains(t, list, look)
	assertNotContains(t, list, "hq/clients")
	assertContains(t, mustQ(t, proj, "next"), look)

	ctx, _, _ := newTestContext(proj, false)
	issues, err := proj.LoadIssues()
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, queueLine(ctx, issues), "nothing open: ∞ think up more and add items (issue-cli --project proj queue add); "+look)
	assertContains(t, tick(t, proj), "queue: nothing open left: ∞ think up more, then issue-cli --project proj queue add <slug> --quote \"<owner's words>\"; "+look)

	// An idea queued for the goal covers it: the line says nothing more.
	mustQ(t, proj, "add", "--idea", "a pricing page", "--goal", "revenue", "--quote", queueWords)
	assertNotContains(t, mustQ(t, proj, "list"), "look first at")

	// An unknown goal is refused with the keys.
	if _, err := runQ(t, proj, "add", "--idea", "x", "--goal", "nope"); err == nil {
		t.Fatal("queue add --goal nope was accepted")
	}
}

func TestCreateWritesGoal(t *testing.T) {
	proj := newTestProject(t)
	proj.Company = &tracker.Company{Goals: []tracker.Goal{{ID: "clients", Name: "Paying clients"}}}
	ctx, _, _ := testContext(proj)
	if err := runCreate(ctx, []string{"--title", "Case studies", "--goal", "clients"}); err != nil {
		t.Fatal(err)
	}
	issues, err := proj.LoadIssues()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, issue := range issues {
		if issue.Title == "Case studies" {
			found = true
			if got := issue.Field("goal"); got != "hq/clients" {
				t.Errorf("goal: = %q, want hq/clients", got)
			}
		}
	}
	if !found {
		t.Fatal("no issue written")
	}
	ctx, _, _ = testContext(proj)
	if err := runCreate(ctx, []string{"--title", "Other", "--goal", "nope"}); err == nil {
		t.Fatal("create --goal nope was accepted")
	}
}
