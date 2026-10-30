package wfedit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// fixture copies testdata/workflow.yaml into a temp dir and returns its path.
func fixture(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/workflow.yaml")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "workflow.yaml")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func str(s string) *string { return &s }

// changedLines is the -/+ lines of a unified diff, without the headers.
func changedLines(diff string) []string {
	var out []string
	for _, l := range strings.Split(diff, "\n") {
		if strings.HasPrefix(l, "---") || strings.HasPrefix(l, "+++") {
			continue
		}
		if strings.HasPrefix(l, "-") || strings.HasPrefix(l, "+") {
			out = append(out, l)
		}
	}
	return out
}

func TestOpenStation(t *testing.T) {
	st, err := Open(fixture(t), "in progress")
	if err != nil {
		t.Fatal(err)
	}
	if st.Description != "Being built" || st.Prompt != "Build it.\nKeep the test plan concrete.\n" || st.Line != 11 {
		t.Errorf("status = %q %q line %d", st.Description, st.Prompt, st.Line)
	}
	if len(st.In) != 1 || st.In[0].Label() != "idea → in progress" || !st.In[0].Gate() || len(st.In[0].Actions) != 3 {
		t.Errorf("in = %+v", st.In)
	}
	if len(st.Out) != 1 || st.Out[0].Label() != "in progress → testing" || st.Out[0].Gate() || st.Out[0].Index != 1 {
		t.Errorf("out = %+v", st.Out)
	}
	var ro []string
	for _, r := range st.ReadOnly {
		ro = append(ro, r.What)
	}
	want := "transitions: * → in progress|types.bugfix.statuses: in progress|types.bugfix.transitions: idea → in progress"
	if got := strings.Join(ro, "|"); got != want {
		t.Errorf("read-only = %s, want %s", got, want)
	}
	if _, err := Open(fixture(t), "nope"); err == nil || !strings.Contains(err.Error(), `no status "nope"`) {
		t.Errorf("unknown station err = %v", err)
	}
}

func TestApplyPromptTouchesOnlyThePrompt(t *testing.T) {
	path := fixture(t)
	st, _ := Open(path, "in progress")
	pv, err := Apply(path, "in progress", st.Hash, Change{Prompt: str("Build it.\r\nWrite the tests first.")})
	if err != nil {
		t.Fatal(err)
	}
	if !pv.OK() || len(pv.Errors) > 0 {
		t.Fatalf("errors = %v", pv.Errors)
	}
	got := strings.Join(changedLines(pv.Diff), "\n")
	want := "-      Keep the test plan concrete.\n+      Write the tests first."
	if got != want {
		t.Errorf("diff changes:\n%s\nwant:\n%s\nfull:\n%s", got, want, pv.Diff)
	}
	if pv.Summary != "prompt edited" || len(pv.Gates) != 0 {
		t.Errorf("summary %q gates %v", pv.Summary, pv.Gates)
	}
	// The line comment on description: stays.
	if !strings.Contains(string(pv.New), `description: "Being built"   # the main station`) {
		t.Error("the description's line comment was lost")
	}
}

func TestApplyPromptAddedAndRemoved(t *testing.T) {
	path := fixture(t)
	pv, err := Apply(path, "testing", "", Change{Prompt: str("Verify it.")})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(changedLines(pv.Diff), "\n"); got != `+    prompt: "Verify it."` {
		t.Errorf("added prompt diff:\n%s", pv.Diff)
	}
	pv, _ = Apply(path, "idea", "", Change{Prompt: str("  ")})
	if got := strings.Join(changedLines(pv.Diff), "\n"); got != "-    prompt: |\n-      Clarify the idea." || pv.Summary != "prompt removed" {
		t.Errorf("removed prompt %q diff:\n%s", pv.Summary, pv.Diff)
	}
	pv, _ = Apply(path, "idea", "", Change{Description: str("Raw idea, to clarify")})
	if got := strings.Join(changedLines(pv.Diff), "\n"); got != "-    description: \"Raw idea\"\n+    description: \"Raw idea, to clarify\"" {
		t.Errorf("description diff:\n%s", pv.Diff)
	}
}

func TestApplyGateToggle(t *testing.T) {
	path := fixture(t)
	st, _ := Open(path, "in progress")
	in := st.In[0]
	pv, err := Apply(path, "in progress", st.Hash, Change{Edges: map[int][]tracker.WorkflowAction{in.Index: SetGate(in.Actions, in.To, false)}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(changedLines(pv.Diff), "\n"); got != "-      - type: \"require_human_approval\"\n-        status: \"in progress\"" {
		t.Errorf("gate off diff:\n%s", pv.Diff)
	}
	if pv.Summary != "gate into in progress removed" || len(pv.Gates) != 1 {
		t.Errorf("summary %q gates %v", pv.Summary, pv.Gates)
	}
	// The comment inside the actions list stays.
	if !strings.Contains(string(pv.New), "      # Sections the builder fills in.\n      - type: \"append_section\"") {
		t.Errorf("comment in actions lost:\n%s", pv.New)
	}

	out := st.Out[0]
	pv, _ = Apply(path, "in progress", st.Hash, Change{Edges: map[int][]tracker.WorkflowAction{out.Index: SetGate(out.Actions, out.To, true)}})
	want := "+      - type: \"require_human_approval\"\n+        status: \"testing\""
	if got := strings.Join(changedLines(pv.Diff), "\n"); got != want || pv.Summary != "gate into testing added" {
		t.Errorf("gate on %q diff:\n%s", pv.Summary, pv.Diff)
	}
	// Checks first, side effects after: the gate lands before inject_prompt.
	if !strings.Contains(string(pv.New), "status: \"testing\"\n      - type: \"inject_prompt\"") {
		t.Errorf("gate not before inject_prompt:\n%s", pv.New)
	}
}

func TestApplyReorderAddRemove(t *testing.T) {
	path := fixture(t)
	st, _ := Open(path, "in progress")
	out := st.Out[0]
	acts := []tracker.WorkflowAction{
		out.Actions[1],
		{Type: "validate", Rule: "has_test_plan"},
		out.Actions[0],
	}
	pv, err := Apply(path, "in progress", st.Hash, Change{Edges: map[int][]tracker.WorkflowAction{out.Index: acts}})
	if err != nil || !pv.OK() {
		t.Fatalf("err %v errors %v", err, pv.Errors)
	}
	if pv.Summary != "actions on in progress → testing edited" {
		t.Errorf("summary %q", pv.Summary)
	}
	again, _ := Open(writeTemp(t, pv.New), "in progress")
	if got := again.Out[0].Actions; len(got) != 3 || got[0].Type != "inject_prompt" || got[1].Rule != "has_test_plan" || got[2].Rule != "section_checkboxes_checked: Implementation" {
		t.Errorf("actions after = %+v", got)
	}
	// Other edges and the file's tail are byte for byte.
	if !strings.HasSuffix(string(pv.New), "            rule: \"has_assignee\"\n# The end of the file.\n") {
		t.Errorf("tail changed:\n%s", pv.New)
	}

	// Removing every action writes actions: [].
	pv, _ = Apply(path, "in progress", st.Hash, Change{Edges: map[int][]tracker.WorkflowAction{out.Index: nil}})
	if !pv.OK() || !strings.Contains(string(pv.New), "    to: \"testing\"\n    actions: []\n") {
		t.Errorf("errors %v new:\n%s", pv.Errors, pv.New)
	}
}

func writeTemp(t *testing.T, raw []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "workflow.yaml")
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestApplyRefusesBadActions(t *testing.T) {
	path := fixture(t)
	st, _ := Open(path, "in progress")
	i := st.Out[0].Index
	for name, tc := range map[string]struct {
		act  tracker.WorkflowAction
		want string
	}{
		"type":     {tracker.WorkflowAction{Type: "launch_rocket"}, `unknown action type "launch_rocket"`},
		"rule":     {tracker.WorkflowAction{Type: "validate", Rule: "has_vibes"}, `unknown validation rule "has_vibes"`},
		"no rule":  {tracker.WorkflowAction{Type: "validate"}, "validate needs a rule"},
		"status":   {tracker.WorkflowAction{Type: "require_human_approval", Status: "shipping"}, `approval for "shipping", a status the file lacks`},
		"title":    {tracker.WorkflowAction{Type: "append_section"}, "append_section needs a title"},
		"prompt":   {tracker.WorkflowAction{Type: "inject_prompt"}, "inject_prompt needs a prompt"},
		"set_none": {tracker.WorkflowAction{Type: "set_fields"}, "set_fields needs a field"},
	} {
		t.Run(name, func(t *testing.T) {
			pv, err := Apply(path, "in progress", st.Hash, Change{Edges: map[int][]tracker.WorkflowAction{i: {tc.act}}})
			if err != nil {
				t.Fatal(err)
			}
			if pv.OK() || len(pv.Errors) != 1 || !strings.Contains(pv.Errors[0], tc.want) || !strings.HasPrefix(pv.Errors[0], "in progress → testing, action 1: ") {
				t.Errorf("errors = %v, want %q", pv.Errors, tc.want)
			}
		})
	}
	if _, err := Apply(path, "in progress", st.Hash, Change{Edges: map[int][]tracker.WorkflowAction{2: nil}}); err == nil {
		t.Error("an edge not at the station was accepted")
	}
}

func TestApplyStale(t *testing.T) {
	path := fixture(t)
	st, _ := Open(path, "idea")
	raw, _ := os.ReadFile(path)
	os.WriteFile(path, append(raw, '\n'), 0o644)
	if _, err := Apply(path, "idea", st.Hash, Change{Prompt: str("x")}); !errors.Is(err, ErrStale) {
		t.Errorf("err = %v, want ErrStale", err)
	}
}

func TestApplyNoChange(t *testing.T) {
	path := fixture(t)
	st, _ := Open(path, "in progress")
	pv, err := Apply(path, "in progress", st.Hash, Change{Prompt: str(st.Prompt), Description: str(st.Description), Edges: map[int][]tracker.WorkflowAction{st.In[0].Index: st.In[0].Actions}})
	if err != nil {
		t.Fatal(err)
	}
	if pv.OK() || pv.Diff != "" || pv.Summary != "" {
		t.Errorf("a no-op edit: ok %v summary %q diff:\n%s", pv.OK(), pv.Summary, pv.Diff)
	}
}

// realFiles are the workflow files this repo ships: its own, the demo's and
// the bundled templates.
func realFiles(t *testing.T) []string {
	t.Helper()
	files, _ := filepath.Glob("../../cmd/issue-cli/templates/workflow/*.yaml")
	files = append(files, "../../workflow.yaml", "../../demo/workflow.yaml", "testdata/workflow.yaml")
	return files
}

// Every status and transition item of every shipped workflow file
// re-encodes to its own bytes: what lets an edit change only its lines.
func TestItemsReencodeByteForByte(t *testing.T) {
	for _, path := range realFiles(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parse(path, raw)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		check := func(name string, seq *yaml.Node, end int) {
			for i, it := range seq.Content {
				sp := f.itemSplice(seq, i, end, it)
				if got, want := strings.Join(sp.lines, "\n"), strings.Join(f.lines[sp.start:sp.end], "\n"); got != want {
					t.Errorf("%s %s[%d] (line %d) re-encodes differently:\n--- file\n%s\n--- encoded\n%s", path, name, i, it.Line, want, got)
				}
			}
		}
		check("statuses", f.statuses, f.statusesEnd)
		if f.transitions != nil {
			check("transitions", f.transitions, f.transitionsEnd)
		}
	}
}

// On every shipped file, toggling the gate on every edge into every
// station changes only that gate's lines and reads back as the change.
func TestGateToggleOnRealFiles(t *testing.T) {
	for _, path := range realFiles(t) {
		raw, _ := os.ReadFile(path)
		f, err := parse(path, raw)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range f.statuses.Content {
			name := scalar(s, "name")
			st, err := f.station(name)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range st.In {
				on := !e.Gate()
				pv, err := Apply(path, name, st.Hash, Change{Edges: map[int][]tracker.WorkflowAction{e.Index: SetGate(e.Actions, e.To, on)}})
				if err != nil {
					t.Fatalf("%s %s: %v", path, e.Label(), err)
				}
				if len(pv.Errors) > 0 {
					t.Errorf("%s %s: errors %v", path, e.Label(), pv.Errors)
					continue
				}
				for _, l := range changedLines(pv.Diff) {
					body := strings.TrimSpace(l[1:])
					// A gate that was the edge's only action leaves actions: [].
					if body != `- type: "require_human_approval"` && body != "- type: require_human_approval" && !strings.HasPrefix(body, "status: ") && body != "actions:" && body != "actions: []" {
						t.Errorf("%s %s (gate on=%v): line outside the gate changed: %q\n%s", path, e.Label(), on, l, pv.Diff)
						break
					}
				}
			}
		}
	}
}

func TestUnifiedDiff(t *testing.T) {
	old := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm\n"
	new := "a\nB\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm\nn\n"
	got := unifiedDiff("w.yaml", old, new)
	want := "--- a/w.yaml\n+++ b/w.yaml\n" +
		"@@ -1,5 +1,5 @@\n a\n-b\n+B\n c\n d\n e\n" +
		"@@ -11,4 +11,5 @@\n k\n l\n m\n+n\n \n"
	if got != want {
		t.Errorf("diff:\n%s\nwant:\n%s", got, want)
	}
	if unifiedDiff("w", "x", "x") != "" {
		t.Error("equal texts diff")
	}
}
