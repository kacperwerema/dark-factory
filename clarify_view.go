package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
	"github.com/michal-franc/issue-viewer/internal/yolo"
)

// The clarify round in the viewer: the Station's Clarify card shows every
// question of the issue's ## Clarify at once, as one form, and POST
// …/issue/<slug>/clarify records the owner's answers (owner's browser only,
// an O route): each one is the question's answer line and a D-n decision
// with the owner's words (tracker.AnswerQuestion). Needs you lists one
// "Clarify" row per issue whose questions wait (clarifyWaiting).

// clarifyRefused is the 403 for a request not from the owner's browser.
const clarifyRefused = "only the owner answers the clarify round, from a browser signed in at /owner; an agent relays the owner's words with issue-cli clarify <slug> --answer Qn=<k> --quote \"…\""

// WbClarify is the Station's Clarify card.
type WbClarify struct {
	Questions []WbClarifyQuestion
	// Waiting is how many the owner still has to answer: open, and assumed
	// ones no YOLO review holds. InReview the assumed ones the review after
	// YOLO lists (answering one settles it there too).
	Waiting, InReview int
	Nothing           bool
	Why               string
	Problems          []string
	PostURL           string
}

// WbClarifyQuestion is one question of the form.
type WbClarifyQuestion struct {
	ID, Text string
	State    string // open, assumed, owner, answered
	Options  []WbClarifyOption
	// Answer is the answer in words: "assumed — <reason>", or the owner's
	// words with the date and decision.
	Answer   string
	Decision string
	// Selected is the option preselected: the chosen one, else the
	// recommendation.
	Selected int
	// Done: the owner answered it; the form leaves it out.
	Done bool
}

// WbClarifyOption is one radio of a question.
type WbClarifyOption struct {
	Num               int
	Label, Cost, Why  string
	Recommended, Pick bool
}

// clarifyWaiting is the issue's questions that wait for the owner: open
// ones, and assumed ones that no YOLO review entry holds (those wait in the
// review after YOLO instead, so nothing is counted twice).
func clarifyWaiting(issue *tracker.Issue, comments []tracker.Comment) []tracker.ClarifyQuestion {
	c := tracker.ParseClarify(issue.BodyRaw)
	if len(c.Questions) == 0 {
		return nil
	}
	held := map[string]bool{}
	for _, p := range yolo.Questions(issue, comments) {
		held[p.Question] = true
	}
	var out []tracker.ClarifyQuestion
	for _, q := range c.Unanswered() {
		if q.Answer.State == tracker.AnswerAssumed && held[q.ID] {
			continue
		}
		out = append(out, q)
	}
	return out
}

// stationClarify is the Clarify card, or nil when the issue has no
// ## Clarify.
func stationClarify(issueURL string, issue *tracker.Issue, comments []tracker.Comment) *WbClarify {
	c := tracker.ParseClarify(issue.BodyRaw)
	if !c.Found {
		return nil
	}
	card := &WbClarify{Nothing: c.Nothing && len(c.Questions) == 0, Why: c.Why, Problems: c.Problems, PostURL: issueURL + "/clarify"}
	card.Waiting = len(clarifyWaiting(issue, comments))
	for _, p := range yolo.Questions(issue, comments) {
		if p.Review == "" {
			card.InReview++
		}
	}
	for _, q := range c.Questions {
		wq := WbClarifyQuestion{ID: q.ID, Text: q.Text, State: q.Answer.State, Decision: q.Answer.Decision, Done: q.Answer.State == tracker.AnswerOwner}
		switch q.Answer.State {
		case tracker.AnswerAssumed:
			wq.Answer = "assumed — " + q.Answer.Reason
		case tracker.AnswerOwner:
			wq.Answer = fmt.Sprintf("%q · %s", q.Answer.Words, q.Answer.Date)
		case tracker.AnswerOther:
			wq.Answer = q.Answer.Words
		}
		if ch := q.Chosen(); ch != nil {
			wq.Selected = ch.Num
		} else if rec := q.Recommended(); rec != nil {
			wq.Selected = rec.Num
		}
		for _, o := range q.Options {
			wq.Options = append(wq.Options, WbClarifyOption{Num: o.Num, Label: o.Label, Cost: o.Cost, Why: o.Why, Recommended: o.Recommended, Pick: o.Num == wq.Selected})
		}
		card.Questions = append(card.Questions, wq)
	}
	return card
}

// clarifyAnswer is one answer the form posts.
type clarifyAnswer struct {
	ID     string `json:"id"`
	Option int    `json:"option"`
	Words  string `json:"words"`
	// Text is the question as the page showed it: a changed question is
	// refused, not answered blind.
	Text string `json:"text"`
}

// clarifyResult is one answer's outcome.
type clarifyResult struct {
	ID       string `json:"id"`
	Decision string `json:"decision,omitempty"`
	Error    string `json:"error,omitempty"`
}

// handleClarifyAnswer is POST …/issue/<slug>/clarify: {answers: [{id,
// option, words, text}]}. Each answer is recorded on its own; the reply
// lists each one's decision or why it was refused.
func (s *Server) handleClarifyAnswer(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	if !s.fromOwner(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]string{"error": clarifyRefused})
		return
	}
	slug := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix+"/issue/"), "/clarify")
	issue := s.findIssueBySlug(proj, slug)
	if issue == nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "issue not found: " + slug})
		return
	}
	var body struct {
		Answers []clarifyAnswer `json:"answers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Answers) == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "answers required: {answers: [{id, option, words, text}]}"})
		return
	}
	res, recorded := answerClarify(issue, body.Answers, time.Now())
	if len(recorded) > 0 {
		note := "clarify: the owner answered " + strings.Join(recorded, ", ") + " on the Station"
		if err := tracker.AddCommentBy(issue.FilePath, 0, note, "app", tracker.OwnerAuthor); err != nil {
			log.Printf("clarify: %s: comment: %v", issue.Slug, err)
		}
		log.Printf("clarify: %s: the owner answered %s", issue.Slug, strings.Join(recorded, ", "))
	}
	code := http.StatusOK
	if len(recorded) == 0 {
		code = http.StatusConflict
	}
	writeJSONStatus(w, code, map[string]any{"ok": len(recorded) > 0, "results": res})
}

// answerClarify records each answer; recorded is "Q1 (D-4)" for each one
// written.
func answerClarify(issue *tracker.Issue, answers []clarifyAnswer, now time.Time) (res []clarifyResult, recorded []string) {
	for _, a := range answers {
		r := clarifyResult{ID: tracker.NormalizeQuestionID(a.ID)}
		if r.ID == "" {
			r.ID, r.Error = a.ID, "not a question id"
			res = append(res, r)
			continue
		}
		_, d, err := tracker.AnswerQuestion(issue.FilePath, issue.Slug, tracker.AnswerInput{ID: r.ID, Option: a.Option, Words: a.Words, Expect: a.Text}, now)
		switch {
		case errors.Is(err, tracker.ErrQuestionChanged):
			r.Error = r.ID + " changed since this page loaded. Reload to see it"
		case errors.Is(err, tracker.ErrQuestionAnswered):
			r.Error = r.ID + " is already answered. Reload to see it"
		case errors.Is(err, tracker.ErrQuestionNotFound):
			r.Error = "no " + r.ID + " on the issue any more. Reload to see it"
		case err != nil:
			r.Error = err.Error()
		default:
			r.Decision = d.ID
			recorded = append(recorded, r.ID+" ("+d.ID+")")
		}
		res = append(res, r)
	}
	return res, recorded
}
