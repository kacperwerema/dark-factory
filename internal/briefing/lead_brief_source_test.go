package briefing

import (
	"os"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"gopkg.in/yaml.v3"
)

// The addendum follows the built-in brief instead of replacing it, with its
// placeholders filled, and comes before the footer.
func TestLeadBrief_AddendumFollowsTheDefault(t *testing.T) {
	wf := &tracker.WorkflowConfig{Leads: tracker.LeadsConfig{ProjectAddendum: "- Extra for {{project}} in {{lead}}: issue-cli list.\n"}}
	got := LeadBrief(wf, LeadBriefInput{Project: &tracker.Project{Slug: "p"}, Root: "/r"})
	base := LeadBrief(nil, LeadBriefInput{Project: &tracker.Project{Slug: "p"}, Root: "/r"})
	head, _, _ := strings.Cut(base, "\n\n## Your role")
	if !strings.HasPrefix(got, head+"\n\n- Extra for p in lead-p: issue-cli --project p list.\n\n## Your role") {
		t.Fatalf("brief with addendum:\n%s", got)
	}
	// A replaced brief still takes the addendum.
	wf.Leads.Project = "Own brief."
	got = LeadBrief(wf, LeadBriefInput{Project: &tracker.Project{Slug: "p"}, Root: "/r"})
	if !strings.HasPrefix(got, "Own brief.\n\n- Extra for p") {
		t.Fatalf("replaced brief with addendum:\n%s", got)
	}
}

// This repo's workflow.yaml adds to the default and never replaces it, so
// the two lines the audit found dropped stay in what its lead reads.
func TestRepoLeadBriefAddsToTheDefault(t *testing.T) {
	raw, err := os.ReadFile("../../workflow.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var wf tracker.WorkflowConfig
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(wf.Leads.Project) != "" {
		t.Error("workflow.yaml sets leads.project: it replaces the built-in brief, use leads.project_addendum")
	}
	if strings.TrimSpace(wf.Leads.ProjectAddendum) == "" {
		t.Error("workflow.yaml has no leads.project_addendum")
	}
	got := LeadBrief(&wf, LeadBriefInput{Project: &tracker.Project{Slug: "p"}, Root: "/r"})
	for _, want := range []string{
		"Check on an agent: issue-cli --project p context <slug>",
		"they message you only when the human tells them to",
		tracker.LeadDuties("ideas", "the process"),
		"Pick the type when you file an issue",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the repo's lead brief misses %q", want)
		}
	}
}

// The project and department briefs open with the same duties.
func TestLeadBriefsShareTheDuties(t *testing.T) {
	proj := leadDeptProject(t, "You write local SEO pages.")
	got := DepartmentLeadBrief(&tracker.WorkflowConfig{}, &proj.Departments[0], LeadBriefInput{Project: proj, Root: proj.Root()})
	if !strings.Contains(got, tracker.LeadDuties("your department's ideas", "the department's process")) {
		t.Errorf("department brief:\n%s", got)
	}
}

// The licence sentence in a department lead's brief is quoted from the doc
// that states the rule, word for word.
func TestPartnerLicenceRuleQuotesTheDoc(t *testing.T) {
	raw, err := os.ReadFile("../../docs/departments.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), tracker.PartnerLicenceRule) {
		t.Errorf("docs/departments.md does not quote tracker.PartnerLicenceRule: %s", tracker.PartnerLicenceRule)
	}
	proj := leadDeptProject(t, "You write local SEO pages.")
	got := DepartmentLeadBrief(&tracker.WorkflowConfig{}, &proj.Departments[0], LeadBriefInput{Project: proj, Root: proj.Root()})
	if !strings.Contains(got, tracker.PartnerLicenceRule) {
		t.Errorf("department brief has no licence sentence:\n%s", got)
	}
}
