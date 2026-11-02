package tracker

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The decision ledger's diff audit (docs/workflow.md#decision-ledger):
//
//	decisions_respected[: since YYYY-MM-DD] — every active hard decision
//	    whose paths: the branch's own changes touch is cited in the body
//	    (an n/a line doesn't count), and this issue has no open challenge
//	    on it
//
// Paths only: no model reads the diff or the decision's text here
// (factory/decision-ledger-…#D-4). Scope and at: decide who is briefed,
// not which branches the guard skips, so every hard decision in the
// project counts.
const DecisionsRespectedRule = "decisions_respected"

// TouchedDecision is an active hard decision whose paths: the branch's
// changed files match, and the files that match.
type TouchedDecision struct {
	Entry *DecisionEntry
	Files []string
}

// TouchedDecisions is every hard decision in x that is in force somewhere
// (not replaced, not deferred, not expired on today), has paths:, and
// guards at least one of changed. Newest first, as the index.
func TouchedDecisions(x *DecisionIndex, changed []string, today time.Time) []TouchedDecision {
	if x == nil || len(changed) == 0 {
		return nil
	}
	var out []TouchedDecision
	for _, e := range x.Entries {
		if !e.Hard || len(e.Paths) == 0 || e.ReplacedBy != nil || e.Deferred || e.ExpiredOn(today) {
			continue
		}
		var files []string
		for _, f := range changed {
			for _, g := range e.Paths {
				if MatchGlob(g, f) {
					files = append(files, f)
					break
				}
			}
		}
		if len(files) > 0 {
			out = append(out, TouchedDecision{Entry: e, Files: files})
		}
	}
	return out
}

// RespectedHold is a touched decision the issue is held on: not cited, or
// cited while the issue's own challenge on it is open.
type RespectedHold struct {
	TouchedDecision
	Uncited    bool
	Challenged bool
}

// Why is the hold in a few words, naming the files.
func (h RespectedHold) Why() string {
	var reasons []string
	if h.Uncited {
		reasons = append(reasons, "not cited")
	}
	if h.Challenged {
		reasons = append(reasons, "this issue's challenge is open")
	}
	return fmt.Sprintf("touched (%s), %s", strings.Join(h.Files, ", "), strings.Join(reasons, ", "))
}

// RespectedHolds is what decisions_respected holds issue on, given the
// decisions the branch touches and the refs this issue has an open
// challenge on. The issue's own decisions count as cited.
func RespectedHolds(x *DecisionIndex, issue *Issue, touched []TouchedDecision, challenged map[string]bool) []RespectedHold {
	if x == nil || issue == nil {
		return nil
	}
	cited := BodyCitations(x, issue)
	var out []RespectedHold
	for _, t := range touched {
		h := RespectedHold{
			TouchedDecision: t,
			Uncited:         !cited[t.Entry.Ref] && t.Entry.Slug != issue.Slug,
			Challenged:      challenged[t.Entry.Ref],
		}
		if h.Uncited || h.Challenged {
			out = append(out, h)
		}
	}
	return out
}

// BodyCitations is the refs issue's body cites outside its own
// ## Decisions and outside "n/a <ref>: <why>" lines.
func BodyCitations(x *DecisionIndex, issue *Issue) map[string]bool {
	var kept []string
	for _, line := range strings.Split(bodyWithoutDecisions(issue.BodyRaw), "\n") {
		if !naLineRe.MatchString(strings.TrimSpace(line)) {
			kept = append(kept, line)
		}
	}
	out := map[string]bool{}
	for _, ref := range citedRefs(strings.Join(kept, "\n"), x.known) {
		out[ref] = true
	}
	return out
}

// openChallengesBy is the refs issue has an open challenge on, from the
// decisions log under issueDir (LoadOpenChallenges, child 4's reader).
func openChallengesBy(issueDir, slug string) (map[string]bool, error) {
	open, err := LoadOpenChallenges(issueDir)
	out := map[string]bool{}
	for _, c := range ChallengesBy(open, slug) {
		out[c.Ref] = true
	}
	return out, err
}

var hexSHARe = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// BranchDiff is the work branch's own changes: the files, the commits the
// diff ran between, and Warn when the audit could not run (no recorded
// base, no branch).
type BranchDiff struct {
	Files      []string
	Base, Head string
	Warn       string
}

// BranchChanges reads the files the issue's work branch changed. The head
// is refs/heads/work/<slug>, else the sidecar's last recorded branch head.
// The recorded base is the first dispatch's base_sha. The diff runs from
// the merge-base of the head and the current main branch, so a merge of
// main into the branch adds nothing; once the head is in main it runs from
// the recorded base. Values from the sidecar reach git only as hex shas
// (docs/API/security.md).
func BranchChanges(repoRoot string, issue *Issue) (BranchDiff, error) {
	stats, _ := LoadStats(issue.FilePath)
	d, ok := DispatchBase(stats)
	if !ok || !hexSHARe.MatchString(d.BaseSHA) {
		return BranchDiff{Warn: fmt.Sprintf("decisions_respected: %s has no recorded base (it wasn't cut by a dispatch): the diff audit didn't run", issue.Slug)}, nil
	}
	if repoRoot == "" {
		return BranchDiff{}, fmt.Errorf("decisions_respected needs the project's checkout, which this program doesn't know — run the transition with issue-cli")
	}
	head := ""
	if out, err := runGit(repoRoot, "rev-parse", "--verify", "--quiet", "refs/heads/work/"+issue.Slug+"^{commit}"); err == nil {
		head = strings.TrimSpace(out)
	}
	if head == "" {
		for i := len(stats.Transitions) - 1; i >= 0; i-- {
			if h := stats.Transitions[i].BranchHead; hexSHARe.MatchString(h) {
				head = h
				break
			}
		}
	}
	if head == "" {
		return BranchDiff{Warn: fmt.Sprintf("decisions_respected: no branch work/%s and no recorded branch head: the diff audit didn't run", issue.Slug)}, nil
	}
	from := d.BaseSHA
	if main, err := MainBranch(repoRoot); err == nil && main != "" && !strings.HasPrefix(main, "-") {
		if _, err := runGit(repoRoot, "merge-base", "--is-ancestor", head, main); err != nil {
			if mb, err := runGit(repoRoot, "merge-base", head, main); err == nil && strings.TrimSpace(mb) != "" {
				from = strings.TrimSpace(mb)
			}
		}
	}
	out, err := runGit(repoRoot, "diff", "--name-only", "--no-renames", from+"..."+head)
	if err != nil {
		return BranchDiff{Base: from, Head: head}, fmt.Errorf("decisions_respected: can't read the branch's changes (%s...%s): %v", shortSHA(from), shortSHA(head), err)
	}
	diff := BranchDiff{Base: from, Head: head}
	for _, f := range strings.Split(out, "\n") {
		if f = strings.TrimSpace(f); f != "" {
			diff.Files = append(diff.Files, f)
		}
	}
	sort.Strings(diff.Files)
	return diff, nil
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// RespectedReport is everything decisions_respected and decisions
// --touched read for one issue.
type RespectedReport struct {
	Diff       BranchDiff
	Touched    []TouchedDecision
	Holds      []RespectedHold
	Challenged map[string]bool
}

// CheckRespected runs the audit for issue: the branch's changes, the hard
// decisions they touch and the holds. issueDir is where the decisions log
// sits ("" reads no challenges).
func CheckRespected(x *DecisionIndex, repoRoot, issueDir string, issue *Issue, today time.Time) (RespectedReport, error) {
	var r RespectedReport
	diff, err := BranchChanges(repoRoot, issue)
	r.Diff = diff
	if err != nil || diff.Warn != "" {
		return r, err
	}
	r.Touched = TouchedDecisions(x, diff.Files, today)
	if len(r.Touched) == 0 {
		return r, nil
	}
	r.Challenged, err = openChallengesBy(issueDir, issue.Slug)
	if err != nil {
		return r, fmt.Errorf("decisions_respected: reading the decisions log: %v", err)
	}
	r.Holds = RespectedHolds(x, issue, r.Touched, r.Challenged)
	return r, nil
}

func (w *WorkflowConfig) checkDecisionsRespected(arg string, issue *Issue) error {
	since, err := parseRuleSince(DecisionsRespectedRule, arg)
	if err != nil {
		return err
	}
	if createdBefore(issue, since) {
		return nil
	}
	x, opts := w.decisionsInForce()
	if x == nil {
		return fmt.Errorf("decisions_respected needs the project's decisions, which this program doesn't load — run the transition with issue-cli")
	}
	today := opts.Today
	if today.IsZero() {
		today = time.Now()
	}
	r, err := CheckRespected(x, w.RepoRoot, w.IssuesRoot, issue, today)
	if err != nil {
		return err
	}
	if r.Diff.Warn != "" {
		w.addDecisionWarning(r.Diff.Warn)
		return nil
	}
	if len(r.Holds) == 0 {
		return nil
	}
	var holds []DecisionHold
	for _, h := range r.Holds {
		holds = append(holds, DecisionHold{Rule: DecisionsRespectedRule, Ref: h.Entry.Ref, Why: h.Why()})
	}
	if w.holdDecisions(holds) {
		return nil
	}
	return respectedError(issue.Slug, r.Holds)
}

func respectedError(slug string, holds []RespectedHold) error {
	var b strings.Builder
	noun := "decisions guard"
	if len(holds) == 1 {
		noun = "decision guards"
	}
	fmt.Fprintf(&b, "decisions_respected: the branch changes files %d hard %s:\n", len(holds), noun)
	for _, h := range holds {
		e := h.Entry
		fmt.Fprintf(&b, "  %s: %s\n", e.Ref, oneLineCut(e.Text, decisionTextCut))
		fmt.Fprintf(&b, "    files: %s  (paths: %s)\n", strings.Join(h.Files, ", "), strings.Join(e.Paths, ", "))
		if h.Uncited {
			b.WriteString("    not cited (an n/a line doesn't count for a decision the branch touches)\n")
		}
		if h.Challenged {
			b.WriteString("    this issue's challenge on it is open: the owner answers it (Needs you); until then the gate holds\n")
		}
	}
	ref := holds[0].Entry.Ref
	fmt.Fprintf(&b, "Cite each, saying how the change follows it:\n  issue-cli append %s --section Design --body \"Follows %s: …\"\n", slug, ref)
	b.WriteString("or, if the change goes against it, undo that part, or challenge it and wait for the owner.\n")
	fmt.Fprintf(&b, "See them all: issue-cli decisions --touched %s\n", slug)
	fmt.Fprintf(&b, DecisionChallengeHint, slug, ref)
	return fmt.Errorf("%s", b.String())
}

// DecisionWarnings is every warning line the ledger rules left on this
// config (the diff audit that didn't run), in the order found.
func (w *WorkflowConfig) DecisionWarnings() []string {
	if w == nil || w.decisionState == nil {
		return nil
	}
	rt := w.decisionState
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return append([]string(nil), rt.warnings...)
}

func (w *WorkflowConfig) addDecisionWarning(s string) {
	rt := w.decisionRT()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for _, have := range rt.warnings {
		if have == s {
			return
		}
	}
	rt.warnings = append(rt.warnings, s)
}
