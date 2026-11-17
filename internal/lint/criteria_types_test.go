package lint

import (
	"sort"
	"strings"
	"testing"
)

// typedCriteriaWorkflow has a testing status on the base path, a feature
// and a bugfix walking it, and a report whose path skips it (and writes
// its own Must not on the way into in progress).
const typedCriteriaWorkflow = `template: custom
statuses:
  - name: idea
    description: raw
  - name: in design
    description: design
  - name: in progress
    description: work
  - name: testing
    description: verify
  - name: human-testing
    description: owner review
  - name: done
    description: shipped
transitions:
  - from: idea
    to: in design
  - from: in design
    to: in progress
  - from: in progress
    to: testing
  - from: testing
    to: human-testing
  - from: human-testing
    to: done
default_type: feature
types:
  feature:
    description: full path
  bugfix:
    description: full path
    path: [idea, in progress, testing, done]
  report:
    description: research only
    path: [idea, in progress, human-testing, done]
    transitions:
      - from: idea
        to: in progress
        actions:
          - type: append_section
            title: Must not
            body: "- <what this change must not do>"
`

const untestedBoxes = `
## Acceptance Criteria

- [ ] Departments get a summary line
`

func TestCriteriaLintSkipsReports(t *testing.T) {
	issue := func(typ, status, extra string) string {
		return "---\ntitle: \"" + typ + "\"\nstatus: " + status + "\ntype: " + typ + "\ncreated: \"2026-11-16\"\n---\n" + untestedBoxes + extra
	}
	env := lintProject(t, typedCriteriaWorkflow, map[string]string{
		"S/feature.md": issue("feature", "in progress", ""),
		"S/bugfix.md":  issue("bugfix", "in progress", ""),
		"S/report.md":  issue("report", "human-testing", "\n## Must not\n- <what this change must not do>\n"),
	})
	var where []string
	for _, f := range run(t, env, "criteria") {
		where = append(where, f.Where)
	}
	sort.Strings(where)
	// The report's box asks no test; its placeholder Must not still shows.
	want := []string{"s/bugfix AC1", "s/feature AC1", "s/report ## Must not"}
	if strings.Join(where, "|") != strings.Join(want, "|") {
		t.Fatalf("findings at %q, want %q", where, want)
	}
}
