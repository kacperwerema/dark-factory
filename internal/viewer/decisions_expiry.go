package viewer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Expired hard decisions on Needs you: one row each with Renew and Let go
// (POST decisions/<slug>/<D-n>/renew {expires, text} and …/let-go {text}).
// Both are O routes (ownerOnlyRoute): the owner's browser only. The owner's
// words, when given, go in a decision: comment; the line changes only in
// its attribute group (tracker.RenewDecisionAs, LetGoDecisionAs).

var decisionExpiryPathRe = regexp.MustCompile(`^decisions/(.+)/(D-\d+)/(renew|let-go)$`)

// renewDefaultDays is how far the Renew form's date starts from today.
const renewDefaultDays = 90

type decisionExpiryRequest struct {
	Expires string `json:"expires"`
	Text    string `json:"text"`
}

type decisionExpiryResponse struct {
	Status  string `json:"status"`
	Ref     string `json:"ref"`
	Line    string `json:"line"`
	Expires string `json:"expires"`
	Ended   string `json:"ended,omitempty"`
}

// decisionExpiryURL is the base of a decision's renew and let-go routes.
func decisionExpiryURL(prefix, slug, id string) string {
	return prefix + "/decisions/" + slug + "/" + id
}

// decisionExpiryNeedsYou is one Needs you row per expired hard decision
// nobody renewed or let go.
func decisionExpiryNeedsYou(p *tracker.Project, color, prefix string, now time.Time) []WbQueueCard {
	list, err := tracker.ExpiredHard(p.IssueDir, now)
	if err != nil {
		return nil
	}
	var rows []WbQueueCard
	for _, d := range list {
		title := d.Slug
		if d.Issue != nil && d.Issue.Title != "" {
			title = d.Issue.Title
		}
		rows = append(rows, WbQueueCard{
			Kind: "decision", Label: "Decision expired", Tone: "you",
			Project: p.Name, ProjectSlug: p.Slug, Color: color,
			Slug: d.Ref, Title: title,
			Note:  fmt.Sprintf("%s: %s · hard, expired after %s. Renew it or let it go.", d.ID, clipRunes(d.Text, needsYouQuestionMax), d.Expires),
			Since: d.Since, Age: shortDuration(now.Sub(d.Since)),
			CTA: "Open", URL: prefix + "/issue/" + d.Slug,
			ExpiryURL: decisionExpiryURL(prefix, d.Slug, d.ID), RenewDefault: now.AddDate(0, 0, renewDefaultDays).Format("2006-01-02"),
			RenewMin: now.Format("2006-01-02"),
		})
	}
	return rows
}

// handleDecisionExpiry serves POST decisions/<slug>/<D-n>/renew and
// …/let-go from the owner's browser (the route is owner-only).
func (s *Server) handleDecisionExpiry(w http.ResponseWriter, r *http.Request, proj *tracker.Project, rest string) {
	m := decisionExpiryPathRe.FindStringSubmatch(rest)
	if m == nil {
		http.NotFound(w, r)
		return
	}
	slug, id, act := m[1], m[2], m[3]
	var req decisionExpiryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeYOLOError(w, http.StatusBadRequest, "bad request")
		return
	}
	now := time.Now() // once: the check, the line, the comment and the log agree
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		writeYOLOError(w, http.StatusInternalServerError, err.Error())
		return
	}
	issue := tracker.FindIssue(issues, slug)
	if issue == nil {
		writeYOLOError(w, http.StatusNotFound, "no issue "+slug)
		return
	}
	e := tracker.BuildDecisionIndex(issues, nil).Lookup(issue.Slug + "#" + id)
	switch {
	case e == nil:
		writeYOLOError(w, http.StatusNotFound, fmt.Sprintf("%s has no %s", issue.Slug, id))
		return
	case e.ReplacedBy != nil:
		writeYOLOError(w, http.StatusConflict, fmt.Sprintf("%s is replaced by %s, so it is out of force already", e.Ref, e.ReplacedBy.Ref))
		return
	}
	wr := ownerBrowserWrite(proj)
	wr.OwnerWords = strings.TrimSpace(req.Text)
	var d tracker.Decision
	if act == "renew" {
		exp, perr := time.ParseInLocation("2006-01-02", strings.TrimSpace(req.Expires), time.Local)
		y, mo, dd := now.Date()
		switch {
		case perr != nil:
			writeYOLOError(w, http.StatusBadRequest, "expires: want a date, YYYY-MM-DD")
			return
		case exp.Before(time.Date(y, mo, dd, 0, 0, 0, 0, time.Local)):
			writeYOLOError(w, http.StatusBadRequest, "expires: "+req.Expires+" is in the past: a decision expires today or later")
			return
		}
		_, d, err = tracker.RenewDecisionAs(issue.FilePath, issue.Slug, id, exp.Format("2006-01-02"), nil, now, wr)
	} else {
		_, d, err = tracker.LetGoDecisionAs(issue.FilePath, issue.Slug, id, nil, now, wr)
	}
	switch {
	case errors.Is(err, tracker.ErrNoExpiry), errors.Is(err, tracker.ErrDecisionEnded), errors.Is(err, tracker.ErrNotExpired), errors.Is(err, tracker.ErrNothingChanged):
		writeYOLOError(w, http.StatusConflict, tracker.ExpiryRefusal(e.Ref, err))
		return
	case errors.Is(err, tracker.ErrDecisionNotFound):
		writeYOLOError(w, http.StatusNotFound, fmt.Sprintf("%s has no %s", issue.Slug, id))
		return
	case err != nil:
		writeYOLOError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(decisionExpiryResponse{Status: "ok", Ref: d.Ref, Line: strings.TrimSpace(d.Raw), Expires: d.Expires, Ended: d.Ended})
}
