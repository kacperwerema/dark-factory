// Package loadavg reads the machine's 1- and 5-minute load averages (macOS
// and BSD sysctl vm.loadavg, Linux /proc/loadavg). It is a kernel leaf:
// auto-start holds starts on it (internal/autostart) and local CI records
// it beside a test run's wall time (internal/ci).
package loadavg

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// Read takes one reading now: the 1- and 5-minute averages. ok is false
// when this OS has no reader or the read failed.
func Read() (avg1, avg5 float64, ok bool) {
	var avg [2]float64
	var err error
	switch runtime.GOOS {
	case "darwin", "freebsd", "netbsd", "openbsd", "dragonfly":
		var out []byte
		out, err = exec.Command("sysctl", "-n", "vm.loadavg").Output()
		if err == nil {
			avg, err = ParseSysctl(string(out))
		}
	case "linux":
		var data []byte
		data, err = os.ReadFile("/proc/loadavg")
		if err == nil {
			avg, err = ParseProc(string(data))
		}
	default:
		err = fmt.Errorf("no load reader for %s", runtime.GOOS)
	}
	if err != nil {
		return 0, 0, false
	}
	return avg[0], avg[1], true
}

// ParseSysctl reads macOS / BSD `sysctl -n vm.loadavg`:
// "{ 4.28 5.00 7.63 }": the 1- and 5-minute averages.
func ParseSysctl(s string) ([2]float64, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, "{"), "}"))
	return firstTwo(s, "vm.loadavg")
}

// ParseProc reads Linux /proc/loadavg: "0.52 0.58 0.59 1/389 12345".
func ParseProc(s string) ([2]float64, error) {
	return firstTwo(s, "/proc/loadavg")
}

func firstTwo(s, what string) ([2]float64, error) {
	var out [2]float64
	f := strings.Fields(s)
	if len(f) < 2 {
		return out, fmt.Errorf("%s: %q has no 1- and 5-minute averages", what, s)
	}
	for i := range out {
		v, err := strconv.ParseFloat(f[i], 64)
		if err != nil || v < 0 {
			return [2]float64{}, fmt.Errorf("%s: %q is not a load average", what, f[i])
		}
		out[i] = v
	}
	return out, nil
}
