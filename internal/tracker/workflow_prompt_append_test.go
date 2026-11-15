package tracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// promptAppendWorkflow: a base in progress prompt, a tweak type that
// replaces it, and an API system overlay that appends to it.
const promptAppendWorkflow = `
statuses:
  - name: "idea"
  - name: "in progress"
    prompt: "BASE: implement the design."
  - name: "done"
transitions:
  - from: "idea"
    to: "in progress"
  - from: "in progress"
    to: "done"
types:
  feature:
    path: ["idea", "in progress", "done"]
  tweak:
    path: ["idea", "in progress", "done"]
    statuses:
      - name: "in progress"
        prompt: "TWEAK: keep it small."
systems:
  API:
    statuses:
      - name: "in progress"
        prompt_append: |
          API: run make security-check.
  UI:
    statuses:
      - name: "in progress"
        prompt: "UI: replaces."
        prompt_append: "UI: and appends."
`

func loadPromptAppend(t *testing.T) *WorkflowConfig {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workflow.yaml")
	if err := os.WriteFile(path, []byte(promptAppendWorkflow), 0o644); err != nil {
		t.Fatal(err)
	}
	wf, err := LoadWorkflow(path)
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

// TestPromptAppendKeepsTheTypePrompt: a system overlay's prompt_append adds
// to the prompt the type set (or the base's), at start, in the entry
// guidance and in the dispatch briefing.
func TestPromptAppendKeepsTheTypePrompt(t *testing.T) {
	wf := loadPromptAppend(t)
	for _, c := range []struct{ typ, want string }{
		{"tweak", "TWEAK: keep it small.\nAPI: run make security-check."},
		{"feature", "BASE: implement the design.\nAPI: run make security-check."},
	} {
		issue := &Issue{Slug: "api/x", Title: "x", Type: c.typ, System: "API", Status: "in progress"}
		scoped := wf.ForIssue(issue)
		if got := scoped.StatusPrompt("in progress"); got != c.want {
			t.Errorf("%s + API StatusPrompt = %q, want %q", c.typ, got, c.want)
		}
		if got := strings.Join(scoped.EntryPrompts("idea", "in progress"), "\n"); !strings.Contains(got, c.want) {
			t.Errorf("%s + API entry guidance = %q, want it to hold %q", c.typ, got, c.want)
		}
		if got := BuildAgentPrompt(nil, issue, scoped, "", ""); !strings.Contains(got, c.want) {
			t.Errorf("%s + API briefing lacks %q:\n%s", c.typ, c.want, got)
		}
	}
	// Without the system the type's prompt stands alone.
	if got := wf.ForIssue(&Issue{Type: "tweak", Status: "in progress"}).StatusPrompt("in progress"); got != "TWEAK: keep it small." {
		t.Errorf("tweak without a system = %q", got)
	}
}

// TestPromptAppendWithPrompt: an overlay setting both replaces the prompt
// and still appends after it.
func TestPromptAppendWithPrompt(t *testing.T) {
	got := loadPromptAppend(t).ForIssue(&Issue{Type: "tweak", System: "UI", Status: "in progress"}).StatusPrompt("in progress")
	if want := "UI: replaces.\nUI: and appends."; got != want {
		t.Errorf("StatusPrompt = %q, want %q", got, want)
	}
}

// TestPromptAppendMergedTwiceShowsOnce: the same append merged twice (an
// overlay over an overlay that already appends it) shows once; a different
// one follows it, and a later prompt: doesn't drop either.
func TestPromptAppendMergedTwiceShowsOnce(t *testing.T) {
	wf := loadPromptAppend(t).ForType("tweak")
	add := func(s WorkflowStatus) { wf.Merge(&WorkflowConfig{Statuses: []WorkflowStatus{s}}) }
	add(WorkflowStatus{Name: "in progress", PromptAppend: "A: one."})
	add(WorkflowStatus{Name: "in progress", PromptAppend: "A: one.\n"})
	add(WorkflowStatus{Name: "in progress", PromptAppend: "B: two."})
	add(WorkflowStatus{Name: "in progress", Prompt: "NEW."})
	if got, want := wf.StatusPrompt("in progress"), "NEW.\nA: one.\nB: two."; got != want {
		t.Errorf("StatusPrompt = %q, want %q", got, want)
	}
	// "A" alone is not a whole line already there: it is added.
	add(WorkflowStatus{Name: "in progress", PromptAppend: "A"})
	if got := wf.StatusPrompt("in progress"); !strings.HasSuffix(got, "\nA") {
		t.Errorf("a prefix of an existing line was skipped: %q", got)
	}
}

// TestPromptAppendThroughSystemsMerge: a project file's systems: block
// merged onto one that already appends keeps both as appends.
func TestPromptAppendThroughSystemsMerge(t *testing.T) {
	wf := loadPromptAppend(t)
	wf.Merge(&WorkflowConfig{Systems: map[string]WorkflowOverlay{
		"API": {Statuses: []WorkflowStatus{{Name: "in progress", PromptAppend: "API: also docs/API."}}},
	}})
	got := wf.ForIssue(&Issue{Type: "tweak", System: "API", Status: "in progress"}).StatusPrompt("in progress")
	if want := "TWEAK: keep it small.\nAPI: run make security-check.\nAPI: also docs/API."; got != want {
		t.Errorf("StatusPrompt = %q, want %q", got, want)
	}
}
