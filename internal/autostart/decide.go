package autostart

import (
	"fmt"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/memguard"
)

// Memory is what mem-guard says right now: internal/memguard runs it.
type Memory = memguard.Reading

// ReadMemory asks mem-guard. A var so tests need no mem-guard.
var ReadMemory = memguard.Read

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
	CPU        CPU
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
	// Load is the 1-minute load average (LoadFound false: no reading),
	// Cores the machine's cores, MaxLoad the limit in use.
	LoadFound bool    `json:"load_found"`
	Load      float64 `json:"load"`
	Cores     int     `json:"cores"`
	MaxLoad   float64 `json:"max_load"`
}

// LoadPart is the load clause of a start line: "load 4.3 < 14 on 14 cores",
// or "load unknown".
func (v Verdict) LoadPart() string {
	if !v.LoadFound {
		return "load unknown"
	}
	return fmt.Sprintf("load %s < %s on %d cores", FormatLoad(v.Load), FormatLimit(v.MaxLoad), v.Cores)
}

// Limits are the numbers the hold rules name; zero where a sentence has no
// number to give (the queue's lead line: its limits are per queue).
type Limits struct {
	Below      int
	MaxWorkers int
	MaxLoad    float64 // 0: the core count
}

// MachineHold is why the machine holds a start now, "" when it may start:
// the one rule auto-start, the queue, jobs and the nightly run all ask.
// Memory must be under below (mem-guard's reading, which must be there;
// check refusing holds whatever the number), and the 1-minute load under
// maxLoad (0: the core count). No load reading (an OS without one) doesn't
// hold: mem-guard stays the hard stop (D-3).
func MachineHold(mem Memory, cpu CPU, below int, maxLoad float64) string {
	switch {
	case !mem.Found:
		return "mem-guard not found: memory is read from mem-guard"
	case !mem.CanStart:
		if mem.Note != "" {
			return "mem-guard says no: " + mem.Note
		}
		return "mem-guard says no"
	case mem.Used >= below:
		return fmt.Sprintf("memory %d%% ≥ %d%%", mem.Used, below)
	}
	return LoadHold(cpu, maxLoad)
}

// LoadHold is the load half of MachineHold, for a start with no memory line
// of its own (the critic): the load is at or over maxLoad (0: the core
// count).
func LoadHold(cpu CPU, maxLoad float64) string {
	limit := maxLoad
	if limit <= 0 {
		limit = float64(cpu.Cores)
	}
	if cpu.Found && limit > 0 && cpu.Avg1 >= limit {
		return LoadLine(cpu, limit)
	}
	return ""
}

// holdRule is one reason Decide holds, in order. words are its clauses in
// the lead brief's sentence (HoldClauses), so a new rule can't be left out
// of it; nil when the brief says it another way (paused).
type holdRule struct {
	words func(Limits) []string
	hold  func(Input, Verdict) string
}

var holdRules = []holdRule{
	{
		hold: func(in Input, _ Verdict) string {
			if !in.State.Paused {
				return ""
			}
			if in.State.PauseReason != "" {
				return "auto-start is paused (" + in.State.PauseReason + ")"
			}
			return "auto-start is paused"
		},
	},
	{
		words: func(l Limits) []string {
			memory, load := "its limit", "the core count"
			if l.Below > 0 {
				memory = fmt.Sprintf("%d%%", l.Below)
			}
			if l.MaxLoad > 0 {
				load = FormatLimit(l.MaxLoad)
			}
			return []string{"memory is at or above " + memory, "the 1-minute load is at or above " + load}
		},
		hold: func(in Input, v Verdict) string { return MachineHold(in.Memory, in.CPU, in.State.Below, v.MaxLoad) },
	},
	{
		words: func(l Limits) []string {
			if l.MaxWorkers > 0 {
				return []string{fmt.Sprintf("%d workers are live", l.MaxWorkers)}
			}
			return []string{"the worker cap is reached"}
		},
		hold: func(in Input, _ Verdict) string {
			if in.Workers >= in.State.MaxWorkers {
				return fmt.Sprintf("%d workers live (cap %d)", in.Workers, in.State.MaxWorkers)
			}
			return ""
		},
	},
	{
		words: func(Limits) []string { return []string{"another project just started one"} },
		hold: func(in Input, _ Verdict) string {
			if l := in.Last; l != nil && !l.At.IsZero() && in.Now.Sub(l.At) < in.State.Gap() {
				return fmt.Sprintf("%s started %s %s ago (gap %dm)", l.Project, l.Slug, roundAgo(in.Now.Sub(l.At)), in.State.GapMinutes)
			}
			return ""
		},
	},
}

// HoldClauses is every hold of Decide in words, in order, for the lead
// brief.
func HoldClauses(l Limits) []string {
	var out []string
	for _, r := range holdRules {
		if r.words != nil {
			out = append(out, r.words(l)...)
		}
	}
	return out
}

// CIRedClause is the queue's one hold on top of Decide's (agentrun.QueueCIHold).
const CIRedClause = "local CI is red on main"

// HoldSentence joins clauses into the brief's "a, b, or c".
func HoldSentence(clauses []string) string {
	n := len(clauses)
	if n < 2 {
		return strings.Join(clauses, "")
	}
	return strings.Join(clauses[:n-1], ", ") + ", or " + clauses[n-1]
}

// Decide checks the rules in order and returns the first that holds, or the
// first ready candidate.
func Decide(in Input) Verdict {
	v := Verdict{Memory: in.Memory.Used, Workers: in.Workers,
		LoadFound: in.CPU.Found, Load: in.CPU.Avg1, Cores: in.CPU.Cores}
	st := in.State
	if st == nil || !st.On {
		v.Reason = "auto-start is off"
		return v
	}
	v.Below, v.Cap = st.Below, st.MaxWorkers
	v.MaxLoad = st.LoadLimit(in.CPU.Cores)
	for _, r := range holdRules {
		if why := r.hold(in, v); why != "" {
			v.Reason = why
			return v
		}
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

// MemGuardAllows asks mem-guard whether a heavy run (local CI's nightly
// tier) may start now: no when `mem-guard check` refuses, with its line. A
// machine without mem-guard is allowed: the guard is this machine's, not
// the project's.
func MemGuardAllows() (bool, string) {
	m := ReadMemory()
	if m.Found && !m.CanStart {
		why := "mem-guard says no"
		if m.Note != "" {
			why += ": " + m.Note
		}
		return false, why
	}
	return true, ""
}
