package proof

import (
	"fmt"
	"os"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/timeline"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// RefsKey is the commit-message line that names the issue a commit belongs
// to when its subject doesn't: "Refs: <slug>".
const RefsKey = "Refs"

// Reference finds the work branch's own commit that references issue: its
// subject names the slug (chore(merge): main into <slug>) or a Refs: line
// does. The branch's commits are the ones the proof links read
// (branchCommits), so a commit merged in from main never counts. refName
// is the branch the walk read, "" when the issue has none.
func Reference(root string, issue *tracker.Issue) (sha, refName string, err error) {
	if root == "" {
		root, _ = os.Getwd()
	}
	stats, _ := tracker.LoadStats(issue.FilePath)
	ref, refName := timeline.GitRef(root, issue.Slug, stats.Transitions)
	c := &checker{root: root, ref: ref, slug: issue.Slug, issuesDir: issuesRoot(issue), statsPath: tracker.StatsSidecarPath(issue.FilePath)}
	commits, err := c.branchCommits(stats)
	if err != nil {
		return "", refName, err
	}
	for _, cm := range commits {
		if NamesSlug(cm.subject, issue.Slug) {
			return cm.sha, refName, nil
		}
		for _, v := range cm.refs {
			if NamesSlug(v, issue.Slug) {
				return cm.sha, refName, nil
			}
		}
	}
	return "", refName, nil
}

// ReferenceError is the commits_reference_issue check: nil when a commit
// references the issue, else what to commit. The fixes add a commit and
// never reword one, since Proves: trailers pin the shas they sit on.
func ReferenceError(root string, issue *tracker.Issue) error {
	sha, refName, err := Reference(root, issue)
	if refName == "" {
		return fmt.Errorf("no branch work/%s, recorded branch head or merge sha: can't find the commits that should reference the issue", issue.Slug)
	}
	if err != nil {
		return fmt.Errorf("git log on %s: %v", refName, err)
	}
	if sha != "" {
		return nil
	}
	return fmt.Errorf("no commit on %s references the issue: name the slug in a commit subject or add a %s: line. Add a commit, don't reword one (Proves: trailers pin their shas):\n\n"+
		"  git merge main -m \"chore(merge): main into %s\"   (when main has moved)\n"+
		"  git commit --allow-empty -m \"chore: reference the issue\" -m \"%s: %s\"",
		refName, RefsKey, issue.Slug, RefsKey, issue.Slug)
}

// NamesSlug reports whether text holds slug as a whole word: the slug
// "sys/foo" is in "main into sys/foo." but not in "sys/foo-bar".
func NamesSlug(text, slug string) bool {
	if slug == "" {
		return false
	}
	for from := 0; ; {
		i := strings.Index(text[from:], slug)
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len(slug)
		if (start == 0 || !slugByte(text[start-1])) && (end == len(text) || !slugByte(text[end])) {
			return true
		}
		from = start + 1
	}
}

func slugByte(b byte) bool {
	return b == '-' || b == '_' || b == '/' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}
