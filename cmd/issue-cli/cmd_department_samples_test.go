package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/depttemplate"
	"github.com/kacperwerema/dark-factory/internal/lint"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// sampleFindings are lint's departments findings that say a mission or
// goals are still a template's sample.
func sampleFindings(t *testing.T, proj *tracker.Project) []lint.Finding {
	t.Helper()
	env, err := lint.Load(proj, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rep, err := lint.Run(env)
	if err != nil {
		t.Fatal(err)
	}
	var out []lint.Finding
	for _, f := range rep.Findings {
		if isTemplateSample(f) {
			out = append(out, f)
		}
	}
	return out
}

func lastOutLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}

// changedLines are the 1-based numbers of the lines that differ between a
// and b, which must have as many lines.
func changedLines(t *testing.T, a, b []byte) []int {
	t.Helper()
	la, lb := strings.SplitAfter(string(a), "\n"), strings.SplitAfter(string(b), "\n")
	if len(la) != len(lb) {
		t.Fatalf("line count %d → %d:\n%s", len(la), len(lb), b)
	}
	var out []int
	for i := range la {
		if la[i] != lb[i] {
			out = append(out, i+1)
		}
	}
	return out
}

// TestDepartmentSamplesEveryTemplate walks each bundled template: add
// writes the sample mission and goals marked and its last line says how to
// set them, lint flags them naming the update command, and update
// --mission/--goal changes only those lines, after which lint is quiet.
func TestDepartmentSamplesEveryTemplate(t *testing.T) {
	asLead(t)
	for _, key := range []string{"marketing", "gtm", "legal", "video", "sales-b2c"} {
		t.Run(key, func(t *testing.T) {
			tmpl, err := depttemplate.Load(key)
			if err != nil {
				t.Fatal(err)
			}
			proj, cfg := deptTemplateProject(t)
			ctx, out := goalsCtx(t, proj, false)
			if err := runDepartment(ctx, []string{"add", key}); err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(cfg)
			if n := strings.Count(string(raw), "mission: "); n == 0 || strings.Count(string(raw), " # sample: replace") < n+len(tmpl.Goals) {
				t.Errorf("want the mission and each of %d goals marked:\n%s", len(tmpl.Goals), raw)
			}
			dkey := tracker.DepartmentKey(tmpl.Name)
			last := lastOutLine(out.String())
			assertContains(t, last, "Set this project's own mission and goals")
			assertContains(t, last, `department update `+dkey+` --mission "…" --goal "<id>=<name>/<target>/<period>"`)
			if key == "gtm" {
				assertContains(t, last, "department update marketing --mission")
			}

			proj = reloadDeptProject(t, cfg)
			var mine []lint.Finding
			for _, f := range sampleFindings(t, proj) {
				if f.Where == "projects.yaml ("+tmpl.Name+")" {
					mine = append(mine, f)
				}
			}
			if len(mine) != 2 {
				t.Fatalf("want a mission and a goals finding for %s, got %+v", tmpl.Name, mine)
			}
			assertContains(t, mine[0].Message, "mission is still the "+key+" template's sample")
			assertContains(t, mine[0].Fix, "department update "+dkey+` --mission "<this project's mission>"`)
			for _, g := range tmpl.Goals {
				assertContains(t, mine[1].Message, g.ID)
				assertContains(t, mine[1].Fix, `--goal "`+g.ID+`=<name>/<target>/<period>"`)
			}

			args := []string{"update", dkey, "--mission", "Acme's own " + key + " mission"}
			for _, g := range tmpl.Goals {
				args = append(args, "--goal", g.ID+"=Own "+g.Name+"/7/month")
			}
			before, _ := os.ReadFile(cfg)
			ctx, out = goalsCtx(t, proj, false)
			if err := runDepartment(ctx, args); err != nil {
				t.Fatal(err)
			}
			assertContains(t, out.String(), "✓ Updated "+tmpl.Name+"'s own mission and goals")
			after, _ := os.ReadFile(cfg)
			changed := changedLines(t, before, after)
			// The mission line; per goal its id line (marker gone) and its
			// name, target and period lines (or fewer when one already read so).
			if len(changed) < 1+2*len(tmpl.Goals) || len(changed) > 1+4*len(tmpl.Goals) {
				t.Errorf("changed lines %v, want the mission and the goals' lines only:\n%s", changed, after)
			}
			bl, al := strings.SplitAfter(string(before), "\n"), strings.SplitAfter(string(after), "\n")
			for _, n := range changed {
				was := strings.TrimSpace(bl[n-1])
				if !strings.HasPrefix(was, "mission:") && !strings.HasPrefix(was, "- id:") && !strings.HasPrefix(was, "name:") && !strings.HasPrefix(was, "target:") && !strings.HasPrefix(was, "period:") {
					t.Errorf("line %d changed and is not a mission or goal line: %q → %q", n, bl[n-1], al[n-1])
				}
			}
			proj = reloadDeptProject(t, cfg)
			d := proj.DepartmentByKey(dkey)
			if d.Mission != "Acme's own "+key+" mission" || len(d.Goals) != len(tmpl.Goals) || d.Goals[0].Name != "Own "+tmpl.Goals[0].Name || *d.Goals[0].Target != 7 || d.Goals[0].Period != "month" {
				t.Errorf("after update: %+v", d)
			}
			for _, f := range sampleFindings(t, proj) {
				if f.Where == "projects.yaml ("+tmpl.Name+")" {
					t.Errorf("still flagged after update: %+v", f)
				}
			}
		})
	}
}

// TestDepartmentAddOwnMissionAndGoals: given --mission and --goal, the entry
// has the project's own, unmarked; a goal id the template has keeps its
// source; the add's last line says nothing of samples and lint is quiet.
func TestDepartmentAddOwnMissionAndGoals(t *testing.T) {
	asLead(t)
	proj, cfg := deptTemplateProject(t)

	ctx, out := goalsCtx(t, proj, false)
	args := []string{"add", "marketing", "--mission", "Acme's shop found on Google", "--goal", "visits=Visits/200/week", "--goal", "leads=Leads / quotes/5/month", "--dry-run"}
	if err := runDepartment(ctx, args); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), `--mission 'Acme'\''s shop found on Google' --goal 'visits=Visits/200/week' --goal 'leads=Leads / quotes/5/month'`)

	ctx, out = goalsCtx(t, proj, false)
	if err := runDepartment(ctx, args[:len(args)-1]); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "sample") {
		t.Errorf("own mission and goals, but the output speaks of samples:\n%s", out)
	}
	raw, _ := os.ReadFile(cfg)
	if strings.Contains(string(raw), "# sample") {
		t.Errorf("own values marked as samples:\n%s", raw)
	}
	proj = reloadDeptProject(t, cfg)
	d := proj.DepartmentByKey("marketing")
	if d.Mission != "Acme's shop found on Google" || len(d.Goals) != 2 || d.Goals[0].ID != "visits" || d.Goals[1].Name != "Leads / quotes" || *d.Goals[1].Target != 5 {
		t.Fatalf("entry %+v", d)
	}
	if !strings.HasPrefix(d.Goals[0].Source, "file:") {
		t.Errorf("visits lost the template's source: %+v", d.Goals[0])
	}
	if f := sampleFindings(t, proj); len(f) != 0 {
		t.Errorf("lint flags own values: %+v", f)
	}

	for _, bad := range [][]string{
		{"add", "legal", "--goal", "x=Name/ten/week"},
		{"add", "legal", "--goal", "x=Name/10/year"},
		{"add", "legal", "--goal", "Name/10/week"},
		{"add", "legal", "--goal", "x=a/1/week", "--goal", "x=b/2/week"},
	} {
		ctx, _ := goalsCtx(t, proj, false)
		if err := runDepartment(ctx, bad); err == nil {
			t.Errorf("%v: want an error", bad)
		}
	}
}

const legacyConfig = `# owner's config
projects:
  - name: Acme   # the sandbox
    issues: ISSUES
    workdir: ROOT
    departments:
      - name: GTM
        color: '#F2A541'
        role: 'Brings in the next clients: marketing and sales under one lead'
        mission: A steady pipeline of new clients   # from an old release
        goals:
          - id: pipeline
            name: New client conversations
            target: 8
            period: month
        jobs:
          - id: weekly-plan
            every: week
            brief: departments/gtm/jobs/weekly-plan.md
      - name: Marketing
        parent: gtm
        mission: "Our own"
        goals:
          - id: posts
            name: Posts published
            target: 2
            period: week
`

// TestDepartmentUpdateOwnOnAnOlderEntry: an entry from before the markers
// (an earlier release's sample texts) is flagged by its values; update
// keeps every other byte, keeping a sample marks it kept, a new goal is
// appended and --drop-goal removes one.
func TestDepartmentUpdateOwnOnAnOlderEntry(t *testing.T) {
	asLead(t)
	root := t.TempDir()
	issues := filepath.Join(root, "issues")
	if err := os.MkdirAll(issues, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "projects.yaml")
	mustWrite(t, cfg, strings.NewReplacer("ISSUES", issues, "ROOT", root).Replace(legacyConfig))
	proj := reloadDeptProject(t, cfg)

	f := sampleFindings(t, proj)
	if len(f) != 3 {
		t.Fatalf("want GTM's mission and goal and Marketing's posts flagged, got %+v", f)
	}
	// Templates in List order: Marketing before GTM.
	assertContains(t, f[0].Where, "Marketing")
	assertContains(t, f[0].Fix, `department update marketing --goal "posts=<name>/<target>/<period>"`)
	assertContains(t, f[1].Message, `mission is still the gtm template's sample ("A steady pipeline of new clients")`)
	assertContains(t, f[2].Message, "goal pipeline is still the gtm template's sample")

	run := func(args ...string) string {
		t.Helper()
		ctx, out := goalsCtx(t, proj, false)
		if err := runDepartment(ctx, append([]string{"update"}, args...)); err != nil {
			t.Fatal(err)
		}
		proj = reloadDeptProject(t, cfg)
		return out.String()
	}

	// Mission: only its value changes; its own comment stays.
	before, _ := os.ReadFile(cfg)
	run("gtm", "--mission", "Ten new shops a month")
	after, _ := os.ReadFile(cfg)
	if c := changedLines(t, before, after); len(c) != 1 || !strings.Contains(string(after), "mission: Ten new shops a month   # from an old release\n") {
		t.Errorf("changed %v:\n%s", c, after)
	}

	// Keeping a sample goal as it is: same value, marked kept, not flagged.
	before = after
	out := run("marketing", "--goal", "posts=Posts published/2/week")
	after, _ = os.ReadFile(cfg)
	if c := changedLines(t, before, after); len(c) != 1 || !strings.Contains(string(after), "- id: posts # sample: kept\n") {
		t.Errorf("changed %v:\n%s", c, after)
	}
	if strings.Contains(out, "Still the") {
		t.Errorf("kept sample still named:\n%s", out)
	}

	// A new goal goes after the last one; the old sample goal is dropped.
	before = after
	out = run("gtm", "--goal", "shops=New shops/10/month", "--drop-goal", "pipeline", "--dry-run")
	assertContains(t, out, "Dry run: department update gtm (nothing changed)")
	assertContains(t, out, "goal pipeline: would remove")
	if after, _ = os.ReadFile(cfg); string(after) != string(before) {
		t.Fatal("dry run changed projects.yaml")
	}
	run("gtm", "--goal", "shops=New shops/10/month", "--drop-goal", "pipeline")
	after, _ = os.ReadFile(cfg)
	want := strings.Replace(string(before), `          - id: pipeline
            name: New client conversations
            target: 8
            period: month
`, `          - id: shops
            name: New shops
            target: 10
            period: month
`, 1)
	if string(after) != want {
		t.Errorf("projects.yaml:\n%s\nwant:\n%s", after, want)
	}
	if f := sampleFindings(t, proj); len(f) != 0 {
		t.Errorf("still flagged: %+v", f)
	}

	// A goal without a period gets the line; an unknown id to drop is refused.
	ctx, _ := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"update", "gtm", "--drop-goal", "nope"}); err == nil || !strings.Contains(err.Error(), "has no goal nope (goals: shops)") {
		t.Errorf("err = %v", err)
	}
	if err := runDepartment(ctx, []string{"update", "gtm", "--goal", "shops=x/1/week", "--side"}); err == nil {
		t.Error("--side with --goal: want a usage error")
	}
}

// TestDepartmentUpdateOwnAddsMissingLines: an entry with no mission: and no
// goals: gets both, inside the item, under its keys.
func TestDepartmentUpdateOwnAddsMissingLines(t *testing.T) {
	asLead(t)
	root := t.TempDir()
	issues := filepath.Join(root, "issues")
	if err := os.MkdirAll(issues, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "projects.yaml")
	mustWrite(t, cfg, strings.NewReplacer("ISSUES", issues, "ROOT", root).Replace(`projects:
  - name: Acme
    issues: ISSUES
    workdir: ROOT
    departments:
      - name: Ops
        role: Runs things
      - name: Other
        goals:
          - id: a
            name: A
`))
	proj := reloadDeptProject(t, cfg)
	ctx, _ := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"update", "ops", "--mission", "Things run", "--goal", "uptime=Uptime/99.9/month"}); err != nil {
		t.Fatal(err)
	}
	ctx, _ = goalsCtx(t, reloadDeptProject(t, cfg), false)
	if err := runDepartment(ctx, []string{"update", "other", "--goal", "a=A/3/week"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(cfg)
	want := `      - name: Ops
        role: Runs things
        mission: Things run
        goals:
          - id: uptime
            name: Uptime
            target: 99.9
            period: month
      - name: Other
        goals:
          - id: a
            name: A
            target: 3
            period: week
`
	if !strings.HasSuffix(string(raw), want) {
		t.Errorf("projects.yaml:\n%s\nwant to end with:\n%s", raw, want)
	}
}
