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
	runs, _ := filepath.Glob(filepath.Join(root, RecordDir, name, "*-machine.txt"))
	if len(runs) == 0 {
		return false, false
	}
	sort.Strings(runs)
	data, err := os.ReadFile(runs[len(runs)-1])
	if err != nil {
		return false, true
	}
	for _, line := range strings.Split(string(data), "\n") {
		if f := strings.Fields(line); len(f) >= 3 && f[0] == "step" && f[2] == "fail" {
			return false, true
		}
	}
	return true, true
}
