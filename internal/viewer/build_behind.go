package viewer

import (
	"bufio"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The installed build behind main, on the engine project's Status page and
// status.md: this viewer's build (main.Version, `git describe` stamped by
// `make install`) against the project's main. When main is ahead it says by
// how many commits, lists the changelog entries main has that the build's
// changelog lacks, and shows the install command. It never runs it. Nothing
// shows for a "dev" build, a describe git can't resolve, a build at (or
// ahead of) main, or any project but the engine's own.

// BehindRow is the card; nil when the build is not behind.
type BehindRow struct {
	Installed string // the build's describe, "v0.57.0-12-gc08e9d1"
	Sha       string // the installed commit, short
	Branch    string // main, or ci.branch
	Ahead     int
	// Changelog is the changelog's path; Entries main's ## Unreleased
	// bullets the installed commit's changelog does not have.
	Changelog string
	Entries   []string
	// Install is the command, from docs/getting-started.md.
	Install string
}

// gitResolveCommit is the commit rev names, "" when git can't resolve it.
// A var so tests don't run git.
var gitResolveCommit = func(root, rev string) string {
	out, err := exec.Command("git", "-C", root, "rev-parse", "-q", "--verify", rev+"^{commit}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitCountAhead counts the commits on branch that from lacks.
var gitCountAhead = func(root, from, branch string) (int, bool) {
	out, err := exec.Command("git", "-C", root, "rev-list", "--count", from+".."+branch).Output()
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	return n, err == nil
}

// gitShowFile is path at rev, "" when it is not there.
var gitShowFile = func(root, rev, path string) string {
	out, err := exec.Command("git", "-C", root, "show", rev+":"+path).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// buildRev turns a build version into something git resolves: "-dirty"
// dropped (the base commit), "" for a build without one ("dev", unknown).
func buildRev(version string) string {
	v := strings.TrimSpace(version)
	v = strings.TrimSuffix(v, "-dirty")
	if v == "" || v == "dev" || v == "unknown" || strings.ContainsAny(v, " :") || strings.HasPrefix(v, "-") {
		return ""
	}
	return v
}

// buildBehind compares the build version with branch in root; changelog is
// the changelog's path in the repo ("" for CHANGELOG.md).
func buildBehind(root, branch, changelog, version string) *BehindRow {
	rev := buildRev(version)
	if rev == "" || root == "" {
		return nil
	}
	if branch == "" {
		branch = "main"
	}
	sha := gitResolveCommit(root, rev)
	if sha == "" || gitResolveCommit(root, branch) == "" {
		return nil
	}
	n, ok := gitCountAhead(root, sha, branch)
	if !ok || n == 0 {
		return nil
	}
	if changelog == "" {
		changelog = "CHANGELOG.md"
	}
	row := &BehindRow{Installed: strings.TrimSpace(version), Sha: shortSha(sha), Branch: branch, Ahead: n,
		Install: "cd " + root + " && make install"}
	if text := gitShowFile(root, branch, changelog); text != "" {
		row.Changelog = changelog
		row.Entries = newEntries(text, gitShowFile(root, sha, changelog))
	}
	return row
}

// newEntries are the ## Unreleased bullets of text that old has nowhere
// (under Unreleased or a release it was cut into since).
func newEntries(text, old string) []string {
	entries, _ := parseUnreleased(text)
	have := map[string]bool{}
	for _, e := range changelogBullets(old) {
		have[e] = true
	}
	var out []string
	for _, e := range entries {
		if !have[e] {
			out = append(out, e)
		}
	}
	return out
}

// changelogBullets is every top-level bullet of a changelog, clipped as
// parseUnreleased clips them, so the two compare.
func changelogBullets(text string) []string {
	var out []string
	inEntry := false
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* "):
			out = append(out, strings.TrimSpace(line[2:]))
			inEntry = true
		case inEntry && t != "" && line != t && !strings.HasPrefix(t, "- ") && !strings.HasPrefix(t, "* "):
			out[len(out)-1] += " " + t
		default:
			inEntry = false
		}
	}
	for i := range out {
		out[i] = clip(out[i], 140)
	}
	return out
}

// projectBehind is buildBehind for a project: its main (ci.branch when set)
// and its changelog.
func projectBehind(proj *tracker.Project, version string) *BehindRow {
	wf := proj.LoadWorkflow()
	branch := ""
	if wf.CI.Enabled() {
		branch = strings.TrimSpace(wf.CI.Branch)
	}
	return buildBehind(projectRoot(proj), branch, wf.Workbench.Changelog, version)
}

// behindMarkdown is the card in status.md.
func behindMarkdown(b *strings.Builder, r *BehindRow) {
	if r == nil {
		return
	}
	b.WriteString("\n## Installed build behind " + r.Branch + "\n")
	fmt.Fprintf(b, "- installed %s · %s is %s ahead\n", r.Installed, r.Branch, plural(r.Ahead, "commit", "commits"))
	if r.Changelog != "" {
		if len(r.Entries) == 0 {
			fmt.Fprintf(b, "- Nothing new under ## Unreleased in %s since %s\n", r.Changelog, r.Sha)
		} else {
			fmt.Fprintf(b, "- New under ## Unreleased in %s since %s (%d):\n", r.Changelog, r.Sha, len(r.Entries))
			for i, e := range r.Entries {
				if i == capMDList {
					fmt.Fprintf(b, "  - +%d more in %s\n", len(r.Entries)-i, r.Changelog)
					break
				}
				fmt.Fprintf(b, "  - %s\n", e)
			}
		}
	}
	fmt.Fprintf(b, "- To get them: %s, then restart the viewer (the owner's call; nothing runs it for you)\n", r.Install)
}
