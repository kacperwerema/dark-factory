package briefing

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// briefIssues writes issues into dir (rel path → file) and returns them.
func briefIssues(t *testing.T, dir string, files map[string]string) []*tracker.Issue {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	issues, err := tracker.LoadIssues(dir)
	if err != nil {
		t.Fatal(err)
	}
	return issues
}

func issueFile(title, status, system, decisions string) string {
	return fmt.Sprintf("---\ntitle: %q\nstatus: %q\nsystem: %q\n---\n\n## Decisions\n\n%s", title, status, system, decisions)
}

// AC1: a worker briefing on a Workflow issue carries the project and the
// Workflow decisions and not the Combat one.
func TestDispatchPromptCarriesScopedDecisions(t *testing.T) {
	dir := t.TempDir()
	briefIssues(t, dir, map[string]string{
		"Ops/rules.md":     issueFile("rules", "done", "Ops", "- **D-1** · 2026-10-01 · chat: Project rule {scope: project}\n- **D-2** · 2026-10-02 · chat: Workflow rule {scope: system:Workflow}\n- **D-3** · 2026-10-03 · chat: Combat rule {scope: system:Combat}\n"),
		"Workflow/gate.md": issueFile("gate", "in progress", "Workflow", ""),
	})
	proj := &tracker.Project{Slug: "acme", IssueDir: dir}
	issues, _ := tracker.LoadIssues(dir)
	var issue *tracker.Issue
	for _, i := range issues {
		if i.Slug == "workflow/gate" {
			issue = i
		}
	}
	dp := BuildDispatchPrompt(proj, issue, nil, filepath.Dir(dir), nil)
	sec := dp.Prompt[strings.Index(dp.Prompt, "## Decisions in force"):]
	sec = sec[:strings.Index(sec, "## Memory")]
	for _, want := range []string{"ops/rules#D-1 · soft · project · Project rule", "ops/rules#D-2 · soft · system:Workflow · Workflow rule"} {
		if !strings.Contains(sec, want) {
			t.Errorf("briefing lacks %q:\n%s", want, sec)
		}
	}
	if strings.Contains(dp.Prompt, "Combat rule") {
		t.Errorf("Combat decision in a Workflow briefing:\n%s", sec)
	}
	if len(dp.DecisionRefs) != 2 {
		t.Errorf("refs %v", dp.DecisionRefs)
	}
}

// AC5: a department lead gets project and its department's decisions; the
// CEO only project-scoped ones.
func TestLeadBriefsCarryTheirScopes(t *testing.T) {
	proj := leadDeptProject(t, "")
	briefIssues(t, proj.IssueDir, map[string]string{
		"Ops/rules.md": issueFile("rules", "done", "Ops", "- **D-1** · 2026-10-01 · chat: Project rule {scope: project}\n"+
			"- **D-2** · 2026-10-02 · chat: Marketing rule {scope: department:marketing}\n"+
			"- **D-3** · 2026-10-03 · chat: Sales rule {scope: department:sales}\n"+
			"- **D-4** · 2026-10-04 · chat: UI rule {scope: system:UI}\n"+
			"- **D-5** · 2026-10-05 · chat: Pieces rule {scope: type:piece}\n"+
			"- **D-6** · 2026-10-06 · chat: Issue rule\n"),
	})
	now := time.Date(2026, 10, 31, 9, 0, 0, 0, time.Local)
	ceo := LeadBrief(nil, LeadBriefInput{Project: proj, Root: proj.Root(), Now: now})
	dept := DepartmentLeadBrief(nil, &proj.Departments[0], LeadBriefInput{Project: proj, Root: proj.Root(), Now: now})
	check := func(name, brief string, want, not []string) {
		for _, w := range want {
			if !strings.Contains(brief, w) {
				t.Errorf("%s brief lacks %q", name, w)
			}
		}
		for _, n := range not {
			if strings.Contains(brief, n) {
				t.Errorf("%s brief has %q", name, n)
			}
		}
	}
	check("CEO", ceo, []string{"Decisions in force (project scope", "Project rule"},
		[]string{"Marketing rule", "Sales rule", "UI rule", "Pieces rule", "Issue rule"})
	check("Marketing", dept, []string{"Decisions in force (project and department:marketing scope", "Project rule", "Marketing rule"},
		[]string{"Sales rule", "UI rule", "Pieces rule", "Issue rule"})
}

// A lead with nothing in force still gets the line and the command.
func TestLeadBriefEmptyDecisions(t *testing.T) {
	proj := leadDeptProject(t, "")
	got := LeadBrief(nil, LeadBriefInput{Project: proj, Root: proj.Root()})
	if !strings.Contains(got, "No decisions in force for the project scope: issue-cli --project acme decisions --lead ceo") {
		t.Errorf("CEO brief:\n%s", got)
	}
}
