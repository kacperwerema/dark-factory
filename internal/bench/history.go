package bench

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/proof"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Reading every done issue's range one git call at a time took 6008 processes
// and 100 s on the live board, so what a range needs comes from a few calls
// fed on stdin instead: the commit graph (loadHistory), the commits' numstats
// (fillRanges), the files (objects), the diffs (foreignFiles). The one-issue
// helpers go through the same code, a batch of one.

// recorded is what an issue's stats sidecar says of its work.
type recorded struct {
	merged, head string
	since        time.Time
}

// recordedOf reads the sidecar: merged is the move to done's merge commit,
// else the last recorded branch head.
func recordedOf(stats tracker.StatsStore) (recorded, error) {
	var rec recorded
	for _, t := range stats.Transitions {
		if t.MergeSHA != "" {
			rec.merged = t.MergeSHA
		}
		if t.BranchHead != "" {
			rec.head = t.BranchHead
		}
	}
	if rec.merged == "" {
		rec.merged = rec.head
	}
	if rec.merged == "" {
		return rec, ErrNoRange
	}
	switch {
	case len(stats.Dispatches) > 0:
		rec.since = stats.Dispatches[0].At
	case len(stats.Transitions) > 0:
		rec.since = stats.Transitions[0].TS
	}
	return rec, nil
}

// walk is where the range's first-parent line starts: the branch head, since
// a merge commit's first parent is main, not the branch.
func (rec recorded) walk() string {
	if rec.head != "" {
		return rec.head
	}
	return rec.merged
}

type commitNode struct {
	date    int64 // committer date, what git's --since compares
	parents []string
}

// history is the commits reachable from some tips, by full hash.
type history map[string]commitNode

// loadHistory is one git call, however many tips. A tip that is not in the
// repository is left out, and rangeOf names it.
func loadHistory(repo string, tips []string) (history, error) {
	h := history{}
	if len(tips) == 0 {
		return h, nil
	}
	out, err := gitIn(repo, strings.Join(tips, "\n")+"\n", "log", "--ignore-missing", "--stdin", "--format=%H %ct %P")
	if err != nil {
		return nil, err
	}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		date, _ := strconv.ParseInt(f[1], 10, 64)
		h[f[0]] = commitNode{date: date, parents: f[2:]}
	}
	return h, nil
}

// rangeOf finds rec's base: the first parent of the oldest commit on the
// first-parent line from rec.walk() made since the issue started. The walk
// ends at the first older commit, as git's --since does. The Range's commits
// are not read yet (fillRanges).
func (h history) rangeOf(rec recorded) (Range, error) {
	r := Range{Merged: rec.merged}
	walk := rec.walk()
	if _, ok := h[walk]; !ok {
		return r, fmt.Errorf("%s is not in this repository", gitdir.ShortSHA(walk))
	}
	oldest := ""
	for sha := walk; ; {
		c, ok := h[sha]
		if !ok || c.date < rec.since.Unix() {
			break
		}
		oldest = sha
		if len(c.parents) == 0 {
			break
		}
		sha = c.parents[0]
	}
	if oldest == "" {
		return r, fmt.Errorf("no commits on %s since the issue started", gitdir.ShortSHA(walk))
	}
	if len(h[oldest].parents) == 0 {
		return r, errors.New("the issue's first commit has no parent")
	}
	r.Base = h[oldest].parents[0]
	if rec.head != "" && rec.head != r.Merged {
		r.Head = rec.head
	}
	return r, nil
}

// own is the range's own commits: its first-parent line down to Base, merges
// left out.
func (h history) own(r Range) []string {
	var out []string
	for sha := r.tip(); sha != r.Base; {
		c, ok := h[sha]
		if !ok {
			break
		}
		if len(c.parents) < 2 {
			out = append(out, sha)
		}
		if len(c.parents) == 0 {
			break
		}
		sha = c.parents[0]
	}
	return out
}

// fillRanges reads every range's commits in one git call. The ranges come
// from rangeOf on h: their commits are on h's first-parent lines.
func fillRanges(repo string, h history, ranges []*Range) error {
	want := map[string]bool{}
	for _, r := range ranges {
		for _, sha := range h.own(*r) {
			want[sha] = true
		}
	}
	if len(want) == 0 {
		return nil
	}
	shas := make([]string, 0, len(want))
	for sha := range want {
		shas = append(shas, sha)
	}
	sort.Strings(shas)
	out, err := gitIn(repo, strings.Join(shas, "\n")+"\n", "log", "--no-walk", "--stdin", "--numstat", "--format=%x1e%H")
	if err != nil {
		return err
	}
	numstat := map[string]string{}
	for _, rec := range strings.Split(out, "\x1e") {
		if sha, rest, ok := strings.Cut(rec, "\n"); ok {
			numstat[sha] = rest
		}
	}
	for _, r := range ranges {
		files := map[string]bool{}
		for _, sha := range h.own(*r) {
			r.addCommit(numstat[sha], files)
		}
		r.setFiles(files)
	}
	return nil
}

// objects reads files out of commits through one `git cat-file --batch`.
type objects struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
}

func openObjects(repo string) *objects {
	cmd := exec.Command("git", "cat-file", "--batch")
	cmd.Dir = repo
	cmd.Env = gitEnv(os.Environ())
	in, err := cmd.StdinPipe()
	if err != nil {
		return &objects{}
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return &objects{}
	}
	if err := cmd.Start(); err != nil {
		return &objects{}
	}
	return &objects{cmd: cmd, in: in, out: bufio.NewReader(out)}
}

func (o *objects) close() {
	if o.cmd != nil {
		o.in.Close()
		o.cmd.Wait()
		o.cmd = nil
	}
}

// read is the file at rev, false when rev has none. A failed read stops the
// process: answers can't be told apart after it, and the rest read as absent.
func (o *objects) read(rev, path string) (string, bool) {
	if o.cmd == nil || strings.Contains(path, "\n") {
		return "", false
	}
	if _, err := io.WriteString(o.in, rev+":"+path+"\n"); err != nil {
		o.close()
		return "", false
	}
	header, err := o.out.ReadString('\n')
	if err != nil {
		o.close()
		return "", false
	}
	f := strings.Fields(header)
	if len(f) != 3 {
		return "", false // "<name> missing"
	}
	size, err := strconv.Atoi(f[2])
	if err != nil {
		return "", false // a name with a space, then "missing"
	}
	data := make([]byte, size+1) // the content and its newline
	if _, err := io.ReadFull(o.out, data); err != nil {
		o.close()
		return "", false
	}
	return string(data[:size]), true
}

// hiddenFiles are the range's test files that exist at merged.
func (o *objects) hiddenFiles(r Range) []string {
	var out []string
	for _, f := range r.Files {
		if !proof.IsTestPath(f) {
			continue
		}
		if _, ok := o.read(r.Merged, f); ok {
			out = append(out, f)
		}
	}
	return out
}

// hiddenTests are the Go test functions the range adds to its hidden files;
// when it adds none (it only changes tests), every test function in them.
func (o *objects) hiddenTests(r Range, hidden []string) []string {
	var added, all []string
	for _, f := range hidden {
		if !strings.HasSuffix(f, "_test.go") {
			continue
		}
		now, _ := o.read(r.Merged, f)
		before, _ := o.read(r.Base, f)
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

var hashLine = regexp.MustCompile(`^[0-9a-f]{40,64}$`)

// foreignFiles is ForeignFiles for each range, in one git call; nil when it
// fails. Base and Merged are full hashes: diff-tree --stdin takes no others.
func foreignFiles(repo string, ranges []Range) [][]string {
	if len(ranges) == 0 {
		return nil
	}
	var in strings.Builder
	for _, r := range ranges {
		fmt.Fprintf(&in, "%s %s\n", r.Merged, r.Base)
	}
	// -M as `git diff` does, so a rename is its one new name. --always prints
	// a range's hash line even when nothing differs: one per input line.
	out, err := gitIn(repo, in.String(), "diff-tree", "--stdin", "-r", "-M", "--name-only", "--always")
	if err != nil {
		return nil
	}
	foreign := make([][]string, len(ranges))
	own := map[string]bool{}
	i := -1
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if hashLine.MatchString(l) {
			if i++; i == len(ranges) {
				break
			}
			own = map[string]bool{}
			for _, f := range ranges[i].Files {
				own[f] = true
			}
			continue
		}
		if i >= 0 && l != "" && !own[l] {
			foreign[i] = append(foreign[i], l)
		}
	}
	return foreign
}
