package tracker

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The clarify round: the design step writes every question for the owner
// once, under `## Clarify`, each with 2–4 options, what each costs, and the
// recommendation first. The owner answers them in one go (the Station's
// form, or the lead relaying with issue-cli clarify --answer); an answer the
// agent took alone is marked assumed. Other sections (the red-round and
// post-mortem questions) use the same block through ParseQuestions and
// AppendQuestion.
//
//	## Clarify
//
//	### Q1. Where does the owner answer?
//	1. **On the Station** (recommended) <why, citing the work>. Cost: one card.
//	2. **In each Needs you row.** Cost: a form per row.
//	**Answer:** —
//
// The answer line is `—` (open), `<n> · <YYYY-MM-DD> · owner: "<words>" (D-k)`
// (the owner's, also a D-k decision) or `assumed[ <n>] — <reason>` (the
// agent's alone; without <n> the recommendation). With nothing to ask the
// section is one line: `Nothing to ask: <why>.`

// ClarifySection is the design step's question section.
const ClarifySection = "Clarify"

// Answer states.
const (
	AnswerOpen    = "open"
	AnswerOwner   = "owner"
	AnswerAssumed = "assumed"
	AnswerOther   = "answered" // a hand-written answer in no known form
)

// ClarifyHow is the how of the D-n decision an answer records.
const ClarifyHow = "clarify round"

// DefaultAssumedReason is the reason --assume and the YOLO pass write.
const DefaultAssumedReason = "took the recommendation"

var (
	// ErrQuestionNotFound: the section has no such Q-n.
	ErrQuestionNotFound = errors.New("no such question")
	// ErrQuestionAnswered: the owner already answered it.
	ErrQuestionAnswered = errors.New("question already answered by the owner")
	// ErrQuestionChanged: the question's text is not what the caller saw.
	ErrQuestionChanged = errors.New("question changed since it was read")

	questionHeadRe = regexp.MustCompile(`^###\s+Q(\d+)[.:)]?\s+(.+?)\s*$`)
	questionIDRe   = regexp.MustCompile(`(?m)^###\s+Q(\d+)\b`)
	optionLineRe   = regexp.MustCompile(`^(\d+)[.)]\s+(.+)$`)
	optionLabelRe  = regexp.MustCompile(`^\*\*(.+?)\*\*`)
	recommendedRe  = regexp.MustCompile(`(?i)\s*\(recommended\)`)
	costRe         = regexp.MustCompile(`(?i)\bcost:\s*(.+?)\s*$`)
	answerLineRe   = regexp.MustCompile(`^\*\*Answer:\*\*\s*(.*)$`)
	answerOwnerRe  = regexp.MustCompile(`^(\d+) · (\d{4}-\d{2}-\d{2}) · ([^:"]+): ("(?:[^"\\]|\\.)*")(?: \((D-\d+)\))?\s*$`)
	answerAssumeRe = regexp.MustCompile(`(?i)^assumed(?:\s+(\d+))?\s*(?:[—–-]+\s*(.*))?$`)
	nothingRe      = regexp.MustCompile(`(?i)^nothing to ask\b[:.,—–-]*\s*(.*)$`)
)

// ClarifyOption is one numbered option of a question.
type ClarifyOption struct {
	Num         int    `json:"num"`
	Label       string `json:"label"`
	Cost        string `json:"cost,omitempty"`
	Why         string `json:"why,omitempty"`
	Recommended bool   `json:"recommended,omitempty"`
}

// ClarifyAnswer is a question's answer line.
type ClarifyAnswer struct {
	State    string `json:"state"`            // open, owner, assumed, answered
	Option   int    `json:"option,omitempty"` // the chosen option's number
	Words    string `json:"words,omitempty"`  // the owner's words
	Who      string `json:"who,omitempty"`
	Date     string `json:"date,omitempty"`
	Decision string `json:"decision,omitempty"` // "D-4"
	Reason   string `json:"reason,omitempty"`   // an assumed answer's reason
	Raw      string `json:"raw,omitempty"`
}

// ClarifyQuestion is one `### Q<n>.` block.
type ClarifyQuestion struct {
	ID      string          `json:"id"` // "Q1"
	Num     int             `json:"num"`
	Text    string          `json:"text"`
	Options []ClarifyOption `json:"options"`
	Answer  ClarifyAnswer   `json:"answer"`
	// HasAnswerLine is false when the block has no **Answer:** line (a
	// problem; it reads as open).
	HasAnswerLine bool `json:"-"`
	line          int  // the heading's line in the body
	answerLine    int  // the answer line, -1 without one
	lastLine      int  // the block's last non-blank line
}

// Open reports whether the question still waits for an answer.
func (q ClarifyQuestion) Open() bool { return q.Answer.State == AnswerOpen }

// Recommended is the recommended option, else the first, else nil.
func (q ClarifyQuestion) Recommended() *ClarifyOption {
	for i := range q.Options {
		if q.Options[i].Recommended {
			return &q.Options[i]
		}
	}
	if len(q.Options) > 0 {
		return &q.Options[0]
	}
	return nil
}

// OptionByNum is option n, or nil.
func (q ClarifyQuestion) OptionByNum(n int) *ClarifyOption {
	for i := range q.Options {
		if q.Options[i].Num == n {
			return &q.Options[i]
		}
	}
	return nil
}

// Chosen is the option the answer picked: the owner's or an assumed one's
// number, else the recommendation for an assumed answer; nil when open.
func (q ClarifyQuestion) Chosen() *ClarifyOption {
	if q.Answer.Option > 0 {
		return q.OptionByNum(q.Answer.Option)
	}
	if q.Answer.State == AnswerAssumed {
		return q.Recommended()
	}
	return nil
}

// Clarify is a parsed question section.
type Clarify struct {
	Found     bool              `json:"found"`
	Nothing   bool              `json:"nothing_to_ask,omitempty"`
	Why       string            `json:"why,omitempty"` // the Nothing to ask line's reason
	Questions []ClarifyQuestion `json:"questions"`
	Problems  []string          `json:"problems,omitempty"`
}

// Open is the questions still waiting for an answer.
func (c Clarify) Open() []ClarifyQuestion { return c.filter(AnswerOpen) }

// Assumed is the questions the agent answered alone.
func (c Clarify) Assumed() []ClarifyQuestion { return c.filter(AnswerAssumed) }

// Unanswered is the questions without the owner's answer: open or assumed.
func (c Clarify) Unanswered() []ClarifyQuestion {
	var out []ClarifyQuestion
	for _, q := range c.Questions {
		if q.Answer.State == AnswerOpen || q.Answer.State == AnswerAssumed {
			out = append(out, q)
		}
	}
	return out
}

// Question is the question with id ("Q2", "q2" or "2"), or nil.
func (c Clarify) Question(id string) *ClarifyQuestion {
	id = NormalizeQuestionID(id)
	for i := range c.Questions {
		if c.Questions[i].ID == id {
			return &c.Questions[i]
		}
	}
	return nil
}

func (c Clarify) filter(state string) []ClarifyQuestion {
	var out []ClarifyQuestion
	for _, q := range c.Questions {
		if q.Answer.State == state {
			out = append(out, q)
		}
	}
	return out
}

// NormalizeQuestionID turns "q2", "Q2" or "2" into "Q2"; "" when id is not one.
func NormalizeQuestionID(id string) string {
	id = strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(id)), "Q")
	n, err := strconv.Atoi(id)
	if err != nil || n <= 0 {
		return ""
	}
	return "Q" + strconv.Itoa(n)
}

// ParseClarify parses the issue's `## Clarify` section.
func ParseClarify(body string) Clarify { return ParseQuestionSection(body, ClarifySection) }

// ParseQuestions is the `### Q<n>.` questions of a level-2 section.
func ParseQuestions(body, section string) []ClarifyQuestion {
	return ParseQuestionSection(body, section).Questions
}

// ParseQuestionSection parses the first level-2 section named section:
// its questions, the "Nothing to ask" line and the format's problems.
// Lines inside fenced code blocks are skipped.
func ParseQuestionSection(body, section string) Clarify {
	var c Clarify
	secs := findSectionHeadings(body, section, 2)
	if len(secs) == 0 {
		return c
	}
	c.Found = true
	sec := secs[0]
	lines := strings.Split(body, "\n")
	fence := computeFenceFlags(lines)
	var cur *ClarifyQuestion
	flush := func() {
		if cur != nil {
			c.Questions = append(c.Questions, *cur)
			cur = nil
		}
	}
	for i := sec.StartLine + 1; i < sec.EndLine && i < len(lines); i++ {
		if fence[i] {
			continue
		}
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if m := questionHeadRe.FindStringSubmatch(line); m != nil {
			flush()
			n, _ := strconv.Atoi(m[1])
			cur = &ClarifyQuestion{ID: "Q" + m[1], Num: n, Text: m[2], line: i, answerLine: -1, lastLine: i, Answer: ClarifyAnswer{State: AnswerOpen}}
			continue
		}
		if cur == nil {
			if m := nothingRe.FindStringSubmatch(line); m != nil {
				c.Nothing = true
				c.Why = strings.TrimRight(strings.TrimSpace(m[1]), ".")
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			// A deeper heading inside a question: keep reading the block.
			cur.lastLine = i
			continue
		}
		cur.lastLine = i
		if m := answerLineRe.FindStringSubmatch(line); m != nil && !cur.HasAnswerLine {
			cur.HasAnswerLine = true
			cur.answerLine = i
			cur.Answer = parseAnswer(m[1])
			continue
		}
		if m := optionLineRe.FindStringSubmatch(line); m != nil && !cur.HasAnswerLine {
			n, _ := strconv.Atoi(m[1])
			cur.Options = append(cur.Options, parseOption(n, m[2]))
			continue
		}
		if k := len(cur.Options); k > 0 && !cur.HasAnswerLine {
			// A continuation of the last option.
			o := &cur.Options[k-1]
			if o.Cost == "" {
				if cm := costRe.FindStringSubmatchIndex(line); cm != nil {
					o.Cost = strings.TrimRight(line[cm[2]:cm[3]], ".")
					line = strings.TrimSpace(line[:cm[0]])
				}
			}
			if line != "" {
				o.Why = strings.TrimSpace(o.Why + " " + line)
			}
		}
	}
	flush()
	c.Problems = questionProblems(section, c)
	return c
}

func parseOption(n int, text string) ClarifyOption {
	o := ClarifyOption{Num: n}
	if recommendedRe.MatchString(text) {
		o.Recommended = true
		text = recommendedRe.ReplaceAllString(text, "")
	}
	if cm := costRe.FindStringSubmatchIndex(text); cm != nil {
		o.Cost = strings.TrimRight(text[cm[2]:cm[3]], ".")
		text = strings.TrimSpace(text[:cm[0]])
	}
	if lm := optionLabelRe.FindStringSubmatchIndex(text); lm != nil {
		o.Label = strings.TrimRight(strings.TrimSpace(text[lm[2]:lm[3]]), ".")
		o.Why = strings.TrimSpace(strings.TrimLeft(text[lm[1]:], " .:—–-"))
	} else {
		label, why, _ := strings.Cut(text, ". ")
		o.Label = strings.TrimRight(strings.TrimSpace(label), ".")
		o.Why = strings.TrimSpace(why)
	}
	return o
}

func parseAnswer(text string) ClarifyAnswer {
	text = strings.TrimSpace(text)
	a := ClarifyAnswer{Raw: text}
	switch {
	case text == "" || strings.Trim(text, "—–- ") == "":
		a.State = AnswerOpen
	case answerAssumeRe.MatchString(text):
		m := answerAssumeRe.FindStringSubmatch(text)
		a.State = AnswerAssumed
		a.Option, _ = strconv.Atoi(m[1])
		a.Reason = strings.TrimSpace(m[2])
	case answerOwnerRe.MatchString(text):
		m := answerOwnerRe.FindStringSubmatch(text)
		a.State = AnswerOwner
		a.Option, _ = strconv.Atoi(m[1])
		a.Date = m[2]
		a.Who = strings.TrimSpace(m[3])
		if w, err := strconv.Unquote(m[4]); err == nil {
			a.Words = w
		}
		a.Decision = m[5]
	default:
		a.State = AnswerOther
		a.Words = text
	}
	return a
}

func questionProblems(section string, c Clarify) []string {
	var out []string
	if len(c.Questions) == 0 {
		if !c.Nothing {
			out = append(out, fmt.Sprintf("## %s has no `### Q1. …` question and no `Nothing to ask: <why>.` line", section))
		}
		return out
	}
	if c.Nothing {
		out = append(out, fmt.Sprintf("## %s has questions and a `Nothing to ask` line: drop one", section))
	}
	seen := map[string]bool{}
	for _, q := range c.Questions {
		if seen[q.ID] {
			out = append(out, q.ID+": the number is used twice")
		}
		seen[q.ID] = true
		switch n := len(q.Options); {
		case n < 2:
			out = append(out, fmt.Sprintf("%s: %d option(s), a question needs 2–4", q.ID, n))
		case n > 4:
			out = append(out, fmt.Sprintf("%s: %d options, at most 4", q.ID, n))
		}
		rec := 0
		for i, o := range q.Options {
			if o.Recommended {
				rec++
				if i != 0 {
					out = append(out, fmt.Sprintf("%s: the recommendation (option %d) goes first", q.ID, o.Num))
				}
			}
			if o.Cost == "" {
				out = append(out, fmt.Sprintf("%s: option %d says no `Cost:`", q.ID, o.Num))
			}
		}
		if rec == 0 && len(q.Options) > 0 {
			out = append(out, q.ID+": no option is marked (recommended)")
		} else if rec > 1 {
			out = append(out, q.ID+": more than one option is marked (recommended)")
		}
		if !q.HasAnswerLine {
			out = append(out, q.ID+": no `**Answer:** —` line")
		}
		if (q.Answer.State == AnswerAssumed || q.Answer.State == AnswerOwner) && q.Answer.Option > 0 && q.OptionByNum(q.Answer.Option) == nil {
			out = append(out, fmt.Sprintf("%s: the answer names option %d, which isn't there", q.ID, q.Answer.Option))
		}
	}
	return out
}

// RenderQuestion is the markdown block of q (heading, options, answer
// line). An empty q.ID renders as Q<q.Num>; the first option is marked
// recommended when none is.
func RenderQuestion(q ClarifyQuestion) string {
	id := q.ID
	if id == "" {
		id = "Q" + strconv.Itoa(max(q.Num, 1))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "### %s. %s\n", id, oneLine(q.Text))
	anyRec := false
	for _, o := range q.Options {
		anyRec = anyRec || o.Recommended
	}
	for i, o := range q.Options {
		n := o.Num
		if n == 0 {
			n = i + 1
		}
		line := fmt.Sprintf("%d. **%s**", n, strings.TrimRight(oneLine(o.Label), "."))
		if o.Recommended || (!anyRec && i == 0) {
			line += " (recommended)"
		}
		if w := oneLine(o.Why); w != "" {
			line += " " + w
		}
		if c := strings.TrimRight(oneLine(o.Cost), "."); c != "" {
			line += " Cost: " + c + "."
		}
		b.WriteString(line + "\n")
	}
	b.WriteString(renderAnswer(q.Answer))
	return b.String()
}

func renderAnswer(a ClarifyAnswer) string {
	switch a.State {
	case AnswerOwner:
		who := a.Who
		if who == "" {
			who = OwnerAuthor
		}
		s := fmt.Sprintf("**Answer:** %d · %s · %s: %s", a.Option, a.Date, who, strconv.Quote(oneLine(a.Words)))
		if a.Decision != "" {
			s += " (" + a.Decision + ")"
		}
		return s
	case AnswerAssumed:
		s := "**Answer:** assumed"
		if a.Option > 0 {
			s += " " + strconv.Itoa(a.Option)
		}
		reason := oneLine(a.Reason)
		if reason == "" {
			reason = DefaultAssumedReason
		}
		return s + " — " + reason
	case AnswerOther:
		return "**Answer:** " + oneLine(a.Words)
	}
	return "**Answer:** —"
}

// AppendQuestion adds q to section (created when missing) as the next
// Q-number there, in one locked write, and returns its id.
func AppendQuestion(filePath, section string, q ClarifyQuestion) (string, error) {
	if oneLine(q.Text) == "" {
		return "", fmt.Errorf("question text is required")
	}
	var id string
	_, _, err := UpdateIssueBody(filePath, func(body string) (string, bool, error) {
		n := 1
		for _, old := range ParseQuestions(body, section) {
			n = max(n, old.Num+1)
		}
		q.ID, q.Num = "Q"+strconv.Itoa(n), n
		if q.Answer.State == "" {
			q.Answer.State = AnswerOpen
		}
		id = q.ID
		body = dropNothingLine(body, section)
		return appendToSectionTight(body, section, strings.TrimRight(RenderQuestion(q), "\n"), true), true, nil
	})
	return id, err
}

// dropNothingLine removes a "Nothing to ask" line from section once it gets
// a question.
func dropNothingLine(body, section string) string {
	secs := findSectionHeadings(body, section, 2)
	if len(secs) == 0 {
		return body
	}
	lines := strings.Split(body, "\n")
	for i := secs[0].StartLine + 1; i < secs[0].EndLine && i < len(lines); i++ {
		if questionHeadRe.MatchString(strings.TrimSpace(lines[i])) {
			return body
		}
		if nothingRe.MatchString(strings.TrimSpace(lines[i])) {
			return strings.Join(append(lines[:i:i], lines[i+1:]...), "\n")
		}
	}
	return body
}

// setAnswerLine writes answer as q's answer line in lines (after its last
// line when it has none).
func setAnswerLine(lines []string, q ClarifyQuestion, answer string) []string {
	if q.answerLine >= 0 {
		indent := lines[q.answerLine][:len(lines[q.answerLine])-len(strings.TrimLeft(lines[q.answerLine], " \t"))]
		lines[q.answerLine] = indent + answer
		return lines
	}
	at := q.lastLine + 1
	out := append([]string(nil), lines[:at]...)
	out = append(out, answer)
	return append(out, lines[at:]...)
}

// AnswerInput is one answer to record with AnswerQuestion.
type AnswerInput struct {
	Section string // default ClarifySection
	ID      string // "Q2"
	Option  int
	Words   string // the owner's words; empty records the option's label
	How     string // the decision's how; default ClarifyHow
	// Expect, when set, is the question text the caller saw; a different
	// text refuses with ErrQuestionChanged.
	Expect string
	// Log is where the answer's decision is logged and who gave it.
	Log DecisionWrite
}

// AnswerQuestion records the owner's answer to one question in one locked
// write: the answer line becomes `<n> · <date> · owner: "<words>" (D-k)`
// and D-k is added under ## Decisions with the owner's words as its quote.
// An assumed answer may be answered; an owner's one is refused.
func AnswerQuestion(filePath, slug string, in AnswerInput, now time.Time) (ClarifyQuestion, Decision, error) {
	section := in.Section
	if section == "" {
		section = ClarifySection
	}
	id := NormalizeQuestionID(in.ID)
	if id == "" {
		return ClarifyQuestion{}, Decision{}, fmt.Errorf("%q is not a question id (Q1, Q2, …)", in.ID)
	}
	how := oneLine(in.How)
	if how == "" {
		how = ClarifyHow
	}
	var out ClarifyQuestion
	var line string
	err := writeDecision(filePath, in.Log, now, func(body string) (decisionChange, error) {
		c := ParseQuestionSection(body, section)
		q := c.Question(id)
		if q == nil {
			return decisionChange{}, fmt.Errorf("%s %s: %w", slug, id, ErrQuestionNotFound)
		}
		if in.Expect != "" && oneLine(in.Expect) != oneLine(q.Text) {
			return decisionChange{}, fmt.Errorf("%s: %w", id, ErrQuestionChanged)
		}
		if q.Answer.State == AnswerOwner {
			return decisionChange{}, fmt.Errorf("%s: %w", id, ErrQuestionAnswered)
		}
		opt := q.OptionByNum(in.Option)
		if opt == nil {
			return decisionChange{}, fmt.Errorf("%s has no option %d (it has %d)", id, in.Option, len(q.Options))
		}
		words := oneLine(in.Words)
		if words == "" {
			words = opt.Label
		}
		did := fmt.Sprintf("D-%d", nextNumber(decisionIDRe, body))
		d := NewDecision{Text: fmt.Sprintf("%s %s %s", id, strings.TrimRight(oneLine(q.Text), "?."), "→ "+opt.Label), How: how, Quote: words}
		if err := ValidateNewDecision(d); err != nil {
			return decisionChange{}, err
		}
		q.Answer = ClarifyAnswer{State: AnswerOwner, Option: opt.Num, Words: words, Who: OwnerAuthor, Date: now.Format(decisionDateLayout), Decision: did}
		out = *q
		lines := setAnswerLine(strings.Split(body, "\n"), *q, renderAnswer(q.Answer))
		line = RenderDecisionLine(did, d, now)
		return decisionChange{Body: appendToSectionTight(strings.Join(lines, "\n"), DecisionsSection, line, false),
			Events: []DecisionEvent{{Event: DecisionRecorded, Ref: slug + "#" + did}}}, nil
	})
	if err != nil {
		return ClarifyQuestion{}, Decision{}, err
	}
	dec, ok := parseDecisionLine(slug, line)
	if !ok {
		return out, Decision{}, fmt.Errorf("internal: wrote an unparseable decision line: %s", line)
	}
	return out, dec, nil
}

// AssumeOpen marks every open question of section as `assumed — <reason>`
// (the recommendation) in one locked write and returns them.
func AssumeOpen(filePath, section, reason string) ([]ClarifyQuestion, error) {
	if section == "" {
		section = ClarifySection
	}
	reason = oneLine(reason)
	if reason == "" {
		reason = DefaultAssumedReason
	}
	var out []ClarifyQuestion
	_, _, err := UpdateIssueBody(filePath, func(body string) (string, bool, error) {
		open := ParseQuestionSection(body, section).Open()
		if len(open) == 0 {
			return body, false, nil
		}
		lines := strings.Split(body, "\n")
		// Bottom up, so an inserted answer line doesn't move the ones above.
		for i := len(open) - 1; i >= 0; i-- {
			q := open[i]
			q.Answer = ClarifyAnswer{State: AnswerAssumed, Reason: reason}
			lines = setAnswerLine(lines, q, renderAnswer(q.Answer))
			open[i] = q
		}
		out = open
		return strings.Join(lines, "\n"), true, nil
	})
	return out, err
}

// ClarifyOfFile reads the issue at filePath and parses its ## Clarify.
func ClarifyOfFile(filePath string) (Clarify, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return Clarify{}, err
	}
	issue, err := ParseIssue(filePath, data)
	if err != nil {
		return Clarify{}, err
	}
	return ParseClarify(issue.BodyRaw), nil
}
