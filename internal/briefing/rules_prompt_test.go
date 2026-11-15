package briefing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/rules"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func writeMachineRules(t *testing.T, text string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	p := rules.MachinePath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// rulesProject is a project on disk with rules: in its workflow.yaml.
func rulesProject(t *testing.T, slug, rulesYAML string) (*tracker.Project, *tracker.WorkflowConfig) {
	t.Helper()
	root := t.TempDir()
	wf := filepath.Join(root, "workflow.yaml")
	body := "statuses:\n  - name: idea\n  - name: done\n" + rulesYAML
	if err := os.WriteFile(wf, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	proj := &tracker.Project{Name: slug, Slug: slug, IssueDir: filepath.Join(root, "issues"), WorkDir: root, WorkflowFile: wf}
	return proj, proj.LoadWorkflow()
}

// AC1: the machine's rule reaches the workers and leads of two projects,
// under its own label, and each project's rule only its own.
func TestRulesReachTwoProjects(t *testing.T) {
	writeMachineRules(t, "- [no-force] Never force-push main.\n")
	alpha, alphaWF := rulesProject(t, "alpha", "rules:\n  - text: Alpha deploys on Tuesdays.\n")
	beta, betaWF := rulesProject(t, "beta", "rules:\n  - text: Beta ships behind a flag.\n")
	issue := &tracker.Issue{Slug: "x/one", Title: "One", Status: "idea"}

	for _, c := range []struct {
		name         string
		prompt       string
		mine, theirs string
	}{
		{"alpha worker", BuildDispatchPrompt(alpha, issue, alphaWF, alpha.WorkDir, nil).Prompt, "Alpha deploys", "Beta ships"},
		{"beta worker", BuildDispatchPrompt(beta, issue, betaWF, beta.WorkDir, nil).Prompt, "Beta ships", "Alpha deploys"},
		{"alpha lead", LeadBrief(alphaWF, LeadBriefInput{Project: alpha, Root: alpha.WorkDir}), "Alpha deploys", "Beta ships"},
		{"beta department lead", DepartmentLeadBrief(betaWF, &tracker.Department{Name: "Marketing"}, LeadBriefInput{Project: beta, Root: beta.WorkDir}), "Beta ships", "Alpha deploys"},
	} {
		t.Run(c.name, func(t *testing.T) {
			machine := strings.Index(c.prompt, "On every project (the owner's")
			project := strings.Index(c.prompt, "On this project (workflow.yaml rules:):")
			if machine < 0 || project < 0 {
				t.Fatalf("the two layers aren't labelled:\n%s", c.prompt)
			}
			rule := strings.Index(c.prompt, "[no-force] Never force-push main.")
			mine := strings.Index(c.prompt, c.mine)
			if !(machine < rule && rule < project && project < mine) {
				t.Errorf("a rule sits under the wrong layer: %d %d %d %d", machine, rule, project, mine)
			}
			if strings.Contains(c.prompt, c.theirs) {
				t.Errorf("another project's rule reached it: %q", c.theirs)
			}
		})
	}
}

func TestNarrowingKeepsTheMachineRule(t *testing.T) {
	writeMachineRules(t, "- [no-force] Never force-push main.\n")
	_, wf := rulesProject(t, "alpha", "rules:\n  - text: Force-push main only on a tag day.\n    narrows: no-force\n")
	got := RulesPromptSection(wf)
	for _, want := range []string{"[no-force] Never force-push main.", "Force-push main only on a tag day. (narrows [no-force])", "never cancels one"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

func TestRulesSectionsWithoutRules(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if got := RulesPromptSection(nil); got != "" {
		t.Errorf("no rules, a section: %q", got)
	}
	if got := RulesLeadLines(nil); !strings.Contains(got, "rules.md") || !strings.Contains(got, "issue-cli rules") {
		t.Errorf("a lead should still learn where rules go: %q", got)
	}
}

func TestRulesSectionSaysWhenTheFileCantBeRead(t *testing.T) {
	writeMachineRules(t, "")
	p := rules.MachinePath()
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(p, 0o755); err != nil { // a folder can't be read as a file
		t.Fatal(err)
	}
	if got := RulesPromptSection(nil); !strings.Contains(got, "could not be read") {
		t.Errorf("an unreadable file went silent: %q", got)
	}
}

func TestRulesPromptCutsAtTheCap(t *testing.T) {
	long := strings.Repeat("word ", rules.BriefChars/5)
	writeMachineRules(t, "- "+long+"\n- second rule\n")
	if got := RulesPromptSection(nil); !strings.Contains(got, "+1 more: issue-cli rules") || strings.Contains(got, "second rule") {
		t.Errorf("the cap: %q", got[len(got)-80:])
	}
}

func TestPinsShowTheirLayerApart(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir := filepath.Join(t.TempDir(), "issues")
	proj := &tracker.Project{Name: "acme", Slug: "acme", IssueDir: dir}
	now := time.Now()
	for _, s := range []struct {
		store memory.Store
		scope string
		text  string
	}{
		{memory.ProjectStore(dir), memory.ScopeProject, "acme deploys on Tuesdays"},
		{memory.MachineStore(), "", "every project tags release candidates"},
	} {
		r, err := s.store.Add(memory.Draft{Text: s.text, Scope: s.scope}, memory.AddOptions{Mode: memory.ModeNew}, now)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.store.Pin(r.Memory.ID, "lead-acme", true, now); err != nil {
			t.Fatal(err)
		}
	}
	for name, got := range map[string]string{
		"worker": MemoryPromptSection(proj, &tracker.Issue{Slug: "x"}),
		"lead":   MemoryLeadLine(proj, "lead-acme", ""),
	} {
		machine := strings.Index(got, "Pinned on every project:")
		rule := strings.Index(got, "every project tags release candidates")
		project := strings.Index(got, "Pinned on this project:")
		fact := strings.Index(got, "acme deploys on Tuesdays")
		if machine < 0 || !(machine < rule && rule < project && project < fact) {
			t.Errorf("%s: the layers aren't apart:\n%s", name, got)
		}
	}
}
