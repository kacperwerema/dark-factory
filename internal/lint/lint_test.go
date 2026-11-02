package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

var lintNow = time.Date(2026, 10, 24, 12, 0, 0, 0, time.Local)

// minimalWorkflow is a valid workflow.yaml the tests break one piece at a time.
const minimalWorkflow = `template: custom
statuses:
  - name: idea
    description: raw
  - name: done
    description: shipped
transitions:
  - from: idea
    to: done
    actions:
      - type: append_section
        title: Design
        body: "- [ ] Approach documented"
      - type: validate
        rule: "section_checkboxes_checked: Design"
`

// lintProject writes workflow.yaml and the issues ({"Sys/a.md": content})
// into a temp project and loads it.
func lintProject(t *testing.T, workflow string, issues map[string]string) *Env {
	t.Helper()
	dir := t.TempDir()
	wfPath := filepath.Join(dir, "workflow.yaml")
	if err := os.WriteFile(wfPath, []byte(workflow), 0644); err != nil {
		t.Fatal(err)
	}
	issueDir := filepath.Join(dir, "issues")
	if err := os.MkdirAll(issueDir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, content := range issues {
		p := filepath.Join(issueDir, name)
		os.MkdirAll(filepath.Dir(p), 0755)
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	env, err := Load(&tracker.Project{Slug: "t", Name: "T", WorkflowFile: wfPath, IssueDir: issueDir}, lintNow)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func run(t *testing.T, env *Env, check string) []Finding {
	t.Helper()
	rep, err := Run(env, check)
	if err != nil {
		t.Fatal(err)
	}
	return rep.Findings
}

// has reports whether a finding mentions every part.
func has(fs []Finding, parts ...string) bool {
	for _, f := range fs {
		text := f.Where + " " + f.Message + " " + f.Fix
		ok := true
		for _, p := range parts {
			ok = ok && strings.Contains(text, p)
		}
		if ok {
			return true
		}
	}
	return false
}

func TestMinimalWorkflowIsClean(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	rep, err := Run(env)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Findings) != 0 {
		t.Errorf("findings on a clean workflow: %+v", rep.Findings)
	}
	if strings.Join(rep.Checks, ",") != "undated,workflow,schema,template,placeholders,sections,departments,untracked,paths,owner-only,na-boxes,owner-marker,criteria,reports,routing,memory,evals,department,critique,testids" {
		t.Errorf("checks = %v", rep.Checks)
	}
}

func TestRunUnknownCheck(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	if _, err := Run(env, "nope"); err == nil || !strings.Contains(err.Error(), "checks: undated") {
		t.Errorf("err = %v, want the check list", err)
	}
}

func TestUndated(t *testing.T) {
	env := lintProject(t, minimalWorkflow, map[string]string{
		"CLI/dated.md":   "---\ntitle: \"Dated\"\nstatus: idea\ncreated: \"2026-01-02\"\n---\n",
		"CLI/undated.md": "---\ntitle: \"Undated\"\nstatus: idea\n---\n\n## Decisions\n\n- **D-1** · 2026-03-04 · lead chat: x\n- **D-2** · 2026-02-01 · lead chat: y\n",
	})
	fs := run(t, env, "undated")
	if len(fs) != 1 {
		t.Fatalf("findings = %+v, want one", fs)
	}
	if !has(fs, "cli/undated", "issue-cli set-meta cli/undated --key created --value 2026-02-01") {
		t.Errorf("finding = %+v, want the earliest decision as the suggested date", fs[0])
	}
}

func TestSchema(t *testing.T) {
	wf := strings.Replace(minimalWorkflow, "transitions:", "transitons:", 1) +
		"actions:\n  - id: x\n    label: X\n    prompt: hi\n"
	wf = strings.Replace(wf, "    description: raw", "    description: raw\n    promt: typo", 1)
	fs := run(t, lintProject(t, wf, nil), "schema")
	for _, want := range []struct {
		parts []string
	}{
		{[]string{"workflow.yaml:8 ", `unknown key "transitons" at the top level`, `did you mean "transitions"?`}},
		{[]string{"workflow.yaml:5 ", `unknown key "promt" in statuses[]`, `did you mean "prompt"?`}},
		{[]string{"actions is deprecated", "rename it to issue_actions"}},
		{[]string{"no transitions: list"}},
	} {
		if !has(fs, want.parts...) {
			t.Errorf("no finding with %q in %+v", want.parts, fs)
		}
	}

	fs = run(t, lintProject(t, "template: custom\nboard:\n  columns: [a]\n", nil), "schema")
	if !has(fs, "no statuses: list") {
		t.Errorf("missing statuses not flagged: %+v", fs)
	}
}

func TestSchemaKnowsNestedShapes(t *testing.T) {
	wf := minimalWorkflow + `ci:
  run:
    - make validate
    - cmd: make slow
      tier: slow
      tierr: typo
systems:
  CLI:
    statuses:
      - name: idea
        prompt: hi
types:
  feature:
    path: [idea, done]
    descripton: typo
`
	fs := run(t, lintProject(t, wf, nil), "schema")
	if len(fs) != 2 || !has(fs, `"tierr" in ci.run[]`, `"tier"`) || !has(fs, `"descripton" in types.feature`, `"description"`) {
		t.Errorf("findings = %+v, want the two typos only", fs)
	}
}

func TestTemplateStamp(t *testing.T) {
	latest, err := workflowupgrade.BundledVersion(workflowupgrade.DefaultTemplate)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.TrimPrefix(minimalWorkflow, "template: custom\n")
	for _, tc := range []struct {
		stamp string
		parts []string
	}{
		{"development@v0.1.0", []string{"workflow.yaml:1 ", "behind the bundled development@" + latest, "issue-cli workflow upgrade"}},
		{"nope@v1.0.0", []string{"no bundled template named"}},
		{"development", []string{"is not <name>@vX.Y.Z"}},
		{"", []string{"no template: stamp"}},
	} {
		wf := body
		if tc.stamp != "" {
			wf = "template: " + tc.stamp + "\n" + body
		}
		fs := run(t, lintProject(t, wf, nil), "template")
		if !has(fs, tc.parts...) {
			t.Errorf("stamp %q: findings = %+v, want one with %q", tc.stamp, fs, tc.parts)
		}
	}
	if fs := run(t, lintProject(t, "template: development@"+latest+"\n"+body, nil), "template"); len(fs) != 0 {
		t.Errorf("an up-to-date stamp was flagged: %+v", fs)
	}
}

// A current stamp whose features: record misses what the file has is
// flagged; a record that only leaves proposals undecided is not.
func TestTemplateStaleRecord(t *testing.T) {
	data, err := workflowupgrade.ReadTemplate(workflowupgrade.DefaultTemplate)
	if err != nil {
		t.Fatal(err)
	}
	if fs := run(t, lintProject(t, string(data), nil), "template"); len(fs) != 0 {
		t.Errorf("undecided proposals were flagged: %+v", fs)
	}
	stale := string(data) + "\nci:\n  run:\n    - cmd: go test ./...\n"
	if fs := run(t, lintProject(t, stale, nil), "template"); !has(fs, "features: record is stale", "local-ci", "issue-cli workflow scan") {
		t.Errorf("findings = %+v, want the stale record with local-ci", fs)
	}
}

func TestPlaceholders(t *testing.T) {
	wf := minimalWorkflow + `issue_actions:
  - id: ok
    label: OK
    prompt: "Look at {{slug}} ({{title}})"
  - id: typo
    label: Typo
    prompt: "Look at {{slugg}}"
  - id: spaced
    label: Spaced
    prompt: "Look at {{ status }}"
project_actions:
  - id: review
    label: Review
    prompt: "Review {{project}} for {{slug}}"
leads:
  project: "Lead of {{project}} in {{lead}}"
`
	wf = strings.Replace(wf, "    description: raw", "    description: raw\n    prompt: \"Run issue-cli show {{number}}\"", 1)
	wf = strings.Replace(wf, `        rule: "section_checkboxes_checked: Design"`, `        rule: "section_checkboxes_checked: Design"
        hint: "issue-cli check {{ slug }} D1 for {{repo}}"
      - type: inject_prompt
        prompt: "Now {{slug}}"`, 1)
	fs := run(t, lintProject(t, wf, nil), "placeholders")
	for _, want := range [][]string{
		{"issue_actions[typo].prompt: {{slugg}} is never filled", "did you mean {{slug}}?"},
		{"issue_actions[spaced].prompt: {{ status }} has spaces", "write {{status}}"},
		{"project_actions[review].prompt: {{slug}} is never filled", "{{project}}"},
		{"statuses[idea].prompt is not templated, so {{number}} reaches the agent"},
		{"idea → done inject_prompt is not templated"},
	} {
		if !has(fs, want...) {
			t.Errorf("no finding with %q", want)
		}
	}
	if len(fs) != 5 {
		t.Errorf("findings = %d, want 5 (the ok prompt, lead brief and validator hint are filled): %+v", len(fs), fs)
	}
}

func TestSections(t *testing.T) {
	wf := minimalWorkflow + `  - from: idea
    to: idea
    actions:
      - type: validate
        rule: "section_has_checkboxes: Review"
`
	fs := run(t, lintProject(t, wf, nil), "sections")
	if !has(fs, `section_has_checkboxes: Review: no append_section or status template writes ## Review`, `append_section titled "Review"`) {
		t.Errorf("unwritten section not flagged: %+v", fs)
	}

	wf = strings.Replace(minimalWorkflow, `body: "- [ ] Approach documented"`, `body: "Write the approach here."`, 1)
	if fs := run(t, lintProject(t, wf, nil), "sections"); !has(fs, "## Design is written without a checkbox") {
		t.Errorf("box-less template not flagged: %+v", fs)
	}

	plan := strings.Replace(minimalWorkflow, `      - type: validate
        rule: "section_checkboxes_checked: Design"`, `      - type: append_section
        title: Test Plan
        body: "### Automated\n- go test\n\n### Manual checks\n- look"
      - type: validate
        rule: has_test_plan`, 1)
	if fs := run(t, lintProject(t, plan, nil), "sections"); !has(fs, "has_test_plan: the Test Plan template lacks the exact") {
		t.Errorf("broken test plan headings not flagged: %+v", fs)
	}
	good := strings.Replace(plan, "### Manual checks", "### Manual", 1)
	if fs := run(t, lintProject(t, good, nil), "sections"); len(fs) != 0 {
		t.Errorf("a correct test plan template was flagged: %+v", fs)
	}
}

// TestBundledTemplatesHaveNoFindings keeps every template issue-cli init
// writes clean: a new project starts with no lint warning.
func TestBundledTemplatesHaveNoFindings(t *testing.T) {
	for _, name := range workflowupgrade.Templates() {
		raw, err := workflowupgrade.ReadTemplate(name)
		if err != nil {
			t.Fatal(err)
		}
		rep, err := Run(lintProject(t, string(raw), nil))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range rep.Findings {
			t.Errorf("template %s: %s %s: %s", name, f.Check, f.Where, f.Message)
		}
	}
}

// TestRepoWorkflowIsClean keeps issue-viewer's own workflow.yaml free of
// schema, placeholder, section and workflow findings. (Its template stamp
// may lag the bundled template between releases.)
func TestRepoWorkflowIsClean(t *testing.T) {
	raw, err := os.ReadFile("../../workflow.yaml")
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Run(lintProject(t, string(raw), nil), "workflow", "schema", "placeholders", "sections")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range rep.Findings {
		t.Errorf("%s %s: %s", f.Check, f.Where, f.Message)
	}
}

func TestFileRunsTheFileChecksOnRawText(t *testing.T) {
	raw := []byte("statuses:\n  - name: idea\n    promt: \"typo\"\ntransitions: []\n")
	got, err := File("/nowhere/workflow.yaml", raw, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var schema []Finding
	for _, f := range got {
		if f.Check == "undated" || f.Check == "department" || f.Check == "departments" {
			t.Errorf("board check %s ran on a file", f.Check)
		}
		if f.Check == "schema" {
			schema = append(schema, f)
		}
	}
	if len(schema) != 1 || schema[0].Where != "workflow.yaml:3" || !strings.Contains(schema[0].Message, `unknown key "promt"`) {
		t.Errorf("schema findings = %+v", schema)
	}
	if _, err := File("w.yaml", []byte("statuses: [\n"), time.Now()); err == nil {
		t.Error("broken YAML parsed")
	}
}

// A box in a named N/A form passes; one out of form, or on the owner's
// box, is a warning. Closed issues are not read.
func TestNABoxes(t *testing.T) {
	issue := func(title, status, boxes string) string {
		return "---\ntitle: \"" + title + "\"\nstatus: " + status + "\ncreated: \"2026-10-30\"\n---\n\n## Testing\n" + boxes
	}
	env := lintProject(t, minimalWorkflow, map[string]string{
		"S/named.md": issue("named", "idea", "- [x] ux-test passed (N/A: no page changes)\n"+
			"- [x] Guided check (N/A: the repro is an automated test (TestStatsPageFastOnABigProject))\n"+
			"- [x] Runner check (N/A: runner-internal, covered by TestShotRetry)\n"+
			"- [x] Session (N/A: a read-only card covered by the template test)\n"+
			"- [x] Playtest session (N/A: cli flow covered by TestCheckNAMinimum)\n"+
			"- [x] ux-test (N/A: page state a scenario can't build (a live worker pane), shown by TestCrewPane)\n"+
			"- [ ] Open box (N/A:)\n"+
			"- [x] Manual checks come as scripts, or N/A with the reason\n"),
		"S/bad.md": issue("bad", "idea", "- [x] Guided check (N/A: the repro is an automated test)\n"+
			"- [x] ux-test passed (n/a: none)\n"+
			"- [x] Repro re-run (owner) (N/A: no page changes)\n"+
			"\n## Human Testing\n- [x] Human verification (N/A: no page changes)\n"),
		"S/closed.md": issue("closed", "done", "- [x] ux-test passed (N/A: none)\n"),
	})
	rep, err := Run(env, "na-boxes")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range rep.Findings {
		got = append(got, f.Where+": "+f.Message)
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		`s/bad T1: "the repro is an automated test" needs the test's name`,
		`s/bad T2: the N/A reason "none" is too short`,
		`s/bad T3: T3 is marked "(owner)" with an N/A: an N/A never skips the owner's box`,
		`s/bad HT1: HT1 is a Human Testing box with an N/A`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing finding %q in:\n%s", want, all)
		}
	}
	if len(got) != 4 {
		t.Errorf("want 4 findings (named forms and closed issues give none), got:\n%s", all)
	}
}

// The docs type, in this repo's workflow.yaml and the bundled development
// template, walks idea → in progress → shipping → done and writes no box
// about playtests, ux-test or check scripts.
func TestDocsTypeHasNoTestBoxes(t *testing.T) {
	dev, err := workflowupgrade.ReadTemplate("development")
	if err != nil {
		t.Fatal(err)
	}
	repo, err := os.ReadFile("../../workflow.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string][]byte{"development template": dev, "workflow.yaml": repo} {
		var wf tracker.WorkflowConfig
		if err := yaml.Unmarshal(raw, &wf); err != nil {
			t.Fatal(err)
		}
		if _, ok := wf.Types["docs"]; !ok {
			t.Errorf("%s: no docs type", name)
			continue
		}
		docs := wf.ForType("docs")
		var path []string
		for _, s := range docs.Statuses {
			if s.Name != "parked" {
				path = append(path, s.Name)
			}
		}
		if got := strings.Join(path, " → "); got != "idea → in progress → shipping → done" {
			t.Errorf("%s: docs path = %s", name, got)
		}
		for _, tr := range docs.Transitions {
			for _, a := range tr.Actions {
				if a.Type != "append_section" {
					continue
				}
				if a.Title == "Test Plan" || a.Title == "Testing" {
					t.Errorf("%s: docs %s → %s appends %s", name, tr.From, tr.To, a.Title)
				}
				low := strings.ToLower(a.Body)
				for _, word := range []string{"playtest", "ux-test", "check-script", "scripts/checks"} {
					if strings.Contains(low, word) {
						t.Errorf("%s: docs %s → %s box mentions %s:\n%s", name, tr.From, tr.To, word, a.Body)
					}
				}
			}
		}
	}
}

// The testing prompt, in this repo's workflow.yaml and the bundled
// development template, lists every named N/A form and check --na.
func TestTestingPromptListsTheNAForms(t *testing.T) {
	dev, err := workflowupgrade.ReadTemplate("development")
	if err != nil {
		t.Fatal(err)
	}
	repo, err := os.ReadFile("../../workflow.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string][]byte{"development template": dev, "workflow.yaml": repo} {
		var wf tracker.WorkflowConfig
		if err := yaml.Unmarshal(raw, &wf); err != nil {
			t.Fatal(err)
		}
		prompt := ""
		for _, s := range wf.Statuses {
			if s.Name == "testing" {
				prompt = s.Prompt
			}
		}
		if !strings.Contains(prompt, `issue-cli check <slug> <id> --na "<reason>"`) {
			t.Errorf("%s: the testing prompt doesn't name check --na", name)
		}
		for _, f := range tracker.NAForms[:len(tracker.NAForms)-1] {
			if !strings.Contains(prompt, "`"+f.Form+"`") {
				t.Errorf("%s: the testing prompt doesn't list the form %q", name, f.Form)
			}
		}
	}
}

func TestOwnerMarkerFlagsAnUnmarkedOwnerCheck(t *testing.T) {
	issue := func(title, status, boxes string) string {
		return "---\ntitle: \"" + title + "\"\nstatus: " + status + "\ncreated: \"2026-10-30\"\n---\n\n## Acceptance Criteria\n" + boxes
	}
	env := lintProject(t, minimalWorkflow, map[string]string{
		"S/open.md": issue("open", "idea", "- [ ] The page renders and the owner confirms it reads well\n"+
			"- [ ] Lists every box (owner)\n"+
			"- [ ] Owner visual check of the board (after ship)\n"+
			"- [ ] The `(owner)` marker counts anywhere (tests)\n"+
			"- [x] the owner looks at the shot\n"+
			"\n## Notes\n- [ ] the owner reviews the notes\n"),
		"S/closed.md": issue("closed", "done", "- [ ] the owner decides the price\n"),
	})
	rep, err := Run(env, "owner-marker")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Findings) != 1 || rep.Findings[0].Where != "s/open AC1" ||
		!strings.Contains(rep.Findings[0].Message, "reads like the owner's check") ||
		!strings.Contains(rep.Findings[0].Fix, "check-edit s/open AC1") || !strings.Contains(rep.Findings[0].Fix, "(owner)") {
		t.Fatalf("findings = %+v, want one on s/open AC1 with a check-edit fix adding (owner)", rep.Findings)
	}
}

// A validate rule this build doesn't know (a typo, or a rule main added
// after the installed build) is a warning naming it, at its line, with the
// branch build as the fix; known rules, with or without their argument, and
// structured validators pass, in transitions, overlays and types alike.
func TestSchemaUnknownRule(t *testing.T) {
	wf := minimalWorkflow + `      - type: validate
        rule: no-such-rule
      - type: validate
        rule: has_section
        section: Design
      - type: validate
        rule: "has_comment_prefix: tests:"
systems:
  CLI:
    transitions:
      - from: idea
        to: done
        actions:
          - type: validate
            rule: "later_rule: arg"
`
	fs := run(t, lintProject(t, wf, nil), "schema")
	if len(fs) != 2 {
		t.Fatalf("findings = %+v, want the two unknown rules", fs)
	}
	if !has(fs, "workflow.yaml:17 ", `rule "no-such-rule" is not a rule this build`, "go build -o ./issue-cli ./cmd/issue-cli", "never make install") {
		t.Errorf("no-such-rule: %+v", fs)
	}
	if !has(fs, "workflow.yaml:30 ", `rule "later_rule"`) {
		t.Errorf("overlay rule: %+v", fs)
	}
	if !UnknownRule(fs[0]) || !UnknownRule(fs[1]) {
		t.Errorf("UnknownRule misses its own findings: %+v", fs)
	}
	if fs := run(t, lintProject(t, minimalWorkflow, nil), "schema"); len(fs) != 0 {
		t.Errorf("known rules flagged: %+v", fs)
	}
}
