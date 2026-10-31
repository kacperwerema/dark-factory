package bench

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/proof"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// git runs git in dir with a clean identity: no global config (hooks,
// signing, aliases), so commits in a sandbox behave the same on every
// machine.
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv(os.Environ())
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return out.String(), nil
}

// gitEnv is env with git's global config and identity replaced.
func gitEnv(env []string) []string {
	return append(env,
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=bench", "GIT_AUTHOR_EMAIL=bench@localhost",
		"GIT_COMMITTER_NAME=bench", "GIT_COMMITTER_EMAIL=bench@localhost",
		"GIT_TERMINAL_PROMPT=0")
}

// Range is a done issue's work: the commit it started from, the merged
// result, and the branch's own commits' files and size.
type Range struct {
	Base   string
	Merged string
	// Head is the branch's last own commit when it differs from Merged (a
	// merge commit); the branch's commits are read from it.
	Head string
	// Files are the files the branch's own commits changed (merges from main
	// left out), sorted.
	Files []string
	// Lines are the lines those commits added plus removed.
	Lines int
	// Commits is the number of the branch's own commits.
	Commits int
}

// ErrNoRange is returned when an issue's commits can't be found.
var ErrNoRange = errors.New("no recorded branch head")

// FindRange reads the issue's range in repo from its stats sidecar: merged is
// the move to done's merge commit, else the last recorded branch head; base
// is the first parent of the oldest commit on the branch head's first-parent
// line made since the issue's first dispatch or transition.
func FindRange(repo string, stats tracker.StatsStore) (Range, error) {
	var r Range
	head := ""
	for _, t := range stats.Transitions {
		if t.MergeSHA != "" {
			r.Merged = t.MergeSHA
		}
		if t.BranchHead != "" {
			head = t.BranchHead
		}
	}
	if r.Merged == "" {
		r.Merged = head
	}
	if r.Merged == "" {
		return r, ErrNoRange
	}
	var since time.Time
	switch {
	case len(stats.Dispatches) > 0:
		since = stats.Dispatches[0].At
	case len(stats.Transitions) > 0:
		since = stats.Transitions[0].TS
	}
	// The walk starts at the branch head: a merge commit's first parent is
	// main, not the branch.
	walk := head
	if walk == "" {
		walk = r.Merged
	}
	out, err := git(repo, "log", "--first-parent", "--format=%H %P", "--since="+strconv.FormatInt(since.Unix(), 10), walk)
	if err != nil {
		return r, err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return r, fmt.Errorf("no commits on %s since the issue started", short(walk))
	}
	f := strings.Fields(lines[len(lines)-1])
	if len(f) < 2 {
		return r, errors.New("the issue's first commit has no parent")
	}
	r.Base = f[1]
	if head != "" && head != r.Merged {
		r.Head = head
	}
	return r, fillRange(repo, &r)
}

// RangeOf is the range between two given commits.
func RangeOf(repo, base, merged string) (Range, error) {
	r := Range{Base: base, Merged: merged}
	return r, fillRange(repo, &r)
}

func fillRange(repo string, r *Range) error {
	tip := r.Merged
	if r.Head != "" {
		tip = r.Head
	}
	out, err := git(repo, "log", "--first-parent", "--no-merges", "--numstat", "--format=%x1e", r.Base+".."+tip)
	if err != nil {
		return err
	}
	files := map[string]bool{}
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}
		r.Commits++
		for _, l := range strings.Split(rec, "\n") {
			f := strings.SplitN(l, "\t", 3)
			if len(f) != 3 {
				continue
			}
			a, _ := strconv.Atoi(f[0])
			d, _ := strconv.Atoi(f[1])
			r.Lines += a + d
			files[f[2]] = true
		}
	}
	for f := range files {
		r.Files = append(r.Files, f)
	}
	sort.Strings(r.Files)
	return nil
}

// ForeignFiles are the files that differ between base and merged though the
// branch's own commits never changed them: main merged into the branch (or
// under its merge commit) after base. The worker starts at base without
// them, so hidden tests that lean on them may not build.
func ForeignFiles(repo string, r Range) []string {
	out, err := git(repo, "diff", "--name-only", r.Base, r.Merged)
	if err != nil {
		return nil
	}
	own := map[string]bool{}
	for _, f := range r.Files {
		own[f] = true
	}
	var foreign []string
	for _, f := range strings.Fields(out) {
		if !own[f] {
			foreign = append(foreign, f)
		}
	}
	return foreign
}

// HiddenFiles are the range's test files that exist at merged.
func HiddenFiles(repo string, r Range) []string {
	var out []string
	for _, f := range r.Files {
		if !proof.IsTestPath(f) {
			continue
		}
		if _, err := git(repo, "cat-file", "-e", r.Merged+":"+f); err == nil {
			out = append(out, f)
		}
	}
	return out
}

var goTestFunc = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]*)\(\w+ \*testing\.T\)`)

// HiddenTests are the Go test functions the range adds to its hidden files;
// when it adds none (it only changes tests), every test function in them.
func HiddenTests(repo string, r Range, hidden []string) []string {
	var added, all []string
	for _, f := range hidden {
		if !strings.HasSuffix(f, "_test.go") {
			continue
		}
		now, _ := git(repo, "show", r.Merged+":"+f)
		before, _ := git(repo, "show", r.Base+":"+f)
		had := map[string]bool{}
		for _, m := range goTestFunc.FindAllStringSubmatch(before, -1) {
			had[m[1]] = true
		}
		for _, m := range goTestFunc.FindAllStringSubmatch(now, -1) {
			all = append(all, m[1])
			if !had[m[1]] {
				added = append(added, m[1])
			}
		}
	}
	if len(added) > 0 {
		return added
	}
	return all
}

// Snapshot is the issue file as it stood just after it entered status:
// the last version in its tracker's git history at or before that
// transition's time plus two minutes (the move commits with the file). The
// status line is set to status.
func Snapshot(issue *tracker.Issue, stats tracker.StatsStore, status string) ([]byte, error) {
	var at time.Time
	for _, t := range stats.Transitions {
		if strings.EqualFold(t.To, status) {
			at = t.TS
			break
		}
	}
	if at.IsZero() {
		return nil, fmt.Errorf("%s has no recorded move to %q", issue.Slug, status)
	}
	dir, file := filepath.Dir(issue.FilePath), filepath.Base(issue.FilePath)
	rel, err := git(dir, "ls-files", "--full-name", "--", file)
	rel = strings.TrimSpace(rel)
	if err != nil || rel == "" {
		return nil, fmt.Errorf("%s is not in a git repository's history", issue.FilePath)
	}
	top, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	top = strings.TrimSpace(top)
	sha, err := git(top, "rev-list", "-1", "--before="+strconv.FormatInt(at.Add(2*time.Minute).Unix(), 10), "HEAD", "--", rel)
	sha = strings.TrimSpace(sha)
	if err != nil || sha == "" {
		return nil, fmt.Errorf("no commit of %s from before %s", rel, at.Format(time.RFC3339))
	}
	data, err := git(top, "show", sha+":"+rel)
	if err != nil {
		return nil, err
	}
	return SetStatus([]byte(data), status), nil
}

var statusLine = regexp.MustCompile(`(?m)^status:.*$`)

// SetStatus replaces the frontmatter's status line and drops the fields that
// belong to a later point of the issue's life (assignee, approvals, done).
func SetStatus(data []byte, status string) []byte {
	s := string(data)
	rest := ""
	if strings.HasPrefix(s, "---\n") {
		if end := strings.Index(s[4:], "\n---"); end >= 0 {
			front := s[4 : 4+end]
			rest = s[4+end:]
			var keep []string
			for _, l := range strings.Split(front, "\n") {
				key, _, _ := strings.Cut(l, ":")
				switch strings.TrimSpace(key) {
				case "assignee", "human_approval", "approved_for", "queued_approval", "done_at":
					continue
				}
				keep = append(keep, l)
			}
			front = strings.Join(keep, "\n")
			if statusLine.MatchString(front) {
				front = statusLine.ReplaceAllString(front, "status: "+strconv.Quote(status))
			} else {
				front += "\nstatus: " + strconv.Quote(status)
			}
			return []byte("---\n" + front + rest)
		}
	}
	return data
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
