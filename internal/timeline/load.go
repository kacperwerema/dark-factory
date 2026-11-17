package timeline

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/internal/clilog"
	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/playtestruns"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// ProjectRoot is the project's checkout: its workdir, else the folder that
// holds its issues folder, else the current directory.
func ProjectRoot(proj *tracker.Project) string {
	if proj != nil && proj.WorkDir != "" {
		return proj.WorkDir
	}
	if proj != nil && proj.IssueDir != "" {
		if abs, err := filepath.Abs(proj.IssueDir); err == nil {
			return filepath.Dir(abs)
		}
		return filepath.Dir(proj.IssueDir)
	}
	cwd, _ := os.Getwd()
	return cwd
}

// WorkDir is where the project's .agent-logs are: its workdir, else the
// current directory.
func WorkDir(proj *tracker.Project) string {
	if proj != nil && proj.WorkDir != "" {
		return proj.WorkDir
	}
	wd, _ := os.Getwd()
	return wd
}

// Cache keeps what Load and proof.Collect read from files many issues
// share: the clilogs, the playtest runs' results and the playtest sessions,
// read once per Cache rather than once per issue. It lives for one pass: the
// aggregate, or one page's request, which reads its issues side by side. A
// nil *Cache reads from disk on every call.
type Cache struct {
	mu       sync.Mutex
	recs     map[string][]clilog.Record
	runs     map[string][]playtestResultsFile
	sessions map[string][]tracker.PlaytestSessionFile
	heads    map[string][]RecordedHead
}

func NewCache() *Cache {
	return &Cache{recs: map[string][]clilog.Record{}, runs: map[string][]playtestResultsFile{}, sessions: map[string][]tracker.PlaytestSessionFile{}, heads: map[string][]RecordedHead{}}
}

func (c *Cache) read(path string, visit func(clilog.Record)) {
	if c == nil {
		clilog.Read(path, visit)
		return
	}
	c.mu.Lock()
	recs, ok := c.recs[path]
	if !ok {
		clilog.Read(path, func(r clilog.Record) { recs = append(recs, r) })
		c.recs[path] = recs
	}
	c.mu.Unlock()
	for _, r := range recs {
		visit(r)
	}
}

// results are the playtest results.yaml files in dir's .playtest/runs.
func (c *Cache) results(dir string) []playtestResultsFile {
	if c != nil {
		c.mu.Lock()
		defer c.mu.Unlock()
		if r, ok := c.runs[dir]; ok {
			return r
		}
	}
	out := resultsIf(dir, nil)
	if c != nil {
		c.runs[dir] = out
	}
	return out
}

// resultsIf are the results.yaml files in dir's .playtest/runs, leaving out
// unparsed each file not yet parsed whose text keep refuses (nil keeps all).
func resultsIf(dir string, keep func(data []byte) bool) []playtestResultsFile {
	var out []playtestResultsFile
	files, _ := filepath.Glob(filepath.Join(dir, ".playtest", "runs", "*", "*", "results.yaml"))
	for _, f := range files {
		if r, ok := readResults(f, keep); ok {
			out = append(out, r)
		}
	}
	return out
}

// PlaytestSessions is tracker.IssuePlaytestSessions, each folder read once
// per Cache.
func (c *Cache) PlaytestSessions(root string, slugs ...string) []tracker.PlaytestSessionFile {
	if c == nil {
		return tracker.IssuePlaytestSessions(root, slugs...)
	}
	return tracker.IssuePlaytestSessionsIn(func(dir string) []tracker.PlaytestSessionFile {
		c.mu.Lock()
		defer c.mu.Unlock()
		s, ok := c.sessions[dir]
		if !ok {
			s = tracker.PlaytestSessionFiles(dir)
			c.sessions[dir] = s
		}
		return s
	}, root, slugs...)
}

// RecordedHead is a branch head an issue's stats sidecar recorded at a
// transition.
type RecordedHead struct {
	Stats string // the sidecar's path
	At    time.Time
	SHA   string
}

// RecordedHeads is every branch head the stats sidecars under issuesDir
// recorded, read once per Cache.
func (c *Cache) RecordedHeads(issuesDir string) []RecordedHead {
	if c != nil {
		c.mu.Lock()
		defer c.mu.Unlock()
		if h, ok := c.heads[issuesDir]; ok {
			return h
		}
	}
	var out []RecordedHead
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
		if !strings.HasSuffix(p, ".stats.json") {
			return nil
		}
		st, err := tracker.LoadStats(strings.TrimSuffix(p, ".stats.json") + ".md")
		if err != nil {
			return nil
		}
		for _, t := range st.Transitions {
			if t.BranchHead != "" {
				out = append(out, RecordedHead{Stats: p, At: t.TS, SHA: t.BranchHead})
			}
		}
		return nil
	})
	if c != nil {
		c.heads[issuesDir] = out
	}
	return out
}

// Load reads the issue's stats sidecar, clilogs, work branch and playtest
// runs. A source that can't be read is listed in Missing; it never fails.
// cache may be nil.
func Load(proj *tracker.Project, issue *tracker.Issue, cache *Cache) Sources {
	root := ProjectRoot(proj)
	workDir := WorkDir(proj)
	worktree := filepath.Join(root, ".worktrees", issue.Slug)
	projSlug := ""
	if proj != nil {
		projSlug = proj.Slug
	}
	var src Sources

	stats, statsErr := tracker.LoadStats(issue.FilePath)
	switch {
	case statsErr != nil:
		src.Missing = append(src.Missing, Missing{"Stats sidecar", statsErr.Error() + ". Status times and approvals are not shown."})
	case len(stats.Transitions) == 0:
		src.Missing = append(src.Missing, Missing{"Stats sidecar", "no transitions recorded in " + tracker.StatsSidecarPath(issue.FilePath) + ". Status times and approvals are not shown."})
	default:
		src.Read = append(src.Read, "stats sidecar ("+plural(len(stats.Transitions), "transition", "transitions")+", "+plural(countApprovals(stats.Transitions), "approval", "approvals")+")")
	}
	src.Transitions = stats.Transitions
	for _, d := range stats.Dispatches {
		src.Dispatches = append(src.Dispatches, d.At)
	}
	src.Model = tracker.WorkerModel(stats, issue.Slug)
	src.RedRounds = tracker.RedRoundCount(stats.Rounds)
	for i := len(src.Transitions) - 1; i >= 0; i-- {
		if src.Transitions[i].MergeSHA != "" {
			src.MergeSHA = src.Transitions[i].MergeSHA
			break
		}
	}

	src.Calls = LoadCalls(cache, LogPaths(workDir, worktree, issue), projSlug, issue.Slug)
	if len(src.Calls) == 0 {
		src.Missing = append(src.Missing, Missing{"CLI log", "no calls for this issue in .agent-logs/<agent>/<agent>.clilog (project and worktree), .agent-logs/clilog.jsonl or $TMPDIR/issue-cli-logs/actions.jsonl. Agent, waiting and idle time can't be split; the CLI calls filter is empty."})
	} else {
		src.Read = append(src.Read, "CLI log ("+plural(len(src.Calls), "call", "calls")+")")
	}

	since := LifeStart(src)
	ref, refName := GitRef(root, issue.Slug, src.Transitions)
	if ref == "" {
		src.Missing = append(src.Missing, Missing{"Work branch", "work/" + issue.Slug + ": not found, and no branch head or merge sha was recorded for this issue. Commits and CI after merge are not shown."})
	} else if commits, err := LoadCommits(root, ref, since); err != nil {
		src.Missing = append(src.Missing, Missing{"Work branch", refName + ": git log failed (" + err.Error() + "). Commits are not shown."})
	} else {
		src.Commits = commits
		src.Read = append(src.Read, "git ("+plural(len(commits), "commit", "commits")+" on "+refName+")")
	}
	if src.MergeSHA == "" && proj.IssueDone(issue) {
		// A fast-forward merge makes main's commit the branch head itself.
		src.MergeSHA = lastBranchHead(src.Transitions)
		if src.MergeSHA == "" && ref != "" && strings.HasPrefix(refName, "work/") {
			src.MergeSHA = ref
		}
	}

	var problems []string
	src.Runs, problems = LoadPlaytestRuns(cache, issue.Slug, root, worktree)
	for _, p := range problems {
		src.Missing = append(src.Missing, Missing{"Playtest run", p + "; the run is not shown."})
	}
	if len(src.Runs) == 0 {
		src.Read = append(src.Read, "playtest runs (none for this issue)")
	} else {
		src.Read = append(src.Read, "playtest runs ("+strconv.Itoa(len(src.Runs))+")")
	}
	return src
}

func countApprovals(rows []tracker.TransitionStat) int {
	n := 0
	for _, r := range rows {
		if r.ApprovedAt != nil {
			n++
		}
	}
	return n
}

func lastBranchHead(rows []tracker.TransitionStat) string {
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].BranchHead != "" {
			return rows[i].BranchHead
		}
	}
	return ""
}

// LogPaths lists every clilog that may hold the issue's calls. The dispatch
// writes the per-agent log in the worktree under the session name, while
// issue-cli also keeps a copy in the project under the assignee, so both
// places and both names are read. The last two are the per-project log and
// the old $TMPDIR log, which LoadCalls filters down to the issue.
func LogPaths(workDir, worktree string, issue *tracker.Issue) []string {
	names := []string{tracker.AgentSessionName(issue.Slug), "agent-" + filepath.Base(issue.Slug)}
	if issue.Assignee != "" {
		names = append([]string{issue.Assignee}, names...)
	}
	var paths []string
	seen := map[string]bool{}
	for _, dir := range []string{worktree, workDir} {
		for _, n := range names {
			p := clilog.AgentPath(dir, n)
			if dir != "" && !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
	}
	return append(paths, clilog.ProjectPath(workDir), clilog.LegacyPath())
}

// LoadCalls merges the clilogs, keeping per-agent records for this project
// and shared-log records that name this issue, deduped on (ts, args): a call
// can be in both the per-agent and the project log. cache may be nil.
func LoadCalls(cache *Cache, paths []string, projSlug, issueSlug string) []Call {
	var calls []Call
	seen := map[string]bool{}
	for _, p := range paths {
		legacy := p == clilog.LegacyPath()
		shared := legacy || filepath.Base(p) == clilog.FileName
		// The project log holds only this project's calls, many without a
		// --project flag; the legacy $TMPDIR log mixes every project's.
		wantProj := ""
		if legacy {
			wantProj = projSlug
		}
		cache.read(p, func(rec clilog.Record) {
			if shared && !MatchesIssue(rec, wantProj, issueSlug) {
				return
			}
			if p := clilog.FlagValue(rec.Args, "--project"); !shared && p != "" && projSlug != "" && p != projSlug {
				return
			}
			args := clilog.StripGlobal(rec.Args)
			at := rec.Start()
			if len(args) == 0 || at.IsZero() {
				return
			}
			key := rec.TS + "|" + strings.Join(args, "\x00")
			if seen[key] {
				return
			}
			seen[key] = true
			calls = append(calls, Call{Record: rec, At: at, Args: args, Agent: !shared})
		})
	}
	sort.SliceStable(calls, func(i, j int) bool { return calls[i].At.Before(calls[j].At) })
	return calls
}

// MatchesIssue reports whether a shared-log record targets the given project
// (--project matches projSlug, or projSlug is empty) and names issueSlug in
// its positional args. The slug is a positional arg of every issue-targeting
// command (start, show, check, transition, append, comment, retrospective,
// checklist, replace, set-meta).
func MatchesIssue(rec clilog.Record, projSlug, issueSlug string) bool {
	if issueSlug == "" {
		return false
	}
	if projSlug != "" && clilog.FlagValue(rec.Args, "--project") != projSlug {
		return false
	}
	for _, a := range clilog.StripGlobal(rec.Args) {
		if a == issueSlug {
			return true
		}
	}
	return false
}

// GitRef picks where the issue's commits are: the live work branch, else
// the last branch head the stats sidecar recorded, else its merge sha.
func GitRef(root, slug string, rows []tracker.TransitionStat) (ref, name string) {
	branch := "work/" + slug
	if head := tracker.BranchHead(root, branch); head != "" {
		return head, branch
	}
	// Recorded heads come from the stats sidecar, which agents write: only
	// a hex sha is used, so "--output=<file>" never reaches git as an
	// option (docs/API/security.md).
	if h := lastBranchHead(rows); IsSHA(h) {
		return h, "the recorded branch head " + gitdir.ShortSHA(h)
	}
	for i := len(rows) - 1; i >= 0; i-- {
		if IsSHA(rows[i].MergeSHA) {
			return rows[i].MergeSHA, "the merge " + gitdir.ShortSHA(rows[i].MergeSHA)
		}
	}
	return "", ""
}

var shaRe = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// IsSHA reports whether s is a (short or full) hex commit id.
func IsSHA(s string) bool { return shaRe.MatchString(s) }

// LoadCommits walks the first-parent chain from ref, a commit sha, back to
// since, so a merge of main into the branch is one commit and other issues'
// commits that came with it never show.
func LoadCommits(root, ref string, since time.Time) ([]Commit, error) {
	args := []string{"log", "--first-parent", "--format=%H%x1f%P%x1f%ct%x1f%s", ref}
	if !since.IsZero() {
		args = append(args, "--since="+strconv.FormatInt(since.Add(-time.Minute).Unix(), 10))
	}
	out, err := tracker.GitCached(root, args...)
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.Split(line, "\x1f")
		if len(parts) != 4 {
			continue
		}
		sec, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			continue
		}
		at := time.Unix(sec, 0)
		if !since.IsZero() && at.Before(since.Add(-time.Minute)) {
			continue
		}
		commits = append(commits, Commit{
			SHA:     parts[0],
			At:      at,
			Subject: parts[3],
			Merge:   len(strings.Fields(parts[1])) > 1,
		})
	}
	// git lists newest first; reverse rather than sort, so commits made in
	// the same second keep their order.
	for i, j := 0, len(commits)-1; i < j; i, j = i+1, j-1 {
		commits[i], commits[j] = commits[j], commits[i]
	}
	return commits, nil
}

type playtestResultsFile struct {
	Session string         `yaml:"session"`
	Issue   string         `yaml:"issue"`
	Mode    string         `yaml:"mode"`
	Started string         `yaml:"started"`
	Build   string         `yaml:"build"`
	Reduced string         `yaml:"reduced"`
	Steps   []PlaytestStep `yaml:"-"`
	dir     string
	// bad is why the file could not be parsed; Issue is then read from its
	// issue: line, so the issue's pages can say so.
	bad string
}

// resultStep is a step as either runner writes it: ours (cmd/playtest), or
// a game's (Kestrel's runner: judgement:, verdict:, checks: and layout:
// on the step, screenshots: named <step>@<W>x<H>.png beside results.yaml).
type resultStep struct {
	PlaytestStep  `yaml:",inline"`
	GameJudgement bool         `yaml:"judgement"`
	Verdict       string       `yaml:"verdict"`
	Screenshots   []string     `yaml:"screenshots"`
	Checks        []gameCheck  `yaml:"checks"`
	Layout        []gameLayout `yaml:"layout"`
}

type gameCheck struct {
	Kind    string `yaml:"kind"`
	Subject string `yaml:"subject"`
	Passed  bool   `yaml:"passed"`
	Reason  string `yaml:"reason"`
}

// gameLayout is one layout rule at one resolution, passed or not.
type gameLayout struct {
	Resolution string   `yaml:"resolution"`
	Rule       string   `yaml:"rule"`
	Passed     bool     `yaml:"passed"`
	Failures   []string `yaml:"failures"`
}

// step is s in our schema; a step our runner wrote passes through.
func (s resultStep) step() PlaytestStep {
	st := s.PlaytestStep
	st.Judgement = st.Judgement || s.GameJudgement
	if st.Judge == nil && s.Verdict != "" {
		st.Judge = &PlaytestJudge{Status: s.Verdict}
	}
	if len(st.Viewports) == 0 {
		st.Viewports = s.viewports()
	}
	return st
}

var shotSizeRe = regexp.MustCompile(`@(\d+x\d+)\.\w+$`)

// viewports turns a game's step into one viewport per screen size: from the
// @WxH in a shot's name or layout[].resolution. Its checks are not per size,
// so each viewport carries them all. None when it saved no shot and ran no
// layout rule.
func (s resultStep) viewports() []PlaytestViewport {
	var out []PlaytestViewport
	at := map[string]int{}
	size := func(wh string) *PlaytestViewport {
		i, ok := at[wh]
		if !ok {
			i = len(out)
			at[wh] = i
			out = append(out, PlaytestViewport{Viewport: wh, Status: "pass"})
		}
		return &out[i]
	}
	var unsized []string
	for _, f := range s.Screenshots {
		if m := shotSizeRe.FindStringSubmatch(f); m == nil {
			unsized = append(unsized, f)
		} else if v := size(m[1]); v.Screenshot == "" {
			v.Screenshot = f
		}
	}
	for _, l := range s.Layout {
		if l.Resolution == "" {
			continue
		}
		v := size(l.Resolution)
		if l.Passed {
			continue
		}
		v.Status = "fail"
		if len(l.Failures) == 0 {
			v.Layout = append(v.Layout, PlaytestLayout{Rule: l.Rule})
		}
		for _, f := range l.Failures {
			v.Layout = append(v.Layout, PlaytestLayout{Rule: l.Rule, Detail: f})
		}
	}
	// A shot with no size in its name (a playtest note) is the only size's
	// shot, else a view of its own.
	for _, f := range unsized {
		if len(out) == 1 && out[0].Screenshot == "" {
			out[0].Screenshot = f
			continue
		}
		out = append(out, PlaytestViewport{Status: "pass", Screenshot: f})
	}
	for i := range out {
		for _, c := range s.Checks {
			out[i].Checks = append(out[i].Checks, PlaytestCheck{Check: strings.TrimSpace(c.Kind + " " + c.Subject), Pass: c.Passed, Actual: c.Reason})
			if !c.Passed {
				out[i].Status = "fail"
			}
		}
	}
	return out
}

// parseRunStarted reads a run's started:. Ours is RFC 3339; a game's runner
// writes local time with no offset (2026-10-24T11:04:20).
func parseRunStarted(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.ParseInLocation("2006-01-02T15:04:05", s, time.Local)
}

var issueLineRe = regexp.MustCompile(`(?m)^issue:\s*["']?([^"'\s]+)`)

// parsedResults keeps each results.yaml parsed once per process, keyed by
// path and checked against its size and mtime: worktrees hold dozens of runs
// and every issue page reads them for the Evidence label.
var parsedResults = struct {
	sync.Mutex
	m map[string]parsedResult
}{m: map[string]parsedResult{}}

type parsedResult struct {
	size int64
	mod  time.Time
	r    playtestResultsFile
	ok   bool
}

// readResults parses the results.yaml at path, once per size and mtime. A
// file it has to read whose text keep refuses comes back not ok, and is not
// remembered: another caller may want it.
func readResults(path string, keep func(data []byte) bool) (playtestResultsFile, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return playtestResultsFile{}, false
	}
	parsedResults.Lock()
	c, hit := parsedResults.m[path]
	parsedResults.Unlock()
	if hit && c.size == st.Size() && c.mod.Equal(st.ModTime()) {
		return c.r, c.ok
	}
	var raw struct {
		playtestResultsFile `yaml:",inline"`
		Steps               []resultStep `yaml:"steps"`
	}
	data, err := os.ReadFile(path)
	ok := err == nil
	if ok && keep != nil && !keep(data) {
		return playtestResultsFile{}, false
	}
	if ok {
		if err := yaml.Unmarshal(data, &raw); err != nil {
			raw.bad = err.Error()
			if m := issueLineRe.FindSubmatch(data); m != nil && raw.Issue == "" {
				raw.Issue = string(m[1])
			}
		}
	}
	r := raw.playtestResultsFile
	r.Mode = playtestruns.Mode(r.Mode) // shared with the done-time archive
	for _, st := range raw.Steps {
		r.Steps = append(r.Steps, st.step())
	}
	r.dir = filepath.Dir(path)
	parsedResults.Lock()
	parsedResults.m[path] = parsedResult{size: st.Size(), mod: st.ModTime(), r: r, ok: ok}
	parsedResults.Unlock()
	return r, ok
}

// LoadPlaytestRuns reads the results.yaml of every run for issueSlug, in
// each dir's .playtest/runs: agents run ux-test in their worktree, so most
// runs live there. cache may be nil.
//
// A game's runner writes its own schema (started: without an offset, modes
// ux-test / playtest, shots beside results.yaml); its runs read as ours.
// The second result names each run for the issue that could not be read.
//
// Without a cache, a results.yaml whose text doesn't hold issueSlug can't be
// the issue's (its issue: line names it) and is skipped unparsed: the
// checkout holds every issue's runs, and parsing them all to keep a few made
// each prove call slow. A cache reads them all, for the other issues it
// serves.
func LoadPlaytestRuns(cache *Cache, issueSlug string, dirs ...string) ([]PlaytestRun, []string) {
	var runs []PlaytestRun
	var problems []string
	seen := map[string]bool{}
	names := func(data []byte) bool { return bytes.Contains(data, []byte(issueSlug)) }
	for _, d := range dirs {
		var files []playtestResultsFile
		if cache == nil {
			files = resultsIf(d, names)
		} else {
			files = cache.results(d)
		}
		for _, r := range files {
			if r.Issue != issueSlug {
				continue
			}
			name := r.dir
			if rel, err := filepath.Rel(d, r.dir); err == nil {
				name = rel
			}
			if r.bad != "" {
				problems = append(problems, "playtest run "+name+": results.yaml can't be parsed ("+r.bad+")")
				continue
			}
			started, err := parseRunStarted(r.Started)
			if err != nil {
				problems = append(problems, "playtest run "+name+": started: "+strconv.Quote(r.Started)+" is not a date")
				continue
			}
			key := r.Session + "|" + r.Started + "|" + r.Mode
			if seen[key] {
				continue
			}
			seen[key] = true
			runs = append(runs, PlaytestRun{Session: r.Session, Mode: r.Mode, Started: started, Steps: r.Steps, Dir: r.dir, Build: r.Build, Reduced: r.Reduced})
		}
	}
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].Started.Before(runs[j].Started) })
	return runs, problems
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
