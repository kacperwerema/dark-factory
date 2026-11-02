package depttemplate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestAddProjectSplicesAsText(t *testing.T) {
	item := "- name: New\n  slug: new\n  issues: /n/issues\n"
	ok := func(*tracker.Project) error { return nil }
	for _, c := range []struct{ name, in, want string }{
		{"empty file", "", "projects:\n  - name: New\n    slug: new\n    issues: /n/issues\n"},
		{"comments only, no newline", "# hi", "# hi\nprojects:\n  - name: New\n    slug: new\n    issues: /n/issues\n"},
		{"no projects key", "terminal: none\n", "terminal: none\nprojects:\n  - name: New\n    slug: new\n    issues: /n/issues\n"},
		{"null projects", "projects:\nterminal: none\n", "projects:\n  - name: New\n    slug: new\n    issues: /n/issues\nterminal: none\n"},
		{"after the last item, its indent", "projects:\n- name: A\n  issues: /a\n# end\n", "projects:\n- name: A\n  issues: /a\n- name: New\n  slug: new\n  issues: /n/issues\n# end\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "projects.yaml")
			if err := os.WriteFile(path, []byte(c.in), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := AddProject(path, "new", item, ok); err != nil {
				t.Fatal(err)
			}
			if got, _ := os.ReadFile(path); string(got) != c.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, c.want)
			}
		})
	}
}

func TestAddProjectRefusesAndLeavesTheFile(t *testing.T) {
	item := "- name: New\n  slug: new\n  issues: /n/issues\n"
	for _, c := range []struct {
		name, in string
		check    func(*tracker.Project) error
		want     string
	}{
		{"flow list", "projects: [{name: A}]\n", nil, "not a block list"},
		{"not a mapping", "- a\n", nil, "not a mapping"},
		{"check fails", "projects:\n  - name: A\n", func(*tracker.Project) error { return os.ErrInvalid }, "left unchanged"},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "projects.yaml")
			if err := os.WriteFile(path, []byte(c.in), 0o644); err != nil {
				t.Fatal(err)
			}
			check := c.check
			if check == nil {
				check = func(*tracker.Project) error { return nil }
			}
			err := AddProject(path, "new", item, check)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if got, _ := os.ReadFile(path); string(got) != c.in {
				t.Errorf("file changed: %q", got)
			}
		})
	}
}
