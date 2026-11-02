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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/testresults"
)

const usage = `playtest — run issue-viewer playtest sessions (playtest/sessions/*.yaml)

Commands:
  check                        validate every session: schema, actions, checks,
                               targets (internal/testids), scenarios, step ids
  ux-test  [-issue SLUG] [-s SESSION] [-step ID,…] [-repeat N] [-keep] [-headed] [-results FILE]
                               run the machine checks headless at every viewport;
                               exit 1 on any failure; -repeat N diffs N runs;
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
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

type opts struct {
	sessions, scenarios, runs string
	issue, session, steps     string
	project                   string
	results                   string
	list, base, out, only     string
	url, scroll               string
	scenario, baseRef, dir    string
	repeat, width, height     int
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
	var o opts
	fs := flag.NewFlagSet("playtest "+cmd, flag.ContinueOnError)
	fs.SetOutput(errw)
	fs.StringVar(&o.sessions, "sessions", filepath.Join(repo, "playtest", "sessions"), "session files")
	fs.StringVar(&o.scenarios, "scenarios", filepath.Join(repo, "playtest", "scenarios"), "scenario fixtures")
	fs.StringVar(&o.runs, "runs", filepath.Join(repo, ".playtest", "runs"), "where runs are kept")
	fs.StringVar(&o.issue, "issue", "", "only sessions for this issue slug")
	fs.StringVar(&o.session, "s", "", "only this session")
	fs.StringVar(&o.steps, "step", "", "only these step ids (comma-separated)")
	fs.StringVar(&o.project, "project", "", "issue-cli --project for the real issue (default $PLAYTEST_PROJECT; for report also $ISSUE_VIEWER_PROJECT and the configured project whose issues dir is this repo's; else issue-viewer)")
	fs.StringVar(&o.results, "results", resultsDefault(), "ux-test: also write the e2e results (sessions, steps, times) here; default $CI_RESULTS_DIR/e2e.json in local CI")
	fs.IntVar(&o.repeat, "repeat", 1, "run N times and fail if results differ")
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
	switch {
	case o.project != "":
	case cmd == "report":
		// report writes to the issue: it must reach a project this
		// machine has.
		o.project = defaultProject(os.Getenv, listProjects, repoIssueDirs(repo))
	default:
		// check reads step ids against the board only where
		// PLAYTEST_PROJECT asks for it; elsewhere ids are checked for
		// shape (the old default).
		o.project = envOr("PLAYTEST_PROJECT", "issue-viewer")
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
	if resolve {
		v.Checklist = issueChecklist(o.project)
	}
	for _, s := range all {
		errs = append(errs, v.Validate(s)...)
	}
	for _, n := range v.Notes {
		fmt.Fprintln(out, "note:", n)
	}
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(errw, e)
		}
		fmt.Fprintf(errw, "✗ %d problem(s) in %s\n", len(errs), o.sessions)
		return nil
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
	b, err := NewBrowser(o.headed, 1440, 900)
	if err != nil {
		fmt.Fprintln(errw, "playtest:", err)
		return 2
	}
	defer b.Close()
	r := &Runner{Repo: repo, ScenarioDir: o.scenarios, RunsRoot: o.runs, Build: BuildID(repo), Keep: o.keep, Out: out, OwnIssue: OwnIssue(repo, o.issue), Named: o.session}
	if o.runs == filepath.Join(repo, ".playtest", "runs") {
		r.EvidenceRuns = EvidenceRunsDir(repo)
	}
	code := 0
	var failed []string
	e2e := testresults.Playtest{Started: time.Now()}
	defer func() {
		if note := r.ShotsNote(); note != "" {
			fmt.Fprintln(out, note)
		}
	}()
	defer func() {
		if o.results == "" {
			return
		}
		e2e.Finished = time.Now()
		if err := writeE2E(o.results, e2e); err != nil {
			fmt.Fprintln(errw, "playtest: e2e results:", err)
		}
	}()
	for _, s := range sel {
		var first *Results
		for i := range max(o.repeat, 1) {
			start := time.Now()
			res, dir, err := r.UXTest(b, s, stepFilter(o.steps))
			if err != nil {
				fmt.Fprintln(errw, "playtest:", err)
				return 2
			}
			e2e.Sessions = append(e2e.Sessions, e2eSession(res, time.Since(start)))
			fmt.Fprintf(out, "results: %s\n", filepath.Join(dir, "results.yaml"))
			if f := failLines(res); len(f) > 0 {
				code = 1
				failed = append(failed, f...)
			}
			if i == 0 {
				first = res
			} else if d := Diff(first, res); d != "" {
				fmt.Fprintf(errw, "✗ %s: run %d differs from run 1\n%s\n", s.Name, i+1, d)
				code = 1
			} else {
				fmt.Fprintf(out, "✓ %s: run %d matches run 1\n", s.Name, i+1)
			}
		}
	}
	if code == 0 {
		fmt.Fprintln(out, "✓ ux-test passed")
	} else {
		for _, l := range failed {
			fmt.Fprintln(errw, l)
		}
		fmt.Fprintln(errw, "✗ ux-test failed")
	}
	return code
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
// whose issue_dir is one of issueDirs, and only then "issue-viewer", the
// engine board's old name. A name this machine doesn't have makes every
// issue-cli call fail, so APPLY ticks nothing.
func defaultProject(getenv func(string) string, list func() ([]byte, error), issueDirs []string) string {
	for _, k := range []string{"PLAYTEST_PROJECT", "ISSUE_VIEWER_PROJECT"} {
		if v := strings.TrimSpace(getenv(k)); v != "" {
			return v
		}
	}
	if raw, err := list(); err == nil {
		var ps []struct {
			Slug     string `json:"slug"`
			IssueDir string `json:"issue_dir"`
		}
		if json.Unmarshal(raw, &ps) == nil {
			for _, d := range issueDirs {
				for _, p := range ps {
					if p.IssueDir != "" && samePath(p.IssueDir, d) {
						return p.Slug
					}
				}
			}
		}
	}
	return "issue-viewer"
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

func listProjects() ([]byte, error) {
	bin, err := exec.LookPath(issueCLI())
	if err != nil {
		return nil, err
	}
	return exec.Command(bin, "--json", "projects").Output()
}

// issueCLI is the real issue-cli, used to read and update the real issue.
func issueCLI() string { return envOr("PLAYTEST_ISSUE_CLI", "issue-cli") }

// issueChecklist reads an issue's checkbox ids through the real issue-cli.
// It returns ok=false when the issue can't be resolved here.
func issueChecklist(project string) func(string) (map[string]string, bool) {
	cache := map[string]map[string]string{}
	return func(slug string) (map[string]string, bool) {
		if ids, ok := cache[slug]; ok {
			return ids, ids != nil
		}
		cache[slug] = nil
		bin, err := exec.LookPath(issueCLI())
		if err != nil {
			return nil, false
		}
		cmd := exec.Command(bin, "--project", project, "--json", "checklist", slug)
		raw, err := cmd.Output()
		if err != nil {
			return nil, false
		}
		var doc struct {
			Items []struct {
				ID      string `json:"id"`
				Section string `json:"section"`
			} `json:"items"`
		}
		if json.Unmarshal(raw, &doc) != nil {
			return nil, false
		}
		ids := map[string]string{}
		for _, it := range doc.Items {
			if it.ID != "" {
				ids[strings.ToUpper(it.ID)] = it.Section
			}
		}
		cache[slug] = ids
		return ids, true
	}
}
