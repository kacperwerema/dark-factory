package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

type leadCall struct {
	dir       string
	env, argv []string
}

func stubLeadRunner(t *testing.T, exit int, out string) *[]leadCall {
	t.Helper()
	var calls []leadCall
	old := leadCommandRunner
	leadCommandRunner = func(dir string, env, argv []string) ([]byte, int, error) {
		calls = append(calls, leadCall{dir, env, argv})
		return []byte(out), exit, nil
	}
	t.Cleanup(func() { leadCommandRunner = old })
	return &calls
}

func leadTestWorker(t *testing.T, slug string) {
	t.Helper()
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", slug)
}

func leadTestLead(t *testing.T) {
	t.Helper()
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "project")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
}

// The worker links a command it can't run; the lead's run proves the
// criterion only when it passes.
func TestProveLeadLinkThenTheLeadsRunProvesIt(t *testing.T) {
	proj, issuePath, _ := makeProofFixture(t, "2026-10-23")
	old := autostart.ReadMemory
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{} }
	t.Cleanup(func() { autostart.ReadMemory = old })
	const cmd = "SANDBOX=1 issue-cli check-script cli/sample --yes"

	leadTestWorker(t, "cli/sample")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runProve(ctx, []string{"cli/sample", "AC2", "--lead", "issue-cli dispatch cli/sample"}); err == nil || !strings.Contains(err.Error(), "subcommand dispatch is not allowed") {
		t.Fatalf("a dispatch lead link: %v", err)
	}
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runProve(ctx, []string{"cli/sample", "AC2", "--lead", cmd}); err != nil {
		t.Fatalf("prove --lead: %v", err)
	}
	out := stdout.String()
	assertContains(t, out, "AC2 waits on the lead: "+cmd)
	assertContains(t, out, "no question: comment needed")
	assertContains(t, out, "✗ lead \""+cmd+"\" (prove) — waits on the lead: "+cmd)

	ctx, _, _ = newTestContext(proj, false)
	err := runProve(ctx, []string{"cli/sample", "--lead-run"})
	if err == nil || !strings.Contains(err.Error(), "workers can't record a lead run") {
		t.Fatalf("worker --lead-run: %v", err)
	}

	leadTestLead(t)
	calls := stubLeadRunner(t, 1, "step S1 fail\n")
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runProve(ctx, []string{"cli/sample", "--lead-run"}); exitOf(err) != 1 {
		t.Fatalf("failing lead run: %v\n%s", err, stdout.String())
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %+v", *calls)
	}
	c := (*calls)[0]
	if !reflect.DeepEqual(c.env, []string{"SANDBOX=1"}) || !reflect.DeepEqual(c.argv, []string{"--project", "test", "check-script", "cli/sample", "--yes"}) || c.dir != projectRoot(proj) {
		t.Errorf("lead run = %+v, want SANDBOX=1, --project test check-script cli/sample --yes in the project checkout", c)
	}
	out = stdout.String()
	assertContains(t, out, "✗ failed (exit 1)")
	assertContains(t, out, "step S1 fail")
	assertContains(t, out, "waits on the lead: "+cmd+" (last run failed")
	assertNotContains(t, readFile(t, issuePath), "- [x] The card glows")

	calls = stubLeadRunner(t, 0, "all steps pass\n")
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runProve(ctx, []string{"cli/sample", "--lead-run"}); err != nil {
		t.Fatalf("passing lead run: %v\n%s", err, stdout.String())
	}
	out = stdout.String()
	assertContains(t, out, "✓ passed")
	assertContains(t, out, "the lead ran it by lead-test")
	assertContains(t, out, "✓ Ticked AC2 (proven)")
	assertContains(t, readFile(t, issuePath), "- [x] The card glows (visual)")
	store, _ := tracker.LoadData(issuePath)
	if len(store.LeadRuns) != 2 || store.LeadRuns[0].OK || !store.LeadRuns[1].OK || store.LeadRuns[1].By != "lead-test" {
		t.Errorf("lead runs = %+v", store.LeadRuns)
	}

	// Nothing waits now: a second --lead-run runs nothing.
	calls = stubLeadRunner(t, 0, "")
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runProve(ctx, []string{"cli/sample", "--lead-run"}); err != nil || len(*calls) != 0 {
		t.Fatalf("second --lead-run: %v, calls %+v", err, *calls)
	}
	assertContains(t, stdout.String(), "Nothing waits on the lead in cli/sample")
}

func TestProveLeadRunRefusedForWorker(t *testing.T) {
	proj, _, _ := makeProofFixture(t, "2026-10-23")
	leadTestWorker(t, "cli/sample")
	calls := stubLeadRunner(t, 0, "")
	ctx, _, _ := newTestContext(proj, false)
	if err := runProve(ctx, []string{"cli/sample", "AC1", "--lead", "issue-cli check-script cli/sample --yes"}); err != nil {
		t.Fatal(err)
	}
	ctx, _, _ = newTestContext(proj, false)
	if err := runProve(ctx, []string{"cli/sample", "--lead-run"}); err == nil || len(*calls) != 0 {
		t.Fatalf("worker --lead-run: %v, calls %+v", err, *calls)
	}
}

// The lead's tick lists what waits on it, from the sidecars alone: the
// sandbox project has no git.
func TestQueueNextListsLeadWaits(t *testing.T) {
	proj := makeAutostartProject(t)
	root := filepath.Dir(proj.IssueDir)
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		t.Fatal("the sandbox must have no git")
	}
	link := func(slug, ac string) {
		t.Helper()
		issue := loadLeadIssue(t, proj, slug)
		store := tracker.DataStore{Proofs: map[string][]tracker.ProofLink{ac: {{Kind: tracker.ProofLead, Ref: "issue-cli check-script " + slug + " --yes", At: time.Now().UTC()}}}}
		if err := tracker.SaveData(issue.FilePath, store); err != nil {
			t.Fatal(err)
		}
	}
	link("base", "AC1")
	if err := os.WriteFile(filepath.Join(proj.IssueDir, "shipped.md"), []byte("---\ntitle: \"Shipped\"\nstatus: \"done\"\n---\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link("shipped", "AC1")

	out := mustQ(t, proj, "next")
	assertContains(t, out, "Waits on you (1):")
	assertContains(t, out, "  base AC1: issue-cli check-script base --yes (never ran)  → issue-cli --project proj prove base --lead-run")
	assertNotContains(t, out, "shipped")
	if strings.Index(out, "Waits on you") > strings.Index(out, "hold: ") {
		t.Errorf("the block comes before the verdict:\n%s", out)
	}

	ctx, stdout, _ := newTestContext(proj, true)
	if err := runQueue(ctx, []string{"next"}); err != nil {
		t.Fatal(err)
	}
	var js struct {
		WaitsOnLead []struct {
			Slug, ID, Command, State string
		} `json:"waits_on_lead"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &js); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout.String())
	}
	if len(js.WaitsOnLead) != 1 || js.WaitsOnLead[0].Slug != "base" || js.WaitsOnLead[0].State != "never ran" {
		t.Errorf("waits_on_lead = %+v", js.WaitsOnLead)
	}

	// A passing run clears it.
	issue := loadLeadIssue(t, proj, "base")
	store, _ := tracker.LoadData(issue.FilePath)
	store.LeadRuns = []tracker.LeadRun{{Command: "issue-cli check-script base --yes", At: time.Now().UTC().Add(time.Minute), OK: true}}
	if err := tracker.SaveData(issue.FilePath, store); err != nil {
		t.Fatal(err)
	}
	assertNotContains(t, mustQ(t, proj, "next"), "Waits on you")
}
