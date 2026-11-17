package depttemplate

import (
	"fmt"
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

// SetProjectScalar changes or adds one line of one project; every other
// byte, comments included, stays.
func TestSetProjectScalarKeepsTheRest(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "issues"), 0o755)
	cfg := filepath.Join(dir, "projects.yaml")
	orig := "# the owner's file\nprojects:\n  - name: \"Atlas\"\n    slug: \"atlas\"   # first\n    issues: ./issues\n    departments:\n      - name: Marketing\n\n  - name: DF\n    slug: dark-factory\n    issues: ./issues\n    start_weight: 2 # was 2\n"
	os.WriteFile(cfg, []byte(orig), 0o644)
	check := func(n int) func(*tracker.Project) error {
		return func(p *tracker.Project) error {
			if p.StartWeight != n {
				return fmt.Errorf("weight %d", p.StartWeight)
			}
			return nil
		}
	}
	if err := SetProjectScalar(cfg, "atlas", "start_weight", "3", check(3)); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(cfg)
	want := strings.Replace(orig, "      - name: Marketing\n", "      - name: Marketing\n    start_weight: 3\n", 1)
	if string(got) != want {
		t.Fatalf("added:\n%s\nwant:\n%s", got, want)
	}
	if err := SetProjectScalar(cfg, "dark-factory", "start_weight", "1", check(1)); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(cfg)
	want = strings.Replace(want, "start_weight: 2 # was 2\n", "start_weight: 1 # was 2\n", 1)
	if string(got) != want {
		t.Fatalf("replaced:\n%s\nwant:\n%s", got, want)
	}
	// A check that fails leaves the file as it was.
	if err := SetProjectScalar(cfg, "atlas", "start_weight", "5", check(4)); err == nil {
		t.Fatal("a failed read-back wrote the file")
	}
	if again, _ := os.ReadFile(cfg); string(again) != want {
		t.Fatal("a refused write changed the file")
	}
	if err := SetProjectScalar(cfg, "nope", "start_weight", "5", check(5)); err == nil {
		t.Fatal("an unknown project was written")
	}
}
