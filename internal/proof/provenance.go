package proof

import (
	"bytes"
	"context"
	"io/fs"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// headRow is one recorded branch head: an issue's work branch at a
// transition, or the live branch now.
type headRow struct {
	at   time.Time
	sha  string
	mine bool
}

// foreignCommits names the commits among commits that another issue's
// branch held first: an other issue recorded a branch head containing the
// commit before this issue did. A branch started on another issue's branch
// before that one reached main has those commits in its own first-parent
// chain, and after the merge the walk of main since the issue began meets
// every issue fast-forwarded meanwhile; their trailers name the other
// issue's criteria. A commit no recorded head holds yet is the branch's own.
func foreignCommits(root, issuesDir, ownStats, ref string, own tracker.StatsStore, commits []commit) map[string]bool {
	if len(commits) == 0 || issuesDir == "" {
		return nil
	}
	oldest := commits[len(commits)-1]
	from := oldest.at.Add(-time.Hour)
	var rows []headRow
	for _, t := range own.Transitions {
		if t.BranchHead != "" {
			rows = append(rows, headRow{at: t.TS, sha: t.BranchHead, mine: true})
		}
	}
	rows = append(rows, headRow{at: time.Now(), sha: ref, mine: true})
	others := false
	_ = filepath.WalkDir(issuesDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != issuesDir && tracker.NotIssueFolder(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".stats.json") || p == ownStats {
			return nil
		}
		st, err := tracker.LoadStats(strings.TrimSuffix(p, ".stats.json") + ".md")
		if err != nil {
			return nil
		}
		for _, t := range st.Transitions {
			if t.BranchHead != "" && !t.TS.Before(from) {
				rows = append(rows, headRow{at: t.TS, sha: t.BranchHead})
				others = true
			}
		}
		return nil
	})
	if !others {
		return nil
	}
	// Mine first on a tie, so a head both issues recorded at once stays ours.
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].at.Equal(rows[j].at) {
			return rows[i].mine && !rows[j].mine
		}
		return rows[i].at.Before(rows[j].at)
	})

	// One walk of every head down to the oldest commit's parents gives the
	// graph the ownership is read from.
	var in strings.Builder
	seen := map[string]bool{}
	for _, r := range rows {
		if !seen[r.sha] {
			seen[r.sha] = true
			in.WriteString(r.sha + "\n")
		}
	}
	for _, p := range oldest.parents {
		in.WriteString("^" + p + "\n")
	}
	ctx, cancel := context.WithTimeout(context.Background(), tracker.GitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "rev-list", "--parents", "--ignore-missing", "--stdin")
	cmd.Stdin = strings.NewReader(in.String())
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	parents := map[string][]string{}
	for _, line := range bytes.Split(out, []byte("\n")) {
		if f := strings.Fields(string(line)); len(f) > 0 {
			parents[f[0]] = f[1:]
		}
	}

	// Rows in time order: the first head that reaches a commit owns it, and
	// everything below an owned commit is owned already.
	todo := map[string]bool{}
	for _, c := range commits {
		todo[c.sha] = true
	}
	owner := map[string]bool{} // sha → mine
	for _, r := range rows {
		if len(todo) == 0 {
			break
		}
		stack := []string{r.sha}
		for len(stack) > 0 {
			s := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			ps, ok := parents[s]
			if _, owned := owner[s]; !ok || owned {
				continue
			}
			owner[s] = r.mine
			delete(todo, s)
			stack = append(stack, ps...)
		}
	}
	foreign := map[string]bool{}
	for _, c := range commits {
		if mine, ok := owner[c.sha]; ok && !mine {
			foreign[c.sha] = true
		}
	}
	return foreign
}

// issuesRoot is the issues directory the issue file sits in: one folder up
// per folder in the slug (Sys/prove-me.md has the slug sys/prove-me).
func issuesRoot(issue *tracker.Issue) string {
	if issue.FilePath == "" {
		return ""
	}
	dir := filepath.Dir(issue.FilePath)
	for i := strings.Count(issue.Slug, "/"); i > 0; i-- {
		dir = filepath.Dir(dir)
	}
	return dir
}
