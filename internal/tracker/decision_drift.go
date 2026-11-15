package tracker

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/jsonfile"
	"github.com/kacperwerema/dark-factory/internal/period"
)

// The drift sweep (docs/workflow.md#decision-ledger): what moved away from
// the decisions between gates. Four kinds:
//
//	citation    an open issue cites a replaced or expired ref
//	paths       a commit on main since the last sweep changed a hard
//	            decision's paths: and its issue doesn't cite it
//	docs        a "Settled rules" line in the docs points at a replaced or
//	            expired ref
//	provisional a provisional decision more than DriftProvisionalAge calendar days old that
//	            reaches beyond its issue (scope wider than issue, or paths:)
//
// Paths only, as the diff audit: no model reads a diff
// (factory/decision-ledger-…#D-4). The sweep only reads; SaveDriftMark,
// called only for --record, moves the window.
const (
	DriftCitation    = "citation"
	DriftPaths       = "paths"
	DriftDocs        = "docs"
	DriftProvisional = "provisional"
)

// DriftKinds is the kinds in report order.
var DriftKinds = []string{DriftCitation, DriftPaths, DriftDocs, DriftProvisional}

// DriftProvisionalAge is how many calendar days old a provisional decision
// may be before the sweep lists it: one dated more than this many days
// before today's date is listed, one exactly this old is not. With no
// mark the path window goes back as many days, from local midnight.
const DriftProvisionalAge = 14

// calendarDaysBetween is how many calendar days lie from day's date to
// today's date, each the date it names in its own zone (a decision's date
// is the day it says; today is the board's local day). It counts dates,
// never hours, so a DST change or the time of day can't move it.
func calendarDaysBetween(day, today time.Time) int {
	dy, dm, dd := day.Date()
	ty, tm, td := today.Date()
	from := time.Date(dy, dm, dd, 0, 0, 0, 0, time.UTC)
	to := time.Date(ty, tm, td, 0, 0, 0, 0, time.UTC)
	return int(to.Sub(from).Hours() / 24)
}

// DriftFinding is one thing the sweep found.
type DriftFinding struct {
	Kind  string `json:"kind"`
	Ref   string `json:"ref"`
	Text  string `json:"text,omitempty"`
	Issue string `json:"issue,omitempty"` // citation: the citing issue; paths: the commit's issues, "" for none
	// File and Line: the docs line (docs), relative to the repo root.
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
	// SHA, Subject and Files: the commit (paths).
	SHA        string   `json:"sha,omitempty"`
	Subject    string   `json:"subject,omitempty"`
	Files      []string `json:"files,omitempty"`
	ReplacedBy string   `json:"replaced_by,omitempty"`
	Expired    string   `json:"expired,omitempty"` // the expires: day
	Since      string   `json:"since,omitempty"`   // provisional: its date
	Scope      string   `json:"scope,omitempty"`
	Fix        string   `json:"fix"`
}

// DriftWindow is the range of main the path kind read.
type DriftWindow struct {
	From    string     `json:"from,omitempty"` // the mark's main sha, "" for a time window
	To      string     `json:"to,omitempty"`   // main's sha now
	SinceAt *time.Time `json:"since_at,omitempty"`
	First   bool       `json:"first,omitempty"` // no usable mark: the last DriftProvisionalAge days
	LastAt  *time.Time `json:"last_sweep,omitempty"`
	Note    string     `json:"note,omitempty"`
}

// DriftReport is the sweep's result.
type DriftReport struct {
	Window          DriftWindow    `json:"window"`
	Findings        []DriftFinding `json:"findings"`
	ProvisionalRest int            `json:"provisional_rest"`
}

// Count is the number of findings of kind.
func (r DriftReport) Count(kind string) int {
	n := 0
	for _, f := range r.Findings {
		if f.Kind == kind {
			n++
		}
	}
	return n
}

// DriftInput is what the sweep reads.
type DriftInput struct {
	Issues   []*Issue
	IsClosed func(*Issue) bool
	IssueDir string
	Root     string // the git repo whose main the path kind reads
	DocsDir  string // "" skips the docs kind
	Today    time.Time
	Since    string // a commit to read main from instead of the mark
}

// DriftMark is <issues>/.decisions/drift.json: the last recorded sweep.
type DriftMark struct {
	At   time.Time `json:"at"`
	Main string    `json:"main"`
	By   string    `json:"by,omitempty"`
}

// DriftMarkPath is <issues>/.decisions/drift.json.
func DriftMarkPath(issueDir string) string {
	return filepath.Join(DecisionLogDir(issueDir), "drift.json")
}

// LoadDriftMark reads the mark; ok is false when there is none.
func LoadDriftMark(issueDir string) (DriftMark, bool, error) {
	data, err := os.ReadFile(DriftMarkPath(issueDir))
	if errors.Is(err, fs.ErrNotExist) {
		return DriftMark{}, false, nil
	}
	if err != nil {
		return DriftMark{}, false, err
	}
	var m DriftMark
	if err := json.Unmarshal(data, &m); err != nil {
		return DriftMark{}, false, fmt.Errorf("%s: %w", DriftMarkPath(issueDir), err)
	}
	return m, m.Main != "" || !m.At.IsZero(), nil
}

// SaveDriftMark writes the mark under the decisions dir's drift lock, aside
// and renamed into place.
func SaveDriftMark(issueDir string, m DriftMark) error {
	dir := DecisionLogDir(issueDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "drift.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	deadline := time.Now().Add(DecisionLogWait)
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		if time.Now().After(deadline) {
			return errors.New("the drift mark is being written by another issue-cli; try again")
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return jsonfile.Write(DriftMarkPath(issueDir), m, 0o600)
}

// Drift runs the sweep. A git failure skips only the path kind and says so
// in the window's note.
func Drift(in DriftInput) (DriftReport, error) {
	today := in.Today
	if today.IsZero() {
		today = time.Now()
	}
	x := BuildDecisionIndex(in.Issues, in.IsClosed)
	rep := DriftReport{Findings: []DriftFinding{}}

	// citation: open issues citing refs out of force.
	issues := append([]*Issue(nil), in.Issues...)
	sort.Slice(issues, func(i, j int) bool { return issues[i].Slug < issues[j].Slug })
	for _, issue := range issues {
		if in.IsClosed != nil && in.IsClosed(issue) {
			continue
		}
		for _, s := range StaleCitations(x, issue, today) {
			f := DriftFinding{Kind: DriftCitation, Ref: s.Ref, Text: s.Entry.Text, Issue: issue.Slug}
			if s.By != nil {
				f.ReplacedBy = s.By.Ref
				f.Fix = fmt.Sprintf("cite %s instead in %s", s.By.Ref, issue.Slug)
			} else {
				f.Expired = s.Expired
				f.Fix = fmt.Sprintf("drop the citation from %s, or ask the owner to renew %s", issue.Slug, s.Ref)
			}
			rep.Findings = append(rep.Findings, f)
		}
	}

	// paths: commits on main since the mark.
	pathFindings, window, err := driftPaths(in, x, today)
	if err != nil {
		return rep, err
	}
	rep.Window = window
	rep.Findings = append(rep.Findings, pathFindings...)

	// docs: Settled rules sources.
	if in.DocsDir != "" {
		docs, err := driftDocs(in.DocsDir, in.Root, x, today)
		if err != nil {
			return rep, err
		}
		rep.Findings = append(rep.Findings, docs...)
	}

	// provisional: old and reaching beyond their issue.
	var prov []DriftFinding
	for _, e := range x.Entries {
		if !e.ProvisionalOn(today) || calendarDaysBetween(e.Date, today) <= DriftProvisionalAge {
			continue
		}
		if e.ScopeOrDefault() == ScopeIssue && len(e.Paths) == 0 {
			rep.ProvisionalRest++
			continue
		}
		prov = append(prov, DriftFinding{Kind: DriftProvisional, Ref: e.Ref, Text: e.Text, Since: e.Date.Format(decisionDateLayout),
			Scope: e.ScopeOrDefault(), Fix: fmt.Sprintf("the owner confirms it (decide %s --confirm %s --quote \"…\") or replaces it", e.Slug, e.ID)})
	}
	sort.SliceStable(prov, func(i, j int) bool {
		if prov[i].Since != prov[j].Since {
			return prov[i].Since < prov[j].Since
		}
		return prov[i].Ref < prov[j].Ref
	})
	rep.Findings = append(rep.Findings, prov...)
	return rep, nil
}

// driftCommit is one non-merge commit on main in the window.
type driftCommit struct {
	sha, subject string
	at           time.Time // committer time
	refs         []string  // Refs: lines' values
	files        []string
}

func driftPaths(in DriftInput, x *DecisionIndex, today time.Time) ([]DriftFinding, DriftWindow, error) {
	var w DriftWindow
	mark, hasMark, err := LoadDriftMark(in.IssueDir)
	if err != nil {
		return nil, w, err
	}
	if hasMark && !mark.At.IsZero() {
		at := mark.At
		w.LastAt = &at
	}
	if in.Root == "" {
		w.Note = "no repository: path drift skipped"
		return nil, w, nil
	}
	mainName, err := MainBranch(in.Root)
	if err != nil {
		w.Note = "no git main branch: path drift skipped"
		return nil, w, nil
	}
	head, err := runGit(in.Root, "rev-parse", "--verify", "--quiet", mainName+"^{commit}")
	if err != nil {
		w.Note = "no git main branch: path drift skipped"
		return nil, w, nil
	}
	w.To = strings.TrimSpace(head)

	from := ""
	switch {
	case strings.TrimSpace(in.Since) != "":
		sha, err := runGit(in.Root, "rev-parse", "--verify", "--quiet", strings.TrimSpace(in.Since)+"^{commit}")
		if err != nil {
			return nil, w, fmt.Errorf("--since %s: not a commit in %s", in.Since, in.Root)
		}
		from = strings.TrimSpace(sha)
	case hasMark && mark.Main != "":
		if _, err := runGit(in.Root, "merge-base", "--is-ancestor", mark.Main, w.To); err == nil {
			from = mark.Main
		} else {
			w.Note = fmt.Sprintf("the last sweep's main %s is not in main's history: read the last %d days", gitdir.ShortSHA(mark.Main), DriftProvisionalAge)
		}
	}
	args := []string{"log", "--no-merges", "--name-only", "--format=%x1e%H%x1f%ct%x1f%s%x1f%b%x1d"}
	if from != "" {
		w.From = from
		args = append(args, from+".."+w.To)
	} else {
		w.First = true
		since := period.DaysBack(today, DriftProvisionalAge)
		w.SinceAt = &since
		args = append(args, "--since="+since.Format(time.RFC3339), w.To)
	}

	var guarded []*DecisionEntry
	for _, e := range x.Entries {
		if e.Hard && len(e.Paths) > 0 && e.ActiveOn(today) {
			guarded = append(guarded, e)
		}
	}
	if len(guarded) == 0 || from == w.To {
		return nil, w, nil
	}
	out, err := runGit(in.Root, args...)
	if err != nil {
		return nil, w, fmt.Errorf("git log on %s: %w", mainName, err)
	}
	commits := parseDriftLog(out)

	var found []DriftFinding
	owners := map[string][]string{}
	var byBranch map[string][]string
	branchMap := func() map[string][]string {
		if byBranch == nil {
			var since time.Time
			if w.SinceAt != nil {
				since = *w.SinceAt
			}
			byBranch = branchOwners(in.Root, x, from, since)
		}
		return byBranch
	}
	for i := len(commits) - 1; i >= 0; i-- { // oldest first
		c := commits[i]
		for _, e := range guarded {
			// A commit from before the decision's day can't have gone
			// against it.
			if !c.at.IsZero() && c.at.Before(e.Date) {
				continue
			}
			var files []string
			for _, f := range c.files {
				for _, g := range e.Paths {
					if MatchGlob(g, f) {
						files = append(files, f)
						break
					}
				}
			}
			if len(files) == 0 {
				continue
			}
			slugs, ok := owners[c.sha]
			if !ok {
				slugs = commitIssues(c, x, branchMap)
				owners[c.sha] = slugs
			}
			if commitCites(x, e, slugs) {
				continue
			}
			f := DriftFinding{Kind: DriftPaths, Ref: e.Ref, Text: e.Text, Issue: strings.Join(slugs, ", "), SHA: c.sha, Subject: c.subject, Files: files}
			if len(slugs) == 0 {
				f.Fix = fmt.Sprintf("no issue names %s (a direct commit on main): the owner checks it against %s", gitdir.ShortSHA(c.sha), e.Ref)
			} else {
				f.Fix = fmt.Sprintf("%s changed %s's paths without citing it: the owner checks the change against it", f.Issue, e.Ref)
			}
			found = append(found, f)
		}
	}
	return found, w, nil
}

func parseDriftLog(out string) []driftCommit {
	var commits []driftCommit
	for _, rec := range strings.Split(out, "\x1e") {
		head, files, ok := strings.Cut(rec, "\x1d")
		if !ok {
			continue
		}
		parts := strings.SplitN(head, "\x1f", 4)
		if len(parts) < 4 {
			continue
		}
		c := driftCommit{sha: strings.TrimSpace(parts[0]), subject: strings.TrimSpace(parts[2])}
		if sec, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64); err == nil {
			c.at = time.Unix(sec, 0)
		}
		for _, line := range strings.Split(parts[3], "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Refs:"); ok {
				for _, r := range strings.Split(v, ",") {
					if r = strings.TrimSpace(r); r != "" {
						c.refs = append(c.refs, r)
					}
				}
			}
		}
		for _, f := range strings.Split(files, "\n") {
			if f = strings.TrimSpace(f); f != "" {
				c.files = append(c.files, f)
			}
		}
		commits = append(commits, c)
	}
	return commits
}

// commitIssues is the issues a commit belongs to: the known slugs its
// Refs: lines name, else the issues whose work branch made it (owners,
// from branchOwners). Sorted.
func commitIssues(c driftCommit, x *DecisionIndex, owners func() map[string][]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range c.refs {
		if s := strings.ToLower(strings.TrimSuffix(r, ".md")); x.known[s] && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		out = append(out, owners()[c.sha]...)
	}
	sort.Strings(out)
	return out
}

// branchOwners maps each commit to the work/<slug> branches that made it,
// one rev-list per branch: the branch's first-parent line (a commit merged
// in from main never is on it) after the base its dispatch recorded, else
// from an hour before the issue's first dispatch or transition, else the
// sweep's window (from, or since). On a fast-forward main a branch cut
// later has older main commits on its first-parent line too; the base or
// the start tells them apart.
func branchOwners(root string, x *DecisionIndex, from string, since time.Time) map[string][]string {
	out := map[string][]string{}
	refs, err := runGit(root, "for-each-ref", "--format=%(refname)", "refs/heads/work/")
	if err != nil {
		return out
	}
	for _, ref := range strings.Split(refs, "\n") {
		ref = strings.TrimSpace(ref)
		slug, ok := strings.CutPrefix(ref, "refs/heads/work/")
		if !ok || !x.known[slug] {
			continue
		}
		args := []string{"rev-list", "--first-parent", ref}
		if from != "" {
			args = append(args, "^"+from)
		} else if !since.IsZero() {
			args = append(args, "--since="+since.Format(time.RFC3339))
		}
		if issue := x.issues[slug]; issue != nil && issue.FilePath != "" {
			st, _ := LoadStats(issue.FilePath)
			if d, ok := DispatchBase(st); ok && !strings.HasPrefix(d.BaseSHA, "-") {
				if _, err := runGit(root, "rev-parse", "--verify", "--quiet", d.BaseSHA+"^{commit}"); err == nil {
					args = append(args, "^"+d.BaseSHA)
				}
			} else if start := issueStartedAt(st); !start.IsZero() {
				args = append(args, "--since="+start.Add(-time.Hour).Format(time.RFC3339))
			}
		}
		list, err := runGit(root, args...)
		if err != nil {
			continue
		}
		for _, sha := range strings.Split(list, "\n") {
			if sha = strings.TrimSpace(sha); sha != "" {
				out[sha] = append(out[sha], slug)
			}
		}
	}
	return out
}

// issueStartedAt is the issue's first dispatch, else its first transition.
func issueStartedAt(st StatsStore) time.Time {
	if len(st.Dispatches) > 0 {
		return st.Dispatches[0].At
	}
	if len(st.Transitions) > 0 {
		return st.Transitions[0].TS
	}
	return time.Time{}
}

// commitCites reports whether any of the commit's issues is the decision's
// own or cites it in its body (outside its own ## Decisions and n/a lines).
func commitCites(x *DecisionIndex, e *DecisionEntry, slugs []string) bool {
	for _, s := range slugs {
		if s == e.Slug {
			return true
		}
		if issue := x.issues[s]; issue != nil && BodyCitations(x, issue)[e.Ref] {
			return true
		}
	}
	return false
}

var (
	headingLineRe = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*#*\s*$`)
	docsRefRe     = regexp.MustCompile(`([a-z0-9][a-z0-9/-]*)?#D-(\d+)\b`)
)

// SettledRulesHeading is the docs heading whose lines the docs kind reads.
const SettledRulesHeading = "Settled rules"

// driftDocs lists the refs out of force on lines under a "Settled rules"
// heading in the markdown files under docsDir. A bare #D-n after
// <slug>#D-m on the same line is <slug>#D-n.
func driftDocs(docsDir, root string, x *DecisionIndex, today time.Time) ([]DriftFinding, error) {
	var out []DriftFinding
	err := filepath.WalkDir(docsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == docsDir && errors.Is(err, fs.ErrNotExist) {
				return filepath.SkipDir
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		name := path
		if root != "" {
			if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
				name = filepath.ToSlash(rel)
			}
		}
		out = append(out, settledRuleDrift(name, string(data), x, today)...)
		return nil
	})
	return out, err
}

func settledRuleDrift(file, text string, x *DecisionIndex, today time.Time) []DriftFinding {
	if !strings.Contains(text, "#D-") {
		return nil
	}
	lines := strings.Split(text, "\n")
	fence := computeFenceFlags(lines)
	var out []DriftFinding
	inLevel := 0 // the Settled rules heading's level while inside it
	for i, line := range lines {
		if fence[i] {
			continue
		}
		if m := headingLineRe.FindStringSubmatch(line); m != nil {
			level := len(m[1])
			if inLevel > 0 && level <= inLevel {
				inLevel = 0
			}
			if strings.EqualFold(strings.TrimSpace(m[2]), SettledRulesHeading) {
				inLevel = level
			}
			continue
		}
		if inLevel == 0 || !strings.Contains(line, "#D-") {
			continue
		}
		seen := map[string]bool{}
		last := ""
		for _, m := range docsRefRe.FindAllStringSubmatch(replacesParenRe.ReplaceAllString(line, ""), -1) {
			slug := last
			if m[1] != "" {
				slug = resolveRefSlug(m[1], x.known)
				last = slug
			}
			if slug == "" {
				continue
			}
			ref := slug + "#D-" + m[2]
			e := x.byRef[ref]
			if e == nil || seen[ref] {
				continue
			}
			seen[ref] = true
			f := DriftFinding{Kind: DriftDocs, Ref: ref, Text: e.Text, File: file, Line: i + 1}
			switch {
			case e.ReplacedBy != nil:
				f.ReplacedBy = e.ReplacedBy.Ref
				f.Fix = fmt.Sprintf("point %s:%d at %s", file, i+1, e.ReplacedBy.Ref)
			case e.ExpiredOn(today):
				f.Expired = e.Expires
				f.Fix = fmt.Sprintf("the rule at %s:%d rests on an expired decision: the owner renews it or the rule changes", file, i+1)
			default:
				continue
			}
			out = append(out, f)
		}
	}
	return out
}
