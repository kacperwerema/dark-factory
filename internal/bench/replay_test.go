package bench

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// replayRepo is a Go module repo for replays: base has Add (wrong) and Sub,
// and a post-checkout hook that must never run in a replay.
type replayRepo struct {
	t      *testing.T
	dir    string
	base   string
	marker string
}

func newReplayRepo(t *testing.T) *replayRepo {
	t.Helper()
	r := &replayRepo{t: t, dir: filepath.Join(t.TempDir(), "repo")}
	r.marker = filepath.Join(t.TempDir(), "hook-ran")
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r.git("init", "-q", "-b", "main")
	r.put("go.mod", "module example.com/calc\n\ngo 1.21\n")
	r.put("calc.go", "package calc\n\nfunc Add(a, b int) int { return a - b }\n\nfunc Sub(a, b int) int { return a - b }\n")
	r.git("add", "-A")
	r.git("commit", "-qm", "base")
	r.base = r.git("rev-parse", "HEAD")
	hook := filepath.Join(r.dir, ".git", "hooks", "post-checkout")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch "+r.marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *replayRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Env = gitEnv(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *replayRepo) put(rel, data string) {
	r.t.Helper()
	p := filepath.Join(r.dir, rel)
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *replayRepo) commit(msg string, files map[string]string) string {
	r.t.Helper()
	for f, d := range files {
		r.put(f, d)
	}
	r.git("add", "-A")
	r.git("commit", "-qm", msg)
	return r.git("rev-parse", "HEAD")
}

const (
	addTest = "package calc\n\nimport \"testing\"\n\nfunc TestAddAdds(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatal(\"2+3\")\n\t}\n}\n"
	fixed   = "package calc\n\nfunc Add(a, b int) int { return a + b }\n\nfunc Sub(a, b int) int { return a - b }\n"
)

// replay runs Replay over base..merged with the range's hidden files and
// tests, and checks it left no worktree, folder or hook run behind.
func (r *replayRepo) replay(merged string, env ReplayEnv) ReplayResult {
	r.t.Helper()
	rng, err := RangeOf(r.dir, r.base, merged)
	if err != nil {
		r.t.Fatal(err)
	}
	hidden := HiddenFiles(r.dir, rng)
	tests := HiddenTests(r.dir, rng, hidden)
	tmp := r.t.TempDir()
	env.TmpRoot = tmp
	_ = os.Remove(r.marker) // the fixture's own checkouts ran it
	res := Replay(context.Background(), r.dir, rng, hidden, tests, env)
	r.leftNothing(tmp)
	return res
}

func (r *replayRepo) leftNothing(tmp string) {
	r.t.Helper()
	if wts := r.git("worktree", "list", "--porcelain"); strings.Count(wts, "worktree ") != 1 {
		r.t.Errorf("a replay left a worktree behind:\n%s", wts)
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		r.t.Errorf("a replay left %d entries in its temp root", len(left))
	}
	if _, err := os.Stat(r.marker); err == nil {
		r.t.Errorf("a replay ran the repo's post-checkout hook")
	}
	if st := r.git("status", "--porcelain"); st != "" {
		r.t.Errorf("a replay changed the repo's checkout:\n%s", st)
	}
}

func TestReplaySolvable(t *testing.T) {
	r := newReplayRepo(t)
	merged := r.commit("fix add", map[string]string{"calc.go": fixed, "add_test.go": addTest})
	res := r.replay(merged, ReplayEnv{})
	if res.Verdict != ReplaySolvable || !strings.Contains(res.Why, "1 hidden tests fail on base and pass with its 1 own commits") {
		t.Errorf("replay = %+v, want solvable", res)
	}
}

func TestReplayTestsPassOnBase(t *testing.T) {
	r := newReplayRepo(t)
	merged := r.commit("sub test", map[string]string{"sub_test.go": "package calc\n\nimport \"testing\"\n\nfunc TestSubOK(t *testing.T) {\n\tif Sub(3, 2) != 1 {\n\t\tt.Fatal(\"3-2\")\n\t}\n}\n"})
	res := r.replay(merged, ReplayEnv{})
	if res.Verdict != ReplayPassOnBase || !strings.Contains(res.Why, "all 1 hidden tests pass on base") {
		t.Errorf("replay = %+v, want tests pass on base", res)
	}
}

// TestReplayDoesNotReplay: the branch merged main, then changed the line
// main changed; its own commit doesn't apply on base.
func TestReplayDoesNotReplay(t *testing.T) {
	r := newReplayRepo(t)
	r.git("checkout", "-qb", "work")
	r.git("checkout", "-q", "main")
	r.commit("main edits add", map[string]string{"calc.go": "package calc\n\nfunc Add(a, b int) int { return a - b } // main\n\nfunc Sub(a, b int) int { return a - b }\n"})
	r.git("checkout", "-q", "work")
	r.git("merge", "-q", "--no-ff", "-m", "merge main", "main")
	merged := r.commit("fix add", map[string]string{"calc.go": "package calc\n\nfunc Add(a, b int) int { return a + b } // fixed\n\nfunc Sub(a, b int) int { return a - b }\n", "add_test.go": addTest})
	res := r.replay(merged, ReplayEnv{})
	if res.Verdict != ReplayNoReplay || !strings.Contains(res.Why, "(fix add) does not apply on base") || !strings.Contains(res.Why, "conflicts in calc.go") {
		t.Errorf("replay = %+v, want doesn't replay", res)
	}
}

// TestReplayNeedsLaterMain: the hidden test calls Mul, which came from
// main after base.
func TestReplayNeedsLaterMain(t *testing.T) {
	r := newReplayRepo(t)
	r.git("checkout", "-qb", "work")
	r.git("checkout", "-q", "main")
	r.commit("main adds mul", map[string]string{"mul.go": "package calc\n\nfunc Mul(a, b int) int { return a * b }\n"})
	r.git("checkout", "-q", "work")
	r.git("merge", "-q", "--no-ff", "-m", "merge main", "main")
	merged := r.commit("fix add, test mul", map[string]string{"calc.go": fixed, "mul_test.go": "package calc\n\nimport \"testing\"\n\nfunc TestMulAndAdd(t *testing.T) {\n\tif Mul(2, 3) != 6 || Add(1, 1) != 2 {\n\t\tt.Fatal(\"mul\")\n\t}\n}\n"})
	res := r.replay(merged, ReplayEnv{})
	if res.Verdict != ReplayNeedsMain || !strings.Contains(res.Why, "TestMulAndAdd fail on base + its own commits") || !strings.Contains(res.Why, "main merged in 1 files") {
		t.Errorf("replay = %+v, want needs later main", res)
	}
}

func TestReplayStopsWhenMemoryIsBlocked(t *testing.T) {
	r := newReplayRepo(t)
	merged := r.commit("fix add", map[string]string{"calc.go": fixed, "add_test.go": addTest})
	ran := false
	res := r.replay(merged, ReplayEnv{
		MemGuard: func() (bool, string) { return true, "BLOCKED: memory used 80%" },
		GoTest:   func(context.Context, string, Task) map[string]bool { ran = true; return nil },
	})
	if res.Verdict != ReplayNotChecked || !strings.Contains(res.Why, "memory before the base run: BLOCKED") || ran {
		t.Errorf("replay = %+v (go test ran: %v), want not checked for memory", res, ran)
	}
}

// TestReplayRemovesWhatTheTestsLeaveInTMPDIR: a hidden test that leaves a
// folder in the temp dir leaves nothing once the replay is done.
func TestReplayRemovesWhatTheTestsLeaveInTMPDIR(t *testing.T) {
	r := newReplayRepo(t)
	leaky := "package calc\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestAddAdds(t *testing.T) {\n\tif _, err := os.MkdirTemp(\"\", \"left-behind-\"); err != nil {\n\t\tt.Fatal(err)\n\t}\n\tif Add(2, 3) != 5 {\n\t\tt.Fatal(\"2+3\")\n\t}\n}\n"
	merged := r.commit("fix add", map[string]string{"calc.go": fixed, "add_test.go": leaky})
	outer := t.TempDir()
	t.Setenv("TMPDIR", outer)
	if res := r.replay(merged, ReplayEnv{}); res.Verdict != ReplaySolvable {
		t.Errorf("replay = %+v, want solvable", res)
	}
	if left, _ := os.ReadDir(outer); len(left) != 0 {
		t.Errorf("the replayed tests left %d entries in TMPDIR", len(left))
	}
}
