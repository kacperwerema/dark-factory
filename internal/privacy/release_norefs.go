package privacy

import (
	"strings"

	"github.com/kacperwerema/dark-factory/internal/checks"
	"github.com/kacperwerema/dark-factory/internal/gitdir"
)

// NoRefsCommit is a commit on main that release pick <slug> never finds:
// it has no Refs: line.
type NoRefsCommit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
}

// Short is its short sha.
func (c NoRefsCommit) Short() string { return gitdir.ShortSHA(c.SHA) }

// PickCommand is the command that picks it by sha.
func (c NoRefsCommit) PickCommand() string { return "issue-cli release pick " + gitdir.ShortSHA(c.SHA) }

// NoRefs is mainBranch's non-merge commits since branch's point, oldest
// first, that branch has neither by patch nor by a "(cherry picked from
// commit <sha>)" line (cherryNew) and that carry no Refs: line in any
// paragraph: release pick <slug> finds an issue's commits by that line, so
// these are picked only by sha. Commits that change only the changelog,
// guided check scripts (checks.Dir) or both are left out: the changelog's
// lines come with each pick, and a check script never ships.
func NoRefs(root, branch, mainBranch, changelog string) ([]NoRefsCommit, error) {
	if changelog == "" {
		changelog = "CHANGELOG.md"
	}
	lacking, err := cherrySet(root, branch, mainBranch)
	if err != nil || len(lacking) == 0 {
		return nil, err
	}
	out, err := git(root, "log", "--reverse", "--no-merges", "--name-only",
		"--format=%x00%H%x1f%s%x1f%B%x1e", branch+".."+mainBranch)
	if err != nil {
		return nil, err
	}
	var res []NoRefsCommit
	for _, rec := range strings.Split(out, "\x00") {
		head, files, ok := strings.Cut(rec, "\x1e")
		if !ok {
			continue
		}
		f := strings.SplitN(head, "\x1f", 3)
		if len(f) != 3 || !lacking[f[0]] || len(refsLines(f[2])) > 0 || onlyChangelogOrChecks(lines(files), changelog) {
			continue
		}
		res = append(res, NoRefsCommit{SHA: f[0], Subject: f[1]})
	}
	return res, nil
}

// onlyChangelogOrChecks is whether files are all the changelog or guided
// check scripts (an empty list counts: the commit ships nothing).
func onlyChangelogOrChecks(files []string, changelog string) bool {
	for _, f := range files {
		if f != changelog && !strings.HasPrefix(f, checks.Dir+"/") {
			return false
		}
	}
	return true
}
