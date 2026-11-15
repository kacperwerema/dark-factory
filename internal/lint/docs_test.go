package lint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// docSection returns the lines of a markdown file's section, from its
// heading to the next heading of the same or a higher level.
func docSection(t *testing.T, path, heading string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	level := strings.Index(heading, " ")
	var out []string
	in := false
	for _, line := range strings.Split(string(raw), "\n") {
		if in {
			if n := strings.Index(line, " "); n > 0 && n <= level && strings.Trim(line[:n], "#") == "" {
				break
			}
			out = append(out, line)
			continue
		}
		in = line == heading
	}
	if !in {
		t.Fatalf("%s has no %q section", path, heading)
	}
	return out
}

// Every registered check has a bullet in the lint family's list
// (docs/workbench-files.md, "The checks"), and every bullet names a
// registered check. docs/CLI/overview.md's "Adding a lint check" names this
// test as the place a new check's doc goes.
func TestLintChecksListedInDocs(t *testing.T) {
	docs := filepath.Join("..", "..", "docs")
	bullet := regexp.MustCompile("^  - `([a-z-]+)`:")
	listed := map[string]bool{}
	for _, line := range docSection(t, filepath.Join(docs, "workbench-files.md"), "### The lint family") {
		if m := bullet.FindStringSubmatch(line); m != nil {
			if listed[m[1]] {
				t.Errorf("docs/workbench-files.md lists check %q twice", m[1])
			}
			listed[m[1]] = true
		}
	}
	known := map[string]bool{}
	for _, c := range Checks() {
		known[c.Name] = true
		if !listed[c.Name] {
			t.Errorf("lint check %q has no bullet under The lint family's checks in docs/workbench-files.md (docs/CLI/overview.md#adding-a-lint-check)", c.Name)
		}
	}
	for name := range listed {
		if !known[name] {
			t.Errorf("docs/workbench-files.md lists lint check %q, which internal/lint/lint.go does not register", name)
		}
	}

	steps := strings.Join(docSection(t, filepath.Join(docs, "CLI", "overview.md"), "### Adding a lint check"), "\n")
	for _, place := range []string{"internal/lint/lint.go", "boardChecks", "TestMinimalWorkflowIsClean", "TestLintChecksListedInDocs", "settled-rules.tsv", "TestSettledRulesTableMatchesDoc", "TestLintScenarioCarriesTheSessionsThreeWarnings"} {
		if !strings.Contains(steps, place) {
			t.Errorf("docs/CLI/overview.md's Adding a lint check doesn't name %s", place)
		}
	}
}
