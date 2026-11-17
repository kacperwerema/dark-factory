package agentrun

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Release trees: `release cut` adds .worktrees/release-vX.Y on
// release/vX.Y, and the line's patches are cut and published from it. Once
// a newer line is published, nothing comes to the old one but a patch, and
// `release cut` adds the tree back when its folder is missing
// (privacy.Cut), so a sweep removes a release tree when every rule holds:
//   - a vX.Y.N tag exists (the line is published), or the line was never
//     published and a newer one is (it was left for that one);
//   - a newer line is published (the newest keeps its tree for patches);
//   - the branch tip has no commits past the line's newest vX.Y.N (no pick
//     or candidate waiting);
//   - then the work tree rules: nothing live inside, idle, clean.
// The branch stays.

var (
	releaseDirRe = regexp.MustCompile(`^release-v(\d+)\.(\d+)$`)
	publishedRe  = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)
)

// ReleaseTags is the repository's tags that look like versions (v*), from
// one git call; nil when git fails.
func ReleaseTags(root string) []string {
	out, err := exec.Command("git", "-C", root, "tag", "-l", "v*").Output()
	if err != nil {
		return nil
	}
	tags := []string{}
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			tags = append(tags, l)
		}
	}
	return tags
}

// releaseLine is "v0.57" for a tree at .worktrees/release-v0.57 on
// release/v0.57; ok is false for any other tree.
func releaseLine(root string, t Tree) (string, bool) {
	rel, err := filepath.Rel(filepath.Join(root, ".worktrees"), t.Path)
	if err != nil {
		return "", false
	}
	m := releaseDirRe.FindStringSubmatch(rel)
	if m == nil {
		return "", false
	}
	line := "v" + m[1] + "." + m[2]
	if t.Detached || t.Branch != "release/"+line {
		return "", false
	}
	return line, true
}

// judgeRelease is the release rules for line's tree: kind and why when it
// is kept, else gone (the Why of a removal).
func judgeRelease(in PruneInput, t Tree, line string) (gone, kind, why string) {
	newest, newestLine := "", [2]int{-1, -1}
	patch := -1
	for _, tag := range in.Tags {
		m := publishedRe.FindStringSubmatch(tag)
		if m == nil {
			continue
		}
		maj, _ := strconv.Atoi(m[1])
		mnr, _ := strconv.Atoi(m[2])
		p, _ := strconv.Atoi(m[3])
		if maj > newestLine[0] || (maj == newestLine[0] && mnr > newestLine[1]) {
			newestLine = [2]int{maj, mnr}
		}
		if "v"+m[1]+"."+m[2] == line && p > patch {
			patch, newest = p, tag
		}
	}
	top := "v" + strconv.Itoa(newestLine[0]) + "." + strconv.Itoa(newestLine[1])
	if newest == "" {
		if newestLine[0] < 0 || !olderLine(line, newestLine) {
			return "", TreeSpecial, "release " + line + " is not published"
		}
		// Never published, and a newer line is: the line was left for it.
		return line + " was never published and " + top + " is out; clean and idle (the branch stays; release cut adds the tree back)", "", ""
	}
	if line == top {
		return "", TreeSpecial, line + " is the newest published release: a patch may still come"
	}
	head := t.Head
	if head == "" {
		head = t.Branch
	}
	out, err := exec.Command("git", "-C", in.Root, "rev-list", "--count", newest+".."+head).Output()
	if err != nil {
		return "", TreeUnmerged, "git rev-list " + newest + ".." + t.Branch + " failed: " + err.Error()
	}
	if n := strings.TrimSpace(string(out)); n != "0" {
		return "", TreeUnmerged, t.Branch + " has " + n + " commit(s) past " + newest + " (a pick or a candidate waiting)"
	}
	return newest + " is published and " + top + " is out; clean and idle (the branch stays; release cut adds the tree back)", "", ""
}

// olderLine: line ("v0.61") is older than the line newest names.
func olderLine(line string, newest [2]int) bool {
	var maj, mnr int
	if _, err := fmt.Sscanf(line, "v%d.%d", &maj, &mnr); err != nil {
		return false
	}
	return maj < newest[0] || (maj == newest[0] && mnr < newest[1])
}
