package lint

import (
	"os"
	"path/filepath"
	"testing"
)

const contextsWorkflow = minimalWorkflow + `workbench:
  layers:
    - name: kernel
      shared: true
    - name: domain
  contexts:
    - name: Kernel
      layer: kernel
      packages: [kernel/...]
      leaks:
        - kernel/util -> app/gone
    - name: App
      layer: domain
      packages: [app/feature, nope/x]
    - name: App
      layer: middle
`

// contextsRepo writes a module where kernel/util imports app/feature, a
// layer up, and app/extra is in no context.
func contextsRepo(t *testing.T, root string, upward bool) {
	t.Helper()
	util := "package util\n"
	if upward {
		util = "package util\n\nimport \"example.com/m/app/feature\"\n\nvar _ = feature.Y\n"
	}
	files := map[string]string{
		"go.mod":                 "module example.com/m\n",
		"kernel/util/util.go":    util,
		"app/feature/feature.go": "package feature\n\nimport \"example.com/m/kernel/util\"\n",
		"app/extra/extra.go":     "package extra\n",
	}
	for p, s := range files {
		full := filepath.Join(root, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestContextsCheck: a fixture that adds an upward import gets a warning
// naming the file and line (A3), beside the other code map problems.
func TestContextsCheck(t *testing.T) {
	env := lintProject(t, contextsWorkflow, nil)
	contextsRepo(t, env.Project.Root(), true)
	got := run(t, env, "contexts")
	for _, want := range [][]string{
		{"kernel/util/util.go:3", "kernel/util (Kernel, layer kernel) imports app/feature (App, layer domain): an import up a layer", `leaks: as "kernel/util -> app/feature"`},
		{"package app/extra is in no context"},
		{`workbench.contexts name "App" is used twice`},
		{`layer "middle"`},
		{`packages: entry "nope/x" matches no Go package`},
		{`leaks: entry "kernel/util" -> "app/gone" no longer happens`},
	} {
		if !has(got, want...) {
			t.Errorf("no finding with %q in %+v", want, got)
		}
	}
}

// TestContextsFindingsAreWarnings: the same fixture without the upward
// import has no upward finding, and every finding is a warning (a lint
// finding never fails a run).
func TestContextsFindingsAreWarnings(t *testing.T) {
	env := lintProject(t, contextsWorkflow, nil)
	contextsRepo(t, env.Project.Root(), false)
	rep, err := Run(env, "contexts")
	if err != nil {
		t.Fatal(err)
	}
	if has(rep.Findings, "an import up a layer") {
		t.Errorf("upward finding without the import: %+v", rep.Findings)
	}
	if rep.Warnings != len(rep.Findings) {
		t.Errorf("warnings %d, findings %d", rep.Warnings, len(rep.Findings))
	}
	if got := run(t, lintProject(t, minimalWorkflow, nil), "contexts"); len(got) != 0 {
		t.Errorf("no contexts, no findings: %+v", got)
	}
}
