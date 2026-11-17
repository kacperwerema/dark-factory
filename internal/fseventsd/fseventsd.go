// Package fseventsd watches macOS's file-event daemon. Its cost grows with
// the paths its clients have registered (every Claude Code and Codex
// session watches its worktree), not with the event rate: after 16 days of
// workers it sat at ~100% CPU and a 13 GB physical footprint, which
// mem-guard counted as used memory, so worker starts were held. Restarting
// it fixes that at once, and needs root: the factory only reads it (ps for
// CPU, top's MEM column for the physical footprint, no root), says when it
// runs away and names the owner's command. Nothing here runs that command.
//
// The viewer records a reading every 10 minutes (Record) into a small state
// file; pages and status.md only read that file (Load, Check).
package fseventsd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/jsonfile"
	"github.com/kacperwerema/dark-factory/internal/viewerstate"
)

// Command is the owner's fix: launchd starts the daemon again at once.
const Command = "sudo killall fseventsd"

// The lines: a footprint over FootprintLimitMB on the newest reading, or CPU
// over CPULimit on two readings of the same daemon PairMin to PairMax apart.
const (
	FootprintLimitMB = 2048
	CPULimit         = 50.0
	PairMin          = 9 * time.Minute
	PairMax          = 30 * time.Minute
)

// Keep is how many readings the state file holds.
const Keep = 6

// Reading is one look at the daemon.
type Reading struct {
	At          time.Time `json:"at"`
	PID         int       `json:"pid"`
	CPU         float64   `json:"cpu"`
	FootprintMB float64   `json:"footprint_mb"`
	// MemMB is the machine's physical memory, for the share.
	MemMB float64 `json:"mem_mb,omitempty"`
}

// ErrNone: no fseventsd on this machine (not macOS), or the machine
// readings are off (viewerstate.MachineOffEnv).
var ErrNone = errors.New("no fseventsd running")

// run is how the package starts ps, top and sysctl: a var for tests.
var run = func(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

// Read takes a reading now: pgrep for the pid, ps for CPU, top for the
// physical footprint (ps's RSS when top gives none), sysctl for the memory
// size. (BSD ps -o takes one field per flag: "pid=,pcpu=" would be one
// header.)
func Read(now time.Time) (Reading, error) {
	if viewerstate.MachineOff(os.Getenv) {
		// A playtest scenario: the host's fseventsd is not the board's.
		return Reading{}, ErrNone
	}
	pids, err := run("pgrep", "-x", "fseventsd")
	pid, perr := strconv.Atoi(strings.TrimSpace(strings.SplitN(strings.TrimSpace(pids), "\n", 2)[0]))
	if err != nil || perr != nil {
		return Reading{}, ErrNone
	}
	out, err := run("ps", "-o", "pid=", "-o", "pcpu=", "-o", "rss=", "-o", "ucomm=", "-p", strconv.Itoa(pid))
	if err != nil {
		return Reading{}, fmt.Errorf("ps: %w", err)
	}
	r, ok := ParsePS(out)
	if !ok {
		return Reading{}, ErrNone
	}
	r.At = now
	if top, err := run("top", "-l", "1", "-stats", "pid,mem", "-pid", strconv.Itoa(r.PID)); err == nil {
		if mb, ok := ParseTopMem(top, r.PID); ok {
			r.FootprintMB = mb
		}
	}
	if mem, err := run("sysctl", "-n", "hw.memsize"); err == nil {
		if b, err := strconv.ParseFloat(strings.TrimSpace(mem), 64); err == nil && b > 0 {
			r.MemMB = b / (1 << 20)
		}
	}
	return r, nil
}

// ParsePS finds fseventsd in `ps -o pid= -o pcpu= -o rss= -o ucomm=`; its footprint
// is the RSS until top gives the real one.
func ParsePS(out string) (Reading, bool) {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[len(f)-1] != "fseventsd" {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		cpu, err2 := strconv.ParseFloat(f[1], 64)
		rss, err3 := strconv.ParseFloat(f[2], 64)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		return Reading{PID: pid, CPU: cpu, FootprintMB: rss / 1024}, true
	}
	return Reading{}, false
}

// ParseTopMem reads the MEM column of `top -l 1 -stats pid,mem -pid <pid>`
// ("13G", "3408K", "512M", "1024B", with an optional + or -) in MB.
func ParseTopMem(out string, pid int) (float64, bool) {
	want := strconv.Itoa(pid)
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || f[0] != want {
			continue
		}
		return parseSize(f[1])
	}
	return 0, false
}

func parseSize(s string) (float64, bool) {
	s = strings.TrimRight(s, "+-")
	if s == "" {
		return 0, false
	}
	mult := 1.0 / (1 << 20)
	switch s[len(s)-1] {
	case 'B':
		s = s[:len(s)-1]
	case 'K':
		mult, s = 1.0/1024, s[:len(s)-1]
	case 'M':
		mult, s = 1, s[:len(s)-1]
	case 'G':
		mult, s = 1024, s[:len(s)-1]
	case 'T':
		mult, s = 1024*1024, s[:len(s)-1]
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v * mult, true
}

// Verdict is what the readings say.
type Verdict struct {
	Over bool
	// Line is the daemon's state in words, e.g. "fseventsd (the macOS
	// file-event daemon) uses 13.2 GB, 34% of memory, at 99% CPU"; Why names
	// the line it crossed.
	Line, Why string
	Latest    Reading
}

// Check reads the verdict from readings, oldest first: the footprint on the
// newest reading, CPU on the newest two of one pid PairMin to PairMax apart.
// A reading older than PairMax before now counts for nothing.
func Check(readings []Reading, now time.Time) Verdict {
	if len(readings) == 0 {
		return Verdict{}
	}
	last := readings[len(readings)-1]
	if now.Sub(last.At) > PairMax {
		return Verdict{}
	}
	v := Verdict{Latest: last, Line: Words(last)}
	switch {
	case last.FootprintMB > FootprintLimitMB:
		v.Over, v.Why = true, fmt.Sprintf("its footprint is over %s", size(FootprintLimitMB))
	default:
		for i := len(readings) - 2; i >= 0; i-- {
			prev := readings[i]
			gap := last.At.Sub(prev.At)
			if prev.PID != last.PID || gap > PairMax {
				break
			}
			if gap < PairMin {
				continue
			}
			if prev.CPU > CPULimit && last.CPU > CPULimit {
				v.Over, v.Why = true, fmt.Sprintf("its CPU stayed over %.0f%% for %d minutes", CPULimit, int(gap.Minutes()+0.5))
			}
			break
		}
	}
	return v
}

// Words is the reading in the owner's words.
func Words(r Reading) string {
	share := ""
	if r.MemMB > 0 {
		share = fmt.Sprintf(", %.0f%% of memory", r.FootprintMB/r.MemMB*100)
	}
	return fmt.Sprintf("fseventsd (the macOS file-event daemon) uses %s%s, at %.0f%% CPU", size(r.FootprintMB), share, r.CPU)
}

// Advice is the sentence that tells the owner what to do.
func Advice() string {
	return "Restart it in a terminal: " + Command + " (launchd starts it again at once; nothing here runs it for you)."
}

func size(mb float64) string {
	if mb >= 1024 {
		return fmt.Sprintf("%.1f GB", mb/1024)
	}
	return fmt.Sprintf("%.0f MB", mb)
}

// Path is the readings file under the state dir, "" without one.
func Path() string {
	d := viewerstate.Dir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "machine", "fseventsd.json")
}

type file struct {
	Readings []Reading `json:"readings"`
}

// Load reads the readings, oldest first; a missing or bad file is none.
func Load(path string) []Reading {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var f file
	if json.Unmarshal(data, &f) != nil {
		return nil
	}
	return f.Readings
}

// Append adds r to the file at path and keeps the last Keep readings.
func Append(path string, r Reading) error {
	if path == "" {
		return errors.New("no state dir")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	rs := append(Load(path), r)
	if len(rs) > Keep {
		rs = rs[len(rs)-Keep:]
	}
	return jsonfile.Write(path, file{Readings: rs}, 0o644)
}

// Record takes a reading and appends it to Path. ErrNone when there is no
// fseventsd (nothing is written).
func Record(now time.Time) (Reading, error) {
	r, err := Read(now)
	if err != nil {
		return r, err
	}
	return r, Append(Path(), r)
}

// Current is the verdict from the readings file: what pages show.
func Current(now time.Time) Verdict {
	return Check(Load(Path()), now)
}
