package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestDepartmentCheckAndPerFileRun(t *testing.T) {
	env := lintProject(t, minimalWorkflow, map[string]string{
		"SEO/a.md": "---\ntitle: a\nstatus: idea\nsystem: SEO\ncreated: 2026-10-01\n---\n",
		"SEO/b.md": "---\ntitle: b\nstatus: brief\nsystem: SEO\ncreated: 2026-10-01\n---\n",
	})
	root := filepath.Dir(env.Project.IssueDir)
	env.Project.WorkDir = root
	path := filepath.Join(root, "workflows", "marketing.yaml")
	os.MkdirAll(filepath.Dir(path), 0755)
	// ci: is Product's; its unknown key nope: is the schema check's to find
	// in this file, not in workflow.yaml.
	os.WriteFile(path, []byte("template: custom\nci:\n  run: [make]\nnope: 1\nstatuses:\n  - name: brief\n  - name: done\ntransitions:\n  - from: brief\n    to: done\n"), 0644)
	env.Project.Departments = nil
	rep, err := Run(env, "department", "schema")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Warnings != 0 {
		t.Fatalf("no departments: %+v", rep.Findings)
	}

	env.Project.Departments = append(env.Project.Departments, deptMarketing())
	rep, err = Run(env, "department", "schema")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range rep.Findings {
		got = append(got, f.Check+" "+f.Where+" "+f.Message)
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{"department marketing.yaml ci: is project-wide", `department seo/a status "idea" is not in Marketing's workflow`, "schema marketing.yaml"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "seo/b") {
		t.Errorf("seo/b is on a status the file has:\n%s", all)
	}

	os.WriteFile(path, []byte("statuses: [: broken\n"), 0644)
	rep, _ = Run(env, "department")
	if rep.Warnings != 1 || !strings.Contains(rep.Findings[0].Message, "department Marketing") {
		t.Fatalf("broken file: %+v", rep.Findings)
	}
}

func deptMarketing() tracker.Department {
	return tracker.Department{Name: "Marketing", Systems: []string{"SEO"}, Workflow: "workflows/marketing.yaml"}
}
