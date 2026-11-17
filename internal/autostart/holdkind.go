package autostart

import "fmt"

// HoldKind is which rule holds a start: what a caller reads to act on a
// hold (the lead tick frees sessions on a memory one), never the hold's
// text, which is free to change.
type HoldKind string

const (
	HoldNone            HoldKind = ""
	HoldOff             HoldKind = "off"               // auto-start is off
	HoldPaused          HoldKind = "paused"            // auto-start is paused
	HoldMemGuardMissing HoldKind = "mem-guard-missing" // no mem-guard to read memory from
	HoldMemGuard        HoldKind = "mem-guard"         // `mem-guard check` refuses
	HoldMemory          HoldKind = "memory"            // memory at or over the project's line
	HoldLoad            HoldKind = "load"              // 1- and 5-minute load over the limit
	HoldWorkers         HoldKind = "workers"           // the worker cap is reached
	HoldGap             HoldKind = "gap"               // another project just started one
	HoldTurn            HoldKind = "turn"              // another project's turn
	HoldNothingReady    HoldKind = "nothing-ready"     // no candidate is ready
)

// Memory is whether the machine's memory holds it: mem-guard refusing, or
// the reading at or over the line. Closing idle sessions can clear it; a
// missing mem-guard isn't one (nothing a closed session fixes).
func (k HoldKind) Memory() bool { return k == HoldMemGuard || k == HoldMemory }

// Hold is a held start: its kind, for code, and its text, for people.
type Hold struct {
	Kind HoldKind
	Text string
}

// MemoryHolds is the one memory-line rule: memory holds a start below
// below unless mem-guard is there, its check passes and the reading is
// under the line. MachineHold, the share's turn and jobs all ask it
// (TestOneMemoryLineRule).
func MemoryHolds(mem Memory, below int) bool {
	return !mem.Found || !mem.CanStart || mem.Used >= below
}

// MachineHoldOf is MachineHold with its kind.
func MachineHoldOf(mem Memory, cpu CPU, below int, maxLoad float64) Hold {
	if MemoryHolds(mem, below) {
		switch {
		case !mem.Found:
			return Hold{HoldMemGuardMissing, "mem-guard not found: memory is read from mem-guard"}
		case !mem.CanStart:
			if mem.Note != "" {
				return Hold{HoldMemGuard, "mem-guard says no: " + mem.Note}
			}
			return Hold{HoldMemGuard, "mem-guard says no"}
		default:
			return Hold{HoldMemory, fmt.Sprintf("memory %d%% ≥ %d%%", mem.Used, below)}
		}
	}
	if why := LoadHold(cpu, maxLoad); why != "" {
		return Hold{HoldLoad, why}
	}
	return Hold{}
}
