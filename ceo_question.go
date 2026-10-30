package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/agentrun"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The CEO chat answers an AskUserQuestion by pressing the keys the owner
// would press in the lead's widget (Claude Code 2.1). The widget shows one
// question at a time, options numbered from 1, then "Type something" and
// "Chat about this":
//
//   - single choice: the option's digit picks it and moves on (with one
//     question, it submits); Other is its digit, the text typed, Enter.
//   - several choices: digits toggle and the cursor stays on the first row;
//     typed text goes to the row under the cursor, so Other is Down to its
//     row and the text (typing ticks it); Tab moves on.
//   - with more than one question, or several choices, a review tab ends
//     it: "Ready to submit your answers?" with 1. Submit answers.

// CEOAnswer is the owner's answer to one question: the options picked (1 is
// the first) and, for Other, the text.
type CEOAnswer struct {
	Picks []int  `json:"picks"`
	Other string `json:"other"`
}

// askCard is wb/ask-card's data: an open question and where to answer it.
type askCard struct {
	Q         *CEOQuestion
	AnswerURL string
}

// ceoAnswerOtherMax caps an Other answer, in runes.
const ceoAnswerOtherMax = 500

// ceoAnswerStep is one press: a key name or text typed as is.
type ceoAnswerStep struct {
	Key, Text string
}

// ceoAnswerGap is the pause between presses, so the widget redraws between
// them; a seam for tests.
var ceoAnswerGap = 150 * time.Millisecond

// checkCEOAnswers checks answers against q and returns them cleaned (Other
// on one line), or why they don't fit.
func checkCEOAnswers(q *CEOQuestion, answers []CEOAnswer) ([]CEOAnswer, string) {
	if len(answers) != len(q.Questions) {
		return nil, "answer each of the " + plural(len(q.Questions), "question", "questions")
	}
	out := make([]CEOAnswer, len(answers))
	for i, a := range answers {
		e := q.Questions[i]
		other := strings.Join(strings.Fields(cleanMessage(a.Other)), " ")
		if len([]rune(other)) > ceoAnswerOtherMax {
			return nil, "an Other answer is over " + strconv.Itoa(ceoAnswerOtherMax) + " characters"
		}
		seen := map[int]bool{}
		for _, p := range a.Picks {
			if p < 1 || p > len(e.Options) || seen[p] {
				return nil, "question " + strconv.Itoa(i+1) + " has no option " + strconv.Itoa(p)
			}
			seen[p] = true
		}
		n := len(a.Picks)
		if other != "" {
			n++
		}
		if n == 0 {
			return nil, "question " + strconv.Itoa(i+1) + " has no answer"
		}
		if !e.MultiSelect && n > 1 {
			return nil, "question " + strconv.Itoa(i+1) + " takes one answer"
		}
		out[i] = CEOAnswer{Picks: a.Picks, Other: other}
	}
	return out, ""
}

// ceoAnswerSteps are the presses that answer q's widget with answers
// (checked). The review tab, when there is one, is submitted after them
// (handleLeadAnswer).
func ceoAnswerSteps(q *CEOQuestion, answers []CEOAnswer) []ceoAnswerStep {
	var steps []ceoAnswerStep
	key := func(k string) { steps = append(steps, ceoAnswerStep{Key: k}) }
	for i, e := range q.Questions {
		a := answers[i]
		otherRow := len(e.Options) + 1
		if !e.MultiSelect {
			if a.Other != "" {
				key(strconv.Itoa(otherRow))
				steps = append(steps, ceoAnswerStep{Text: a.Other})
				key("Enter")
			} else {
				key(strconv.Itoa(a.Picks[0]))
			}
			continue
		}
		for _, p := range a.Picks {
			key(strconv.Itoa(p))
		}
		if a.Other != "" {
			for range e.Options {
				key("Down")
			}
			steps = append(steps, ceoAnswerStep{Text: a.Other})
		}
		key("Tab")
	}
	return steps
}

// handleLeadAnswer serves POST /p/<project>/lead/answer, body {id, answers}:
// answers the lead's open AskUserQuestion id in its widget. 409 when the
// lead isn't running (no-session), the question is answered (answered) or
// its widget isn't on the pane (moved-on: answer in the Terminal view); 404
// for an unknown id; 400 for answers that don't fit the questions.
func (s *Server) handleLeadAnswer(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	var body struct {
		ID      string      `json:"id"`
		Answers []CEOAnswer `json:"answers"`
	}
	if !decodeLeadChatBody(w, r, 16*1024, &body) {
		return
	}
	name := tracker.LeadSessionName(proj)
	target := agentrun.Target(proj, name)
	if !agentrun.HasSession(target) {
		leadChatError(w, http.StatusConflict, "no-session", agentDisplayName(proj, name)+" is not running")
		return
	}
	var q *CEOQuestion
	if path, ok := leadTranscript(projectRoot(proj), name); ok {
		if t, err := readLeadTranscript(path); err == nil {
			q = findCEOQuestion(t.Turns, body.ID)
		}
	}
	if q == nil {
		leadChatError(w, http.StatusNotFound, "unknown", "no such question from the CEO")
		return
	}
	if q.Answered {
		leadChatError(w, http.StatusConflict, "answered", "this question was already answered")
		return
	}
	answers, why := checkCEOAnswers(q, body.Answers)
	if why != "" {
		leadChatError(w, http.StatusBadRequest, "error", why)
		return
	}
	defer leadChatLock(target)()
	pane, err := agentrun.CaptureHistory(target, leadChatScrollback)
	if err != nil || !askOnPane(q, paneTailText(cleanPane(pane))) {
		leadChatError(w, http.StatusConflict, "moved-on", "the CEO's screen doesn't show this question now: answer it in the Terminal view")
		return
	}
	for i, st := range ceoAnswerSteps(q, answers) {
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
	time.Sleep(2 * ceoAnswerGap)
	if pane, err := agentrun.CaptureHistory(target, leadChatScrollback); err == nil && strings.Contains(paneTailText(cleanPane(pane)), "Submit answers") {
		if err := agentrun.SendKey(target, "1"); err != nil {
			leadChatError(w, http.StatusBadGateway, "error", err.Error())
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "sent", "id": q.ID, "session": agentDisplayName(proj, name)})
}

// findCEOQuestion is the AskUserQuestion call id in turns, or nil.
func findCEOQuestion(turns []CEOTurn, id string) *CEOQuestion {
	for _, t := range turns {
		for _, q := range t.Asks {
			if q.ID == id {
				return q
			}
		}
	}
	return nil
}
