package viewer

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// An open AskUserQuestion is read from the lead's pane: Claude Code (2.1)
// writes the assistant's message to the transcript only once the question
// is answered. Its widget shows one question at a time:
//
//	←  ☒ Color  ☐ Pets  ✔ Submit  →        the tabs (one question: "☐ Food")
//	Which pets do you like?
//	❯ 1. [✔] Cats                          options, ticked when several may be
//	         Cats as pets                    picked, each with its description
//	  2. [ ] Type something                  Other (or the text typed into it)
//	     Submit                              several choices: the row that moves on
//	────────
//	  3. Chat about this
//	Enter to select · Tab/Arrow keys to navigate · Esc to cancel
//
// then a review tab: "Ready to submit your answers?" with 1. Submit answers,
// 2. Cancel. The page answers by pressing the keys the owner would (verified
// on 2.1.314): a single choice is its digit (it moves on; with one question
// it submits); Other is its digit, the text, Enter. With several choices a
// digit ticks or unticks and leaves the cursor where it is; text goes to the
// row under the cursor; Enter on Submit moves on.

// CEOPaneAsk is the question widget on the lead's pane.
type CEOPaneAsk struct {
	// Tabs are the questions' headers, Done once answered.
	Tabs []CEOAskTab `json:"tabs,omitempty"`
	// Question is the question on screen; empty on the review tab.
	Question string          `json:"question"`
	Multi    bool            `json:"multi"`
	Options  []CEOPaneOption `json:"options"`
	// Other is the "Type something" row: Text is what was typed into it.
	Other CEOPaneOption `json:"other"`
	// Cursor is the row the cursor is on: an option's key, Other's, or
	// Other's + 1 for the Submit row.
	Cursor int `json:"cursor"`
	// Review is the last tab: the answers and Submit answers.
	Review  bool           `json:"review"`
	Answers []CEOAskReview `json:"answers,omitempty"`
}

// CEOAskTab is one question's header in the widget's tab row.
type CEOAskTab struct {
	Label string `json:"label"`
	Done  bool   `json:"done"`
}

// CEOPaneOption is one row of the widget: Key is its digit.
type CEOPaneOption struct {
	Key         int    `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Checked     bool   `json:"checked"`
}

// CEOAskReview is one question and its answer on the review tab.
type CEOAskReview struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

var (
	// askRow is a numbered widget row: "❯ 1. [✔] Cats", "  3. Type something.".
	askRow = regexp.MustCompile(`^(❯\s*)?(\d+)\.\s+(.*)$`)
	// askBox is a row's tick box when several may be picked.
	askBox = regexp.MustCompile(`^\[(.)\]\s*`)
	// askTab is one tab: "☐ Color", "☒ Pets".
	askTab = regexp.MustCompile(`([☐☒])\s+(.+?)(?:\s{2,}|$)`)
)

// askOtherPlaceholder is the Other row before anything is typed.
var askOtherPlaceholder = map[string]bool{"Type something.": true, "Type something": true}

// parsePaneAsk finds the question widget at the bottom of lines (a cleaned
// pane, from tail on); it returns the widget and the line it starts on, or
// nil.
func parsePaneAsk(lines []string, tail int) (*CEOPaneAsk, int) {
	if tail < 0 {
		tail = 0
	}
	for i := len(lines) - 1; i >= tail; i-- {
		t := boxText(lines[i])
		if t == "Ready to submit your answers?" {
			return parseAskReview(lines, tail, i)
		}
		if strings.Contains(t, "Esc to cancel") && strings.Contains(t, "Enter to select") {
			return parseAskQuestion(lines, tail, i)
		}
	}
	return nil, 0
}

// askTop is the widget's first line above end: its tab row (nil tabs and
// end+1 when there is none).
func askTop(lines []string, tail, end int) ([]CEOAskTab, int) {
	for i := end; i >= tail; i-- {
		t := boxText(lines[i])
		if !strings.HasPrefix(t, "←") && !strings.HasPrefix(t, "☐") && !strings.HasPrefix(t, "☒") {
			continue
		}
		var tabs []CEOAskTab
		for _, m := range askTab.FindAllStringSubmatch(t, -1) {
			tabs = append(tabs, CEOAskTab{Label: strings.TrimSpace(m[2]), Done: m[1] == "☒"})
		}
		if len(tabs) > 0 {
			return tabs, i
		}
	}
	return nil, -1
}

// parseAskQuestion reads the widget whose footer is on line foot.
func parseAskQuestion(lines []string, tail, foot int) (*CEOPaneAsk, int) {
	tabs, top := askTop(lines, tail, foot)
	if top < 0 {
		return nil, 0
	}
	ask := &CEOPaneAsk{Tabs: tabs}
	var question []string
	var rows []CEOPaneOption
	cursor := 0
	ruled := false
	for i := top + 1; i < foot && !ruled; i++ {
		raw := lines[i]
		t := boxText(raw)
		switch m := askRow.FindStringSubmatch(t); {
		case isRule(raw):
			// Under the rule: "Chat about this", not an answer.
			ruled = len(rows) > 0
		case m != nil:
			key, _ := strconv.Atoi(m[2])
			label := m[3]
			row := CEOPaneOption{Key: key}
			if b := askBox.FindStringSubmatch(label); b != nil {
				ask.Multi, row.Checked = true, b[1] != " "
				label = label[len(b[0]):]
			}
			row.Label = strings.TrimSpace(label)
			if m[1] != "" {
				cursor = key
			}
			rows = append(rows, row)
		case len(rows) == 0:
			if t != "" {
				question = append(question, t)
			}
		case strings.TrimSpace(strings.TrimPrefix(t, "❯")) == "Submit":
			ask.Multi = true
			if strings.HasPrefix(t, "❯") {
				cursor = len(rows) + 1
			}
		case t != "":
			d := &rows[len(rows)-1].Description
			*d = strings.TrimSpace(*d + " " + t)
		}
	}
	if len(rows) < 2 || len(question) == 0 {
		return nil, 0
	}
	ask.Question = strings.Join(question, " ")
	ask.Options, ask.Other = rows[:len(rows)-1], rows[len(rows)-1]
	if askOtherPlaceholder[ask.Other.Label] {
		ask.Other.Label = ""
	}
	ask.Other.Description = ""
	ask.Cursor = cursor
	return ask, top
}

// parseAskReview reads the review tab whose question is on line ready.
func parseAskReview(lines []string, tail, ready int) (*CEOPaneAsk, int) {
	next := ready + 1
	for next < len(lines) && boxText(lines[next]) == "" {
		next++
	}
	if next >= len(lines) || !strings.Contains(boxText(lines[next]), "Submit answers") {
		return nil, 0
	}
	tabs, top := askTop(lines, tail, ready)
	if top < 0 {
		return nil, 0
	}
	ask := &CEOPaneAsk{Tabs: tabs, Review: true}
	for i := top + 1; i < ready; i++ {
		t := boxText(lines[i])
		switch {
		case strings.HasPrefix(t, "●"):
			ask.Answers = append(ask.Answers, CEOAskReview{Question: strings.TrimSpace(strings.TrimPrefix(t, "●"))})
		case strings.HasPrefix(t, "→") && len(ask.Answers) > 0:
			ask.Answers[len(ask.Answers)-1].Answer = strings.TrimSpace(strings.TrimPrefix(t, "→"))
		}
	}
	return ask, top
}

// ceoAnswerStep is one press: a key name or text typed as is.
type ceoAnswerStep struct {
	Key, Text string
}

// ceoAnswerGap is the pause between presses, so the widget redraws between
// them; a seam for tests.
var ceoAnswerGap = 150 * time.Millisecond

// ceoAnswerOtherMax caps an Other answer, in runes.
const ceoAnswerOtherMax = 500

// paneAnswerSteps are the presses that answer the question on screen with
// picks (option keys) and other (cleaned text), or why they don't fit it.
func paneAnswerSteps(ask *CEOPaneAsk, picks []int, other string) ([]ceoAnswerStep, string) {
	n := len(ask.Options)
	want := map[int]bool{}
	for _, p := range picks {
		if p < 1 || p > n || want[p] {
			return nil, "there is no option " + strconv.Itoa(p)
		}
		want[p] = true
	}
	if len([]rune(other)) > ceoAnswerOtherMax {
		return nil, "the Other answer is over " + strconv.Itoa(ceoAnswerOtherMax) + " characters"
	}
	var steps []ceoAnswerStep
	key := func(k string) { steps = append(steps, ceoAnswerStep{Key: k}) }
	otherKey := strconv.Itoa(n + 1)
	if !ask.Multi {
		switch {
		case len(picks)+boolInt(other != "") != 1:
			return nil, "pick one answer"
		case other != "":
			key(otherKey)
			steps = append(steps, ceoAnswerStep{Text: other})
			key("Enter")
		default:
			key(strconv.Itoa(picks[0]))
		}
		return steps, ""
	}
	if len(picks) == 0 && other == "" {
		return nil, "pick at least one answer"
	}
	if other != "" && ask.Other.Label != "" && ask.Other.Label != other {
		return nil, "Other already has text on the CEO's screen: change it in the Terminal view"
	}
	for _, o := range ask.Options {
		if want[o.Key] != o.Checked {
			key(strconv.Itoa(o.Key))
		}
	}
	cursor := ask.Cursor
	if cursor < 1 {
		cursor = 1
	}
	move := func(to int) {
		for ; cursor < to; cursor++ {
			key("Down")
		}
		for ; cursor > to; cursor-- {
			key("Up")
		}
	}
	switch {
	case other != "" && ask.Other.Label == "":
		// Typing into the Other row ticks it.
		move(n + 1)
		steps = append(steps, ceoAnswerStep{Text: other})
	case other == "" && ask.Other.Checked:
		key(otherKey)
	}
	move(n + 2)
	key("Enter")
	return steps, ""
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// handleLeadAnswer serves POST /p/<project>/lead/answer, body {question,
// picks, other}: answers the question on the lead's screen in its widget.
// question is the text the card showed: when the screen shows another one
// (or none), 409 moved-on and nothing is pressed. 409 no-session when the
// lead isn't running, 400 for answers that don't fit the question. The
// review tab's Submit answers is a plain key (/lead/key 1).
func (s *Server) handleLeadAnswer(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	var body struct {
		Question string `json:"question"`
		Picks    []int  `json:"picks"`
		Other    string `json:"other"`
	}
	name, _, ok := chatLead(proj, r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !decodeLeadChatBody(w, r, 16*1024, &body) {
		return
	}
	target := agentrun.Target(proj, name)
	if !agentrun.HasSession(target) {
		leadChatError(w, http.StatusConflict, "no-session", agentDisplayName(proj, name)+" is not running")
		return
	}
	defer leadChatLock(target)()
	pane, err := agentrun.CaptureHistory(target, leadChatScrollback)
	var ask *CEOPaneAsk
	if err == nil {
		ask = parseCEOPane(cleanPane(pane)).Ask
	}
	if ask == nil || ask.Review || ask.Question != strings.TrimSpace(body.Question) {
		leadChatError(w, http.StatusConflict, "moved-on", "the CEO's screen doesn't show this question now: answer it in the Terminal view")
		return
	}
	other := strings.Join(strings.Fields(cleanMessage(body.Other)), " ")
	steps, why := paneAnswerSteps(ask, body.Picks, other)
	if why != "" {
		leadChatError(w, http.StatusBadRequest, "error", why)
		return
	}
	for i, st := range steps {
		if i > 0 {
			time.Sleep(ceoAnswerGap)
		}
		if st.Text != "" {
			err = agentrun.SendText(target, st.Text)
		} else {
			err = agentrun.SendKey(target, st.Key)
		}
		if err != nil {
			leadChatError(w, http.StatusBadGateway, "error", err.Error())
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "sent", "session": agentDisplayName(proj, name)})
}
