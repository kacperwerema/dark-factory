package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// writeComments puts comment lines (date, text, source, author) under the
// issue at rel.
func writeComments(t *testing.T, p tracker.Project, rel string, lines ...[4]string) {
	t.Helper()
	var comments []tracker.Comment
	for i, l := range lines {
		comments = append(comments, tracker.Comment{ID: i + 1, Date: l[0], Text: l[1], Status: "open", Source: l[2], Author: l[3]})
	}
	if err := tracker.SaveComments(filepath.Join(p.IssueDir, rel), comments); err != nil {
		t.Fatal(err)
	}
}

func TestRadioComments(t *testing.T) {
	p := setupAgentsProject(t)
	now := time.Now()
	at := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	writeComments(t, p, "UI/child.md",
		[4]string{at(3 * time.Hour), "progress: tests green on the branch", "cli", "agent-ui-child"},
		[4]string{at(time.Hour), "Question: which API do we keep?", "cli", "lead-agents"},
		[4]string{at(2 * time.Hour), "approval: \"go\" (in the lead chat) — recorded by lead-agents", "cli", ""},
		[4]string{at(30 * time.Minute), "ci: main red", "ci", ""},
		[4]string{at(8 * 24 * time.Hour), "progress: an old note", "cli", "agent-ui-child"},
	)
	writeComments(t, p, "loner.md",
		[4]string{at(10 * time.Minute), "question: is this still wanted?", "app", tracker.OwnerAuthor},
		[4]string{at(20 * time.Minute), "progress: unsigned from a shell", "cli", ""},
	)
	issues, err := tracker.LoadIssues(p.IssueDir)
	if err != nil {
		t.Fatal(err)
	}

	got := radioComments(&p, "/p/agents", issues, now, 8)
	var lines []string
	for _, m := range got {
		lines = append(lines, fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s", m.From, m.Initials, m.Tone, m.Kind, m.To, m.When, m.Text))
	}
	want := []string{
		"owner|You|you|question|loner|10 m|is this still wanted?",
		"cli|W|agent|progress|loner|20 m|unsigned from a shell",
		"lead-agents|CEO|ceo|question|ui/child|1 h|which API do we keep?",
		"agent-ui-child|W|agent|progress|ui/child|3 h|tests green on the branch",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("radio:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if got[0].URL != "/p/agents/issue/loner" {
		t.Errorf("URL = %q", got[0].URL)
	}
	if n := len(radioComments(&p, "/p/agents", issues, now, 2)); n != 2 {
		t.Errorf("cap: %d lines, want 2", n)
	}

	// A file nobody wrote to this week is not read.
	old := now.Add(-8 * 24 * time.Hour)
	os.Chtimes(filepath.Join(p.IssueDir, "loner.md"), old, old)
	if got := radioComments(&p, "/p/agents", issues, now, 8); len(got) != 2 {
		t.Errorf("after the window: %d lines, want only ui/child's two", len(got))
	}
}

func TestCompanyRadio_CommentsBelowTheCEO(t *testing.T) {
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "⏺ I sent two workers to Product.\n")
	withMockTmuxSessions(t, []AgentSession{{Name: "lead-agents"}})
	writeComments(t, a, "UI/child.md", [4]string{time.Now().Add(-time.Hour).Format(time.RFC3339), "progress: the Radio reads comments", "cli", "agent-ui-child"})
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()

	_, html := getBody(t, ts.URL+"/p/agents/company?classic=1")
	assertContains(t, html,
		`<span class="wb-msg-from">agent-ui-child</span><span>on <a href="/p/agents/issue/ui/child" data-testid="company.radio.issue">ui/child</a></span>`,
		`<span class="wb-msg-kind wb-msg-kind--progress" data-testid="company.radio.kind">progress</span><span class="wb-msg-when">1 h</span>`,
		`the Radio reads comments`,
	)
	if strings.Index(html, "I sent two workers") > strings.Index(html, "the Radio reads comments") {
		t.Error("the CEO's live lines come first")
	}
}

func TestCompanyRadio_Empty(t *testing.T) {
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	_, html := getBody(t, ts.URL+"/p/agents/company?classic=1")
	assertContains(t, html, `and progress: and question: comments on the issues from the last week.`)
}

// The viewer signs its comments as the owner; the JSON reads an older
// comment's recorder as its author.
func TestComments_ViewerSignsOwner(t *testing.T) {
	a := setupAgentsProject(t)
	writeComments(t, a, "UI/child.md", [4]string{"2026-10-01", `dispatch: "go" (lead chat, 2026-10-01) — recorded by lead-agents`, "cli", ""})
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()

	if code, _ := postJSON(t, ts.URL+"/p/agents/issue/ui/child/comments", `{"block":0,"text":"looks good"}`); code != 201 {
		t.Fatalf("add comment: %d", code)
	}
	comments, _ := tracker.LoadComments(filepath.Join(a.IssueDir, "UI/child.md"))
	if last := comments[len(comments)-1]; last.Author != tracker.OwnerAuthor {
		t.Errorf("viewer comment author %q, want owner", last.Author)
	}
	_, body := getBody(t, ts.URL+"/p/agents/issue/ui/child/comments")
	if !strings.Contains(body, `"author":"lead-agents"`) || !strings.Contains(body, `"author":"owner"`) {
		t.Errorf("comments JSON: %s", body)
	}
	data, _ := os.ReadFile(filepath.Join(a.IssueDir, "UI/child.md"))
	if strings.Count(string(data), `"author"`) != 1 {
		t.Errorf("the older line was rewritten:\n%s", data)
	}
}

// The Station's question card names who asked.
func TestStationQuestionAskedBy(t *testing.T) {
	issue := &tracker.Issue{Slug: "ui/x", Status: "in progress"}
	ask := func(c tracker.Comment) string {
		m := stationMove("agents", nil, issue, "/u", needsYou{}, Attention{OpenQuestion: true}, nil, []tracker.Comment{c}, time.Now())
		return m.Tasks[0].Asked
	}
	if got := ask(tracker.Comment{Text: "question: which API?", Author: "agent-ui-x", Date: "2026-10-30T10:00:00Z"}); !strings.HasPrefix(got, "asked by agent-ui-x ") {
		t.Errorf("Asked = %q", got)
	}
	if got := ask(tracker.Comment{Text: "question: which API?", Date: "2026-10-30T10:00:00Z"}); strings.Contains(got, " by ") {
		t.Errorf("unsigned: Asked = %q", got)
	}
}
