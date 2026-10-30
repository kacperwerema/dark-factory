package autostart

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Memory is what mem-guard says right now. issue-cli never measures memory
// itself: mem-guard owns the numbers and the hard stop.
type Memory struct {
	Found    bool   // mem-guard is on PATH
	Used     int    // `mem-guard used`, percent
	CanStart bool   // `mem-guard check` passed
	Note     string // check's line when it refused
}

// ReadMemory asks mem-guard. A var so tests need no mem-guard.
var ReadMemory = readMemGuard

func readMemGuard() Memory {
	path, err := exec.LookPath("mem-guard")
	if err != nil {
		return Memory{}
	}
	m := Memory{Found: true}
	out, err := exec.Command(path, "used").Output()
	if err != nil {
		return Memory{}
	}
	m.Used, err = strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(string(out)), "%"))
	if err != nil {
		return Memory{}
	}
	check, err := exec.Command(path, "check").CombinedOutput()
	m.CanStart = err == nil
	if !m.CanStart {
		m.Note = strings.TrimSpace(string(check))
	}
	return m
}

// Candidate is one issue in the project's next order and, when it can't be
// started now, why ("worker live", "waiting on in progress").
type Candidate struct {
	Slug string
	Hold string
}

// Input is everything one verdict reads.
type Input struct {
	Project    string
	State      *State
	Memory     Memory
	Workers    int // the project's live workers, without the lead
	Last       *Start
	Candidates []Candidate
	Now        time.Time
}

// Verdict is whether a worker may start now, and which.
type Verdict struct {
	Start   bool   `json:"start"`
	Slug    string `json:"slug,omitempty"`
	Reason  string `json:"reason,omitempty"` // why it holds
	Memory  int    `json:"memory"`
	Below   int    `json:"below"`
	Workers int    `json:"workers"`
	Cap     int    `json:"cap"`
}

// Decide checks the rules in order and returns the first that holds, or the
// first ready candidate.
func Decide(in Input) Verdict {
	v := Verdict{Memory: in.Memory.Used, Workers: in.Workers}
	st := in.State
	if st == nil || !st.On {
		v.Reason = "auto-start is off"
		return v
	}
	v.Below, v.Cap = st.Below, st.MaxWorkers
	if st.Paused {
		v.Reason = "auto-start is paused"
		if st.PauseReason != "" {
			v.Reason += " (" + st.PauseReason + ")"
		}
		return v
	}
	if !in.Memory.Found {
		v.Reason = "mem-guard not found: auto-start reads memory from mem-guard"
		return v
	}
	if !in.Memory.CanStart {
		v.Reason = "mem-guard says no"
		if in.Memory.Note != "" {
			v.Reason += ": " + in.Memory.Note
		}
		return v
	}
	if in.Memory.Used >= st.Below {
		v.Reason = fmt.Sprintf("memory %d%% ≥ %d%%", in.Memory.Used, st.Below)
		return v
	}
	if in.Workers >= st.MaxWorkers {
		v.Reason = fmt.Sprintf("%d workers live (cap %d)", in.Workers, st.MaxWorkers)
		return v
	}
	if l := in.Last; l != nil && !l.At.IsZero() && in.Now.Sub(l.At) < st.Gap() {
		v.Reason = fmt.Sprintf("%s started %s %s ago (gap %dm)", l.Project, l.Slug, roundAgo(in.Now.Sub(l.At)), st.GapMinutes)
		return v
	}
	for _, c := range in.Candidates {
		if c.Hold == "" {
			v.Start, v.Slug = true, c.Slug
			return v
		}
	}
	v.Reason = "nothing ready to start"
	if len(in.Candidates) > 0 {
		v.Reason += fmt.Sprintf(" (first: %s, %s)", in.Candidates[0].Slug, in.Candidates[0].Hold)
	}
	return v
}

func roundAgo(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}
