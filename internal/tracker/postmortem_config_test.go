package tracker

import (
	"strings"
	"testing"
)

func autoTypesWorkflow(types map[string]WorkflowType, autoTypes ...string) *WorkflowConfig {
	return &WorkflowConfig{
		Statuses:    []WorkflowStatus{{Name: "shipping"}, {Name: "done"}},
		Types:       types,
		DefaultType: "feature",
		Postmortem:  &PostmortemConfig{Start: PostmortemAuto, AutoTypes: autoTypes},
	}
}

// AC3: an auto_types name the workflow's types: doesn't define is an error,
// and Lint (the workflow check of health lint) warns with it.
func TestCheckPostmortemRefusesAnUnknownAutoType(t *testing.T) {
	types := map[string]WorkflowType{"feature": {}, "tweak": {}, "epic": {}}
	wf := autoTypesWorkflow(types, "feature", "tweek")
	err := wf.CheckPostmortem()
	if err == nil || !strings.Contains(err.Error(), `postmortem: auto_types: "tweek" is not a type of the workflow (types: feature, epic, tweak)`) {
		t.Fatalf("CheckPostmortem = %v", err)
	}
	found := false
	for _, w := range wf.Lint() {
		if strings.Contains(w, `auto_types: "tweek"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("Lint has no auto_types warning: %q", wf.Lint())
	}
	if err := autoTypesWorkflow(types, "feature", "epic").CheckPostmortem(); err != nil {
		t.Fatalf("known types refused: %v", err)
	}
	noTypes := autoTypesWorkflow(nil, "feature")
	if err := noTypes.CheckPostmortem(); err == nil || !strings.Contains(err.Error(), "it has no types:") {
		t.Fatalf("no types: CheckPostmortem = %v", err)
	}
}

func TestPostmortemAutoFor(t *testing.T) {
	types := map[string]WorkflowType{"feature": {}, "tweak": {}, "epic": {}, "chore": {}}
	listed := autoTypesWorkflow(types, "feature", "epic")
	for typ, want := range map[string]bool{"feature": true, "Epic": true, "tweak": false, "chore": false, "": true} {
		if got := listed.PostmortemAutoFor(typ); got != want {
			t.Errorf("listed: PostmortemAutoFor(%q) = %v, want %v", typ, got, want)
		}
	}
	unset := autoTypesWorkflow(types)
	for typ, want := range map[string]bool{"feature": true, "tweak": true, "chore": false} {
		if got := unset.PostmortemAutoFor(typ); got != want {
			t.Errorf("unset: PostmortemAutoFor(%q) = %v, want %v", typ, got, want)
		}
	}
	ask := autoTypesWorkflow(types, "feature")
	ask.Postmortem.Start = PostmortemAsk
	if ask.PostmortemAutoFor("feature") {
		t.Error("start: ask started a listed type")
	}
}
