package bench

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/standin"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// doneBoard is a repo whose n done issues each have a branch merged with
// --no-ff: a source file and a test file of their own, with main moved on by
// one file beside each.
func doneBoard(t *testing.T, n int) *tracker.Project {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "r")
	mustGit(t, filepath.Dir(repo), "init", "-q", "-b", "main", repo)
	mustGit(t, repo, "config", "maintenance.auto", "false")
	mustGit(t, repo, "config", "gc.auto", "0")
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	commit := func(msg string, files ...string) {
		t.Helper()
		at = at.Add(time.Minute)
		for _, f := range files {
			write(t, filepath.Join(repo, f), "package a\n\nimport \"testing\"\n\nfunc Test"+strings.NewReplacer("/", "", ".", "", "_", "").Replace(f)+"(t *testing.T) {}\n")
		}
		mustGit(t, repo, "add", "-A")
		t.Setenv("GIT_COMMITTER_DATE", at.Format(time.RFC3339))
		mustGit(t, repo, "commit", "-q", "--allow-empty", "--date", at.Format(time.RFC3339), "-m", msg)
	}
	commit("start", "go.go")
	issues := t.TempDir() // outside the repo: not in any commit
	for i := 0; i < n; i++ {
		start := at
		mustGit(t, repo, "checkout", "-q", "-b", fmt.Sprintf("work/%d", i))
		commit("code", fmt.Sprintf("c%d.go", i))
		commit("test", fmt.Sprintf("c%d_test.go", i))
		head := mustGit(t, repo, "rev-parse", "HEAD")
		mustGit(t, repo, "checkout", "-q", "main")
		commit(fmt.Sprintf("main moves %d", i), fmt.Sprintf("m%d.go", i))
		t.Setenv("GIT_COMMITTER_DATE", at.Format(time.RFC3339))
		mustGit(t, repo, "merge", "-q", "--no-ff", "-m", "merge", head)
		merged := mustGit(t, repo, "rev-parse", "HEAD")

		path := filepath.Join(issues, fmt.Sprintf("i%03d.md", i))
		write(t, path, fmt.Sprintf("---\ntitle: \"I %03d\"\nstatus: \"done\"\ntype: \"bugfix\"\n---\n\nBody.\n", i))
		if err := tracker.SaveStats(path, tracker.StatsStore{
			Dispatches:  []tracker.Dispatch{{At: start.Add(time.Second)}},
			Transitions: []tracker.TransitionStat{{To: "done", TS: at, BranchHead: head, MergeSHA: merged}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	return &tracker.Project{Name: "R", Slug: "r", IssueDir: issues, WorkDir: repo}
}

// loggingGit puts a git on PATH that logs each call to the returned file.
func loggingGit(t *testing.T) (log string) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	bin := t.TempDir()
	log = filepath.Join(bin, "calls.log")
	// The log sits beside the stand-in, so its body is the same every run.
	standin.Write(t, filepath.Join(bin, "git"), "echo \"$*\" >> \"${0%/*}/calls.log\"\nexec "+real+" \"$@\"\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// TestCandidatesRunsNoGitPerIssue is the bug: 17 git calls per done issue,
// 6008 and 100 s on the live board, shown or not, since the ranking needs
// every row. The calls may not grow with the board at all.
func TestCandidatesRunsNoGitPerIssue(t *testing.T) {
	run := func(n int) (calls int, cands []Candidate, log string) {
		p := doneBoard(t, n)
		issues, err := tracker.LoadIssues(p.IssueDir)
		if err != nil {
			t.Fatal(err)
		}
		log = loggingGit(t)
		cands = Candidates(p, issues, nil)
		data, _ := os.ReadFile(log)
		return strings.Count(string(data), "\n"), cands, string(data)
	}
	small, cands, _ := run(3)
	big, bigCands, log := run(16)
	t.Logf("git calls: %d on 3 done issues, %d on 16", small, big)
	if big > small || big > 6 {
		t.Errorf("git calls grow with the board: %d on 3 done issues, %d on 16\n%s", small, big, log)
	}
	for _, all := range [][]Candidate{cands, bigCands} {
		for _, c := range all {
			if c.Why != "" || c.Lines != 10 || c.Files != 2 || c.Hidden != 1 || c.Tests != 1 {
				t.Errorf("%+v", c)
			}
			// Each is merged over the one main commit made beside it.
			if strings.Join(c.Risks, ",") != "main merged in (1 files not its own)" {
				t.Errorf("%s risks = %v", c.Slug, c.Risks)
			}
		}
	}
	if len(bigCands) != 16 {
		t.Fatalf("%d candidates", len(bigCands))
	}
}

// A candidate's row says what the one-issue path says of the same issue.
func TestCandidatesAgreeWithFindRange(t *testing.T) {
	p := doneBoard(t, 4)
	issues, err := tracker.LoadIssues(p.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	byslug := map[string]Candidate{}
	for _, c := range Candidates(p, issues, nil) {
		byslug[c.Slug] = c
	}
	for _, is := range issues {
		stats, _ := tracker.LoadStats(is.FilePath)
		r, err := FindRange(p.WorkDir, stats)
		if err != nil {
			t.Fatal(err)
		}
		c := byslug[is.Slug]
		if c.rng.Base != r.Base || c.rng.Merged != r.Merged || c.rng.Head != r.Head || c.rng.Commits != r.Commits || c.rng.Lines != r.Lines ||
			strings.Join(c.rng.Files, ",") != strings.Join(r.Files, ",") {
			t.Errorf("%s: %+v, FindRange %+v", is.Slug, c.rng, r)
		}
		if strings.Join(c.hidden, ",") != strings.Join(HiddenFiles(p.WorkDir, r), ",") {
			t.Errorf("%s: hidden %v", is.Slug, c.hidden)
		}
		// git's own reading of the same range, the path RangeOf keeps.
		own, err := RangeOf(p.WorkDir, r.Base, r.tip())
		if err != nil {
			t.Fatal(err)
		}
		if own.Commits != r.Commits || own.Lines != r.Lines || strings.Join(own.Files, ",") != strings.Join(r.Files, ",") {
			t.Errorf("%s: git reads %+v, the batch %+v", is.Slug, own, r)
		}
	}
}

// A commit gone from the repo costs that issue alone, and it says why.
func TestCandidatesNameACommitThatIsGone(t *testing.T) {
	p := doneBoard(t, 2)
	issues, err := tracker.LoadIssues(p.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	gone := strings.Repeat("ab", 20)
	stats := tracker.StatsStore{Transitions: []tracker.TransitionStat{{To: "done", TS: time.Now(), BranchHead: gone, MergeSHA: gone}}}
	if err := tracker.SaveStats(issues[0].FilePath, stats); err != nil {
		t.Fatal(err)
	}
	cands := Candidates(p, issues, nil)
	if len(cands) != 2 || cands[0].Why != "" || !strings.Contains(cands[1].Why, "not in this repository") {
		t.Fatalf("%+v", cands)
	}
}

// The walk ends at the first commit older than the issue, as --since does,
// and an empty commit is no commit of its own.
func TestRangeWalkEndsAtTheFirstOlderCommit(t *testing.T) {
	t0 := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	h := history{
		"d": {date: t0.Add(3 * time.Hour).Unix(), parents: []string{"c", "side"}},
		"c": {date: t0.Add(2 * time.Hour).Unix(), parents: []string{"b"}},
		"b": {date: t0.Add(time.Hour).Unix(), parents: []string{"a"}},
		// Older than the issue, with a newer commit under it: not reached.
		"a":    {date: t0.Add(-time.Hour).Unix(), parents: []string{"z"}},
		"z":    {date: t0.Add(time.Hour).Unix(), parents: []string{"y"}},
		"root": {date: t0.Unix()},
	}
	r, err := h.rangeOf(recorded{merged: "d", since: t0})
	if err != nil || r.Base != "a" {
		t.Fatalf("base %q, %v", r.Base, err)
	}
	if own := h.own(r); strings.Join(own, ",") != "c,b" {
		t.Errorf("own = %v, want the line without its merge", own)
	}
	if _, err := h.rangeOf(recorded{merged: "a", since: t0}); err == nil || !strings.Contains(err.Error(), "no commits on") {
		t.Errorf("err = %v", err)
	}
	if _, err := h.rangeOf(recorded{merged: "root", since: t0}); err == nil || !strings.Contains(err.Error(), "no parent") {
		t.Errorf("err = %v", err)
	}
	if _, err := h.rangeOf(recorded{merged: "gone", since: t0}); err == nil || !strings.Contains(err.Error(), "not in this repository") {
		t.Errorf("err = %v", err)
	}
	var e Range
	e.addCommit("\n", map[string]bool{})
	if e.Commits != 0 {
		t.Errorf("an empty commit counted: %+v", e)
	}
}

// A range with nothing to diff keeps the ranges after it on their own lines.
func TestForeignFilesKeepsEachRangesAnswer(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "r")
	mustGit(t, filepath.Dir(repo), "init", "-q", "-b", "main", repo)
	commit := func(file string) string {
		write(t, filepath.Join(repo, file), "x "+file+"\n")
		mustGit(t, repo, "add", "-A")
		mustGit(t, repo, "commit", "-q", "-m", file)
		return mustGit(t, repo, "rev-parse", "HEAD")
	}
	a, b, c := commit("a"), commit("b b"), commit("c")
	got := foreignFiles(repo, []Range{
		{Base: a, Merged: b},
		{Base: a, Merged: a},
		{Base: b, Merged: c, Files: []string{"c"}},
		{Base: a, Merged: c, Files: []string{"c"}},
	})
	want := []string{"b b", "", "", "b b"}
	if len(got) != len(want) {
		t.Fatalf("got %q", got)
	}
	for i := range want {
		if strings.Join(got[i], ",") != want[i] {
			t.Errorf("range %d: %q, want %q", i, got[i], want[i])
		}
	}
	if foreignFiles(repo, nil) != nil {
		t.Error("no ranges, no call")
	}
}

// One process answers file after file: an empty file, a binary one, a name
// with a space, a missing one between them.
func TestObjectsReadsFileAfterFile(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "r")
	mustGit(t, filepath.Dir(repo), "init", "-q", "-b", "main", repo)
	bin := "a\x00b\nc"
	write(t, filepath.Join(repo, "empty"), "")
	write(t, filepath.Join(repo, "bin"), bin)
	write(t, filepath.Join(repo, "a b"), "spaced\n")
	mustGit(t, repo, "add", "-A")
	mustGit(t, repo, "commit", "-q", "-m", "x")
	o := openObjects(repo)
	defer o.close()
	for _, x := range []struct {
		path, want string
		ok         bool
	}{{"empty", "", true}, {"nope", "", false}, {"bin", bin, true}, {"no pe", "", false}, {"a b", "spaced\n", true}, {"two\nlines", "", false}, {"bin", bin, true}} {
		got, ok := o.read("HEAD", x.path)
		if ok != x.ok || got != x.want {
			t.Errorf("read %q = %q, %v", x.path, got, ok)
		}
	}
	o.close()
	if _, ok := o.read("HEAD", "bin"); ok {
		t.Error("a closed reader answered")
	}
}

func TestLoadHistorySkipsAMissingTip(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "r")
	mustGit(t, filepath.Dir(repo), "init", "-q", "-b", "main", repo)
	write(t, filepath.Join(repo, "a"), "a\n")
	mustGit(t, repo, "add", "-A")
	mustGit(t, repo, "commit", "-q", "-m", "x")
	head := mustGit(t, repo, "rev-parse", "HEAD")
	h, err := loadHistory(repo, []string{strings.Repeat("0a", 20), head})
	if err != nil || len(h) != 1 {
		t.Fatalf("%v, %v", h, err)
	}
	ct, _ := strconv.ParseInt(mustGit(t, repo, "log", "-1", "--format=%ct"), 10, 64)
	if h[head].date != ct || len(h[head].parents) != 0 {
		t.Errorf("%+v", h[head])
	}
}
