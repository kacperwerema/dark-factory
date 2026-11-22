package workflowupgrade

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// TestEveryWorkflowFeatureHasACatalogEntry fails when workflow.yaml gains a
// key, a validator rule or an action type that no catalog entry detects or
// teaches: such a feature would be invisible to `issue-cli workflow upgrade`.
func TestEveryWorkflowFeatureHasACatalogEntry(t *testing.T) {
	entries, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}

	keys, err := WorkflowKeys()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, k := range keys {
		seen[k] = true
		_, allowed := NonFeatureKeys[k]
		switch covered := KeyCovered(entries, k); {
		case covered && allowed:
			t.Errorf("%s is in NonFeatureKeys but an entry covers it now; drop the allowlist line", k)
		case !covered && !allowed:
			t.Error(UncoveredKeyProblem(entries, k))
		}
	}
	for k := range NonFeatureKeys {
		if !seen[k] {
			t.Errorf("NonFeatureKeys has %s, which is no longer a workflow.yaml key; drop the line", k)
		}
	}

	for _, r := range tracker.WorkflowValidationRules {
		name, _ := tracker.SplitRule(r.Name)
		if !ruleCovered(entries, name) {
			t.Errorf("validator rule %s has no catalog entry detecting it: add %s to the rule_in: of the entry whose probe matches the rule's statuses[]/transitions[] path (or give a new entry a probe with rule_in: [%s]), in %s/<id>.yaml", name, name, name, CatalogPath)
		}
	}
	for _, e := range entries {
		for name := range ruleNames(e) {
			if !tracker.KnownRule(name) {
				t.Errorf("catalog entry %s detects rule_in %s, which tracker.WorkflowValidationRules doesn't list", e.ID, name)
			}
		}
	}
	for _, a := range tracker.WorkflowActionTypes {
		if !actionCovered(entries, a.Name) {
			t.Errorf("action type %s has no catalog entry probing transitions[].actions[].type: add it to that probe's one_of: in the entry that teaches it, or a new entry file, in %s/<id>.yaml (docs/Workflow/overview.md, Feature catalog)", a.Name, CatalogPath)
		}
	}
}

func ruleCovered(entries []Entry, rule string) bool {
	for _, e := range entries {
		for _, p := range e.Detect {
			if containsString(p.RuleIn, rule) {
				return true
			}
		}
	}
	return false
}

func actionCovered(entries []Entry, action string) bool {
	for _, e := range entries {
		for _, p := range e.Detect {
			if p.Path == "transitions[].actions[].type" && (containsString(p.OneOf, action) || p.Equals == action) {
				return true
			}
			// Only a validate action has a rule, so a rule probe covers it.
			if action == "validate" && p.Path == "transitions[].actions[].rule" {
				return true
			}
		}
	}
	return false
}

func TestGuardCatchesANewKey(t *testing.T) {
	entries, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"ci.run", "health.limits", "workbench.pages", "leads.project", "leads.project_addendum", "ci.run[].results"} {
		if !KeyCovered(entries, k) {
			t.Errorf("%s should be covered", k)
		}
	}
	if KeyCovered(entries, "workbench.not_a_feature_yet") || ruleCovered(entries, "not_a_rule_yet") || actionCovered(entries, "not_an_action_yet") {
		t.Error("an unknown key, rule or action counts as covered")
	}
}

// Every entry names the code behind it, and every source exists, so
// catalog-check can ask for a version bump when that code changes.
// catalog-check runs the same CatalogProblems on a branch head.
func TestEveryEntryHasSourcesThatExist(t *testing.T) {
	entries, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	root := repoRoot(t)
	read := func(p string) ([]byte, bool) {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		return data, err == nil
	}
	for _, p := range CatalogProblems(entries, trackedFiles(t, root), read) {
		t.Error(p)
	}
}

// Each problem names its fix: the folder's ** for a bare folder, @prompt
// alone, a known category.
func TestCatalogProblemsNameTheFix(t *testing.T) {
	files := []string{"internal/x/a.go", "internal/x/sub/b.go", "internal/y/y.go"}
	cases := []struct {
		entry Entry
		want  string
	}{
		{Entry{ID: "bare", Category: "board", Sources: []string{"internal/x/"}}, "write internal/x/** for every file under it"},
		{Entry{ID: "mixed", Category: "board", Sources: []string{PromptSource, "internal/y/y.go"}}, `keep sources: ["@prompt"] alone`},
		{Entry{ID: "odd", Category: "boards", Sources: []string{"internal/y/y.go"}}, "is not one of lifecycle, gates"},
		{Entry{ID: "gone", Category: "board", Sources: []string{"internal/z/**"}}, "matches no tracked file"},
	}
	for _, c := range cases {
		got := strings.Join(CatalogProblems([]Entry{c.entry}, files, nil), "\n")
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: problems %q, want %q", c.entry.ID, got, c.want)
		}
	}
	if got := CatalogProblems([]Entry{{ID: "ok", Category: "board", Sources: []string{"internal/x/**"}}}, files, nil); len(got) > 0 {
		t.Errorf("a folder's ** is fine: %v", got)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, CatalogPath)); err != nil {
		t.Fatalf("repo root %s: %v", root, err)
	}
	return root
}

func trackedFiles(t *testing.T, root string) []string {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "ls-files", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		t.Skipf("git ls-files: %v", err)
	}
	files := strings.Split(strings.TrimSpace(string(out)), "\n")
	sort.Strings(files)
	return files
}

func TestSetupStepsSayWhoDoesThem(t *testing.T) {
	entries, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		for i, st := range e.Setup {
			if st.Who != "owner" && st.Who != "agent" {
				t.Errorf("%s: setup[%d].who %q is not one of %v", e.ID, i, st.Who, SetupWho)
			}
			if strings.TrimSpace(st.Do) == "" {
				t.Errorf("%s: setup[%d].do is empty", e.ID, i)
			}
		}
	}
}
