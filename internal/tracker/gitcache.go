package tracker

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/kacperwerema/dark-factory/internal/gitdir"
)

// The cache of git calls whose arguments name commits by sha. Their output
// never changes, so it is kept in memory for the life of the process and,
// for merge-base, rev-list and small diffs, on disk in the repo's git common
// dir (gitCacheFile), so the next issue-cli call starts warm. A merge-base
// that found no common ancestor (exit 1, nothing on stderr) is an answer too
// and is cached; other failures (a timeout, a missing object) are not.
//
// The file is append-only JSON lines, one record per answer, written with
// O_APPEND so processes running side by side don't lose each other's lines;
// a torn or unknown line is skipped on load. Past gitCacheFileMax bytes it
// is dropped and starts over.

// gitCacheFile is the disk cache's name inside the git common dir.
const gitCacheFile = "issue-cli-gitcache.jsonl"

const (
	gitCacheMax        = 20000
	gitCacheBytesMax   = 64 << 20 // a log of a merged issue can be 100s of KB
	gitCacheFileMax    = 16 << 20
	gitCachePersistMax = 64 << 10 // larger outputs stay in memory only
)

type gitAnswer struct {
	out     string
	noMerge bool // exit 1 that is an answer (gitNoAnswer)
}

var gitCache = struct {
	sync.Mutex
	m     map[string]gitAnswer
	bytes int                 // of the answers in m
	disks map[string]*gitDisk // root → its repo's disk cache (nil: none)
}{m: map[string]gitAnswer{}, disks: map[string]*gitDisk{}}

// gitDisk is one repo's disk cache, loaded once per process.
type gitDisk struct {
	mu   sync.Mutex
	path string
	m    map[string]gitAnswer
	f    *os.File
}

type gitDiskLine struct {
	K string `json:"k"`
	O string `json:"o,omitempty"`
	N bool   `json:"n,omitempty"`
}

// gitCached is runGit for calls keyed only by shas (merge-base, rev-list,
// diff between two commits).
func gitCached(root string, args ...string) (string, error) {
	return gitCachedIn(root, "", args...)
}

// GitCached is the same for the packages that read commits: every revision
// in args must be a commit sha, with any --since absolute, because the
// answer is kept for good. A log, a grep or an ancestor check of shas is
// the same answer on every page.
func GitCached(root string, args ...string) (string, error) {
	return gitCached(root, args...)
}

// GitCachedIn is GitCached for a call fed on stdin. The input is part of
// the key, so it must name commits by sha too.
func GitCachedIn(root, stdin string, args ...string) (string, error) {
	return gitCachedIn(root, stdin, args...)
}

// gitInflight is the cached calls running now: a branch scan runs them side
// by side, and a call already running is waited for, not run again.
var gitInflight = struct {
	sync.Mutex
	m map[string]*gitCall
}{m: map[string]*gitCall{}}

type gitCall struct {
	done chan struct{}
	out  string
	err  error
}

func gitCachedIn(root, stdin string, args ...string) (string, error) {
	key := gitCacheKey(args)
	if stdin != "" {
		key += fmt.Sprintf("\x00%x", sha256.Sum256([]byte(stdin)))
	}
	memKey := root + "\x00" + key
	gitInflight.Lock()
	if c, ok := gitInflight.m[memKey]; ok {
		gitInflight.Unlock()
		<-c.done
		return c.out, c.err
	}
	c := &gitCall{done: make(chan struct{})}
	gitInflight.m[memKey] = c
	gitInflight.Unlock()
	c.out, c.err = gitCachedKey(root, stdin, key, memKey, args)
	gitInflight.Lock()
	delete(gitInflight.m, memKey)
	gitInflight.Unlock()
	close(c.done)
	return c.out, c.err
}

func gitCachedKey(root, stdin, key, memKey string, args []string) (string, error) {
	gitCache.Lock()
	ans, ok := gitCache.m[memKey]
	gitCache.Unlock()
	persist := stdin == "" && gitPersistable(args)
	var disk *gitDisk
	if !ok && persist {
		if disk = gitDiskFor(root); disk != nil {
			ans, ok = disk.get(key)
			if ok {
				gitRemember(memKey, ans)
			}
		}
	}
	if ok {
		return ans.answer(args)
	}

	out, stderr, code, err := runGitIn(root, stdin, args...)
	switch {
	case err == nil:
		ans = gitAnswer{out: out}
	case gitNoAnswer(args[0]) && code == 1 && strings.TrimSpace(stderr) == "":
		ans = gitAnswer{noMerge: true}
	default:
		return "", err
	}
	gitRemember(memKey, ans)
	if persist && disk != nil && len(ans.out) <= gitCachePersistMax {
		disk.put(key, ans)
	}
	return ans.answer(args)
}

// gitNoAnswer names the commands whose exit 1 is a result: no common
// ancestor, not an ancestor, nothing found.
func gitNoAnswer(cmd string) bool { return cmd == "merge-base" || cmd == "grep" }

func (a gitAnswer) answer(args []string) (string, error) {
	if a.noMerge {
		return "", gitExitError(args, "")
	}
	return a.out, nil
}

func gitRemember(memKey string, ans gitAnswer) {
	gitCache.Lock()
	if len(gitCache.m) >= gitCacheMax || gitCache.bytes+len(ans.out) > gitCacheBytesMax {
		gitCache.m = map[string]gitAnswer{}
		gitCache.bytes = 0
	}
	gitCache.m[memKey] = ans
	gitCache.bytes += len(ans.out)
	gitCache.Unlock()
}

// gitCacheKey is the call's arguments; a two-sha merge-base is symmetric, so
// its shas are put in order.
func gitCacheKey(args []string) string {
	if len(args) == 3 && args[0] == "merge-base" && isFullSha(args[1]) && isFullSha(args[2]) && args[2] < args[1] {
		args = []string{args[0], args[2], args[1]}
	}
	return strings.Join(args, "\x00")
}

// gitPersistable reports whether a call's answer may go to disk: merge-base,
// rev-list or diff whose every revision is a full sha (or a range of two).
func gitPersistable(args []string) bool {
	if len(args) < 2 {
		return false
	}
	switch args[0] {
	case "merge-base", "rev-list", "diff":
	default:
		return false
	}
	revs := 0
	for _, a := range args[1:] {
		if a == "--" {
			break
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		from, to, isRange := strings.Cut(a, "..")
		if !isFullSha(from) || (isRange && !isFullSha(to)) {
			return false
		}
		revs++
	}
	return revs > 0
}

func isFullSha(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// gitDiskFor finds root's repo's disk cache, nil when root is not a git
// checkout. Every worktree of a repo shares the one in its common dir.
func gitDiskFor(root string) *gitDisk {
	gitCache.Lock()
	d, ok := gitCache.disks[root]
	gitCache.Unlock()
	if ok {
		return d
	}
	// The files name the shared git dir of a checkout's top folder; git is
	// asked only for anything else (a subdirectory).
	if dir := gitdir.Common(root); dir != "" {
		d = loadGitDisk(filepath.Join(dir, gitCacheFile))
	} else if dir, err := runGit(root, "rev-parse", "--git-common-dir"); err == nil {
		dir = strings.TrimSpace(dir)
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(root, dir)
		}
		d = loadGitDisk(filepath.Join(dir, gitCacheFile))
	}
	gitCache.Lock()
	if prev, ok := gitCache.disks[root]; ok {
		d = prev
	} else {
		gitCache.disks[root] = d
	}
	gitCache.Unlock()
	return d
}

func loadGitDisk(path string) *gitDisk {
	d := &gitDisk{path: path, m: map[string]gitAnswer{}}
	if st, err := os.Stat(path); err == nil && st.Size() > gitCacheFileMax {
		_ = os.Remove(path)
		return d
	}
	f, err := os.Open(path)
	if err != nil {
		return d
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 4*gitCachePersistMax)
	for sc.Scan() {
		var l gitDiskLine
		if json.Unmarshal(sc.Bytes(), &l) != nil || l.K == "" {
			continue
		}
		d.m[l.K] = gitAnswer{out: l.O, noMerge: l.N}
	}
	return d
}

func (d *gitDisk) get(key string) (gitAnswer, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	a, ok := d.m[key]
	return a, ok
}

func (d *gitDisk) put(key string, ans gitAnswer) {
	line, err := json.Marshal(gitDiskLine{K: key, O: ans.out, N: ans.noMerge})
	if err != nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.m[key] = ans
	if d.f == nil {
		if d.f, err = os.OpenFile(d.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644); err != nil {
			d.f = nil
			return
		}
	}
	_, _ = d.f.Write(append(line, '\n')) // one write per line: whole under O_APPEND
}

// resetGitCacheMemory forgets what this process cached, as a new process
// would start: the disk cache is read again on the next call.
func resetGitCacheMemory() {
	gitCache.Lock()
	defer gitCache.Unlock()
	for _, d := range gitCache.disks {
		if d != nil && d.f != nil {
			d.f.Close()
		}
	}
	gitCache.m = map[string]gitAnswer{}
	gitCache.bytes = 0
	gitCache.disks = map[string]*gitDisk{}
}

// gitCalls counts git runs by their arguments, for the tests; nil leaves it off.
var gitCalls struct {
	sync.Mutex
	n map[string]int
}

func countGitCall(args []string) {
	gitCalls.Lock()
	if gitCalls.n != nil && len(args) > 0 {
		gitCalls.n[strings.Join(args, " ")]++
	}
	gitCalls.Unlock()
}
