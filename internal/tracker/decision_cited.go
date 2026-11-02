package tracker

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// The decision ledger's design-gate rules (docs/workflow.md#decision-ledger):
//
//	decisions_cited[: since YYYY-MM-DD]    — ## Design cites every hard
//	                                          decision in force for the issue
//	                                          at its status, or says
//	                                          "n/a <ref>: <why>"
//	citations_current[: since YYYY-MM-DD]  — no ref the body cites is
//	                                          replaced or expired
const (
	DecisionsCitedRule   = "decisions_cited"
	CitationsCurrentRule = "citations_current"
	decisionCitedSection = "Design"
	decisionTextCut      = 100
)

// DecisionChallengeHint is the refusal's last line: what an agent does when
// it thinks a decision is wrong. %[1]s is the issue's slug, %[2]s the ref.
var DecisionChallengeHint = `Think it's wrong? Challenge it; your issue waits for the owner's answer (%[1]s): issue-cli decision challenge %[2]s --why "…"`

// DecisionHold is one decision a ledger rule would hold the issue on. Under
// YOLO (WorkflowConfig.DeferDecisionHolds) the rule records it instead of
// failing, and issue-cli posts it as a yolo-hold: comment.
type DecisionHold struct {
	Rule string // decisions_cited, citations_current, decision_unchallenged
	Ref  string
	Why  string // "not cited in ## Design", "replaced by x#D-5"
}

// decisionRuntime is what the ledger rules read, built once per config.
type decisionRuntime struct {
	once  sync.Once
	index *DecisionIndex
	opts  InForceOptions
	mu    sync.Mutex
	holds []DecisionHold
	found []DecisionHold
	// warnings are lines a rule leaves when it couldn't run
	// (decisions_respected without a base); DecisionWarnings.
	warnings []string
}

// decisionsInForce returns the index and options the ledger rules use: the
// DecisionsInForce hook's, built once per config, or nil without a hook.
func (w *WorkflowConfig) decisionsInForce() (*DecisionIndex, InForceOptions) {
	if w.DecisionsInForce == nil {
		return nil, InForceOptions{}
	}
	rt := w.decisionRT()
	rt.once.Do(func() { rt.index, rt.opts = w.DecisionsInForce() })
	return rt.index, rt.opts
}

func (w *WorkflowConfig) decisionRT() *decisionRuntime {
	if w.decisionState == nil {
		w.decisionState = &decisionRuntime{}
	}
	return w.decisionState
}

// DecisionHolds is every hold the ledger rules deferred under YOLO on this
// config, once per rule and ref, in the order found.
func (w *WorkflowConfig) DecisionHolds() []DecisionHold {
	if w == nil || w.decisionState == nil {
		return nil
	}
	rt := w.decisionState
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return append([]DecisionHold(nil), rt.holds...)
}

// DecisionHoldsFound is every hold the ledger rules found on this config,
// deferred under YOLO or refused, once per rule and ref, in the order
// found: what a real transition logs as held events (RecordHolds).
func (w *WorkflowConfig) DecisionHoldsFound() []DecisionHold {
	if w == nil || w.decisionState == nil {
		return nil
	}
	rt := w.decisionState
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return append([]DecisionHold(nil), rt.found...)
}

// holdDecisions records holds a ledger rule found and reports whether the
// rule defers them (YOLO: DeferDecisionHolds) instead of refusing. Every
// ledger rule, children 4 and 5's too, goes through it so its holds reach
// the audit log.
func (w *WorkflowConfig) holdDecisions(holds []DecisionHold) bool {
	rt := w.decisionRT()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.found = appendHolds(rt.found, holds)
	if w.DeferDecisionHolds {
		rt.holds = appendHolds(rt.holds, holds)
	}
	return w.DeferDecisionHolds
}

func appendHolds(list, holds []DecisionHold) []DecisionHold {
	for _, h := range holds {
		dup := false
		for _, have := range list {
			if have.Rule == h.Rule && have.Ref == h.Ref {
				dup = true
				break
			}
		}
		if !dup {
			list = append(list, h)
		}
	}
	return list
}

// parseRuleSince reads a rule's "since YYYY-MM-DD" argument ("" for none).
func parseRuleSince(rule, arg string) (string, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", nil
	}
	m := proofSinceRe.FindStringSubmatch(arg)
	if m == nil {
		return "", fmt.Errorf("%s: want \"since YYYY-MM-DD\", got %q", rule, arg)
	}
	if _, err := time.Parse("2006-01-02", m[1]); err != nil {
		return "", fmt.Errorf("%s: %q is not a date", rule, m[1])
	}
	return m[1], nil
}

// createdBefore reports whether issue was created before since.
func createdBefore(issue *Issue, since string) bool {
	created := strings.TrimSpace(issue.Created)
	if created == "" || since == "" {
		return false
	}
	if len(created) > 10 {
		created = created[:10]
	}
	return created < since
}

// naLineRe is an n/a line in ## Design: "n/a <ref>: <why>", as a list item
// or a plain line. The reason may be empty; the rule refuses that.
var naLineRe = regexp.MustCompile(`^(?:[-*+]\s+)?(?:\*\*)?n/a(?:\*\*)?\s+` + "`?" + `(#?[a-z0-9][a-z0-9/-]*#D-\d+)` + "`?" + `\s*(?::\s*(.*))?$`)

// designCitations reads ## Design: the refs it cites (outside n/a lines),
// and its n/a lines, ref → reason ("" when none was given).
func designCitations(body string, known map[string]bool) (cited map[string]bool, na map[string]string) {
	cited, na = map[string]bool{}, map[string]string{}
	text, ok := SectionText(body, decisionCitedSection)
	if !ok {
		return cited, na
	}
	var rest []string
	for _, line := range strings.Split(text, "\n") {
		m := naLineRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			rest = append(rest, line)
			continue
		}
		slug, num, _ := strings.Cut(strings.TrimPrefix(m[1], "#"), "#D-")
		if s := resolveRefSlug(slug, known); s != "" {
			ref := s + "#D-" + num
			if _, seen := na[ref]; !seen || na[ref] == "" {
				na[ref] = strings.TrimSpace(m[2])
			}
		}
	}
	for _, ref := range citedRefs(strings.Join(rest, "\n"), known) {
		cited[ref] = true
	}
	return cited, na
}

// bodyWithoutDecisions is body with its level-2 ## Decisions sections cut:
// the issue's own D-n lines are its history, not citations to check.
func bodyWithoutDecisions(body string) string {
	matches := findHeadingMatches(body, DecisionsSection)
	if len(matches) == 0 {
		return body
	}
	lines := strings.Split(body, "\n")
	drop := make([]bool, len(lines))
	for _, m := range matches {
		if m.Level != 2 {
			continue
		}
		for i := m.StartLine; i < m.EndLine && i < len(lines); i++ {
			drop[i] = true
		}
	}
	var kept []string
	for i, l := range lines {
		if !drop[i] {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n")
}

// oneLineCut is s on one line, cut at n runes with "…".
func oneLineCut(s string, n int) string {
	s = oneLine(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// UncitedDecision is a hard decision in force that ## Design doesn't name,
// or names on an n/a line without a reason (BareNA).
type UncitedDecision struct {
	Entry  InForceEntry
	BareNA bool
}

// UncitedDecisions is what decisions_cited holds the issue on at status:
// each hard decision in force, not the issue's own, that ## Design neither
// cites nor sets aside with "n/a <ref>: <why>".
func UncitedDecisions(x *DecisionIndex, issue *Issue, status string, o InForceOptions) []UncitedDecision {
	if x == nil || issue == nil {
		return nil
	}
	cited, na := designCitations(issue.BodyRaw, x.known)
	var out []UncitedDecision
	for _, f := range InForce(x, issue, status, o) {
		if !f.Entry.Hard || f.Why == "own" || cited[f.Entry.Ref] {
			continue
		}
		why, isNA := na[f.Entry.Ref]
		if isNA && why != "" {
			continue
		}
		out = append(out, UncitedDecision{Entry: f, BareNA: isNA})
	}
	return out
}

// StaleCitation is a ref the body cites that is out of force: replaced
// (By) or expired (Expired, the last day).
type StaleCitation struct {
	Ref     string
	Entry   *DecisionEntry
	By      *DecisionEntry
	Expired string
}

// StaleCitations is what citations_current holds the issue on: each ref
// cited in the body outside its own ## Decisions that is replaced, or
// expired before today.
func StaleCitations(x *DecisionIndex, issue *Issue, today time.Time) []StaleCitation {
	if x == nil || issue == nil {
		return nil
	}
	var out []StaleCitation
	for _, ref := range citedRefs(bodyWithoutDecisions(issue.BodyRaw), x.known) {
		e := x.byRef[ref]
		switch {
		case e == nil:
		case e.ReplacedBy != nil:
			out = append(out, StaleCitation{Ref: ref, Entry: e, By: e.ReplacedBy})
		case e.ExpiredOn(today):
			out = append(out, StaleCitation{Ref: ref, Entry: e, Expired: e.Expires})
		}
	}
	return out
}

func (w *WorkflowConfig) checkDecisionsCited(arg string, issue *Issue) error {
	since, err := parseRuleSince(DecisionsCitedRule, arg)
	if err != nil {
		return err
	}
	if createdBefore(issue, since) {
		return nil
	}
	x, opts := w.decisionsInForce()
	if x == nil {
		return fmt.Errorf("decisions_cited needs the project's decisions, which this program doesn't load — run the transition with issue-cli")
	}
	missing := UncitedDecisions(x, issue, issue.Status, opts)
	if len(missing) == 0 {
		return nil
	}
	var holds []DecisionHold
	for _, m := range missing {
		why := "not cited in ## Design"
		if m.BareNA {
			why = "n/a without a reason in ## Design"
		}
		holds = append(holds, DecisionHold{Rule: DecisionsCitedRule, Ref: m.Entry.Entry.Ref, Why: why})
	}
	if w.holdDecisions(holds) {
		return nil
	}
	return uncitedError(issue.Slug, missing)
}

func uncitedError(slug string, missing []UncitedDecision) error {
	var b strings.Builder
	noun := "decisions"
	verb := "are"
	if len(missing) == 1 {
		noun, verb = "decision", "is"
	}
	fmt.Fprintf(&b, "decisions_cited: %d hard %s in force %s not cited in ## Design:\n", len(missing), noun, verb)
	for _, m := range missing {
		e := m.Entry.Entry
		fmt.Fprintf(&b, "  %s (%s): %s\n", e.Ref, m.Entry.Why, oneLineCut(e.Text, decisionTextCut))
		if m.BareNA {
			fmt.Fprintf(&b, "    its n/a line has no reason: write \"n/a %s: <why it doesn't apply here>\"\n", e.Ref)
		}
	}
	ref := missing[0].Entry.Entry.Ref
	fmt.Fprintf(&b, "Cite each in ## Design, saying how the design follows it:\n  issue-cli append %s --section Design --body \"Follows %s: …\"\n", slug, ref)
	fmt.Fprintf(&b, "or say why it doesn't apply here:\n  issue-cli append %s --section Design --body \"n/a %s: <why>\"\n", slug, ref)
	fmt.Fprintf(&b, DecisionChallengeHint, slug, ref)
	return fmt.Errorf("%s", b.String())
}

func (w *WorkflowConfig) checkCitationsCurrent(arg string, issue *Issue) error {
	since, err := parseRuleSince(CitationsCurrentRule, arg)
	if err != nil {
		return err
	}
	if createdBefore(issue, since) {
		return nil
	}
	x, opts := w.decisionsInForce()
	if x == nil {
		return fmt.Errorf("citations_current needs the project's decisions, which this program doesn't load — run the transition with issue-cli")
	}
	stale := StaleCitations(x, issue, opts.Today)
	if len(stale) == 0 {
		return nil
	}
	var holds []DecisionHold
	for _, s := range stale {
		holds = append(holds, DecisionHold{Rule: CitationsCurrentRule, Ref: s.Ref, Why: s.reason()})
	}
	if w.holdDecisions(holds) {
		return nil
	}
	return staleError(issue.Slug, stale)
}

func (s StaleCitation) reason() string {
	if s.By != nil {
		return "replaced by " + s.By.Ref
	}
	return "expired after " + s.Expired
}

func staleError(slug string, stale []StaleCitation) error {
	var b strings.Builder
	noun := "refs are"
	if len(stale) == 1 {
		noun = "ref is"
	}
	fmt.Fprintf(&b, "citations_current: %d cited decision %s no longer in force:\n", len(stale), noun)
	for _, s := range stale {
		if s.By != nil {
			fmt.Fprintf(&b, "  %s is replaced by %s: %s\n", s.Ref, s.By.Ref, oneLineCut(s.By.Text, decisionTextCut))
			fmt.Fprintf(&b, "    cite %s instead (issue-cli decisions --in-force %s), or write the history as \"(replaces %s)\"\n", s.By.Ref, slug, s.Ref)
		} else {
			fmt.Fprintf(&b, "  %s expired after %s: %s\n", s.Ref, s.Expired, oneLineCut(s.Entry.Text, decisionTextCut))
			fmt.Fprintf(&b, "    drop the citation, or ask the owner to renew it\n")
		}
	}
	fmt.Fprintf(&b, DecisionChallengeHint, slug, stale[0].Ref)
	return fmt.Errorf("%s", b.String())
}

// UsesDecisionRules reports whether the workflow runs decisions_cited,
// citations_current or decisions_respected on any transition.
func (w *WorkflowConfig) UsesDecisionRules() bool {
	if w == nil {
		return false
	}
	for _, t := range w.Transitions {
		if TransitionHasRule(t.Actions, DecisionsCitedRule) || TransitionHasRule(t.Actions, CitationsCurrentRule) || TransitionHasRule(t.Actions, DecisionsRespectedRule) {
			return true
		}
	}
	return false
}

// yoloHoldPrefix starts the comment a YOLO pass over a decision hold
// leaves; internal/yolo lists it in the after-the-fact queue.
const yoloHoldPrefix = "yolo-hold: "

var yoloHoldRe = regexp.MustCompile(`^yolo-hold: (\S+) (\S+#D-\d+) from (.+?): (.+)$`)

// YOLOHoldComment is the comment a transition writes for a hold YOLO
// passed:
//
//	yolo-hold: <rule> <ref> from <status>: <why>
func YOLOHoldComment(h DecisionHold, from string) string {
	return fmt.Sprintf("%s%s %s from %s: %s", yoloHoldPrefix, h.Rule, h.Ref, from, oneLine(h.Why))
}

// ParseYOLOHoldComment reads a comment written by YOLOHoldComment, and the
// status the issue left.
func ParseYOLOHoldComment(text string) (h DecisionHold, from string, ok bool) {
	m := yoloHoldRe.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return DecisionHold{}, "", false
	}
	return DecisionHold{Rule: m[1], Ref: m[2], Why: m[4]}, m[3], true
}

// knownRuleNames is every validate rule name WorkflowValidationRules lists.
func knownRuleNames() map[string]bool {
	out := map[string]bool{}
	for _, r := range WorkflowValidationRules {
		name, _, _ := strings.Cut(r.Name, ":")
		out[strings.TrimSpace(name)] = true
	}
	return out
}

// lintRules warns on a validate action whose rule this build doesn't know,
// and on a bad "since" date on a rule that takes one, in the base
// transitions, system overlays and types.
func (w *WorkflowConfig) lintRules() []string {
	known := knownRuleNames()
	var out []string
	seen := map[string]bool{}
	check := func(where string, ts []WorkflowTransition) {
		for _, t := range ts {
			for _, a := range t.Actions {
				if a.Type != "validate" || strings.TrimSpace(a.Rule) == "" {
					continue
				}
				name, arg, _ := strings.Cut(a.Rule, ":")
				name = strings.TrimSpace(name)
				var msg string
				switch {
				case !known[name] && !isStructuredRule(name):
					msg = fmt.Sprintf("%s%s → %s: unknown validate rule %q: the transition refuses with it (rules: issue-cli process schema)", where, t.From, t.To, name)
				case name == DecisionsCitedRule || name == CitationsCurrentRule || name == DecisionUnchallengedRule || name == DecisionsRespectedRule || name == criteriaProvenRule:
					if _, err := parseRuleSince(name, arg); err != nil {
						msg = fmt.Sprintf("%s%s → %s: %v", where, t.From, t.To, err)
					}
				}
				if msg != "" && !seen[msg] {
					seen[msg] = true
					out = append(out, msg)
				}
			}
		}
	}
	check("", w.Transitions)
	systems := make([]string, 0, len(w.Systems))
	for name := range w.Systems {
		systems = append(systems, name)
	}
	sort.Strings(systems)
	for _, name := range systems {
		check("system "+name+": ", w.Systems[name].Transitions)
	}
	for _, name := range w.TypeNames() {
		check("type "+name+": ", w.Types[name].Transitions)
	}
	return out
}

// ProjectInForceOptions is the InForce options for a project's issues at
// now: their department from projects.yaml, expiry against today.
func ProjectInForceOptions(p *Project, now time.Time) InForceOptions {
	o := InForceOptions{Today: now}
	if p != nil {
		o.Department = p.DepartmentOf
	}
	return o
}

// ProjectInForce is every decision in force for issue at status in p, the
// unfolded list the briefing block and the decisions_cited gate both read.
func ProjectInForce(p *Project, issues []*Issue, issue *Issue, status string, now time.Time) []InForceEntry {
	return InForce(BuildDecisionIndex(issues, nil), issue, status, ProjectInForceOptions(p, now))
}
