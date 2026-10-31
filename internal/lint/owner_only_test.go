package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/cmd/issue-cli/templates"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

const publishWorkflow = `template: custom
statuses:
  - name: draft
  - name: publish
  - name: done
transitions:
  - from: draft
    to: publish
    actions:
      - type: require_human_approval
        status: publish
  - from: publish
    to: done
`

func ownerOnlyFindings(t *testing.T, env *Env) []Finding {
	t.Helper()
	rep, err := Run(env, "owner-only")
	if err != nil {
		t.Fatal(err)
	}
	return rep.Findings
}

func TestOwnerOnlyFlagsPublishWithoutIt(t *testing.T) {
	got := ownerOnlyFindings(t, lintProject(t, publishWorkflow, nil))
	if len(got) != 1 || !strings.Contains(got[0].Message, "enters publish from draft, its gate isn't owner_only") || !strings.Contains(got[0].Fix, "owner_only: true") {
		t.Fatalf("findings = %+v", got)
	}
	if got[0].Where != "workflow.yaml:8" {
		t.Errorf("where = %q", got[0].Where)
	}

	ok := strings.Replace(publishWorkflow, "        status: publish\n", "        status: publish\n        owner_only: true\n", 1)
	if got := ownerOnlyFindings(t, lintProject(t, ok, nil)); len(got) != 0 {
		t.Fatalf("owner_only gate still flagged: %+v", got)
	}

	misplaced := strings.Replace(ok, "  - from: publish\n    to: done\n", "  - from: publish\n    to: done\n    actions:\n      - type: validate\n        rule: body_not_empty\n        owner_only: true\n", 1)
	if got := ownerOnlyFindings(t, lintProject(t, misplaced, nil)); len(got) != 1 || !strings.Contains(got[0].Message, "owner_only on a validate action does nothing") {
		t.Fatalf("misplaced owner_only: %+v", got)
	}
}

func TestOwnerOnlyFlagsSendsConnector(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	env.Project.Departments = []tracker.Department{
		{Name: "Marketing", Connectors: []tracker.Connector{{Name: "ads", Kind: "tool", Sends: true}, {Name: "analytics", Kind: "tool"}}},
	}
	got := ownerOnlyFindings(t, env)
	if len(got) != 1 || !strings.Contains(got[0].Message, "department Marketing has connector ads with sends: true") {
		t.Fatalf("findings = %+v", got)
	}

	// A department on its own file that has the gate is fine; the finding
	// is about the file its issues walk.
	dir := filepath.Dir(env.File)
	ok := strings.Replace(publishWorkflow, "        status: publish\n", "        status: publish\n        owner_only: true\n", 1)
	if err := os.WriteFile(filepath.Join(dir, "marketing.yaml"), []byte(ok), 0o644); err != nil {
		t.Fatal(err)
	}
	env.Project.Departments[0].Workflow = "marketing.yaml"
	env.Project.WorkDir = dir
	if got := ownerOnlyFindings(t, env); len(got) != 0 {
		t.Fatalf("department with an owner_only gate flagged: %+v", got)
	}
}

// The bundled templates that publish keep their publish gate owner_only.
func TestOwnerOnlyBundledTemplatesClean(t *testing.T) {
	for _, name := range []string{"content", "marketing", "development", "review", "writing"} {
		raw, err := templates.FS.ReadFile("workflow/" + name + ".yaml")
		if err != nil {
			t.Fatal(err)
		}
		got := ownerOnlyFindings(t, lintProject(t, string(raw), nil))
		if len(got) != 0 {
			t.Errorf("%s: %+v", name, got)
		}
	}
}
