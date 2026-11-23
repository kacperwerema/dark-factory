// Command playtest is issue-viewer's own playtest system: an issue's manual
// checks as YAML sessions that drive the board in Chrome (chromedp) against
// a throwaway scenario project.
//
//	playtest check                       validate every session (no browser)
//	playtest ux-test [-issue S] [-s N]   machine checks, headless, every viewport
//	playtest playtest [-issue S] [-s N]  the owner's judgement steps, in an overlay
//	playtest report -issue S [-apply]    tick passed boxes, comment failures
//	playtest shots -list F -base U -out D the README's screenshots (make readme-shots)
//	playtest shot -url U [-w W] [-out F]  one screenshot of one page (make shot)
//	playtest serve -scenario N [-ref R]   a scenario board left running (make serve)
//	playtest stop [-scenario N]           stop what serve started (make serve-stop)
//
// The make targets (playtest-check, ux-test, playtest, playtest-report, shot) wrap
// these. The shared contract behind the format: `issue-cli process playtest`.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kacperwerema/dark-factory/internal/liveboard"
	"github.com/kacperwerema/dark-factory/internal/restrace"
	"github.com/kacperwerema/dark-factory/internal/testresults"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

const usage = `playtest — run issue-viewer playtest sessions (playtest/sessions/*.yaml)

Commands:
  check                        validate every session: schema, actions, checks,
                               targets (internal/testids), scenarios, step ids
  ux-test  [-issue SLUG] [-s SESSION] [-step ID,…] [-repeat N] [-jobs N] [-keep] [-headed] [-results FILE]
                               run the machine checks headless at every viewport;
                               exit 1 on any failure; -repeat N diffs N runs;
                               -jobs N runs N sessions side by side (default 3;
                               1 while mem-guard says BLOCKED or with -headed);
                               -results writes sessions, steps and times as JSON
                               (default $CI_RESULTS_DIR/e2e.json in local CI)
  playtest [-issue SLUG] [-s SESSION]
                               walk the owner through the judgement steps
                               (expect:) in a Chrome window with the overlay
  report   -issue SLUG [-apply]
                               show what the last runs mean for the issue;
                               -apply ticks passed boxes and comments failures
                               and opinions through issue-cli
  shots    -list FILE -base URL -out DIR [-only a.png,b.png]
                               shoot each page in the list at 1280 px, 2x, light
                               theme, into DIR (make readme-shots stages the demo)
  shot     -url URL [-w W] [-h H] [-full] [-scroll SEL] [-out FILE]
                               one screenshot of one page (default 1280x800,
                               .playtest/shot.png); waits for load, not for
                               the network to go idle (make shot); -full on a
                               page that doesn't scroll unrolls its biggest
                               inner scroller, -scroll SEL that one; -url may
                               be a path (/p/demo/company) on the board make
                               serve runs; -ref R also shoots its R board into
                               FILE with .base before the extension; -html also
                               saves each page's rendered HTML for a diff
  serve    -scenario NAME [-ref REF] [-port N] [-dir DIR]
                               stage the scenario as ux-test does (own HOME, XDG
                               dirs, tmux socket) and leave its board running;
                               -ref also builds REF and serves the same scenario
                               on a second board; prints the URLs and the stop
                               command; record in .playtest/serve/NAME.json
  stop     [-scenario NAME] [-keep]
                               stop the boards serve started (every one without
                               -scenario) and remove their folders

Common flags: -sessions DIR, -scenarios DIR, -runs DIR, -project NAME
Runs are kept in .playtest/runs/<session>/<time>-<mode>/ (gitignored).
Contract and guides: issue-cli process playtest
`

func main() {
	stopOnSignal()
	owner := "playtest"
	if len(os.Args) > 1 {
		owner += " " + os.Args[1]
	}
	restrace.SetOwner(owner)
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

type opts struct {
	repo                      string
	sessions, scenarios, runs string
	issue, session, steps     string
	project                   string
	results                   string
	list, base, out, only     string
	url, scroll               string
	scenario, baseRef, dir    string
	repeat, width, height     int
	jobs                      int
	port                      int
	keep, headed, apply, full bool
	html                      bool
}

func run(args []string, out, errw io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(out, usage)
		return 0
	}
	cmd := args[0]
	repo, err := repoRoot()
	if err != nil {
		fmt.Fprintln(errw, "playtest:", err)
		return 2
	}
	o := opts{repo: repo}
	fs := flag.NewFlagSet("playtest "+cmd, flag.ContinueOnError)
	fs.SetOutput(errw)
	fs.StringVar(&o.sessions, "sessions", filepath.Join(repo, "playtest", "sessions"), "session files")
	fs.StringVar(&o.scenarios, "scenarios", filepath.Join(repo, "playtest", "scenarios"), "scenario fixtures")
	fs.StringVar(&o.runs, "runs", filepath.Join(repo, ".playtest", "runs"), "where runs are kept")
	fs.StringVar(&o.issue, "issue", "", "only sessions for this issue slug")
	fs.StringVar(&o.session, "s", "", "only this session")
	fs.StringVar(&o.steps, "step", "", "only these step ids (comma-separated)")
	fs.StringVar(&o.project, "project", "", "check, report: the project of the real issue (default $PLAYTEST_PROJECT, $ISSUE_VIEWER_PROJECT, the configured project whose issues dir is this repo's; else issue-viewer)")
	fs.StringVar(&o.results, "results", resultsDefault(), "ux-test: also write the e2e results (sessions, steps, times) here; default $CI_RESULTS_DIR/e2e.json in local CI")
	fs.IntVar(&o.repeat, "repeat", 1, "run N times and fail if results differ")
	fs.IntVar(&o.jobs, "jobs", defaultJobs, "ux-test: run N sessions side by side, each worker its own Chrome (1 while mem-guard says BLOCKED, or with -headed)")
	fs.BoolVar(&o.keep, "keep", false, "keep scenario temp dirs")
	fs.BoolVar(&o.headed, "headed", false, "show the browser during ux-test")
	fs.BoolVar(&o.apply, "apply", false, "report: tick and comment through issue-cli")
	fs.StringVar(&o.list, "list", "", "shots: the shot list (docs/images/shots.yaml)")
	fs.StringVar(&o.base, "base", "", "shots: the board's URL")
	fs.StringVar(&o.out, "out", "", "shots: where the images go; shot: the PNG file")
	fs.StringVar(&o.url, "url", "", "shot: the page")
	fs.IntVar(&o.width, "w", 0, "shot: viewport width in CSS pixels (default 1280)")
	fs.IntVar(&o.height, "h", 0, "shot: viewport height in CSS pixels (default 800)")
	fs.BoolVar(&o.full, "full", false, "shot: the whole page, not just the viewport")
	fs.StringVar(&o.scroll, "scroll", "", "shot: unroll this scrolling element (CSS selector) and shoot the whole page")
	fs.StringVar(&o.scenario, "scenario", "", "serve, stop, shot: the scenario (playtest/scenarios/<name>)")
	fs.StringVar(&o.baseRef, "ref", "", "serve: also serve the scenario on a board built from this git ref; shot: also shoot that board")
	fs.IntVar(&o.port, "port", 0, "serve: the board's port when it is free (default any free port)")
	fs.StringVar(&o.dir, "dir", "", "serve: a new or empty folder to stage in (default a pt-serve-* temp dir)")
	fs.BoolVar(&o.html, "html", false, "shot: also save the rendered HTML beside each PNG")
	fs.StringVar(&o.only, "only", "", "shots: only these files (comma-separated)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if o.project == "" && (cmd == "check" || cmd == "report") {
		// Both read the real board; report writes to it too.
		o.project = defaultProject(os.Getenv, listProjects, repoIssueDirs(repo))
	}

	switch cmd {
	case "check":
		return cmdCheck(o, out, errw)
	case "ux-test":
		return cmdUXTest(repo, o, out, errw)
	case "playtest":
		return cmdPlaytest(repo, o, out, errw)
	case "report":
		return cmdReport(o, out, errw)
	case "shots":
		return cmdShots(o, out, errw)
	case "shot":
		return cmdShot(repo, o, out, errw)
	case "serve":
		return cmdServe(repo, o, out, errw)
	case "stop":
		return cmdStop(repo, o, out, errw)
	}
	fmt.Fprintf(errw, "playtest: unknown command %q\n\n%s", cmd, usage)
	return 2
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

// repoRoot walks up from the working directory to the go.mod.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("run from inside the dark-factory repo (no go.mod found)")
		}
		dir = parent
	}
}

// loadAll loads and validates every session; it prints problems and returns
// nil when there are any.
func loadAll(o opts, out, errw io.Writer, resolve bool) []*Session {
	all, errs := LoadSessions(o.sessions)
	v := &Validator{ScenarioDir: o.scenarios}
	var open func(string) bool
	if resolve {
		var note string
		v.Checklist, open, note = boardChecklist(o.project, o.repo, listProjects)
		if note != "" {
			fmt.Fprintln(out, "note:", note)
		}
	}
	for _, s := range all {
		errs = append(errs, v.Validate(s)...)
	}
	for _, n := range v.Notes {
		fmt.Fprintln(out, "note:", n)
	}
	if n := UnresolvedNote(v.Unresolved); n != "" {
		fmt.Fprintln(out, "note:", n)
	}
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(errw, e)
		}
		fmt.Fprintf(errw, "✗ %d problem(s) in %s\n", len(errs), o.sessions)
		return nil
	}
	if resolve {
		for _, w := range ProofOverlapWarnings(all, open) {
			fmt.Fprintln(out, "warning:", w)
		}
	}
	return all
}

func cmdCheck(o opts, out, errw io.Writer) int {
	all := loadAll(o, out, errw, true)
	if all == nil {
		return 1
	}
	steps := 0
	for _, s := range all {
		steps += len(s.Steps)
	}
	fmt.Fprintf(out, "✓ %d session(s), %d step(s) valid\n", len(all), steps)
	return 0
}

func selectOrFail(o opts, all []*Session, errw io.Writer) []*Session {
	sel := SelectSessions(all, o.session, o.issue)
	if len(sel) == 0 {
		fmt.Fprintln(errw, noMatch(all, o))
	}
	return sel
}

// noMatch says why no session was selected: the filters used and, when
// the named session exists under another issue, that issue.
func noMatch(all []*Session, o opts) string {
	msg := fmt.Sprintf("playtest: no session matches (issue=%q session=%q) in %s", o.issue, o.session, o.sessions)
	if o.session == "" || o.issue == "" {
		return msg
	}
	for _, s := range all {
		if s.Name == o.session && s.Issue != o.issue {
			return fmt.Sprintf("%s: session %q is for issue %q, not %q; drop ISSUE= or pass ISSUE=%s", msg, s.Name, s.Issue, o.issue, s.Issue)
		}
	}
	return msg
}

func stepFilter(s string) map[string]bool {
	if s == "" {
		return nil
	}
	m := map[string]bool{}
	for _, id := range strings.Split(s, ",") {
		m[strings.TrimSpace(id)] = true
	}
	return m
}

func cmdUXTest(repo string, o opts, out, errw io.Writer) int {
	all := loadAll(o, out, errw, false)
	if all == nil {
		return 1
	}
	sel := selectOrFail(o, all, errw)
	if len(sel) == 0 {
		return 1
	}
	r := &Runner{Repo: repo, ScenarioDir: o.scenarios, RunsRoot: o.runs, Build: BuildID(repo), Keep: o.keep, Out: out, OwnIssue: OwnIssue(repo, o.issue), Named: o.session}
	if o.runs == filepath.Join(repo, ".playtest", "runs") {
		r.EvidenceRuns = EvidenceRunsDir(repo)
	}
	started := time.Now()
	e2e := testresults.Playtest{Started: started}
	sessions := make([]uxSessionRun, len(sel))
	defer func() {
		if o.results == "" {
			return
		}
		e2e.Finished = time.Now()
		for _, s := range sessions {
			e2e.Sessions = append(e2e.Sessions, s.e2e...)
		}
		if err := writeE2E(o.results, e2e); err != nil {
			fmt.Fprintln(errw, "playtest: e2e results:", err)
		}
	}()
	// Built once, before any worker needs them.
	r.bins = uxTestBins
	if _, err := r.binaries(); err != nil {
		fmt.Fprintln(errw, "playtest:", err)
		return 2
	}
	workers, note := uxWorkers(min(o.jobs, len(sel)), o.headed)
	if note != "" {
		fmt.Fprintln(out, note)
	}
	browsers := make([]*Browser, workers)
	defer func() {
		for _, b := range browsers {
			if b != nil {
				b.Close()
			}
		}
	}()
	var printMu sync.Mutex
	var failedSetup atomic.Bool
	runPool(workers, len(sel), memBlockedNot, failedSetup.Load, func(w, i int) {
		if browsers[w] == nil {
			b, err := NewBrowser(o.headed, 1440, 900)
			if err != nil {
				sessions[i].err = err
				failedSetup.Store(true)
				return
			}
			browsers[w] = b
		}
		sessions[i] = r.uxSession(browsers[w], sel[i], o)
		if sessions[i].err != nil {
			failedSetup.Store(true)
		}
		printMu.Lock()
		defer printMu.Unlock()
		_, _ = sessions[i].out.WriteTo(out)
		_, _ = sessions[i].errOut.WriteTo(errw)
	})
	code := 0
	var failed []string
	for _, s := range sessions {
		if s.shotsSkipped {
			r.shotsSkipped = true
		}
		if s.err != nil {
			fmt.Fprintln(errw, "playtest:", s.err)
			code = 2
		}
		if s.failed && code == 0 {
			code = 1
		}
		failed = append(failed, s.failLines...)
	}
	if note := r.ShotsNote(); note != "" {
		fmt.Fprintln(out, note)
	}
	if code == 2 {
		return 2
	}
	took := time.Since(started).Round(time.Second)
	if code == 0 {
		fmt.Fprintf(out, "✓ ux-test passed (%s)\n", uxSummary(len(sel), workers, took))
	} else {
		for _, l := range failed {
			fmt.Fprintln(errw, l)
		}
		fmt.Fprintf(errw, "✗ ux-test failed (%s)\n", uxSummary(len(sel), workers, took))
	}
	return code
}

// uxTestBins, set by a test, skips the build.
var uxTestBins *Binaries

// memBlockedNot: more work may start (mem-guard does not say BLOCKED).
func memBlockedNot() bool { return !memBlocked() }

// uxSessionRun is one session's -repeat runs: their e2e rows, FAIL lines,
// and the output, printed whole once the session ends so parallel sessions
// never interleave their lines.
type uxSessionRun struct {
	e2e          []testresults.PlaytestSession
	failLines    []string
	failed       bool
	shotsSkipped bool
	err          error
	out, errOut  bytes.Buffer
}

// uxSession runs s (-repeat times) on its own copy of r, writing into the
// run's buffers.
func (r *Runner) uxSession(b *Browser, s *Session, o opts) uxSessionRun {
	var run uxSessionRun
	wr := *r
	wr.Out = &run.out
	wr.shotsSkipped = false
	defer func() { run.shotsSkipped = wr.shotsSkipped }()
	var first *Results
	for i := range max(o.repeat, 1) {
		start := time.Now()
		res, dir, err := wr.UXTest(b, s, stepFilter(o.steps))
		if err != nil {
			run.err = err
			return run
		}
		run.e2e = append(run.e2e, e2eSession(res, time.Since(start)))
		fmt.Fprintf(&run.out, "results: %s\n", filepath.Join(dir, "results.yaml"))
		if f := failLines(res); len(f) > 0 {
			run.failed = true
			run.failLines = append(run.failLines, f...)
		}
		if i == 0 {
			first = res
		} else if d := Diff(first, res); d != "" {
			fmt.Fprintf(&run.errOut, "✗ %s: run %d differs from run 1\n%s\n", s.Name, i+1, d)
			run.failed = true
		} else {
			fmt.Fprintf(&run.out, "✓ %s: run %d matches run 1\n", s.Name, i+1)
		}
	}
	return run
}

// failLines is one line per failed step of a run, naming the session and
// the step: "FAIL <session>/<step> <title>". Local CI's failures: regex
// (workflow.yaml, the slow tier) reads the session/step from them, so a red
// nightly run names what went red.
func failLines(res *Results) []string {
	var out []string
	for _, st := range res.Steps {
		if st.Status == StatusFail {
			out = append(out, fmt.Sprintf("FAIL %s/%s %s", res.Session, st.ID, st.Title))
		}
	}
	return out
}

// defaultProject is the issue-cli project for the real issue when -project
// is not given: $PLAYTEST_PROJECT, the dispatched worker's
// $ISSUE_VIEWER_PROJECT, the configured project (issue-cli --json projects)
// whose issue_dir is one of issueDirs, and only then fallbackProject, the
// engine board's old name. A name this machine doesn't have makes every
// issue-cli call fail, so APPLY ticks nothing.
func defaultProject(getenv func(string) string, list func() ([]byte, error), issueDirs []string) string {
	for _, k := range []string{"PLAYTEST_PROJECT", "ISSUE_VIEWER_PROJECT"} {
		if v := strings.TrimSpace(getenv(k)); v != "" {
			return v
		}
	}
	ps := configuredProjects(list)
	for _, d := range issueDirs {
		for _, p := range ps {
			if p.IssueDir != "" && samePath(p.IssueDir, d) {
				return p.Slug
			}
		}
	}
	return fallbackProject
}

// fallbackProject is what defaultProject answers when nothing named a
// project and none matched this repo.
const fallbackProject = "issue-viewer"

// configuredProject is one row of issue-cli --json projects.
type configuredProject struct {
	Slug     string `json:"slug"`
	IssueDir string `json:"issue_dir"`
}

// configuredProjects is what list prints, nil when it can't be read.
func configuredProjects(list func() ([]byte, error)) []configuredProject {
	raw, err := list()
	if err != nil {
		return nil
	}
	var ps []configuredProject
	if json.Unmarshal(raw, &ps) != nil {
		return nil
	}
	return ps
}

func samePath(a, b string) bool {
	clean := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		return filepath.Clean(p)
	}
	return clean(a) == clean(b)
}

// repoIssueDirs is where this repo's board can live: issues/ in this
// checkout and in the primary checkout a worktree belongs to.
func repoIssueDirs(repo string) []string {
	dirs := []string{filepath.Join(repo, "issues")}
	cmd := exec.Command("git", "-C", repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if raw, err := cmd.Output(); err == nil {
		if common := strings.TrimSpace(string(raw)); common != "" {
			dirs = append(dirs, filepath.Join(filepath.Dir(common), "issues"))
		}
	}
	return dirs
}

// listProjects asks issue-cli for the configured projects. The call is the
// tool's, not the worker's: it runs without ISSUE_CLI_LOG, so it stays out
// of the clilog of the agent running make validate. It runs from the
// filesystem root because issue-cli answers a folder with an issues/ as its
// one project: from the primary checkout the configured list never arrives.
func listProjects() ([]byte, error) {
	bin, err := exec.LookPath(issueCLI())
	if err != nil {
		return nil, err
	}
	if bin, err = filepath.Abs(bin); err != nil {
		return nil, err
	}
	cmd := exec.Command(bin, "--json", "projects")
	cmd.Dir = string(filepath.Separator)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "ISSUE_CLI_LOG=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	return cmd.Output()
}

// issueCLI is the real issue-cli, used to read and update the real issue.
func issueCLI() string { return envOr("PLAYTEST_ISSUE_CLI", "issue-cli") }

// boardDir is the board check reads: LIVE_BOARD when set, as for tests,
// then the project's issue_dir, then, when nothing named a project, the
// board beside this repo's git common dir. A project named but not listed
// is an error: it never reads another project's board.
func boardDir(project, repo string, list func() ([]byte, error)) (string, error) {
	if v := os.Getenv(liveboard.Env); v != "" {
		return liveboard.Find(repo, v)
	}
	for _, p := range configuredProjects(list) {
		if p.Slug == project && p.IssueDir != "" {
			return p.IssueDir, nil
		}
	}
	if project == fallbackProject {
		return liveboard.Find(repo, "")
	}
	return "", errors.New("issue-cli doesn't list it here")
}

// boardChecklist reads the project's board once and answers each issue's
// checkbox ids (uppercase) with their sections, and whether each is still
// open. The issue files are read
// here, not through issue-cli checklist: a session outlives its issue, and a
// child per session would add a record to the project's clilog on every
// validate, a failing one for each issue that has left the board.
//
// A project it can't read gives a nil checklist and open, so nothing is
// looked up, and the one note that says so.
func boardChecklist(project, repo string, list func() ([]byte, error)) (func(string) (map[string]string, bool), func(string) bool, string) {
	unread := func(why string) (func(string) (map[string]string, bool), func(string) bool, string) {
		return nil, nil, fmt.Sprintf("no board read for project %q (%s; -project, PLAYTEST_PROJECT or LIVE_BOARD names one): step ids checked for shape only", project, why)
	}
	dir, err := boardDir(project, repo, list)
	if err != nil {
		return unread(err.Error())
	}
	issues, err := tracker.LoadIssues(dir)
	if err != nil {
		return unread(err.Error())
	}
	board := make(map[string]map[string]string, len(issues))
	open := make(map[string]bool, len(issues))
	for _, issue := range issues {
		ids := map[string]string{}
		for _, it := range tracker.ListCheckboxes(issue.BodyRaw) {
			if it.ID != "" {
				ids[strings.ToUpper(it.ID)] = it.Section
			}
		}
		board[strings.ToLower(issue.Slug)] = ids
		open[strings.ToLower(issue.Slug)] = !tracker.IsClosedStatus(nil, issue.Status)
	}
	return func(slug string) (map[string]string, bool) {
		ids, ok := board[strings.ToLower(slug)]
		return ids, ok
	}, func(slug string) bool { return open[strings.ToLower(slug)] }, ""
}
