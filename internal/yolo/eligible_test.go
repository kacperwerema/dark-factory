package yolo

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/tracker/statustest"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

// bundled parses one bundled workflow template.
func bundled(t *testing.T, name string) *tracker.WorkflowConfig {
	t.Helper()
	raw, err := workflowupgrade.ReadTemplate(name)
	if err != nil {
		t.Fatal(err)
	}
	var wf tracker.WorkflowConfig
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return &wf
}

// The audit's concrete case: with human-testing optional, Gates listed
// testing → human-testing as passed while the transition refused it.
func TestGatesLeaveOutTheOptionalHumanTestingStep(t *testing.T) {
	wf := bundled(t, "development")
	for i := range wf.Statuses {
		if wf.Statuses[i].Name == "human-testing" {
			wf.Statuses[i].Optional = true
		}
	}
	for i := range wf.Transitions {
		if tr := &wf.Transitions[i]; tr.From == "testing" && tr.To == "human-testing" {
			tr.Actions = []tracker.WorkflowAction{{Type: "require_human_approval", Status: "human-testing"}}
		}
	}
	for _, g := range Gates(wf) {
		if g.To == "human-testing" {
			t.Fatalf("Gates lists %s → %s, which the transition refuses", g.From, g.To)
		}
	}
}

// withFeature is wf with one catalog entry's snippet laid over it: a status
// it names replaces the one of that name (or joins the end), an edge it
// names replaces that edge (or joins). false when the snippet changes no
// status, edge or type.
func withFeature(t *testing.T, wf *tracker.WorkflowConfig, snippet string) bool {
	t.Helper()
	var f tracker.WorkflowConfig
	if yaml.Unmarshal([]byte(snippet), &f) != nil || len(f.Statuses)+len(f.Transitions)+len(f.Types) == 0 {
		return false
	}
	for _, s := range f.Statuses {
		at := slices.IndexFunc(wf.Statuses, func(x tracker.WorkflowStatus) bool { return x.Name == s.Name })
		if at < 0 {
			wf.Statuses = append(wf.Statuses, s)
		} else {
			wf.Statuses[at] = s
		}
	}
	for _, tr := range f.Transitions {
		at := slices.IndexFunc(wf.Transitions, func(x tracker.WorkflowTransition) bool { return x.From == tr.From && x.To == tr.To })
		if at < 0 {
			wf.Transitions = append(wf.Transitions, tr)
		} else {
			wf.Transitions[at] = tr
		}
	}
	for name, typ := range f.Types {
		if wf.Types == nil {
			wf.Types = map[string]tracker.WorkflowType{}
		}
		wf.Types[name] = typ
	}
	return true
}

// checkSplit holds Gates and the never-passes list to the rule: on each
// path every approval step is in exactly one, and each list holds nothing
// else.
func checkSplit(t *testing.T, name string, wf *tracker.WorkflowConfig) {
	t.Helper()
	gates := map[string]bool{}
	for _, g := range Gates(wf) {
		gates[g.From+" → "+g.To] = true
	}
	owner := map[string]bool{}
	for _, g := range WorkflowOwnerGates(wf, "") {
		owner[g.From+" → "+g.To] = true
	}
	rules := WorkflowNeverRules(wf)
	passedSomewhere := map[string]bool{}
	eachPath(wf, func(scoped *tracker.WorkflowConfig, typ string) {
		order := scoped.GetStatusOrder()
		for i := 0; i+1 < len(order); i++ {
			from, to := order[i], order[i+1]
			step := from + " → " + to
			required := scoped.RequiredHumanApproval(from, to)
			if required == "" {
				continue
			}
			var never bool
			switch {
			case scoped.OwnerOnlyGate(from, to):
				never = owner[step]
			case scoped.GetStatus(to).Optional:
				never = slices.Contains(rules, tracker.YOLONeverOptional)
			}
			eligible := Eligible(scoped, nil, from, to)
			if eligible {
				passedSomewhere[step] = true
			}
			if eligible == never {
				t.Errorf("%s, path %q: %s: passes %v, on the never-passes list %v: it must be on exactly one", name, typ, step, eligible, never)
			}
			if eligible && !gates[step] {
				t.Errorf("%s, path %q: %s passes but Gates leaves it out", name, typ, step)
			}
		}
	})
	for step := range gates {
		if !passedSomewhere[step] {
			t.Errorf("%s: Gates lists %s, which the rule refuses on every path", name, step)
		}
	}
}

// Guard: for every bundled template, alone and with each feature overlay,
// the two lists split the approval steps.
func TestGatesAndOwnerGatesSplitTheApprovalSteps(t *testing.T) {
	entries, err := workflowupgrade.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	applied := map[string]bool{}
	for _, name := range workflowupgrade.Templates() {
		checkSplit(t, name, bundled(t, name))
		for _, e := range entries {
			wf := bundled(t, name)
			if withFeature(t, wf, e.Snippet) {
				applied[e.ID] = true
				checkSplit(t, name+" + "+e.ID, wf)
			}
		}
	}
	for _, id := range []string{"optional-statuses", "owner-only-gates"} {
		if !applied[id] {
			t.Errorf("feature %s never applied: its snippet no longer parses, so this guard stopped covering it", id)
		}
	}

	// The bundled workflow has the status: human-testing → documentation asks
	// for `documentation`, which YOLO passes.
	wf := bundled(t, "development")
	if !wf.HasRole("human-testing", tracker.StatusRoleOwnerTest) {
		t.Fatal("the bundled workflow lost the status this case is about")
	}
	if rules := WorkflowNeverRules(wf); slices.ContainsFunc(rules, func(r string) bool { return strings.Contains(r, "Human Testing") }) {
		t.Fatalf("never-passes rules %v name the Human Testing approval", rules)
	}
	if !slices.ContainsFunc(Gates(wf), func(g Gate) bool { return g.From == "human-testing" && g.To == "documentation" }) {
		t.Fatalf("Gates leaves out human-testing → documentation: %v", Gates(wf))
	}
	checkSplit(t, "the status, no gate", wf)
	checkSplit(t, "a gate asks for it", humanTestingGate(t))
}

// humanTestingGate is the bundled workflow with a gate that asks for the
// human's own approval, which no bundled template does; YOLO passes it too.
func humanTestingGate(t *testing.T) *tracker.WorkflowConfig {
	t.Helper()
	wf := bundled(t, "development")
	for i := range wf.Transitions {
		if tr := &wf.Transitions[i]; tr.From == "testing" && tr.To == "human-testing" {
			tr.Actions = append(tr.Actions, tracker.WorkflowAction{Type: "require_human_approval"})
			return wf
		}
	}
	t.Fatal("no testing → human-testing edge")
	return nil
}

// The transition and the list ask the same rule about a step into an
// optional status that isn't Human Testing, and the rule is on the list.
func TestOptionalGateIsOnTheNeverPassesList(t *testing.T) {
	wf := &tracker.WorkflowConfig{
		Statuses: []tracker.WorkflowStatus{{Name: "work"}, {Name: "review", Optional: true}, {Name: "done"}},
		Transitions: []tracker.WorkflowTransition{
			{From: "work", To: "review", Actions: []tracker.WorkflowAction{{Type: "require_human_approval"}}},
		},
	}
	if Eligible(wf, nil, "work", "review") || len(Gates(wf)) != 0 {
		t.Fatalf("a gate into an optional status passes: Gates %v", Gates(wf))
	}
	if !slices.Contains(WorkflowNeverRules(wf), tracker.YOLONeverOptional) {
		t.Fatalf("never-passes rules %v leave it out", WorkflowNeverRules(wf))
	}
}

func TestIsOwnerTestApproval(t *testing.T) {
	var wf *tracker.WorkflowConfig
	for _, s := range []string{"human-testing", "Human Testing", "HUMAN-TESTING", " human testing "} {
		if !wf.IsOwnerTestApproval(s) {
			t.Errorf("%q not matched", s)
		}
	}
	for _, s := range []string{"", "testing", "human-testing-2", "human_testing"} {
		if wf.IsOwnerTestApproval(s) {
			t.Errorf("%q matched", s)
		}
	}
}

// Guard: every reason OwnerGates can give is named in the briefing's list
// and quoted in yolo.md, and yolo.md quotes the rule a worker is given.
func TestYOLOOwnerTextsReadOneSource(t *testing.T) {
	reasons := []struct{ why, briefing string }{
		{tracker.YOLONeverOwnerOnly, "owner_only gates"},
		{tracker.YOLONeverOptional, "a gate into an optional status"},
		{tracker.YOLONeverExperiment, "experiments"},
		{tracker.YOLONeverDesignFirst, "the design gate of an issue labelled design-first"},
		{tracker.YOLONeverOwnerBoxFirst, "an (owner) box due before the build"},
		{tracker.YOLONeverRelease, "release publish"},
	}
	known := func(why string) bool {
		return slices.ContainsFunc(reasons, func(r struct{ why, briefing string }) bool { return r.why == why })
	}

	// A project whose workflow reaches every reason.
	raw, err := workflowupgrade.ReadTemplate("marketing")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "workflow.yaml")
	if err := os.WriteFile(file, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	gates := OwnerGates(&tracker.Project{WorkflowFile: file}, Valves{})
	wf := humanTestingGate(t)
	wf.Statuses = append(wf.Statuses, tracker.WorkflowStatus{Name: "side", Optional: true})
	wf.Transitions = append(wf.Transitions, tracker.WorkflowTransition{From: "testing", To: "side",
		Actions: []tracker.WorkflowAction{{Type: "require_human_approval"}}})
	for _, r := range WorkflowNeverRules(wf) {
		gates = append(gates, OwnerGate{Why: r})
	}
	seen := map[string]bool{}
	for _, g := range gates {
		if !known(g.Why) {
			t.Errorf("OwnerGates gives %q: add it to this table, YOLOOwnerOnly and yolo.md", g.Why)
		}
		seen[g.Why] = true
	}

	raw, err = os.ReadFile(filepath.Join("..", "..", "docs", "Workflow", "yolo.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	for _, r := range reasons {
		if !seen[r.why] {
			t.Errorf("no OwnerGates reason %q any more: drop it from the table", r.why)
		}
		if !strings.Contains(tracker.YOLOOwnerOnly(wf), r.briefing) {
			t.Errorf("YOLOOwnerOnly leaves out %q: %s", r.briefing, tracker.YOLOOwnerOnly(wf))
		}
		if !strings.Contains(doc, r.why) {
			t.Errorf("docs/Workflow/yolo.md doesn't quote the reason %q", r.why)
		}
	}
	if rule := tracker.YOLORule(wf, "<slug>"); !strings.Contains(doc, rule) {
		t.Errorf("docs/Workflow/yolo.md doesn't quote YOLORule(\"<slug>\"):\n%s", rule)
	}

	// The Human Testing approval passes in YOLO: no text names it as the
	// owner's, even where a gate asks for it.
	for name, w := range map[string]*tracker.WorkflowConfig{"a gate asks for it": wf, "the bundled workflow": bundled(t, "development")} {
		for _, r := range WorkflowNeverRules(w) {
			if strings.Contains(r, "Human Testing") {
				t.Errorf("%s: the never-passes list names it: %q", name, r)
			}
		}
		if s := tracker.YOLOOwnerOnly(w) + tracker.YOLORule(w, "<slug>"); strings.Contains(s, "Human Testing") {
			t.Errorf("%s: the briefing names it: %s", name, s)
		}
	}
}

// TestRenamedOwnerTestPassesInYOLO: the owner-test approval passes in YOLO
// under any name, and Gates lists it.
func TestRenamedOwnerTestPassesInYOLO(t *testing.T) {
	wf := statustest.Renamed()
	owner := statustest.Name(tracker.StatusRoleOwnerTest)
	verify := statustest.Name(tracker.StatusRoleVerify)
	for i := range wf.Transitions {
		if tr := &wf.Transitions[i]; tr.From == verify && tr.To == owner {
			tr.Actions = []tracker.WorkflowAction{{Type: "require_human_approval", Status: owner}}
		}
	}
	if !Eligible(wf, nil, verify, owner) {
		t.Fatal("YOLO refuses the renamed owner-test approval")
	}
	listed := false
	for _, g := range Gates(wf) {
		listed = listed || (g.From == verify && g.To == owner)
	}
	if !listed {
		t.Fatalf("Gates leaves out %s → %s: %+v", verify, owner, Gates(wf))
	}
}
