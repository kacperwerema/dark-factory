package bench

import (
	"github.com/kacperwerema/dark-factory/internal/trace"
)

// StageSide is one run's numbers for a status, over the tasks both runs
// traced: the mean per task that visited it.
type StageSide struct {
	// Tasks is how many tasks visited the status.
	Tasks   int     `json:"tasks"`
	Tokens  float64 `json:"tokens"`
	WallSec float64 `json:"wall_sec"`
	Calls   float64 `json:"cli_calls"`
	// FirstTry is the share of gates out of the status that passed first
	// try, -1 when no gate's tries are known.
	FirstTry float64 `json:"first_try"`
	// TokensKnown is how many of Tasks had their tokens known.
	TokensKnown int `json:"tokens_known"`
}

// StageDelta is a status in both runs, A against B.
type StageDelta struct {
	Status string    `json:"status"`
	A      StageSide `json:"a"`
	B      StageSide `json:"b"`
}

// CompareByStage is the runs by stage over the tasks both have a trace for
// (matched by TaskRecord.Key), in the order the statuses first appear. Nil
// when no task is traced in both: runs before v0.63 have no trace.
func CompareByStage(a, b *Run) []StageDelta {
	traced := map[string]TaskRecord{}
	for _, t := range a.Tasks {
		if len(t.Trace) > 0 {
			traced[t.Key()] = t
		}
	}
	var as, bs [][]trace.Stage
	for _, tb := range b.Tasks {
		ta, ok := traced[tb.Key()]
		if !ok || len(tb.Trace) == 0 {
			continue
		}
		as = append(as, trace.ByStage(ta.Trace))
		bs = append(bs, trace.ByStage(tb.Trace))
	}
	if len(as) == 0 {
		return nil
	}
	var order []string
	seen := map[string]bool{}
	for _, list := range append(as, bs...) {
		for _, s := range list {
			if !seen[s.Status] {
				seen[s.Status] = true
				order = append(order, s.Status)
			}
		}
	}
	out := make([]StageDelta, 0, len(order))
	for _, status := range order {
		out = append(out, StageDelta{Status: status, A: traceStageSide(as, status), B: traceStageSide(bs, status)})
	}
	return out
}

func traceStageSide(tasks [][]trace.Stage, status string) StageSide {
	var s StageSide
	gates, first := 0, 0
	for _, list := range tasks {
		for _, st := range list {
			if st.Status != status {
				continue
			}
			s.Tasks++
			s.WallSec += st.WallSec
			s.Calls += float64(st.CLICalls)
			if st.TokensKnown > 0 {
				s.TokensKnown++
				s.Tokens += float64(st.Tokens)
			}
			gates += st.Gates
			first += st.FirstTry
		}
	}
	if s.Tasks > 0 {
		s.WallSec /= float64(s.Tasks)
		s.Calls /= float64(s.Tasks)
	}
	if s.TokensKnown > 0 {
		s.Tokens /= float64(s.TokensKnown)
	}
	s.FirstTry = -1
	if gates > 0 {
		s.FirstTry = float64(first) / float64(gates)
	}
	return s
}
