package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/depttemplate"
	"github.com/kacperwerema/dark-factory/internal/goals"
	"github.com/kacperwerema/dark-factory/internal/lint"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

// TestDepartmentAddSalesB2CUnderGTM: on a sandbox project with a GTM
// group, department add gtm names Sales as a department that can join it,
// a top-level add says how to put it under GTM, and department add
// sales-b2c --parent gtm plans the workflow, role and job briefs and the
// projects.yaml entry under GTM. The handoffs are printed, never written.
func TestDepartmentAddSalesB2CUnderGTM(t *testing.T) {
	asLead(t)
	proj, cfg := deptTemplateProject(t)

	ctx, out := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "gtm"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Can join GTM later: Sales (issue-cli --project acme department add sales-b2c --parent gtm)")
	proj = reloadDeptProject(t, cfg)

	// Without --parent: the plan says GTM takes it.
	ctx, out = goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "sales-b2c", "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "GTM is a group here and takes Sales: add --parent gtm to put it under GTM")

	ctx, out = goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "sales-b2c", "--parent", "gtm", "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{
		"Dry run: department add sales-b2c (nothing changed)",
		"workflows/sales.yaml: write",
		"departments/sales/role.md: write",
		"departments/sales/jobs/weekly-funnel.md: write",
		"departments/sales/jobs/support-digest.md: write",
		"parent: gtm",
		"Handoffs Sales usually has",
		"from: marketing",
		"via: hq",
		"Apply: issue-cli --project acme department add sales-b2c --parent gtm",
	} {
		assertContains(t, s, want)
	}
	if strings.Contains(s, "takes Sales") {
		t.Error("with --parent gtm the plan still says GTM takes it")
	}
	before, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}

	ctx, out = goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "sales-b2c", "--parent", "gtm"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "✓ Added department Sales (template sales-b2c)")
	after, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "connections:") {
		t.Errorf("department add wrote connections: into projects.yaml:\n%s", after)
	}
	if !strings.HasPrefix(string(after), strings.TrimRight(string(before), "\n")[:20]) {
		t.Errorf("projects.yaml's head changed")
	}

	proj = reloadDeptProject(t, cfg)
	d := proj.DepartmentByKey("sales")
	if d == nil {
		t.Fatal("no Sales department")
	}
	if d.Parent != "gtm" || d.Workflow != "workflows/sales.yaml" || d.Prompt != "departments/sales/role.md" {
		t.Errorf("Sales entry = parent %q workflow %q prompt %q", d.Parent, d.Workflow, d.Prompt)
	}
	var ids []string
	for _, g := range d.Goals {
		ids = append(ids, g.ID)
		if g.Source != "file:<issues>/.goals/sales/weekly-funnel.json#"+g.ID {
			t.Errorf("goal %s source = %q, want the funnel file", g.ID, g.Source)
		}
	}
	if strings.Join(ids, ",") != "visits,signups,testers,retention" {
		t.Errorf("goals = %v", ids)
	}
	if len(d.Jobs) != 2 || d.Jobs[0].Type != "funnel-review" || d.Jobs[1].Type != "support-digest" {
		t.Errorf("jobs = %+v", d.Jobs)
	}
	// The umami example is in the entry, commented: no live connector.
	if len(d.Connectors) != 0 {
		t.Errorf("the template installs no live connector: %+v", d.Connectors)
	}
	for _, want := range []string{"#   - name: umami", `#       Authorization: "Bearer keychain:umami-api-key"`} {
		assertContains(t, string(after), want)
	}
	if _, err := proj.WorkflowFor("Sales"); err != nil {
		t.Fatalf("department workflow: %v", err)
	}
	// Until the weekly funnel review runs, the goals have no value and say
	// why, not a read error.
	g := goals.Resolve(proj, time.Now()).Goal("sales/visits")
	if g == nil || g.Value != nil || !strings.HasPrefix(g.Err, "no value yet: <issues>/.goals/sales/weekly-funnel.json is not written yet (job sales/weekly-funnel writes it") {
		t.Errorf("goal sales/visits = %+v", g)
	}

	// --json carries the handoffs; a project that declares them already
	// gets none printed.
	proj2, cfg2 := deptTemplateProject(t)
	ctx, out = goalsCtx(t, proj2, true)
	if err := runDepartment(ctx, []string{"add", "sales-b2c", "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	var plan struct {
		Connections string `json:"connections"`
	}
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Connections, "requests:") || !strings.Contains(plan.Connections, "feeds:") {
		t.Errorf("json connections = %q", plan.Connections)
	}
	raw, _ := os.ReadFile(cfg2)
	mustWrite(t, cfg2, strings.Replace(string(raw), "projects:\n  - name: Acme   # the sandbox\n", "projects:\n  - name: Acme   # the sandbox\n    connections:\n      requests:\n        - {from: sales, to: product, via: hq}\n      feeds:\n        - {from: marketing, to: sales}\n", 1))
	proj2 = reloadDeptProject(t, cfg2)
	ctx, out = goalsCtx(t, proj2, false)
	if err := runDepartment(ctx, []string{"add", "sales-b2c", "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Handoffs Sales usually has") {
		t.Errorf("handoffs the project declares are printed again:\n%s", out)
	}
}

// salesWorkflow is the bundled sales-b2c workflow file, with its "when you
// start charging" block uncommented when charging is set.
func salesWorkflow(t *testing.T, charging bool) string {
	t.Helper()
	tmpl, err := depttemplate.Load("sales-b2c")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := workflowupgrade.ReadTemplate(tmpl.Workflow)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !charging {
		return text
	}
	const marker = "  # BEGIN when-you-start-charging\n"
	begin := strings.Index(text, marker)
	end := strings.Index(text, "  # END when-you-start-charging\n")
	if begin < 0 || end < begin {
		t.Fatal("no BEGIN/END when-you-start-charging block")
	}
	begin += len(marker)
	block := regexp.MustCompile(`(?m)^  # ?`).ReplaceAllString(text[begin:end], "  ")
	return text[:begin] + block + text[end:]
}

// TestSalesB2CWorkflowMoneyGatesOwnerOnly: every type's way into publish,
// the only step where a customer sees something or money moves, passes
// the owner_only gate, which YOLO never passes, and no other gate is
// owner_only. The money types are commented out; uncommented, they load
// and keep the same gate, checkout and price changes behind Legal's word.
func TestSalesB2CWorkflowMoneyGatesOwnerOnly(t *testing.T) {
	for _, charging := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "sales.yaml")
		mustWrite(t, path, salesWorkflow(t, charging))
		wf, err := tracker.LoadWorkflow(path)
		if err != nil {
			t.Fatalf("charging=%v: %v", charging, err)
		}
		want := []string{"offer", "landing", "launch", "funnel-review", "support-digest"}
		money := []string{"checkout", "price-change", "refund"}
		for _, m := range money {
			if _, ok := wf.Types[m]; ok != charging {
				t.Errorf("charging=%v: type %s present = %v", charging, m, ok)
			}
		}
		if charging {
			want = append(want, money...)
		}
		for _, typ := range want {
			eff := wf.ForIssue(&tracker.Issue{Type: typ, Status: "brief"})
			path := wf.Types[typ].Path
			publishes := false
			for i := 1; i < len(path); i++ {
				from, to := path[i-1], path[i]
				var tr *tracker.WorkflowTransition
				for j := range eff.Transitions {
					if eff.Transitions[j].From == from && eff.Transitions[j].To == to {
						tr = &eff.Transitions[j]
					}
				}
				if tr == nil {
					t.Errorf("charging=%v %s: no %s → %s", charging, typ, from, to)
					continue
				}
				owner, legal := false, false
				for _, a := range tr.Actions {
					owner = owner || (a.Type == "require_human_approval" && a.OwnerOnly)
					legal = legal || (a.Type == "validate" && a.Rule == "has_comment_prefix: legal:")
				}
				if to == "publish" {
					publishes = true
					if !owner {
						t.Errorf("charging=%v %s: %s → publish is not owner_only", charging, typ, from)
					}
					if (typ == "checkout" || typ == "price-change") && !legal {
						t.Errorf("%s: %s → publish doesn't wait for Legal", typ, from)
					}
				} else if owner {
					t.Errorf("charging=%v %s: %s → %s is owner_only: the design names only → publish", charging, typ, from, to)
				}
			}
			reports := typ == "funnel-review" || typ == "support-digest"
			if publishes == reports {
				t.Errorf("%s: publishes = %v (a report never goes out; everything else goes through publish)", typ, publishes)
			}
		}
		if wf.DefaultType != "offer" {
			t.Errorf("default type = %q", wf.DefaultType)
		}
	}
}

// TestSalesB2CDepartmentLintClean: with GTM and Sales installed and
// Marketing's brand foundation written, lint finds nothing in the sales
// workflow, uncommented money block included, and says one thing about
// Sales: its goals' file isn't written until the weekly funnel review runs.
func TestSalesB2CDepartmentLintClean(t *testing.T) {
	asLead(t)
	proj, cfg := deptTemplateProject(t)
	for _, args := range [][]string{{"add", "gtm"}, {"add", "sales-b2c", "--parent", "gtm"}} {
		ctx, _ := goalsCtx(t, reloadDeptProject(t, cfg), false)
		if err := runDepartment(ctx, args); err != nil {
			t.Fatal(err)
		}
	}
	proj = reloadDeptProject(t, cfg)
	for _, f := range []string{"brand.md", "icp.md"} {
		mustWrite(t, filepath.Join(proj.Root(), "departments", "marketing", f), "# settled\n")
	}
	wfPath := filepath.Join(proj.Root(), "workflows", "sales.yaml")
	for _, charging := range []bool{false, true} {
		if charging {
			if err := os.Remove(wfPath); err != nil {
				t.Fatal(err)
			}
			mustWrite(t, wfPath, salesWorkflow(t, true))
		}
		env, err := lint.Load(proj, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		rep, err := lint.Run(env)
		if err != nil {
			t.Fatal(err)
		}
		funnel := 0
		for _, f := range rep.Findings {
			switch {
			case isUnwrittenVisitsFile(f):
			case strings.Contains(f.Where, "sales.yaml") || strings.Contains(f.Message, "sales.yaml"):
				t.Errorf("charging=%v: finding in the sales workflow: %+v", charging, f)
			case f.Check == "departments" && strings.Contains(f.Message, "goals visits, signups, testers, retention: source file "+filepath.Join(proj.IssueDir, ".goals", "sales", "weekly-funnel.json")+" does not exist (job sales/weekly-funnel writes it on its first run"):
				funnel++
			case strings.Contains(strings.ToLower(f.Where+" "+f.Message), "sales"):
				t.Errorf("charging=%v: finding about Sales: %+v", charging, f)
			}
		}
		if funnel != 1 {
			t.Errorf("charging=%v: want one finding for the unwritten metrics file, got %d in %+v", charging, funnel, rep.Findings)
		}
	}
}

// TestSalesB2CGoalsReadJobFile: an installed Sales department's goals
// read the weekly funnel review's metrics file; the job's brief and its
// workflow's gate name the same file, and goal write fills it.
func TestSalesB2CGoalsReadJobFile(t *testing.T) {
	asLead(t)
	proj, cfg := deptTemplateProject(t)
	ctx, _ := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "sales-b2c"}); err != nil {
		t.Fatal(err)
	}
	proj = reloadDeptProject(t, cfg)

	brief, err := os.ReadFile(filepath.Join(proj.Root(), "departments", "sales", "jobs", "weekly-funnel.md"))
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, string(brief), "issue-cli goal write sales/weekly-funnel visits=512 signups=31")
	wf, err := proj.WorkflowFor("Sales")
	if err != nil {
		t.Fatal(err)
	}
	gate := ""
	for _, tr := range wf.Types["funnel-review"].Transitions {
		for _, a := range tr.Actions {
			if a.Rule == "file_exists" {
				gate = a.Path
			}
		}
	}
	if gate != "<issues>/.goals/{{department}}/weekly-funnel.json" {
		t.Errorf("funnel-review's file gate = %q, want the job's metrics file", gate)
	}

	ctx, _ = goalsCtx(t, proj, false)
	if err := runGoal(ctx, []string{"write", "sales/weekly-funnel", "visits=250", "signups=10", "testers=8", "retention=50"}); err != nil {
		t.Fatal(err)
	}
	rep := goals.Resolve(proj, ctx.now())
	for key, want := range map[string]float64{"sales/visits": 250, "sales/signups": 10, "sales/testers": 8, "sales/retention": 50} {
		g := rep.Goal(key)
		if g == nil || g.Err != "" || g.Value == nil || *g.Value != want || g.Job != "sales/weekly-funnel" {
			t.Errorf("%s = %+v, want %v from the job file", key, g, want)
		}
	}
}
