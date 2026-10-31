// Package deptfamily is the `departments` health family: per department of
// projects.yaml, whether its goals are moving, the owner reviews its outputs
// in time, its jobs run when due and its handoffs are taken up, rolled into
// one on-track share (the `departments` score component). A project without
// departments gets no rows. Rows carry box keys, goal ids and numbers, never
// titles (Settled rule "Private projects stay private"). Names are documented
// in docs/workbench-files.md, "The departments family"; keep them stable.
package deptfamily

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/connections"
	"github.com/michal-franc/issue-viewer/internal/goals"
	"github.com/michal-franc/issue-viewer/internal/health"
	"github.com/michal-franc/issue-viewer/internal/jobs"
	"github.com/michal-franc/issue-viewer/internal/outputs"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// Family is the family's name, the group its rows carry.
const Family = "departments"

// Metric names.
const (
	GoalProgress   = "departments.goal.progress"
	GoalsProgress  = "departments.goals.progress"
	GoalsMoving    = "departments.goals.moving"
	GoalsDelta     = "departments.goals.delta"
	OutputsReview  = "departments.outputs.reviewed"
	OutputsWaiting = "departments.outputs.waiting"
	JobsOnTime     = "departments.jobs.on_time"
	JobsDue        = "departments.jobs.due"
	JobsMissed     = "departments.jobs.missed"
	HandoffsOpen   = "departments.handoffs.open"
	HandoffsOldest = "departments.handoffs.oldest_days"
	OnTrack        = "departments.on_track"
	Count          = "departments.count"
	// Measured is how many departments have an on-track row; the rest have
	// nothing to measure yet (StartsMeasuring).
	Measured = "departments.measured"
)

// StartsMeasuring is what gives a department its first measure, for the
// pages' "not measured yet".
const StartsMeasuring = "a goal value with one from 7 days before, an output reviewed (or past its review window), or a job's first run"

// Windows the measures look over.
const (
	// movingDays is how far back goals moving compares.
	movingDays = 7
	// outputsWindow is how far back outputs count for the reviewed share.
	outputsWindow = 30 * 24 * time.Hour
	// jobGrace is how long a due job may wait for its run before it is late.
	jobGrace = 24 * time.Hour
)

func init() {
	health.Register(Family, family)
	health.Describe(Family, "departments on track: /health#departments")
}

func family(_ context.Context, env health.Env) ([]health.Metric, error) {
	if env.Backfill {
		return nil, health.ErrNoPast
	}
	if env.Project == nil {
		return nil, errors.New("no project")
	}
	if len(env.Project.Departments) == 0 {
		return nil, nil
	}
	issues, err := tracker.LoadIssues(env.Project.IssueDir)
	if err != nil {
		return nil, err
	}
	past := health.Before(env.Root, env.Now.AddDate(0, 0, -movingDays))
	return Measure(Input{Project: env.Project, Issues: issues, Config: env.Config, Past: past, Now: env.Now,
		Comments: func(i *tracker.Issue) []tracker.Comment {
			c, _ := tracker.LoadComments(i.FilePath)
			return c
		}}), nil
}

// Input is what Measure reads.
type Input struct {
	Project  *tracker.Project
	Issues   []*tracker.Issue
	Comments func(*tracker.Issue) []tracker.Comment
	Config   *tracker.HealthConfig
	// Past is the snapshot from 7 days before (nil: none yet).
	Past *health.Snapshot
	Now  time.Time
}

// dept collects one department's measures before they become rows.
type dept struct {
	key    string
	shares []float64
}

// Measure is the family's rows for the project's departments.
func Measure(in Input) []health.Metric {
	p := in.Project
	if p == nil || len(p.Departments) == 0 {
		return nil
	}
	var out []health.Metric
	rep := goals.Resolve(p, in.Now)
	byKey := map[string]*goals.Owner{}
	for _, o := range rep.Departments {
		byKey[o.Key] = o
	}
	outs, _ := outputs.Load(p)
	reviewDays, _ := in.Config.Limit("outputs_review_days")
	if reviewDays <= 0 {
		reviewDays = tracker.HealthLimitDefaults["outputs_review_days"]
	}
	statuses := jobs.Evaluate(p, in.Issues, jobs.Input{Now: in.Now, SkipBudget: true})
	handoffs := connections.ListHandoffs(p, in.Issues, in.Comments, connections.HandoffFilter{}, in.Now)
	created := map[string]string{}
	for _, is := range in.Issues {
		created[is.Slug] = is.Created
	}

	var tracked []float64
	for _, d := range p.Departments {
		key := tracker.DepartmentKey(d.Name)
		l := func(kv ...string) map[string]string {
			m := map[string]string{"department": key}
			for i := 0; i+1 < len(kv); i += 2 {
				m[kv[i]] = kv[i+1]
			}
			return m
		}
		dm := &dept{key: key}
		out = append(out, goalRows(byKey[key], in.Past, l, dm)...)
		out = append(out, outputRows(outs, key, reviewDays, in.Now, l, dm)...)
		out = append(out, jobRows(statuses, d.Name, in.Now, l, dm)...)
		out = append(out, handoffRows(handoffs, key, created, in.Now, l)...)
		out = append(out, spendRows(p, d, l)...)
		if len(dm.shares) > 0 {
			v := round(mean(dm.shares))
			tracked = append(tracked, v)
			out = append(out, health.Metric{Name: OnTrack, Labels: l(), Value: v, Unit: health.UnitShare,
				Help: "Department on track: mean of goals moving, outputs reviewed in time and jobs on time"})
		}
	}
	out = append(out,
		health.Metric{Name: Count, Value: float64(len(p.Departments)), Unit: health.UnitCount, Help: "Departments in projects.yaml"},
		health.Metric{Name: Measured, Value: float64(len(tracked)), Unit: health.UnitCount, Help: "Departments with something to measure (an on-track share)"})
	if len(tracked) > 0 {
		out = append(out, health.Metric{Name: OnTrack, Value: round(mean(tracked)), Unit: health.UnitShare,
			Help: "Department on track: mean of goals moving, outputs reviewed in time and jobs on time"})
	}
	return out
}

type labeler func(kv ...string) map[string]string

// goalRows: each goal's progress, their mean, and against the snapshot 7
// days before how many moved and by how much.
func goalRows(o *goals.Owner, past *health.Snapshot, l labeler, dm *dept) []health.Metric {
	if o == nil {
		return nil
	}
	var out []health.Metric
	var now, deltas []float64
	moving, compared := 0, 0
	for _, g := range o.Goals {
		if g.Progress == nil {
			continue
		}
		v := float64(*g.Progress)
		now = append(now, v)
		out = append(out, health.Metric{Name: GoalProgress, Labels: l("goal", g.ID), Value: v, Unit: health.UnitCount, Help: "Each department goal's progress towards its target, 0–100"})
		if past == nil {
			continue
		}
		then, ok := past.Value(GoalProgress, l("goal", g.ID))
		if !ok {
			continue
		}
		compared++
		deltas = append(deltas, v-then)
		if v > then || v >= 100 {
			moving++
		}
	}
	if len(now) == 0 {
		return out
	}
	out = append(out, health.Metric{Name: GoalsProgress, Labels: l(), Value: math.Round(mean(now)), Unit: health.UnitCount, Help: "Mean progress of a department's goals, 0–100"})
	if compared > 0 {
		share := round(float64(moving) / float64(compared))
		dm.shares = append(dm.shares, share)
		out = append(out,
			health.Metric{Name: GoalsMoving, Labels: l(), Value: share, Unit: health.UnitShare, Help: "Department goals whose progress rose in 7 days or is at the target"},
			health.Metric{Name: GoalsDelta, Labels: l(), Value: math.Round(mean(deltas)*10) / 10, Unit: "delta", Help: "Mean change in a department's goal progress over 7 days, points"})
	}
	return out
}

// outputRows: outputs in review now, and of the last 30 days' outputs the
// share reviewed within the review window (young ones still in review are
// not late yet and don't count).
func outputRows(list []*outputs.Output, key string, days float64, now time.Time, l labeler, dm *dept) []health.Metric {
	window := time.Duration(days * 24 * float64(time.Hour))
	waiting, inTime, counted, any := 0, 0, 0, false
	for _, o := range list {
		if o.Department != key {
			continue
		}
		any = true
		if o.State == outputs.StateReview {
			waiting++
		}
		if o.Added.IsZero() || now.Sub(o.Added) > outputsWindow {
			continue
		}
		reviewed := !o.Reviewed.IsZero()
		if !reviewed && now.Sub(o.Added) <= window {
			continue
		}
		counted++
		if reviewed && o.Reviewed.Sub(o.Added) <= window {
			inTime++
		}
	}
	if !any {
		return nil
	}
	out := []health.Metric{{Name: OutputsWaiting, Labels: l(), Value: float64(waiting), Unit: health.UnitCount, Help: "Department outputs waiting for the owner's review"}}
	if counted > 0 {
		share := round(float64(inTime) / float64(counted))
		dm.shares = append(dm.shares, share)
		out = append(out, health.Metric{Name: OutputsReview, Labels: l(), Value: share, Unit: health.UnitShare, Help: "Department outputs of the last 30 days reviewed within health.limits.outputs_review_days"})
	}
	return out
}

// jobRows: of the department's jobs that have run at least once, the share
// not late (due over a day with no run); how many are due and how many
// periods were missed. A job that has never run has nothing to measure
// yet: it counts as due, not as late.
func jobRows(statuses []jobs.Status, name string, now time.Time, l labeler, dm *dept) []health.Metric {
	total, ran, late, due, missed := 0, 0, 0, 0, 0
	for _, s := range statuses {
		if s.Department != name || s.Verdict == jobs.VerdictError {
			continue
		}
		total++
		if s.Runs > 0 {
			ran++
		}
		if !s.Due {
			continue
		}
		due++
		missed += s.Missed
		if s.Runs > 0 && now.Sub(s.DueAt) > jobGrace {
			late++
		}
	}
	if total == 0 {
		return nil
	}
	out := []health.Metric{
		{Name: JobsDue, Labels: l(), Value: float64(due), Unit: health.UnitCount, Help: "Department jobs due now"},
		{Name: JobsMissed, Labels: l(), Value: float64(missed), Unit: health.UnitCount, Help: "Job periods missed since each job's last run"},
	}
	if ran == 0 {
		return out
	}
	share := round(float64(ran-late) / float64(ran))
	dm.shares = append(dm.shares, share)
	return append([]health.Metric{{Name: JobsOnTime, Labels: l(), Value: share, Unit: health.UnitShare, Help: "Department jobs that have run, not late (due for over a day without a run)"}}, out...)
}

// handoffRows: open handoffs to (in) and from (out) the department, and the
// age of the oldest open one in its inbox.
func handoffRows(list []connections.Handoff, key string, created map[string]string, now time.Time, l labeler) []health.Metric {
	in, out, oldest, any := 0, 0, -1.0, false
	for _, h := range list {
		if h.To != key && h.From != key {
			continue
		}
		any = true
		if !h.Open() {
			continue
		}
		if h.From == key {
			out++
		}
		if h.To != key {
			continue
		}
		in++
		if t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(created[h.Slug]), now.Location()); err == nil {
			if age := math.Floor(now.Sub(t).Hours() / 24); age > oldest {
				oldest = age
			}
		}
	}
	if !any {
		return nil
	}
	rows := []health.Metric{
		{Name: HandoffsOpen, Labels: l("dir", "in"), Value: float64(in), Unit: health.UnitCount, Help: "Open handoffs to (in) and from (out) a department"},
		{Name: HandoffsOpen, Labels: l("dir", "out"), Value: float64(out), Unit: health.UnitCount, Help: "Open handoffs to (in) and from (out) a department"},
	}
	if oldest >= 0 {
		rows = append(rows, health.Metric{Name: HandoffsOldest, Labels: l(), Value: math.Max(0, oldest), Unit: health.UnitCount, Help: "Age in days of a department's oldest open inbox handoff"})
	}
	return rows
}

// spendRows is the hook for department budgets: once that work lands it
// returns departments.spend{department}. Nothing yet.
func spendRows(_ *tracker.Project, _ tracker.Department, _ labeler) []health.Metric { return nil }

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

func round(v float64) float64 { return math.Round(v*1000) / 1000 }
