package autostart

import (
	"fmt"
	"runtime"
	"strconv"

	"github.com/kacperwerema/dark-factory/internal/loadavg"
	"github.com/kacperwerema/dark-factory/internal/recent"
)

// CPU is the machine's CPU load: the 1- and 5-minute load averages and the
// cores. Load holds a start only while both averages are at or over the
// limit (the core count unless the owner set max_load): one make validate
// pushes Avg1 over the cores for a minute or two, Avg5 stays under.
type CPU struct {
	Found bool    // a reading was taken
	Avg1  float64 // 1-minute load average
	Avg5  float64 // 5-minute load average
	Cores int     // runtime.NumCPU()
}

// ReadLoad reads the load now. A var so tests never depend on the
// machine's real load.
var ReadLoad = readLoad

var lastLoad recent.Reading[CPU]

func readLoad() CPU { return lastLoad.Get(readLoadNow) }

func readLoadNow() CPU {
	l := CPU{Cores: runtime.NumCPU()}
	l.Avg1, l.Avg5, l.Found = loadavg.Read()
	return l
}

// LoadLimit is the load under which a start is allowed: MaxLoad when the
// owner set one, else the core count.
func (s *State) LoadLimit(cores int) float64 {
	if s != nil && s.MaxLoad > 0 {
		return s.MaxLoad
	}
	return float64(cores)
}

// OwnerMaxLoad is the max_load the owner set, while auto-start is on; 0
// when it is off or none was set (the core count).
func (s *State) OwnerMaxLoad() float64 {
	if s == nil || !s.On {
		return 0
	}
	return s.MaxLoad
}

// MaxLoadOf is project's OwnerMaxLoad under root; an unreadable state is 0.
func MaxLoadOf(root, project string) float64 {
	st, err := Load(root, project)
	if err != nil {
		return 0
	}
	return st.OwnerMaxLoad()
}

// FormatLoad prints a load average with one decimal ("4.3").
func FormatLoad(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }

// FormatLimit prints a limit without a trailing ".0" ("14", "10.5").
func FormatLimit(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// Avgs prints the 1- and 5-minute averages: "4.3/5.0".
func (l CPU) Avgs() string { return FormatLoad(l.Avg1) + "/" + FormatLoad(l.Avg5) }

// LoadLine is "load 4.3/5.0 on 14 cores (max 14)" (the 1- and 5-minute
// averages), or "load unknown" without a reading.
func LoadLine(l CPU, limit float64) string {
	if !l.Found {
		return "load unknown"
	}
	return fmt.Sprintf("load %s on %d cores (max %s)", l.Avgs(), l.Cores, FormatLimit(limit))
}
