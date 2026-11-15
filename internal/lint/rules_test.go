package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/rules"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func rulesFindings(t *testing.T, machine string, project []tracker.ProjectRule) []Finding {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if machine != "" {
		p := rules.MachinePath()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(machine), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return checkRules(&Env{Workflow: &tracker.WorkflowConfig{Rules: project}, Raw: []byte("rules:\n"), Now: time.Now()})
}

const machineRules = "# Rules\n- [no-force] Never force-push main.\n- Keep commits small and on one topic.\n"

// AC2: lint names a project rule that contradicts a machine one, and stops
// once the rule says it narrows it.
func TestRulesConflict(t *testing.T) {
	byWording := rulesFindings(t, machineRules, []tracker.ProjectRule{{ID: "deploy", Text: "Always force-push main before a release."}})
	if len(byWording) != 1 || !strings.Contains(byWording[0].Message, "project rule deploy contradicts machine rule [no-force]") || !strings.Contains(byWording[0].Fix, "narrows: no-force") {
		t.Fatalf("by wording: %+v", byWording)
	}
	byName := rulesFindings(t, machineRules, []tracker.ProjectRule{{ID: "no-force", Text: "Rebase before a merge."}})
	if len(byName) != 1 || !strings.Contains(byName[0].Message, "takes the name of machine rule [no-force]") {
		t.Fatalf("by name: %+v", byName)
	}
	unkeyed := rulesFindings(t, machineRules, []tracker.ProjectRule{{Text: "Don't keep commits small."}})
	if len(unkeyed) != 1 || !strings.Contains(unkeyed[0].Message, "machine rule line 3") || !strings.Contains(unkeyed[0].Fix, "give that rule a [key]") {
		t.Fatalf("unkeyed: %+v", unkeyed)
	}
	declared := rulesFindings(t, machineRules, []tracker.ProjectRule{{Text: "Always force-push main before a release.", Narrows: "no-force"}})
	if len(declared) != 0 {
		t.Fatalf("declared narrowing: %+v", declared)
	}
}

func TestRulesFindingsNameTheLineNotTheText(t *testing.T) {
	for _, f := range rulesFindings(t, machineRules, []tracker.ProjectRule{{Text: "Don't keep commits small."}, {Text: "Always force-push main."}}) {
		if strings.Contains(f.Message+f.Fix, "on one topic") || strings.Contains(f.Message+f.Fix, "Never force-push") {
			t.Errorf("a machine rule's words in a finding: %+v", f)
		}
	}
}

func TestRulesFileProblems(t *testing.T) {
	got := rulesFindings(t, "- [a] one\n- [a] two\nA stray paragraph.\n- [] \n", []tracker.ProjectRule{
		{Text: " "},
		{ID: "x", Text: "first"},
		{ID: "x", Text: "second", Narrows: "gone"},
	})
	var all []string
	for _, f := range got {
		all = append(all, f.Where+" "+f.Message)
	}
	text := strings.Join(all, "\n")
	for _, want := range []string{"rules.md:2 [a] names two rules", "rules.md:3 this line is not a rule", "rule 1 has no text", `two rules have the id "x"`, "rule 3 narrows [gone]"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
}

func TestRulesOverTheCap(t *testing.T) {
	long := strings.Repeat("word ", rules.BriefChars/5)
	got := rulesFindings(t, "- "+long+"\n- more\n", nil)
	if len(got) != 1 || !strings.Contains(got[0].Message, "don't fit the briefing") {
		t.Fatalf("%+v", got)
	}
}

func TestNoRulesNoFindings(t *testing.T) {
	if got := rulesFindings(t, "", nil); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}
