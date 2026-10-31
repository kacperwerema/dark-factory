package checks

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// IssueWorktree is the git worktree of the repository at root checked out on
// the issue's branch, work/<slug>, wherever it lives and whatever its folder
// is called, or "" when there is none. Workers keep a worktree even when the
// workflow declares none, and that is where their scripts are.
func IssueWorktree(root, slug string) string {
	if root == "" || slug == "" {
		return ""
	}
	out, err := exec.Command("git", "-C", root, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return ""
	}
	want := "branch refs/heads/work/" + slug
	path := ""
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		line := sc.Text()
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			path = p
		} else if line == want && path != "" {
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
	}
	return ""
}

// MergedInto names the main branch (main, else master) of the repository at
// root when the worktree wt is a stale copy of it: its HEAD is already on
// that branch and it holds no uncommitted change. "" otherwise, also for a
// fresh worktree with a new script not yet committed: that one is in use.
func MergedInto(root, wt string) string {
	if root == "" || wt == "" || resolved(root) == resolved(wt) {
		return ""
	}
	git := func(dir string, args ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
		return strings.TrimSpace(string(out)), err
	}
	for _, main := range []string{"main", "master"} {
		if _, err := git(root, "rev-parse", "--verify", "--quiet", "refs/heads/"+main); err != nil {
			continue
		}
		if _, err := git(wt, "merge-base", "--is-ancestor", "HEAD", "refs/heads/"+main); err != nil {
			return ""
		}
		if status, err := git(wt, "status", "--porcelain"); err != nil || status != "" {
			return ""
		}
		return main
	}
	return ""
}

// WorktreeHolding is the git worktree of the repository at root that holds
// dir, when that isn't root itself, or "". A slug with a space has no
// work/<slug> branch, so a worker on such an issue names its own: the
// worktree it runs check-script from is where its scripts are.
func WorktreeHolding(root, dir string) string {
	if root == "" || dir == "" {
		return ""
	}
	out, err := exec.Command("git", "-C", root, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return ""
	}
	root, dir = resolved(root), resolved(dir)
	best := ""
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		p, ok := strings.CutPrefix(sc.Text(), "worktree ")
		if !ok {
			continue
		}
		p = resolved(p)
		if p == root || len(p) <= len(best) {
			continue
		}
		if dir == p || strings.HasPrefix(dir, p+string(filepath.Separator)) {
			best = p
		}
	}
	return best
}

func resolved(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return filepath.Clean(p)
}

// bashCandidates are where a bash 4 or newer usually is: PATH first, then
// Homebrew (Apple silicon, Intel) and MacPorts, which a stock macOS PATH in an
// agent's shell may miss.
var bashCandidates = []string{"/opt/homebrew/bin/bash", "/usr/local/bin/bash", "/opt/local/bin/bash", "/bin/bash", "/usr/bin/bash"}

// FindBash is a bash 4 or newer to run the scripts with (lib.sh uses
// associative arrays and mapfile): bash on PATH, then the usual install
// folders. The error says which bash it found and how to get a newer one.
func FindBash() (string, error) {
	var candidates []string
	if p, err := exec.LookPath("bash"); err == nil {
		candidates = append(candidates, p)
	}
	return findBash(append(candidates, bashCandidates...))
}

func findBash(candidates []string) (string, error) {
	seen := map[string]bool{}
	var old []string
	for _, p := range candidates {
		if seen[p] {
			continue
		}
		seen[p] = true
		if _, err := os.Stat(p); err != nil {
			continue
		}
		out, err := exec.Command(p, "-c", `echo "${BASH_VERSINFO[0]}"`).Output()
		if err != nil {
			continue
		}
		major := strings.TrimSpace(string(out))
		if n, err := strconv.Atoi(major); err == nil && n >= 4 {
			return p, nil
		}
		old = append(old, fmt.Sprintf("%s is bash %s", p, major))
	}
	found := "no bash found"
	if len(old) > 0 {
		found = strings.Join(old, ", ")
	}
	return "", fmt.Errorf("guided checks need bash 4 or newer (%s; tried PATH and %s).\nInstall one: brew install bash (macOS ships bash 3.2); issue-cli finds it in /opt/homebrew/bin or /usr/local/bin without a PATH change", found, strings.Join(bashCandidates[:3], ", "))
}

// MachineRunPassed reads the newest machine run record of the script name
// under root: ran is false when there is none, ok when no step failed in it
// (steps needing the owner's eyes, or skipped, are not failures).
func MachineRunPassed(root, name string) (ok, ran bool) {
	steps, ran := machineRunSteps(root, name)
	for _, st := range steps {
		if st.Verdict == "fail" {
			return false, true
		}
	}
	return ran, ran
}

// RunStep is one step line of a run record.
type RunStep struct {
	ID, Verdict string
	Boxes       []string
}

// OwnerLeft reports whether a machine run left the step to the owner: it
// needs their eyes, or it was skipped.
func (s RunStep) OwnerLeft() bool { return s.Verdict == "eyes" || s.Verdict == "skip" }

// MachineRunOwnerSteps is the steps the newest machine run of the script
// name under root left to the owner, with the boxes each names.
func MachineRunOwnerSteps(root, name string) []RunStep {
	steps, _ := machineRunSteps(root, name)
	var out []RunStep
	for _, st := range steps {
		if st.OwnerLeft() {
			out = append(out, st)
		}
	}
	return out
}

// machineRunSteps reads the step lines of the newest machine run record
// ("step <id> <verdict> boxes=<a,b> note=…"); ran is false when there is none.
func machineRunSteps(root, name string) (steps []RunStep, ran bool) {
	runs, _ := filepath.Glob(filepath.Join(root, RecordDir, name, "*-machine.txt"))
	if len(runs) == 0 {
		return nil, false
	}
	sort.Strings(runs)
	data, err := os.ReadFile(runs[len(runs)-1])
	if err != nil {
		return []RunStep{{Verdict: "fail"}}, true
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || f[0] != "step" {
			continue
		}
		st := RunStep{ID: f[1], Verdict: f[2]}
		if len(f) > 3 {
			if boxes, ok := strings.CutPrefix(f[3], "boxes="); ok {
				for _, b := range strings.Split(boxes, ",") {
					if b != "" {
						st.Boxes = append(st.Boxes, b)
					}
				}
			}
		}
		if len(st.Boxes) == 0 {
			st.Boxes = []string{st.ID}
		}
		steps = append(steps, st)
	}
	return steps, true
}
