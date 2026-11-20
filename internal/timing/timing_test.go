package timing

import (
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
)

func TestMain(m *testing.M) { tmuxguard.Main(m) }

// timer is a stand-in clock: each call returns d, times factor for the
// first spiked calls (every call when spiked < 0).
func timer(d time.Duration, spiked int, factor time.Duration) func() time.Duration {
	n := 0
	return func() time.Duration {
		n++
		if spiked < 0 || n <= spiked {
			return d * factor
		}
		return d
	}
}

func steady(d time.Duration) func() time.Duration { return timer(d, 0, 1) }

// A burst on the operation's first samples (three in a row) fails nothing
// while later samples are back to normal; an operation slow in every
// sample still fails, and says so with its samples.
func TestRunRidesOutABurst(t *testing.T) {
	c := Check{Limit: 150 * time.Millisecond, Ratio: 3, Load: quiet}
	if r := c.Run(timer(200*time.Millisecond, 3, 5), steady(80*time.Millisecond)); r.Slow || r.Rounds != 4 {
		t.Errorf("a burst on the first three samples: %+v", r)
	}
	r := c.Run(timer(200*time.Millisecond, -1, 5), steady(80*time.Millisecond))
	if !r.Slow || r.Rounds != DefaultRounds || len(r.Samples) != DefaultRounds {
		t.Fatalf("slow in every sample: %+v", r)
	}
	if s := r.String(); !strings.Contains(s, "best 1s against best baseline 80ms (limit 240ms) over 8 rounds") || !strings.Contains(s, "3× the baseline") {
		t.Errorf("message = %s", s)
	}
}

// A burst on the baseline alone never passes a slow operation: the
// baseline's best is its own cost.
func TestRunBaselineBurstDoesNotHideASlowOperation(t *testing.T) {
	c := Check{Limit: 150 * time.Millisecond, Ratio: 3, Rounds: 4, Load: quiet}
	if r := c.Run(steady(time.Second), timer(80*time.Millisecond, 2, 10)); !r.Slow || r.BestBase != 80*time.Millisecond {
		t.Errorf("a baseline burst passed a slow operation: %+v", r)
	}
}

// The quiet-machine limit passes on its own and stops at the first round;
// with no baseline Limit alone holds.
func TestRunLimitAlone(t *testing.T) {
	calls := 0
	op := func() time.Duration { calls++; return 10 * time.Millisecond }
	if r := (Check{Limit: time.Second}).Run(op, nil); r.Slow || calls != 1 || r.BestBase != 0 {
		t.Errorf("under the limit: %+v after %d calls", r, calls)
	}
	r := (Check{Limit: time.Second, Rounds: 2}).Run(steady(2*time.Second), nil)
	if !r.Slow || r.Rounds != 2 || strings.Contains(r.String(), "baseline") {
		t.Errorf("over the limit with no baseline: %+v %s", r, r)
	}
}

// Max caps the slow-machine limit: a timeout check whose baseline crawls
// still fails an operation that ran as long as the uncut one would.
func TestRunMaxCapsTheSlowLimit(t *testing.T) {
	c := Check{Limit: 3 * time.Second, Ratio: 3, Max: 4500 * time.Millisecond, Rounds: 2, Load: quiet}
	if r := c.Run(steady(5*time.Second), steady(10*time.Second)); !r.Slow || r.Allowed != 4500*time.Millisecond {
		t.Errorf("an uncut operation passed under a crawling baseline: %+v", r)
	}
	if r := c.Run(steady(4*time.Second), steady(2*time.Second)); r.Slow {
		t.Errorf("4s against a 2s baseline (limit 4.5s): %+v", r)
	}
}

func TestOfTimesTheCall(t *testing.T) {
	// one sample: a lower bound; load only adds time.
	if d := Of(func() { time.Sleep(20 * time.Millisecond) }); d < 20*time.Millisecond {
		t.Errorf("Of = %v", d)
	}
	ran := false
	if Timed(func() { ran = true })(); !ran {
		t.Error("Timed did not run the call")
	}
}

// quiet and saturated are stand-in load readings for a 14-core machine.
func quiet() (float64, int, bool)     { return 4, 14, true }
func saturated() (float64, int, bool) { return 65.8, 14, true }

// The samples 9dead35's red run took (load 65.8 on 14 cores): /status best
// 211.1 ms against /board 56.1 ms is 3.76× the baseline. On a saturated
// machine (load above the core count) Ratio doubles and it passes, saying
// so; on a quiet reading it still fails; a page 5× slower (11.9× in that
// run) fails on a saturated machine too.
func TestRunWidensTheRatioOnASaturatedMachine(t *testing.T) {
	c := Check{Limit: 150 * time.Millisecond, Ratio: 3, Load: saturated}
	r := c.Run(steady(211100*time.Microsecond), steady(56100*time.Microsecond))
	if r.Slow || !r.Saturated || r.Allowed != 336600*time.Microsecond {
		t.Errorf("3.76× on a saturated machine: %+v", r)
	}
	if s := r.String(); !strings.Contains(s, "load 65.8 on 14 cores") || !strings.Contains(s, "6×") {
		t.Errorf("a widened pass does not say so: %s", s)
	}
	c.Load = quiet
	if r := c.Run(steady(211100*time.Microsecond), steady(56100*time.Microsecond)); !r.Slow || r.Saturated {
		t.Errorf("3.76× on a quiet machine passed: %+v", r)
	}
	c.Load = saturated
	r = c.Run(steady(678300*time.Microsecond), steady(57100*time.Microsecond))
	if !r.Slow {
		t.Fatalf("a page 5× slower passed on a saturated machine: %+v", r)
	}
	if s := r.String(); !strings.Contains(s, "load 65.8 on 14 cores") {
		t.Errorf("the failure does not name the load: %s", s)
	}
}

// The load is read only once the operation misses Limit (a quiet pass
// runs no sysctl), and again before a failure when the first reading was
// under the cores; an unreadable load counts as quiet; Limit alone (no
// Ratio) is never widened.
func TestRunReadsTheLoadOnlyWhenSlow(t *testing.T) {
	reads := 0
	counting := func() (float64, int, bool) { reads++; return 1, 14, true }
	c := Check{Limit: time.Second, Ratio: 3, Load: counting}
	if r := c.Run(steady(10*time.Millisecond), steady(time.Millisecond)); r.Slow || reads != 0 {
		t.Errorf("a quiet pass read the load %d times: %+v", reads, r)
	}
	climbing := func() (float64, int, bool) { reads++; return float64(reads) * 10, 14, true }
	reads = 0
	c = Check{Limit: 150 * time.Millisecond, Ratio: 3, Load: climbing}
	if r := c.Run(steady(211100*time.Microsecond), steady(56100*time.Microsecond)); r.Slow || reads != 2 {
		t.Errorf("load that climbs past the cores during the run: %+v after %d reads", r, reads)
	}
	unread := func() (float64, int, bool) { return 0, 0, false }
	c.Load = unread
	if r := c.Run(steady(211100*time.Microsecond), steady(56100*time.Microsecond)); !r.Slow || r.Saturated {
		t.Errorf("an unreadable load widened the ratio: %+v", r)
	}
	c = Check{Limit: time.Second, Rounds: 2, Load: saturated}
	if r := c.Run(steady(2*time.Second), nil); !r.Slow || r.Allowed != time.Second {
		t.Errorf("Limit alone widened: %+v", r)
	}
}
