package decisionstats

import (
	"context"
	"errors"
	"time"

	"github.com/kacperwerema/dark-factory/internal/health"
)

// Family is the health family's name.
const Family = "decisions"

// Window is how far back a snapshot counts holds and briefings.
const Window = 30 * 24 * time.Hour

// The decisions family: the ledger's counts per snapshot (Measure), so the
// Stats page can draw them over time. Not a score component. It needs the
// issue bodies as they are: backfill leaves it out. See
// docs/workbench-files.md, "The decisions family".
func init() {
	health.Register(Family, family)
	health.Describe(Family, "the decision ledger: issue-cli decisions, issue-cli decisions log")
}

func family(_ context.Context, env health.Env) ([]health.Metric, error) {
	if env.Backfill {
		return nil, health.ErrNoPast
	}
	if env.Project == nil {
		return nil, errors.New("no project")
	}
	x, evs, err := Load(env.Project)
	if err != nil {
		return nil, err
	}
	return Metrics(Measure(Input{Index: x, Events: evs, Today: env.Now, From: env.Now.Add(-Window), To: env.Now.Add(time.Second)})), nil
}

// Metrics is c as snapshot metrics. decisions.total is always there (0 is
// a real count); the rest only once the project has a ledger.
func Metrics(c Counts) []health.Metric {
	out := []health.Metric{{Name: "decisions.total", Value: float64(c.Total), Unit: health.UnitCount, Help: "D-n decision lines in the project"}}
	if c.Empty() {
		return out
	}
	add := func(name string, labels map[string]string, v int, help string) {
		out = append(out, health.Metric{Name: name, Labels: labels, Value: float64(v), Unit: health.UnitCount, Help: help})
	}
	add("decisions.in_force", nil, c.InForce, "decisions in force: not replaced, deferred or expired")
	for _, s := range Scopes {
		if n := c.ByScope[s]; n > 0 {
			add("decisions.in_force_by_scope", map[string]string{"scope": s}, n, "decisions in force by scope kind")
		}
	}
	add("decisions.hard", nil, c.Hard, "hard decisions in force")
	add("decisions.soft", nil, c.Soft, "soft decisions in force")
	add("decisions.provisional", nil, c.Provisional, "decisions in force with no owner's words on the line yet")
	add("decisions.challenges_open", nil, c.ChallengesOpen, "decisions challenged and not resolved")
	add("decisions.holds", nil, c.HoldEvents, "gate holds by a decision in the last 30 days (held events)")
	add("decisions.injected", nil, c.Briefings, "briefings that carried the decisions block in the last 30 days")
	for _, b := range Briefings {
		if n := c.Injected[b]; n > 0 {
			add("decisions.injected_by_briefing", map[string]string{"briefing": b}, n, "briefings that carried the block, by kind, last 30 days")
		}
	}
	return out
}
