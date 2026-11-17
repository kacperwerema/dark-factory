package spend

import (
	"context"
	"errors"
	"time"

	"github.com/kacperwerema/dark-factory/internal/health"
	"github.com/kacperwerema/dark-factory/internal/period"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// ContextFamily is the health family of the context floor.
const ContextFamily = "context"

// The context family: the floor of the worker sessions (those in an
// issue's worktree) that started on the snapshot's calendar day, up to its
// time, so the Stats page's Context floor trend shows a change to
// CLAUDE.md, a briefing or the launch config. Transcripts are dated, so it
// backfills. Rows carry numbers and a role label only. See
// docs/workbench-files.md, "The context family".
func init() {
	health.Register(ContextFamily, contextFamily)
	health.Describe(ContextFamily, "per box and session: issue-cli spend --context")
}

// contextProjectsDir is where the family reads transcripts; "" is the
// default (a seam for tests).
var contextProjectsDir = ""

func contextFamily(_ context.Context, env health.Env) ([]health.Metric, error) {
	if env.Project == nil {
		return nil, errors.New("no project")
	}
	issues, err := tracker.LoadIssues(env.Project.IssueDir)
	if err != nil {
		return nil, err
	}
	now := env.Now
	if now.IsZero() {
		now = time.Now()
	}
	day := period.DaysBack(now, 0)
	return ContextMetrics(ContextSessions(env.Project, issues, day, now, Options{ProjectsDir: contextProjectsDir}), day), nil
}

// ContextMetrics are the family's rows for the worker sessions in sessions
// that started on or after day: their count always (0 is a real count), the
// median floor and median turn only when one has a floor.
func ContextMetrics(sessions []ContextSession, day time.Time) []health.Metric {
	var workers []ContextSession
	for _, s := range sessions {
		if s.Worker && !s.Start.Before(day) {
			workers = append(workers, s)
		}
	}
	st := SumContext(workers)
	role := map[string]string{"role": "worker"}
	out := []health.Metric{{Name: "context.floor.sessions", Labels: role, Value: float64(st.Floored), Unit: health.UnitCount,
		Help: "worker sessions started that day with a first turn of their own (not resumed)"}}
	if st.Floored == 0 {
		return out
	}
	return append(out,
		health.Metric{Name: "context.floor.p50", Labels: role, Value: float64(st.Floor), Unit: "tokens",
			Help: "median first-turn context (input + cache read + cache write) of those sessions: the fixed floor"},
		health.Metric{Name: "context.turn.p50", Labels: role, Value: float64(st.Median), Unit: "tokens",
			Help: "median context per turn of the worker sessions that started that day"},
		health.Metric{Name: "context.floor.share", Labels: role, Value: st.FloorShare, Unit: health.UnitShare,
			Help: "floor × turns over the context those sessions read"},
	)
}
