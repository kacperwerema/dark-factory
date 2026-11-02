package viewer

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/jobs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Recurring jobs in the viewer: a due job without a standing start is a
// Needs you row with ▶ Start run (POST /p/<project>/jobs/<dept>/<job>/run);
// each department room lists its jobs; status.md has a Jobs: line. The
// viewer starts a run only on that click: it never ticks jobs on a timer.

// viewerJobs is every job's status for the pages: no mem-guard read, so a
// due job with its standing start reads as "the lead starts it". The
// standing starts live in the main checkout (.jobs/), next to .yolo/.
func viewerJobs(p *tracker.Project, issues []*tracker.Issue, now time.Time) []jobs.Status {
	if len(jobs.List(p)) == 0 {
		return nil
	}
	st, err := jobs.LoadState(ciRootFor(p), p.Slug)
	if err != nil {
		st = &jobs.State{}
	}
	return jobs.Evaluate(p, issues, jobs.Input{State: st, Now: now, SkipBudget: true, Limits: jobs.DefaultLimits})
}

// jobRunURL is where ▶ Start run posts.
func jobRunURL(prefix, key string) string { return prefix + "/jobs/" + key + "/run" }

// jobNeedsYouRows are the due jobs that wait for the owner to start them.
// Needs you holds only open issues, and a job's last run may be done, so
// the project's issues are read here, only when it has jobs.
func jobNeedsYouRows(p *tracker.Project, color, prefix string, now time.Time) []WbQueueCard {
	if len(jobs.List(p)) == 0 {
		return nil
	}
	issues, err := tracker.LoadIssues(p.IssueDir)
	if err != nil {
		return nil
	}
	var rows []WbQueueCard
	for _, s := range viewerJobs(p, issues, now) {
		if s.Verdict != jobs.VerdictOwner {
			continue
		}
		note := s.Name + " · " + s.Period + " — " + s.Department
		if first := jobBriefLine(p, s.Key); first != "" {
			note += ": " + first
		}
		if s.LastSlug != "" {
			note += fmt.Sprintf(" · last run %s %s", s.LastPeriod, s.LastStatus)
			if s.LastTokens > 0 {
				note += fmt.Sprintf(" (~%d tokens)", s.LastTokens)
			}
		}
		if s.Missed > 0 {
			note += fmt.Sprintf(" · %d missed", s.Missed)
		}
		url := prefix + "/company"
		if s.LastSlug != "" {
			url = prefix + "/issue/" + s.LastSlug
		}
		rows = append(rows, WbQueueCard{
			Kind: "job", Label: "Due job", Tone: "you",
			Project: p.Name, ProjectSlug: p.Slug, Color: color,
			Slug: s.Key, Title: s.Name, Note: note,
			Since: s.DueAt, Age: shortDuration(now.Sub(s.DueAt)),
			CTA: "Open", URL: url,
			RunURL: jobRunURL(prefix, s.Key), RunLabel: "▶ Start run",
		})
	}
	return rows
}

// jobBriefLine is the first non-heading line of the job's brief, cut to a
// sentence's length; "" when it can't be read.
func jobBriefLine(p *tracker.Project, key string) string {
	r, err := jobs.Find(p, key)
	if err != nil {
		return ""
	}
	data, err := jobs.BriefText(p, r.Job)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
		if line == "" {
			continue
		}
		if len(line) > 120 {
			line = line[:117] + "…"
		}
		return line
	}
	return ""
}

// WbJobLine is one job in its department's room.
type WbJobLine struct {
	Key, Name, Schedule, Next string
	// Last is the last run ("2026-W43 done"), LastURL its station.
	Last, LastURL string
	// State is due / held / waits / "" and Note says why.
	State, Note string
	Auto        bool
	Error       string
}

// deptJobLines are department dept's jobs for its room.
func deptJobLines(prefix, dept string, all []jobs.Status, now time.Time) []WbJobLine {
	var out []WbJobLine
	for _, s := range all {
		if s.Department != dept {
			continue
		}
		l := WbJobLine{Key: s.Key, Name: s.Name, Schedule: s.Schedule, Auto: s.Auto != nil, Error: s.Error}
		if !s.Next.IsZero() {
			l.Next = whenShort(s.Next, now)
		}
		if s.LastSlug != "" {
			l.Last, l.LastURL = s.LastPeriod+" "+s.LastStatus, prefix+"/issue/"+s.LastSlug
		}
		switch s.Verdict {
		case jobs.VerdictOwner:
			l.State, l.Note = "waits", "due "+s.Period+": waits for you (Needs you)"
		case jobs.VerdictStart:
			l.State, l.Note = "due", "due "+s.Period+": the lead starts it at its check-in"
		case jobs.VerdictHold:
			l.State, l.Note = "held", "due "+s.Period+": "+s.Reason
		}
		out = append(out, l)
	}
	return out
}

// whenShort is "Mon 07:00" within a week, else the date.
func whenShort(t, now time.Time) string {
	if d := t.Sub(now); d >= 0 && d < 7*24*time.Hour {
		return t.Format("Mon 15:04")
	}
	return t.Format("2006-01-02")
}

// jobsStatusLine is status.md's "Jobs:" line; "" without jobs.
func jobsStatusLine(all []jobs.Status) string {
	if len(all) == 0 {
		return ""
	}
	var waits, start, held, broken int
	for _, s := range all {
		switch s.Verdict {
		case jobs.VerdictOwner:
			waits++
		case jobs.VerdictStart:
			start++
		case jobs.VerdictHold:
			held++
		case jobs.VerdictError:
			broken++
		}
	}
	parts := []string{plural(len(all), "job", "jobs")}
	if n := waits + start + held; n == 0 {
		parts = append(parts, "none due")
	} else {
		if waits > 0 {
			parts = append(parts, fmt.Sprintf("%d due waiting for you", waits))
		}
		if start > 0 {
			parts = append(parts, fmt.Sprintf("%d due for the lead to start", start))
		}
		if held > 0 {
			parts = append(parts, fmt.Sprintf("%d held", held))
		}
	}
	if broken > 0 {
		parts = append(parts, fmt.Sprintf("%d broken (issue-cli health lint --check departments)", broken))
	}
	return "Jobs: " + strings.Join(parts, ", ")
}

// handleJobRun serves POST /p/<project>/jobs/<dept>/<job>/run: the owner's
// ▶ Start run on a due job. It creates the run and starts its worker like
// issue-cli job run --quote, quoting the click.
func (s *Server) handleJobRun(w http.ResponseWriter, r *http.Request, proj *tracker.Project, key string) {
	w.Header().Set("Content-Type", "application/json")
	fail := func(code int, why string) {
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(agentrun.Response{Status: "error", Steps: []agentrun.Step{{Name: why}}})
	}
	ref, err := jobs.Find(proj, key)
	if err != nil {
		fail(http.StatusNotFound, err.Error())
		return
	}
	if ref.Err != nil {
		fail(http.StatusConflict, "job "+ref.Key+": "+ref.Err.Error())
		return
	}
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		fail(http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now()
	st := jobs.Evaluate1(proj, ref, jobs.Runs(proj, issues, ref.Key), jobs.Input{State: &jobs.State{}, Now: now, SkipBudget: true})
	if !st.Due {
		fail(http.StatusConflict, fmt.Sprintf("%s is not due: its %s run is there (%s)", ref.Key, st.LastPeriod, st.LastSlug))
		return
	}
	rs := agentrun.RunStart{Words: "▶ Start run on Needs you", How: "Needs you", By: "owner"}
	res, err := agentrun.StartRun(proj, ref, issues, st.Period, rs, resolveProjectWorkDir(proj), now, startDispatch)
	if err != nil {
		fail(http.StatusConflict, err.Error())
		return
	}
	if res.Response.Status == "dispatched" {
		if err := tracker.AddCommentBy(res.Issue.FilePath, 0, res.Comment, "app", tracker.OwnerAuthor); err != nil {
			log.Printf("jobs: %s: dispatch comment: %v", res.Issue.Slug, err)
		}
		if err := agentrun.RecordRun(proj, ref.Key, res, rs, now); err != nil {
			log.Printf("jobs: %s: run log: %v", ref.Key, err)
		}
		log.Printf("jobs: %s run %s started from Needs you (%s)", ref.Key, st.Period, res.Issue.Slug)
	}
	json.NewEncoder(w).Encode(res.Response)
}
