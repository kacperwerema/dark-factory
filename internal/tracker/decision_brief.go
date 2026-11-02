package tracker

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// The decisions-in-force block: the decisions InForce returns, put in front
// of the agent where it already looks (issue-cli start and show, the worker
// briefing, the CEO and department lead briefs, the critic's brief). Hard
// decisions are never folded; soft ones fill what is left of the cap, the
// rest is one count line naming issue-cli decisions --in-force.

// DefaultDecisionBriefCap is the block's line cap when decisions: brief_cap:
// is unset.
const DefaultDecisionBriefCap = 12

// Cut lengths for one block line.
const (
	decisionBriefTextRunes  = 100
	decisionBriefQuoteRunes = 60
)

// DecisionsConfig is workflow.yaml's decisions: block.
type DecisionsConfig struct {
	BriefCap int `yaml:"brief_cap,omitempty" desc:"How many lines the decisions-in-force block in start, show and the briefings carries (default 12); hard decisions are always shown and count toward it, the rest is one 'and N more' line"`
}

// DecisionBriefCap is the workflow's block cap: decisions: brief_cap:, else
// 12.
func DecisionBriefCap(wf *WorkflowConfig) int {
	if wf != nil && wf.Decisions != nil && wf.Decisions.BriefCap > 0 {
		return wf.Decisions.BriefCap
	}
	return DefaultDecisionBriefCap
}

// DecisionBlock is the capped view of an in-force list: every hard
// decision, then the soft ones that fit, and how many were folded.
type DecisionBlock struct {
	Shown  []InForceEntry
	Folded []InForceEntry
}

// NewDecisionBlock folds items (InForce's order: hard first) to cap lines:
// every hard one is shown, soft ones fill what is left of cap.
func NewDecisionBlock(items []InForceEntry, cap int) DecisionBlock {
	if cap <= 0 {
		cap = DefaultDecisionBriefCap
	}
	var b DecisionBlock
	hard := 0
	for _, it := range items {
		if it.Entry.Hard {
			hard++
		}
	}
	room := cap - hard
	for _, it := range items {
		switch {
		case it.Entry.Hard:
			b.Shown = append(b.Shown, it)
		case room > 0:
			b.Shown = append(b.Shown, it)
			room--
		default:
			b.Folded = append(b.Folded, it)
		}
	}
	return b
}

// Empty reports whether nothing is in force.
func (b DecisionBlock) Empty() bool { return len(b.Shown)+len(b.Folded) == 0 }

// Refs is every ref in force, shown then folded.
func (b DecisionBlock) Refs() []string {
	var out []string
	for _, it := range append(append([]InForceEntry{}, b.Shown...), b.Folded...) {
		out = append(out, it.Entry.Ref)
	}
	return out
}

// DecisionBriefLine is one decision's line in the block:
//
//   - <ref> · hard|soft · <scope> · <text, one line>[ — "<short quote>"]
func DecisionBriefLine(e *DecisionEntry) string {
	strength := "soft"
	if e.Hard {
		strength = "hard"
	}
	line := fmt.Sprintf("- %s · %s · %s · %s", e.Ref, strength, e.ScopeOrDefault(), cutRunes(oneLine(e.Text), decisionBriefTextRunes))
	if q := oneLine(e.Quote); q != "" && len([]rune(q)) <= decisionBriefQuoteRunes {
		line += fmt.Sprintf(" — %q", q)
	}
	return line
}

// Lines renders the block's body (no heading): the shown lines, the count
// line when some were folded, or the empty line; then how to challenge.
// cmd is the command that lists them all (issue-cli decisions --in-force
// <slug>), subject what they are in force for ("this issue", "the project").
func (b DecisionBlock) Lines(cmd, subject string) []string {
	if b.Empty() {
		return []string{fmt.Sprintf("No decisions in force for %s: %s", subject, cmd)}
	}
	var out []string
	for _, it := range b.Shown {
		out = append(out, DecisionBriefLine(it.Entry))
	}
	if n := len(b.Folded); n > 0 {
		out = append(out, fmt.Sprintf("- and %d more: %s", n, cmd))
	}
	out = append(out, DecisionChallengeLine)
	return out
}

// DecisionChallengeLine says how to disagree with a decision in force.
// (Child 4's issue-cli decision challenge replaces the question: comment.)
const DecisionChallengeLine = "Disagree with one? Challenge it: issue-cli decision challenge <ref> --why \"…\" (your issue waits for the owner's answer); never work against a hard one."

// InForceCommand is the command the block names for slug.
func InForceCommand(slug string) string { return "issue-cli decisions --in-force " + slug }

// IssueDecisionBlock is the block for issue at status, from the project's
// issues. It never fails: an unreadable project gives the empty block.
func IssueDecisionBlock(proj *Project, issues []*Issue, issue *Issue, status string, wf *WorkflowConfig, today time.Time) DecisionBlock {
	if issue == nil {
		return DecisionBlock{}
	}
	// The same list the decisions_cited gate reads (ProjectInForce).
	return NewDecisionBlock(ProjectInForce(proj, issues, issue, status, today), DecisionBriefCap(wf))
}

// InForceLead returns the decisions in force for a lead: project scope for
// the CEO (department ""), project and department:<key> for a department's
// lead. at: is not filtered: a lead oversees every step. Same order as
// InForce.
func InForceLead(x *DecisionIndex, department string, today time.Time) []InForceEntry {
	if x == nil {
		return nil
	}
	key := ""
	if department != "" {
		key = DepartmentKey(department)
	}
	var out []InForceEntry
	for _, e := range x.Entries {
		if e.ReplacedBy != nil || e.Deferred || e.ExpiredOn(today) {
			continue
		}
		kind, name := e.ScopeKind()
		switch {
		case kind == ScopeProject:
			out = append(out, InForceEntry{Entry: e, Why: ScopeProject})
		case kind == ScopeDepartment && key != "" && strings.EqualFold(DepartmentKey(name), key):
			out = append(out, InForceEntry{Entry: e, Why: e.ScopeOrDefault()})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Entry, out[j].Entry
		if a.Hard != b.Hard {
			return a.Hard
		}
		ka, _ := a.ScopeKind()
		kb, _ := b.ScopeKind()
		return scopeRank[ka] < scopeRank[kb]
	})
	return out
}

// LeadDecisionBlock is the block for a lead (department "" is the CEO).
func LeadDecisionBlock(proj *Project, department string, wf *WorkflowConfig, today time.Time) DecisionBlock {
	var issues []*Issue
	if proj != nil && proj.IssueDir != "" {
		issues, _ = LoadIssues(proj.IssueDir)
	}
	index := BuildDecisionIndex(issues, ClosedIssueFunc(proj))
	return NewDecisionBlock(InForceLead(index, department, today), DecisionBriefCap(wf))
}

// DecisionsPromptSection is the "## Decisions in force" part of a worker's
// briefing, at the issue's status.
func DecisionsPromptSection(b DecisionBlock, slug string) string {
	var s strings.Builder
	s.WriteString("\n\n## Decisions in force\n\n")
	s.WriteString("The owner's decisions that reach this issue (hard first; a hard one is a rule, cite its ref when your work touches it):\n")
	for _, l := range b.Lines(InForceCommand(slug), "this issue") {
		s.WriteString(l + "\n")
	}
	return s.String()
}

// DecisionsLeadLines is the lead brief's decisions item: the project's (and
// for a department lead its department's) decisions in force.
func DecisionsLeadLines(b DecisionBlock, department string) string {
	scope, cmd := "project", "issue-cli decisions --lead ceo"
	if department != "" {
		scope, cmd = "project and department:"+DepartmentKey(department), "issue-cli decisions --lead "+DepartmentKey(department)
	}
	var s strings.Builder
	fmt.Fprintf(&s, "- Decisions in force (%s scope; hard ones are rules for every worker you start):\n", scope)
	for _, l := range b.Lines(cmd, "the "+scope+" scope") {
		s.WriteString("  " + l + "\n")
	}
	return s.String()
}

// Injected is one real briefing that carried the decisions block.
type Injected struct {
	Briefing string // start, dispatch, reprompt, lead, department-lead
	Issue    string // the issue slug; "" for a lead
	Actor    DecisionActor
	Refs     []string
}

// Briefings that write an injected event.
const (
	BriefingStart          = "start"
	BriefingDispatch       = "dispatch"
	BriefingReprompt       = "reprompt"
	BriefingLead           = "lead"
	BriefingDepartmentLead = "department-lead"
)

// DecisionInjected is the log event a real briefing writes.
const DecisionInjected = "injected"

// RecordInjected appends one injected event (op done) for a real briefing
// that carried at least one decision; nothing when none was in force or
// issueDir is "". Reads (show, decisions, dispatch-prompt, critique
// --brief) never call it.
func RecordInjected(issueDir string, in Injected, now time.Time) error {
	if issueDir == "" || len(in.Refs) == 0 {
		return nil
	}
	ev := DecisionEvent{Write: newWriteID(), Op: DecisionOpDone, Event: DecisionInjected, At: now.Format(time.RFC3339),
		By: in.Actor.Name, Role: in.Actor.Role, Briefing: in.Briefing, Issue: in.Issue, Refs: in.Refs}
	return AppendDecisionEvents(issueDir, []DecisionEvent{ev})
}

// WarnInjected records in and prints a warning on stderr when the log
// can't be written: a briefing never fails for it.
func WarnInjected(issueDir string, in Injected, now time.Time) {
	if err := RecordInjected(issueDir, in, now); err != nil {
		fmt.Fprintf(os.Stderr, "warning: the decisions log did not take the injected event: %v\n", err)
	}
}

// decisionsNow is the clock the briefing judges expiry by; a var for tests.
var decisionsNow = time.Now

func cutRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
