package lint

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathsCheck(t *testing.T) {
	wf := minimalWorkflow + `workbench:
  systems:
    Engine:
      paths:
        - src/**/*.go
        - nope/
        - ../outside/**
    Docs:
      summary: no paths, nothing to check
`
	env := lintProject(t, wf, nil)
	root := env.Project.Root()
	os.MkdirAll(filepath.Join(root, "src", "a"), 0755)
	os.WriteFile(filepath.Join(root, "src", "a", "b.go"), []byte("package a\n"), 0644)

	got := run(t, env, "paths")
	if len(got) != 2 {
		t.Fatalf("findings = %+v", got)
	}
	if !has(got, "paths", "workflow.yaml:21", `workbench.systems.Engine.paths "nope/"`, "matches no file") {
		t.Errorf("a glob that matches nothing, with its line: %+v", got)
	}
	if !has(got, "paths", "workflow.yaml:22", `"../outside/**"`, "leaves the repository") {
		t.Errorf("a glob outside the repository: %+v", got)
	}

	if got := run(t, lintProject(t, minimalWorkflow, nil), "paths"); len(got) != 0 {
		t.Errorf("no paths, no findings: %+v", got)
	}
}
