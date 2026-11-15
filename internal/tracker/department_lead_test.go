package tracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// The gate bridge's rule reaches the worker: a department with a partner
// gets a Partner bullet saying the partner's approval waits for the
// owner's D-n, in the adapter's words when it has some.
func TestDepartmentPromptSectionPartner(t *testing.T) {
	proj := leadDeptProject(t, "")
	seo := &Issue{Slug: "seo/x", System: "SEO"}
	if got := DepartmentPromptSection(proj, seo); strings.Contains(got, "- Partner:") {
		t.Fatalf("no partner, yet a bullet:\n%s", got)
	}
	proj.Departments[0].Partner = &Partner{Name: "Studio", Adapter: "studio", Path: "/opt/studio"}
	defer func(old func(Partner, string, string) string) { PartnerApprovalHow = old }(PartnerApprovalHow)
	PartnerApprovalHow = nil
	got := DepartmentPromptSection(proj, seo)
	for _, want := range []string{
		"- Partner: Studio (adapter studio, checkout /opt/studio).",
		"which records a D-n on this issue (ref partner-approved:<stage> or partner-sent-back:<stage>)",
		"Only then write the partner's approval of <stage> the way its guide says, quoting <D-n ref>; never without that D-n",
		"issue-cli show seo/x",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("bullet lacks %q:\n%s", want, got)
		}
	}
	PartnerApprovalHow = func(_ Partner, stage, ref string) string { return "set approved on " + stage + " citing " + ref }
	if got := DepartmentPromptSection(proj, seo); !strings.Contains(got, "Only then set approved on <stage> citing <D-n ref>; never without") {
		t.Errorf("the adapter's words are not used:\n%s", got)
	}
}
