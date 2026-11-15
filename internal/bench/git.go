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

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// git runs git in dir with a clean identity: no global config (hooks,
// signing, aliases), so commits in a sandbox behave the same on every
// machine.
func git(dir string, args ...string) (string, error) {
	return gitIn(dir, "", args...)
}

// gitIn is git with stdin to read.
func gitIn(dir, stdin string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv(os.Environ())
	cmd.Stdin = strings.NewReader(stdin)
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
	rec, err := recordedOf(stats)
	if err != nil {
		return Range{}, err
	}
	h, err := loadHistory(repo, []string{rec.walk()})
	if err != nil {
		return Range{Merged: rec.merged}, err
	}
	r, err := h.rangeOf(rec)
	if err != nil {
		return r, err
	}
	return r, fillRanges(repo, h, []*Range{&r})
}

// RangeOf is the range between two given commits.
func RangeOf(repo, base, merged string) (Range, error) {
	r := Range{Base: base, Merged: merged}
	return r, fillRange(repo, &r)
}

func fillRange(repo string, r *Range) error {
	out, err := git(repo, "log", "--first-parent", "--no-merges", "--numstat", "--format=%x1e", r.Base+".."+r.tip())
	if err != nil {
		return err
	}
	files := map[string]bool{}
	for _, rec := range strings.Split(out, "\x1e") {
		r.addCommit(rec, files)
	}
	r.setFiles(files)
	return nil
}

// tip is the range's last own commit.
func (r Range) tip() string {
	if r.Head != "" {
		return r.Head
	}
	return r.Merged
}

// addCommit counts one commit's numstat lines; one with none (an empty
// commit) doesn't count.
func (r *Range) addCommit(numstat string, files map[string]bool) {
	numstat = strings.TrimSpace(numstat)
	if numstat == "" {
		return
	}
	r.Commits++
	for _, l := range strings.Split(numstat, "\n") {
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

func (r *Range) setFiles(files map[string]bool) {
	for f := range files {
		r.Files = append(r.Files, f)
	}
	sort.Strings(r.Files)
}

// ForeignFiles are the files that differ between base and merged though the
// branch's own commits never changed them: main merged into the branch (or
// under its merge commit) after base. The worker starts at base without
// them, so hidden tests that lean on them may not build.
func ForeignFiles(repo string, r Range) []string {
	if all := foreignFiles(repo, []Range{r}); len(all) == 1 {
		return all[0]
	}
	return nil
}

// HiddenFiles are the range's test files that exist at merged.
func HiddenFiles(repo string, r Range) []string {
	o := openObjects(repo)
	defer o.close()
	return o.hiddenFiles(r)
}

var goTestFunc = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]*)\(\w+ \*testing\.T\)`)

// HiddenTests are the Go test functions the range adds to its hidden files;
// when it adds none (it only changes tests), every test function in them.
func HiddenTests(repo string, r Range, hidden []string) []string {
	o := openObjects(repo)
	defer o.close()
	return o.hiddenTests(r, hidden)
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
	data, err := issueFileAt(issue, at)
	if err != nil {
		return nil, err
	}
	return SetStatus(data, status), nil
}

// issueFileAt is the issue file as it stood just after time at: the last
// version in its tracker's git history at or before at plus two minutes (a
// move commits with the file).
func issueFileAt(issue *tracker.Issue, at time.Time) ([]byte, error) {
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
	return []byte(data), nil
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
