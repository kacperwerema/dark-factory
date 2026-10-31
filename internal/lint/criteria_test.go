package lint

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/michal-franc/issue-viewer/cmd/issue-cli/templates"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// criteriaWorkflow writes Acceptance Criteria and Must not on idea → in
// design, as the development template does.
const criteriaWorkflow = `template: custom
statuses:
  - name: idea
    description: raw
  - name: in design
    description: design
  - name: backlog
    description: ready
  - name: done
    description: shipped
transitions:
  - from: idea
    to: in design
    actions:
      - type: append_section
        title: Acceptance Criteria
        body: "- [ ] First observable requirement"
      - type: append_section
        title: Must not
        body: "- <what this change must not do; name a test or a command in backticks when one can catch it>"
  - from: in design
    to: backlog
    actions:
      - type: validate
        rule: "criteria_checkboxes: Acceptance Criteria"
  - from: backlog
    to: done
`

// criteriaFixtures reads testdata/criteria into the {"S/x.md": content}
// map lintProject takes.
func criteriaFixtures(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", "criteria", "S", "*.md"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	out := map[string]string{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out["S/"+filepath.Base(f)] = string(raw)
	}
	return out
}

func TestCriteriaFlagsUnfalsifiableAndPassesProvenOnes(t *testing.T) {
	env := lintProject(t, criteriaWorkflow, criteriaFixtures(t))
	var where []string
	byWhere := map[string]Finding{}
	for _, f := range run(t, env, "criteria") {
		where = append(where, f.Where)
		byWhere[f.Where] = f
	}
	sort.Strings(where)
	// unfalsifiable: AC1 "works", AC2 "is clear", AC3 no check named.
	// Not flagged: the template box AC4, the ticked AC5, the owner's AC6,
	// the N/A AC7; every box of falsifiable; the closed issue; the Must
	// not of an issue still in design or with a line written.
	want := []string{"s/mustnot-placeholder ## Must not", "s/unfalsifiable AC1", "s/unfalsifiable AC2", "s/unfalsifiable AC3"}
	if strings.Join(where, "|") != strings.Join(want, "|") {
		t.Fatalf("findings at %q, want %q", where, want)
	}
	if m := byWhere["s/unfalsifiable AC1"].Message; !strings.Contains(m, `says "works"`) {
		t.Errorf("AC1 message %q should quote the vague word", m)
	}
	if m := byWhere["s/unfalsifiable AC2"].Message; !strings.Contains(m, `says "is clear"`) {
		t.Errorf("AC2 message %q should quote the vague word", m)
	}
	if f := byWhere["s/unfalsifiable AC3"]; !strings.Contains(f.Message, "names no test, command or playtest step") ||
		!strings.Contains(f.Fix, "issue-cli check-edit s/unfalsifiable AC3") || !strings.Contains(f.Fix, "(owner)") {
		t.Errorf("AC3 finding %+v: want the plain message and a check-edit fix naming the (owner) way out", f)
	}
	if f := byWhere["s/mustnot-placeholder ## Must not"]; !strings.Contains(f.Fix, `--section "Must not"`) {
		t.Errorf("Must not finding %+v: want a replace --section fix", f)
	}
}

func TestFalsifiable(t *testing.T) {
	for text, want := range map[string]bool{
		"TestCriteriaFlags passes":             true,
		"covered by tests":                     true,
		"`go vet ./...` is clean":              true,
		"issue-cli status prints it":           true,
		"the playtest step shows it":           true,
		"the chart keeps its colours (visual)": true,
		"the command exits 1":                  true,
		"It works":                             false,
		"The flow is clear":                    false,
		"Departments get a summary line":       false,
	} {
		if got := Falsifiable(text); got != want {
			t.Errorf("Falsifiable(%q) = %v, want %v", text, got, want)
		}
	}
}

// The bundled development template and this repository's workflow.yaml
// append ## Must not on idea → in design, with the placeholder the lint
// and tracker.MustNot know.
func TestTemplatesCarryMustNot(t *testing.T) {
	raw, err := templates.FS.ReadFile("workflow/development.yaml")
	if err != nil {
		t.Fatal(err)
	}
	repo, err := os.ReadFile(filepath.Join("..", "..", "workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"development.yaml": raw, "workflow.yaml": repo} {
		var wf tracker.WorkflowConfig
		if err := yaml.Unmarshal(data, &wf); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		tr := wf.GetTransition("idea", "in design")
		if tr == nil {
			t.Fatalf("%s: no idea → in design edge", name)
		}
		found := false
		for _, a := range tr.Actions {
			if a.Type == "append_section" && a.Title == tracker.MustNotSection {
				found = true
				if !strings.Contains(a.Body, tracker.MustNotPlaceholder) || len(tracker.MustNot("## Must not\n"+a.Body)) != 0 {
					t.Errorf("%s: Must not body %q is not the placeholder tracker.MustNot drops", name, a.Body)
				}
			}
		}
		if !found {
			t.Errorf("%s: idea → in design appends no ## Must not", name)
		}
	}
}
