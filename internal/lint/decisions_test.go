package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/outputs"
)

// decisionIssue is an issue of system Core whose ## Decisions holds lines.
func decisionIssue(title string, lines ...string) string {
	body := "---\ntitle: \"" + title + "\"\nstatus: idea\nsystem: Core\ncreated: \"2026-10-01\"\n---\n\nBody.\n\n## Decisions\n\n"
	for _, l := range lines {
		body += l + "\n"
	}
	return body
}

const owned = `owner chat: Keep it, "Yes, keep it."`

// TestLintDecisionsFourShapes: each bad shape is reported once, on its own
// line, and a board of good groups reports nothing.
func TestLintDecisionsFourShapes(t *testing.T) {
	env := lintProject(t, minimalWorkflow, map[string]string{
		"Core/bad.md": decisionIssue("Bad",
			"- **D-1** · 2026-10-02 · "+owned+" {scope: project; owner: me}",
			"- **D-2** · 2026-10-02 · YOLO, provisional: Make it a rule {scope: project; hard}",
			"- **D-3** · 2026-10-02 · "+owned+" {hard; paths: nowhere/**}",
			"- **D-4** · 2026-10-02 · "+owned+" {at: shipping}",
		),
	})
	os.MkdirAll(filepath.Join(env.Project.Root(), "src"), 0755)
	os.WriteFile(filepath.Join(env.Project.Root(), "src", "a.go"), []byte("package a\n"), 0644)

	got := run(t, env, "decisions")
	if len(got) != 4 {
		t.Fatalf("want 4 findings, one per shape: %+v", got)
	}
	for _, want := range [][]string{
		{"core/bad#D-1", "unknown scope key or value", `"owner: me"`},
		{"core/bad#D-2", "hard without the owner's words", "--confirm D-2"},
		{"core/bad#D-3", `paths: "nowhere/**" matches no file`},
		{"core/bad#D-4", `at: "shipping" is no status`, "statuses: idea, done"},
	} {
		if !has(got, want...) {
			t.Errorf("missing %q in %+v", want, got)
		}
	}
}

// TestLintDecisionsUnknownScopeValue: a group that parses but names a
// system, department or type the project lacks.
func TestLintDecisionsUnknownScopeValue(t *testing.T) {
	env := lintProject(t, minimalWorkflow, map[string]string{
		"Core/a.md": decisionIssue("A",
			"- **D-1** · 2026-10-02 · chat: One {scope: system:Nowhere}",
			"- **D-2** · 2026-10-02 · chat: Two {scope: department:sales}",
			"- **D-3** · 2026-10-02 · chat: Three {scope: type:epic}",
			"- **D-4** · 2026-10-02 · chat: Four {scope: system:core}",
			"- **D-5** · 2026-10-02 · chat: Five {scope: department:product}",
		),
	})
	got := run(t, env, "decisions")
	if len(got) != 3 || !has(got, "core/a#D-1", "Nowhere") || !has(got, "core/a#D-2", `"sales"`) || !has(got, "core/a#D-3", `"epic"`) {
		t.Errorf("want D-1..D-3 only: %+v", got)
	}
}

// TestLintDecisionsClean: good groups, lines without one, a replaced bad
// one and legacy brace text report nothing.
func TestLintDecisionsClean(t *testing.T) {
	env := lintProject(t, minimalWorkflow, map[string]string{
		"Core/good.md": decisionIssue("Good",
			"- **D-1** · 2026-10-02 · "+owned+" {scope: system:Core; hard; at: idea; paths: src/**}",
			"- **D-2** · 2026-10-02 · chat: Plain line",
			"- **D-3** · 2026-10-02 · chat: Old bad {scope: project; hard}",
			"- **D-4** · 2026-10-03 · "+owned+" (replaces D-3) {scope: project; hard}",
			"- **D-5** · 2026-10-03 · chat: requests are declared as {from: marketing, to: product, via: hq}",
			"- **D-6** · 2026-10-03 · chat: Confirmed later, \"ok\" {hard}",
		),
	})
	os.MkdirAll(filepath.Join(env.Project.Root(), "src"), 0755)
	os.WriteFile(filepath.Join(env.Project.Root(), "src", "a.go"), []byte("package a\n"), 0644)
	if got := run(t, env, "decisions"); len(got) != 0 {
		t.Errorf("clean board: %+v", got)
	}
}

// TestLintDecisionsLegacyBraceQuiet: a trailing {…} that names no decision
// key stays text, as the parser reads it.
func TestLintDecisionsLegacyBraceQuiet(t *testing.T) {
	env := lintProject(t, minimalWorkflow, map[string]string{
		"Core/a.md": decisionIssue("A", "- **D-1** · 2026-10-02 · chat: route {from: marketing, to: product, via: hq}"),
	})
	if got := run(t, env, "decisions"); len(got) != 0 {
		t.Errorf("legacy brace text reported: %+v", got)
	}
}

// writeOutputs puts lines on the project's outputs shelf.
func writeOutputs(t *testing.T, env *Env, lines ...string) {
	t.Helper()
	path := outputs.LogPath(env.Project)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestLintDecisionScopeOfARemovedDepartmentIsHistory: a decision scoped to
// a department projects.yaml no longer has, whose work the board still
// holds (an output, an issue's department: field), is an info line in the
// report's history, not a warning.
func TestLintDecisionScopeOfARemovedDepartmentIsHistory(t *testing.T) {
	env := lintProject(t, minimalWorkflow, map[string]string{
		"Core/a.md": decisionIssue("A",
			"- **D-1** · 2026-10-02 · chat: One {scope: department:marketing}",
			"- **D-2** · 2026-10-02 · chat: Two {scope: department:sales}",
		),
		"Core/old.md": "---\ntitle: \"Old\"\nstatus: done\nsystem: Core\ndepartment: Sales\ncreated: \"2026-10-01\"\n---\n\nBody.\n",
	})
	writeOutputs(t, env, `{"op":"add","id":"o-1","date":"2026-10-01T00:00:00Z","department":"marketing","title":"Post","kind":"post","url":"https://a.example"}`)
	rep, err := Run(env, "decisions")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Findings) != 0 || rep.Warnings != 0 {
		t.Errorf("removed departments' decisions warned: %+v", rep.Findings)
	}
	if len(rep.History) != 2 || !has(rep.History, "core/a#D-1", `"marketing"`, "no longer has") || !has(rep.History, "core/a#D-2", `"sales"`) {
		t.Errorf("want D-1 and D-2 as history: %+v", rep.History)
	}
}

// TestLintDecisionScopeTypoStillWarns: a department the board never saw
// (no output, no issue of it) is a typo, and still warns.
func TestLintDecisionScopeTypoStillWarns(t *testing.T) {
	env := lintProject(t, minimalWorkflow, map[string]string{
		"Core/a.md": decisionIssue("A", "- **D-1** · 2026-10-02 · chat: One {scope: department:markting}"),
	})
	writeOutputs(t, env, `{"op":"add","id":"o-1","date":"2026-10-01T00:00:00Z","department":"marketing","title":"Post","kind":"post","url":"https://a.example"}`)
	rep, err := Run(env, "decisions")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Warnings != 1 || !has(rep.Findings, "core/a#D-1", "unknown scope key or value", `"markting"`) || len(rep.History) != 0 {
		t.Errorf("typo: want one warning, no history: findings %+v history %+v", rep.Findings, rep.History)
	}
}
