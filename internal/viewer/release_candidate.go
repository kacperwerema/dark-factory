package viewer

import (
	"encoding/json"
	"fmt"
	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"net/http"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/clibug"
	"github.com/kacperwerema/dark-factory/internal/milestone"
	"github.com/kacperwerema/dark-factory/internal/privacy"
	"github.com/kacperwerema/dark-factory/internal/telemetry"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Release candidates on the Waiting for release card
// (workbench.release_candidates): before a release is published, a local
// tag <cut>-rc.N is installed and runs on this machine's projects. The card
// shows the newest candidate and what was merged after it (each merge needs
// a new candidate), and on the engine project the builds running, which
// projects ran the candidate (their usage logs) and the report-bug issues
// filed from it. It never calls a soaked release ready: the owner's ok is
// the gate. Two more git calls, only with the key on.

// CandidateRow is the milestone's newest release candidate.
type CandidateRow struct {
	Tag   string // "v0.54.0-rc.2"
	N     int
	CutAt time.Time
	// Since counts the branch's commits after Tag that touch more than the
	// changelog; SinceKnown is false when git could not count them.
	Since      int
	SinceKnown bool
	// Next is the candidate after this one: "v0.54.0-rc.3".
	Next string
	// Soak is what the engine project adds; nil elsewhere.
	Soak *SoakRow
}

// SoakRow is the candidate's soak on this machine, from the engine
// project's view.
type SoakRow struct {
	// Viewer is this viewer's build; CLI the newest issue-cli build in any
	// project's usage log ("" when none records one).
	Viewer string
	CLI    string
	CLIAt  time.Time
	// Projects ran the candidate (Calls > 0), have not yet, or keep no
	// usage log (Log false).
	Projects []SoakProject
	// Reports are open report-bug issues filed from one of the milestone's
	// candidates; Fixed are those closed after the newest candidate was cut,
	// whose fixes ship in the next one.
	Reports []SoakReport
	Fixed   []SoakReport
	// MovedOut are reports from a candidate whose version the owner moved
	// past this release (ownerMovedOut): they no longer hold the cut.
	MovedOut []SoakReport
}

// SoakProject is one configured project's use of the candidate.
type SoakProject struct {
	Name, Slug string
	Log        bool
	Calls      int
	Last       time.Time
}

// SoakReport is a report-bug issue and the candidate it was found in.
type SoakReport struct {
	Issue *tracker.Issue
	Found string // "rc.1"
	// MovedTo is the version a moved-out report went to ("0.66");
	// Unapproved the version of a report moved without the owner's ok
	// recorded, which still holds the cut.
	MovedTo    string
	Unapproved string
}

// ViewerStale and CLIStale report a running build that is not the
// candidate itself (a later commit, a dirty tree, an older candidate).
func (c *CandidateRow) ViewerStale() bool {
	return c.Soak != nil && c.Soak.Viewer != c.Tag
}

func (c *CandidateRow) CLIStale() bool {
	return c.Soak != nil && c.Soak.CLI != "" && c.Soak.CLI != c.Tag
}

// Ran, NotYet and NoLog split the projects for the page.
func (s *SoakRow) Ran() []SoakProject { return s.pick(func(p SoakProject) bool { return p.Calls > 0 }) }
func (s *SoakRow) NotYet() []SoakProject {
	return s.pick(func(p SoakProject) bool { return p.Log && p.Calls == 0 })
}
func (s *SoakRow) NoLog() []SoakProject { return s.pick(func(p SoakProject) bool { return !p.Log }) }

func (s *SoakRow) pick(keep func(SoakProject) bool) []SoakProject {
	var out []SoakProject
	for _, p := range s.Projects {
		if keep(p) {
			out = append(out, p)
		}
	}
	return out
}

// rcTag is one local candidate tag and when it was made.
type rcTag struct {
	Name string
	At   time.Time
}

// gitRCTags lists cut's local candidate tags. A var so tests don't run git.
var gitRCTags = func(root, cut string) []rcTag {
	return refsKept(root, []string{"rc-tags", cut}, func() []rcTag {
		if gitdir.Outside(root) {
			return nil
		}
		out, err := exec.Command("git", "-C", root, "for-each-ref",
			"--format=%(refname:short)%00%(creatordate:unix)", "refs/tags/"+cut+"-rc.*").Output()
		if err != nil {
			return nil
		}
		var tags []rcTag
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			f := strings.Split(line, "\x00")
			if len(f) != 2 {
				continue
			}
			t := rcTag{Name: f[0]}
			if unix, err := strconv.ParseInt(f[1], 10, 64); err == nil {
				t.At = time.Unix(unix, 0)
			}
			tags = append(tags, t)
		}
		return tags
	})
}

// gitCommitsSince counts the commits from tag to head that touch a file
// other than the changelog. A var so tests don't run git.
var gitCommitsSince = func(root, tag, head, changelog string) (int, bool) {
	if head == "" {
		head = "HEAD"
	}
	c := refsKept(root, []string{"since", tag, head, changelog}, func() refsCount {
		args := []string{"-C", root, "rev-list", "--count", tag + ".." + head, "--", "."}
		if changelog != "" {
			args = append(args, ":(exclude)"+changelog)
		}
		out, err := exec.Command("git", args...).Output()
		if err != nil {
			return refsCount{}
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(out)))
		return refsCount{n, err == nil}
	})
	return c.n, c.ok
}

// refsCount is a kept count and whether git could give it.
type refsCount struct {
	n  int
	ok bool
}

// gitHasBranch reports whether root has the local branch. A var so tests
// don't run git.
var gitHasBranch = func(root, branch string) bool {
	return refsKept(root, []string{"has-branch", branch}, func() bool {
		return exec.Command("git", "-C", root, "rev-parse", "-q", "--verify", "refs/heads/"+branch).Run() == nil
	})
}

// gitUnpicked is, for each done issue's slug with commits the release
// branch lacks, how many (privacy.UnpickedIssues: its Refs: commits on
// main, else work/<slug>'s; picked by patch or by a release-branch
// commit's "(cherry picked from commit <sha>)" line). A work branch that
// merged main carries other issues' commits and its own dropped ones, so
// it is read only for an issue with no Refs: commit on main. A var so
// tests don't run git.
var gitUnpicked = func(root, branch string, slugs []string) map[string]int {
	return refsKept(root, append([]string{"unpicked", branch}, slugs...), func() map[string]int {
		mainBranch, err := tracker.MainBranch(root)
		if err != nil || mainBranch == "" {
			return nil
		}
		lacking, err := privacy.UnpickedIssues(root, branch, mainBranch, slugs)
		if err != nil {
			return nil
		}
		res := map[string]int{}
		for s, shas := range lacking {
			if len(shas) > 0 {
				res[s] = len(shas)
			}
		}
		return res
	})
}

// gitNoRefs is main's commits the release branch lacks that have no Refs:
// line (privacy.NoRefs). A var so tests don't run git.
var gitNoRefs = func(root, branch, changelog string) []privacy.NoRefsCommit {
	return refsKept(root, []string{"no-refs", branch, changelog}, func() []privacy.NoRefsCommit {
		mainBranch, err := tracker.MainBranch(root)
		if err != nil || mainBranch == "" {
			return nil
		}
		shas, err := privacy.NoRefs(root, branch, mainBranch, changelog)
		if err != nil {
			return nil
		}
		return shas
	})
}

// addPicked adds the Fixes picked check: every done issue of the milestone
// has its commits on the release branch. Fixes land on main and are
// cherry-picked onto the branch (issue-cli release pick).
func (ms *MilestoneRow) addPicked(root string, items []*tracker.Issue, cs *canvasStatuses) {
	var slugs []string
	for _, issue := range items {
		if statusKey(issue.Status) == cs.final {
			slugs = append(slugs, issue.Slug)
		}
	}
	unpicked := gitUnpicked(root, ms.Branch, slugs)
	ms.Unpicked = nil
	for _, s := range slugs {
		if unpicked[s] > 0 {
			ms.Unpicked = append(ms.Unpicked, s)
		}
	}
	check := ReleaseCheck{Key: "picked", Text: "Every done " + ms.Version + " fix is on " + ms.Branch, OK: len(ms.Unpicked) == 0}
	if check.OK {
		check.Meta = "all"
	} else {
		check.Meta = fmt.Sprintf("%d to pick", len(ms.Unpicked))
		check.Detail = "issue-cli release pick " + strings.Join(ms.Unpicked, " ")
	}
	ms.Checks = append(ms.Checks, check)
}

// findCandidate is the newest candidate tag for cut, with the commits
// merged after it; nil without one.
func findCandidate(root, cut, head, changelog string) *CandidateRow {
	tags := gitRCTags(root, cut)
	names := make([]string, len(tags))
	for i, t := range tags {
		names[i] = t.Name
	}
	rc, ok := milestone.NewestRC(names, cut)
	if !ok {
		return nil
	}
	c := &CandidateRow{Tag: rc.Tag, N: rc.N, Next: milestone.RCTag(cut, rc.N+1)}
	for _, t := range tags {
		if t.Name == rc.Tag {
			c.CutAt = t.At
		}
	}
	c.Since, c.SinceKnown = gitCommitsSince(root, rc.Tag, head, changelog)
	return c
}

// addCandidate turns the milestone into one released through candidates:
// it finds the newest candidate and adds the soaked check.
func (ms *MilestoneRow) addCandidate(root, head, changelog string) {
	ms.RC = true
	ms.Candidate = findCandidate(root, ms.Cut, head, changelog)
	c := ms.Candidate
	if c == nil {
		ms.finishRC()
		return
	}
	check := ReleaseCheck{Key: "soaked", Text: "Nothing merged after " + c.Tag + " but the changelog"}
	if ms.Branch != "" {
		check.Text = "Nothing picked onto " + ms.Branch + " after " + c.Tag + " but the changelog"
	}
	switch {
	case !c.SinceKnown:
		check.Meta, check.Detail = "?", "can't count the commits since "+c.Tag
	case c.Since == 0:
		check.OK, check.Meta = true, "0 since"
	default:
		check.Meta = fmt.Sprintf("%d since", c.Since)
		check.Detail = fmt.Sprintf("%s since %s: cut %s", plural(c.Since, "commit", "commits"), c.Tag, c.Next)
		if ms.Branch != "" {
			check.Detail += " (issue-cli release cut " + ms.Cut + ")"
		}
	}
	ms.Checks = append(ms.Checks, check)
	if ms.Branch != "" {
		ms.Checks = append(ms.Checks, validatedCheck(root, ms.Branch, ms.Cut, c.Tag))
	}
	ms.finishRC()
}

// validatedCheck passes when release check validated the candidate: the
// commit it recorded is the candidate tag's or the branch tip's (a
// changelog-only commit after the tag needs no new check). It is what a
// publish reads instead of main's head.
func validatedCheck(root, branch, cut, tag string) ReleaseCheck {
	c := ReleaseCheck{Key: "validated", Text: "Candidate " + tag + " validated in the release worktree"}
	done := releaseValidatedTip(root, branch)
	tagAt, tip := gitTip(root, tag), gitTip(root, branch)
	switch {
	case done != "" && (sameSha(done, tagAt) || sameSha(done, tip)):
		c.OK, c.Meta = true, gitdir.ShortSHA(done)
	case done != "":
		c.Meta = "stale"
		c.Detail = fmt.Sprintf("validated at %s, not at %s: issue-cli release check %s", gitdir.ShortSHA(done), tag, cut)
	default:
		c.Meta = "not run"
		c.Detail = "not validated: issue-cli release check " + cut + " runs local CI's commands in the release worktree and records it"
	}
	return c
}

// finishRC settles what the lead does next: cut a candidate (CutNext) or,
// with a soaked candidate and every check met, ask the owner (Soaking).
func (ms *MilestoneRow) finishRC() {
	ms.Ready = true
	others := true
	for _, c := range ms.Checks {
		ms.Ready = ms.Ready && c.OK
		// The candidate's own checks wait for it: they never hold its cut.
		if c.Key != "soaked" && c.Key != "validated" {
			others = others && c.OK
		}
	}
	ms.CutNext, ms.Soaking = "", false
	switch c := ms.Candidate; {
	case !others:
	case c == nil:
		ms.CutNext = milestone.RCTag(ms.Cut, 1)
	case ms.Ready:
		ms.Soaking = true
	case c.SinceKnown && c.Since > 0:
		ms.CutNext = c.Next
	}
}

// soakInput is what the engine project's card reads beyond its own repo.
type soakInput struct {
	projects []tracker.Project
	issues   []*tracker.Issue // the engine board
	cs       *canvasStatuses
	viewer   string
}

// addSoak fills the engine project's soak rows and adds the reports check.
func (ms *MilestoneRow) addSoak(in soakInput) {
	c := ms.Candidate
	if c == nil {
		return
	}
	s := &SoakRow{Viewer: in.viewer}
	for i := range in.projects {
		p := &in.projects[i]
		sp := SoakProject{Name: p.Name, Slug: p.Slug}
		path := ""
		if p.TelemetryEnabled() {
			if root := p.TelemetryRoot(); root != "" {
				path = telemetry.ProjectPath(root)
			}
		}
		if path != "" && (fileExists(path) || fileExists(path+".1")) {
			sp.Log = true
			if use, err := telemetry.ScanVersion(path, c.Tag); err == nil {
				sp.Calls, sp.Last = use.Calls, use.Last
				if use.NewestVer != "" && use.NewestAt.After(s.CLIAt) {
					s.CLI, s.CLIAt = use.NewestVer, use.NewestAt
				}
			}
		}
		s.Projects = append(s.Projects, sp)
	}
	sort.SliceStable(s.Projects, func(i, j int) bool { return s.Projects[i].Calls > s.Projects[j].Calls })

	for _, issue := range in.issues {
		if !issueHasLabel(issue, clibug.Label) {
			continue
		}
		found := foundInCandidate(issue, ms.Cut)
		if found == "" {
			continue
		}
		r := SoakReport{Issue: issue, Found: found}
		if to := issue.Version; to != "" && normVersion(to) != normVersion(ms.Version) {
			if ownerMovedOut(issue, to) {
				r.MovedTo = to
				s.MovedOut = append(s.MovedOut, r)
				continue
			}
			r.Unapproved = to
		}
		switch k := statusKey(issue.Status); {
		case !in.cs.closed(issue.Status):
			s.Reports = append(s.Reports, r)
		case k == in.cs.final:
			if at, _ := doneAt(issue); at.After(c.CutAt) {
				s.Fixed = append(s.Fixed, r)
			}
		}
	}
	c.Soak = s

	check := ReleaseCheck{Key: "reports", Text: "No open reports against " + ms.Cut + " candidates", OK: len(s.Reports) == 0}
	if check.OK {
		check.Meta = "none"
	} else {
		check.Meta = fmt.Sprintf("%d open", len(s.Reports))
		var slugs []string
		for _, r := range s.Reports {
			slug := r.Issue.Slug
			if r.Unapproved != "" {
				slug += " (version " + r.Unapproved + " without the owner's ok recorded: a decision or a lead comment quoting the owner)"
			}
			slugs = append(slugs, slug)
		}
		check.Detail = plural(len(s.Reports), "report", "reports") + " open: " + strings.Join(slugs, ", ")
	}
	if len(s.MovedOut) > 0 {
		var moved []string
		for _, r := range s.MovedOut {
			moved = append(moved, r.Issue.Slug+" → "+r.MovedTo)
		}
		check.Meta += fmt.Sprintf(", %d moved out", len(s.MovedOut))
		if check.Detail != "" {
			check.Detail += "; "
		}
		check.Detail += "moved out: " + strings.Join(moved, ", ")
	}
	ms.Checks = append(ms.Checks, check)
	ms.finishRC()
}

// soakVersionRe finds a version named in a decision or comment ("0.66",
// "v0.66.0").
var soakVersionRe = regexp.MustCompile(`\bv?\d+\.\d+(?:\.\d+)?\b`)

// quotedRe is a quoted phrase: the owner's words in a lead's comment.
var quotedRe = regexp.MustCompile(`"[^"]+"|“[^”]+”`)

// ownerMovedOut reports whether the move of a soak report to version to
// carries the owner's ok (docs/roadmap.md: moving a report out of the
// release needs the owner's ok, recorded as a decision): a decision naming
// to that has the owner's words (not provisional, quoted or confirmed), a
// comment from the owner naming it, or a lead's comment naming it that
// quotes the owner.
func ownerMovedOut(issue *tracker.Issue, to string) bool {
	names := func(text string) bool {
		for _, v := range soakVersionRe.FindAllString(text, -1) {
			if normVersion(v) == normVersion(to) {
				return true
			}
		}
		return false
	}
	for _, d := range issue.Decisions() {
		if !tracker.NeedsOwnerWords(d) && names(d.Text+" "+d.Quote) {
			return true
		}
	}
	for _, cm := range issue.Comments() {
		author := tracker.CommentAuthor(cm)
		switch {
		case !names(cm.Text):
		case author == "owner":
			return true
		case strings.HasPrefix(author, "lead-") && strings.Contains(strings.ToLower(cm.Text), "owner") && quotedRe.MatchString(cm.Text):
			return true
		}
	}
	return false
}

// reportVersionRe finds the build a report came from in its context lines
// ("- issue-cli: v0.54.0-rc.2 linux").
var reportVersionRe = regexp.MustCompile(`(?m)^- issue-cli: (\S+)`)

// foundInCandidate is the newest of cut's candidates the issue's reports
// came from ("rc.2"), from its body and report: comments; "" for none.
func foundInCandidate(issue *tracker.Issue, cut string) string {
	text := issue.BodyRaw
	for _, cm := range issue.Comments() {
		text += "\n" + cm.Text
	}
	best := 0
	for _, m := range reportVersionRe.FindAllStringSubmatch(text, -1) {
		if rc, ok := milestone.ParseRC(m[1]); ok && milestone.Norm(rc.Cut) == milestone.Norm(cut) && rc.N > best {
			best = rc.N
		}
	}
	if best == 0 {
		return ""
	}
	return "rc." + strconv.Itoa(best)
}

func issueHasLabel(issue *tracker.Issue, label string) bool {
	for _, l := range issue.Labels {
		if strings.EqualFold(l, label) {
			return true
		}
	}
	return false
}

// engineSlug is the engine project among projects (the one report-bug files
// on): engine_project when set, else the one whose repo is the engine's.
func engineSlug(cfg *tracker.ProjectsConfig) string {
	if p := clibug.FindEngine(cfg); p != nil {
		return p.Slug
	}
	return ""
}

// candidateMarkdown is the candidate's lines in status.md.
func candidateMarkdown(b *strings.Builder, c *CandidateRow, now time.Time) {
	fmt.Fprintf(b, "- Candidate %s: cut %s ago; ", c.Tag, shortDuration(now.Sub(c.CutAt)))
	switch {
	case !c.SinceKnown:
		b.WriteString("can't count the commits since\n")
	case c.Since == 0:
		b.WriteString("nothing merged since but the changelog\n")
	default:
		fmt.Fprintf(b, "%s merged since: they soak in %s\n", plural(c.Since, "commit", "commits"), c.Next)
	}
	s := c.Soak
	if s == nil {
		return
	}
	cli := s.CLI
	if cli == "" {
		cli = "unknown (no usage log records a build yet)"
	}
	fmt.Fprintf(b, "- Running: viewer %s, issue-cli %s", s.Viewer, cli)
	if c.ViewerStale() || c.CLIStale() {
		fmt.Fprintf(b, " (not the candidate: install %s and restart the viewer)", c.Tag)
	}
	b.WriteString("\n")
	var ran, not, nolog []string
	for _, p := range s.Ran() {
		ran = append(ran, fmt.Sprintf("%s (%s, last %s ago)", p.Slug, plural(p.Calls, "call", "calls"), shortDuration(now.Sub(p.Last))))
	}
	for _, p := range s.NotYet() {
		not = append(not, p.Slug)
	}
	for _, p := range s.NoLog() {
		nolog = append(nolog, p.Slug)
	}
	fmt.Fprintf(b, "- Ran %s: ", c.Tag)
	if len(ran) == 0 {
		b.WriteString("no project yet")
	} else {
		b.WriteString(strings.Join(ran, ", "))
	}
	if len(not) > 0 {
		b.WriteString("; not yet: " + strings.Join(not, ", "))
	}
	if len(nolog) > 0 {
		b.WriteString("; no usage log: " + strings.Join(nolog, ", "))
	}
	b.WriteString("\n")
	if len(s.Reports) > 0 {
		fmt.Fprintf(b, "- Open reports from candidates (%d):\n", len(s.Reports))
		for _, r := range s.Reports {
			fmt.Fprintf(b, "  - %s (found in %s, %s)\n", r.Issue.Slug, r.Found, r.Issue.Status)
		}
	}
	if len(s.Fixed) > 0 {
		var slugs []string
		for _, r := range s.Fixed {
			slugs = append(slugs, r.Issue.Slug)
		}
		fmt.Fprintf(b, "- Fixed since %s, soak in %s: %s\n", c.Tag, c.Next, strings.Join(slugs, ", "))
	}
	if len(s.MovedOut) > 0 {
		var moved []string
		for _, r := range s.MovedOut {
			moved = append(moved, r.Issue.Slug+" → "+r.MovedTo)
		}
		fmt.Fprintf(b, "- Moved out on the owner's ok: %s\n", strings.Join(moved, ", "))
	}
}

// releaseJSON is /p/<project>/release.json: the Waiting for release card's
// milestone, for issue-cli release cut and publish, which refuse on the
// checks the card shows.
type releaseJSON struct {
	Milestone *releaseJSONMilestone `json:"milestone"`
	// Patch is a patch release's candidate on an existing release branch.
	Patch *releaseJSONMilestone `json:"patch,omitempty"`
}

type releaseJSONMilestone struct {
	Version   string   `json:"version"`
	Cut       string   `json:"cut"`
	RC        bool     `json:"rc"`
	Ready     bool     `json:"ready"`
	CutNext   string   `json:"cut_next,omitempty"`
	Soaking   bool     `json:"soaking"`
	Candidate string   `json:"candidate,omitempty"`
	Branch    string   `json:"branch,omitempty"`
	Unpicked  []string `json:"unpicked,omitempty"`
	// NoRefs are main's commits the branch lacks that no release pick
	// <slug> finds (MilestoneRow.NoRefs).
	NoRefs []privacy.NoRefsCommit `json:"no_refs,omitempty"`
	Checks []ReleaseCheck         `json:"checks"`
}

func buildReleaseJSON(m *StatusModel) releaseJSON {
	var out releaseJSON
	if m.ReleaseWait == nil {
		return out
	}
	out.Milestone = releaseJSONRow(m.ReleaseWait.Milestone)
	out.Patch = releaseJSONRow(m.ReleaseWait.Patch)
	return out
}

func releaseJSONRow(ms *MilestoneRow) *releaseJSONMilestone {
	if ms == nil {
		return nil
	}
	row := &releaseJSONMilestone{Version: ms.Version, Cut: ms.Cut, RC: ms.RC, Ready: ms.Ready, CutNext: ms.CutNext,
		Soaking: ms.Soaking, Branch: ms.Branch, Unpicked: ms.Unpicked, NoRefs: ms.NoRefs, Checks: ms.Checks}
	if ms.Candidate != nil {
		row.Candidate = ms.Candidate.Tag
	}
	return row
}

func (s *Server) handleReleaseJSON(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	m, err := s.loadStatus(proj, prefix)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(buildReleaseJSON(m))
}
