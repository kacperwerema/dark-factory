package health

import (
	"context"
	"errors"
	"time"

	"github.com/kacperwerema/dark-factory/internal/fseventsd"
)

// The machine family: what the Mac's own daemons cost, so a runaway one
// shows in the daily snapshot. For now fseventsd, the file-event daemon:
// its CPU and physical footprint (internal/fseventsd; ps and top, no root).
// The newest reading the viewer recorded in the last half hour, else one
// taken now. No fseventsd (not macOS) is no metric. It only knows the
// present: backfill leaves it out. See docs/workbench-files.md, "The
// machine family".
func init() {
	Register("machine", machineFamily)
	Describe("machine", "Needs you names a runaway fseventsd; fix: sudo killall fseventsd")
}

// machineReading is where the family gets its reading: a var for tests.
var machineReading = func(now time.Time) (fseventsd.Reading, error) {
	if rs := fseventsd.Load(fseventsd.Path()); len(rs) > 0 {
		if last := rs[len(rs)-1]; now.Sub(last.At) <= fseventsd.PairMax && !last.At.After(now) {
			return last, nil
		}
	}
	return fseventsd.Read(now)
}

func machineFamily(ctx context.Context, env Env) ([]Metric, error) {
	if env.Backfill {
		return nil, ErrNoPast
	}
	now := env.Now
	if now.IsZero() {
		now = time.Now()
	}
	r, err := machineReading(now)
	if errors.Is(err, fseventsd.ErrNone) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	mb := float64(int64(r.FootprintMB*10+0.5)) / 10
	return []Metric{
		{Name: "machine.fseventsd_cpu", Value: r.CPU, Unit: UnitPercent, Help: "fseventsd's CPU (ps %CPU; over 50% on two readings 10 minutes apart puts a row in Needs you)"},
		{Name: "machine.fseventsd_footprint_mb", Value: mb, Unit: UnitMB, Help: "fseventsd's physical footprint (top's MEM; over 2 GB puts a row in Needs you: sudo killall fseventsd)"},
	}, nil
}

// UnitPercent is a percent, 0..100 and more (ps's %CPU passes 100 on many cores).
const UnitPercent = "percent"
