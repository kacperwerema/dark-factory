package lint

import (
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// licenceBoard is a board holding the owner's Postiz exception (D-1), a
// replaced one (D-2, replaced by D-3) and a provisional one (D-4).
var licenceBoard = map[string]string{
	"social.md": "---\ntitle: \"Social\"\nstatus: \"done\"\n---\n## Decisions\n" +
		"- **D-1** · 2026-11-02 · CEO chat: Postiz is accepted as the licence exception, \"Postiz can be exception for now.\"\n" +
		"- **D-2** · 2026-11-02 · CEO chat: Listmonk is accepted, \"ok\"\n" +
		"- **D-3** · 2026-11-03 · CEO chat: Listmonk is not accepted after all, \"no\" (replaces D-2)\n" +
		"- **D-4** · 2026-11-03 · provisional · YOLO: OpenMontage accepted (provisional)\n",
}

func licenceMessages(t *testing.T, env *Env) string {
	t.Helper()
	var got []string
	for _, f := range checkDepartments(env) {
		got = append(got, f.Where+": "+f.Message+" | fix: "+f.Fix)
	}
	return strings.Join(got, "\n")
}

func TestLintWarnsOnAnAGPLConnectorWithNoException(t *testing.T) {
	for _, tc := range []struct {
		name, exception, want string
	}{
		{"none", "", "this entry names none"},
		{"no decision", "social#D-9", "exception: social#D-9 names no decision on this board"},
		{"not a ref", "D-1", `exception: "D-1" is not a decision ref`},
		{"replaced", "social#D-2", "exception: social#D-2 is replaced by social#D-3"},
		{"provisional", "social#D-4", "exception: social#D-4 has no owner's words"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := lintProject(t, minimalWorkflow, licenceBoard)
			env.Project.Departments = []tracker.Department{{Name: "Marketing", Connectors: []tracker.Connector{
				{Name: "postiz", Kind: "mcp", URL: "http://127.0.0.1:5000/mcp", Exception: tc.exception},
			}}}
			all := licenceMessages(t, env)
			for _, want := range []string{
				"projects.yaml (Marketing): connector postiz: Postiz is AGPL-3.0 (partners catalog)",
				licenceRule,
				tc.want,
				"issue-cli department partners --slot social",
			} {
				if !strings.Contains(all, want) {
					t.Errorf("missing %q in:\n%s", want, all)
				}
			}
		})
	}
	t.Run("resolves", func(t *testing.T) {
		env := lintProject(t, minimalWorkflow, licenceBoard)
		env.Project.Departments = []tracker.Department{{Name: "Marketing", Connectors: []tracker.Connector{
			{Name: "Postiz", Kind: "mcp", URL: "http://127.0.0.1:5000/mcp", Exception: "social#D-1"},
		}}}
		if all := licenceMessages(t, env); strings.Contains(all, "AGPL") {
			t.Errorf("a ref to the owner's decision in force still warns:\n%s", all)
		}
	})
}

func TestLintWarnsOnAnAGPLPartnerWithNoException(t *testing.T) {
	env := lintProject(t, minimalWorkflow, licenceBoard)
	env.Project.Departments = []tracker.Department{{Name: "Video", Partner: &tracker.Partner{Adapter: "openmontage"}}}
	all := licenceMessages(t, env)
	for _, want := range []string{
		"projects.yaml (Video): partner openmontage: OpenMontage is AGPL-3.0 (partners catalog)",
		licenceRule,
		"this entry names none",
		"--slot video",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	env.Project.Departments[0].Partner.Exception = "social#D-1"
	if all := licenceMessages(t, env); strings.Contains(all, "AGPL") {
		t.Errorf("a partner with a resolving exception still warns:\n%s", all)
	}
}

func TestLintSilentOnAToolTheCatalogDoesNotKnow(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	env.Project.Departments = []tracker.Department{
		{Name: "Marketing", Connectors: []tracker.Connector{
			{Name: "house-crm", Kind: "tool", Command: "crm"},
			{Name: "umami", Kind: "mcp", URL: "http://127.0.0.1:3000/mcp"},
		}},
		{Name: "Studio", Partner: &tracker.Partner{Name: "Homegrown", Adapter: "homegrown"}},
	}
	if all := licenceMessages(t, env); strings.Contains(all, "licence rule") || strings.Contains(all, "partners catalog") {
		t.Errorf("a tool the catalog doesn't know, or an MIT one, raised a licence finding:\n%s", all)
	}
}

// TestLintFindsASlotNamedConnectorByItsTool: a channel's connector is named
// by its slot (Marketing's `social`), so the catalog row is found by its
// per_run: tool: id as well; with the owner's exception it is silent.
func TestLintFindsASlotNamedConnectorByItsTool(t *testing.T) {
	env := lintProject(t, minimalWorkflow, licenceBoard)
	env.Project.Departments = []tracker.Department{{Name: "Marketing", Connectors: []tracker.Connector{
		{Name: "social", Kind: "mcp", URL: "http://127.0.0.1:4007/api/mcp", PerRun: &tracker.PerRun{Tool: "postiz", Start: "true", Ready: "true", Stop: "true"}},
	}}}
	all := licenceMessages(t, env)
	for _, want := range []string{"connector social: Postiz is AGPL-3.0 (partners catalog)", "this entry names none"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	env.Project.Departments[0].Connectors[0].Exception = "social#D-1"
	if all := licenceMessages(t, env); strings.Contains(all, "AGPL") {
		t.Errorf("a slot-named connector with a resolving exception still warns:\n%s", all)
	}
}
