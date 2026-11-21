package leaks

import (
	"context"
	"time"

	"github.com/kacperwerema/dark-factory/internal/health"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The sessions health family: what runs past its bound (LongReport), as
// counts in the daily snapshot. A lead past its rotation is counted in its
// own project's snapshot; a grown process is the machine's, so every
// project's snapshot counts it, as the machine family does fseventsd. An
// orphan lead needs every project to be told apart from another project's,
// so only issue-cli health, the lead tick and Needs you name those. It
// only knows the present: backfill leaves it out.
func init() {
	health.Register("sessions", sessionsFamily)
	health.Describe("sessions", "a lead past its rotation or a process that doubled in a day; fix: the command issue-cli health prints for it")
}

func sessionsFamily(_ context.Context, env health.Env) ([]health.Metric, error) {
	if env.Backfill {
		return nil, health.ErrNoPast
	}
	now := env.Now
	if now.IsZero() {
		now = time.Now()
	}
	var projects []*tracker.Project
	if env.Project != nil {
		projects = []*tracker.Project{env.Project}
	}
	r := LongNow(projects, now)
	own := 0
	for _, s := range r.Sessions {
		if s.Kind == SessionUnrotated {
			own++
		}
	}
	return []health.Metric{
		{Name: "sessions.leads_unrotated", Value: float64(own), Unit: health.UnitCount, Help: "This project's leads that ran over 26 h without rotating (issue-cli health names each with its rotate)"},
		{Name: "sessions.grown_processes", Value: float64(len(r.Grown)), Unit: health.UnitCount, Help: "Long-lived factory processes (viewer, leads, CI runs) whose RSS more than doubled in 24 h"},
	}, nil
}
