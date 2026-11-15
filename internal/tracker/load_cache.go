package tracker

import (
	"io/fs"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// racyWindow is how old a file's mtime must be, when the read starts, before
// the parsed result may be kept. A file written in the same clock tick as a
// read can change again with the same size and mtime (git's "racily clean"
// problem), so a file younger than this is read again on the next load.
const racyWindow = 2 * time.Second

// fileStamp is what a stat says about a file: a cached parse stands while
// the file's stamp is the same.
type fileStamp struct {
	size  int64
	mtime time.Time
}

func stampOf(info fs.FileInfo) fileStamp {
	return fileStamp{size: info.Size(), mtime: info.ModTime()}
}

func (s fileStamp) same(o fileStamp) bool {
	return s.size == o.size && s.mtime.Equal(o.mtime)
}

// settled reports whether a read begun at readAt can be trusted for as long
// as the stamp stands (racyWindow).
func (s fileStamp) settled(readAt time.Time) bool {
	return readAt.Sub(s.mtime) > racyWindow
}

// maxFileCache bounds a FileCache in a process that sees many folders.
const maxFileCache = 20000

// FileCache keeps what was made from each file by path, for as long as the
// file's size and mtime stand. A file younger than racyWindow is made again.
type FileCache[T any] struct {
	mu    sync.Mutex
	files map[string]cachedFile[T]
}

type cachedFile[T any] struct {
	stamp fileStamp
	value T
}

// Get is what build returned for the file info describes, kept from an earlier
// call when the file hasn't changed. Stat before calling: a write in between
// leaves an old stamp on new content, which the next Get sees as a change.
// The value is shared between callers, who must not change it.
func (c *FileCache[T]) Get(path string, info fs.FileInfo, build func() (T, error)) (T, error) {
	stamp := stampOf(info)
	c.mu.Lock()
	kept, hit := c.files[path]
	c.mu.Unlock()
	if hit && kept.stamp.same(stamp) {
		return kept.value, nil
	}
	readAt := time.Now()
	value, err := build()
	if err != nil || !stamp.settled(readAt) {
		return value, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.files == nil || len(c.files) >= maxFileCache {
		c.files = map[string]cachedFile[T]{}
	}
	c.files[path] = cachedFile[T]{stamp: stamp, value: value}
	return value, nil
}

// LoadCounts is what this process has parsed so far. The tests that keep a
// page's cost from growing back read it, and a performance audit needs no
// patch of its own.
type LoadCounts struct {
	// BoardWalks counts full walks of an issues folder.
	BoardWalks int64
	// IssueParses counts issue files read and parsed by those walks.
	IssueParses int64
	// FileReads counts issue files read from disk by any walk, filtered or
	// not: a warm one reads none.
	FileReads int64
	// WorkflowParses counts workflow files unmarshalled.
	WorkflowParses int64
	// CommentReads counts LoadComments calls: each reads the file again.
	CommentReads int64
	// BodyRenders counts issue bodies rendered to HTML.
	BodyRenders int64
	// DecisionParses counts bodies with D-n lines parsed by ParseDecisions;
	// Issue.Decisions parses an unchanged file only once.
	DecisionParses int64
}

var loadCounts struct {
	boardWalks, issueParses, fileReads, workflowParses, commentReads, bodyRenders, decisionParses atomic.Int64
}

// ReadLoadCounts returns the counters.
func ReadLoadCounts() LoadCounts {
	return LoadCounts{
		BoardWalks:     loadCounts.boardWalks.Load(),
		IssueParses:    loadCounts.issueParses.Load(),
		FileReads:      loadCounts.fileReads.Load(),
		WorkflowParses: loadCounts.workflowParses.Load(),
		CommentReads:   loadCounts.commentReads.Load(),
		BodyRenders:    loadCounts.bodyRenders.Load(),
		DecisionParses: loadCounts.decisionParses.Load(),
	}
}

// cachedIssue is one parsed file. issue is never handed out: LoadIssues
// gives callers copies, since they set fields such as Slug.
type cachedIssue struct {
	stamp fileStamp
	issue *Issue
	data  []byte // the file: LoadIssuesWhere's keep reads it, never changed
}

// maxCachedBoards bounds the cache in a process that opens many folders
// (a test binary, a benchmark's sandboxes); a viewer has one per project.
const maxCachedBoards = 64

// issueCache holds each folder's parsed files. A folder's map is never
// changed once stored: a load builds a new one, so two loads can run at once
// and the later store simply wins.
var issueCache = struct {
	sync.Mutex
	boards map[string]map[string]cachedIssue
}{boards: map[string]map[string]cachedIssue{}}

func cachedBoard(dir string) map[string]cachedIssue {
	issueCache.Lock()
	defer issueCache.Unlock()
	return issueCache.boards[dir]
}

func storeBoard(dir string, files map[string]cachedIssue) {
	issueCache.Lock()
	defer issueCache.Unlock()
	if _, ok := issueCache.boards[dir]; !ok && len(issueCache.boards) >= maxCachedBoards {
		clear(issueCache.boards)
	}
	issueCache.boards[dir] = files
}

// copyIssue is what a caller gets: the struct and the slices a caller could
// change in place are its own, the strings and the rest are shared.
func copyIssue(src *Issue) *Issue {
	c := *src
	c.Labels = slices.Clone(src.Labels)
	c.ExtraFields = slices.Clone(src.ExtraFields)
	return &c
}

// boardMemo is one load shared by everything that asks during a request.
type boardMemo struct {
	once     sync.Once
	issues   []*Issue
	err      error
	released atomic.Bool
}

// ShareBoardLoad makes every p.LoadIssues on this copy of the project share
// one load until release is called; after that (work that outlives the
// request) it reads the board again. It is for a read-only request: a write
// followed by a load on the same copy would see the board from before it.
func (p *Project) ShareBoardLoad() (release func()) {
	m := &boardMemo{}
	p.board = m
	return func() { m.released.Store(true) }
}

// LoadIssues is the project's board: the load this request shares when one
// is (ShareBoardLoad), else LoadIssues on its issues folder. Each caller gets
// its own copies either way.
func (p *Project) LoadIssues() ([]*Issue, error) {
	m := p.board
	if m == nil || m.released.Load() {
		return LoadIssues(p.IssueDir)
	}
	m.once.Do(func() { m.issues, m.err = LoadIssues(p.IssueDir) })
	if m.err != nil {
		return nil, m.err
	}
	out := make([]*Issue, len(m.issues))
	for i, issue := range m.issues {
		out[i] = copyIssue(issue)
	}
	return out, nil
}

// cachedWorkflow is one parsed workflow file; config is never handed out.
type cachedWorkflow struct {
	stamp  fileStamp
	config *WorkflowConfig
}

// workflowCache holds parsed workflow files by absolute path.
var workflowCache sync.Map

func workflowCacheKey(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}
