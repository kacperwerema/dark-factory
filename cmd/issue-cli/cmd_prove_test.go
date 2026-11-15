package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// makeProofFixture is a git repo holding workflow.yaml (testing →
// human-testing runs criteria_proven since 2026-10-01; features as given)
// and issues/CLI/sample.md in testing, created on created.
func makeProofFixture(t *testing.T, created string, features ...string) (*tracker.Project, string, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	run("commit", "-q", "--allow-empty", "-m", "init")
	head := run("rev-parse", "HEAD")

	featureBlock := ""
	if len(features) > 0 {
		featureBlock = "features:\n  installed:\n"
		for _, f := range features {
			featureBlock += "    - " + f + "\n"
		}
	}
	workflow := featureBlock + `statuses:
  - name: "testing"
  - name: "human-testing"
transitions:
  - from: "testing"
    to: "human-testing"
    actions:
      - type: validate
        rule: "criteria_proven: since 2026-10-01"
`
	workflowPath := filepath.Join(dir, "workflow.yaml")
	if err := os.WriteFile(workflowPath, []byte(workflow), 0644); err != nil {
		t.Fatal(err)
	}
	issuesDir := filepath.Join(dir, "issues")
	if err := os.MkdirAll(filepath.Join(issuesDir, "CLI"), 0755); err != nil {
		t.Fatal(err)
	}
	issuePath := filepath.Join(issuesDir, "CLI", "sample.md")
	issue := `---
title: "sample"
status: "testing"
system: "CLI"
created: "` + created + `"
---

## Acceptance Criteria
- [ ] The count shows on the card
- [ ] The card glows (visual)
`
	if err := os.WriteFile(issuePath, []byte(issue), 0644); err != nil {
		t.Fatal(err)
	}
	return &tracker.Project{Name: "test", Slug: "test", IssueDir: issuesDir, WorkflowFile: workflowPath}, issuePath, head
}

func TestProveStoresLinksListsAndTicks(t *testing.T) {
	proj, issuePath, head := makeProofFixture(t, "2026-10-23")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runProve(ctx, []string{"cli/sample", "ac1", "--value", "690 vs 380", "--test", "TestCount", "--value", "690 vs 380", "--commit", head[:7]}); err != nil {
		t.Fatalf("prove: %v", err)
	}
	out := stdout.String()
	assertContains(t, out, "AC1 [x] proven  The count shows on the card")
	assertContains(t, out, `✓ value "690 vs 380" (prove) — reported`)
	assertContains(t, out, "✗ test TestCount (prove) — not checked: no work branch")
	assertContains(t, out, "AC2 [ ] UNPROVEN  The card glows (visual)")
	assertContains(t, out, "Prove each UNPROVEN criterion with any of (shown for AC2):")
	assertContains(t, out, "?tab=evidence\n")
	assertContains(t, out, "✓ Ticked AC1 (proven)")
	assertContains(t, out, "1/2 criteria proven")
	assertContains(t, readFile(t, issuePath), "- [x] The count shows on the card")

	store, err := tracker.LoadData(issuePath)
	if err != nil {
		t.Fatal(err)
	}
	links := store.Proofs["AC1"]
	if len(links) != 3 || links[2].Ref != head || links[0].Criterion != "The count shows on the card" || links[0].At.IsZero() {
		t.Fatalf("stored links = %+v, want value, test and the full commit sha once each", links)
	}

	ctx, stdout, _ = newTestContext(proj, true)
	if err := runProve(ctx, []string{"cli/sample", "--list"}); err != nil {
		t.Fatalf("prove --list: %v", err)
	}
	var js proveOutput
	if err := json.Unmarshal(stdout.Bytes(), &js); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout.String())
	}
	if js.Proven != 1 || js.Total != 2 || len(js.Criteria[0].Links) != 3 || !js.Criteria[0].Links[0].Check.OK {
		t.Fatalf("json = %+v", js)
	}

	ctx, _, _ = newTestContext(proj, false)
	if err := runProve(ctx, []string{"cli/sample", "AC1", "--clear"}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if store, _ := tracker.LoadData(issuePath); store.Proofs != nil {
		t.Fatalf("after --clear: %+v", store.Proofs)
	}
}

func TestProveRefusals(t *testing.T) {
	proj, _, _ := makeProofFixture(t, "2026-10-23")
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"cli/sample", "AC9", "--value", "x"}, "AC9 is not an acceptance criterion of cli/sample; criteria: AC1, AC2"},
		{[]string{"cli/sample", "AC1"}, "say where the proof is"},
		{[]string{"cli/sample", "--value", "x"}, "name the criterion"},
		{[]string{"cli/sample", "AC1", "--clear", "--value", "x"}, "don't combine"},
		{[]string{"cli/sample", "AC1", "--step", "first step"}, "is not a step id"},
		{[]string{"cli/sample", "AC1", "--commit", "deadbeef"}, "no commit deadbeef"},
		{[]string{"cli/sample", "AC1", "--shot", "shot.png"}, "AC1 is not marked visual"},
		{[]string{"cli/sample", "AC2", "--shot", "/etc/passwd"}, "must be in the issue's worktree or the project checkout"},
	}
	for _, c := range cases {
		ctx, _, _ := newTestContext(proj, false)
		err := runProve(ctx, c.args)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("prove %v: got %v, want %q", c.args, err, c.want)
		}
	}

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runProve(ctx, []string{"cli/sample", "AC2", "--shot", "shots/glow.png"}); err != nil {
		t.Fatalf("visual shot without a runner: %v", err)
	}
	assertContains(t, stdout.String(), "✗ shot shots/glow.png (prove) — missing")

	// The feature from the template alone is no runner: a lead's notes in
	// sessions/ and no playtest/sessions/ (the orchard report).
	runner, _, _ := makeProofFixture(t, "2026-10-23", "playtest-sessions@v0.36.0")
	if err := os.MkdirAll(filepath.Join(runner.Root(), "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runner.Root(), "sessions", "2026-10-26-lead-orchard.md"), []byte("# notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, _, _ = newTestContext(runner, false)
	if err := runProve(ctx, []string{"cli/sample", "AC2", "--shot", "shots/glow.png"}); err != nil {
		t.Errorf("shot with the feature but no runner: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(runner.Root(), "playtest", "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runner.Root(), "playtest", "sessions", "glow.yaml"), []byte("session: glow\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, _, _ = newTestContext(runner, false)
	if err := runProve(ctx, []string{"cli/sample", "AC2", "--clear"}); err != nil {
		t.Fatal(err)
	}
	if err := runProve(ctx, []string{"cli/sample", "AC2", "--shot", "shots/glow.png"}); err == nil || !strings.Contains(err.Error(), "this project has a playtest runner") {
		t.Errorf("shot with a playtest runner: %v", err)
	}
}

func TestCheckRefusesCriteriaUnderTheGate(t *testing.T) {
	proj, issuePath, _ := makeProofFixture(t, "2026-10-23")
	for _, args := range [][]string{
		{"cli/sample", "AC1"},
		{"cli/sample", "--section", "Acceptance Criteria", "--all"},
		{"cli/sample", "--section", "Acceptance Criteria", "--index", "2"},
		{"cli/sample", "count shows"},
	} {
		ctx, _, _ := newTestContext(proj, false)
		err := runCheck(ctx, args)
		if err == nil || !strings.Contains(err.Error(), "ticked by their proof") || !strings.Contains(err.Error(), "issue-cli prove cli/sample --list") {
			t.Errorf("check %v: %v", args, err)
		}
	}
	assertContains(t, readFile(t, issuePath), "- [ ] The count shows on the card")

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runChecklist(ctx, []string{"cli/sample"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "Acceptance criteria are ticked by their proof, not by hand: 0/2 proven. See each link and what is missing: issue-cli prove cli/sample --list")

	old, oldPath, _ := makeProofFixture(t, "2026-09-01")
	ctx, _, _ = newTestContext(old, false)
	if err := runCheck(ctx, []string{"cli/sample", "AC1"}); err != nil {
		t.Fatalf("issue created before the gate: %v", err)
	}
	assertContains(t, readFile(t, oldPath), "- [x] The count shows on the card")
}

func TestTransitionGateRequiresProofAndTicks(t *testing.T) {
	proj, issuePath, _ := makeProofFixture(t, "2026-10-23")
	ctx, _, _ := newTestContext(proj, false)
	if err := runProve(ctx, []string{"cli/sample", "AC1", "--value", "3 cards"}); err != nil {
		t.Fatal(err)
	}
	// prove ticked AC1; untick it so the transition has to.
	body := strings.Replace(readFile(t, issuePath), "- [x] The count", "- [ ] The count", 1)
	if err := os.WriteFile(issuePath, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	ctx, stdout, _ := newTestContext(proj, false)
	err := runTransition(ctx, []string{"cli/sample", "--to", "human-testing"})
	if err == nil {
		t.Fatal("transition passed with AC2 unproven")
	}
	all := stdout.String() + err.Error()
	assertContains(t, all, "1 of 2 acceptance criteria are not proven")
	assertContains(t, all, "AC2 The card glows (visual)")
	assertContains(t, readFile(t, issuePath), "- [x] The count shows on the card")

	ctx, _, _ = newTestContext(proj, false)
	if err := runProve(ctx, []string{"cli/sample", "AC2", "--value", "glows at 2px"}); err != nil {
		t.Fatal(err)
	}
	ctx, _, _ = newTestContext(proj, false)
	if err := runTransition(ctx, []string{"cli/sample", "--to", "human-testing"}); err != nil {
		t.Fatalf("transition with every criterion proven: %v", err)
	}
}

// TestProveHintWhenCriteriaMissing: with no Acceptance Criteria heading the
// hint is append --section (replace refuses a missing section); with the
// heading but no boxes it stays replace. Both hints run as printed.
func TestProveHintWhenCriteriaMissing(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"no section", "## Repro\nsteps\n", `issue-cli append cli/sample --section 'Acceptance Criteria' --body "- [ ] ..."`},
		{"section without boxes", "## Acceptance Criteria\nTBD\n", `issue-cli replace cli/sample --section 'Acceptance Criteria' --body "- [ ] ..."`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			proj, issuePath, _ := makeProofFixture(t, "2026-10-23")
			raw, err := os.ReadFile(issuePath)
			if err != nil {
				t.Fatal(err)
			}
			front := string(raw)[:strings.Index(string(raw), "## Acceptance Criteria")]
			if err := os.WriteFile(issuePath, []byte(front+c.body), 0644); err != nil {
				t.Fatal(err)
			}
			ctx, _, _ := newTestContext(proj, false)
			err = runProve(ctx, []string{"cli/sample"})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("prove: got %v, want hint %q", err, c.want)
			}

			// The printed command works.
			args := []string{"cli/sample", "--section", "Acceptance Criteria", "--body", "- [ ] The count shows"}
			run := runAppend
			if strings.HasPrefix(c.want, "issue-cli replace") {
				run = runReplace
			}
			ctx, _, _ = newTestContext(proj, false)
			if err := run(ctx, args); err != nil {
				t.Fatalf("hint command: %v", err)
			}
			ctx, _, _ = newTestContext(proj, false)
			if err := runProve(ctx, []string{"cli/sample"}); err != nil {
				t.Errorf("prove after the hint: %v", err)
			}
		})
	}
}

// A Proves: trailer that names a test links it, but the criterion stays
// unproven until a tests: report: the dry run's criteria_proven line names
// prove --run, the command that posts it.
func TestTransitionDryRunNamesProveRunWhenNoTestsReport(t *testing.T) {
	proj, _, _ := makeProofFixture(t, "2026-10-23")
	dir := filepath.Dir(proj.WorkflowFile)
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "maintenance.auto=false", "-c", "gc.auto=0"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("checkout", "-q", "-b", "work/cli/sample")
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module sample\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "count_test.go"), []byte("package sample\n\nimport \"testing\"\n\nfunc TestCountShows(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "go.mod", "count_test.go")
	git("commit", "-q", "-m", "feat: count\n\nRefs: cli/sample\nProves: AC1 TestCountShows")
	git("checkout", "-q", "main")

	ctx, stdout, _ := newTestContext(proj, false)
	err := runTransition(ctx, []string{"cli/sample", "--to", "human-testing", "--dry-run"})
	if err == nil {
		t.Fatal("dry run ready with no tests: report")
	}
	out := stdout.String()
	var line string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "[validator]") && strings.Contains(l, "proof link") {
			line = l
		}
	}
	if !strings.Contains(line, "no tests: report yet: issue-cli --project test prove cli/sample --run") {
		t.Errorf("criteria_proven line doesn't name prove --run:\n%q\n\nfull output:\n%s", line, out)
	}
}

// judgementRun writes a playtest run of session looks for cli/sample whose
// TP4 is a judgement step proving AC1.
func judgementRun(t *testing.T, proj *tracker.Project) {
	t.Helper()
	runDir := filepath.Join(projectRoot(proj), ".playtest", "runs", "looks", "1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	results := "session: looks\nissue: cli/sample\nmode: auto\nstarted: \"2026-10-31T10:00:00Z\"\nsteps:\n  - id: TP4\n    status: pass\n    judgement_step: true\n    proves: [AC1]\n"
	if err := os.WriteFile(filepath.Join(runDir, "results.yaml"), []byte(results), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A Proves: AC1 trailer names a test and a judgement step lists AC1 too:
// prove --list and the transition into criteria_proven warn, naming the step.
func TestProveListFlagsJudgementOverTest(t *testing.T) {
	proj, _, _ := makeProofFixture(t, "2026-10-23")
	dir := filepath.Dir(proj.WorkflowFile)
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "maintenance.auto=false", "-c", "gc.auto=0"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("checkout", "-q", "-b", "work/cli/sample")
	if err := os.WriteFile(filepath.Join(dir, "count_test.go"), []byte("package sample\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "count_test.go")
	git("commit", "-q", "-m", "feat: count\n\nRefs: cli/sample\nProves: AC1 TestX")
	git("checkout", "-q", "main")
	judgementRun(t, proj)

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runProve(ctx, []string{"cli/sample", "--list"}); err != nil {
		t.Fatalf("prove --list: %v", err)
	}
	want := "Warning: AC1 is the owner's only because judgement step TP4 (session looks) lists it, while test TestX"
	assertContains(t, stdout.String(), want)
	assertContains(t, stdout.String(), "drop AC1 from that step's proves:")

	ctx, stdout, _ = newTestContext(proj, true)
	if err := runProve(ctx, []string{"cli/sample", "--list"}); err != nil {
		t.Fatalf("prove --list --json: %v", err)
	}
	var out struct {
		Overlaps []struct {
			ID    string   `json:"id"`
			Steps []string `json:"steps"`
		} `json:"overlaps"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Overlaps) != 1 || out.Overlaps[0].ID != "AC1" || len(out.Overlaps[0].Steps) != 1 {
		t.Fatalf("overlaps = %+v, want AC1 by TP4", out.Overlaps)
	}

	ctx, stdout, _ = newTestContext(proj, false)
	_ = runTransition(ctx, []string{"cli/sample", "--to", "human-testing"})
	assertContains(t, stdout.String(), want)
}

// Only the judgement step proves AC1: it is the owner's on purpose, no warning.
func TestProveListJudgementOnlyQuiet(t *testing.T) {
	proj, _, _ := makeProofFixture(t, "2026-10-23")
	judgementRun(t, proj)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runProve(ctx, []string{"cli/sample", "--list"}); err != nil {
		t.Fatalf("prove --list: %v", err)
	}
	if strings.Contains(stdout.String(), "Warning:") {
		t.Fatalf("a warning with only the judgement step proving AC1:\n%s", stdout.String())
	}
	assertContains(t, stdout.String(), "owner check (playtest judgement step TP4)")
}
