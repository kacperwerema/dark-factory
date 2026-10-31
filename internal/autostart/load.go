package autostart

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// CPU is the machine's CPU load: the 1-minute load average and the cores.
// Auto-start starts while Avg1 is under the window's limit (the core count
// unless the owner set max_load).
type CPU struct {
	Found bool    // a reading was taken
	Avg1  float64 // 1-minute load average
	Cores int     // runtime.NumCPU()
}

// ReadLoad reads the load now. A var so tests never depend on the
// machine's real load.
var ReadLoad = readLoad

func readLoad() CPU {
	l := CPU{Cores: runtime.NumCPU()}
	var avg float64
	var err error
	switch runtime.GOOS {
	case "darwin", "freebsd", "netbsd", "openbsd", "dragonfly":
		var out []byte
		out, err = exec.Command("sysctl", "-n", "vm.loadavg").Output()
		if err == nil {
			avg, err = parseSysctlLoadavg(string(out))
		}
	case "linux":
		var data []byte
		data, err = os.ReadFile("/proc/loadavg")
		if err == nil {
			avg, err = parseProcLoadavg(string(data))
		}
	default:
		err = fmt.Errorf("no load reader for %s", runtime.GOOS)
	}
	if err != nil {
		return l
	}
	l.Found, l.Avg1 = true, avg
	return l
}

// parseSysctlLoadavg reads macOS / BSD `sysctl -n vm.loadavg`:
// "{ 4.28 5.00 7.63 }".
func parseSysctlLoadavg(s string) (float64, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, "{"), "}"))
	return firstFloat(s, "vm.loadavg")
}

// parseProcLoadavg reads Linux /proc/loadavg: "0.52 0.58 0.59 1/389 12345".
func parseProcLoadavg(s string) (float64, error) {
	return firstFloat(s, "/proc/loadavg")
}

func firstFloat(s, what string) (float64, error) {
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0, fmt.Errorf("%s: empty", what)
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("%s: %q is not a load average", what, f[0])
	}
	return v, nil
}

// LoadLimit is the load under which a start is allowed: MaxLoad when the
// owner set one, else the core count.
func (s *State) LoadLimit(cores int) float64 {
	if s != nil && s.MaxLoad > 0 {
		return s.MaxLoad
	}
	return float64(cores)
}

// FormatLoad prints a load average with one decimal ("4.3").
func FormatLoad(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }

// FormatLimit prints a limit without a trailing ".0" ("14", "10.5").
func FormatLimit(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// LoadLine is "load 4.3 on 14 cores (max 14)", or "load unknown" without a
// reading.
func LoadLine(l CPU, limit float64) string {
	if !l.Found {
		return "load unknown"
	}
	return fmt.Sprintf("load %s on %d cores (max %s)", FormatLoad(l.Avg1), l.Cores, FormatLimit(limit))
}
