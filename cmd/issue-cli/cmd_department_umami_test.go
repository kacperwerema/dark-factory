package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/connectors"
	"github.com/kacperwerema/dark-factory/internal/goals"
	"github.com/kacperwerema/dark-factory/internal/lint"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// uncommentConnectors uncomments a projects.yaml's commented connector
// example the way its header says: from "# connectors:" down, one "# "
// dropped after the indent, until the first line that isn't a comment.
func uncommentConnectors(t *testing.T, cfg string) {
	t.Helper()
	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
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
	if !on {
		t.Fatalf("no commented connectors: in %s", cfg)
	}
	if err := os.WriteFile(cfg, []byte(strings.Join(lines, "")), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDepartmentAddWritesTheUmamiExampleCommented: department add
// marketing and sales-b2c each write an umami connector into the entry
// commented out: a read-only rest connector (GETs on the stats path the
// visits goal reads, no command a write could go through), its key a
// keychain: header reference, no per_run:. Installed, the department has
// no connector; uncommented, umami loads as that rest connector and lint
// says nothing about it.
func TestDepartmentAddWritesTheUmamiExampleCommented(t *testing.T) {
	for _, c := range []struct{ template, key string }{{"marketing", "marketing"}, {"sales-b2c", "sales"}} {
		t.Run(c.template, func(t *testing.T) {
			asLead(t)
			proj, cfg := deptTemplateProject(t)
			ctx, out := goalsCtx(t, proj, false)
			if err := runDepartment(ctx, []string{"add", c.template}); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(cfg)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{
				"Umami (MIT, umami.is)",
				"#   - name: umami",
				"#     kind: rest",
				"#     url: http://localhost:3000/api",
				`#       Authorization: "Bearer keychain:umami-api-key"`,
				"#     get: [/websites/*/stats]",
				"#     sends: false",
				"issue-cli connector exec " + c.key + "/umami --",
				"'/websites/<id>/stats?startAt=<ms>&endAt=<ms>'",
			} {
				assertContains(t, out.String(), want)
				assertContains(t, string(raw), want)
			}
			if c, g := strings.Index(string(raw), "#   - name: umami"), strings.Index(string(raw), "goals:"); g < c {
				t.Errorf("umami example not before the entry's goals: (at %d, goals at %d):\n%s", c, g, raw)
			}

			proj = reloadDeptProject(t, cfg)
			if d := proj.DepartmentByKey(c.key); d == nil || len(d.Connectors) != 0 {
				t.Fatalf("%s installed with connectors %+v, want none (the example is commented)", c.key, d)
			}

			uncommentConnectors(t, cfg)
			proj = reloadDeptProject(t, cfg)
			d := proj.DepartmentByKey(c.key)
			if d == nil {
				t.Fatalf("no %s department after uncommenting", c.key)
			}
			var um *tracker.Connector
			for i := range d.Connectors {
				if d.Connectors[i].Name == "umami" {
					um = &d.Connectors[i]
				}
			}
			if um == nil {
				t.Fatalf("uncommented: %s connectors %+v, want umami", c.key, d.Connectors)
			}
			if um.Kind != tracker.ConnectorRest || um.Command != "" || um.Sends || um.PerRun != nil || um.URL != "http://localhost:3000/api" {
				t.Errorf("umami = %+v, want a rest connector on http://localhost:3000/api, no command, sends false, no per_run", *um)
			}
			if strings.Join(um.Get, ",") != "/websites/*/stats" {
				t.Errorf("umami get: = %v, want only /websites/*/stats (the path the visits goal reads)", um.Get)
			}
			if len(um.Secrets) != 0 || len(um.Headers) != 1 || um.Headers["Authorization"] != "Bearer keychain:umami-api-key" {
				t.Errorf("umami secrets = %v, headers = %v, want only Authorization: Bearer keychain:umami-api-key", um.Secrets, um.Headers)
			}
			// The example's call is one get: allows; a write-shaped or
			// other path is refused.
			if _, err := connectors.RestPath(c.key+"/umami", um, "/websites/abc/stats?startAt=1&endAt=2"); err != nil {
				t.Errorf("the example's call is refused: %v", err)
			}
			for _, p := range []string{"/websites/abc", "/websites/abc/reset", "/me/websites", "/users"} {
				if _, err := connectors.RestPath(c.key+"/umami", um, p); err == nil {
					t.Errorf("umami reads %s, want only the stats path", p)
				}
			}
			for _, cn := range d.Connectors {
				for v, ref := range cn.Secrets {
					if r, err := tracker.ParseSecretRef(ref); err != nil || r.Scheme != "keychain" {
						t.Errorf("%s secret %s is not a keychain: reference (%v)", cn.Name, v, err)
					}
				}
			}

			if c.key == "marketing" {
				for _, f := range []string{"brand.md", "icp.md"} {
					mustWrite(t, filepath.Join(proj.Root(), "departments", "marketing", f), "# settled\n")
				}
			}
			env, err := lint.Load(proj, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			rep, err := lint.Run(env)
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range rep.Findings {
				if isSocialExceptionMissing(f) {
					continue
				}
				// The temp folder's name holds the test's name: leave it out.
				s := strings.ToLower(strings.ReplaceAll(f.Where+" "+f.Message, proj.Root(), "<root>"))
				if strings.Contains(s, "umami") || strings.Contains(s, "connector") {
					t.Errorf("uncommented: lint finding: %+v", f)
				}
			}
		})
	}
}

// TestMarketingVisitsGoalReadsTheJobFile: an installed Marketing
// department's visits goal reads the weekly SEO pass's metrics file; the
// job's brief names the goal write that fills it, and goal write does.
// pages and posts stay manual.
func TestMarketingVisitsGoalReadsTheJobFile(t *testing.T) {
	asLead(t)
	proj, cfg := deptTemplateProject(t)
	ctx, _ := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "marketing"}); err != nil {
		t.Fatal(err)
	}
	proj = reloadDeptProject(t, cfg)
	d := proj.DepartmentByKey("marketing")
	if d == nil {
		t.Fatal("no Marketing department")
	}
	sources := map[string]string{}
	for _, g := range d.Goals {
		sources[g.ID] = g.Source
	}
	if sources["visits"] != "file:<issues>/.goals/marketing/weekly-seo-pass.json#visits" || sources["pages"] != "" || sources["posts"] != "" {
		t.Errorf("goal sources = %v, want visits from the job file, pages and posts manual", sources)
	}

	brief, err := os.ReadFile(filepath.Join(proj.Root(), "departments", "marketing", "jobs", "weekly-seo-pass.md"))
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, string(brief), "issue-cli goal write marketing/weekly-seo-pass visits=")
	assertContains(t, string(brief), "`umami` connector")

	g := goals.Resolve(proj, time.Now()).Goal("marketing/visits")
	if g == nil || g.Value != nil || !strings.HasPrefix(g.Err, "no value yet: <issues>/.goals/marketing/weekly-seo-pass.json is not written yet (job marketing/weekly-seo-pass writes it") {
		t.Errorf("before the first run: marketing/visits = %+v", g)
	}

	ctx, _ = goalsCtx(t, proj, false)
	if err := runGoal(ctx, []string{"set", "marketing/visits", "3"}); err == nil || !strings.Contains(err.Error(), "issue-cli goal write marketing/weekly-seo-pass") {
		t.Errorf("goal set on the job-file goal: %v, want it to name goal write", err)
	}

	ctx, _ = goalsCtx(t, proj, false)
	if err := runGoal(ctx, []string{"write", "marketing/weekly-seo-pass", "visits=640"}); err != nil {
		t.Fatal(err)
	}
	g = goals.Resolve(proj, ctx.now()).Goal("marketing/visits")
	if g == nil || g.Err != "" || g.Value == nil || *g.Value != 640 || g.Job != "marketing/weekly-seo-pass" {
		t.Errorf("marketing/visits = %+v, want 640 from the job file", g)
	}
}

// TestDepartmentsDocNamesTheUmamiExample: docs/departments.md no longer
// says templates carry no connectors, and names the umami example and
// Marketing's visits goal.
func TestDepartmentsDocNamesTheUmamiExample(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "departments.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	for _, gone := range []string{"connectors in department templates", "**No connectors** in the template"} {
		if strings.Contains(doc, gone) {
			t.Errorf("docs/departments.md still says %q", gone)
		}
	}
	for _, want := range []string{"`umami`", "file:<issues>/.goals/marketing/weekly-seo-pass.json#visits", "goal write marketing/weekly-seo-pass"} {
		assertContains(t, doc, want)
	}
}

// TestUmamiExecExamplesMatchTheRestConnector: every place that shows how
// to read Umami (both connector examples, the weekly SEO pass and weekly
// funnel briefs, docs/departments.md) gives the one rest call on the stats
// path, and none still says curl or sh -c for it.
func TestUmamiExecExamplesMatchTheRestConnector(t *testing.T) {
	const call = "'/websites/<id>/stats?startAt=<ms>&endAt=<ms>'"
	for _, c := range []struct{ file, exec string }{
		{"templates/departments/marketing/connectors.example.yaml", "issue-cli connector exec marketing/umami --"},
		{"templates/departments/marketing/jobs/weekly-seo-pass.md", "issue-cli connector exec marketing/umami --"},
		{"templates/departments/sales-b2c/connectors.example.yaml", "issue-cli connector exec sales/umami --"},
		{"templates/departments/sales-b2c/jobs/weekly-funnel.md", "issue-cli connector exec sales/umami --"},
		{"../../docs/departments.md", "issue-cli connector exec marketing/umami --"},
		{"../../docs/departments.md", "issue-cli connector exec sales/umami --"},
	} {
		raw, err := os.ReadFile(c.file)
		if err != nil {
			t.Fatal(err)
		}
		s := string(raw)
		i := strings.Index(s, c.exec)
		if i < 0 {
			t.Errorf("%s: no %q", c.file, c.exec)
			continue
		}
		// The call follows the exec, on its line or the next.
		rest := s[i+len(c.exec):]
		if j := strings.Index(rest, call); j < 0 || strings.Count(rest[:j], "\n") > 1 {
			t.Errorf("%s: %q is not followed by %s", c.file, c.exec, call)
		}
		for _, gone := range []string{"umami -- sh -c", "UMAMI_API_KEY"} {
			if strings.Contains(s, gone) {
				t.Errorf("%s still says %q (umami is a rest connector now)", c.file, gone)
			}
		}
	}
}
