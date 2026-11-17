package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/ci"
	"github.com/kacperwerema/dark-factory/internal/deploy"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// threeDeploySteps touch one file each in the project root, so a test sees
// which ran; middle's run is the given command.
func threeDeploySteps(middle string) string {
	return `deploy:
  - name: backup
    run: touch backup.ran
  - name: app
    run: ` + middle + `
  - name: landing
    run: touch landing.ran
    probe: test -f landing.ran
`
}

// makeDeployFixture is makeCIFixture with block (deploy: and maybe ci:) in
// workflow.yaml, and no live workers.
func makeDeployFixture(t *testing.T, block string) (*tracker.Project, string) {
	t.Helper()
	proj, root := makeCIFixture(t, block)
	old := deployLiveWorkers
	deployLiveWorkers = func(*tracker.Project, string) []string { return nil }
	t.Cleanup(func() { deployLiveWorkers = old })
	return proj, root
}

func runDeployIn(proj *tracker.Project, args ...string) (string, error) {
	ctx, out, errOut := newTestContext(proj, false)
	err := runDeploy(ctx, args)
	return out.String() + errOut.String(), err
}

func ran(root, name string) bool {
	_, err := os.Stat(filepath.Join(root, name+".ran"))
	return err == nil
}

func TestDeployDryRunPrintsPlan(t *testing.T) {
	proj, root := makeDeployFixture(t, threeDeploySteps("touch app.ran"))
	out, err := runDeployIn(proj, "--dry-run")
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	b, a, l := strings.Index(out, "1. backup"), strings.Index(out, "2. app"), strings.Index(out, "3. landing")
	if b < 0 || a < b || l < a {
		t.Fatalf("plan not in order:\n%s", out)
	}
	if !strings.Contains(out, "probe: test -f landing.ran") || !strings.Contains(out, deployNoOwnerHint) {
		t.Errorf("plan lacks the probe or the owner-ok note:\n%s", out)
	}
	for _, name := range []string{"backup", "app", "landing"} {
		if ran(root, name) {
			t.Errorf("dry run ran %s", name)
		}
	}
	if _, err := os.Stat(deploy.Dir(proj.IssueDir)); !os.IsNotExist(err) {
		t.Errorf("dry run wrote %s (err %v)", deploy.Dir(proj.IssueDir), err)
	}
}

func TestDeployStopsAtFirstRed(t *testing.T) {
	proj, root := makeDeployFixture(t, threeDeploySteps("echo migrating; exit 3"))
	out, err := runDeployIn(proj)
	if err == nil || !strings.Contains(err.Error(), "deploy stopped at app (red): run exit 3") || !strings.Contains(err.Error(), "deploy --from app") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if !ran(root, "backup") || ran(root, "landing") {
		t.Fatalf("backup ran %v, landing ran %v; want only backup", ran(root, "backup"), ran(root, "landing"))
	}
	if !strings.Contains(out, "migrating") {
		t.Errorf("the red step's log tail is not shown:\n%s", out)
	}
	rec := deploy.Last(proj.IssueDir)
	if rec == nil {
		t.Fatal("no record written")
	}
	if rec.State != deploy.Red || rec.Failed != "app" || len(rec.Steps) != 3 {
		t.Fatalf("record = %+v", rec)
	}
	if rec.Steps[0].State != deploy.Green || rec.Steps[2].State != deploy.NotRun || *rec.Steps[1].Exit != 3 {
		t.Errorf("steps = %+v", rec.Steps)
	}
	raw, _ := os.ReadFile(filepath.Join(deploy.Dir(proj.IssueDir), rec.ID+".json"))
	if !strings.Contains(string(raw), `"failed": "app"`) {
		t.Errorf("the result file does not name the red step:\n%s", raw)
	}

	// Fixed: --from app goes on and runs the rest.
	if err := os.WriteFile(proj.WorkflowFile, []byte("statuses:\n  - name: \"idea\"\n"+threeDeploySteps("touch app.ran")), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(root, "backup.ran"))
	if out, err := runDeployIn(proj, "--from", "app"); err != nil {
		t.Fatalf("--from app: %v\n%s", err, out)
	}
	if ran(root, "backup") || !ran(root, "app") || !ran(root, "landing") {
		t.Errorf("--from app ran backup %v, app %v, landing %v", ran(root, "backup"), ran(root, "app"), ran(root, "landing"))
	}
	if last := deploy.Last(proj.IssueDir); last.State != deploy.Green || last.From != "app" || len(last.Steps) != 2 {
		t.Errorf("record after --from = %+v", last)
	}
}

func TestDeployNeedsMainGreen(t *testing.T) {
	block := "ci:\n  run:\n    - \"true\"\ndeploy:\n  - name: app\n    run: touch app.ran\n    needs: main-green\n"
	proj, root := makeDeployFixture(t, block)
	head, err := ci.Head(root, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := ci.WriteState(root, &ci.State{Branch: "main", LastSha: head, State: ci.Red, RedSince: head, Failed: "true"}); err != nil {
		t.Fatal(err)
	}
	out, err := runDeployIn(proj)
	if err == nil || !strings.Contains(err.Error(), "needs main-green") || !strings.Contains(err.Error(), "(refused)") {
		t.Fatalf("red CI: err = %v\n%s", err, out)
	}
	if ran(root, "app") {
		t.Fatal("app ran while CI was red")
	}
	if rec := deploy.Last(proj.IssueDir); rec == nil || rec.State != deploy.Refused || rec.Failed != "app" {
		t.Fatalf("record = %+v", rec)
	}

	// Green, but on an older commit than main's head: still refused.
	if err := ci.WriteState(root, &ci.State{Branch: "main", LastSha: "0000000", State: ci.Green, GreenSha: "0000000"}); err != nil {
		t.Fatal(err)
	}
	if _, err := runDeployIn(proj); err == nil || !strings.Contains(err.Error(), "has not passed main's head") {
		t.Fatalf("green on an old commit: err = %v", err)
	}

	// Green on main's head: runs.
	if err := ci.WriteState(root, &ci.State{Branch: "main", LastSha: head, State: ci.Green, GreenSha: head}); err != nil {
		t.Fatal(err)
	}
	if out, err := runDeployIn(proj); err != nil || !ran(root, "app") {
		t.Fatalf("green: err = %v, app ran %v\n%s", err, ran(root, "app"), out)
	}
}

func TestDeployNoLiveWorkers(t *testing.T) {
	proj, root := makeDeployFixture(t, "deploy:\n  - name: app\n    run: touch app.ran\n    needs: [no-live-workers]\n")
	deployLiveWorkers = func(*tracker.Project, string) []string { return []string{"agent-ui-b"} }
	if _, err := runDeployIn(proj); err == nil || !strings.Contains(err.Error(), "1 worker is live: agent-ui-b") {
		t.Fatalf("err = %v", err)
	}
	if ran(root, "app") {
		t.Fatal("app ran beside a live worker")
	}
}

func TestDeployOwnerOkNeedsQuote(t *testing.T) {
	proj, root := makeDeployFixture(t, "deploy:\n  - name: app\n    run: touch app.ran\n    needs: owner-ok\n")
	out, err := runDeployIn(proj)
	if err == nil || !strings.Contains(err.Error(), "needs owner-ok") || !strings.Contains(err.Error(), `--quote "<owner's words>"`) {
		t.Fatalf("no quote: err = %v\n%s", err, out)
	}
	if ran(root, "app") {
		t.Fatal("owner-ok step ran without the owner's words")
	}
	if out, err := runDeployIn(proj, "--quote", "ship it"); err != nil || !ran(root, "app") {
		t.Fatalf("with quote: err = %v\n%s", err, out)
	}
	if rec := deploy.Last(proj.IssueDir); rec.Quote != "ship it" || rec.State != deploy.Green {
		t.Errorf("record = %+v", rec)
	}
}

func TestDeployRefusesAWorker(t *testing.T) {
	proj, root := makeDeployFixture(t, threeDeploySteps("touch app.ran"))
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "ui/b")
	if _, err := runDeployIn(proj); err == nil || !strings.Contains(err.Error(), "workers don't deploy") {
		t.Fatalf("worker: err = %v", err)
	}
	if ran(root, "backup") {
		t.Fatal("a worker's deploy ran a step")
	}
	if out, err := runDeployIn(proj, "--dry-run"); err != nil || !strings.Contains(out, "1. backup") {
		t.Fatalf("worker dry run: err = %v\n%s", err, out)
	}
}

func TestDeployConfigErrors(t *testing.T) {
	proj, _ := makeDeployFixture(t, "")
	out, err := runDeployIn(proj)
	if err != nil || !strings.Contains(out, "No deploy: steps") {
		t.Fatalf("no deploy: err = %v\n%s", err, out)
	}
	for block, want := range map[string]string{
		"deploy:\n  - name: a\n    run: \"true\"\n    needs: prod-ok\n":             `needs "prod-ok"`,
		"deploy:\n  - name: a\n    run: \"true\"\n  - name: a\n    run: \"true\"\n": `two steps are called "a"`,
		"deploy:\n  - name: a\n": `has no run`,
		"deploy:\n  - name: a\n    run: \"true\"\n    timeout: soon\n": `timeout "soon"`,
	} {
		if err := os.WriteFile(proj.WorkflowFile, []byte("statuses:\n  - name: \"idea\"\n"+block), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := runDeployIn(proj, "--dry-run"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", block, err, want)
		}
	}
	if err := os.WriteFile(proj.WorkflowFile, []byte("statuses:\n  - name: \"idea\"\n"+threeDeploySteps("true")), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runDeployIn(proj, "--step", "nope"); err == nil || !strings.Contains(err.Error(), "steps: backup, app, landing") {
		t.Errorf("unknown step: err = %v", err)
	}
}

func TestDeployTimeout(t *testing.T) {
	proj, _ := makeDeployFixture(t, "deploy:\n  - name: slow\n    run: sleep 30\n    timeout: 200ms\n")
	if _, err := runDeployIn(proj); err == nil || !strings.Contains(err.Error(), "run timed out after 200ms") {
		t.Fatalf("err = %v", err)
	}
}

func TestDeployLast(t *testing.T) {
	proj, _ := makeDeployFixture(t, threeDeploySteps("exit 1"))
	if out, _ := runDeployIn(proj, "--last"); !strings.Contains(out, "No deploy recorded yet") {
		t.Fatalf("no record: %s", out)
	}
	runDeployIn(proj)
	out, err := runDeployIn(proj, "--last")
	if err != nil || !strings.Contains(out, "red at app") || !strings.Contains(out, "✗ 2. app: red (run exit 1)") || !strings.Contains(out, "· 3. landing: not run") {
		t.Fatalf("--last: err = %v\n%s", err, out)
	}
}

// TestDeployDocumented: docs/workflow.md documents deploy: and the CLI page
// lists the command.
func TestDeployDocumented(t *testing.T) {
	wfDoc, err := os.ReadFile(filepath.Join("..", "..", "docs", "workflow.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Deploy", "deploy:", tracker.NeedMainGreen, tracker.NeedNoLiveWorkers, tracker.NeedOwnerOK, "probe:", ".deploys/"} {
		if !strings.Contains(string(wfDoc), want) {
			t.Errorf("docs/workflow.md lacks %q", want)
		}
	}
	cliDoc, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI", "overview.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"`issue-cli deploy", "--dry-run", "--from", "--quote"} {
		if !strings.Contains(string(cliDoc), want) {
			t.Errorf("docs/CLI/overview.md lacks %q", want)
		}
	}
}
