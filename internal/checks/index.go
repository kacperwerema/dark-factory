package checks

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kacperwerema/dark-factory/internal/recent"
)

// racyWindow is how old a script's mtime must be before its header is kept
// by its stamp: a file written within it may change again in the same tick
// with the same size, so it is read again next time.
const racyWindow = 2 * time.Second

// scriptIndex keeps, per scripts folder, every *.sh in it with the slug its
// header names, keyed by the file's stamp (size, mtime). A pass over a board
// asks each folder once per issue (the waiting-on-you count runs Due for
// every issue in owner-test, across every worktree): without it each ask
// read every script again. Each ask still lists the folder and stats its
// files, so an added, removed or edited script is seen at once; in the
// viewer a listing stands for one page (listScripts).
var scriptIndex = struct {
	sync.Mutex
	dirs map[string]map[string]indexedScript // folder → file name → entry
}{dirs: map[string]map[string]indexedScript{}}

// maxIndexedDirs bounds the index; past it, it starts over.
const maxIndexedDirs = 4096

type indexedScript struct {
	size  int64
	mtime time.Time
	slug  string
}

// scriptReads counts the reads of each script file for its header.
var scriptReads sync.Map // path → *atomic.Int64

// ScriptReads is how many times each script under dir has been read for its
// header in this process: what a test compares across requests.
func ScriptReads(dir string) map[string]int64 {
	dir = filepath.Clean(dir) + string(filepath.Separator)
	out := map[string]int64{}
	scriptReads.Range(func(k, v any) bool {
		if path := k.(string); strings.HasPrefix(path, dir) {
			out[path] = v.(*atomic.Int64).Load()
		}
		return true
	})
	return out
}

func countRead(path string) {
	n, _ := scriptReads.LoadOrStore(path, new(atomic.Int64))
	n.(*atomic.Int64).Add(1)
}

// listed is one *.sh in a scripts folder with the slug its header names.
type listed struct {
	path, name, slug string
}

// listScripts is every scripts/checks/*.sh under root, sorted by path, with
// the slug of its header: read from the file only when its stamp is new.
// In the viewer a folder's listing stands for one page (recent.Keep): the
// page's parts and its pass over the board list each folder once.
func listScripts(root string) []listed {
	dir := filepath.Join(root, Dir)
	return recentLists.Get(dir, func() []listed { return scanScripts(dir) })
}

var recentLists recent.Readings[string, []listed]

func scanScripts(dir string) []listed {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	scriptIndex.Lock()
	old := scriptIndex.dirs[dir]
	scriptIndex.Unlock()
	now := time.Now()
	fresh := make(map[string]indexedScript, len(entries))
	var out []listed
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sh") {
			continue
		}
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		entry, ok := old[name]
		if !ok || entry.size != info.Size() || !entry.mtime.Equal(info.ModTime()) || now.Sub(info.ModTime()) < racyWindow {
			countRead(path)
			entry = indexedScript{size: info.Size(), mtime: info.ModTime(), slug: IssueOf(path)}
		}
		fresh[name] = entry
		out = append(out, listed{path: path, name: strings.TrimSuffix(name, ".sh"), slug: entry.slug})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	scriptIndex.Lock()
	if _, ok := scriptIndex.dirs[dir]; !ok && len(scriptIndex.dirs) >= maxIndexedDirs {
		clear(scriptIndex.dirs)
	}
	scriptIndex.dirs[dir] = fresh
	scriptIndex.Unlock()
	return out
}
