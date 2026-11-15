package lint

import (
	"sort"
	"strings"
	"testing"
)

const typesWorkflow = `template: custom
default_type: feature
statuses:
  - name: idea
    description: raw
  - name: in progress
    description: doing
  - name: testing
    description: checks
  - name: done
    description: shipped
transitions:
  - from: idea
    to: in progress
  - from: in progress
    to: testing
  - from: testing
    to: done
types:
  feature:
    description: full path
    path: [idea, in progress, testing, done]
  docs:
    description: docs only
    path: [idea, in progress, done]
    transitions:
      - from: in progress
        to: done
`

func docsIssue(title, status, typ, body string) string {
	return "---\ntitle: \"" + title + "\"\nstatus: " + status + "\ntype: " + typ + "\ncreated: \"2026-11-04\"\n---\n\n" + body + "\n"
}

// The three things a docs issue may not ask for, each from the Wanted
// section and from a criterion, and what stays quiet.
func TestTypesWarnsOnADocsIssueAskingForCode(t *testing.T) {
	env := lintProject(t, typesWorkflow, map[string]string{
		"S/wanted-test.md": docsIssue("wanted test", "idea", "docs", "## Wanted\n\nA test over templates: every class has a rule.\n"),
		"S/wanted-go.md":   docsIssue("wanted go", "in progress", "docs", "## Wanted\n\nA line in internal/viewer/routes.go.\n"),
		"S/wanted-pkg.md":  docsIssue("wanted pkg", "idea", "docs", "## Wanted\n\nA helper in internal/tracker or similar.\n"),
		"S/ac-test.md": docsIssue("ac test", "idea", "docs", `## Acceptance criteria

- [ ] A go test fails on a missing rule
- [ ] TestWorkshopCSS passes
- [ ] The helper has a test
- [ ] Add tests for it
- [ ] The doc names cmd/issue-cli/main.go.
- [ ] The doc matches internal/lint
- [ ] It changes the Go package behind the page
- [x] A ticked box asks for a test
- [ ] The doc reads the same (N/A: a test would only repeat it)
`),
		// Not docs, closed, or naming nothing the rule looks for.
		"S/feature.md": docsIssue("feature", "idea", "feature", "## Wanted\n\nA test in internal/lint/types.go.\n"),
		"S/closed.md":  docsIssue("closed", "done", "docs", "## Wanted\n\nA test in internal/lint/types.go.\n"),
		"S/readme.md": docsIssue("readme", "in progress", "docs", "## Wanted\n\nThe viewer in cmd/issue-viewer; install with `go install github.com/x/y/cmd/issue-cli@latest`; links to pkg.go.dev and go.mod; the npm package manager.\n\n"+
			"## Why\n\nA test in internal/lint would not help.\n\n## Acceptance criteria\n\n- [ ] make validate is green (readme tests, the upstream-name guard)\n- [ ] `make link-check` passes, which tests every link\n"),
	})
	got := map[string]Finding{}
	var where []string
	for _, f := range run(t, env, "types") {
		got[f.Where] = f
		where = append(where, f.Where)
	}
	sort.Strings(where)
	want := []string{
		"s/ac-test AC1", "s/ac-test AC2", "s/ac-test AC3", "s/ac-test AC4", "s/ac-test AC5", "s/ac-test AC6", "s/ac-test AC7",
		"s/wanted-go ## Wanted", "s/wanted-pkg ## Wanted", "s/wanted-test ## Wanted",
	}
	if strings.Join(where, "|") != strings.Join(want, "|") {
		t.Fatalf("findings at\n%q, want\n%q", where, want)
	}
	for at, hit := range map[string]string{
		"s/ac-test AC1":         "A go test",
		"s/ac-test AC2":         "TestWorkshopCSS",
		"s/ac-test AC5":         "cmd/issue-cli/main.go",
		"s/ac-test AC6":         "internal/lint",
		"s/wanted-go ## Wanted": "internal/viewer/routes.go",
	} {
		if m := got[at].Message; !strings.Contains(strings.ToLower(m), strings.ToLower(`"`+hit+`"`)) {
			t.Errorf("%s: message %q should quote %q", at, m, hit)
		}
	}
	if f := got["s/wanted-test ## Wanted"]; f.Check != "types" || !strings.Contains(f.Fix, "issue-cli set-type s/wanted-test tweak") {
		t.Errorf("finding %+v: want check types and a set-type fix", f)
	}
}

// Without types: in the workflow, type: is a plain custom field and picks
// no path, so type: docs promises nothing.
func TestTypesLeavesAProjectWithoutTypesAlone(t *testing.T) {
	env := lintProject(t, "template: custom\nstatuses:\n  - name: idea\n    description: raw\n  - name: done\n    description: shipped\ntransitions:\n  - from: idea\n    to: done\n", map[string]string{
		"S/a.md": docsIssue("a", "idea", "docs", "## Wanted\n\nA test in internal/lint.\n"),
	})
	if got := run(t, env, "types"); len(got) != 0 {
		t.Fatalf("findings %+v, want none", got)
	}
}
