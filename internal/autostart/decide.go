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
	// Share is the machine's turn state (nil: no turn, first come first
	// served); Weight the project's start_weight: (0 counts as 1).
	Share  *Share
	Weight int
}

// Verdict is whether a worker may start now, and which.
type Verdict struct {
	Start  bool   `json:"start"`
	Slug   string `json:"slug,omitempty"`
	Reason string `json:"reason,omitempty"` // why it holds
	// Kind is which rule holds it: code reads this, never Reason's words.
	Kind    HoldKind `json:"hold_kind,omitempty"`
	Memory  int      `json:"memory"`
	Below   int      `json:"below"`
	Workers int      `json:"workers"`
	Cap     int      `json:"cap"`
	// Load and Load5 are the 1- and 5-minute load averages (LoadFound
	// false: no reading), Cores the machine's cores, MaxLoad the limit in
	// use.
	LoadFound bool    `json:"load_found"`
	Load      float64 `json:"load"`
	Load5     float64 `json:"load5"`
	Cores     int     `json:"cores"`
	MaxLoad   float64 `json:"max_load"`
	// Weight is the project's start_weight:; Waiting whether it had a ready
	// item held only by the machine, the gap or another project's turn
	// (what its ask records); Contenders who the turn was taken among.
	Weight     int         `json:"weight"`
	Waiting    bool        `json:"waiting"`
	Contenders []Contender `json:"-"`
	// Clears is true when the hold clears by itself (the machine's memory
	// or load, the worker cap, the gap, another project's turn): nobody
	// has to act for the next start. The lead's watch stays quiet on it.
	Clears bool `json:"clears,omitempty"`
	gap    int
}

// Ask is what the share records of this verdict.
func (v Verdict) Ask(now time.Time) Ask {
	return Ask{At: now, Weight: max(v.Weight, 1), Waiting: v.Waiting, Below: v.Below, MaxLoad: v.MaxLoad, GapMinutes: v.gap}
}

// CPU is the load reading the verdict was taken on.
func (v Verdict) CPU() CPU {
	return CPU{Found: v.LoadFound, Avg1: v.Load, Avg5: v.Load5, Cores: v.Cores}
}

// LoadPart is the load clause of a start line, the hold line's form: "load
// 4.3/5.0 on 14 cores (max 14)", or "load unknown". A start may go with
// one average over the max (the hold needs both), so it names the max
// instead of claiming "<".
func (v Verdict) LoadPart() string { return LoadLine(v.CPU(), v.MaxLoad) }

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
// check refusing holds whatever the number), and the load must not have
// both its 1- and 5-minute averages at or over maxLoad (0: the core count). No load reading (an OS without one) doesn't
// hold: mem-guard stays the hard stop (D-3).
func MachineHold(mem Memory, cpu CPU, below int, maxLoad float64) string {
	return MachineHoldOf(mem, cpu, below, maxLoad).Text
}

// LoadHold is the load half of MachineHold, for a start with no memory line
// of its own (the critic): both the 1- and the 5-minute load average are
// at or over maxLoad (0: the core count). Avg1 alone spikes over the cores
// for a minute or two on one make validate; Avg5 says the machine stays
// busy.
func LoadHold(cpu CPU, maxLoad float64) string {
	limit := maxLoad
	if limit <= 0 {
		limit = float64(cpu.Cores)
	}
	if cpu.Over(limit) {
		return LoadLine(cpu, limit)
	}
	return ""
}

// Over is whether the load holds at limit: both the 1- and the 5-minute
// average at or over it.
func (l CPU) Over(limit float64) bool {
	return l.Found && limit > 0 && l.Avg1 >= limit && l.Avg5 >= limit
}

// holdRule is one reason Decide holds, in order. words are its clauses in
// the lead brief's sentence (HoldClauses), so a new rule can't be left out
// of it; nil when the brief says it another way (paused).
type holdRule struct {
	words func(Limits) []string
	hold  func(Input, Verdict) Hold
	// own: the project's own limit holds it, so it isn't waiting for the
	// shared gap (the share leaves it out of the turn).
	own bool
	// clears: the hold goes away by itself (Verdict.Clears); nil never.
	clears func(Input) bool
}

func always(Input) bool { return true }

var holdRules = []holdRule{
	{
		own: true,
		hold: func(in Input, _ Verdict) Hold {
			if !in.State.Paused {
				return Hold{}
			}
			if in.State.PauseReason != "" {
				return Hold{HoldPaused, "auto-start is paused (" + in.State.PauseReason + ")"}
			}
			return Hold{HoldPaused, "auto-start is paused"}
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
			return []string{"memory is at or above " + memory, "the 1- and 5-minute load are both at or above " + load}
		},
		hold: func(in Input, v Verdict) Hold { return MachineHoldOf(in.Memory, in.CPU, in.State.Below, v.MaxLoad) },
		// Memory and load come down by themselves; a missing mem-guard
		// needs someone to install it.
		clears: func(in Input) bool { return in.Memory.Found },
	},
	{
		words: func(l Limits) []string {
			if l.MaxWorkers > 0 {
				return []string{fmt.Sprintf("%d workers are live", l.MaxWorkers)}
			}
			return []string{"the worker cap is reached"}
		},
		own:    true,
		clears: always,
		hold: func(in Input, _ Verdict) Hold {
			if in.Workers >= in.State.MaxWorkers {
				return Hold{HoldWorkers, fmt.Sprintf("%d workers live (cap %d)", in.Workers, in.State.MaxWorkers)}
			}
			return Hold{}
		},
	},
	{
		words:  func(Limits) []string { return []string{"another project just started one"} },
		clears: always,
		hold: func(in Input, _ Verdict) Hold {
			if l := in.Last; l != nil && !l.At.IsZero() && in.Now.Sub(l.At) < in.State.Gap() {
				return Hold{HoldGap, fmt.Sprintf("%s started %s %s ago (gap %dm)", l.Project, l.Slug, roundAgo(in.Now.Sub(l.At)), in.State.GapMinutes)}
			}
			return Hold{}
		},
	},
	{
		words:  func(Limits) []string { return []string{"it is another project's turn"} },
		clears: always,
		hold: func(in Input, v Verdict) Hold {
			if in.Share == nil || firstReady(in.Candidates) == "" {
				return Hold{}
			}
			if why := TurnHold(v.Contenders, in.Project); why != "" {
				return Hold{HoldTurn, why}
			}
			return Hold{}
		},
	},
}

func firstReady(cands []Candidate) string {
	for _, c := range cands {
		if c.Hold == "" {
			return c.Slug
		}
	}
	return ""
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
		LoadFound: in.CPU.Found, Load: in.CPU.Avg1, Load5: in.CPU.Avg5, Cores: in.CPU.Cores}
	st := in.State
	if st == nil || !st.On {
		v.Reason, v.Kind = "auto-start is off", HoldOff
		return v
	}
	v.Below, v.Cap = st.Below, st.MaxWorkers
	v.MaxLoad = st.LoadLimit(in.CPU.Cores)
	v.Weight, v.gap = max(in.Weight, 1), st.GapMinutes
	if in.Share != nil {
		v.Contenders = in.Share.Contenders(in.Project, v.Weight, in.Memory, in.CPU, in.Now)
	}
	ready := firstReady(in.Candidates)
	for _, r := range holdRules {
		if h := r.hold(in, v); h.Text != "" {
			v.Reason, v.Kind = h.Text, h.Kind
			v.Waiting = ready != "" && !r.own
			v.Clears = r.clears != nil && r.clears(in)
			return v
		}
	}
	if ready != "" {
		v.Start, v.Slug, v.Waiting = true, ready, true
		return v
	}
	v.Reason, v.Kind = "nothing ready to start", HoldNothingReady
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
