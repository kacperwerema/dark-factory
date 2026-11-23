package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/clibug"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

// withPrivateEngine makes the fixture's engine a dark-factory checkout (the
// workflow catalog is at its root, so create cleans private names out of
// slugs) and puts name on the private-names list.
func withPrivateEngine(t *testing.T, f *reportBugFixture, name string) {
	t.Helper()
	catalog := filepath.Join(filepath.Dir(f.engineDir), filepath.FromSlash(workflowupgrade.CatalogPath))
	if err := os.MkdirAll(catalog, 0755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(catalog, "stand-in.yaml"), []byte("id: stand-in\n"), 0644)
	cfg := filepath.Join(f.dir, "xdg-config")
	os.MkdirAll(filepath.Join(cfg, "ai-workflow-engine"), 0755)
	os.WriteFile(filepath.Join(cfg, "ai-workflow-engine", "private-names.txt"), []byte(name+"\n"), 0644)
	t.Setenv("XDG_CONFIG_HOME", cfg)
}

// A report from another project's worker whose title names a private
// project: create leaves the name out of the slug, and report-bug used to
// look the new issue up under the slug with the name in it, queue the
// report and leave a bare issue behind.
func TestReportBugPrivateNameInTitleFilesOnTheEngineBoard(t *testing.T) {
	f := newReportBugFixture(t, "", engineEntry)
	withPrivateEngine(t, f, "kestrel")

	out := f.run(t, false, "worktree add (kestrel) left no root node_modules link in the worktree. Fixed by hand.")
	if strings.Contains(out, "saved locally") {
		t.Fatalf("report was queued, not filed:\n%s", out)
	}
	if n := clibug.PendingCount(); n != 0 {
		t.Fatalf("queue = %d, want 0", n)
	}
	issues := f.cliBugs(t)
	if len(issues) != 1 {
		t.Fatalf("engine issues = %d, want 1", len(issues))
	}
	issue := issues[0]
	if strings.Contains(issue.Slug, "kestrel") {
		t.Errorf("slug %q keeps the private name", issue.Slug)
	}
	if !hasLabel(issue, clibug.Label) || !strings.Contains(issue.BodyRaw, "## Report") {
		t.Errorf("issue is bare: labels %v, body:\n%s", issue.Labels, issue.BodyRaw)
	}
	if !strings.Contains(out, "✓ Reported as new cli-bug issue "+issue.Slug) {
		t.Errorf("output does not name %s:\n%s", issue.Slug, out)
	}

	// The same report again joins it as a report: comment.
	out = f.run(t, false, "worktree add (kestrel) left no root node_modules link in the worktree")
	if !strings.Contains(out, "Already reported") || len(f.cliBugs(t)) != 1 {
		t.Fatalf("repeat did not comment:\n%s", out)
	}
}

// The incident: queued reports whose slugs are taken by parked or done
// issues (the bare issues the old bug left, or a closed cli-bug issue) are
// all sent by one --send, each as an (again) issue naming the one in the
// way (the latest one), and nothing stays queued.
func TestReportBugSendFilesReportsWhoseSlugIsTakenByAClosedIssue(t *testing.T) {
	f := newReportBugFixture(t, "", engineEntry)
	withPrivateEngine(t, f, "kestrel")
	cli := filepath.Join(f.engineDir, "CLI")
	os.MkdirAll(cli, 0755)
	// A bare parked issue at the slug create gives the first report.
	os.WriteFile(filepath.Join(cli, "worktree-add-left-no-root-link.md"), []byte("---\ntitle: \"worktree add (kestrel) left no root link\"\nslug: \"worktree-add-left-no-root-link\"\nstatus: \"parked\"\nsystem: \"CLI\"\ntype: \"bugfix\"\n---\n"), 0644)
	// A done cli-bug issue with the second report's title, and its done
	// (again) issue.
	for _, name := range []string{"queue-drain-stops", "queue-drain-stops-again"} {
		title := "Queue drain stops"
		if strings.HasSuffix(name, "again") {
			title += " (again)"
		}
		os.WriteFile(filepath.Join(cli, name+".md"), []byte("---\ntitle: \""+title+"\"\nstatus: \"done\"\nsystem: \"CLI\"\ntype: \"bugfix\"\nlabels: [\"cli-bug\"]\n---\n"), 0644)
	}
	at := time.Now().Add(-time.Hour)
	clibug.Enqueue(clibug.Report{At: at, Text: "worktree add (kestrel) left no root link", Project: "kestrel"})
	clibug.Enqueue(clibug.Report{At: at.Add(time.Minute), Text: "Queue drain stops", Project: "kestrel"})
	clibug.Enqueue(clibug.Report{At: at.Add(2 * time.Minute), Text: "A third, unrelated report", Project: "kestrel"})

	out := f.run(t, false, "--send")
	if !strings.Contains(out, "✓ Sent 3 queued report(s)") {
		t.Fatalf("--send output:\n%s", out)
	}
	if n := clibug.PendingCount(); n != 0 {
		t.Fatalf("queue = %d after --send, want 0\n%s", n, out)
	}
	byTitle := map[string]*tracker.Issue{}
	for _, issue := range f.cliBugs(t) {
		byTitle[issue.Title] = issue
	}
	for title, names := range map[string]string{
		"worktree add (kestrel) left no root link (again)": "cli/worktree-add-left-no-root-link",
		"Queue drain stops (again 2)":                      "cli/queue-drain-stops-again",
		"A third, unrelated report":                        "",
	} {
		issue := byTitle[title]
		if issue == nil {
			t.Errorf("no issue %q on the board", title)
			continue
		}
		if !hasLabel(issue, clibug.Label) || !strings.Contains(issue.BodyRaw, "## Report") {
			t.Errorf("%s is bare: labels %v", issue.Slug, issue.Labels)
		}
		if names != "" && !strings.Contains(issue.BodyRaw, "`"+names+"`") {
			t.Errorf("%s does not name %s:\n%s", issue.Slug, names, issue.BodyRaw)
		}
	}
}

// failingSender fails the reports whose text is in fail and files the rest.
type failingSender struct {
	fail map[string]bool
	sent []string
}

func (s *failingSender) Send(r clibug.Report) (clibug.Result, error) {
	if s.fail[r.Text] {
		return clibug.Result{}, errors.New("board refused " + r.Text)
	}
	s.sent = append(s.sent, r.Text)
	return clibug.Result{Kind: "created", Slug: "cli/" + tracker.Slugify(r.Text)}, nil
}

// One queued report that fails does not hold back the ones after it; it
// stays queued, and the reason names it.
func TestReportBugQueueDrainGoesPastAFailingReport(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	at := time.Now().Add(-time.Hour)
	for i, text := range []string{"first fails", "second goes", "third goes"} {
		if _, err := clibug.Enqueue(clibug.Report{At: at.Add(time.Duration(i) * time.Minute), Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	s := &failingSender{fail: map[string]bool{"first fails": true}}
	sent, why := drainQueue(s, nil)
	if len(sent) != 2 || strings.Join(s.sent, ",") != "second goes,third goes" {
		t.Fatalf("sent = %v, want the second and third", sent)
	}
	if !strings.Contains(why, "board refused first fails") {
		t.Errorf("reason = %q, want the first report's error", why)
	}
	left := clibug.Pending()
	if len(left) != 1 || left[0].Report.Text != "first fails" {
		t.Fatalf("still queued = %+v, want only the failing one", left)
	}
}

// A report that just failed to send is queued, and the older queued reports
// are still tried in the same call.
func TestReportBugQueueDrainRunsAfterTheNewReportIsQueued(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	clibug.Enqueue(clibug.Report{At: time.Now().Add(-time.Hour), Text: "older goes"})
	s := &failingSender{fail: map[string]bool{"new fails": true}}
	current := deliverReport(s, "", clibug.Report{At: time.Now(), Text: "new fails"})
	if current.Kind != "queued" {
		t.Fatalf("new report = %+v, want queued", current)
	}
	sent, _ := drainQueue(s, &current)
	if len(sent) != 1 || strings.Join(s.sent, ",") != "older goes" {
		t.Fatalf("sent = %v (sender saw %v), want only the older report", sent, s.sent)
	}
	if left := clibug.Pending(); len(left) != 1 || left[0].Path != current.Queued {
		t.Fatalf("still queued = %+v, want the new report only", left)
	}
}
