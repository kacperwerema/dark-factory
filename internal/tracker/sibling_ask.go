package tracker

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Sibling asks: one worker asks the worker of another open issue (a sibling
// whose format or record it builds on) to agree something, and gets the
// answer back. Both are ordinary comments; nothing is stored elsewhere and
// nothing travels outside the issue files.
//
//   - the ask, on the sibling's issue:   ask: from <asker slug>: <words>
//   - the answer, on the asker's issue:  answer: from <sibling slug> (ask #<id>): <words>
//
// An ask is open while its comment's status is "open"; answering marks it
// "done". An answer is open (not yet delivered) until the asker's own worker
// has been shown it once. Neither is ever an approval: the words sit after
// the "ask:"/"answer:" prefix, which no gate parser reads.

// Comment prefixes of the sibling channel.
const (
	AskPrefix    = "ask:"
	AnswerPrefix = "answer:"
)

// NotAnApproval is the line every surface of the channel carries.
const NotAnApproval = "An ask or answer is between workers: it never approves a gate."

var (
	askRe    = regexp.MustCompile(`^ask:\s*from\s+(\S+?):\s*(.*)$`)
	answerRe = regexp.MustCompile(`^answer:\s*from\s+(\S+)\s+\(ask ((?:#\d+(?:,\s*)?)+)\):\s*(.*)$`)
	askIDRe  = regexp.MustCompile(`#(\d+)`)
)

// AskComment is the text of an ask from asker.
func AskComment(asker, words string) string {
	return fmt.Sprintf("%s from %s: %s", AskPrefix, asker, strings.TrimSpace(words))
}

// AnswerComment is the text of an answer from answerer to the asks ids.
func AnswerComment(answerer string, ids []int, words string) string {
	refs := make([]string, len(ids))
	for i, id := range ids {
		refs[i] = "#" + strconv.Itoa(id)
	}
	return fmt.Sprintf("%s from %s (ask %s): %s", AnswerPrefix, answerer, strings.Join(refs, ", "), strings.TrimSpace(words))
}

// SiblingAsk is one ask: the issue it is on (the sibling asked) and who asked.
type SiblingAsk struct {
	// On is the sibling's issue, the one the ask comment is on.
	On string `json:"on"`
	// From is the asker's issue.
	From  string `json:"from"`
	ID    int    `json:"id"`
	Date  string `json:"date"`
	Words string `json:"words"`
	Open  bool   `json:"open"`
}

// SiblingAnswer is one answer on the asker's issue.
type SiblingAnswer struct {
	// From is the answering sibling's issue.
	From  string `json:"from"`
	ID    int    `json:"id"`
	Asks  []int  `json:"asks"`
	Date  string `json:"date"`
	Words string `json:"words"`
	// Delivered is true once the asker's worker was shown it.
	Delivered bool `json:"delivered"`
}

// ParseAsk reads an ask comment's asker and words.
func ParseAsk(text string) (from, words string, ok bool) {
	m := askRe.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return "", "", false
	}
	return m[1], strings.TrimSpace(m[2]), true
}

// ParseAnswer reads an answer comment's answerer, the asks it answers and
// its words.
func ParseAnswer(text string) (from string, asks []int, words string, ok bool) {
	m := answerRe.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return "", nil, "", false
	}
	for _, id := range askIDRe.FindAllStringSubmatch(m[2], -1) {
		n, _ := strconv.Atoi(id[1])
		asks = append(asks, n)
	}
	return m[1], asks, strings.TrimSpace(m[3]), true
}

// AsksOn lists the asks on the issue slug, oldest first.
func AsksOn(slug string, comments []Comment) []SiblingAsk {
	var out []SiblingAsk
	for _, c := range comments {
		from, words, ok := ParseAsk(c.Text)
		if !ok {
			continue
		}
		out = append(out, SiblingAsk{On: slug, From: from, ID: c.ID, Date: c.Date, Words: words, Open: c.Status != "done"})
	}
	return out
}

// OpenAsksOn is AsksOn's open asks.
func OpenAsksOn(slug string, comments []Comment) []SiblingAsk {
	var out []SiblingAsk
	for _, a := range AsksOn(slug, comments) {
		if a.Open {
			out = append(out, a)
		}
	}
	return out
}

// AnswersOn lists the answers on an asker's issue, oldest first.
func AnswersOn(comments []Comment) []SiblingAnswer {
	var out []SiblingAnswer
	for _, c := range comments {
		from, asks, words, ok := ParseAnswer(c.Text)
		if !ok {
			continue
		}
		out = append(out, SiblingAnswer{From: from, ID: c.ID, Asks: asks, Date: c.Date, Words: words, Delivered: c.Status == "done"})
	}
	return out
}

// MarkCommentsDone sets the status of the comments ids to "done" (unlike
// ToggleComment, a done comment stays done).
func MarkCommentsDone(filePath string, ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	want := map[int]bool{}
	for _, id := range ids {
		want[id] = true
	}
	return withIssueLock(filePath, func() error {
		comments, err := LoadComments(filePath)
		if err != nil {
			return err
		}
		changed := false
		for i := range comments {
			if want[comments[i].ID] && comments[i].Status != "done" {
				comments[i].Status = "done"
				changed = true
			}
		}
		if !changed {
			return nil
		}
		return saveCommentsLocked(filePath, comments)
	})
}

// AskBetween says what passed between issue a and issue b: "ask open" when
// either has an open ask from the other, "agreed" when every ask between
// them is answered, "" when neither asked the other.
func AskBetween(a, b *Issue) string {
	if a == nil || b == nil {
		return ""
	}
	state := ""
	for _, pair := range [][2]*Issue{{a, b}, {b, a}} {
		comments, err := LoadComments(pair[0].FilePath)
		if err != nil {
			continue
		}
		for _, ask := range AsksOn(pair[0].Slug, comments) {
			if !strings.EqualFold(ask.From, pair[1].Slug) {
				continue
			}
			if ask.Open {
				return "ask open"
			}
			state = "agreed"
		}
	}
	return state
}

// ProjectOpenAsks lists every open ask on the open issues, oldest first.
// isClosed may be nil.
func ProjectOpenAsks(issues []*Issue, isClosed func(*Issue) bool) []SiblingAsk {
	var out []SiblingAsk
	for _, issue := range issues {
		if isClosed != nil && isClosed(issue) {
			continue
		}
		comments, err := LoadComments(issue.FilePath)
		if err != nil {
			continue
		}
		out = append(out, OpenAsksOn(issue.Slug, comments)...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ti, _ := CommentTime(out[i].Date)
		tj, _ := CommentTime(out[j].Date)
		return ti.Before(tj)
	})
	return out
}
