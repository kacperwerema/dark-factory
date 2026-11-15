package viewer

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kacperwerema/dark-factory/internal/partner"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The partner layer in the viewer: where a department's outside partner
// (OpenMontage for Video) is with an issue, read by internal/partner from
// the partner's own files. One line, partnerOf, feeds the Station's
// Partner block, the room's foot item and the Needs you row; a stage
// waiting for the owner is a waiting item (waitingOnOwner), so it counts
// once in needsYouRows like everything else (docs/departments.md#partners).

// WbPartner is one issue's partner status, worded.
type WbPartner struct {
	// Name is the partner's name ("OpenMontage"); Project the id read.
	Name, Project string
	// Stage and State are the partner's (partner.State*); Awaiting marks
	// a stage waiting for the owner.
	Stage, State string
	Awaiting     bool
	// Line is "OpenMontage: pitch · awaiting you", or the problem.
	Line string
	// Summary is the waiting stage's own summary; Spend "$0.40 spent of
	// $2.00"; Artifacts what the stage produced; Error a failed stage's.
	Summary, Spend, Error string
	Artifacts             []string
	// Problem is why nothing could be read (format not understood, path
	// missing, unknown adapter); Unknown marks the format case.
	Problem string
	Unknown bool
	// LiveURL is the partner's own viewer for the project, set only while
	// it answers.
	LiveURL string
	Since   time.Time
	// Answered is the owner's answer to the stage awaiting them (it then
	// waits on the worker, not the owner: Awaiting is false); AnswerURL is
	// where the owner answers while it awaits them (the gate bridge).
	Answered  *partner.Answer
	AnswerURL string
	// Unpaired are gated stages approved at the partner with no owner
	// decision, worded (partner.UnpairedLine).
	Unpaired []string
}

// AnsweredLine words the owner's answer waiting on the worker.
func (w *WbPartner) AnsweredLine() string {
	if w.Answered == nil {
		return ""
	}
	if w.Answered.Approved {
		return fmt.Sprintf("You approved %s (%s): the worker writes it at %s", w.Stage, w.Answered.Ref, w.Name)
	}
	return fmt.Sprintf("You sent %s back (%s): the worker revises it at %s", w.Stage, w.Answered.Ref, w.Name)
}

// partnerOf is the partner status of issue, nil when its department names
// no partner.
func partnerOf(p *tracker.Project, issue *tracker.Issue) *WbPartner {
	cfg, _ := p.PartnerOf(issue)
	if cfg == nil {
		return nil
	}
	st, err := partner.Read(*cfg, p.Root(), issue)
	w := partnerWords(*cfg, st, err)
	if err != nil {
		return w
	}
	b := partner.BridgeOf(*cfg, p.Root(), issue, st)
	if a := b.Answered; a != nil {
		w.Answered, w.Awaiting = a, false
		w.Line = partner.AnsweredLine(*cfg, st.Stage, *a)
	}
	w.Unpaired = b.Unpaired
	return w
}

// partnerWords words a status (or the error reading it).
func partnerWords(cfg tracker.Partner, st partner.Status, err error) *WbPartner {
	w := &WbPartner{Name: cfg.Label(), Project: st.Project, Stage: st.Stage, State: st.State, Since: st.Since}
	w.Line = partner.Line(cfg, st, err)
	if err != nil {
		w.State, w.Problem, w.Unknown = "", partner.Problem(err), errors.Is(err, partner.ErrFormat)
		return w
	}
	w.Awaiting = st.Awaiting()
	w.Summary, w.Error, w.Artifacts = st.Summary, st.Error, st.Artifacts
	if st.Cost != nil {
		w.Spend = fmt.Sprintf("$%.2f spent of $%.2f", st.Cost.Spent, st.Cost.Budget())
	}
	if st.LiveURL != "" && liveAnswers(st.HealthURL) {
		w.LiveURL = st.LiveURL
	}
	return w
}

// Note is the row's words after the line: summary, spend, artifacts.
func (w *WbPartner) Note() string {
	var parts []string
	for _, s := range []string{w.Summary, w.Spend} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	if len(w.Artifacts) > 0 {
		parts = append(parts, "artifacts: "+strings.Join(w.Artifacts, ", "))
	}
	if w.Error != "" {
		parts = append(parts, "error: "+w.Error)
	}
	return strings.Join(parts, " · ")
}

// liveAnswers reports whether a partner's local viewer answers its health
// URL, asked at most every liveTTL per URL with a short timeout. Only
// loopback URLs are asked (the adapter builds them from live_port:).
func liveAnswers(health string) bool {
	if !strings.HasPrefix(health, "http://127.0.0.1:") {
		return false
	}
	liveMu.Lock()
	if c, ok := liveCache[health]; ok && time.Since(c.at) < liveTTL {
		liveMu.Unlock()
		return c.up
	}
	liveMu.Unlock()
	up := false
	client := http.Client{Timeout: liveTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if resp, err := client.Get(health); err == nil {
		resp.Body.Close()
		up = resp.StatusCode == http.StatusOK
	}
	liveMu.Lock()
	liveCache[health] = liveEntry{up: up, at: time.Now()}
	liveMu.Unlock()
	return up
}

type liveEntry struct {
	up bool
	at time.Time
}

var (
	liveMu    sync.Mutex
	liveCache = map[string]liveEntry{}
)

const (
	liveTTL     = 30 * time.Second
	liveTimeout = 300 * time.Millisecond
)

// roomPartner is a department room's foot item: its partner's name and
// how many open issues wait on the owner or are being worked there; a
// config problem in words. nil when the department names no partner.
func roomPartner(p *tracker.Project, dept tracker.Department, own []*tracker.Issue) *WbFootItem {
	cfg := dept.Partner
	if cfg == nil {
		return nil
	}
	if _, err := partner.For(*cfg); err != nil {
		return &WbFootItem{Text: cfg.Label() + ": adapter unknown", Tone: "red", Title: err.Error()}
	}
	if dir := cfg.Dir(p.Root()); dir == "" || !isDir(dir) {
		return &WbFootItem{Text: cfg.Label() + ": path missing", Tone: "red", Title: "partner path: " + cfg.Path}
	}
	awaiting, working, unknown := 0, 0, 0
	for _, issue := range openOnly(p, own) {
		w := partnerOf(p, issue)
		switch {
		case w == nil:
		case w.Unknown:
			unknown++
		case w.Awaiting:
			awaiting++
		case w.State == partner.StateWorking || w.State == partner.StateFailed:
			working++
		}
	}
	parts := []string{cfg.Label()}
	if awaiting > 0 {
		parts = append(parts, fmt.Sprintf("%d awaiting you", awaiting))
	}
	if working > 0 {
		parts = append(parts, fmt.Sprintf("%d working", working))
	}
	if unknown > 0 {
		parts = append(parts, fmt.Sprintf("%d not understood", unknown))
	}
	if len(parts) == 1 {
		parts = append(parts, "nothing in flight")
	}
	f := &WbFootItem{Text: strings.Join(parts, " · "), Title: "partner: " + cfg.Label() + " (" + cfg.Adapter + ")"}
	if awaiting > 0 {
		f.Tone = "you"
	}
	return f
}

// isDir reports whether path is a folder.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// The gate bridge (docs/departments.md#the-owners-answer): the owner
// answers a partner's stage that awaits them, Approve or Send back, from
// the owner's browser only. The answer is a D-n on the issue whose ref
// names the stage (partner.ApprovedRef / SentBackRef), with the owner's
// words, and the issue's worker is nudged to write the partner's own
// approval quoting it. The engine never writes in the partner's checkout.

// partnerAnswerRefused is the refusal's way for an agent.
const partnerAnswerRefused = "the owner answers a partner's gate on Needs you or the Station; an agent asks on its issue trail: issue-cli comment <slug> \"question: …\""

// partnerAnswerResponse is POST …/issue/<slug>/partner's reply.
type partnerAnswerResponse struct {
	OK                bool   `json:"ok"`
	Decision          string `json:"decision"`
	Stage             string `json:"stage"`
	Action            string `json:"action"`
	NotifiedSession   string `json:"notified_session,omitempty"`
	NotificationError string `json:"notification_error,omitempty"`
}

// handlePartnerAnswer is POST …/issue/<slug>/partner {action: approve |
// send-back, stage, words, from}. The stage must still await the owner at
// the partner (else 409: the page is stale); a send-back needs words.
func (s *Server) handlePartnerAnswer(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	if !s.fromOwner(r) {
		w.Header().Set(ownerSignInHeader, "/owner")
		writeJSONStatus(w, http.StatusForbidden, map[string]string{"error": ownerRefused + ": " + partnerAnswerRefused})
		return
	}
	slug := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix+"/issue/"), "/partner")
	issue := s.findIssueBySlug(proj, slug)
	if issue == nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "issue not found: " + slug})
		return
	}
	var body struct {
		Action string `json:"action"`
		Stage  string `json:"stage"`
		Words  string `json:"words"`
		// From is where the owner answered: "needs-you", else the Station.
		From string `json:"from"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "bad request: {action, stage, words}"})
		return
	}
	approve := body.Action == "approve"
	if !approve && body.Action != "send-back" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "action is approve or send-back"})
		return
	}
	words, stage := oneLineWords(body.Words), strings.TrimSpace(body.Stage)
	if !approve && words == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "say what to revise: your words reach the agent"})
		return
	}
	cfg, _ := proj.PartnerOf(issue)
	if cfg == nil {
		writeJSONStatus(w, http.StatusConflict, map[string]string{"error": "the issue's department has no partner"})
		return
	}
	st := partnerOf(proj, issue)
	if st != nil && st.Stage == stage && st.Answered != nil {
		writeJSONStatus(w, http.StatusConflict, map[string]string{"error": fmt.Sprintf("%s is already answered (%s). Reload", stage, st.Answered.Ref)})
		return
	}
	if st == nil || !st.Awaiting || st.Stage != stage {
		now := "nothing awaits you"
		if st != nil && st.Awaiting {
			now = st.Stage + " awaits you"
		}
		writeJSONStatus(w, http.StatusConflict, map[string]string{"error": fmt.Sprintf("%s moved on: %s is no longer awaiting you (%s). Reload", cfg.Label(), stage, now)})
		return
	}
	where := "the Station"
	if body.From == "needs-you" {
		where = "Needs you"
	}
	d, err := recordPartnerAnswer(proj, issue, *cfg, stage, st.Since, approve, words, where, time.Now())
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": "your answer was not recorded: " + err.Error()})
		return
	}
	resp := partnerAnswerResponse{OK: true, Decision: d.Ref, Stage: stage, Action: body.Action}
	resp.NotifiedSession, resp.NotificationError = nudgeIssueWorker(proj, issue, partnerNudge(*cfg, issue.Slug, stage, d.Ref, approve, words))
	log.Printf("partner: %s: the owner %s %s at %s (%s)", issue.Slug, map[bool]string{true: "approved", false: "sent back"}[approve], stage, cfg.Label(), d.Ref)
	writeJSONStatus(w, http.StatusOK, resp)
}

// recordPartnerAnswer writes the owner's answer to stage as a D-n on the
// issue, logged as the owner's browser.
func recordPartnerAnswer(proj *tracker.Project, issue *tracker.Issue, cfg tracker.Partner, stage string, asked time.Time, approve bool, words, where string, now time.Time) (tracker.Decision, error) {
	nd := tracker.NewDecision{How: "owner on " + where, Quote: words}
	if approve {
		nd.Text, nd.Refs = cfg.Label()+" "+stage+" approved", []string{partner.ApprovedRef(stage, asked)}
	} else {
		nd.Text, nd.Refs = cfg.Label()+" "+stage+" sent back for revision", []string{partner.SentBackRef(stage, asked)}
	}
	return tracker.AddDecisionAs(issue.FilePath, issue.Slug, nd, now, ownerBrowserWrite(proj))
}

// partnerAlongGate records the partner's approval along with an engine gate
// the owner approved, when the issue's partner awaits them: the row showed
// both as one decision, so one click answers both. It returns the line the
// worker's nudge gets ("" when nothing awaited).
func partnerAlongGate(proj *tracker.Project, issue *tracker.Issue, words, where string, now time.Time) string {
	cfg, _ := proj.PartnerOf(issue)
	if cfg == nil {
		return ""
	}
	st := partnerOf(proj, issue)
	if st == nil || !st.Awaiting {
		return ""
	}
	d, err := recordPartnerAnswer(proj, issue, *cfg, st.Stage, st.Since, true, words, where, now)
	if err != nil {
		log.Printf("partner: %s: recording the approval of %s along with the gate: %v", issue.Slug, st.Stage, err)
		return ""
	}
	return partnerNudge(*cfg, issue.Slug, st.Stage, d.Ref, true, "")
}

// partnerNudge is the line the worker's session gets after the owner's
// answer: the decision to quote and, from the adapter, how to write the
// partner's approval.
func partnerNudge(cfg tracker.Partner, slug, stage, ref string, approve bool, words string) string {
	said := ""
	if words != "" {
		said = fmt.Sprintf(": %q", words)
	}
	if approve {
		return fmt.Sprintf("%s: the owner approved %s (%s)%s. Now %s, then go on to its next stage.", cfg.Label(), stage, ref, said, partner.ApprovalHow(cfg, stage, ref))
	}
	return fmt.Sprintf("%s: the owner sent %s back (%s)%s. Revise %s at the partner, then write its checkpoint awaiting the owner again; never approve it yourself. issue-cli show %s has the decision.", cfg.Label(), stage, ref, said, stage, slug)
}

// nudgeIssueWorker pastes line into the issue's live worker session: the
// session it went to, or why none was nudged.
func nudgeIssueWorker(proj *tracker.Project, issue *tracker.Issue, line string) (session, why string) {
	sessionMap, _ := sessionsByIssueSlug(proj, []*tracker.Issue{issue})
	sessions := sessionMap[issue.Slug]
	if len(sessions) == 0 {
		return "", "no worker is live on this issue: it sees the decision on issue-cli start or show"
	}
	if err := tmuxSendKeys(sessions[0].Name, []string{line}); err != nil {
		return "", err.Error()
	}
	return sessions[0].Name, ""
}

// oneLineWords is the owner's words on one line, trimmed.
func oneLineWords(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
