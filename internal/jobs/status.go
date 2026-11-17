package jobs

import (
	"fmt"
	"time"

	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Verdicts for a job.
const (
	VerdictNone  = ""      // not due
	VerdictStart = "start" // due, and its standing start lets the lead start it now
	VerdictOwner = "owner" // due, waits for the owner to start it
	VerdictHold  = "hold"  // due, held (Reason)
	VerdictError = "error" // the schedule does not parse
)

// Limits are an unattended start's budget: the project's own (auto-start's
// window, else the queue's limits) or auto-start's defaults. BelowFrom and
// WorkersFrom say where each came from ("auto-start default", "acme queue
// limit"); a hold names it.
type Limits struct {
	Below       int
	MaxWorkers  int // 0: no worker cap (a queue with none; memory is the limit)
	Gap         time.Duration
	MaxLoad     float64 // the owner's max_load; 0: the core count
	BelowFrom   string
	WorkersFrom string
}

// LimitDefault is where DefaultLimits' lines come from, in a hold.
const LimitDefault = "auto-start default"

// DefaultLimits are auto-start's defaults (memory under 50%, 2 workers,
// one start per 5 minutes across the machine).
var DefaultLimits = Limits{
	Below:       autostart.DefaultBelow,
	MaxWorkers:  autostart.DefaultMaxWorkers,
	Gap:         autostart.DefaultGapMinutes * time.Minute,
	BelowFrom:   LimitDefault,
	WorkersFrom: LimitDefault,
}

// from is " (src)", or "" when src is.
func from(src string) string {
	if src == "" {
		return ""
	}
	return " (" + src + ")"
}

// Input is everything one evaluation reads besides the jobs and issues.
type Input struct {
	State   *State
	Memory  autostart.Memory
	CPU     autostart.CPU
	Workers int // the project's live workers, without leads
	Last    *autostart.Start
	Limits  Limits
	Now     time.Time
	// SkipBudget: a due job with its standing start reads as start without
	// mem-guard or the worker cap (the viewer's pages, which never start it;
	// the lead's job run checks the budget).
	SkipBudget bool
}

// Status is one job now: its schedule, last run and verdict.
type Status struct {
	Key        string    `json:"key"`
	Name       string    `json:"name"`
	Department string    `json:"department"`
	Schedule   string    `json:"schedule"`
	Error      string    `json:"error,omitempty"`
	Next       time.Time `json:"next,omitempty"`
	// Period is the newest period due by now, DueAt when it was due.
	Period string    `json:"period,omitempty"`
	DueAt  time.Time `json:"due_at,omitempty"`
	Due    bool      `json:"due"`
	Missed int       `json:"missed,omitempty"`
	// The last run.
	LastSlug   string `json:"last_slug,omitempty"`
	LastPeriod string `json:"last_period,omitempty"`
	LastStatus string `json:"last_status,omitempty"`
	LastOpen   bool   `json:"last_open,omitempty"`
	// LastTokens is the last run's workflow token estimate from its stats
	// sidecar (docs/workflow-stats.md); 0 when it has none yet.
	LastTokens int    `json:"last_tokens,omitempty"`
	Runs       int    `json:"runs"`
	Auto       *Auto  `json:"auto,omitempty"`
	Verdict    string `json:"verdict"`
	Reason     string `json:"reason,omitempty"`
	// HoldKind is which machine rule holds it (autostart's kinds), ""
	// for the job's own holds (its last run open, the owner).
	HoldKind autostart.HoldKind `json:"hold_kind,omitempty"`
	Memory   int                `json:"memory,omitempty"`
}

// Evaluate is every job's status, in projects.yaml order.
func Evaluate(p *tracker.Project, issues []*tracker.Issue, in Input) []Status {
	var out []Status
	for _, r := range List(p) {
		out = append(out, Evaluate1(p, r, Runs(p, issues, r.Key), in))
	}
	return out
}

// Evaluate1 is one job's status, given its runs.
func Evaluate1(p *tracker.Project, r *Ref, runs []Run, in Input) Status {
	s := Status{Key: r.Key, Name: r.Name(), Department: r.Department, Runs: len(runs)}
	if a, ok := in.State.For(r.Key); ok {
		s.Auto = &a
	}
	if len(runs) > 0 {
		last := runs[len(runs)-1]
		s.LastSlug, s.LastPeriod, s.LastStatus, s.LastOpen = last.Issue.Slug, last.Period, last.Issue.Status, last.Open
		s.LastTokens = Tokens(last.Issue)
	}
	if r.Err != nil {
		s.Verdict, s.Error = VerdictError, r.Err.Error()
		return s
	}
	sch := r.Schedule
	s.Schedule = sch.Label()
	s.Next = sch.Next(in.Now)
	s.Period, s.DueAt = sch.Latest(in.Now)
	if s.LastPeriod >= s.Period && s.LastPeriod != "" {
		return s
	}
	s.Due = true
	s.Missed = sch.Missed(s.LastPeriod, in.Now)
	s.Verdict, s.Reason, s.HoldKind, s.Memory = decide(r, s, in)
	return s
}

// decide is a due job's verdict: held by its open last run, waiting for the
// owner without a standing start, else auto-start's budget.
func decide(r *Ref, s Status, in Input) (verdict, reason string, kind autostart.HoldKind, memory int) {
	if s.LastOpen && !r.Job.Overlap {
		return VerdictHold, fmt.Sprintf("last run %s (%s) is still in %s", s.LastSlug, s.LastPeriod, s.LastStatus), "", 0
	}
	if s.Auto == nil {
		return VerdictOwner, "no standing start: the owner starts it", "", 0
	}
	if in.SkipBudget {
		return VerdictStart, "", "", 0
	}
	m, lim := in.Memory, in.Limits
	if h := autostart.MachineHoldOf(m, in.CPU, lim.Below, lim.MaxLoad); h.Text != "" {
		if h.Kind == autostart.HoldMemory {
			h.Text += from(lim.BelowFrom) // the project's line, not mem-guard's
		}
		return VerdictHold, h.Text, h.Kind, m.Used
	}
	if lim.MaxWorkers > 0 && in.Workers >= lim.MaxWorkers {
		return VerdictHold, fmt.Sprintf("%d/%d workers live%s", in.Workers, lim.MaxWorkers, from(lim.WorkersFrom)), autostart.HoldWorkers, m.Used
	}
	if l := in.Last; l != nil {
		if ago := in.Now.Sub(l.At); ago < lim.Gap {
			return VerdictHold, fmt.Sprintf("%s started %s %d min ago (one start per %d min on this machine)",
				l.Project, l.Slug, int(ago.Minutes()), int(lim.Gap.Minutes())), autostart.HoldGap, m.Used
		}
	}
	return VerdictStart, "", "", m.Used
}

// Tokens is a run issue's token estimate: each transition's actual count
// when recorded, else its static and dynamic estimate.
func Tokens(is *tracker.Issue) int {
	if is == nil || is.FilePath == "" {
		return 0
	}
	st, err := tracker.LoadStats(is.FilePath)
	if err != nil {
		return 0
	}
	n := 0
	for _, t := range st.Transitions {
		if t.ActualTokens != nil {
			n += *t.ActualTokens
		} else {
			n += t.StaticTokens + t.DynamicTokens
		}
	}
	return n
}
