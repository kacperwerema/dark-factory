package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// signedComment signs a comment on slug's file as author.
func signedComment(t *testing.T, proj *tracker.Project, slug, text, author string) {
	t.Helper()
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	is, ok := issueExists(issues, slug)
	if !ok {
		t.Fatalf("no issue %s", slug)
	}
	if err := tracker.AddCommentBy(is.FilePath, 0, text, "cli", author); err != nil {
		t.Fatal(err)
	}
}

// newsTick is one lead tick --watch --exit-on-news that must exit on its
// first tick: a watch that sleeps found no news.
func newsTick(t *testing.T, proj *tracker.Project) string {
	t.Helper()
	orig := leadTickSleep
	slept := false
	leadTickSleep = func(time.Duration) bool { slept = true; return false }
	defer func() { leadTickSleep = orig }()
	out := tick(t, proj, "--watch", "--exit-on-news")
	if slept {
		t.Fatalf("--exit-on-news found no news and slept:\n%s", out)
	}
	return out
}

// 2026-11-18: the queue-low sweep's report: landed at 02:01 and the tick
// under --exit-on-news printed nothing; the queue ran dry. The report is
// news, said once per report: comment.
func TestLeadTickSaysASweepsReportWhenItLands(t *testing.T) {
	proj := sweepProject(t)
	autostartOnFor(t, proj)
	yoloOnFor(t, proj)
	mustQ(t, proj, "add", "high", "--quote", queueWords)

	tick(t, proj)
	slug := sweepItems(t, proj)[0].Slug
	if out := tick(t, proj); out != "" {
		t.Fatalf("a tick before the report printed:\n%s", out)
	}
	signedComment(t, proj, slug, "report: o-20261118-3f16, 14 causes, 6 proposals", "agent-"+strings.ReplaceAll(slug, "/", "-"))

	out := newsTick(t, proj)
	assertContains(t, out, "report: "+slug+": o-20261118-3f16, 14 causes, 6 proposals: file its proposals")
	if again := tick(t, proj); strings.Contains(again, "report: "+slug) {
		t.Fatalf("the report was said twice:\n%s", again)
	}

	// A second report: comment is said again.
	signedComment(t, proj, slug, "report: o-20261118-a1b2, revised", "agent-x")
	assertContains(t, tick(t, proj), "report: "+slug+": o-20261118-a1b2, revised")
}

// 402 at 00:31: a worker stopped on a question: comment and held its slot
// 7 h with no tick line. A worker's open question is news, once.
func TestLeadTickSaysAWorkersQuestion(t *testing.T) {
	proj, _, _ := tickProject(t)
	writeIssue(t, proj, "asks", "in progress")
	writeIssue(t, proj, "answered", "in progress")
	writeIssue(t, proj, "finished", "done")
	tick(t, proj)

	signedComment(t, proj, "asks", "question: which flag name, --all or --every?", "agent-asks")
	signedComment(t, proj, "answered", "question: ok to rename?", "agent-answered")
	signedComment(t, proj, "answered", "answer: yes", "lead-proj")
	signedComment(t, proj, "finished", "question: anything else?", "agent-finished")

	out := newsTick(t, proj)
	assertContains(t, out, "question: asks: which flag name, --all or --every?")
	assertNotContains(t, out, "answered:")
	assertNotContains(t, out, "finished:")
	if again := tick(t, proj); strings.Contains(again, "question: asks") {
		t.Fatalf("the question was said twice:\n%s", again)
	}
}

// The lead's guide lists both lines under what counts as news, and the
// shape of the lead's follow-ups-filed comment the tick reads.
func TestLeadTickReportAndQuestionAreNewsInTheDocs(t *testing.T) {
	data, err := os.ReadFile("../../docs/patterns/lead-session.md")
	if err != nil {
		t.Fatal(err)
	}
	_, news, _ := strings.Cut(string(data), "#### What counts as news")
	news, _, _ = strings.Cut(news, "\n#")
	for _, want := range []string{"- `report:` a `report:` comment", "- `question:` an open issue", "`" + leadFollowUpsFiled + "`"} {
		if !strings.Contains(news, want) {
			t.Errorf("What counts as news has no %q", want)
		}
	}
}

// 2026-11-21 09:40: the tick said "has N follow-ups to file" for reports
// whose follow-ups were filed: one by issues naming its output id, one by
// the lead's comment. A filed report, comment or shelf output, is no news;
// an unfiled one still is, and a report after the lead's comment too.
func TestLeadTickSkipsReportsWhoseFollowUpsAreFiled(t *testing.T) {
	proj, _ := nightProject(t)
	stubStart(t)
	write := func(slug, typ, body string) {
		text := "---\ntitle: \"" + slug + "\"\nstatus: \"idea\"\ntype: \"" + typ + "\"\n---\n\n" + body + "\n"
		if err := os.WriteFile(filepath.Join(proj.IssueDir, slug+".md"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("notes-a", "report", "Research it.")
	write("notes-b", "report", "Research it.")
	file := filepath.Join(t.TempDir(), "r.md")
	if err := os.WriteFile(file, []byte("# report\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"notes-a", "notes-b"} {
		if _, err := outputs.Add(proj, outputs.AddInput{Department: "product", Title: "Report " + slug, Kind: "report", File: file, Issue: slug}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	shelf, err := outputs.Load(proj)
	if err != nil {
		t.Fatal(err)
	}
	id := func(slug string) string {
		got := outputs.Select(shelf, outputs.Filter{Issue: slug})
		if len(got) != 1 {
			t.Fatalf("%s has %d outputs", slug, len(got))
		}
		return got[0].ID
	}
	// lead-floor's report names no id: its shelved output is what a
	// follow-up cites. notes-a's output is cited by a follow-up.
	write("floor-follow-up", "feature", "From the sweep report "+id("lead-floor")+", item 1: tick news.")
	write("notes-follow-up", "feature", "From "+id("notes-a")+", item 2.")
	signedComment(t, proj, "payments", "lead: follow-ups filed: 464, 465", "lead-proj")

	out, _ := watchTickNews(t, proj, time.Now())
	for _, slug := range []string{"lead-floor", "payments", "notes-a"} {
		assertNotContains(t, out, "report: "+slug)
	}
	assertContains(t, out, "report: notes-b's output "+id("notes-b"))

	// A report after the lead's comment is news again.
	signedComment(t, proj, "payments", "report: revised. Follow-ups: one more", tracker.AgentSessionName("payments"))
	out, _ = watchTickNews(t, proj, time.Now())
	assertContains(t, out, "report: payments's report has 1 follow-up to file: revised.")
}
