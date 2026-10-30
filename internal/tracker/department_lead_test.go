package tracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// leadDeptProject is a project at dir with a Marketing department on its own
// workflow file and role prompt.
func leadDeptProject(t *testing.T, role string) *Project {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "issues"), 0o755); err != nil {
		t.Fatal(err)
	}
	if role != "" {
		if err := os.MkdirAll(filepath.Join(dir, "departments"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "departments", "marketing.md"), []byte(role), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &Project{Slug: "acme", Name: "Acme", IssueDir: filepath.Join(dir, "issues"), Departments: []Department{{
		Name: "Marketing", Systems: []string{"SEO"}, Mission: "Every site refreshed weekly", Role: "SEO and content",
		Prompt: "departments/marketing.md", Workflow: "workflows/marketing.yaml",
		Goals: []Goal{{ID: "pages"}},
	}}}
}

func TestDepartmentPromptSection(t *testing.T) {
	proj := leadDeptProject(t, "You write local SEO pages.")
	seo := &Issue{Slug: "seo/acme-faq", System: "SEO"}
	got := DepartmentPromptSection(proj, seo)
	for _, want := range []string{
		"## Your department", "You work for Marketing. Mission: Every site refreshed weekly",
		"- Role: SEO and content", "- Workflow: marketing.yaml", "issue-cli goals marketing",
		"issue-cli output add --issue seo/acme-faq", "### Role (departments/marketing.md)", "You write local SEO pages.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("section lacks %q:\n%s", want, got)
		}
	}
	if s := DepartmentPromptSection(proj, &Issue{Slug: "engine/bug", System: "Engine"}); s != "" {
		t.Errorf("Product issue got a section:\n%s", s)
	}
}

func TestDepartmentPromptSection_MissingAndLongPrompt(t *testing.T) {
	proj := leadDeptProject(t, "")
	seo := &Issue{Slug: "seo/x", System: "SEO"}
	if got := DepartmentPromptSection(proj, seo); !strings.Contains(got, "not found; issue-cli department show marketing") {
		t.Errorf("missing prompt:\n%s", got)
	}

	proj = leadDeptProject(t, strings.Repeat("word ", RolePromptMax))
	got := DepartmentPromptSection(proj, seo)
	if !strings.Contains(got, "[cut at 8 KB") || len(got) > RolePromptMax+2000 {
		t.Errorf("long prompt not cut (len %d):\n%s", len(got), got[len(got)-300:])
	}
}

func TestDepartmentLeadBrief(t *testing.T) {
	proj := leadDeptProject(t, "You write local SEO pages.")
	wf := &WorkflowConfig{Statuses: []WorkflowStatus{{Name: "draft"}, {Name: "publish"}}}
	got := DepartmentLeadBrief(wf, &proj.Departments[0], LeadBriefInput{Project: proj, Root: proj.Root(), Now: time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)})
	for _, want := range []string{
		"You are the Marketing lead of Acme. You run in lead-acme-marketing", "report to the CEO lead-acme",
		"- Mission: Every site refreshed weekly", "- Workflow: marketing.yaml (draft → publish)",
		"issue-cli --project acme list --department marketing", "You write local SEO pages.",
		"- Scope: only Marketing's issues", "Dispatch only when the human tells you to",
		"2026-10-30-lead-acme-marketing.md",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("brief lacks %q:\n%s", want, got)
		}
	}
}

func TestBuildDispatchPrompt_DepartmentLead(t *testing.T) {
	proj := leadDeptProject(t, "Role text.")
	seo := &Issue{Slug: "seo/acme-faq", System: "SEO", Status: "draft"}
	live := func(n string) bool { return n == "lead-acme" }
	got := BuildDispatchPrompt(proj, seo, nil, proj.Root(), live).Prompt
	if !strings.Contains(got, "## Your department") || !strings.Contains(got, "You report to the CEO lead-acme (live), covering for the Marketing lead lead-acme-marketing") {
		t.Errorf("covering prompt:\n%s", got)
	}
	live = func(n string) bool { return n == "lead-acme" || n == "lead-acme-marketing" }
	dp := BuildDispatchPrompt(proj, seo, nil, proj.Root(), live)
	if !strings.Contains(dp.Prompt, "You report to the Marketing lead lead-acme-marketing (live), who reports to the CEO lead-acme.") || dp.Lead != "lead-acme-marketing" {
		t.Errorf("department lead prompt (lead %q):\n%s", dp.Lead, dp.Prompt)
	}
}
