package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/michal-franc/issue-viewer/internal/outputs"
	"github.com/michal-franc/issue-viewer/internal/tracker"
	"github.com/michal-franc/issue-viewer/internal/workflowupgrade"
)

// bothDevWorkflows is the bundled development template and this repo's
// workflow.yaml, parsed.
func bothDevWorkflows(t *testing.T) map[string]*tracker.WorkflowConfig {
	t.Helper()
	dev, err := workflowupgrade.ReadTemplate("development")
	if err != nil {
		t.Fatal(err)
	}
	repo, err := os.ReadFile("../../workflow.yaml")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*tracker.WorkflowConfig{}
	for name, raw := range map[string][]byte{"development template": dev, "workflow.yaml": repo} {
		var wf tracker.WorkflowConfig
		if err := yaml.Unmarshal(raw, &wf); err != nil {
			t.Fatal(err)
		}
		out[name] = &wf
	}
	return out
}

// The report type walks idea → in progress → human-testing → done: no
// Implementation, Test Plan, Documentation or Shipping, no commit check; the
// report on the shelf gates the owner review, and done is the owner's.
func TestReportTypeHasNoBuildBoxes(t *testing.T) {
	for name, wf := range bothDevWorkflows(t) {
		if _, ok := wf.Types["report"]; !ok {
			t.Errorf("%s: no report type", name)
			continue
		}
		r := wf.ForType("report")
		if got := r.PathLine(); got != "idea → in progress → human-testing → done" {
			t.Errorf("%s: report path = %s", name, got)
		}
		for _, tr := range r.Transitions {
			for _, a := range tr.Actions {
				switch {
				case a.Type == "append_section":
					switch a.Title {
					case "Research", "Owner Review":
					default:
						t.Errorf("%s: report %s → %s appends %s", name, tr.From, tr.To, a.Title)
					}
				case a.Type == "validate" && a.Rule == "commits_reference_issue":
					t.Errorf("%s: report %s → %s checks commits", name, tr.From, tr.To)
				}
			}
		}
		if to, kind, ok := r.HasOutputRule(); !ok || to != "human-testing" || kind != "report" {
			t.Errorf("%s: has_output gate = %q %q %v, want human-testing report", name, to, kind, ok)
		}
		if got := r.RequiredHumanApproval("human-testing", "done"); got != "done" {
			t.Errorf("%s: human-testing → done approval = %q, want done", name, got)
		}
		owner := false
		for _, tr := range r.Transitions {
			for _, a := range tr.Actions {
				if a.Title == "Owner Review" && strings.Contains(a.Body, "(owner)") {
					owner = true
				}
			}
		}
		if !owner {
			t.Errorf("%s: no (owner) box in Owner Review", name)
		}
	}
}

// Adding report changes no other type: the base statuses (the board's
// columns) and every other type's path are as before.
func TestReportTypeLeavesOtherTypesAlone(t *testing.T) {
	base := "idea → in design → backlog → in progress → testing → human-testing → documentation → shipping → done"
	paths := map[string]string{
		"feature": base,
		"tweak":   "idea → in progress → testing → shipping → done",
		"docs":    "idea → in progress → shipping → done",
		"bugfix":  "idea → in progress → testing → human-testing → documentation → shipping → done",
	}
	for name, wf := range bothDevWorkflows(t) {
		if got := wf.PathLine(); got != base {
			t.Errorf("%s: base statuses = %s", name, got)
		}
		for typ, want := range paths {
			scoped := wf.ForType(typ)
			if got := scoped.PathLine(); got != want {
				t.Errorf("%s: %s path = %s, want %s", name, typ, got, want)
			}
			if _, _, ok := scoped.HasOutputRule(); ok {
				t.Errorf("%s: %s gates on has_output", name, typ)
			}
		}
	}
}

func TestCheckReports(t *testing.T) {
	dev, err := workflowupgrade.ReadTemplate("development")
	if err != nil {
		t.Fatal(err)
	}
	issue := func(title, status, typ string) string {
		return "---\ntitle: \"" + title + "\"\nstatus: \"" + status + "\"\ntype: \"" + typ + "\"\ncreated: \"2026-10-31\"\n---\n\nBody.\n"
	}
	env := lintProject(t, string(dev), map[string]string{
		"researching.md": issue("Researching", "in progress", "report"),
		"none.md":        issue("None", "human-testing", "report"),
		"shelved.md":     issue("Shelved", "done", "report"),
		"rejected.md":    issue("Rejected", "done", "report"),
		"gone.md":        issue("Gone", "human-testing", "report"),
		"feature.md":     issue("Feature", "done", "feature"),
	})
	now := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	file := filepath.Join(t.TempDir(), "r.md")
	if err := os.WriteFile(file, []byte("# r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	add := func(slug string) *outputs.Output {
		o, err := outputs.Add(env.Project, outputs.AddInput{Department: "product", Title: slug, Kind: "report", File: file, Issue: slug}, now)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	add("shelved")
	rej := add("rejected")
	if _, err := outputs.Review(env.Project, rej.ID, outputs.StateRejected, "no", "", "owner", now); err != nil {
		t.Fatal(err)
	}
	gone := add("gone")
	if err := os.Remove(filepath.Join(outputs.FilesDir(env.Project), filepath.FromSlash(gone.File))); err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, f := range run(t, env, "reports") {
		got = append(got, f.Where+": "+f.Message+" | "+f.Fix)
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		"none: at human-testing, past the has_output gate into human-testing, but no report on the shelf for none",
		"rejected: at done, past the has_output gate into human-testing, but no report on the shelf for rejected (" + rej.ID + " rejected by the owner",
		"gone: at human-testing, past the has_output gate into human-testing, but no report on the shelf for gone (" + gone.ID + ": its file is gone)",
		`issue-cli output add --issue none --kind report --title "..." --file <file>`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if len(got) != 3 {
		t.Errorf("got %d findings, want 3 (researching is before the gate, shelved has its report, feature has no gate):\n%s", len(got), all)
	}
}
