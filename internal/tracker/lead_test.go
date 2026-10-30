package tracker

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func issueWith(slug string, fields map[string]string) *Issue {
	issue := &Issue{Slug: slug, Title: slug, Status: "in progress"}
	for k, v := range fields {
		issue.ExtraFields = append(issue.ExtraFields, ExtraField{Key: k, Value: v})
	}
	return issue
}

func TestParentSlug_ParentWinsUmbrellaIsAlias(t *testing.T) {
	both := issueWith("a", map[string]string{"parent": "epic-p", "umbrella": "epic-u"})
	if got := ParentSlug(both); got != "epic-p" || UsesLegacyUmbrella(both) {
		t.Fatalf("parent+umbrella: ParentSlug = %q, legacy = %v", got, UsesLegacyUmbrella(both))
	}
	legacy := issueWith("b", map[string]string{"umbrella": "epic-u"})
	if got := ParentSlug(legacy); got != "epic-u" || !UsesLegacyUmbrella(legacy) {
		t.Fatalf("umbrella only: ParentSlug = %q, legacy = %v", got, UsesLegacyUmbrella(legacy))
	}
	if got := ParentSlug(issueWith("c", nil)); got != "" {
		t.Fatalf("no link: ParentSlug = %q", got)
	}
}

// A leftover lead: or base: is a plain custom field: it doesn't make an epic.
func TestIsEpic(t *testing.T) {
	epic := issueWith("ui/epic", nil)
	child := issueWith("ui/child", map[string]string{"parent": "ui/epic", "base": "base/ui/epic"})
	typed := issueWith("typed", nil)
	typed.Type = "epic"
	led := issueWith("led", map[string]string{"lead": "lead-led", "base": "base/led"})
	issues := []*Issue{epic, child, typed, led}

	for _, c := range []struct {
		issue *Issue
		want  bool
	}{{epic, true}, {child, false}, {typed, true}, {led, false}} {
		if got := IsEpic(c.issue, issues); got != c.want {
			t.Errorf("IsEpic(%s) = %v, want %v", c.issue.Slug, got, c.want)
		}
	}
	if kids := EpicChildren(issues, "UI/Epic"); len(kids) != 1 || kids[0] != child {
		t.Errorf("EpicChildren = %+v", kids)
	}
}

func TestLeadSessionName(t *testing.T) {
	if got := LeadSessionName(&Project{Slug: "issue-viewer"}); got != "lead-issue-viewer" {
		t.Errorf("project lead = %q", got)
	}
	if got := LeadSessionName(&Project{Slug: "my.proj"}); got != "lead-my-proj" {
		t.Errorf("dotted slug = %q", got)
	}
	if got := LeadSessionName(nil); got != "lead-project" {
		t.Errorf("no project = %q", got)
	}
}

// Epics and their children branch from HEAD like any issue; an old base: is
// ignored.
func TestIssueWorktree(t *testing.T) {
	enabled := true
	wf := &WorkflowConfig{Worktree: &enabled}
	epic := issueWith("ui/epic", map[string]string{"base": "base/ui/epic", "lead": "lead-ui-epic"})
	child := issueWith("ui/child", map[string]string{"parent": "ui/epic", "base": "base/ui/epic"})

	if got := IssueWorktree("/w", epic, wf); got != (WorktreeSpec{Path: "/w/.worktrees/ui/epic", Branch: "work/ui/epic"}) {
		t.Errorf("epic = %+v", got)
	}
	if got := IssueWorktree("/w", child, wf); got != (WorktreeSpec{Path: "/w/.worktrees/ui/child", Branch: "work/ui/child"}) {
		t.Errorf("child = %+v", got)
	}
	if got := IssueWorktree("/w", child, nil); got != (WorktreeSpec{}) {
		t.Errorf("worktrees disabled = %+v", got)
	}
}

// Every issue reports to the project lead, even when an old lead-<epic>
// session is still running.
func TestLeadFor(t *testing.T) {
	proj := &Project{Slug: "p"}
	epic := issueWith("ui/epic", nil)
	child := issueWith("ui/child", map[string]string{"parent": "UI/Epic"})
	issues := []*Issue{epic, child}
	liveSet := func(names ...string) func(string) bool {
		return func(n string) bool {
			for _, x := range names {
				if x == n {
					return true
				}
			}
			return false
		}
	}

	r := LeadFor(proj, child, issues, liveSet("lead-ui-epic", "lead-p"))
	if r != (LeadRoute{Lead: "lead-p", Live: true, Epic: "ui/epic", CEO: "lead-p", CEOLive: true}) {
		t.Errorf("child: %+v", r)
	}
	r = LeadFor(proj, child, issues, liveSet("lead-ui-epic"))
	if r != (LeadRoute{Lead: "lead-p", Live: false, Epic: "ui/epic", CEO: "lead-p"}) {
		t.Errorf("project lead down: %+v", r)
	}
	r = LeadFor(proj, epic, issues, nil)
	if r != (LeadRoute{Lead: "lead-p", CEO: "lead-p"}) {
		t.Errorf("epic: %+v", r)
	}
}

// A department issue reports to its department's lead while it is live,
// else to the CEO covering for it; Product's issues report to the CEO.
func TestLeadFor_Department(t *testing.T) {
	proj := &Project{Slug: "acme", Departments: []Department{{Name: "Social Media", Systems: []string{"Social"}}}}
	post := &Issue{Slug: "social/post", System: "Social"}
	bug := &Issue{Slug: "engine/bug", System: "Engine"}
	live := func(names ...string) func(string) bool {
		return func(n string) bool { return slices.Contains(names, n) }
	}

	r := LeadFor(proj, post, nil, live("lead-acme", "lead-acme-social-media"))
	if r.Lead != "lead-acme-social-media" || !r.Live || r.Covering || r.CEO != "lead-acme" || r.Department != "Social Media" {
		t.Errorf("department lead live: %+v", r)
	}
	r = LeadFor(proj, post, nil, live("lead-acme"))
	if r.Lead != "lead-acme" || !r.Live || !r.Covering || r.DepartmentLead != "lead-acme-social-media" {
		t.Errorf("department lead down: %+v", r)
	}
	r = LeadFor(proj, bug, nil, live("lead-acme", "lead-acme-social-media"))
	if r.Lead != "lead-acme" || r.Department != "" || r.Covering {
		t.Errorf("product issue: %+v", r)
	}
	if got := proj.DepartmentOfLead("lead-acme-social-media"); got == nil || got.Name != "Social Media" {
		t.Errorf("DepartmentOfLead = %+v", got)
	}
	if got := proj.DepartmentByKey("social-media"); got == nil || got.Name != "Social Media" {
		t.Errorf("DepartmentByKey = %+v", got)
	}
	if LeadScopeDepartment("Social Media") != "department:social-media" {
		t.Errorf("scope = %q", LeadScopeDepartment("Social Media"))
	}
}

func TestBuildDispatchPrompt_ChildOfEpic(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(path), 0755)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("UI/epic.md", "---\ntitle: \"Epic\"\nstatus: \"in progress\"\nbase: \"base/ui/epic\"\nlead: \"lead-ui-epic\"\n---\n\nEpic body.\n")
	write("UI/child.md", "---\ntitle: \"Child\"\nstatus: \"in progress\"\numbrella: \"ui/epic\"\n---\n\nChild body.\n")
	proj := &Project{Slug: "p", IssueDir: dir}
	issues, err := LoadIssues(dir)
	if err != nil {
		t.Fatal(err)
	}
	child := FindIssue(issues, "ui/child")
	enabled := true
	wf := &WorkflowConfig{Worktree: &enabled}

	dp := BuildDispatchPrompt(proj, child, wf, "/w", func(n string) bool { return n == "lead-ui-epic" || n == "lead-p" })
	if dp.Lead != "lead-p" || dp.Branch != "work/ui/child" {
		t.Fatalf("got %+v", dp)
	}
	for _, want := range []string{
		"You report to the project lead lead-p (live). Your issue is part of epic ui/epic.",
		"`issue-cli --project p comment ui/child \"question: ...\"`",
		"Don't message lead-p or any other agent (SendMessage) unless the human, in your chat, gives you permission or asks you to.",
		"Your address is agent-ui-child",
	} {
		if !strings.Contains(dp.Prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	for _, gone := range []string{"sub-lead", "## Base branch", "base/ui/epic", "you can also message", "message your lead"} {
		if strings.Contains(dp.Prompt, gone) {
			t.Errorf("prompt still mentions %q", gone)
		}
	}

	dp = BuildDispatchPrompt(proj, child, wf, "/w", nil)
	if !strings.Contains(dp.Prompt, "You report to the project lead lead-p (not running).") {
		t.Errorf("lead line missing:\n%s", dp.Prompt)
	}
}
