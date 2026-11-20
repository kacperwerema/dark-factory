package proof

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/timeline"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// checker holds what the link checks share: the branch, its last change,
// the newest tests: report and each playtest step's latest verdict.
type checker struct {
	root, worktree, ref, slug string
	// miss is FindIssueRef's message when no branch was found.
	miss                 string
	cache                *timeline.Cache
	issuesDir, statsPath string
	lastChange           time.Time
	lastChangeSHA        string
	// leadRuns are the sidecar's lead runs, for lead links.
	leadRuns  []tracker.LeadRun
	reportAt  time.Time
	hasReport bool
	// reportMatch: the newest tests: comment names a commit that covers the
	// last change by sha or by patch (an amended commit), so its time
	// doesn't make the report stale.
	reportMatch bool
	// commits are the branch's own commits, newest first.
	commits []commit
	steps   map[string]stepVerdict
	tests   map[string]Check
	// goModule: prove --run can run the tests with go test (the project
	// has a go.mod); without one it runs the project's ci: commands.
	goModule bool
	// ciCommand caches hasCICommand(root), read on the first fix hint.
	ciCommand *bool
}

// branchCommits lists the work branch's own first-parent commits, newest
// first. A live branch is read as main..ref, so commits merged in from main
// never count. After the merge only the recorded head is left, so the walk
// stops at the issue's first dispatch or transition. Commits another issue's
// branch held first are left out (foreignCommits).
func (c *checker) branchCommits(stats tracker.StatsStore) ([]commit, error) {
	if c.ref == "" {
		return nil, nil
	}
	rng := LogRange(c.root, c.ref, stats)
	if rng == nil {
		return nil, nil
	}
	// Each record starts with \x1e, so the --name-only file list that
	// follows a commit's header stays in its record. The whole message is
	// read, not git's trailers: those are only the last paragraph, and a
	// Proves: line often sits in its own paragraph before Co-Authored-By.
	args := []string{"log", "--first-parent", "--name-only", "--format=%x1e%H%x1f%ct%x1f%P%x1f%B%x1f"}
	out, err := tracker.GitCached(c.root, append(args, rng...)...)
	if err != nil {
		return nil, err
	}
	var commits []commit
	for _, rec := range strings.Split(out, "\x1e") {
		f := strings.Split(strings.TrimSpace(rec), "\x1f")
		if len(f) < 5 {
			continue
		}
		parents := strings.Fields(f[2])
		cm := commit{sha: f[0], at: unixTime(f[1]), merge: len(parents) > 1, parents: parents}
		cm.subject, _, _ = strings.Cut(f[3], "\n")
		cm.proves = provesLines(f[3])
		cm.refs = MessageLines(f[3], RefsKey)
		cm.files = strings.Fields(f[4])
		commits = append(commits, cm)
	}
	foreign := foreignCommits(c.cache, c.root, c.issuesDir, c.statsPath, c.ref, stats, commits)
	own := commits[:0]
	for _, cm := range commits {
		if !foreign[cm.sha] {
			own = append(own, cm)
		}
	}
	return own, nil
}

// provesLines reads the values of the Proves: lines in a commit message's
// body, in any paragraph.
func provesLines(msg string) []string { return MessageLines(msg, "Proves") }

// MessageLines reads the values of the `<key>:` lines in a commit message's
// body, in any paragraph: git's own trailers are only the last paragraph,
// and such a line often sits in its own paragraph before Co-Authored-By.
// catalog-check reads its Catalog: lines with it too.
func MessageLines(msg, key string) []string {
	var out []string
	lines := strings.Split(msg, "\n")
	for _, l := range lines[1:] {
		k, v, ok := strings.Cut(strings.TrimSpace(l), ":")
		if ok && strings.EqualFold(k, key) {
			if v = strings.TrimSpace(v); v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}

// LogRange is the git log range of the work branch's own commits at ref:
// main..ref while the branch is live, so commits merged in from main never
// count; after the merge only the recorded head is left, so the walk stops
// at the issue's first dispatch or transition. nil: nothing to walk. ref is
// a commit sha and main is named by its sha too: the range is a key for
// tracker.GitCached, and it says the same thing as the branch's name.
func LogRange(root, ref string, stats tracker.StatsStore) []string {
	// A ref is never an option: it can come from the agent-written stats
	// sidecar (docs/API/security.md).
	if ref == "" || strings.HasPrefix(ref, "-") {
		return nil
	}
	if _, main, _ := tracker.MainHead(root); main != "" {
		if _, err := tracker.GitCached(root, "merge-base", "--is-ancestor", ref, main); err != nil {
			return []string{main + ".." + ref}
		}
	}
	since := issueStart(stats)
	if since.IsZero() {
		return nil
	}
	return []string{"--since=" + strconv.FormatInt(since.Unix(), 10), ref}
}

// IsTestPath reports whether path holds tests in the usual layouts. A test
// link's name is looked up in those files and in Rust sources with a test
// attribute (testFiles).
func IsTestPath(path string) bool { return testPathRe.MatchString(path) }

// issueStart is when work on the issue began: its first dispatch, else its
// first transition.
func issueStart(stats tracker.StatsStore) time.Time {
	if len(stats.Dispatches) > 0 {
		return stats.Dispatches[0].At
	}
	if len(stats.Transitions) > 0 {
		return stats.Transitions[0].TS
	}
	return time.Time{}
}

func (c *checker) check(l Link) Check {
	switch l.Kind {
	case tracker.ProofTest:
		return c.checkTest(l.Ref)
	case tracker.ProofStep:
		// A declared step already names its session.
		return c.checkStep(l.Ref, l.Source != SourcePlaytest)
	case tracker.ProofCommit:
		return c.checkCommit(l)
	case tracker.ProofValue:
		return Check{OK: true, State: "reported"}
	case tracker.ProofShot:
		return c.checkShot(l.Ref)
	case tracker.ProofLead:
		return c.checkLead(l.Ref)
	}
	return Check{State: "unknown link kind " + l.Kind}
}

// testPathRe matches files that hold tests in the usual layouts.
var testPathRe = regexp.MustCompile(`(?i)(_test\.|(^|/)test_|\.test\.|\.spec\.|(^|/)(tests?|__tests__)/|tests?\.(cs|java|kt|swift|py)$)`)

// findTests looks every test name up in one git grep at the ref, so a long
// list costs one walk of the tree instead of one per name.
func (c *checker) findTests(names []string) {
	var todo []string
	for _, n := range names {
		if _, ok := c.tests[n]; !ok && n != "" && !containsString(todo, n) {
			todo = append(todo, n)
		}
	}
	if c.ref == "" || len(todo) == 0 {
		return
	}
	// Whole lines, not -o: -o prints only the longest match on a line, so
	// a name inside a longer one's line (lista beside the it() title that
	// starts with it) was never printed. -z ends the path with a NUL, since
	// a path or a title may hold a colon.
	args := []string{"grep", "-z", "-w", "-F", "-I"}
	for _, n := range todo {
		args = append(args, "-e", n)
	}
	out, _ := tracker.GitCached(c.root, append(args, c.ref, "--")...)
	hit := map[string]bool{}
	files := &testFiles{ref: c.ref, grep: func(args ...string) (string, error) { return tracker.GitCached(c.root, args...) }}
	for _, line := range strings.Split(out, "\n") {
		// <ref>:<path>\0<line>
		path, text, ok := strings.Cut(strings.TrimPrefix(line, c.ref+":"), "\x00")
		if !ok || !files.is(path) {
			continue
		}
		for _, n := range todo {
			if !hit[n] && hasWholeWord(text, n) {
				hit[n] = true
			}
		}
	}
	for _, n := range todo {
		if hit[n] {
			c.tests[n] = Check{OK: true}
		} else {
			c.tests[n] = Check{State: "not found", Fix: "no test file on the branch has " + n + " as a whole word: check the name (a JS/TS it() title goes whole, in quotes in a Proves: line)"}
		}
	}
}

// hasWholeWord reports whether name occurs in line the way git grep -w
// matches it: the bytes either side of an occurrence are not ASCII letters,
// digits or _, so a title with spaces, colons or letters outside ASCII
// matches as one phrase.
func hasWholeWord(line, name string) bool {
	word := func(b byte) bool {
		return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
	}
	for from := 0; ; {
		i := strings.Index(line[from:], name)
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len(name)
		if (start == 0 || !word(line[start-1])) && (end == len(line) || !word(line[end])) {
			return true
		}
		from = start + 1
	}
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// noBranch is a test or commit link's state when the issue has no branch:
// what the lookup tried and the fix.
func (c *checker) noBranch() Check {
	return Check{State: "not checked: no branch found", Fix: c.miss}
}

func (c *checker) checkTest(name string) Check {
	if c.ref == "" {
		return c.noBranch()
	}
	c.findTests([]string{name})
	found := c.tests[name]
	if !found.OK {
		return found
	}
	if !c.hasReport {
		return Check{State: "no tests: report", Fix: c.testsFix("run the tests, then")}
	}
	if !c.lastChange.IsZero() && c.reportAt.Before(c.lastChange) && !c.reportMatch {
		return Check{State: "stale (code changed since the last tests: comment)", Fix: c.testsFix("re-run the tests, then")}
	}
	if !c.lastChange.IsZero() && c.reportAt.Before(c.lastChange) {
		return Check{OK: true, State: "reported for the last change (its commit names it, by sha or patch)"}
	}
	return Check{OK: true, State: "reported after the last change"}
}

// testsFix says how to post the tests: report: prove --run in a Go project,
// else by hand ("<manual>: issue-cli comment …").
func (c *checker) testsFix(manual string) string {
	if c.goModule || c.hasCI() {
		return fmt.Sprintf("issue-cli prove %s --run (runs the named tests and posts the tests: comment); or %s: issue-cli comment %s --text \"tests: ...\"", c.slug, manual, c.slug)
	}
	return fmt.Sprintf("%s: issue-cli comment %s --text \"tests: ...\"", manual, c.slug)
}

func (c *checker) checkStep(id string, nameSession bool) Check {
	v, ok := c.steps[strings.ToUpper(id)]
	in := ""
	if ok && nameSession {
		in = " in " + v.session
	}
	switch {
	case !ok:
		return Check{State: "not run", Fix: "run the playtest session of " + c.slug + " that has step " + id + " (ux-test, or the owner's playtest for a judgement step)"}
	case v.status == "fail":
		return Check{State: "failed" + in, Fix: "fix it and run the session again"}
	}
	return Check{OK: true, State: "passed" + in}
}

func (c *checker) checkCommit(l Link) Check {
	if l.Source == SourceTrailer {
		return Check{OK: true, State: "found"}
	}
	if c.ref == "" {
		return c.noBranch()
	}
	full, err := ResolveCommit(c.root, l.Ref)
	if err != nil {
		return Check{State: "not found", Fix: "no commit " + l.Ref + " in the repository"}
	}
	if _, err := tracker.GitCached(c.root, "merge-base", "--is-ancestor", full, c.ref); err != nil {
		if m := c.patchMatch(full); m != "" {
			return Check{OK: true, State: "found as " + gitdir.ShortSHA(m) + " (same patch)"}
		}
		return Check{State: "not found", Fix: l.Ref + " is not on the work branch or its merge"}
	}
	return Check{OK: true, State: "found"}
}

func (c *checker) checkShot(path string) Check {
	for _, p := range shotCandidates(c.root, c.worktree, path) {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return Check{OK: true, State: "shown"}
		}
	}
	return Check{State: "missing", Fix: path + " is not in the worktree or the project checkout"}
}

// shotCandidates are the files a --shot path can mean: itself when
// absolute, else under the worktree, then the project checkout.
func shotCandidates(root, worktree, path string) []string {
	if filepath.IsAbs(path) {
		return []string{path}
	}
	return []string{filepath.Join(worktree, path), filepath.Join(root, path)}
}

// InsideCheckout reports whether a --shot path resolves under the issue's
// worktree (FindIssueRef) or the project checkout.
func InsideCheckout(root, worktree, path string) bool {
	for _, p := range shotCandidates(root, worktree, path) {
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		for _, base := range []string{worktree, root} {
			if b, err := filepath.Abs(base); err == nil && (abs == b || strings.HasPrefix(abs, b+string(filepath.Separator))) {
				return true
			}
		}
	}
	return false
}

// Ways prints the ways to prove criterion id, filled in.
func Ways(slug, id string) []string {
	return []string{
		fmt.Sprintf("a playtest step that shows it: add `proves: [%s]` to the step, then re-run make ux-test", id),
		fmt.Sprintf("a test: a `Proves: %s <TestName>` trailer on the commit that adds or changes it", id),
		fmt.Sprintf("the change itself: a `Proves: %s` trailer on its commit", id),
		fmt.Sprintf("or: issue-cli prove %s %s --test <Name> | --step <id> | --commit <sha> | --value \"<measured>\"", slug, id),
		fmt.Sprintf("a command only the lead can run (the worker guard refuses it): issue-cli prove %s %s --lead \"issue-cli check-script %s --yes\", then stop: the lead's tick lists it", slug, id, slug),
	}
}

// GateError is nil when every criterion is proven; otherwise it names each
// unproven one, why its links fail, and how to prove it.
func GateError(r Report) error { return gateError(r, r.Unproven()) }

func gateError(r Report, unproven []Criterion) error {
	if len(unproven) == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d of %d acceptance criteria are not proven:", len(unproven), len(r.Criteria))
	for _, c := range unproven {
		fmt.Fprintf(&b, "\n  %s %s", c.ID, c.Text)
		for _, l := range c.Links {
			fmt.Fprintf(&b, "\n    ✗ %s — %s", describe(l.Link), l.Check.State)
			if l.Check.Fix != "" {
				fmt.Fprintf(&b, "\n      → %s", l.Check.Fix)
			}
		}
	}
	var waits []string
	for _, c := range unproven {
		for _, l := range c.Links {
			if l.Kind == tracker.ProofLead && !l.Check.OK {
				waits = append(waits, c.ID+" "+l.Ref)
			}
		}
	}
	if len(waits) > 0 {
		fmt.Fprintf(&b, "\n\nWaits on the lead: %s.\nNot yours to run: stop here, no question: comment needed. The lead's tick (issue-cli queue next) lists it; the lead runs issue-cli prove %s --lead-run.", strings.Join(waits, "; "), r.Slug)
		if len(waits) == len(unproven) {
			// Only the lead's run proves these: no fix, so no hint: either.
			return fmt.Errorf("%s", b.String())
		}
	}
	b.WriteString("\n")
	var fix strings.Builder
	fmt.Fprintf(&fix, "\nProve each one with any of (shown for %s):", unproven[0].ID)
	for _, w := range Ways(r.Slug, unproven[0].ID) {
		fmt.Fprintf(&fix, "\n  - %s", w)
	}
	fmt.Fprintf(&fix, "\n\nEvery link and its check: issue-cli prove %s --list", r.Slug)
	// The gate's action hint: replaces the fix (tracker.RuleShowsHint).
	return tracker.RuleFailureJoined(b.String(), "", fix.String())
}

// Describe renders a link as "test TestFoo (trailer 3f2a1c9)".
func Describe(l Link) string { return describe(l) }

func describe(l Link) string {
	ref := l.Ref
	if l.Kind == tracker.ProofCommit {
		ref = gitdir.ShortSHA(ref)
	}
	if l.Kind == tracker.ProofValue || l.Kind == tracker.ProofLead {
		ref = strconv.Quote(ref)
	}
	s := l.Kind + " " + ref
	switch l.Source {
	case SourceTrailer:
		if l.Kind != tracker.ProofCommit {
			s += " (trailer on " + l.From + ")"
		} else {
			s += " (trailer)"
		}
	case SourcePlaytest:
		s += " (proves: in session " + l.From + ")"
	default:
		s += " (prove)"
	}
	if l.Changed {
		s += " [criterion text changed since linked]"
	}
	return s
}

// TickProven ticks the unticked boxes of the proven criteria and returns
// their ids. A ticked box whose proof stopped passing is left alone, and an
// owner's box (OwnerBoxWhy: owner-only, "(owner)", Human Testing) ticks
// only when the owner proved it: the owner's playtest passed its judgement
// step (Tickable).
func TickProven(issuePath string, r Report) ([]string, error) {
	tickable := map[string]Criterion{}
	for _, c := range r.Criteria {
		if c.Tickable() && !c.Checked {
			tickable[strings.ToUpper(c.ID)] = c
		}
	}
	if len(tickable) == 0 {
		return nil, nil
	}
	var ticked []string
	_, _, err := tracker.UpdateIssueBody(issuePath, func(body string) (string, bool, error) {
		var lines []int
		for _, it := range tracker.Criteria(body) {
			c, ok := tickable[strings.ToUpper(it.ID)]
			if it.Checked || !ok || (r.OwnerBoxWhy(it) != "" && !c.OwnerProven()) {
				continue
			}
			lines = append(lines, it.Line)
			ticked = append(ticked, it.ID)
		}
		if len(lines) == 0 {
			return body, false, nil
		}
		return tracker.CheckLines(body, lines), true, nil
	})
	if err != nil {
		return nil, err
	}
	return ticked, nil
}

// ResolveCommit returns the full sha of a commit in the repository at root.
func ResolveCommit(root, sha string) (string, error) {
	read := git
	if timeline.IsSHA(sha) {
		read = tracker.GitCached // a missing commit isn't kept: it may come
	}
	out, err := read(root, "rev-parse", "--verify", "--quiet", sha+"^{commit}")
	full := strings.TrimSpace(out)
	if err != nil || full == "" {
		return "", fmt.Errorf("no commit %s in %s", sha, root)
	}
	return full, nil
}

func (c *checker) hasCI() bool {
	if c.ciCommand == nil {
		v := hasCICommand(c.root)
		c.ciCommand = &v
	}
	return *c.ciCommand
}

// hasCICommand reports whether the project's workflow.yaml at root has a
// default-tier ci: command, which prove --run runs where there is no go.mod.
// Read only for a fix hint, so a page of reports doesn't parse it.
func hasCICommand(root string) bool {
	wf, err := tracker.LoadWorkflow(filepath.Join(root, "workflow.yaml"))
	return err == nil && wf != nil && len(wf.CI.Commands("")) > 0
}
