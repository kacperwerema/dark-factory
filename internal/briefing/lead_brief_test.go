package briefing

import (
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"gopkg.in/yaml.v3"
)

func TestLeadBrief_CustomPromptsAndPlaceholders(t *testing.T) {
	var wf tracker.WorkflowConfig
	if err := yaml.Unmarshal([]byte("leads:\n  project: \"Lead {{project}} as {{lead}}.\"\n  epic: \"Stabilise {{epic}}.\"\n"), &wf); err != nil {
		t.Fatal(err)
	}
	proj := &tracker.Project{Slug: "p", Name: "Proj"}
	day := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)

	got := LeadBrief(&wf, LeadBriefInput{Project: proj, Root: "/r", Now: day})
	if !strings.HasPrefix(got, "Lead Proj as lead-p.\n\n## Your role") {
		t.Fatalf("project brief:\n%s", got)
	}
	if !strings.Contains(got, "/r/sessions/2026-10-18-lead-p.md") || !strings.Contains(got, "no earlier handoff note") {
		t.Fatalf("project brief handoff lines:\n%s", got)
	}
	if !strings.Contains(got, "message each other with ListAgents / SendMessage, addressed by tmux name (yours is lead-p;") {
		t.Fatalf("project brief messaging line:\n%s", got)
	}
	if !strings.Contains(got, "a ## Next section with the ordered next steps. The viewer's Status page shows that ## Next list; its text form, issue-cli --project p status,") {
		t.Fatalf("project brief should ask for ## Next and name issue-cli status:\n%s", got)
	}
	if !strings.Contains(got, "issue-cli --project p dispatch <slug> --quote \"<the human's words>\" starts the worker exactly like ▶") {
		t.Fatalf("project brief should name issue-cli dispatch:\n%s", got)
	}
	// The brief never sends the lead to the viewer's HTTP endpoints.
	for _, raw := range []string{"curl", "/status.md", "/status.json", "/dispatch"} {
		if strings.Contains(got, raw) {
			t.Errorf("project brief mentions %q:\n%s", raw, got)
		}
	}

	// leads.epic is obsolete: it loads, but nothing renders it.
	if strings.Contains(got, "Stabilise") || strings.Contains(got, "sub-lead") {
		t.Fatalf("project brief uses the obsolete epic brief or names sub-leads:\n%s", got)
	}
}

func TestLeadBrief_DefaultsWithoutLeadsBlock(t *testing.T) {
	got := LeadBrief(nil, LeadBriefInput{Project: &tracker.Project{Slug: "p"}, Root: "/r"})
	if !strings.HasPrefix(got, "You are the project lead of p.") {
		t.Fatalf("default brief:\n%s", got)
	}
	// The default brief agrees with the rules: dispatch is conditional, and
	// workers are reached with SendMessage, not a tmux paste.
	for _, want := range []string{"dispatch the workers the human names", "names them in your chat and you dispatch exactly those", "Talk to a worker: SendMessage to agent-<slug>"} {
		if !strings.Contains(got, want) {
			t.Errorf("default brief missing %q", want)
		}
	}
	for _, stale := range []string{"You groom ideas, dispatch workers,", "Talk to a worker: issue-cli", "tmux-paste fallback", "or you ask the human to"} {
		if strings.Contains(got, stale) {
			t.Errorf("default brief still says %q", stale)
		}
	}
}

// Every lead brief carries the four rules, whatever leads.project says: recall
// decisions with a source, escalate what none covers, dispatch only on the
// human's word, tick only with the human's quote.
func TestLeadBrief_CarriesTheFourRules(t *testing.T) {
	custom := &tracker.WorkflowConfig{Leads: tracker.LeadsConfig{Project: "Lead {{project}}."}}
	for name, wf := range map[string]*tracker.WorkflowConfig{"default": nil, "custom": custom} {
		got := LeadBrief(wf, LeadBriefInput{Project: &tracker.Project{Slug: "p"}, Root: "/r"})
		for _, want := range []string{
			"answer only from recorded decisions (the issue body and its ## Decisions, comments, design docs, approvals) and cite the source",
			"No recorded decision covers it: don't decide. Ask the human",
			"tell the worker to wait",
			"Dispatch only when the human tells you to in this chat, and only the issues the human names: issue-cli --project p dispatch <slug> --quote",
			"Never on your own: not when planning finishes, not the next issue, not more workers than named",
			"Tick a gate only with the human's quoted approval from this chat: issue-cli --project p approve <slug> --to <status> --quote",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("%s brief missing %q:\n%s", name, want, got)
			}
		}
	}
}

func TestLeadBrief_AutoStartLineOnlyWhenOn(t *testing.T) {
	proj := &tracker.Project{Slug: "proj", Name: "Proj"}
	off := LeadBrief(&tracker.WorkflowConfig{}, LeadBriefInput{Project: proj, Root: t.TempDir()})
	if strings.Contains(off, "Auto-start") {
		t.Fatalf("brief without auto-start mentions it:\n%s", off)
	}
	on := LeadBrief(&tracker.WorkflowConfig{}, LeadBriefInput{Project: proj, Root: t.TempDir(), AutoStart: &AutoStartBrief{Words: "start one under 50%", Below: 50, MaxWorkers: 3}})
	for _, w := range []string{`Auto-start is on for this project (the owner's words: "start one under 50%")`, "issue-cli --project proj autostart next", "dispatch --autostart", "memory is at or above 50%, the 1-minute load is at or above the core count, 3 workers are live, or another project just started one"} {
		if !strings.Contains(on, w) {
			t.Errorf("brief missing %q:\n%s", w, on)
		}
	}
	paused := LeadBrief(&tracker.WorkflowConfig{}, LeadBriefInput{Project: proj, Root: t.TempDir(), AutoStart: &AutoStartBrief{Words: "w", Below: 50, MaxWorkers: 2, Paused: true}})
	if !strings.Contains(paused, "It is paused now") {
		t.Errorf("paused brief:\n%s", paused)
	}
}

func TestLeadBrief_JobsRuleOnlyWithJobs(t *testing.T) {
	proj := &tracker.Project{Slug: "proj", Name: "Proj", Departments: []tracker.Department{{Name: "Marketing"}, {Name: "Sales"}}}
	if got := LeadBrief(&tracker.WorkflowConfig{}, LeadBriefInput{Project: proj, Root: t.TempDir()}); strings.Contains(got, "Recurring jobs") {
		t.Fatalf("brief without jobs mentions them:\n%s", got)
	}
	proj.Departments[0].Jobs = []tracker.Job{{ID: "weekly"}, {ID: "monthly"}}
	proj.Departments[1].Jobs = []tracker.Job{{ID: "leads"}}
	ceo := LeadBrief(&tracker.WorkflowConfig{}, LeadBriefInput{Project: proj, Root: t.TempDir()})
	for _, w := range []string{"- Recurring jobs (3): you tick the recurring jobs of departments whose lead is not live", "issue-cli --project proj job due", "--scheduled only where it says start", "never turn a job's standing start on or off without the human's words"} {
		if !strings.Contains(strings.ToLower(ceo), strings.ToLower(w)) {
			t.Errorf("CEO brief missing %q:\n%s", w, ceo)
		}
	}
	dept := DepartmentLeadBrief(&tracker.WorkflowConfig{}, &proj.Departments[1], LeadBriefInput{Project: proj, Root: t.TempDir()})
	if !strings.Contains(dept, "- Recurring jobs (1): you tick your department's recurring jobs.") {
		t.Errorf("department brief:\n%s", dept)
	}
}

// The CEO is pointed at the HQ report only when the project has departments.
func TestLeadBriefNamesTheHQReportWithDepartments(t *testing.T) {
	proj := &tracker.Project{Name: "P", Slug: "p"}
	in := LeadBriefInput{Project: proj, Root: t.TempDir()}
	if strings.Contains(LeadBrief(&tracker.WorkflowConfig{}, in), "status --hq") {
		t.Error("no departments, but the brief names the HQ report")
	}
	proj.Departments = []tracker.Department{{Name: "Marketing"}}
	if on := LeadBrief(&tracker.WorkflowConfig{}, in); !strings.Contains(on, "The company in a minute: issue-cli --project p status --hq") {
		t.Errorf("brief missing the HQ report line:\n%s", on)
	}
}

// TestLeadBrief_RulesAndScopeGoInTheIssueBody: a lead's rules for a worker
// and a scope change mid-issue land in the issue body (a Lead rules section,
// a criterion box plus a D-n), not only in a peer message; the boxes rule
// leaves a worker its own boxes. Every lead brief carries it: project,
// custom leads.project, department.
func TestLeadBrief_RulesAndScopeGoInTheIssueBody(t *testing.T) {
	custom := &tracker.WorkflowConfig{Leads: tracker.LeadsConfig{Project: "Lead {{project}}."}}
	briefs := map[string]string{}
	for name, wf := range map[string]*tracker.WorkflowConfig{"default": nil, "custom": custom} {
		briefs[name] = LeadBrief(wf, LeadBriefInput{Project: &tracker.Project{Slug: "p"}, Root: "/r"})
	}
	proj := leadDeptProject(t, "You write local SEO pages.")
	briefs["department"] = DepartmentLeadBrief(&tracker.WorkflowConfig{}, &proj.Departments[0], LeadBriefInput{Project: proj, Root: proj.Root()})
	for name, got := range briefs {
		for _, want := range []string{
			`dispatch <slug> --quote`,
			`append <slug> --section "Lead rules"`,
			"Rules for a worker go in its issue body, not only in a message",
			"A scope change mid-issue is an appended criterion box",
			`--section "Acceptance criteria" --body "- [ ] ..."`,
			"plus a D-n",
			"the message to the worker only points at the box and the D-n",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("%s brief missing %q:\n%s", name, want, got)
			}
		}
		i := strings.Index(got, "- Boxes rule")
		if i < 0 {
			t.Fatalf("%s brief has no boxes rule:\n%s", name, got)
		}
		line, _, _ := strings.Cut(got[i:], "\n")
		for _, want := range []string{"owner boxes", "other issues' boxes are untouched", "your own", "Implementation/Testing/Shipping boxes are yours"} {
			if !strings.Contains(line, want) {
				t.Errorf("%s boxes rule missing %q: %s", name, want, line)
			}
		}
		if strings.Contains(got, "don't tick or untick any box on any issue") {
			t.Errorf("%s brief keeps the blanket no-tick rule", name)
		}
	}
}
