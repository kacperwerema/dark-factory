// Package proof links an issue's acceptance criteria to their proof and
// checks every link. Links come from three places: `Proves:` trailers on the
// work branch's own commits, `proves:` on playtest steps (copied into each
// run's results.yaml), and `issue-cli prove` (the data sidecar's "proofs").
// Nothing is copied: each link points at something that exists, and Collect
// checks it every time it reads it.
package proof

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/timeline"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func init() {
	tracker.SetProofChecker(func(w *tracker.WorkflowConfig, issue *tracker.Issue, comments []tracker.Comment) error {
		r := Collect(w.RepoRoot, issue, comments)
		if w.DeferOwnerProof {
			return YOLOGateError(r)
		}
		return GateError(r)
	})
	tracker.SetReferenceChecker(func(w *tracker.WorkflowConfig, issue *tracker.Issue) error {
		return ReferenceError(w.RepoRoot, issue)
	})
}

// Where a link came from.
const (
	SourceProve    = "prove"
	SourceTrailer  = "trailer"
	SourcePlaytest = "playtest"
)

// Link is one criterion → proof link.
type Link struct {
	Kind string `json:"kind"` // tracker.ProofTest, ProofStep, …
	Ref  string `json:"ref"`
	// Source is where the link was declared; From names it (the commit
	// sha or the playtest session).
	Source string `json:"source"`
	From   string `json:"from,omitempty"`
	// Changed: a prove link whose criterion text differs from the text it
	// was linked to (the box was rewritten or renumbered since).
	Changed bool `json:"changed,omitempty"`
	// Owner: a link to a playtest judgement step, which only the owner's
	// playtest passes.
	Owner bool `json:"owner,omitempty"`
	// Stale: a playtest link from a run whose step id is no longer in the
	// session file (renumbered or removed since); it proves nothing.
	Stale bool `json:"stale,omitempty"`
}

// Check is a link's check result.
type Check struct {
	OK    bool   `json:"ok"`
	State string `json:"state"`
	// Fix says what makes a failing check pass.
	Fix string `json:"fix,omitempty"`
}

// CheckedLink is a link with its check result.
type CheckedLink struct {
	Link
	Check Check `json:"check"`
	// OwnerPassed: an Owner link whose step the owner's own playtest (a
	// judge-mode run) passed last. A ux-test pass of a judgement step only
	// checks the parts are there; it is not the owner's look.
	OwnerPassed bool `json:"owner_passed,omitempty"`
}

// Criterion is one acceptance criterion with its links.
type Criterion struct {
	ID      string `json:"id"`
	Text    string `json:"text"`
	Checked bool   `json:"checked"`
	Visual  bool   `json:"visual"`
	// Owner says why only the owner can prove the criterion; "" when anyone
	// can (owner.go).
	Owner string        `json:"owner,omitempty"`
	Links []CheckedLink `json:"links"`
}

// Proven reports whether any link passes its check.
func (c Criterion) Proven() bool {
	for _, l := range c.Links {
		if l.Check.OK {
			return true
		}
	}
	return false
}

// Stray is a declared link naming a criterion id the issue doesn't have.
type Stray struct {
	ID string `json:"id"`
	Link
}

// Report is everything Collect found for one issue.
type Report struct {
	Slug     string      `json:"slug"`
	Ref      string      `json:"ref,omitempty"`
	RefName  string      `json:"ref_name,omitempty"`
	Criteria []Criterion `json:"criteria"`
	Strays   []Stray     `json:"strays,omitempty"`
	// OwnerBoxes is the unticked boxes other than criteria that are the
	// owner's: a guided check left them, ## Human Testing, or "(owner)".
	OwnerBoxes []OwnerItem `json:"owner_boxes,omitempty"`
	// Missing names the sources that could not be read.
	Missing []string `json:"missing,omitempty"`
	// TestsAt is the newest tests: comment (zero: none). LastChange and
	// LastChangeSHA are the newest commit that changed code, the one a test
	// link's report must come after.
	TestsAt       time.Time `json:"tests_at,omitzero"`
	LastChange    time.Time `json:"last_change,omitzero"`
	LastChangeSHA string    `json:"last_change_sha,omitempty"`
	// judgement maps each playtest judgement step id (upper case) to its
	// session, for JudgementHint.
	judgement map[string]string
}

// Unproven returns the criteria with no passing link.
func (r Report) Unproven() []Criterion {
	var out []Criterion
	for _, c := range r.Criteria {
		if !c.Proven() {
			out = append(out, c)
		}
	}
	return out
}

// Collect reads every link for issue's criteria and checks each one. root is
// the project checkout; the issue's worktree is root/.worktrees/<slug>.
// It never fails: a source it can't read is listed in Missing.
func Collect(root string, issue *tracker.Issue, comments []tracker.Comment) Report {
	if root == "" {
		root, _ = os.Getwd()
	}
	worktree := filepath.Join(root, ".worktrees", issue.Slug)
	r := Report{Slug: issue.Slug}
	criteria := tracker.Criteria(issue.BodyRaw)
	byID := map[string]int{}
	for i, it := range criteria {
		byID[strings.ToUpper(it.ID)] = i
		r.Criteria = append(r.Criteria, Criterion{ID: it.ID, Text: it.Label(), Checked: it.Checked, Visual: tracker.IsVisualCriterion(it.Text), Links: []CheckedLink{}})
	}
	prefix := criteriaPrefix(criteria)

	stats, _ := tracker.LoadStats(issue.FilePath)
	r.Ref, r.RefName = timeline.GitRef(root, issue.Slug, stats.Transitions)
	c := &checker{root: root, worktree: worktree, ref: r.Ref, slug: issue.Slug, issuesDir: issuesRoot(issue), statsPath: tracker.StatsSidecarPath(issue.FilePath), tests: map[string]Check{}}
	c.goModule = IsGoModule(worktree) || IsGoModule(root)
	if r.Ref == "" {
		r.Missing = append(r.Missing, "work/"+issue.Slug+": no branch, recorded branch head or merge sha; test and commit links can't be checked")
	}
	c.reportAt, c.hasReport = lastTestsComment(comments)

	add := func(id string, l Link) {
		i, ok := byID[strings.ToUpper(id)]
		if !ok {
			r.Strays = append(r.Strays, Stray{ID: id, Link: l})
			return
		}
		r.Criteria[i].Links = append(r.Criteria[i].Links, CheckedLink{Link: l})
	}

	// Trailers on the branch's own commits.
	commits, err := c.branchCommits(stats)
	if err != nil {
		r.Missing = append(r.Missing, "git log on "+r.RefName+": "+err.Error())
	}
	if lc, ok := lastChange(commits); ok {
		c.lastChange, r.LastChange, r.LastChangeSHA = lc.at, lc.at, lc.sha
	}
	if c.hasReport {
		r.TestsAt = c.reportAt
	}
	for _, cm := range commits {
		for _, value := range cm.proves {
			ids, tests := splitTrailer(value, prefix)
			for _, id := range ids {
				if len(tests) == 0 {
					add(id, Link{Kind: tracker.ProofCommit, Ref: cm.sha, Source: SourceTrailer, From: timeline.ShortSHA(cm.sha)})
				}
				for _, t := range tests {
					add(id, Link{Kind: tracker.ProofTest, Ref: t, Source: SourceTrailer, From: timeline.ShortSHA(cm.sha)})
				}
			}
		}
	}

	// proves: on playtest steps.
	runs, problems := timeline.LoadPlaytestRuns(nil, issue.Slug, root, worktree)
	r.Missing = append(r.Missing, problems...)
	c.steps = latestSteps(runs)
	current := currentSessionSteps(root, issue)
	for _, d := range declaredSteps(runs) {
		l := Link{Kind: tracker.ProofStep, Ref: d.step, Source: SourcePlaytest, From: d.session}
		if ids, ok := current[d.session]; ok && !ids[strings.ToUpper(d.step)] {
			l.Stale = true
		}
		add(d.criterion, l)
	}

	// issue-cli prove links.
	store, err := tracker.LoadData(issue.FilePath)
	if err != nil {
		r.Missing = append(r.Missing, "data sidecar: "+err.Error())
	}
	ids := make([]string, 0, len(store.Proofs))
	for id := range store.Proofs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		for _, pl := range store.Proofs[id] {
			l := Link{Kind: pl.Kind, Ref: pl.Ref, Source: SourceProve}
			if i, ok := byID[strings.ToUpper(id)]; ok && pl.Criterion != "" && strings.TrimSpace(pl.Criterion) != strings.TrimSpace(criteria[i].Text) {
				l.Changed = true
			}
			add(id, l)
		}
	}

	var tests []string
	for _, cr := range r.Criteria {
		for _, l := range cr.Links {
			if l.Kind == tracker.ProofTest {
				tests = append(tests, l.Ref)
			}
		}
	}
	c.findTests(tests)
	steps := stepSessions(runs)
	for i := range r.Criteria {
		for j := range r.Criteria[i].Links {
			l := r.Criteria[i].Links[j].Link
			r.Criteria[i].Links[j].Check = c.check(l)
			if l.Stale {
				r.Criteria[i].Links[j].Check = staleStep(l, current[l.From])
			}
			if fix := stepIDHint(issue, steps, r.Criteria[i].ID, l); fix != "" && !r.Criteria[i].Links[j].Check.OK {
				r.Criteria[i].Links[j].Check.Fix = fix
			}
		}
	}
	// A trailer's test that is gone, with a newer test link on the same
	// criterion: the old commit can't be reworded, so say what replaced it.
	found := func(n string) bool { return c.tests[n].OK }
	for i := range r.Criteria {
		links := r.Criteria[i].Links
		for j, l := range links {
			if l.Kind != tracker.ProofTest || c.tests[l.Ref].OK {
				continue
			}
			if newer, ok := supersededIn(links, j, found); ok {
				links[j].Check = Check{State: "superseded by " + newer.Ref + " (" + linkFrom(newer.Link) + "); prove --run skips it"}
			}
		}
	}
	r.markOwner(issue, judgementSteps(runs, current), ownerPassedSteps(runs), guidedOwnerBoxes(root, worktree, issue.Slug))
	return r
}

// criteriaPrefix is the id prefix of the criteria boxes ("AC").
func criteriaPrefix(items []tracker.CheckboxItem) string {
	for _, it := range items {
		if it.ID != "" {
			return strings.TrimRightFunc(it.ID, func(r rune) bool { return r >= '0' && r <= '9' })
		}
	}
	return "AC"
}

var idShapeRe = regexp.MustCompile(`^([A-Za-z]+)\d+$`)

// splitTrailer reads one Proves: value ("AC1, AC3 TestA TestB"): words
// shaped like a criterion id (the criteria prefix plus a number) are ids,
// the rest test names.
func splitTrailer(value, prefix string) (ids, tests []string) {
	for _, w := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == ';' }) {
		if m := idShapeRe.FindStringSubmatch(w); m != nil && strings.EqualFold(m[1], prefix) {
			ids = append(ids, strings.ToUpper(w))
			continue
		}
		tests = append(tests, w)
	}
	return ids, tests
}

type commit struct {
	sha     string
	at      time.Time
	merge   bool
	parents []string
	subject string
	proves  []string
	refs    []string
	files   []string
}

// lastChange is the time of the newest commit that changed code: merging
// main into the branch, or a commit that only touches non-code files, doesn't
// make a tests: report stale.
func lastChange(commits []commit) (commit, bool) {
	for _, c := range commits {
		if !c.merge && !nonCodeOnly(c.files) {
			return c, true
		}
	}
	return commit{}, false
}

// nonCodeOnly reports whether every file is NonCode. No files is not.
func nonCodeOnly(files []string) bool {
	for _, f := range files {
		if !NonCode(f) {
			return false
		}
	}
	return len(files) > 0
}

// nonCodeDirs hold files no test reads: docs, playtest sessions (run by the
// playtest runner, not go test), guided check scripts and the tracker's notes.
var nonCodeDirs = []string{"docs/", "playtest/sessions/", "scripts/checks/", "issues/", "retros/", "sessions/"}

// NonCode reports whether a change to path can't change a test's result: a
// file under nonCodeDirs, a Markdown file or a CHANGELOG, in any folder.
// Other non-Go files (templates, static assets, testdata, embedded YAML) can.
func NonCode(file string) bool {
	for _, d := range nonCodeDirs {
		if strings.HasPrefix(file, d) {
			return true
		}
	}
	return strings.EqualFold(path.Ext(file), ".md") || strings.HasPrefix(strings.ToUpper(path.Base(file)), "CHANGELOG")
}

// lastTestsComment is the time of the newest tests: comment.
func lastTestsComment(comments []tracker.Comment) (time.Time, bool) {
	var last time.Time
	found := false
	for _, c := range comments {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(c.Text)), "tests:") {
			continue
		}
		at, ok := tracker.CommentTime(c.Date)
		if !ok {
			continue
		}
		if !found || at.After(last) {
			last, found = at, true
		}
	}
	return last, found
}

type stepVerdict struct {
	status  string
	session string
}

// latestSteps is each step's latest verdict (pass or fail) across runs; a
// pending or skipped result is not a verdict.
func latestSteps(runs []timeline.PlaytestRun) map[string]stepVerdict {
	out := map[string]stepVerdict{}
	for _, run := range runs { // oldest first
		for _, st := range run.Steps {
			if st.Status == "pass" || st.Status == "fail" {
				out[strings.ToUpper(st.ID)] = stepVerdict{status: st.Status, session: run.Session}
			}
		}
	}
	return out
}

// stepSessions maps each step id (upper case) in the runs to its session.
func stepSessions(runs []timeline.PlaytestRun) map[string]string {
	out := map[string]string{}
	for _, run := range runs {
		for _, st := range run.Steps {
			out[strings.ToUpper(st.ID)] = run.Session
		}
	}
	return out
}

// stepIDHint is the Fix for a trailer test word that is really a box or
// playtest step id ("Proves: AC1 TP4"): step ids are the issue's box ids, so
// the step proves the criterion through proves: in its session, not a
// trailer. "" when the link is not such a word.
func stepIDHint(issue *tracker.Issue, steps map[string]string, criterion string, l Link) string {
	if l.Kind != tracker.ProofTest || l.Source != SourceTrailer || !idShapeRe.MatchString(l.Ref) {
		return ""
	}
	id := strings.ToUpper(l.Ref)
	session, isStep := steps[id]
	if !isStep {
		if _, st := tracker.ResolveCheckboxRef(tracker.ListCheckboxes(issue.BodyRaw), id); st != tracker.RefFound {
			return ""
		}
	}
	where := "its playtest session"
	if session != "" {
		where = "the session " + session
	}
	return fmt.Sprintf("%s is a box or playtest step id, not a test: a trailer reads it as a test name. To prove %s with that step, add `proves: [%s]` to step %s in %s, or run: issue-cli prove %s %s --step %s",
		id, criterion, criterion, id, where, issue.Slug, criterion, id)
}

// currentSessionSteps maps each playtest session of the issue that has a
// session file (the issue's worktree first, then the checkout) to its step
// ids in upper case. A session without a file (archived, or another
// project's runner) is not in the map: its runs' ids can't be checked.
func currentSessionSteps(root string, issue *tracker.Issue) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, s := range tracker.IssuePlaytestSessions(root, issue.Slug) {
		ids := map[string]bool{}
		for _, st := range s.Steps {
			ids[strings.ToUpper(strings.TrimSpace(st.ID))] = true
		}
		out[s.Session] = ids
	}
	return out
}

// staleStep is the check of a stale step link: an old run's step id the
// session no longer has.
func staleStep(l Link, ids map[string]bool) Check {
	have := make([]string, 0, len(ids))
	for id := range ids {
		have = append(have, id)
	}
	sort.Strings(have)
	return Check{State: "stale: " + l.From + " has no step " + l.Ref + " now (an old run's id)",
		Fix: "the session's steps are " + strings.Join(have, ", ") + "; put proves: on the step that now covers it and run the session again"}
}

type declared struct{ criterion, step, session string }

// declaredSteps lists each step → criterion pair the newest run of each
// session declares (newestSteps), once: a step retargeted from AC2 to AC6
// links AC6 only.
func declaredSteps(runs []timeline.PlaytestRun) []declared {
	var out []declared
	seen := map[string]bool{}
	for _, s := range newestSteps(runs) {
		for _, id := range s.step.Proves {
			key := strings.ToUpper(s.step.ID) + "|" + strings.ToUpper(id)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, declared{criterion: strings.ToUpper(strings.TrimSpace(id)), step: s.step.ID, session: s.session})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].step < out[j].step })
	return out
}

func git(root string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), tracker.GitTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...).Output()
	return string(out), err
}

func unixTime(s string) time.Time {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(n, 0)
}
