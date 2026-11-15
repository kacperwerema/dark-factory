package bench

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func fixtureProject(f *Fixture) *tracker.Project {
	return &tracker.Project{Name: FixtureName, Slug: FixtureSlug, IssueDir: f.Issues, WorkflowFile: f.Workflow, WorkDir: f.Repo, Terminal: "none"}
}

func TestFixtureInitWritesTheSameProjectOnEveryRun(t *testing.T) {
	a, err := FixtureInit(filepath.Join(t.TempDir(), "a"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := FixtureInit(filepath.Join(t.TempDir(), "b"))
	if err != nil {
		t.Fatal(err)
	}
	for _, repo := range [][2]string{{a.Repo, b.Repo}, {a.Tracker, b.Tracker}} {
		if x, y := mustGit(t, repo[0], "rev-parse", "HEAD"), mustGit(t, repo[1], "rev-parse", "HEAD"); x != y {
			t.Errorf("two inits differ: %s vs %s", x, y)
		}
	}
	// Pinned: a golden task added from the fixture names the repo's commits
	// on every machine. A change to the fixture's code files changes them:
	// update the pin and say so in the changelog. (The tracker's commits
	// hold the bundled workflow template, so they move with it; no task
	// names them.)
	if got := mustGit(t, a.Repo, "rev-parse", "HEAD"); got != "36b6fec641077b20d736430f64fd2e2dd4be7554" {
		t.Errorf("fixture repo main = %s", got)
	}
	if got := mustGit(t, a.Repo, "log", "--format=%an <%ae>", "-1"); got != "Bench Fixture <fixture@example.com>" {
		t.Errorf("author = %q", got)
	}
	if len(a.Tasks) != 3 || !strings.HasPrefix(a.Tasks[0], "tally/") {
		t.Errorf("tasks = %q", a.Tasks)
	}
	if !strings.Contains(a.Entry, `slug: "bench-fixture"`) || !strings.Contains(a.Entry, a.Repo) {
		t.Errorf("entry = %q", a.Entry)
	}
	if _, err := FixtureInit(a.Dir); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Errorf("init over a written fixture: %v", err)
	}
	// The project's own tests pass at main, without the network.
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = a.Repo
	cmd.Env = append(os.Environ(), "GOPROXY=off", "GOFLAGS=-mod=mod", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test in the fixture: %v\n%s", err, out)
	}
}

func TestTheFixturesTasksAreProposedWithAReferencePerStage(t *testing.T) {
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", filepath.Join(t.TempDir(), "bench"))
	f, err := FixtureInit(filepath.Join(t.TempDir(), "fx"))
	if err != nil {
		t.Fatal(err)
	}
	p := fixtureProject(f)
	issues, err := tracker.LoadIssues(f.Issues)
	if err != nil {
		t.Fatal(err)
	}
	cands := Candidates(p, issues, nil)
	if len(cands) != 3 {
		t.Fatalf("candidates = %+v", cands)
	}
	for _, c := range cands {
		if c.Why != "" || len(c.Risks) != 0 {
			t.Errorf("%s not proposed cleanly: why %q risks %q", c.Slug, c.Why, c.Risks)
		}
		if len(c.Stages) != 3 {
			t.Errorf("%s stages = %q", c.Slug, c.Stages)
		}
	}
	// Each is solvable from its base: the hidden tests fail there and pass
	// on the replayed branch.
	CheckCandidates(context.Background(), cands, nil, ReplayEnv{}, func(int, int, Candidate) {})
	for _, c := range cands {
		if c.Replay != ReplaySolvable {
			t.Errorf("%s replay: %s — %s", c.Slug, c.Replay, c.ReplayWhy)
		}
	}

	byDir := map[string]Task{}
	for i, slug := range f.Tasks {
		var issue *tracker.Issue
		for _, is := range issues {
			if is.Slug == slug {
				issue = is
			}
		}
		task, warnings, err := NewTask(context.Background(), p, issue, AddOptions{Verify: true, DocsRubric: []string{"Says the fixture's own words"}}, time.Now())
		if err != nil {
			t.Fatalf("%s: %v (warnings %q)", slug, err, warnings)
		}
		if len(warnings) > 0 {
			t.Errorf("%s warnings: %q", slug, warnings)
		}
		byDir[fixtureTasks[i].dir] = task
	}

	top := byDir["top-words"]
	if len(top.HiddenTests) != 3 || top.Start != "in progress" || top.Stages == nil || top.Stages.Design == nil {
		t.Fatalf("top-words = %+v", top)
	}
	design, err := os.ReadFile(StagePath(top, top.Stages.Design))
	if err != nil || !strings.Contains(string(design), "## Design") || !strings.Contains(string(design), "count descending, then the word ascending") {
		t.Errorf("design reference = %q, %v", design, err)
	}
	snap, _ := os.ReadFile(SnapshotPath(top))
	if !strings.Contains(string(snap), `status: "in progress"`) || !strings.Contains(string(snap), "## Design") {
		t.Errorf("snapshot = %q", snap)
	}
	docs, err := os.ReadFile(StagePath(top, top.Stages.Docs))
	if err != nil || !strings.Contains(string(docs), "+- `TopWords(text, n)`") {
		t.Errorf("docs reference = %q, %v", docs, err)
	}
	if n := len(top.Stages.Docs.Rubric); n != len(DefaultDocsRubric)+1 {
		t.Errorf("docs rubric = %q", top.Stages.Docs.Rubric)
	}
	if got := StagesLine(top); got != "design ✓ · code 3 tests · docs rubric 4, reference 1 file" {
		t.Errorf("stages line = %q", got)
	}

	tweak := byDir["last-line"]
	if tweak.Stages.Design != nil || tweak.Stages.Missing[StageDesign] != "never went through a design status" {
		t.Errorf("tweak stages = %+v", tweak.Stages)
	}
	if got := StagesLine(tweak); got != "design missing: never went through a design status · code 1 test · docs rubric 4, reference 1 file" {
		t.Errorf("tweak stages line = %q", got)
	}
	if fold := byDir["fold-case"]; fold.Stages.Design == nil || len(fold.DocsFiles) != 1 || fold.DocsFiles[0] != "README.md" {
		t.Errorf("fold-case = %+v", fold)
	}
}
