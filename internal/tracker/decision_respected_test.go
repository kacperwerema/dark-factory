package tracker

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// respectedRepo is a git repo in a TempDir with main and helpers to commit
// on it and on work branches. Background maintenance is off so nothing is
// still writing .git/objects at cleanup.
type respectedRepo struct {
	t   *testing.T
	dir string
}

func newRespectedRepo(t *testing.T) *respectedRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	r := &respectedRepo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	r.git("config", "maintenance.auto", "false")
	r.git("config", "gc.auto", "0")
	r.write("README.md", "hi\n")
	r.write("internal/store/db.go", "package store\n")
	r.write("web/page.html", "<p>\n")
	r.commit("init")
	return r
}

func (r *respectedRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false",
		"-c", "maintenance.auto=false", "-c", "gc.auto=0", "-c", "core.hooksPath=/dev/null"}, args...)...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *respectedRepo) write(rel, content string) {
	r.t.Helper()
	p := filepath.Join(r.dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *respectedRepo) commit(msg string) string {
	r.git("add", "-A")
	r.git("commit", "-q", "-m", msg)
	return r.git("rev-parse", "HEAD")
}

// branch cuts work/<slug> from main and returns main's sha (the base).
func (r *respectedRepo) branch(slug string) string {
	base := r.git("rev-parse", "main")
	r.git("checkout", "-q", "-b", "work/"+slug, "main")
	return base
}

// respectedBoard is a board with rules guarding internal/store/** (hard,
// project), web/** (soft), a hard rule scoped system:Workflow and one with
// at: in design, a replaced hard rule and its replacer (which inherits
// paths:), and an expired hard rule. The issue under test is orchard/child,
// body extra; its file and stats sidecar sit in an issues dir.
type respectedBoard struct {
	wf    *WorkflowConfig
	issue *Issue
	dir   string // issues dir
}

func newRespectedBoard(t *testing.T, repo string, extra string, base string) *respectedBoard {
	t.Helper()
	dir := t.TempDir()
	rules := &Issue{Slug: "orchard/rules", Status: "done", BodyRaw: "## Decisions\n\n" +
		`- **D-1** · 2026-10-01 · chat: The store is append-only, "append only" {scope: project; hard; paths: internal/store/**}` + "\n" +
		`- **D-2** · 2026-10-02 · chat: Pages stay plain {scope: project; paths: web/**}` + "\n" +
		`- **D-3** · 2026-10-03 · chat: Workflow files are tiny, "tiny" {scope: system:Workflow; hard; paths: flows/**}` + "\n" +
		`- **D-4** · 2026-10-04 · chat: Designs name the docs, "docs" {scope: project; hard; at: in design; paths: docs/**}` + "\n" +
		`- **D-5** · 2026-10-05 · chat: Old cache rule, "old" {scope: project; hard; paths: cache/**}` + "\n" +
		`- **D-6** · 2026-10-06 · chat: New cache rule, "new" (replaces D-5) {scope: project; hard; paths: cache/**}` + "\n" +
		`- **D-7** · 2026-10-07 · chat: Freeze vendor, "freeze" {scope: project; hard; paths: vendor/**; expires: 2026-10-20}` + "\n"}
	issue := &Issue{Slug: "orchard/child", Status: "in progress", System: "UI", Created: "2026-11-01",
		FilePath: filepath.Join(dir, "orchard", "child.md"),
		BodyRaw:  "## Idea\n\nSomething.\n\n## Design\n\n" + extra + "\n"}
	other := &Issue{Slug: "orchard/other", Status: "in progress", BodyRaw: "## Idea\n\nx\n"}
	if err := os.MkdirAll(filepath.Dir(issue.FilePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if base != "" {
		st := StatsStore{Dispatches: []Dispatch{{At: time.Now(), Agent: "claude", BaseBranch: "main", BaseSHA: base}}}
		data, _ := json.Marshal(st)
		if err := os.WriteFile(StatsSidecarPath(issue.FilePath), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	x := BuildDecisionIndex([]*Issue{rules, issue, other}, nil)
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{{Name: "in progress"}, {Name: "testing"}},
		Transitions: []WorkflowTransition{{From: "in progress", To: "testing", Actions: []WorkflowAction{
			{Type: "validate", Rule: DecisionsRespectedRule + ": since 2026-11-01"},
		}}},
		RepoRoot:   repo,
		IssuesRoot: dir,
		DecisionsInForce: func() (*DecisionIndex, InForceOptions) {
			return x, InForceOptions{Today: time.Date(2026, 11, 1, 9, 0, 0, 0, time.Local)}
		},
	}
	return &respectedBoard{wf: wf, issue: issue, dir: dir}
}

func (b *respectedBoard) check() error {
	return b.wf.ValidateTransition(b.issue, "in progress", "testing", nil)
}

func (b *respectedBoard) challenge(t *testing.T, ref, by string, resolved bool) {
	t.Helper()
	evs := []DecisionEvent{{Write: "w1", Op: DecisionOpDone, Event: DecisionChallenged, Ref: ref, Issue: by, Why: "too strict", At: "2026-11-01T09:00:00Z"}}
	if resolved {
		evs = append(evs, DecisionEvent{Write: "w2", Op: DecisionOpDone, Event: DecisionResolved, Ref: ref, Issue: by, New: ChallengeKept, At: "2026-11-01T10:00:00Z"})
	}
	if err := AppendDecisionEvents(b.dir, evs); err != nil {
		t.Fatal(err)
	}
}

func wantRefusal(t *testing.T, err error, want ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want a refusal naming %q, got pass", want)
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("refusal lacks %q:\n%v", w, err)
		}
	}
}

// TestDecisionsRespectedRefusesUncited is AC1: a branch changing a file
// under a hard decision's paths: without citing it fails, naming the ref
// and the file.
func TestDecisionsRespectedRefusesUncited(t *testing.T) {
	r := newRespectedRepo(t)
	base := r.branch("orchard/child")
	r.write("internal/store/db.go", "package store\n// rewrite\n")
	r.commit("feat: rewrite the store")
	b := newRespectedBoard(t, r.dir, "We change the store.", base)
	err := b.check()
	wantRefusal(t, err, "decisions_respected", "orchard/rules#D-1", "internal/store/db.go", "The store is append-only",
		"not cited", "issue-cli append orchard/child --section Design", "decisions --touched orchard/child")
}

// TestDecisionsRespectedCitationNAChallengesSoft is AC2.
func TestDecisionsRespectedCitationNAChallengesSoft(t *testing.T) {
	r := newRespectedRepo(t)
	base := r.branch("orchard/child")
	r.write("internal/store/db.go", "package store\n// rewrite\n")
	r.write("web/page.html", "<p>new\n")
	r.commit("feat: change store and page")

	t.Run("cited passes; soft with paths never holds", func(t *testing.T) {
		b := newRespectedBoard(t, r.dir, "Follows orchard/rules#D-1: appends only.", base)
		if err := b.check(); err != nil {
			t.Fatalf("want pass, got:\n%v", err)
		}
	})
	t.Run("cited outside Design passes", func(t *testing.T) {
		b := newRespectedBoard(t, r.dir, "We change it.\n\n## Implementation\n\nKeeps orchard/rules#D-1.", base)
		if err := b.check(); err != nil {
			t.Fatalf("want pass, got:\n%v", err)
		}
	})
	t.Run("n/a alone fails", func(t *testing.T) {
		b := newRespectedBoard(t, r.dir, "n/a orchard/rules#D-1: we only read the store", base)
		wantRefusal(t, b.check(), "orchard/rules#D-1", "an n/a line doesn't count")
	})
	t.Run("own open challenge fails even when cited", func(t *testing.T) {
		b := newRespectedBoard(t, r.dir, "Follows orchard/rules#D-1.", base)
		b.challenge(t, "orchard/rules#D-1", "orchard/child", false)
		err := b.check()
		wantRefusal(t, err, "orchard/rules#D-1", "this issue's challenge on it is open")
		if strings.Contains(err.Error(), "not cited") {
			t.Errorf("a cited decision reads as not cited:\n%v", err)
		}
	})
	t.Run("own challenge resolved passes", func(t *testing.T) {
		b := newRespectedBoard(t, r.dir, "Follows orchard/rules#D-1.", base)
		b.challenge(t, "orchard/rules#D-1", "orchard/child", true)
		if err := b.check(); err != nil {
			t.Fatalf("want pass, got:\n%v", err)
		}
	})
	t.Run("another issue's open challenge does not hold", func(t *testing.T) {
		b := newRespectedBoard(t, r.dir, "Follows orchard/rules#D-1.", base)
		b.challenge(t, "orchard/rules#D-1", "orchard/other", false)
		if err := b.check(); err != nil {
			t.Fatalf("another issue's challenge held this one:\n%v", err)
		}
	})
}

// TestDecisionsRespectedYOLOHold is AC3 at the rule: with holds deferred,
// the gate passes and the hold names the decision and the files.
func TestDecisionsRespectedYOLOHold(t *testing.T) {
	r := newRespectedRepo(t)
	base := r.branch("orchard/child")
	r.write("internal/store/db.go", "package store\n// rewrite\n")
	r.commit("feat: rewrite")
	b := newRespectedBoard(t, r.dir, "Nothing cited.", base)
	b.wf.DeferDecisionHolds = true
	if err := b.check(); err != nil {
		t.Fatalf("YOLO: want pass, got:\n%v", err)
	}
	holds := b.wf.DecisionHolds()
	if len(holds) != 1 || holds[0].Rule != DecisionsRespectedRule || holds[0].Ref != "orchard/rules#D-1" || !strings.Contains(holds[0].Why, "internal/store/db.go") {
		t.Fatalf("holds = %+v", holds)
	}
	c := YOLOHoldComment(holds[0], "in progress")
	if h, _, ok := ParseYOLOHoldComment(c); !ok || h.Ref != "orchard/rules#D-1" || !strings.Contains(h.Why, "internal/store/db.go") {
		t.Fatalf("comment %q does not round-trip", c)
	}
}

// TestDecisionsRespectedIgnoresScopeAndAt is AC4: a UI issue changing a
// file under a hard decision scoped system:Workflow, or one with at: in
// design, is refused the same way.
func TestDecisionsRespectedIgnoresScopeAndAt(t *testing.T) {
	r := newRespectedRepo(t)
	base := r.branch("orchard/child")
	r.write("flows/main.yaml", "x: 1\n")
	r.write("docs/a.md", "# a\n")
	r.commit("feat: flows and docs")
	b := newRespectedBoard(t, r.dir, "Nothing cited.", base)
	wantRefusal(t, b.check(), "orchard/rules#D-3", "flows/main.yaml", "orchard/rules#D-4", "docs/a.md")
}

// TestDecisionsRespectedMergeOfMain is AC5: main changes a guarded file and
// is merged into a branch that didn't touch it: the branch passes.
func TestDecisionsRespectedMergeOfMain(t *testing.T) {
	r := newRespectedRepo(t)
	base := r.branch("orchard/child")
	r.write("README.md", "branch\n")
	r.commit("feat: readme")
	r.git("checkout", "-q", "main")
	r.write("internal/store/db.go", "package store\n// main moved\n")
	r.commit("feat: main changes the store")
	r.git("checkout", "-q", "work/orchard/child")
	r.git("merge", "-q", "--no-edit", "-m", "chore(merge): main into orchard/child", "main")
	b := newRespectedBoard(t, r.dir, "Nothing cited.", base)
	if err := b.check(); err != nil {
		t.Fatalf("a merge of main counted as the branch's change:\n%v", err)
	}
	// Main moving on after the merge changes nothing either.
	r.git("checkout", "-q", "main")
	r.write("internal/store/more.go", "package store\n")
	r.commit("feat: more on main")
	if err := b.check(); err != nil {
		t.Fatalf("main moving on held the branch:\n%v", err)
	}
}

// TestDecisionsRespectedReplacedJudgedByReplacement is AC6: touching the
// paths: of a replaced hard decision is judged by its replacement.
func TestDecisionsRespectedReplacedJudgedByReplacement(t *testing.T) {
	r := newRespectedRepo(t)
	base := r.branch("orchard/child")
	r.write("cache/lru.go", "package cache\n")
	r.write("vendor/x.go", "package x\n")
	r.commit("feat: cache and vendor")
	b := newRespectedBoard(t, r.dir, "Nothing cited.", base)
	err := b.check()
	wantRefusal(t, err, "orchard/rules#D-6", "cache/lru.go")
	for _, not := range []string{"orchard/rules#D-5", "orchard/rules#D-7", "vendor/x.go"} {
		if strings.Contains(err.Error(), not) {
			t.Errorf("replaced or expired decision named (%s):\n%v", not, err)
		}
	}
	b = newRespectedBoard(t, r.dir, "Follows orchard/rules#D-6.", base)
	if err := b.check(); err != nil {
		t.Fatalf("citing the replacement: want pass, got:\n%v", err)
	}
}

// TestDecisionsRespectedNoTouchAndNoBase is AC7: a branch touching no
// guarded path passes; an issue with no recorded base passes with a
// warning line; issues created before since pass.
func TestDecisionsRespectedNoTouchAndNoBase(t *testing.T) {
	r := newRespectedRepo(t)
	base := r.branch("orchard/child")
	r.write("README.md", "only the readme\n")
	r.commit("docs: readme")
	b := newRespectedBoard(t, r.dir, "Nothing cited.", base)
	if err := b.check(); err != nil {
		t.Fatalf("no guarded path: want pass, got:\n%v", err)
	}
	if w := b.wf.DecisionWarnings(); len(w) != 0 {
		t.Fatalf("unexpected warnings %q", w)
	}

	r.write("internal/store/db.go", "package store\n// x\n")
	r.commit("feat: store")
	nb := newRespectedBoard(t, r.dir, "Nothing cited.", "")
	if err := nb.check(); err != nil {
		t.Fatalf("no recorded base: want pass, got:\n%v", err)
	}
	w := nb.wf.DecisionWarnings()
	if len(w) != 1 || !strings.Contains(w[0], "no recorded base") || !strings.Contains(w[0], "didn't run") {
		t.Fatalf("warnings = %q", w)
	}

	old := newRespectedBoard(t, r.dir, "Nothing cited.", base)
	old.issue.Created = "2026-10-15"
	if err := old.check(); err != nil {
		t.Fatalf("created before since: want pass, got:\n%v", err)
	}
}

// TestDecisionsRespectedNoBranch: a recorded base but no work branch and
// no recorded head is a warning, not a pass in silence.
func TestDecisionsRespectedNoBranch(t *testing.T) {
	r := newRespectedRepo(t)
	base := r.git("rev-parse", "main")
	b := newRespectedBoard(t, r.dir, "Nothing cited.", base)
	if err := b.check(); err != nil {
		t.Fatalf("want pass, got:\n%v", err)
	}
	if w := b.wf.DecisionWarnings(); len(w) != 1 || !strings.Contains(w[0], "no branch work/orchard/child") {
		t.Fatalf("warnings = %q", w)
	}
}

// TestDecisionsRespectedOwnDecisionCounts: a hard decision the issue made
// itself counts as cited.
func TestDecisionsRespectedOwnDecisionCounts(t *testing.T) {
	r := newRespectedRepo(t)
	base := r.branch("orchard/child")
	r.write("own/x.go", "package own\n")
	r.commit("feat: own")
	b := newRespectedBoard(t, r.dir, "Nothing cited.\n\n## Decisions\n\n"+
		`- **D-1** · 2026-11-01 · chat: Own area, "own" {scope: project; hard; paths: own/**}`, base)
	x := BuildDecisionIndex([]*Issue{b.issue}, nil)
	b.wf.DecisionsInForce = func() (*DecisionIndex, InForceOptions) { return x, InForceOptions{} }
	if err := b.check(); err != nil {
		t.Fatalf("own decision: want pass, got:\n%v", err)
	}
}

// TestDecisionsRespectedLint: a bad since is a lint finding, and the rule
// counts as a decision rule.
func TestDecisionsRespectedLint(t *testing.T) {
	wf := &WorkflowConfig{Transitions: []WorkflowTransition{{From: "a", To: "b", Actions: []WorkflowAction{
		{Type: "validate", Rule: DecisionsRespectedRule + ": since yesterday"}}}}}
	got := strings.Join(wf.lintRules(), "\n")
	if !strings.Contains(got, "decisions_respected") {
		t.Fatalf("lint = %q", got)
	}
	if !wf.UsesDecisionRules() {
		t.Fatal("UsesDecisionRules false with decisions_respected")
	}
}
