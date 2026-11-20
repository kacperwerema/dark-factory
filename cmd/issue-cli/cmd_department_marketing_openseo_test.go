package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/depttemplate"
	"github.com/kacperwerema/dark-factory/internal/lint"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// TestDepartmentAddMarketingOpenSEOExample: department add marketing on a
// sandbox prints and writes the openseo connector example commented out
// inside Marketing's entry, so the department gets no connector and lint
// is clean; uncommented as its header says, it is a url: connector lint
// still finds nothing in; department remove takes the comments with it.
func TestDepartmentAddMarketingOpenSEOExample(t *testing.T) {
	asLead(t)
	proj, cfg := deptTemplateProject(t)
	ctx, out := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "marketing"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# A rankings source for the seo-pass measure step: OpenSEO",
		"# connectors:",
		"#   - name: openseo",
		"#     url: http://localhost:3011/mcp",
		"#     deny_tools: [submit_indexnow_urls, set_report_sharing, set_ai_visibility_schedule]",
		"#     # headers:",
	} {
		assertContains(t, out.String(), want)
		assertContains(t, string(raw), want)
	}
	if strings.Contains(string(raw), "keychain:dataforseo") || strings.Contains(string(raw), "DATAFORSEO_API_KEY:") {
		t.Errorf("the DataForSEO key is a connector secret in projects.yaml:\n%s", raw)
	}
	// The comments sit inside the entry, before its goals:.
	if c, g := strings.Index(string(raw), "# connectors:"), strings.Index(string(raw), "goals:"); c < 0 || g < c {
		t.Errorf("example not before the entry's goals: (at %d, goals at %d):\n%s", c, g, raw)
	}

	proj = reloadDeptProject(t, cfg)
	if d := proj.DepartmentByKey("marketing"); d == nil || len(d.Connectors) != 0 {
		t.Fatalf("marketing installed with connectors %+v, want none (the example is commented)", d)
	}
	for _, f := range []string{"brand.md", "icp.md"} {
		mustWrite(t, filepath.Join(proj.Root(), "departments", "marketing", f), "# settled\n")
	}
	assertMarketingLintClean(t, proj, "commented")

	// Uncomment from connectors: down, the way the header says.
	lines := strings.SplitAfter(string(raw), "\n")
	on := false
	re := regexp.MustCompile(`^(\s*)# ?`)
	for i, l := range lines {
		if strings.Contains(l, "# connectors:") {
			on = true
		}
		if on && !re.MatchString(l) {
			break
		}
		if on {
			lines[i] = re.ReplaceAllString(l, "$1")
		}
	}
	if err := os.WriteFile(cfg, []byte(strings.Join(lines, "")), 0o644); err != nil {
		t.Fatal(err)
	}
	proj = reloadDeptProject(t, cfg)
	d := proj.DepartmentByKey("marketing")
	// The umami, social, social-analytics and reddit examples below it are
	// uncommented too (TestDepartmentAddWritesTheUmamiExampleCommented,
	// TestMarketingSocialExampleIsPostizPerRun,
	// TestMarketingSocialAnalyticsExampleIsRestPerRun,
	// TestMarketingRedditExampleIsReadOnly).
	// Looked up by name: another example in the template adds an entry and
	// turns nothing here red.
	if d == nil {
		t.Fatal("uncommented: no marketing department")
	}
	for _, name := range []string{"openseo", "umami", "social", "social-analytics", "reddit", "cms"} {
		if deptConnector(d, name) == nil {
			t.Errorf("uncommented: no %s connector in %+v", name, d.Connectors)
		}
	}
	if c := deptConnector(d, "openseo"); c != nil && (c.Kind != tracker.ConnectorMCP || c.URL != "http://localhost:3011/mcp" || len(c.Missing()) != 0) {
		t.Errorf("uncommented connector %+v (missing %v)", *c, c.Missing())
	}
	assertMarketingLintClean(t, proj, "uncommented")

	if err := depttemplate.RemoveEntry(cfg, proj.Slug, "Marketing"); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(cfg); strings.Contains(string(raw), "name: openseo") || strings.Contains(string(raw), "rankings source") {
		t.Errorf("department remove left the example behind:\n%s", raw)
	}
}

func assertMarketingLintClean(t *testing.T, proj *tracker.Project, state string) {
	t.Helper()
	env, err := lint.Load(proj, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rep, err := lint.Run(env)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range rep.Findings {
		if isUnwrittenVisitsFile(f) || isSocialExceptionMissing(f) || isTemplateSample(f) || isGoalWithoutWork(f) {
			continue
		}
		if s := strings.ToLower(f.Where + " " + f.Message); strings.Contains(s, "marketing") || strings.Contains(s, "connector") || strings.Contains(s, "openseo") {
			t.Errorf("%s: lint finding: %+v", state, f)
		}
	}
}

// isGoalWithoutWork is a goal of the freshly added department with no
// work on the board yet: coverage, not the department's config, which is
// what these tests check (lint goal-without-work, TestLintGoalWithoutWork).
func isGoalWithoutWork(f lint.Finding) bool { return f.Check == "goal-without-work" }

// isUnwrittenVisitsFile is lint's one expected word on a fresh Marketing:
// its visits goal's metrics file isn't written until the weekly SEO pass
// first runs (TestMarketingVisitsGoalReadsTheJobFile).
// isTemplateSample is lint's word on a department add left on the
// template's sample mission and goals (TestDepartmentSamplesEveryTemplate).
func isTemplateSample(f lint.Finding) bool {
	return f.Check == "departments" && strings.Contains(f.Message, "template's sample")
}

func isUnwrittenVisitsFile(f lint.Finding) bool {
	return f.Check == "departments" && strings.HasPrefix(f.Message, "goal visits: source file ") &&
		strings.Contains(f.Message, filepath.Join(".goals", "marketing", "weekly-seo-pass.json")+" does not exist (job marketing/weekly-seo-pass writes it on its first run")
}

// isSocialExceptionMissing is lint's word on the uncommented social
// connector (Postiz, AGPL) while the project's board holds no exception of
// its own: each project's owner records theirs. With one on the board it is
// clean (TestMarketingSocialExampleIsPostizPerRun).
func isSocialExceptionMissing(f lint.Finding) bool {
	return f.Check == "departments" && (strings.HasPrefix(f.Message, "connector social: Postiz is AGPL-3.0 (partners catalog)") ||
		strings.HasPrefix(f.Message, "connector social-analytics: Postiz is AGPL-3.0 (partners catalog)")) &&
		strings.Contains(f.Message, "names no decision on this board")
}

// deptConnector is d's connector called name, nil when it has none: tests
// of a bundled template find its entries by name, never by position or
// count, so a new entry in the template breaks none of them.
func deptConnector(d *tracker.Department, name string) *tracker.Connector {
	for i := range d.Connectors {
		if d.Connectors[i].Name == name {
			return &d.Connectors[i]
		}
	}
	return nil
}
