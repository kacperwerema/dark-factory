// Package timing holds the one shape a test with a time limit takes so a
// loaded machine (CI beside several validates) does not fail it: the
// operation is timed in turns with a cheaper baseline over several rounds,
// and the best of each is compared. Load only ever adds time, so a best is
// the request's own cost and a burst that lands on a few samples is ridden
// out. The operation passes when its best is under an absolute limit (a
// quiet machine), or under Ratio× the best baseline (a slow one). On a
// saturated machine (1-minute load above the core count) a heavier
// operation loses more of each time slice than its light baseline, so even
// the bests drift apart: Ratio widens by SaturatedSlack there, and the
// result says so.
package timing

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/loadavg"
)

// SaturatedSlack is what Ratio is multiplied by when the machine's
// 1-minute load is above its core count. 9dead35's red run measured a
// /status 3.76× its /board baseline at load 65.8 on 14 cores (~2.2× on a
// quiet machine), and a /status made 5× slower 11.9×: doubling Ratio 3
// passes the first and still fails the second.
const SaturatedSlack = 2

// DefaultRounds is how many baseline and operation pairs a check takes
// before it fails: enough that a burst of load misses at least one sample.
const DefaultRounds = 8

// ratioRounds is how many baselines the slow-machine limit waits for: one
// baseline caught in a burst would let a slow operation pass on its first
// round. Limit passes at once.
const ratioRounds = 3

// Check is one time limit.
type Check struct {
	// Limit is the operation's limit on a quiet machine.
	Limit time.Duration
	// Ratio is how many best baselines the operation may take on a slow
	// machine; 0 keeps Limit alone.
	Ratio float64
	// Max caps the slow-machine limit: a check that the operation was cut
	// short (a timeout) proves nothing past the time the uncut one takes.
	// 0 is no cap.
	Max time.Duration
	// Rounds is how many pairs it takes before it fails (DefaultRounds
	// when 0).
	Rounds int
	// Load reads the machine's 1-minute load average and core count (ok
	// false when it can't); nil reads this machine (MachineLoad). It is
	// read only once the operation misses Limit and a Ratio applies.
	Load func() (avg1 float64, cores int, ok bool)
}

// MachineLoad is this machine's 1-minute load and core count.
func MachineLoad() (avg1 float64, cores int, ok bool) {
	avg1, _, ok = loadavg.Read()
	return avg1, runtime.NumCPU(), ok
}

// Result is what a check saw.
type Result struct {
	Slow     bool
	Best     time.Duration // the operation's best
	BestBase time.Duration // the baseline's best (0 with no baseline)
	Allowed  time.Duration // the limit Best was held to
	Rounds   int           // rounds taken
	Samples  []string      // each round: "op (base base)"
	Check    Check
	// Saturated is true when the load read during the check was above the
	// cores, and Ratio was widened by SaturatedSlack; Load and Cores are
	// the reading (0 when none was taken).
	Saturated bool
	Load      float64
	Cores     int
}

// Of times f.
func Of(f func()) time.Duration {
	start := time.Now()
	f()
	return time.Since(start)
}

// Timed turns f into a timer for Run.
func Timed(f func()) func() time.Duration {
	return func() time.Duration { return Of(f) }
}

// Run times op, and when it misses Limit times base right after it, at
// most c.Rounds times, so a slow stretch of the machine slows both. It
// stops at the first round whose bests pass: under Limit at once (a quiet
// machine pays for one op and no baseline), under the slow-machine limit
// once ratioRounds baselines are in. base may be nil: Limit alone holds
// then. The load is read at the first baseline, and once more before a
// failure when that reading was under the cores (load that climbed during
// the check).
func (c Check) Run(op, base func() time.Duration) (r Result) {
	rounds := c.Rounds
	if rounds <= 0 {
		rounds = DefaultRounds
	}
	r = Result{Check: c, Best: 1<<63 - 1, Allowed: c.Limit}
	bases := 0
	defer func() {
		if r.Slow && bases > 0 && !r.Saturated && r.readLoad() {
			r.Allowed = r.allowed()
			r.Slow = r.Best > r.Allowed
		}
	}()
	for r.Rounds < rounds {
		r.Rounds++
		d := op()
		r.Best = min(r.Best, d)
		if r.Best <= c.Limit {
			r.Slow = false
			return r
		}
		r.Slow = true
		s := round(d)
		if base != nil && c.Ratio > 0 {
			b := base()
			if bases == 0 {
				r.BestBase = b
				r.readLoad()
			}
			bases++
			r.BestBase = min(r.BestBase, b)
			r.Allowed = r.allowed()
			s += " (base " + round(b) + ")"
			if r.Best <= r.Allowed && bases >= min(ratioRounds, rounds) {
				r.Slow = false
				return r
			}
		}
		r.Samples = append(r.Samples, s)
	}
	return r
}

// SlowedMargin is how far over the check's own bounds Slowed's stand-in
// operation is held: twice the larger of Limit and Ratio× the slowest
// baseline seen.
const SlowedMargin = 2

// Slowed is the stand-in a self-check runs through c to prove c bites: op
// made factor times slower, and never under SlowedMargin× the larger of
// c.Limit and c.Ratio× the slowest baseline seen so far (base is timed once
// first when none has been yet). A fixed factor alone proves nothing on a
// machine whose baseline is slow too: a /status of 36 ms against a /board
// of 60 ms is 180 ms at 5×, inside 3× the baseline. slowBase is base, its
// samples recorded; pass both to c.Run. On a saturated machine Ratio
// widens by SaturatedSlack (= SlowedMargin), so the stand-in can pass
// there: a self-check skips on a Result that passed with Saturated set.
func (c Check) Slowed(factor float64, op, base func() time.Duration) (slowOp, slowBase func() time.Duration) {
	var worst time.Duration
	if base == nil {
		c.Ratio = 0
	} else {
		slowBase = func() time.Duration {
			b := base()
			worst = max(worst, b)
			return b
		}
	}
	slowOp = func() time.Duration {
		d := time.Duration(factor * float64(op()))
		if c.Ratio > 0 && worst == 0 {
			slowBase()
		}
		floor := max(c.Limit, time.Duration(c.Ratio*float64(worst)))
		return max(d, SlowedMargin*floor)
	}
	return slowOp, slowBase
}

// readLoad takes a load reading into r and says whether it saturated the
// machine.
func (r *Result) readLoad() bool {
	load := r.Check.Load
	if load == nil {
		load = MachineLoad
	}
	avg1, cores, ok := load()
	if !ok || cores <= 0 {
		return false
	}
	r.Load, r.Cores = avg1, cores
	r.Saturated = avg1 > float64(cores)
	return r.Saturated
}

// ratio is the Ratio in force: widened on a saturated machine.
func (r Result) ratio() float64 {
	if r.Saturated {
		return r.Check.Ratio * SaturatedSlack
	}
	return r.Check.Ratio
}

func (r Result) allowed() time.Duration {
	c := r.Check
	slow := time.Duration(r.ratio() * float64(r.BestBase))
	if c.Max > 0 {
		slow = min(slow, c.Max)
	}
	return max(c.Limit, slow)
}

// String says why a slow result failed, for t.Error.
func (r Result) String() string {
	if !r.Slow {
		return fmt.Sprintf("best %v under %v%s", round(r.Best), round(r.Allowed), r.loadNote())
	}
	var b strings.Builder
	fmt.Fprintf(&b, "best %v", round(r.Best))
	if r.Check.Ratio > 0 {
		fmt.Fprintf(&b, " against best baseline %v", round(r.BestBase))
	}
	fmt.Fprintf(&b, " (limit %v) over %d rounds: %s; want under %v", round(r.Allowed), r.Rounds, strings.Join(r.Samples, ", "), round(r.Check.Limit))
	if r.Check.Ratio > 0 {
		fmt.Fprintf(&b, ", or %g× the baseline on a slow machine", r.Check.Ratio)
		if r.Check.Max > 0 {
			fmt.Fprintf(&b, " up to %v", round(r.Check.Max))
		}
	}
	b.WriteString(r.loadNote())
	return b.String()
}

// loadNote names the load reading, and the widened ratio when it
// saturated the machine.
func (r Result) loadNote() string {
	switch {
	case r.Cores == 0:
		return ""
	case r.Saturated:
		return fmt.Sprintf(" (load %.1f on %d cores: saturated, ratio widened to %g×)", r.Load, r.Cores, r.ratio())
	}
	return fmt.Sprintf(" (load %.1f on %d cores)", r.Load, r.Cores)
}

func round(d time.Duration) string {
	switch {
	case d >= time.Second:
		return d.Round(10 * time.Millisecond).String()
	case d >= time.Millisecond:
		return d.Round(100 * time.Microsecond).String()
	}
	return d.String()
}
