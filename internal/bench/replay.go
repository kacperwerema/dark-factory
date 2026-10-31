package bench

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The verdicts of a candidate's replay (Replay).
const (
	// ReplaySolvable: base plus the branch's own commits passes the hidden
	// tests, and base alone does not.
	ReplaySolvable = "solvable"
	// ReplayNeedsMain: base plus the own commits fails hidden tests that pass
	// on the merged commit: they lean on code main merged into the branch.
	ReplayNeedsMain = "needs later main"
	// ReplayNoReplay: an own commit does not apply to base.
	ReplayNoReplay = "doesn't replay"
	// ReplayPassOnBase: every hidden test already passes on base, so the
	// tests judge nothing.
	ReplayPassOnBase = "tests pass on base"
	// ReplayNotChecked: the replay could not tell (memory, git, the tests
	// failing on the merged commit itself).
	ReplayNotChecked = "not checked"
)

// ReplayEnv is what a replay needs from the machine; tests replace it.
type ReplayEnv struct {
	// TmpRoot is where the throwaway worktree's folder is made; "" is the
	// system temp dir (TMPDIR). Never a repo's .worktrees/.
	TmpRoot string
	// MemGuard reports whether memory is too high to run go test; nil:
	// never blocked.
	MemGuard func() (blocked bool, msg string)
	// GoTest runs the hidden tests in dir and returns the ones that passed;
	// nil is goHidden with TMPDIR inside the replay's folder, so what the
	// tests leave in it is removed with the worktree.
	GoTest func(ctx context.Context, dir string, t Task) map[string]bool
}

// ReplayResult is one candidate's replay verdict and why.
type ReplayResult struct {
	Verdict string
	Why     string
}

// replayGit is git with the repo's hooks off: a replay checks out and
// cherry-picks in the owner's repository, and its post-checkout or commit
// hooks must not run.
func replayGit(dir string, args ...string) (string, error) {
	return git(dir, append([]string{"-c", "core.hooksPath=/dev/null"}, args...)...)
}

// OwnCommits are the branch's own commits in r, oldest first: first-parent,
// merges from main left out (the commits fillRange counts).
func OwnCommits(repo string, r Range) ([]string, error) {
	tip := r.Merged
	if r.Head != "" {
		tip = r.Head
	}
	out, err := git(repo, "rev-list", "--reverse", "--first-parent", "--no-merges", r.Base+".."+tip)
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// Replay checks whether a candidate can be solved from its base, in a
// throwaway worktree of repo that it removes before returning: base with
// the merged commit's hidden files must fail the hidden tests, and base
// plus the branch's own commits (with the same hidden files) must pass
// them. hidden and tests are the candidate's hidden files and test names.
// Nothing is written outside the worktree; no model is called.
func Replay(ctx context.Context, repo string, r Range, hidden, tests []string, env ReplayEnv) ReplayResult {
	goTest := env.GoTest
	t := Task{Base: r.Base, Merged: r.Merged, Hidden: hidden, HiddenTests: tests}
	if len(tests) == 0 {
		return ReplayResult{ReplayNotChecked, "no hidden Go tests to run"}
	}
	commits, err := OwnCommits(repo, r)
	if err != nil {
		return ReplayResult{ReplayNotChecked, err.Error()}
	}
	root, err := os.MkdirTemp(env.TmpRoot, "awe-bench-replay-")
	if err != nil {
		return ReplayResult{ReplayNotChecked, err.Error()}
	}
	defer os.RemoveAll(root)
	wt := filepath.Join(root, "wt")
	if goTest == nil {
		// The replayed tests' own temp files go with the replay's folder.
		tmp := filepath.Join(root, "tmp")
		if err := os.Mkdir(tmp, 0o700); err != nil {
			return ReplayResult{ReplayNotChecked, err.Error()}
		}
		goEnv := append(os.Environ(), "TMPDIR="+tmp)
		goTest = func(ctx context.Context, dir string, t Task) map[string]bool { return goHiddenEnv(ctx, dir, t, goEnv) }
	}
	if _, err := replayGit(repo, "worktree", "add", "--quiet", "--detach", wt, r.Base); err != nil {
		return ReplayResult{ReplayNotChecked, err.Error()}
	}
	defer func() {
		// Only this replay's own worktree, then prune its record.
		_, _ = replayGit(repo, "worktree", "remove", "--force", wt)
		_, _ = replayGit(repo, "worktree", "prune")
	}()

	run := func(label string) (map[string]bool, string) {
		if env.MemGuard != nil {
			if blocked, msg := env.MemGuard(); blocked {
				return nil, "memory before the " + label + " run: " + msg
			}
		}
		return goTest(ctx, wt, t), ""
	}

	// Base side: what the worker starts from, scored by the merged tests.
	if err := putHidden(repo, wt, t); err != nil {
		return ReplayResult{ReplayNotChecked, err.Error()}
	}
	onBase, stop := run("base")
	if stop != "" {
		return ReplayResult{ReplayNotChecked, stop}
	}
	if len(onBase) == len(tests) {
		return ReplayResult{ReplayPassOnBase, fmt.Sprintf("all %d hidden tests pass on base %s without the change", len(tests), short(r.Base))}
	}

	// Replay side: base plus the branch's own commits.
	if _, err := replayGit(wt, "reset", "--quiet", "--hard", r.Base); err != nil {
		return ReplayResult{ReplayNotChecked, err.Error()}
	}
	if _, err := replayGit(wt, "clean", "-fdq"); err != nil {
		return ReplayResult{ReplayNotChecked, err.Error()}
	}
	for _, c := range commits {
		if _, err := replayGit(wt, "cherry-pick", "--no-commit", c); err != nil {
			conflicts, _ := replayGit(wt, "diff", "--name-only", "--diff-filter=U")
			subject, _ := git(repo, "log", "-1", "--format=%s", c)
			why := fmt.Sprintf("commit %s (%s) does not apply on base %s", short(c), strings.TrimSpace(subject), short(r.Base))
			if f := strings.Fields(conflicts); len(f) > 0 {
				why += ": conflicts in " + listSome(f, 3)
			}
			return ReplayResult{ReplayNoReplay, why}
		}
	}
	if err := putHidden(repo, wt, t); err != nil {
		return ReplayResult{ReplayNotChecked, err.Error()}
	}
	onReplay, stop := run("replay")
	if stop != "" {
		return ReplayResult{ReplayNotChecked, stop}
	}
	if len(onReplay) == len(tests) {
		why := fmt.Sprintf("%d hidden tests fail on base and pass with its %d own commits", len(tests)-len(onBase), len(commits))
		if len(onBase) > 0 {
			why += fmt.Sprintf("; %s already pass on base and judge nothing", listSome(passedNames(tests, onBase, true), 3))
		}
		return ReplayResult{ReplaySolvable, why}
	}
	failing := passedNames(tests, onReplay, false)

	// Do they pass on the merged commit? Then main's merged-in code is the
	// difference.
	if _, err := replayGit(wt, "reset", "--quiet", "--hard", r.Merged); err != nil {
		return ReplayResult{ReplayNotChecked, err.Error()}
	}
	if _, err := replayGit(wt, "clean", "-fdq"); err != nil {
		return ReplayResult{ReplayNotChecked, err.Error()}
	}
	onMerged, stop := run("merged")
	if stop != "" {
		return ReplayResult{ReplayNotChecked, stop}
	}
	var stillFail []string
	for _, n := range failing {
		if !onMerged[n] {
			stillFail = append(stillFail, n)
		}
	}
	if len(stillFail) > 0 {
		return ReplayResult{ReplayNotChecked, fmt.Sprintf("%s fail on the merged commit %s too", listSome(stillFail, 3), short(r.Merged))}
	}
	why := fmt.Sprintf("%s fail on base + its own commits but pass on the merged commit", listSome(failing, 3))
	if n := len(ForeignFiles(repo, r)); n > 0 {
		why += fmt.Sprintf(" (main merged in %d files not its own)", n)
	}
	return ReplayResult{ReplayNeedsMain, why}
}

// putHidden writes the merged commit's hidden files into dir, as scoring
// does; a hidden file merged lacks is removed.
func putHidden(repo, dir string, t Task) error {
	for _, h := range t.Hidden {
		dst := filepath.Join(dir, filepath.FromSlash(h))
		data, err := git(repo, "show", t.Merged+":"+h)
		if err != nil {
			_ = os.Remove(dst)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, []byte(data), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// passedNames are the tests (in their order) that passed, or with pass
// false the ones that did not.
func passedNames(tests []string, passed map[string]bool, pass bool) []string {
	var out []string
	for _, n := range tests {
		if passed[n] == pass {
			out = append(out, n)
		}
	}
	return out
}

// listSome is up to n names joined, with "and N more".
func listSome(names []string, n int) string {
	if len(names) <= n {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:n], ", "), len(names)-n)
}
