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

// naRefPat is one ref on an n/a line: a full ref, or after the first a bare
// #D-n that takes the slug of the ref before it; either may sit in backticks.
const naRefPat = "`?" + `(?:#?[a-z0-9][a-z0-9/-]*)?#D-\d+` + "`?"

// naLineRe is an n/a line in ## Design: "n/a <ref>[, <ref>…]: <why>", as a
// list item or a plain line. The reason may be empty; the rule refuses that.
var naLineRe = regexp.MustCompile(`^(?:[-*+]\s+)?(?:\*\*)?n/a(?:\*\*)?\s+(` + naRefPat + `(?:\s*,\s*` + naRefPat + `)*)\s*(?::\s*(.*))?$`)

// naStartRe is any line that starts as an n/a line: one naLineRe doesn't
// parse is a format problem, never a citation.
var naStartRe = regexp.MustCompile(`^(?:[-*+]\s+)?(?:\*\*)?n/a(?:\*\*)?(?:[\s:]|$)`)

// naRefRe picks the refs out of naLineRe's ref list.
var naRefRe = regexp.MustCompile(`(#?[a-z0-9][a-z0-9/-]*)?#D-(\d+)`)

// designRefs is what ## Design says about decisions: refs it cites (outside
// n/a lines), refs its n/a lines set aside (ref → reason, "" when none was
// given), and refs on lines that start with n/a but don't parse (ref → line).
type designRefs struct {
	cited map[string]bool
	na    map[string]string
	badNA map[string]string
}

// designCitations reads ## Design: the refs it cites (outside n/a lines),
// and its n/a lines, ref → reason ("" when none was given).
func designCitations(body string, known map[string]bool) (cited map[string]bool, na map[string]string) {
	d := readDesignCitations(body, known)
	return d.cited, d.na
}

// readDesignCitations reads ## Design into designRefs.
func readDesignCitations(body string, known map[string]bool) designRefs {
	d := designRefs{cited: map[string]bool{}, na: map[string]string{}, badNA: map[string]string{}}
	text, ok := SectionText(body, decisionCitedSection)
	if !ok {
		return d
	}
	var rest []string
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = refListLine(line)
		trimmed := strings.TrimSpace(line)
		if !naStartRe.MatchString(trimmed) {
			lines = append(lines, line)
			continue
		}
		lines = append(lines, naSentences(trimmed)...)
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		m := naLineRe.FindStringSubmatch(trimmed)
		if m == nil {
			if !naStartRe.MatchString(trimmed) {
				rest = append(rest, line)
				continue
			}
			for _, ref := range append(citedRefs(trimmed, known), shorthandRefs(trimmed, known)...) {
				if _, seen := d.badNA[ref]; !seen {
					d.badNA[ref] = trimmed
				}
			}
			continue
		}
		why := strings.TrimSpace(m[2])
		slug := ""
		for _, r := range naRefRe.FindAllStringSubmatch(m[1], -1) {
			if r[1] != "" {
				slug = resolveRefSlug(strings.TrimPrefix(r[1], "#"), known)
			}
			if slug == "" {
				continue
			}
			ref := slug + "#D-" + r[2]
			if _, seen := d.na[ref]; !seen || d.na[ref] == "" {
				d.na[ref] = why
			}
		}
	}
	kept := strings.Join(rest, "\n")
	for _, ref := range append(citedRefs(kept, known), shorthandRefs(kept, known)...) {
		d.cited[ref] = true
	}
	return d
}

// refListTailRe is one more ref in a comma list: "D-6" after "#D-5," (the
// backticks a ref may sit in kept), with no # of its own.
var refListTailRe = regexp.MustCompile("(D-\\d+`?\\s*,\\s*`?)D-(\\d+)\\b")

// refListLine is line with each bare D-n in a comma list after a ref
// written as #D-n, so "<slug>#D-5,D-6,D-7" reads as "<slug>#D-5,#D-6,#D-7"
// and shorthandRefs gives each the full ref's slug.
func refListLine(line string) string {
	if !strings.Contains(line, "#D-") || !strings.Contains(line, ",") {
		return line
	}
	for {
		next := refListTailRe.ReplaceAllString(line, "${1}#D-${2}")
		if next == line {
			return line
		}
		line = next
	}
}

// naNextRe is where a second n/a sentence starts on a line: after a "." or
// ";" that ends the one before it.
var naNextRe = regexp.MustCompile(`[.;]\s+(?:\*\*)?n/a(?:\*\*)?\s`)

// naSentences splits a line that starts with n/a into its n/a sentences,
// so "n/a a#D-5: x. n/a a#D-6: y." sets each ref aside with its own reason.
// Each sentence loses its closing "." or ";", so "n/a <ref>." is a bare n/a.
func naSentences(line string) []string {
	var out []string
	for {
		loc := naNextRe.FindStringIndex(line)
		if loc == nil {
			return append(out, strings.TrimRight(line, ". "))
		}
		out = append(out, strings.TrimSpace(line[:loc[0]]))
		line = strings.TrimSpace(line[loc[0]+1:])
	}
}

// shorthandRe is a bare #D-n; shorthandRefs drops the ones that are the end
// of a full ref or of a word.
var shorthandRe = regexp.MustCompile(`#D-(\d+)\b`)

// shorthandRefs resolves the bare #D-n refs in text: each takes the slug of
// a full ref on its own line, the nearest one before it, else the first
// after it. A bare ref on a line with no full ref of a known issue names
// nothing. Only ## Design reads these: anywhere else a bare #D-n may be the
// issue's own decision.
func shorthandRefs(text string, known map[string]bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, line := range refLines(text) {
		full := decisionRefRe.FindAllStringSubmatchIndex(line, -1)
		for _, m := range shorthandRe.FindAllStringSubmatchIndex(line, -1) {
			if m[0] > 0 && isWordByte(line[m[0]-1]) {
				continue
			}
			slug := ""
			for _, f := range full {
				s := resolveRefSlug(line[f[2]:f[3]], known)
				if s == "" {
					continue
				}
				if f[0] > m[0] {
					if slug == "" {
						slug = s
					}
					break
				}
				slug = s
			}
			if slug == "" {
				continue
			}
			ref := slug + "#D-" + line[m[2]:m[3]]
			if !seen[ref] {
				seen[ref] = true
				out = append(out, ref)
			}
		}
	}
	sort.Strings(out)
	return out
}

func isWordByte(c byte) bool {
	return c == '-' || c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
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
// names on an n/a line without a reason (BareNA), or on a line that starts
// with n/a but doesn't parse (BadNA: that line).
type UncitedDecision struct {
	Entry  InForceEntry
	BareNA bool
	BadNA  string
}

// UncitedDecisions is what decisions_cited holds the issue on at status:
// each hard decision in force, not the issue's own, that ## Design neither
// cites nor sets aside with "n/a <ref>: <why>".
func UncitedDecisions(x *DecisionIndex, issue *Issue, status string, o InForceOptions) []UncitedDecision {
	if x == nil || issue == nil {
		return nil
	}
	d := readDesignCitations(issue.BodyRaw, x.known)
	var out []UncitedDecision
	for _, f := range InForce(x, issue, status, o) {
		if !f.Entry.Hard || f.Why == "own" || d.cited[f.Entry.Ref] {
			continue
		}
		why, isNA := d.na[f.Entry.Ref]
		if isNA && why != "" {
			continue
		}
		u := UncitedDecision{Entry: f, BareNA: isNA}
		if !isNA {
			u.BadNA = d.badNA[f.Entry.Ref]
		}
		out = append(out, u)
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
		if m.BadNA != "" {
			why = "n/a line in ## Design doesn't parse"
		}
		holds = append(holds, DecisionHold{Rule: DecisionsCitedRule, Ref: m.Entry.Entry.Ref, Why: why})
	}
	if w.holdDecisions(holds) {
		return nil
	}
	return uncitedError(issue.Slug, missing)
}

// citedFormsHint lists the forms of ## Design that cite a decision.
const citedFormsHint = "Forms that count, each on one line:\n" +
	"  <slug>#D-5 · <slug>#D-5,D-6 · <slug>#D-5, #D-6 (a list takes the slug of the full ref before it)\n" +
	"  n/a <ref>: <why>. n/a <ref>[, <ref>…]: <why>. (each n/a sentence sets its own refs aside)\n"

func uncitedError(slug string, missing []UncitedDecision) *ruleFailure {
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
		if m.BadNA != "" {
			fmt.Fprintf(&b, "    its n/a line doesn't parse: %q; write \"n/a <ref>[, <ref>…]: <why>\"\n", m.BadNA)
		}
	}
	ref := missing[0].Entry.Entry.Ref
	var fix strings.Builder
	fix.WriteString(citedFormsHint)
	fmt.Fprintf(&fix, "Cite each in ## Design, saying how the design follows it:\n  issue-cli append %s --section Design --body \"Follows %s: …\"\n", slug, ref)
	fmt.Fprintf(&fix, "or say why it doesn't apply here:\n  issue-cli append %s --section Design --body \"n/a %s: <why>\"\n", slug, ref)
	fmt.Fprintf(&fix, DecisionChallengeHint, slug, ref)
	return &ruleFailure{problem: b.String(), fix: fix.String()}
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

func staleError(slug string, stale []StaleCitation) *ruleFailure {
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
	return &ruleFailure{problem: b.String(), fix: fmt.Sprintf(DecisionChallengeHint, slug, stale[0].Ref)}
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

// unknownRuleWarning is in lintRules' unknown-rule message.
const unknownRuleWarning = ": unknown validate rule "

// UnknownRuleWarning reports whether msg, one of WorkflowConfig.Lint's
// warnings, is the unknown-rule one: issue-cli health lint's schema check
// says the same with the file's line, so its workflow check skips it.
func UnknownRuleWarning(msg string) bool {
	return strings.Contains(msg, unknownRuleWarning)
}

// lintRules warns on a validate action whose rule this build doesn't know,
// and on a bad "since" date on a rule that takes one, in the base
// transitions, system overlays and types.
func (w *WorkflowConfig) lintRules() []string {
	var out []string
	seen := map[string]bool{}
	check := func(where string, ts []WorkflowTransition) {
		for _, t := range ts {
			for _, a := range t.Actions {
				if a.Type != "validate" || strings.TrimSpace(a.Rule) == "" {
					continue
				}
				name, arg := SplitRule(a.Rule)
				var msg string
				switch {
				case !KnownRule(name):
					msg = fmt.Sprintf("%s%s → %s"+unknownRuleWarning+"%q: the transition refuses with it (rules: issue-cli process schema)", where, t.From, t.To, name)
				case ruleTakesSince(name):
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
