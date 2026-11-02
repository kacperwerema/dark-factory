package tracker

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DecisionsSection is the issue section holding the D-n decision lines.
const DecisionsSection = "Decisions"

var decisionIDRe = regexp.MustCompile(`\*\*D-(\d+)\*\*`)

// Decisions are numbered lines under an issue's `## Decisions` section.
// `issue-cli decide` writes them. The project-wide decisions index is
// generated from these lines; nobody edits it by hand. Older lines written by
// the removed `issue-cli answer` ("answered in lead chat (Q-n)") still parse.
//
//	- **D-<n>** · <YYYY-MM-DD>[ · deferred] · <how>: <text>[, "<quote>"][ (also considered: …)][ (ref <ref>[, <ref>])][ (replaces <ref>[, <ref>])][ {<attribute group>}]
//
// The attribute group (decision_attrs.go) says whom the decision reaches and
// whether it is the owner's hard rule; a line without one is scope issue,
// soft.
//
// A ref is `<slug>#D-n` (or `D-n` inside the same issue). Any `<slug>#D-n`
// in another issue's body is a citation. "(ref …)" names what the decision
// answers outside the D-n scheme, such as a lead's provisional N-26
// (decide --ref); it is kept as written and is no citation.

const (
	DecisionActive   = "active"
	DecisionDeferred = "deferred"
	DecisionReplaced = "replaced"
	DecisionStale    = "stale" // replaced, but open issues still cite it

	decisionDateLayout = "2006-01-02"
	deferredMarker     = "deferred"
	defaultDecisionHow = "recorded by agent"
)

// Decision is one parsed `- **D-n** …` line.
type Decision struct {
	Slug           string    `json:"slug"`
	ID             string    `json:"id"`  // "D-3"
	Ref            string    `json:"ref"` // "sim/x#D-3"
	Num            int       `json:"-"`
	Date           time.Time `json:"date"`
	How            string    `json:"how"`
	Text           string    `json:"text"`
	Quote          string    `json:"quote,omitempty"`
	AlsoConsidered string    `json:"also_considered,omitempty"`
	Deferred       bool      `json:"deferred,omitempty"`
	Refs           []string  `json:"refs,omitempty"`
	Replaces       []string  `json:"replaces,omitempty"`
	DecisionAttrs
	// AttrsAt is where the attribute group starts in Raw, -1 without one.
	AttrsAt int    `json:"-"`
	Raw     string `json:"line"`
}

var (
	decisionLineRe     = regexp.MustCompile(`^- \*\*D-(\d+)\*\* · (\d{4}-\d{2}-\d{2}) · (.+)$`)
	decisionReplacesRe = regexp.MustCompile(`\s*\(replaces ([^()]+)\)$`)
	decisionRefsRe     = regexp.MustCompile(`\s*\(ref ([^()]+)\)$`)
	decisionAlsoRe     = regexp.MustCompile(`\s*\(also considered: (.+)\)$`)
	decisionQuoteRe    = regexp.MustCompile(`, ("(?:[^"\\]|\\.)*")$`)
	decisionRefRe      = regexp.MustCompile(`#?\b([a-z0-9][a-z0-9/-]*)#D-(\d+)\b`)
	sameIssueRefRe     = regexp.MustCompile(`^D-(\d+)$`)
	replacesParenRe    = regexp.MustCompile(`\(replaces [^()]*\)`)
)

// ParseDecisions returns the D-n lines directly under the issue's level-2
// `## Decisions` sections, in file order. Free-form items and lines inside
// fenced code blocks are skipped; for a repeated D-n the first one wins.
func ParseDecisions(slug, body string) []Decision {
	if !strings.Contains(body, "**D-") {
		return nil
	}
	lines := strings.Split(body, "\n")
	fence := computeFenceFlags(lines)
	seen := map[int]bool{}
	var out []Decision
	for _, sec := range findHeadingMatches(body, DecisionsSection) {
		if sec.Level != 2 {
			continue
		}
		for i := sec.StartLine + 1; i < sec.EndLine && i < len(lines); i++ {
			if fence[i] {
				continue
			}
			d, ok := parseDecisionLine(slug, lines[i])
			if !ok || seen[d.Num] {
				continue
			}
			seen[d.Num] = true
			out = append(out, d)
		}
	}
	return out
}

func parseDecisionLine(slug, line string) (Decision, bool) {
	line = strings.TrimSpace(line)
	m := decisionLineRe.FindStringSubmatch(line)
	if m == nil {
		return Decision{}, false
	}
	n, _ := strconv.Atoi(m[1])
	date, err := time.ParseInLocation(decisionDateLayout, m[2], time.Local)
	if err != nil {
		return Decision{}, false
	}
	d := Decision{Slug: slug, Num: n, ID: fmt.Sprintf("D-%d", n), Date: date, Raw: line}
	d.Ref = slug + "#" + d.ID

	// The group is the line's very last part; only then are the replaces,
	// also-considered and quote parts peeled off what is left.
	rest := m[3]
	if head, attrs, at := splitAttrsTail(rest); at >= 0 {
		rest = head
		d.DecisionAttrs = attrs
		d.AttrsAt = len(line) - len(m[3]) + at
	} else {
		d.AttrsAt = -1
		d.Scope = ScopeIssue
	}
	if after, ok := strings.CutPrefix(rest, deferredMarker+" · "); ok {
		d.Deferred = true
		rest = after
	}
	how, text, ok := strings.Cut(rest, ": ")
	if !ok {
		how, text = "", rest
	}
	d.How = strings.TrimSpace(how)

	if rm := decisionReplacesRe.FindStringSubmatchIndex(text); rm != nil {
		for _, ref := range strings.Split(text[rm[2]:rm[3]], ",") {
			if ref = NormalizeDecisionRef(slug, ref); ref != "" {
				d.Replaces = append(d.Replaces, ref)
			}
		}
		text = text[:rm[0]]
	}
	if fm := decisionRefsRe.FindStringSubmatchIndex(text); fm != nil {
		for _, r := range strings.Split(text[fm[2]:fm[3]], ",") {
			if r = strings.TrimSpace(r); r != "" {
				d.Refs = append(d.Refs, r)
			}
		}
		text = text[:fm[0]]
	}
	if am := decisionAlsoRe.FindStringSubmatchIndex(text); am != nil {
		d.AlsoConsidered = text[am[2]:am[3]]
		text = text[:am[0]]
	}
	if qm := decisionQuoteRe.FindStringSubmatchIndex(text); qm != nil {
		if q, err := strconv.Unquote(text[qm[2]:qm[3]]); err == nil {
			d.Quote = q
			text = text[:qm[0]]
		}
	}
	d.Text = strings.TrimSpace(text)
	return d, true
}

// NormalizeDecisionRef turns `D-2`, `#sim/x#D-2` or `sim/x#d-2` into
// `<slug>#D-n`. A bare `D-n` refers to the issue named by slug. It returns
// "" when ref is not a decision ref.
func NormalizeDecisionRef(slug, ref string) string {
	ref = strings.TrimSpace(ref)
	if m := sameIssueRefRe.FindStringSubmatch(strings.ToUpper(ref)); m != nil {
		return slug + "#D-" + m[1]
	}
	ref = strings.TrimPrefix(ref, "#")
	base, num, ok := strings.Cut(ref, "#")
	if !ok || base == "" {
		return ""
	}
	n, err := strconv.Atoi(strings.TrimPrefix(strings.ToUpper(num), "D-"))
	if err != nil || !strings.HasPrefix(strings.ToUpper(num), "D-") {
		return ""
	}
	return fmt.Sprintf("%s#D-%d", strings.ToLower(strings.TrimSuffix(base, ".md")), n)
}

// NewDecision is the input for AddDecision.
type NewDecision struct {
	Text     string
	How      string
	Quote    string
	Deferred bool
	Refs     []string // what it answers outside the D-n scheme, e.g. "N-26"
	Replaces []string // normalised refs
	Attrs    DecisionAttrs
}

// ValidateNewDecision checks a decision before it is written.
func ValidateNewDecision(d NewDecision) error {
	if oneLine(d.Text) == "" {
		return fmt.Errorf("decision text is required")
	}
	if strings.Contains(d.How, ":") {
		return fmt.Errorf("--how %q must not contain ':' (it separates how from the decision text)", d.How)
	}
	for _, r := range d.Refs {
		if oneLine(r) == "" || strings.ContainsAny(r, "(),\n") {
			return fmt.Errorf("--ref %q must be one non-empty word or id without parentheses or commas, e.g. N-26", r)
		}
	}
	return nil
}

// RenderDecisionLine formats a new decision as its `## Decisions` line.
func RenderDecisionLine(id string, d NewDecision, now time.Time) string {
	how := oneLine(d.How)
	if how == "" {
		how = defaultDecisionHow
	}
	line := fmt.Sprintf("- **%s** · %s · ", id, now.Format(decisionDateLayout))
	if d.Deferred {
		line += deferredMarker + " · "
	}
	text := oneLine(d.Text)
	if q := oneLine(d.Quote); q != "" {
		line += how + ": " + strings.TrimRight(text, ".") + fmt.Sprintf(", %q", q)
	} else {
		line += how + ": " + text
	}
	if len(d.Refs) > 0 {
		line += " (ref " + strings.Join(trimAll(d.Refs), ", ") + ")"
	}
	if len(d.Replaces) > 0 {
		line += " (replaces " + strings.Join(d.Replaces, ", ") + ")"
	}
	if g := RenderAttrs(d.Attrs); g != "" {
		line += " " + g
	}
	return line
}

// AddDecision appends a new D-n line to the issue's `## Decisions` section
// (created when missing) in one locked write and returns it parsed. It logs
// nothing; commands use AddDecisionAs.
func AddDecision(filePath, slug string, d NewDecision, now time.Time) (Decision, error) {
	return AddDecisionAs(filePath, slug, d, now, DecisionWrite{})
}

// AddDecisionAs is AddDecision with the write logged (a recorded event, and
// a replaced event for each ref it replaces) and, for a relayed owner act,
// the decision: comment in the same write.
func AddDecisionAs(filePath, slug string, d NewDecision, now time.Time, w DecisionWrite) (Decision, error) {
	if err := ValidateNewDecision(d); err != nil {
		return Decision{}, err
	}
	var line string
	err := writeDecision(filePath, w, now, func(body string) (decisionChange, error) {
		id := fmt.Sprintf("D-%d", nextNumber(decisionIDRe, body))
		line = RenderDecisionLine(id, d, now)
		ref := slug + "#" + id
		group := RenderAttrs(d.Attrs)
		c := decisionChange{Body: appendToSectionTight(body, DecisionsSection, line, false), RelayRef: ref}
		c.Events = append(c.Events, DecisionEvent{Event: DecisionRecorded, Ref: ref, New: group})
		for _, old := range d.Replaces {
			c.Events = append(c.Events, DecisionEvent{Event: DecisionReplaced, Ref: old, ByRef: ref})
		}
		c.RelayAction = "recorded"
		if d.Attrs.Hard {
			c.RelayAction = "recorded hard"
		}
		if len(d.Replaces) > 0 {
			c.RelayAction += ", replacing " + strings.Join(d.Replaces, ", ")
		}
		if group != "" {
			c.RelayAction += " " + group
		}
		return c, nil
	})
	if err != nil {
		return Decision{}, err
	}
	parsed, ok := parseDecisionLine(slug, line)
	if !ok {
		return Decision{}, fmt.Errorf("internal: wrote an unparseable decision line: %s", line)
	}
	return parsed, nil
}

// DecisionEntry is a decision with its project-wide state.
type DecisionEntry struct {
	Decision
	Issue       *Issue
	ReplacedBy  *DecisionEntry
	CitedOpen   []*Issue // open issues citing it, by slug
	CitedClosed []*Issue
	State       string
}

// Stale reports whether the decision is replaced but still cited by open issues.
func (e *DecisionEntry) Stale() bool { return e.State == DecisionStale }

// DecisionIndex is every decision in a project.
type DecisionIndex struct {
	Entries []*DecisionEntry // newest first
	byRef   map[string]*DecisionEntry
	cites   map[string][]string // citing slug → refs it cites
	known   map[string]bool     // every issue slug, for resolving refs
	issues  map[string]*Issue   // by slug, for the parent walk
}

// Lookup returns the entry for a normalised ref, or nil.
func (x *DecisionIndex) Lookup(ref string) *DecisionEntry {
	if x == nil {
		return nil
	}
	return x.byRef[ref]
}

// Cited returns the decisions the issue's body cites, in ref order.
func (x *DecisionIndex) Cited(slug string) []*DecisionEntry {
	var out []*DecisionEntry
	for _, ref := range x.cites[slug] {
		if e := x.byRef[ref]; e != nil {
			out = append(out, e)
		}
	}
	return out
}

// CitedIn returns the indexed decisions that text (a design doc section, say)
// cites with `<slug>#D-n`, by the same rules as issue citations.
func (x *DecisionIndex) CitedIn(text string) []*DecisionEntry {
	if x == nil {
		return nil
	}
	var out []*DecisionEntry
	for _, ref := range citedRefs(text, x.known) {
		if e := x.byRef[ref]; e != nil {
			out = append(out, e)
		}
	}
	return out
}

// BuildDecisionIndex parses every issue's decisions and resolves
// replacements and citations. isClosed tells open from closed issues (only
// open citers make a replaced decision stale).
func BuildDecisionIndex(issues []*Issue, isClosed func(*Issue) bool) *DecisionIndex {
	known := map[string]bool{}
	x := &DecisionIndex{byRef: map[string]*DecisionEntry{}, cites: map[string][]string{}, known: known, issues: map[string]*Issue{}}
	for _, issue := range issues {
		known[issue.Slug] = true
		x.issues[issue.Slug] = issue
		for _, d := range ParseDecisions(issue.Slug, issue.BodyRaw) {
			e := &DecisionEntry{Decision: d, Issue: issue}
			x.Entries = append(x.Entries, e)
			x.byRef[d.Ref] = e
		}
	}
	sort.SliceStable(x.Entries, func(i, j int) bool {
		a, b := x.Entries[i], x.Entries[j]
		if !a.Date.Equal(b.Date) {
			return a.Date.After(b.Date)
		}
		if a.Slug != b.Slug {
			return a.Slug < b.Slug
		}
		return a.Num > b.Num
	})

	// Newest replacer wins: entries are newest first, so keep the first seen.
	for _, e := range x.Entries {
		for _, ref := range e.Replaces {
			if old := x.byRef[ref]; old != nil && old != e && old.ReplacedBy == nil {
				old.ReplacedBy = e
			}
		}
	}

	for _, issue := range issues {
		refs := citedRefs(issue.BodyRaw, known)
		x.cites[issue.Slug] = refs
		for _, ref := range refs {
			e := x.byRef[ref]
			if e == nil || e.Slug == issue.Slug {
				continue
			}
			if isClosed != nil && isClosed(issue) {
				e.CitedClosed = append(e.CitedClosed, issue)
			} else {
				e.CitedOpen = append(e.CitedOpen, issue)
			}
		}
	}

	for _, e := range x.Entries {
		sort.Slice(e.CitedOpen, func(i, j int) bool { return e.CitedOpen[i].Slug < e.CitedOpen[j].Slug })
		sort.Slice(e.CitedClosed, func(i, j int) bool { return e.CitedClosed[i].Slug < e.CitedClosed[j].Slug })
		switch {
		case e.ReplacedBy != nil && len(e.CitedOpen) > 0:
			e.State = DecisionStale
		case e.ReplacedBy != nil:
			e.State = DecisionReplaced
		case e.Deferred:
			e.State = DecisionDeferred
		default:
			e.State = DecisionActive
		}
	}
	return x
}

// citedRefs returns the distinct `<slug>#D-n` refs in body, outside fenced
// code and `(replaces …)` parentheticals. A candidate that is not a known
// slug is retried without its leading path segments, so a pasted viewer URL
// (`…/issue/sim/x#D-1`) still resolves.
func citedRefs(body string, known map[string]bool) []string {
	if !strings.Contains(body, "#D-") {
		return nil
	}
	lines := strings.Split(body, "\n")
	fence := computeFenceFlags(lines)
	seen := map[string]bool{}
	var out []string
	for i, line := range lines {
		if fence[i] || !strings.Contains(line, "#D-") {
			continue
		}
		line = replacesParenRe.ReplaceAllString(line, "")
		for _, m := range decisionRefRe.FindAllStringSubmatch(line, -1) {
			slug := resolveRefSlug(m[1], known)
			if slug == "" {
				continue
			}
			ref := slug + "#D-" + m[2]
			if !seen[ref] {
				seen[ref] = true
				out = append(out, ref)
			}
		}
	}
	sort.Strings(out)
	return out
}

func resolveRefSlug(candidate string, known map[string]bool) string {
	for candidate != "" {
		if known[candidate] {
			return candidate
		}
		_, rest, ok := strings.Cut(candidate, "/")
		if !ok {
			return ""
		}
		candidate = rest
	}
	return ""
}

// ClosedIssueFunc reports an issue as closed when its status is `done` or
// sorts after it in the issue's workflow (e.g. `obsolete`). Without a `done`
// status, only the workflow's last status is closed.
func ClosedIssueFunc(proj *Project) func(*Issue) bool {
	workflows := map[string]*WorkflowConfig{}
	return func(issue *Issue) bool {
		key := issue.System + "|" + issue.Type
		wf, ok := workflows[key]
		if !ok {
			wf = proj.LoadWorkflowForIssue(issue)
			workflows[key] = wf
		}
		if wf == nil || len(wf.Statuses) == 0 {
			return strings.EqualFold(issue.Status, "done")
		}
		idx := wf.GetStatusIndex(issue.Status)
		if idx < 0 {
			return strings.EqualFold(issue.Status, "done")
		}
		done := wf.GetStatusIndex("done")
		if done < 0 {
			done = len(wf.Statuses) - 1
		}
		return idx >= done
	}
}

// appendToSectionTight appends content at the end of the first level-2
// section titled title, creating it at the end of the body when missing.
// With blankLine, blocks are separated by a blank line; otherwise list items
// (decisions) are kept as a tight list.
func appendToSectionTight(body, title, content string, blankLine bool) string {
	var sec *headingMatch
	if secs := findSectionHeadings(body, title, 2); len(secs) > 0 {
		sec = &secs[0]
	}
	if sec == nil {
		body = strings.TrimRight(body, "\n")
		if body != "" {
			body += "\n\n"
		}
		return body + "## " + title + "\n\n" + content + "\n"
	}
	lines := strings.Split(body, "\n")
	end := sec.EndLine
	for end > sec.StartLine+1 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	sep := []string{}
	if end == sec.StartLine+1 || blankLine {
		sep = []string{""}
	}
	out := append([]string(nil), lines[:end]...)
	out = append(out, sep...)
	out = append(out, strings.Split(content, "\n")...)
	if sec.EndLine < len(lines) {
		out = append(out, "")
	}
	out = append(out, lines[sec.EndLine:]...)
	return strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
}

func nextNumber(re *regexp.Regexp, body string) int {
	max := 0
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n > max {
			max = n
		}
	}
	return max + 1
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// trimAll trims each string.
func trimAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.TrimSpace(s)
	}
	return out
}
