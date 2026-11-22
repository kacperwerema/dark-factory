package workflowupgrade

import (
	"strings"
	"testing"
)

// catalogWithout is the bundled catalog with the probes and snippets that
// cover key taken out: the catalog a branch has when it adds key to
// workflow.yaml without a detect: path.
func catalogWithout(t *testing.T, key string) []Entry {
	t.Helper()
	entries, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	var out []Entry
	for _, e := range entries {
		var probes []Probe
		for _, p := range e.Detect {
			if p.Path != key && !strings.HasPrefix(p.Path, key+".") {
				probes = append(probes, p)
			}
		}
		e.Detect = probes
		if KeyCovered([]Entry{{Snippet: e.Snippet}}, key) {
			e.Snippet = ""
		}
		out = append(out, e)
	}
	if KeyCovered(out, key) {
		t.Fatalf("%s still covered", key)
	}
	return out
}

// catalog-check names a workflow.yaml key no entry detects or teaches,
// with the detect: line to add, the entries already probing its block,
// and the allowlist for a tuning knob.
func TestKeyProblemsNameANewKeyWithItsFix(t *testing.T) {
	entries := catalogWithout(t, "leads.project_addendum")
	got, err := KeyProblems(entries, func() []byte { return []byte("Project string `yaml:\"project_addendum,omitempty\"`") })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("problems = %q, want the one key", got)
	}
	for _, want := range []string{
		"workflow.yaml key leads.project_addendum has no catalog entry: no detect: path starts at it",
		"Fix: add `- path: leads.project_addendum` under detect: of the entry that teaches it, in " + CatalogPath + "/<id>.yaml (entries probing its block: ",
		"then issue-cli workflow catalog-bump <id>",
		"a line in NonFeatureKeys in " + KeysFile,
	} {
		if !strings.Contains(got[0], want) {
			t.Errorf("problem %q lacks %q", got[0], want)
		}
	}
}

// A key whose yaml tag the checkout's internal/tracker lacks is this
// build's, not the branch's: catalog-check asks nothing about it.
func TestKeyProblemsSkipAKeyTheCheckoutLacks(t *testing.T) {
	entries := catalogWithout(t, "leads.project_addendum")
	got, err := KeyProblems(entries, func() []byte { return []byte("Other string `yaml:\"other\"`") })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("problems = %q, want none", got)
	}
}

// CheckBranch carries the key problems: a branch head whose tracker
// declares a key the head's catalog doesn't cover fails the check.
func TestCheckBranchNamesAnUncoveredKey(t *testing.T) {
	b := fakeBranch(nil, map[string]string{}, map[string]string{
		"internal/tracker/workflow_config.go": "package tracker\n\ntype X struct {\n\tA string `yaml:\"ci\"`\n}\n",
	})
	dir := b.HeadDir
	b.HeadDir = func(d string) []string {
		if d == "internal/tracker" {
			return []string{"workflow_config.go", "x_test.go"}
		}
		return dir(d)
	}
	rep, err := CheckBranch(b, "v0.53.0")
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK() || !strings.Contains(strings.Join(rep.Problems, "\n"), "workflow.yaml key ci has no catalog entry") {
		t.Errorf("problems = %q", rep.Problems)
	}
}
