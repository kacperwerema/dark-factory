package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/depttemplate"
	"github.com/kacperwerema/dark-factory/internal/lint"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

const videoPin = "195eab24478dc9363326aec437aefc3701635ddf"

// TestDepartmentAddVideoPrintsInstallSteps: department add video on a
// sandbox project plans the department's files, records only the partner's
// path and pinned commit, and prints the install steps without running
// them: the chosen path stays absent (nothing cloned).
func TestDepartmentAddVideoPrintsInstallSteps(t *testing.T) {
	asLead(t)
	proj, cfg := deptTemplateProject(t)
	studio := filepath.Join(t.TempDir(), "studio") // the user's chosen path, not there yet

	ctx, out := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "video", "--partner-path", studio, "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	assertContains(t, s, "Dry run: department add video (nothing changed)")
	assertContains(t, s, "workflows/video.yaml: write")
	assertContains(t, s, "departments/video/role.md: write")
	assertContains(t, s, "git clone https://github.com/calesthio/openmontage "+studio)
	assertContains(t, s, "Apply: issue-cli --project acme department add video --partner-path "+studio)

	ctx, out = goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "video", "--partner-path", studio}); err != nil {
		t.Fatal(err)
	}
	s = out.String()
	assertContains(t, s, "✓ Added department Video (template video)")
	assertContains(t, s, "workflows/video.yaml: wrote")
	assertContains(t, s, "Partner: OpenMontage at "+studio+", pinned to "+videoPin+" (the entry records only the path and the commit; nothing was cloned or run)")
	for _, step := range []string{
		"git -C " + studio + " checkout " + videoPin,
		"make -C " + studio + " setup",
		"export HF_HOME=" + studio + "/.hf-cache",
		"export PATH=" + studio + "/.venv/bin:$PATH",
		`"telemetryEnabled": false`,
		"AGPL-3.0",
		"Remotion",
		"Models: each has its own licence",
	} {
		assertContains(t, s, step)
	}
	assertContains(t, s, "Its connectors are in the entry")
	if strings.Contains(s, "Templates carry no connectors") {
		t.Error("the video template carries its connector")
	}
	if _, err := os.Stat(studio); !os.IsNotExist(err) {
		t.Fatalf("department add created the partner's path (%v): it must only print the steps", err)
	}

	proj = reloadDeptProject(t, cfg)
	d := proj.DepartmentByKey("video")
	if d == nil || d.Partner == nil {
		t.Fatalf("no Video department with a partner: %+v", d)
	}
	if d.Partner.Path != studio || d.Partner.Commit != videoPin || d.Partner.Adapter != "openmontage" {
		t.Errorf("partner = %+v", *d.Partner)
	}
	om := deptConnector(d, "openmontage")
	if om == nil || om.Command != studio+"/.venv/bin/python" || !om.Sends || len(om.Optional) != 2 {
		t.Fatalf("openmontage connector in %+v", d.Connectors)
	}
	for v, ref := range om.Secrets {
		if !strings.HasPrefix(ref, "keychain:") {
			t.Errorf("secret %s = %q: references only, in the Keychain", v, ref)
		}
	}
	// The reel connectors: Kling's CLI from PATH with no secret (its login
	// is the owner's), ElevenLabs' key a Keychain reference in a header,
	// both metered and neither with a cap (the owner writes it).
	kl := deptConnector(d, "kling")
	if kl == nil || kl.Command != "kling" || len(kl.Secrets) != 0 || len(kl.Headers) != 0 || !kl.Sends ||
		kl.Spend == nil || kl.Spend.Meter != "json" || kl.Spend.Field != "creditsConsumed" || kl.Spend.Unit != "credits" || kl.Spend.Cap != 0 {
		t.Fatalf("kling connector = %+v", kl)
	}
	if !slices.Contains(kl.Spend.Refuse, "login") || !slices.Contains(kl.Spend.Free, "query_tasks") {
		t.Errorf("kling spend = %+v", *kl.Spend)
	}
	el := deptConnector(d, "elevenlabs")
	if el == nil || el.Command != "" || el.URL != "https://api.elevenlabs.io" || el.Headers["xi-api-key"] != "keychain:elevenlabs-api" || len(el.Secrets) != 0 ||
		el.Spend == nil || el.Spend.Meter != "elevenlabs" || el.Spend.Unit != "characters" || el.Spend.Cap != 0 {
		t.Fatalf("elevenlabs connector = %+v", el)
	}
	raw, _ := os.ReadFile(cfg)
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "cap:") {
			t.Errorf("the entry carries a cap: %q", l)
		}
	}
	if strings.Contains(string(raw), ".kling") {
		t.Error("the entry names the Kling CLI's own files")
	}
	for _, step := range []string{"npm i -g @klingai/cli-global", "kling login in your own", "security add-generic-password -s elevenlabs-api", "consent: noted"} {
		assertContains(t, s, step)
	}
	if _, err := proj.WorkflowFor("Video"); err != nil {
		t.Fatalf("department workflow: %v", err)
	}

	// --json carries the steps too.
	proj2, _ := deptTemplateProject(t)
	ctx, out = goalsCtx(t, proj2, true)
	if err := runDepartment(ctx, []string{"add", "video", "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	var plan struct {
		PartnerPath string `json:"partner_path"`
		Install     string `json:"install"`
	}
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.PartnerPath != "~/openmontage" || !strings.Contains(plan.Install, "make -C ") {
		t.Errorf("json plan = %+v", plan)
	}

	// A template without a partner refuses --partner-path.
	ctx, _ = goalsCtx(t, proj2, false)
	if err := runDepartment(ctx, []string{"add", "marketing", "--partner-path", studio, "--dry-run"}); err == nil || !strings.Contains(err.Error(), "has no partner") {
		t.Errorf("marketing --partner-path: %v", err)
	}
}

// TestVideoDepartmentLintClean: with the video department installed, lint
// finds nothing in its workflow file, and the departments check says only
// that the partner isn't installed yet.
func TestVideoDepartmentLintClean(t *testing.T) {
	asLead(t)
	proj, cfg := deptTemplateProject(t)
	studio := filepath.Join(t.TempDir(), "studio")
	ctx, _ := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "video", "--partner-path", studio}); err != nil {
		t.Fatal(err)
	}
	proj = reloadDeptProject(t, cfg)
	env, err := lint.Load(proj, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rep, err := lint.Run(env)
	if err != nil {
		t.Fatal(err)
	}
	wf := filepath.Join("workflows", "video.yaml")
	var missing, noCap int
	for _, f := range rep.Findings {
		switch {
		case isTemplateSample(f), isGoalWithoutWork(f):
		case f.Check == "departments" && strings.Contains(f.Message, "spend: has no cap: every billing call is refused until the owner writes one"):
			noCap++
		case strings.Contains(f.Where, wf) || strings.Contains(f.Message, "video.yaml"):
			t.Errorf("finding in the video workflow: %+v", f)
		case f.Check == "departments" && strings.Contains(f.Message, "partner OpenMontage: path "+studio+" is not a folder"):
			missing++
		case strings.Contains(strings.ToLower(f.Message), "video"):
			t.Errorf("finding about the video department: %+v", f)
		}
	}
	if missing != 1 {
		t.Errorf("want the one 'install the partner there' finding, got %d in %+v", missing, rep.Findings)
	}
	// The reel connectors ship without a cap, the owner's number.
	if noCap != 2 {
		t.Errorf("want kling's and elevenlabs's 'no cap' findings, got %d in %+v", noCap, rep.Findings)
	}
}

// TestVideoWorkflowOwnerOnlyGates: the six video reviews (proposal,
// script, scene plan, assets, review, publish) are owner_only, so YOLO
// never passes them.
func TestVideoWorkflowOwnerOnlyGates(t *testing.T) {
	tmpl, err := depttemplate.Load("video")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := workflowupgrade.ReadTemplate(tmpl.Workflow)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "video.yaml")
	mustWrite(t, path, string(raw))
	wf, err := tracker.LoadWorkflow(path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"proposal": "script", "script": "scene plan", "scene plan": "assets", "assets": "compose", "review": "publish", "publish": "done"}
	for _, tr := range wf.Transitions {
		owner := false
		for _, a := range tr.Actions {
			if a.Type == "require_human_approval" && a.OwnerOnly {
				owner = true
			}
		}
		if to, gated := want[tr.From]; gated && to == tr.To {
			if !owner {
				t.Errorf("%s → %s: not owner_only", tr.From, tr.To)
			}
			delete(want, tr.From)
		} else if owner {
			t.Errorf("%s → %s: an owner_only gate the design doesn't name", tr.From, tr.To)
		}
	}
	for from, to := range want {
		t.Errorf("no %s → %s transition", from, to)
	}
}
