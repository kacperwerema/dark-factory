// Package memguard is the one place that talks to mem-guard, the machine's
// memory watchdog (~/CLAUDE.md). issue-cli never measures memory itself:
// mem-guard owns the numbers and the hard stop, and this package holds its
// lines, runs it and reads its output. testdata pins the three formats.
package memguard

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/recent"
)

// mem-guard's own lines, in percent of memory used (its START_MAX, PAUSE_AT,
// RESUME_AT and HARD_LIMIT defaults). TestLimitsMatchMemGuard compares them
// with what mem-guard says.
const (
	StartMax  = 75 // check refuses at or over this
	PauseAt   = 82 // the watchdog pauses the largest heavy process
	ResumeAt  = 70 // the watchdog resumes; optional work waits for this too
	HardLimit = 85 // never exceeded
)

// Reading is what mem-guard says right now.
type Reading struct {
	Found    bool   // mem-guard is on PATH and gave a reading
	Used     int    // `mem-guard used`, percent
	CanStart bool   // `mem-guard check` passed
	Note     string // check's line when it refused
}

var lastReading recent.Reading[Reading]

// Read asks mem-guard for both numbers. No mem-guard, or one that gives no
// reading, is the zero Reading. Each number is a process: a viewer reuses the
// last answer for a moment (package recent).
func Read() Reading { return lastReading.Get(read) }

func read() Reading {
	used, err := Used()
	if err != nil {
		return Reading{}
	}
	m := Reading{Found: true, Used: used}
	found, blocked, msg := Check()
	if !found {
		return Reading{}
	}
	m.CanStart = !blocked
	if blocked {
		m.Note = msg
	}
	return m
}

// Used is `mem-guard used`: the percent of memory in use.
func Used() (int, error) {
	path, err := exec.LookPath("mem-guard")
	if err != nil {
		return 0, errors.New("mem-guard not found")
	}
	out, err := exec.Command(path, "used").Output()
	if err != nil {
		return 0, fmt.Errorf("mem-guard used: %v", err)
	}
	return ParseUsed(string(out))
}

// ParseUsed reads `mem-guard used`: "44", with or without a percent sign.
func ParseUsed(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(s), "%"))
	if err != nil {
		return 0, fmt.Errorf("mem-guard used said %q", strings.TrimSpace(s))
	}
	return n, nil
}

// Check is `mem-guard check`: found is false without mem-guard on PATH;
// blocked is a non-zero exit or a BLOCKED line; msg is what it printed.
func Check() (found, blocked bool, msg string) {
	path, err := exec.LookPath("mem-guard")
	if err != nil {
		return false, false, ""
	}
	out, err := exec.Command(path, "check").CombinedOutput()
	msg = strings.TrimSpace(string(out))
	return true, err != nil || strings.Contains(msg, "BLOCKED"), msg
}

var checkUsed = regexp.MustCompile(`used (\d+)%`)

// CheckUsed reads the percent out of a `mem-guard check` line; ok is false
// on a line without one.
func CheckUsed(line string) (used int, ok bool) {
	m := checkUsed.FindStringSubmatch(line)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

// Status is the numbers of `mem-guard status`.
type Status struct {
	Used, Start, Pause, Resume, Hard int
}

var statusLine = regexp.MustCompile(`memory used: (\d+)%\s+start<(\d+)\s+pause>=(\d+)\s+resume<=(\d+)\s+hard=(\d+)`)

// ParseStatus reads `mem-guard status`'s first line.
func ParseStatus(s string) (Status, error) {
	m := statusLine.FindStringSubmatch(s)
	if m == nil {
		return Status{}, fmt.Errorf("mem-guard status said %q", strings.TrimSpace(s))
	}
	n := make([]int, len(m))
	for i := 1; i < len(m); i++ {
		n[i], _ = strconv.Atoi(m[i])
	}
	return Status{Used: n[1], Start: n[2], Pause: n[3], Resume: n[4], Hard: n[5]}, nil
}
