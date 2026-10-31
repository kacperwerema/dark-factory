package workflowupgrade

import (
	"strings"
	"testing"
)

const checkCatalog = `entries:
  - id: health-tuning
    version: v0.53.0
    sources: ["internal/health/**", "internal/tracker/health.go"]
  - id: checklist-gates
    version: v0.36.0
    sources: ["internal/tracker/workflow_validate.go#checkRule"]
  - id: lead-handoff-prompts
    version: v0.36.0
    sources: ["@prompt"]
`

const validateBase = `package tracker

func checkRule() string { return "a" }

func other() string { return "x" }
`

// fakeBranch serves base and head content from maps; head defaults to the
// catalog above.
func fakeBranch(changed []string, base, head map[string]string, trailers ...string) Branch {
	if _, ok := head[CatalogPath]; !ok {
		head[CatalogPath] = checkCatalog
	}
	get := func(m map[string]string) func(string) ([]byte, bool) {
		return func(p string) ([]byte, bool) {
			s, ok := m[p]
			return []byte(s), ok
		}
	}
	var ts []Trailer
	for _, v := range trailers {
		ts = append(ts, Trailer{Commit: "abc1234", Value: v})
	}
	return Branch{Changed: changed, Base: get(base), Head: get(head), Trailers: ts}
}

func entryStates(rep *CheckReport) string {
	var parts []string
	for _, e := range rep.Entries {
		parts = append(parts, e.ID+"="+e.State)
	}
	for _, t := range rep.Templates {
		parts = append(parts, "template:"+t.Name+"="+t.State)
	}
	return strings.Join(parts, ",")
}

func TestCheckBranchAsksForABump(t *testing.T) {
	b := fakeBranch([]string{"internal/health/score.go", "docs/x.md"}, map[string]string{}, map[string]string{})
	rep, err := CheckBranch(b, "v0.53.0")
	if err != nil {
		t.Fatal(err)
	}
	if got := entryStates(rep); got != "health-tuning=stale" || rep.OK() {
		t.Fatalf("states = %s, ok = %v", got, rep.OK())
	}
	if rep.NextRelease != "v0.54.0" || strings.Join(rep.Entries[0].Files, ",") != "internal/health/score.go" {
		t.Errorf("report = %+v", rep)
	}
}

func TestCheckBranchPassesABumpOrATrailer(t *testing.T) {
	bumped := fakeBranch([]string{"internal/tracker/health.go"}, map[string]string{}, map[string]string{
		CatalogPath: strings.Replace(checkCatalog, "version: v0.53.0", "version: v0.54.0", 1),
	})
	rep, err := CheckBranch(bumped, "v0.53.0")
	if err != nil {
		t.Fatal(err)
	}
	if got := entryStates(rep); got != "health-tuning=bumped" || !rep.OK() {
		t.Errorf("bumped: %s", got)
	}

	marked := fakeBranch([]string{"internal/tracker/health.go"}, map[string]string{}, map[string]string{}, "n/a health-tuning,checklist-gates rename only", "")
	rep, err = CheckBranch(marked, "v0.53.0")
	if err != nil {
		t.Fatal(err)
	}
	if got := entryStates(rep); got != "health-tuning=n/a" || !rep.OK() || rep.Entries[0].Why != "rename only" {
		t.Errorf("n/a: %s %+v", got, rep.Entries)
	}
}

func TestCheckBranchRejectsTrailersThatMarkNothing(t *testing.T) {
	for _, tc := range []struct{ value, problem string }{
		{"n/a refactor only", `no catalog entry "refactor"`},
		{"n/a", "names no entry"},
		{"none, a refactor", "only n/a is read"},
		{"n/a health-tuning,health-tunning typo", `no catalog entry "health-tunning"`},
	} {
		b := fakeBranch([]string{"internal/tracker/health.go"}, map[string]string{}, map[string]string{}, tc.value)
		rep, err := CheckBranch(b, "v0.53.0")
		if err != nil {
			t.Fatal(err)
		}
		if rep.OK() || len(rep.BadTrailers) != 1 || !strings.Contains(rep.BadTrailers[0].Problem, tc.problem) || rep.BadTrailers[0].Commit != "abc1234" {
			t.Errorf("%q: ok = %v, bad = %+v", tc.value, rep.OK(), rep.BadTrailers)
		}
	}

	// Several ids with a space after the comma, an n/a for an entry the
	// branch doesn't touch, and an upper-case N/A all mark something.
	b := fakeBranch([]string{"internal/tracker/health.go"}, map[string]string{}, map[string]string{}, "n/a health-tuning, checklist-gates rename only", "N/A lead-handoff-prompts wording")
	rep, err := CheckBranch(b, "v0.53.0")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK() || len(rep.BadTrailers) != 0 || rep.Entries[0].Why != "rename only" {
		t.Errorf("good trailers: ok = %v, bad = %+v, entries = %+v", rep.OK(), rep.BadTrailers, rep.Entries)
	}
}

// unlocks: is display text on the Updates page: a branch that changes only
// it (in the catalog, no source file) asks no bump.
func TestCheckBranchIgnoresUnlocks(t *testing.T) {
	withUnlocks := strings.Replace(checkCatalog, "    sources: [\"internal/health/**\"", "    unlocks:\n      - text: \"Health: the score\"\n        url: /health\n    sources: [\"internal/health/**\"", 1)
	if withUnlocks == checkCatalog {
		t.Fatal("test catalog didn't change")
	}
	b := fakeBranch([]string{CatalogPath}, map[string]string{CatalogPath: checkCatalog}, map[string]string{CatalogPath: withUnlocks})
	rep, err := CheckBranch(b, "v0.53.0")
	if err != nil {
		t.Fatal(err)
	}
	if got := entryStates(rep); got != "" || !rep.OK() {
		t.Errorf("an unlocks-only change: states = %q, ok = %v", got, rep.OK())
	}
}

// A #symbol source only counts when that symbol's code changed.
func TestCheckBranchComparesSymbols(t *testing.T) {
	path := "internal/tracker/workflow_validate.go"
	otherOnly := strings.Replace(validateBase, `return "x"`, `return "y"`, 1)
	rep, err := CheckBranch(fakeBranch([]string{path}, map[string]string{path: validateBase}, map[string]string{path: otherOnly}), "v0.53.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Entries) != 0 {
		t.Errorf("a change outside checkRule flagged %s", entryStates(rep))
	}

	withComment := strings.Replace(validateBase, "func checkRule", "// checkRule checks.\nfunc checkRule", 1)
	rep, err = CheckBranch(fakeBranch([]string{path}, map[string]string{path: validateBase}, map[string]string{path: withComment}), "v0.53.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Entries) != 0 {
		t.Errorf("a doc comment flagged %s", entryStates(rep))
	}

	changed := strings.Replace(validateBase, `return "a"`, `return "b"`, 1)
	rep, err = CheckBranch(fakeBranch([]string{path}, map[string]string{path: validateBase}, map[string]string{path: changed}), "v0.53.0")
	if err != nil {
		t.Fatal(err)
	}
	if got := entryStates(rep); got != "checklist-gates=stale" {
		t.Errorf("changed checkRule: %s", got)
	}
}

func TestCheckBranchTemplatesNeedAStampBump(t *testing.T) {
	path := TemplatesPath + "/development.yaml"
	stale := fakeBranch([]string{path}, map[string]string{}, map[string]string{path: "template: development@v0.53.0\nstatuses: []\n"})
	rep, err := CheckBranch(stale, "v0.53.0")
	if err != nil {
		t.Fatal(err)
	}
	if got := entryStates(rep); got != "template:development=stale" || rep.OK() {
		t.Errorf("stale template: %s", got)
	}
	bumped := fakeBranch([]string{path}, map[string]string{}, map[string]string{path: "template: development@v0.54.0\nstatuses: []\n"})
	rep, err = CheckBranch(bumped, "v0.53.0")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK() {
		t.Errorf("bumped template: %s", entryStates(rep))
	}
}

func TestCheckBranchNeedsTheCatalog(t *testing.T) {
	b := Branch{Head: func(string) ([]byte, bool) { return nil, false }, Base: func(string) ([]byte, bool) { return nil, false }}
	if _, err := CheckBranch(b, "v0.53.0"); err == nil || !strings.Contains(err.Error(), "issue-viewer repository") {
		t.Fatalf("err = %v", err)
	}
}

func TestReleaseHelpers(t *testing.T) {
	last, ok := LastRelease([]byte("# Changelog\n\n## Unreleased\n\n- x\n\n## v0.53.0 — 2026-10-23\n\n## v0.52.0\n"))
	if !ok || last != "v0.53.0" || NextRelease(last) != "v0.54.0" {
		t.Errorf("last = %s %v, next = %s", last, ok, NextRelease(last))
	}
	if _, ok := LastRelease([]byte("## Unreleased\n")); ok {
		t.Error("no release heading should not parse")
	}
}

func TestMatchSource(t *testing.T) {
	cases := []struct {
		glob, path string
		want       bool
	}{
		{"internal/ci/**", "internal/ci/run/x.go", true},
		{"internal/ci/**", "internal/ci.go", false},
		{"ci_watch.go", "ci_watch.go", true},
		{"ci_watch.go", "internal/x/ci_watch.go", false},
		{"cmd/issue-cli/cmd_ci*.go", "cmd/issue-cli/cmd_ci_gotest.go", true},
	}
	for _, c := range cases {
		if got := MatchSource(c.glob, c.path); got != c.want {
			t.Errorf("MatchSource(%s, %s) = %v", c.glob, c.path, got)
		}
	}
}

const playtestCatalog = `entries:
  - id: health-tuning
    version: v0.53.0
    sources: ["internal/health/**"]
  - id: playtest-sessions
    version: v0.40.0
    sources: ["cmd/playtest/**"]
`

func TestCheckBranchExemptTestOnly(t *testing.T) {
	b := fakeBranch([]string{
		"internal/health/score_test.go",
		"internal/health/testdata/snap.json",
		"cmd/playtest/scenario_test.go",
		"playtest/scenarios/status/scenario.yaml",
	}, map[string]string{}, map[string]string{CatalogPath: playtestCatalog})
	rep, err := CheckBranch(b, "v0.53.0")
	if err != nil {
		t.Fatal(err)
	}
	if got := entryStates(rep); got != "" || !rep.OK() {
		t.Errorf("test-only branch: states = %q, ok = %v", got, rep.OK())
	}
}

func TestCheckBranchExemptFixtureKeysNotSessionKeys(t *testing.T) {
	fixture := fakeBranch([]string{"cmd/playtest/scenario.go"}, map[string]string{}, map[string]string{CatalogPath: playtestCatalog})
	rep, err := CheckBranch(fixture, "v0.53.0")
	if err != nil {
		t.Fatal(err)
	}
	if got := entryStates(rep); got != "" || !rep.OK() {
		t.Errorf("fixture-only: states = %q, ok = %v", got, rep.OK())
	}
	for _, f := range []string{"cmd/playtest/session.go", "cmd/playtest/vocab.go"} {
		session := fakeBranch([]string{f, "cmd/playtest/scenario.go"}, map[string]string{}, map[string]string{CatalogPath: playtestCatalog})
		rep, err := CheckBranch(session, "v0.53.0")
		if err != nil {
			t.Fatal(err)
		}
		if got := entryStates(rep); got != "playtest-sessions=stale" || rep.OK() {
			t.Errorf("%s: states = %q, ok = %v", f, got, rep.OK())
		}
		if files := strings.Join(rep.Entries[0].Files, ","); files != f {
			t.Errorf("%s: files = %s, want the session file only", f, files)
		}
	}
}

func TestExemptPaths(t *testing.T) {
	for p, want := range map[string]bool{
		"internal/health/score_test.go":             true,
		"score_test.go":                             true,
		"cmd/playtest/testdata/x/y.yaml":            true,
		"playtest/scenarios/basic/tracker/A/a.md":   true,
		"cmd/playtest/scenario.go":                  true,
		"cmd/playtest/session.go":                   false,
		"internal/health/score.go":                  false,
		"internal/health/testdata.go":               false,
		"playtest/sessions/x.yaml":                  false,
		"cmd/issue-cli/templates/workflow/dev.yaml": false,
	} {
		if got := Exempt(p); got != want {
			t.Errorf("Exempt(%q) = %v, want %v", p, got, want)
		}
	}
}
