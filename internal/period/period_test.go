package period

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // Europe/Warsaw on a machine without a zoneinfo database
)

func TestBoundaries(t *testing.T) {
	warsaw := time.FixedZone("CET", 3600)
	cases := []struct {
		name string
		got  func(time.Time) time.Time
		at   time.Time
		want string
	}{
		{"sunday is the end of its week", WeekStart, time.Date(2026, 11, 1, 23, 0, 0, 0, time.UTC), "2026-10-26"},
		{"monday starts its week", WeekStart, time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), "2026-11-02"},
		{"a week over a year end", WeekStart, time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC), "2026-12-28"},
		{"local time, not UTC", WeekStart, time.Date(2026, 11, 2, 0, 30, 0, 0, warsaw), "2026-11-02"},
		{"day", DayStart, time.Date(2026, 11, 4, 13, 5, 0, 0, time.UTC), "2026-11-04"},
		{"month", MonthStart, time.Date(2026, 11, 30, 23, 0, 0, 0, time.UTC), "2026-11-01"},
		{"quarter", QuarterStart, time.Date(2026, 12, 31, 23, 0, 0, 0, time.UTC), "2026-10-01"},
		{"first quarter", QuarterStart, time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC), "2026-01-01"},
	}
	for _, c := range cases {
		got := c.got(c.at)
		if got.Format("2006-01-02 15:04") != c.want+" 00:00" {
			t.Errorf("%s: %v", c.name, got)
		}
		if got.Location() != c.at.Location() {
			t.Errorf("%s: location %v, want %v", c.name, got.Location(), c.at.Location())
		}
	}
}

func TestKeys(t *testing.T) {
	// 2027-01-01 is a Friday in ISO week 53 of 2026.
	at := time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC)
	if got := WeekKey(at); got != "2026-W53" {
		t.Errorf("WeekKey = %q", got)
	}
	if got := WeekLabel(at); got != "W53" {
		t.Errorf("WeekLabel = %q", got)
	}
	if got := MonthKey(at); got != "2027-01" {
		t.Errorf("MonthKey = %q", got)
	}
	if got := DayKey(at); got != "2027-01-01" {
		t.Errorf("DayKey = %q", got)
	}
	if got := WeekKey(time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)); got != "2026-W02" {
		t.Errorf("WeekKey pads the week: %q", got)
	}
}

func TestValidWeekKey(t *testing.T) {
	for key, want := range map[string]bool{
		"2026-W45": true, "2026-W01": true, "2026-W53": true,
		"2026-W00": false, "2026-W54": false, "2026-W5": false, "1999-W10": false,
		"2026-45": false, "": false, "../2026-W45": false, "2026-W45/x": false,
	} {
		if got := ValidWeekKey(key); got != want {
			t.Errorf("ValidWeekKey(%q) = %v", key, got)
		}
	}
}

// TestOneWeekBoundary: the code that works out a Monday or writes an ISO
// week lives here. A second copy is how spend, goals and jobs came to hold
// three of each.
func TestOneWeekBoundary(t *testing.T) {
	copyOf := regexp.MustCompile(`\.ISOWeek\(\)|\.Weekday\(\)\) *\+ *6|-W%02d`)
	root := filepath.Join("..", "..")
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "period" || d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if m := copyOf.Find(raw); m != nil {
				t.Errorf("%s holds %q: use internal/period", p, m)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// A window counts dates: on the day after a DST change it still starts at
// midnight of the date n days back, in the location of t. time.Date
// normalises the month and year.
func TestDaysBack(t *testing.T) {
	warsaw, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		at   time.Time
		n    int
		want time.Time
	}{
		{"a plain week", time.Date(2026, 11, 5, 14, 0, 0, 0, warsaw), 7, time.Date(2026, 10, 29, 0, 0, 0, 0, warsaw)},
		{"the day after a 25-hour day", time.Date(2026, 10, 26, 0, 30, 0, 0, warsaw), 7, time.Date(2026, 10, 19, 0, 0, 0, 0, warsaw)},
		{"the day after a 23-hour day", time.Date(2026, 3, 30, 0, 30, 0, 0, warsaw), 7, time.Date(2026, 3, 23, 0, 0, 0, 0, warsaw)},
		{"late in the evening", time.Date(2026, 3, 30, 23, 59, 0, 0, warsaw), 7, time.Date(2026, 3, 23, 0, 0, 0, 0, warsaw)},
		{"over a year end", time.Date(2027, 1, 3, 12, 0, 0, 0, time.UTC), 7, time.Date(2026, 12, 27, 0, 0, 0, 0, time.UTC)},
		{"zero days is today", time.Date(2026, 11, 5, 14, 0, 0, 0, warsaw), 0, time.Date(2026, 11, 5, 0, 0, 0, 0, warsaw)},
	} {
		if got := DaysBack(c.at, c.n); !got.Equal(c.want) || got.Location() != c.at.Location() {
			t.Errorf("%s: DaysBack = %v, want %v", c.name, got, c.want)
		}
	}
}

// notADateWindow names each day-count span the code keeps, by file and the
// text that holds it, and why it isn't a window over dates. A span of n×24h
// that cuts "the last n days" belongs to DaysBack: the day it counts from is
// 23 or 25 hours long twice a year.
var notADateWindow = map[string]string{
	"internal/push/push.go: 7 * 24 * time.Hour":                         "keepClosed: how long a closed event stays in the ledger, a retention span",
	"internal/viewer/timeline.go: 7 * 24 * time.Hour":                   "a tick step on the time axis",
	"internal/viewer/jobs_view.go: 7*24*time.Hour":                      "whenShort: whether a weekday name alone is unambiguous, a label",
	"cmd/issue-cli/cmd_job.go: 7*24*time.Hour":                          "the same label choice for a next run",
	"internal/tracker/health.go: time.Duration(days) * 24 * time.Hour":  "ShrinkShotsAfter: how long after done shots stay full size, a retention span",
	"internal/viewer/stats_dashboard.go: time.Duration(d)*24*time.Hour": "statsOpts.window: how much the aggregate loads (the range plus timeline.Window of slack), not the days the page shows",
	"internal/bench/fixture.go: time.Duration(i+1)*24*time.Hour":        "the fixture tasks' dates, a day apart from a fixed start",
	"internal/procs/procs.go: time.Duration(days)*24*time.Hour":         "ParseEtime: ps's elapsed time (dd-hh:mm:ss), a duration the kernel counts in seconds",
}

// dayWindow matches a span of days written in hours: 7×24h, 168h, or
// time.Duration(n) × 24h.
var dayWindow = regexp.MustCompile(`\b7 *\* *24 *\* *time\.Hour|\b168 *\* *time\.Hour|time\.Duration\([^()]*(?:\([^()]*\))?[^()]*\) *\* *24 *\* *time\.Hour`)

func TestDayWindowPattern(t *testing.T) {
	for _, in := range []string{
		"now.Add(-7 * 24 * time.Hour)",
		"168*time.Hour",
		"now.Add(-time.Duration(days) * 24 * time.Hour)",
		"time.Duration(n)*24*time.Hour",
		"time.Duration(len(x)) * 24 * time.Hour",
	} {
		if !dayWindow.MatchString(in) {
			t.Errorf("dayWindow misses %q", in)
		}
	}
	for _, in := range []string{"24 * time.Hour", "30 * 24 * time.Hour", "time.Duration(n) * time.Hour"} {
		if dayWindow.MatchString(in) {
			t.Errorf("dayWindow matches %q", in)
		}
	}
}

// TestOneDayWindow: a look-back window is DaysBack, not now minus n×24h.
func TestOneDayWindow(t *testing.T) {
	root := filepath.Join("..", "..")
	seen := map[string]bool{}
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			for _, m := range dayWindow.FindAll(raw, -1) {
				key := rel + ": " + string(m)
				seen[key] = true
				if _, ok := notADateWindow[key]; !ok {
					t.Errorf("%s holds %q: a last-n-days window is period.DaysBack (or name a span that isn't one in notADateWindow)", rel, m)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for key := range notADateWindow {
		if !seen[key] {
			t.Errorf("%s is no longer in the code: drop it from notADateWindow", key)
		}
	}
}
