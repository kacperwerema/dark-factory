package proof

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

var t0 = time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)

const issueBody = `---
title: "Prove me"
status: "testing"
system: "Sys"
created: "2026-10-23"
---

Body.

## Acceptance Criteria
- [ ] The test proves it
- [ ] The change proves it
- [ ] A value proves it
- [ ] A step proves it
`

// fixture is a repo with main and work/<slug>, and the issue under issues/.
type fixture struct {
	root  string
	issue *tracker.Issue
}

func gitAt(t *testing.T, dir string, at time.Time, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	stamp := at.Format(time.RFC3339)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp,
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commitFile(t *testing.T, dir string, at time.Time, name, content, msg string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAt(t, dir, at, "add", name)
	gitAt(t, dir, at, "commit", "-q", "-m", msg)
	return gitAt(t, dir, at, "rev-parse", "HEAD")
}

// newFixture builds main (with a pre-fork Proves: trailer from another
// issue), the work branch (a test commit, a change commit, a merge of main
// that brings another issue's trailer) and the issue file.
func newFixture(t *testing.T, branchTest string) fixture {
	t.Helper()
	root := t.TempDir()
	gitAt(t, root, t0, "init", "-q", "-b", "main")
	commitFile(t, root, t0, "README", "x", "init\n\nProves: AC2")
	if err := os.MkdirAll(filepath.Join(root, "issues", "Sys"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "issues", "Sys", "prove-me.md")
	if err := os.WriteFile(path, []byte(issueBody), 0o644); err != nil {
		t.Fatal(err)
	}
	issues, err := tracker.LoadIssues(filepath.Join(root, "issues"))
	if err != nil || len(issues) != 1 {
		t.Fatalf("load issues: %v %d", err, len(issues))
	}
	issue := issues[0]
	branch := "work/" + issue.Slug
	gitAt(t, root, t0, "checkout", "-q", "-b", branch)
	commitFile(t, root, t0.Add(time.Hour), "pkg/foo_test.go", "func "+branchTest+"(t *testing.T) {}\n", "test\n\nProves: AC1 "+branchTest)
	commitFile(t, root, t0.Add(2*time.Hour), "pkg/foo.go", "package pkg\n", "change\n\nProves: AC2")
	gitAt(t, root, t0, "checkout", "-q", "main")
	commitFile(t, root, t0.Add(3*time.Hour), "other.go", "x", "other issue\n\nProves: AC3")
	gitAt(t, root, t0, "checkout", "-q", branch)
	gitAt(t, root, t0.Add(4*time.Hour), "merge", "-q", "--no-ff", "-m", "Merge main", "main")
	gitAt(t, root, t0, "checkout", "-q", "main")
	return fixture{root: root, issue: issue}
}

func testsComment(at time.Time) []tracker.Comment {
	return []tracker.Comment{{ID: 1, Date: at.Format(time.RFC3339), Text: "tests: go test ./... pass"}}
}

func writeRun(t *testing.T, dir, session, started, results string) {
	t.Helper()
	runDir := filepath.Join(dir, ".playtest", "runs", session, started)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "results.yaml"), []byte(results), 0o644); err != nil {
		t.Fatal(err)
	}
}

func criterion(t *testing.T, r Report, id string) Criterion {
	t.Helper()
	for _, c := range r.Criteria {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no criterion %s in %+v", id, r.Criteria)
	return Criterion{}
}

func TestCollectReadsEverySourceAndChecksIt(t *testing.T) {
	f := newFixture(t, "TestFoo")
	writeRun(t, f.root, "s", "1-auto", `session: s
issue: `+f.issue.Slug+`
mode: auto
started: "2026-10-23T15:00:00Z"
steps:
  - id: TP1
    status: pass
    proves: [AC4]
  - id: TP2
    status: pass
    proves: [AC9]
`)
	if err := tracker.SaveData(f.issue.FilePath, tracker.DataStore{Proofs: map[string][]tracker.ProofLink{
		"AC3": {{Kind: tracker.ProofValue, Ref: "690 vs 380", Criterion: "A value proves it"}},
	}}); err != nil {
		t.Fatal(err)
	}

	r := Collect(f.root, f.issue, testsComment(t0.Add(150*time.Minute)))

	ac1 := criterion(t, r, "AC1")
	if len(ac1.Links) != 1 || ac1.Links[0].Kind != tracker.ProofTest || ac1.Links[0].Ref != "TestFoo" || !ac1.Links[0].Check.OK {
		t.Errorf("AC1 = %+v, want a passing test link to TestFoo from the trailer", ac1.Links)
	}
	if ac1.Links[0].Check.State != "reported after the last change" {
		t.Errorf("AC1 state = %q: the merge of main must not make the report stale", ac1.Links[0].Check.State)
	}
	ac2 := criterion(t, r, "AC2")
	if len(ac2.Links) != 1 || ac2.Links[0].Kind != tracker.ProofCommit || !ac2.Links[0].Check.OK {
		t.Errorf("AC2 = %+v, want one commit link (main's pre-fork trailer must not count)", ac2.Links)
	}
	ac3 := criterion(t, r, "AC3")
	if len(ac3.Links) != 1 || ac3.Links[0].Kind != tracker.ProofValue || ac3.Links[0].Check.State != "reported" {
		t.Errorf("AC3 = %+v, want only the prove value (main's trailer arrived by merge)", ac3.Links)
	}
	ac4 := criterion(t, r, "AC4")
	if len(ac4.Links) != 1 || ac4.Links[0].Kind != tracker.ProofStep || ac4.Links[0].Source != SourcePlaytest || !ac4.Links[0].Check.OK {
		t.Errorf("AC4 = %+v, want a passing step link from proves:", ac4.Links)
	}
	if len(r.Strays) != 1 || r.Strays[0].ID != "AC9" {
		t.Errorf("strays = %+v, want AC9 from TP2", r.Strays)
	}
	if err := GateError(r); err != nil {
		t.Errorf("GateError = %v, want nil with every criterion proven", err)
	}
}

func TestTestLinkStaleNotFoundAndUnreported(t *testing.T) {
	f := newFixture(t, "TestFoo")
	stale := Collect(f.root, f.issue, testsComment(t0.Add(90*time.Minute)))
	if c := criterion(t, stale, "AC1"); c.Proven() || !strings.HasPrefix(c.Links[0].Check.State, "stale") {
		t.Errorf("report before the last change: %+v, want stale", c.Links)
	}
	none := Collect(f.root, f.issue, nil)
	if c := criterion(t, none, "AC1"); c.Proven() || c.Links[0].Check.State != "no tests: report" {
		t.Errorf("no report: %+v", c.Links)
	}

	g := newFixture(t, "TestFoo")
	store := tracker.DataStore{Proofs: map[string][]tracker.ProofLink{"AC1": {{Kind: tracker.ProofTest, Ref: "TestNowhere"}}}}
	if err := tracker.SaveData(g.issue.FilePath, store); err != nil {
		t.Fatal(err)
	}
	r := Collect(g.root, g.issue, testsComment(t0.Add(5*time.Hour)))
	for _, l := range criterion(t, r, "AC1").Links {
		if l.Ref == "TestNowhere" && (l.Check.OK || l.Check.State != "not found") {
			t.Errorf("TestNowhere = %+v, want not found", l)
		}
	}
}

func TestStepUsesLatestVerdict(t *testing.T) {
	f := newFixture(t, "TestFoo")
	results := func(started, status string) string {
		return "session: s\nissue: " + f.issue.Slug + "\nmode: auto\nstarted: \"" + started + "\"\nsteps:\n  - id: TP1\n    status: " + status + "\n    proves: [AC4]\n"
	}
	writeRun(t, f.root, "s", "1-auto", results("2026-10-23T15:00:00Z", "pass"))
	writeRun(t, f.root, "s", "2-auto", results("2026-10-23T16:00:00Z", "fail"))
	writeRun(t, f.root, "s", "3-judge", results("2026-10-23T17:00:00Z", "pending"))
	c := criterion(t, Collect(f.root, f.issue, nil), "AC4")
	if c.Proven() || !strings.HasPrefix(c.Links[0].Check.State, "failed") {
		t.Errorf("AC4 = %+v, want failed (latest verdict; pending is not one)", c.Links)
	}
}

func TestManualCommitMustBeOnBranch(t *testing.T) {
	f := newFixture(t, "TestFoo")
	other := gitAt(t, f.root, t0, "rev-parse", "main")
	store := tracker.DataStore{Proofs: map[string][]tracker.ProofLink{"AC3": {{Kind: tracker.ProofCommit, Ref: "0000000000000000000000000000000000000000"}}}}
	if err := tracker.SaveData(f.issue.FilePath, store); err != nil {
		t.Fatal(err)
	}
	if c := criterion(t, Collect(f.root, f.issue, nil), "AC3"); c.Proven() {
		t.Errorf("unknown sha proved AC3: %+v", c.Links)
	}
	// main's head arrived on the branch through the merge, so it is reachable.
	store.Proofs["AC3"] = []tracker.ProofLink{{Kind: tracker.ProofCommit, Ref: other[:7]}}
	if err := tracker.SaveData(f.issue.FilePath, store); err != nil {
		t.Fatal(err)
	}
	if c := criterion(t, Collect(f.root, f.issue, nil), "AC3"); !c.Proven() {
		t.Errorf("reachable commit not found: %+v", c.Links)
	}
}

func TestGateErrorNamesUnprovenAndHowToFix(t *testing.T) {
	f := newFixture(t, "TestFoo")
	err := GateError(Collect(f.root, f.issue, nil))
	if err == nil {
		t.Fatal("want an error: AC1 has no report, AC3 and AC4 no links")
	}
	msg := err.Error()
	for _, want := range []string{"3 of 4 acceptance criteria are not proven", "AC1 The test proves it", "no tests: report", "AC3 A value proves it", "AC4", "proves: [AC1]", "Proves: AC1 <TestName>", "issue-cli prove " + f.issue.Slug + " --list"} {
		if !strings.Contains(msg, want) {
			t.Errorf("gate error lacks %q:\n%s", want, msg)
		}
	}
}

func TestTickProvenAndChangedCriterion(t *testing.T) {
	f := newFixture(t, "TestFoo")
	store := tracker.DataStore{Proofs: map[string][]tracker.ProofLink{"AC3": {{Kind: tracker.ProofValue, Ref: "1.8x", Criterion: "An older wording"}}}}
	if err := tracker.SaveData(f.issue.FilePath, store); err != nil {
		t.Fatal(err)
	}
	r := Collect(f.root, f.issue, testsComment(t0.Add(5*time.Hour)))
	if l := criterion(t, r, "AC3").Links[0]; !l.Changed {
		t.Errorf("AC3 link = %+v, want Changed (text differs)", l)
	}
	ticked, err := TickProven(f.issue.FilePath, r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ticked, ",") != "AC1,AC2,AC3" {
		t.Errorf("ticked %v, want AC1,AC2,AC3", ticked)
	}
	data, _ := os.ReadFile(f.issue.FilePath)
	if !strings.Contains(string(data), "- [x] The test proves it") || !strings.Contains(string(data), "- [ ] A step proves it") {
		t.Errorf("body after tick:\n%s", data)
	}
}

func TestSplitTrailer(t *testing.T) {
	ids, tests := splitTrailer("AC1, ac3 TestA TestV2", "AC")
	if strings.Join(ids, ",") != "AC1,AC3" || strings.Join(tests, ",") != "TestA,TestV2" {
		t.Errorf("ids %v tests %v", ids, tests)
	}
}

func TestTrailersSurviveTheMerge(t *testing.T) {
	f := newFixture(t, "TestFoo")
	gitAt(t, f.root, t0, "merge", "-q", "--ff-only", "work/"+f.issue.Slug)
	if err := tracker.SaveStats(f.issue.FilePath, tracker.StatsStore{Dispatches: []tracker.Dispatch{{At: t0.Add(30 * time.Minute)}}}); err != nil {
		t.Fatal(err)
	}
	r := Collect(f.root, f.issue, testsComment(t0.Add(5*time.Hour)))
	if c := criterion(t, r, "AC1"); !c.Proven() {
		t.Errorf("AC1 after the merge: %+v", c.Links)
	}
	if c := criterion(t, r, "AC2"); len(c.Links) != 1 {
		t.Errorf("AC2 after the merge = %+v, want only the branch's trailer (main's init predates the dispatch)", c.Links)
	}
	if c := criterion(t, r, "AC3"); len(c.Links) != 0 {
		t.Errorf("AC3 after the merge = %+v, want none (the other issue's commit is a second parent)", c.Links)
	}
}

func TestNonCodeCommitDoesNotStaleTheReport(t *testing.T) {
	f := newFixture(t, "TestFoo")
	branch := "work/" + f.issue.Slug
	gitAt(t, f.root, t0, "checkout", "-q", branch)
	commitFile(t, f.root, t0.Add(5*time.Hour), "docs/guide.md", "# Guide\n", "docs: guide")
	commitFile(t, f.root, t0.Add(6*time.Hour), "CHANGELOG.md", "- entry\n", "docs: changelog")
	commitFile(t, f.root, t0.Add(7*time.Hour), "cmd/x/README.md", "readme\n", "docs: readme")
	commitFile(t, f.root, t0.Add(7*time.Hour+10*time.Minute), "playtest/sessions/prove.yaml", "steps: []\n", "test(playtest): session")
	commitFile(t, f.root, t0.Add(7*time.Hour+20*time.Minute), "scripts/checks/prove.sh", "#!/bin/sh\n", "chore(checks): guided check")
	gitAt(t, f.root, t0, "checkout", "-q", "main")

	// The report came after the last code commit (2h) and before the docs ones.
	r := Collect(f.root, f.issue, testsComment(t0.Add(150*time.Minute)))
	if c := criterion(t, r, "AC1"); !c.Proven() || c.Links[0].Check.State != "reported after the last change" {
		t.Errorf("AC1 after non-code commits = %+v, want reported after the last change", c.Links)
	}

	gitAt(t, f.root, t0, "checkout", "-q", branch)
	commitFile(t, f.root, t0.Add(8*time.Hour), "docs/notes.go", "package docs\n", "a Go file under docs/ is docs too")
	commitFile(t, f.root, t0.Add(9*time.Hour), "pkg/bar.go", "package pkg\n", "code")
	gitAt(t, f.root, t0, "checkout", "-q", "main")
	r = Collect(f.root, f.issue, testsComment(t0.Add(150*time.Minute)))
	if c := criterion(t, r, "AC1"); c.Proven() || !strings.HasPrefix(c.Links[0].Check.State, "stale") {
		t.Errorf("AC1 after a code commit = %+v, want stale", c.Links)
	}
}

func TestNonCodeOnly(t *testing.T) {
	for _, tc := range []struct {
		files []string
		want  bool
	}{
		{[]string{"docs/a.md", "README.md", "cmd/issue-cli/CHANGELOG.md"}, true},
		{[]string{"docs/img/shot.png"}, true},
		{[]string{"CHANGELOG"}, true},
		{[]string{"docs/a.md", "main.go"}, false},
		{[]string{"templates/docs.html"}, false},
		{[]string{"playtest/sessions/prove.yaml", "scripts/checks/prove.sh", "issues/CLI/x.md", "retros/r.md", "sessions/s.md"}, true},
		{[]string{"playtest/scenarios/board/tracker/x.md"}, true},
		{[]string{"playtest/scenarios/board/projects.yaml"}, false},
		{[]string{"static/style.css"}, false},
		{[]string{"cmd/issue-cli/testdata/out.txt"}, false},
		{nil, false},
	} {
		if got := nonCodeOnly(tc.files); got != tc.want {
			t.Errorf("nonCodeOnly(%v) = %v, want %v", tc.files, got, tc.want)
		}
	}
}

// A branch started on another issue's branch before that one reached main
// holds the other issue's commits in its own first-parent chain. Their
// trailers name the other issue's criteria, so they must not count, while
// the branch is live or after both are merged.
func TestTrailersFromABaseBranchOfAnotherIssue(t *testing.T) {
	root := t.TempDir()
	gitAt(t, root, t0, "init", "-q", "-b", "main")
	commitFile(t, root, t0, "README", "x", "init")
	dir := filepath.Join(root, "issues", "Sys")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prove-me.md"), []byte(issueBody), 0o644); err != nil {
		t.Fatal(err)
	}
	base := strings.Replace(issueBody, "Prove me", "Base work", 1)
	if err := os.WriteFile(filepath.Join(dir, "base-work.md"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	issues, err := tracker.LoadIssues(filepath.Join(root, "issues"))
	if err != nil || len(issues) != 2 {
		t.Fatalf("load issues: %v %d", err, len(issues))
	}
	var issue, other *tracker.Issue
	for _, is := range issues {
		if is.Slug == "sys/prove-me" {
			issue = is
		} else {
			other = is
		}
	}

	// The other issue's branch: a test commit, recorded at its transition.
	gitAt(t, root, t0, "checkout", "-q", "-b", "work/"+other.Slug)
	otherHead := commitFile(t, root, t0.Add(time.Hour), "pkg/base_test.go", "func TestBase(t *testing.T) {}\n", "base test\n\nProves: AC1 TestBase")
	if err := tracker.SaveStats(other.FilePath, tracker.StatsStore{Transitions: []tracker.TransitionStat{{TS: t0.Add(90 * time.Minute), BranchHead: otherHead}}}); err != nil {
		t.Fatal(err)
	}
	// This issue's branch starts on it, dispatched before that commit.
	gitAt(t, root, t0, "checkout", "-q", "-b", "work/"+issue.Slug)
	ownHead := commitFile(t, root, t0.Add(2*time.Hour), "pkg/own_test.go", "func TestOwn(t *testing.T) {}\n", "own test\n\nProves: AC2 TestOwn")
	if err := tracker.SaveStats(issue.FilePath, tracker.StatsStore{
		Dispatches:  []tracker.Dispatch{{At: t0.Add(30 * time.Minute)}},
		Transitions: []tracker.TransitionStat{{TS: t0.Add(3 * time.Hour), BranchHead: ownHead}},
	}); err != nil {
		t.Fatal(err)
	}
	gitAt(t, root, t0, "checkout", "-q", "main")

	check := func(when string) {
		t.Helper()
		r := Collect(root, issue, testsComment(t0.Add(5*time.Hour)))
		if c := criterion(t, r, "AC1"); len(c.Links) != 0 {
			t.Errorf("%s: AC1 = %+v, want none (TestBase is the other issue's)", when, c.Links)
		}
		if c := criterion(t, r, "AC2"); len(c.Links) != 1 || c.Links[0].Ref != "TestOwn" {
			t.Errorf("%s: AC2 = %+v, want the branch's own TestOwn", when, c.Links)
		}
	}
	check("live branch")

	gitAt(t, root, t0, "merge", "-q", "--ff-only", "work/"+issue.Slug)
	gitAt(t, root, t0, "branch", "-q", "-D", "work/"+issue.Slug, "work/"+other.Slug)
	check("after the merge")
}

// A test title with a colon ("D-8: …", an it() title) is found, and a
// Proves: line in its own paragraph before Co-Authored-By counts.
func TestColonTitleAndProvesParagraph(t *testing.T) {
	f := newFixture(t, "TestFoo")
	gitAt(t, f.root, t0, "checkout", "-q", "work/"+f.issue.Slug)
	commitFile(t, f.root, t0.Add(5*time.Hour), "core/test/atHome.test.ts",
		"it('D-8: kupuje mleko…', () => {})\n",
		"test: D-8\n\nProves: AC4\n\nCo-Authored-By: t <t@t>")
	gitAt(t, f.root, t0, "checkout", "-q", "main")
	if err := tracker.SaveData(f.issue.FilePath, tracker.DataStore{Proofs: map[string][]tracker.ProofLink{
		"AC3": {{Kind: tracker.ProofTest, Ref: "D-8: kupuje mleko…", Criterion: "A value proves it"}},
	}}); err != nil {
		t.Fatal(err)
	}
	r := Collect(f.root, f.issue, testsComment(t0.Add(6*time.Hour)))
	if c := criterion(t, r, "AC3"); len(c.Links) != 1 || !c.Links[0].Check.OK {
		t.Errorf("AC3 = %+v, want the it() title with a colon found", c.Links)
	}
	if c := criterion(t, r, "AC4"); len(c.Links) != 1 || c.Links[0].Kind != tracker.ProofCommit {
		t.Errorf("AC4 = %+v, want the commit from the Proves: paragraph before Co-Authored-By", c.Links)
	}
}

// A live branch that only fast-forwarded to main, with no commits of its
// own, collects no trailers: the Proves: on main came from another issue
// that merged after this one was dispatched.
func TestFastForwardedBranchWithNoOwnCommits(t *testing.T) {
	root := t.TempDir()
	gitAt(t, root, t0, "init", "-q", "-b", "main")
	start := commitFile(t, root, t0, "README", "x", "init")
	dir := filepath.Join(root, "issues", "Sys")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prove-me.md"), []byte(issueBody), 0o644); err != nil {
		t.Fatal(err)
	}
	other := strings.Replace(issueBody, "Prove me", "Other work", 1)
	if err := os.WriteFile(filepath.Join(dir, "other-work.md"), []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}
	issues, err := tracker.LoadIssues(filepath.Join(root, "issues"))
	if err != nil || len(issues) != 2 {
		t.Fatalf("load issues: %v %d", err, len(issues))
	}
	var issue, otherIssue *tracker.Issue
	for _, is := range issues {
		if is.Slug == "sys/prove-me" {
			issue = is
		} else {
			otherIssue = is
		}
	}

	// This issue is dispatched on main and records main as its head.
	gitAt(t, root, t0, "branch", "work/"+issue.Slug)
	if err := tracker.SaveStats(issue.FilePath, tracker.StatsStore{
		Dispatches:  []tracker.Dispatch{{At: t0.Add(30 * time.Minute)}},
		Transitions: []tracker.TransitionStat{{TS: t0.Add(35 * time.Minute), BranchHead: start}},
	}); err != nil {
		t.Fatal(err)
	}
	// The other issue commits a Proves: trailer, records it and reaches main.
	gitAt(t, root, t0, "checkout", "-q", "-b", "work/"+otherIssue.Slug)
	otherHead := commitFile(t, root, t0.Add(time.Hour), "pkg/other_test.go", "func TestOther(t *testing.T) {}\n", "other test\n\nProves: AC1")
	gitAt(t, root, t0, "checkout", "-q", "main")
	gitAt(t, root, t0, "merge", "-q", "--ff-only", "work/"+otherIssue.Slug)
	if err := tracker.SaveStats(otherIssue.FilePath, tracker.StatsStore{Transitions: []tracker.TransitionStat{
		{TS: t0.Add(90 * time.Minute), BranchHead: otherHead},
		{TS: t0.Add(2 * time.Hour), BranchHead: otherHead, MergeSHA: otherHead},
	}}); err != nil {
		t.Fatal(err)
	}
	// This branch fast-forwards to main and has nothing of its own yet.
	gitAt(t, root, t0, "branch", "-f", "work/"+issue.Slug, "main")

	r := Collect(root, issue, testsComment(t0.Add(3*time.Hour)))
	for _, c := range r.Criteria {
		if len(c.Links) != 0 {
			t.Errorf("%s = %+v, want none: the branch has no commits of its own", c.ID, c.Links)
		}
	}
}

func TestTrailerNamingAStepIDGetsAHint(t *testing.T) {
	f := newFixture(t, "TestFoo")
	gitAt(t, f.root, t0, "checkout", "-q", "work/"+f.issue.Slug)
	commitFile(t, f.root, t0.Add(5*time.Hour), "pkg/bar.go", "package pkg\n", "bar\n\nProves: AC4 TP4")
	gitAt(t, f.root, t0, "checkout", "-q", "main")
	writeRun(t, f.root, "flow", "1-auto", "session: flow\nissue: "+f.issue.Slug+"\nmode: auto\nstarted: \"2026-10-23T15:00:00Z\"\nsteps:\n  - id: TP4\n    status: pass\n")

	r := Collect(f.root, f.issue, testsComment(t0.Add(6*time.Hour)))
	var tp4 *CheckedLink
	ac4 := criterion(t, r, "AC4")
	for i, l := range ac4.Links {
		if l.Ref == "TP4" {
			tp4 = &ac4.Links[i]
		}
	}
	if tp4 == nil || tp4.Check.OK {
		t.Fatalf("AC4 links = %+v, want a failing TP4 test link", ac4.Links)
	}
	for _, want := range []string{"TP4 is a box or playtest step id", "`proves: [AC4]`", "step TP4 in the session flow", "issue-cli prove " + f.issue.Slug + " AC4 --step TP4"} {
		if !strings.Contains(tp4.Check.Fix, want) {
			t.Errorf("fix = %q, want it to contain %q", tp4.Check.Fix, want)
		}
	}

	// A real test name that isn't found keeps the usual fix.
	g := newFixture(t, "TestFoo")
	gitAt(t, g.root, t0, "checkout", "-q", "work/"+g.issue.Slug)
	commitFile(t, g.root, t0.Add(5*time.Hour), "pkg/bar.go", "package pkg\n", "bar\n\nProves: AC4 TestGone")
	gitAt(t, g.root, t0, "checkout", "-q", "main")
	for _, l := range criterion(t, Collect(g.root, g.issue, nil), "AC4").Links {
		if l.Ref == "TestGone" && strings.Contains(l.Check.Fix, "step id") {
			t.Errorf("TestGone fix = %q, want no step hint", l.Check.Fix)
		}
	}
}

func TestRemovedCriterionKeepsTheNextOnesProof(t *testing.T) {
	f := newFixture(t, "TestFoo")
	gitAt(t, f.root, t0, "checkout", "-q", "work/"+f.issue.Slug)
	commitFile(t, f.root, t0.Add(5*time.Hour), "pkg/bar.go", "package pkg\n", "bar\n\nProves: AC3")
	gitAt(t, f.root, t0, "checkout", "-q", "main")

	body, _, err := tracker.RemoveBoxes(f.issue.BodyRaw, []string{"AC2"})
	if err != nil {
		t.Fatal(err)
	}
	f.issue.BodyRaw = body
	r := Collect(f.root, f.issue, testsComment(t0.Add(6*time.Hour)))
	ac3 := criterion(t, r, "AC3")
	if ac3.Text != "A value proves it" || !ac3.Proven() {
		t.Fatalf("AC3 after removing AC2 = %+v, want the same box, proven by its trailer", ac3)
	}
	if len(r.Criteria) != 3 {
		t.Fatalf("criteria = %d, want 3", len(r.Criteria))
	}
	var strays []string
	for _, s := range r.Strays {
		strays = append(strays, s.ID)
	}
	if !containsString(strays, "AC2") {
		t.Errorf("strays = %v, want AC2's trailer listed as a stray", strays)
	}
}
