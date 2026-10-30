package main

import (
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/agentrun"
	"github.com/michal-franc/issue-viewer/internal/playtestruns"
	"github.com/michal-franc/issue-viewer/internal/timeline"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// playtestWindowName is the tmux window (or session) ▶ Run playtest and
// Tick passed boxes open for slug: playtest-<slug>, sanitised as in agent
// names. One window per issue, so the run and its report never overlap.
func playtestWindowName(slug string) string {
	r := strings.NewReplacer("/", "-", ".", "-", " ", "-")
	return "playtest-" + r.Replace(slug)
}

// playtestRunURL is where the page's ▶ Run playtest posts, or "" when the
// button stays hidden: no playtest due, or the owner playtest is off.
func playtestRunURL(issueURL string, att Attention, wf *tracker.WorkflowConfig) string {
	if !att.PlaytestDue || !wf.PlaytestForOwner() {
		return ""
	}
	return issueURL + "/playtest"
}

// playtestChrome is the PLAYTEST_CHROME the window gets: the viewer's own
// setting, else on a Mac Playwright's Chrome for Testing; "" leaves it to
// the runner.
func playtestChrome() string {
	if p := strings.TrimSpace(os.Getenv("PLAYTEST_CHROME")); p != "" {
		return p
	}
	if runtime.GOOS != "darwin" {
		return ""
	}
	home, _ := os.UserHomeDir()
	return playtestruns.PlaywrightChrome(home)
}

// playtestWindowCommand runs cmd, then pauses so the outcome stays readable.
func playtestWindowCommand(cmd string) []string {
	return []string{"sh", "-c", cmd + `; printf '\nPress Enter to close this window. '; read _`}
}

// playtestSlugRe keeps a slug that reaches a shell line to plain characters.
var playtestSlugRe = regexp.MustCompile(`^[A-Za-z0-9/._-]+$`)

// playtestStart is the POST's answer: the run's window and when it began,
// so the page reads only runs from this click on.
type playtestStart struct {
	DispatchResponse
	Since string `json:"since,omitempty"`
}

// playtestState is the GET's answer: whether the window still runs and the
// judge runs started since ?since=, step by step.
type playtestState struct {
	Running bool          `json:"running"`
	Session string        `json:"session"`
	Runs    []playtestRes `json:"runs"`
	Passed  int           `json:"passed"`
	Failed  int           `json:"failed"`
	Open    int           `json:"open"`
}

type playtestRes struct {
	Session string            `json:"session"`
	Started string            `json:"started"`
	Steps   []playtestResStep `json:"steps"`
}

type playtestResStep struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Verdict string `json:"verdict"` // pass, fail, or "" (not judged)
}

// handlePlaytest is ▶ Run playtest. POST opens tmux window playtest-<slug>
// in the checkout that holds the issue's session (its worktree first) running
// the project's playtest command; ?report=1 runs the report instead (Tick
// passed boxes). A click while the window lives only brings it back. GET
// tells the page whether it still runs and what the judge runs since
// ?since= say.
func (s *Server) handlePlaytest(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	slug := strings.TrimPrefix(r.URL.Path, prefix+"/issue/")
	slug = strings.TrimSuffix(slug, "/playtest")
	issue := s.findIssueBySlug(proj, slug)
	if issue == nil {
		http.NotFound(w, r)
		return
	}
	wf := proj.LoadWorkflowForIssue(issue)
	name := playtestWindowName(issue.Slug)
	roots := checkRoots(proj, issue, wf)
	w.Header().Set("Content-Type", "application/json")

	if r.Method == http.MethodGet {
		since, _ := time.Parse(time.RFC3339, r.URL.Query().Get("since"))
		json.NewEncoder(w).Encode(playtestResult(issue.Slug, name, checkWindowLive(proj, name), since, roots))
		return
	}

	resp := playtestStart{DispatchResponse: DispatchResponse{Session: name, AttachCmd: agentrun.AttachCmd(proj, name), SwitchCmd: agentrun.SwitchCmd(proj, name)}}
	refuse := func(code int, status, why string) {
		resp.Status = status
		resp.Steps = []DispatchStep{{Name: why, Status: "error"}}
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(resp)
	}
	if !wf.PlaytestForOwner() {
		refuse(http.StatusNotFound, "off", "The owner playtest is off for this project")
		return
	}
	dir := ""
	for _, root := range roots {
		if len(playtestSessions(root, issue.Slug)) > 0 {
			dir = root
			break
		}
	}
	if dir == "" {
		refuse(http.StatusNotFound, "no-playtest", "No playtest/sessions/*.yaml names "+issue.Slug)
		return
	}
	if !playtestSlugRe.MatchString(issue.Slug) {
		refuse(http.StatusBadRequest, "error", "Slug "+issue.Slug+" has characters a shell line can't carry")
		return
	}

	if checkWindowLive(proj, name) {
		resp.Status = "already-running"
		resp.Steps = append(resp.Steps, DispatchStep{Name: name + " is already running", Status: "ok"})
	} else {
		cmd := wf.PlaytestCommandFor(issue.Slug)
		if r.URL.Query().Get("report") == "1" {
			cmd = wf.PlaytestReportFor(issue.Slug)
		}
		env := []string{"PLAYTEST_PROJECT=" + proj.Slug}
		if chrome := playtestChrome(); chrome != "" {
			env = append(env, "PLAYTEST_CHROME="+chrome)
		}
		if cfg := strings.TrimSpace(os.Getenv("ISSUE_VIEWER_CONFIG")); cfg != "" {
			env = append(env, "ISSUE_VIEWER_CONFIG="+cfg)
		}
		resp.Since = time.Now().UTC().Format(time.RFC3339)
		if !createCheckWindow(proj, name, dir, env, playtestWindowCommand(cmd), &resp.Steps) {
			resp.Status = "error"
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(resp)
			return
		}
		resp.Status = "started"
	}
	if !attachAgent(proj, name, &resp.Steps) {
		resp.Status = "error"
	}
	json.NewEncoder(w).Encode(resp)
}

// playtestResult reads the judge runs of slug started at or after since in
// the roots, newest first, with each step's verdict and the totals.
func playtestResult(slug, name string, running bool, since time.Time, roots []string) playtestState {
	st := playtestState{Running: running, Session: name, Runs: []playtestRes{}}
	runs, _ := timeline.LoadPlaytestRuns(nil, slug, roots...)
	seen := map[string]bool{}
	for i := len(runs) - 1; i >= 0; i-- {
		run := runs[i]
		if playtestruns.Mode(run.Mode) != "judge" || run.Started.Before(since) {
			continue
		}
		// No since (a click that only brought the window back): the newest
		// judge run of each session.
		if since.IsZero() {
			if seen[run.Session] {
				continue
			}
			seen[run.Session] = true
		}
		res := playtestRes{Session: run.Session, Started: run.Started.Format(time.RFC3339), Steps: []playtestResStep{}}
		for j := range run.Steps {
			step := &run.Steps[j]
			v := judgeVerdict(step)
			switch v {
			case "pass":
				st.Passed++
			case "fail":
				st.Failed++
			default:
				st.Open++
			}
			res.Steps = append(res.Steps, playtestResStep{ID: step.ID, Title: step.Title, Verdict: v})
		}
		st.Runs = append(st.Runs, res)
	}
	return st
}
