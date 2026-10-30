package agentrun

import (
	"fmt"
	"time"

	"github.com/michal-franc/issue-viewer/internal/jobs"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// RunStart is who starts a recurring job's run, and on whose words.
type RunStart struct {
	// Words are the owner's: quoted now, or the job's stored standing start.
	Words string
	// How the words reached the starter ("in the lead chat", "Needs you").
	How string
	// By is the session (or "owner") that starts it.
	By string
	// Auto is the standing start an unattended run starts on; nil when the
	// owner started this run.
	Auto   *jobs.Auto
	Memory int
	Agent  string
	Again  bool
}

// RunResult is a created run and its worker's start.
type RunResult struct {
	Run      *jobs.NewRun
	Issue    *tracker.Issue
	Response Response
	// Comment is the dispatch: line for the run issue.
	Comment string
}

// StartRun creates job r's run for period as an issue in its department
// and starts its worker, as issue-cli dispatch does. The caller writes
// res.Comment when the worker was dispatched (RecordRun). start is
// agentrun.Start or a test's stand-in.
func StartRun(proj *tracker.Project, r *jobs.Ref, issues []*tracker.Issue, period string, rs RunStart, workDir string, now time.Time, start func(*tracker.Project, Spec) Response) (RunResult, error) {
	var res RunResult
	n, err := jobs.PlanRun(proj, r, jobs.Runs(proj, issues, r.Key), period, rs.Again)
	if err != nil {
		return res, err
	}
	brief, err := jobs.Brief(proj, r, n, now)
	if err != nil {
		return res, err
	}
	if err := jobs.WriteRun(proj, r, n, brief, now); err != nil {
		return res, err
	}
	res.Run = n
	fresh, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		return res, err
	}
	for _, is := range fresh {
		if is.FilePath == n.File {
			res.Issue = is
		}
	}
	if res.Issue == nil {
		return res, fmt.Errorf("run issue %s was written but does not load", n.File)
	}
	res.Comment = RunComment(r.Key, period, rs, now)
	agent := rs.Agent
	if agent == "" {
		agent = "claude"
	}
	spec, refused := DispatchSpec(proj, res.Issue, agent, workDir, false)
	if refused != nil {
		res.Response = *refused
		return res, nil
	}
	res.Response = start(proj, spec)
	if res.Response.AttachCmd == "" && res.Response.Status != "error" {
		res.Response.AttachCmd, res.Response.SwitchCmd = AttachCmd(proj, spec.Session), SwitchCmd(proj, spec.Session)
	}
	return res, nil
}

// RunComment is a run's dispatch: line: the owner's words, the job and run,
// and for an unattended run who turned its standing start on and the
// memory reading.
func RunComment(key, period string, rs RunStart, now time.Time) string {
	how := fmt.Sprintf("%s; recurring job %s, run %s", rs.How, key, period)
	if a := rs.Auto; a != nil {
		how = fmt.Sprintf("recurring job %s, run %s, unattended, turned on by %s %s %s; memory %d%%",
			key, period, a.By, a.How, a.Since.Format("2006-01-02"), rs.Memory)
	}
	c := tracker.DispatchComment(rs.Words, how, now.Format("2006-01-02"))
	if rs.By != "" && rs.By != "owner" {
		c += " — recorded by " + rs.By
	}
	return c
}

// RecordRun logs a dispatched run's start in the run log.
func RecordRun(proj *tracker.Project, key string, res RunResult, rs RunStart, now time.Time) error {
	how := jobs.HowOwner
	if rs.Auto != nil {
		how = jobs.HowUnattended
	}
	return jobs.Append(proj, jobs.Event{
		Op: jobs.OpStart, Job: key, Run: res.Run.Period, Date: now.Format(time.RFC3339),
		Issue: res.Issue.Slug, How: how, Quote: rs.Words, By: rs.By, Memory: rs.Memory,
	})
}
