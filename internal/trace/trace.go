// Package trace builds an issue's or a benchmark task's trace: one span per
// status visit (entering a status to leaving it), with the agent, the
// briefing it got, the tokens it spent, the wall time, its issue-cli calls,
// whether the gate out passed first try and whether the issue went back.
// Build reads nothing from disk: the issue side (timeline.Trace) and the
// benchmark (bench.runTask) give it what they recorded. A trace holds sizes,
// counts, times and ids only, never a briefing's or a transcript's text,
// and it never leaves the machine. docs/workflow-stats.md, "Traces".
package trace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/kacperwerema/dark-factory/internal/clilog"
	"github.com/kacperwerema/dark-factory/internal/gitfile"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Dir is the folder under an issues tree that holds the issues' traces.
const Dir = ".traces"

// Span is one status visit.
type Span struct {
	Status string    `json:"status"`
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
	// Open: the issue is still in this status; To is when the trace was
	// built.
	Open    bool    `json:"open,omitempty"`
	WallSec float64 `json:"wall_sec"`

	// Agents are the agent CLIs working in the visit (the dispatches),
	// Models the models their messages name, Sessions the session ids;
	// each distinct, in order.
	Agents   []string `json:"agents,omitempty"`
	Models   []string `json:"models,omitempty"`
	Sessions []string `json:"sessions,omitempty"`

	// Briefing is the first briefing sent in the visit, nil when none was
	// recorded; Briefings counts them all.
	Briefing  *tracker.Briefing `json:"briefing,omitempty"`
	Briefings int               `json:"briefings,omitempty"`

	Tokens Tokens `json:"tokens"`

	// CLICalls and CLIRefused count the issue-cli calls in the visit and
	// those that exited non-zero; CallsKnown is false when no clilog was
	// read.
	CLICalls   int  `json:"cli_calls"`
	CLIRefused int  `json:"cli_refused"`
	CallsKnown bool `json:"calls_known"`

	// Gate is the transition out of the status; nil on the open span.
	Gate *Gate `json:"gate,omitempty"`
	// Back: the move out went to an earlier status; EnteredBack: the move
	// in came from a later one.
	Back        bool `json:"back,omitempty"`
	EnteredBack bool `json:"entered_back,omitempty"`
}

// Tokens are a visit's tokens from the transcripts' messages timed in it.
// Known is false when no transcript covers the visit: then the numbers are
// unknown, not zero.
type Tokens struct {
	Known      bool  `json:"known"`
	In         int64 `json:"in"`
	Out        int64 `json:"out"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
}

// Total is every token of the visit.
func (t Tokens) Total() int64 { return t.In + t.Out + t.CacheRead + t.CacheWrite }

// Gate is a visit's way out: the transition to To, how many real tries it
// took (refused ones and the one that passed) and whether the first passed.
// FirstTry is nil when no clilog was read, so the tries are unknown.
type Gate struct {
	To       string `json:"to"`
	Tries    int    `json:"tries,omitempty"`
	FirstTry *bool  `json:"first_try,omitempty"`
}

// Message is one transcript message as the trace reads it: numbers only.
type Message struct {
	At                                   time.Time
	Session, Model                       string
	Input, Output, CacheRead, CacheWrite int64
}

// In is what Build reads.
type In struct {
	Transitions []tracker.TransitionStat
	Briefings   []tracker.Briefing
	Dispatches  []tracker.Dispatch
	Calls       []clilog.Record
	Messages    []Message
	// Statuses is the workflow's status order, for back moves.
	Statuses []string
	// Status is the issue's status now; Start when its life began (the
	// first span's start; zero: the first thing recorded); End when the
	// trace ends (now for an open issue). Open: the issue is still in
	// Status, so the last span is open.
	Status     string
	Start, End time.Time
	Open       bool
	// CallsKnown: a clilog was read. TokensKnown: a transcript was read.
	// Agent is the agent when no dispatch names one (a bench worker).
	CallsKnown, TokensKnown bool
	Agent                   string
}

// Build is the trace: one span per status visit, oldest first.
func Build(in In) []Span {
	start := in.Start
	if start.IsZero() {
		start = firstRecorded(in)
	}
	end := in.End
	if len(in.Transitions) > 0 && end.Before(in.Transitions[len(in.Transitions)-1].TS) {
		end = in.Transitions[len(in.Transitions)-1].TS
	}
	order := map[string]int{}
	for i, s := range in.Statuses {
		order[s] = i
	}
	back := func(from, to string) bool {
		a, okA := order[from]
		b, okB := order[to]
		return okA && okB && b < a
	}

	var spans []Span
	from, status := start, in.Status
	if len(in.Transitions) > 0 {
		status = in.Transitions[0].From
	}
	for i, t := range in.Transitions {
		s := Span{Status: status, From: from, To: t.TS}
		if s.To.Before(s.From) {
			s.From = s.To
		}
		s.Gate = &Gate{To: t.To}
		s.Back = back(t.From, t.To)
		if i > 0 {
			s.EnteredBack = back(in.Transitions[i-1].From, in.Transitions[i-1].To)
		}
		spans = append(spans, s)
		from, status = t.TS, t.To
	}
	if in.Open || len(in.Transitions) == 0 {
		s := Span{Status: status, From: from, To: end, Open: in.Open}
		if n := len(in.Transitions); n > 0 {
			s.EnteredBack = back(in.Transitions[n-1].From, in.Transitions[n-1].To)
		}
		spans = append(spans, s)
	}
	if len(spans) == 0 {
		return nil
	}

	idx := func(at time.Time) int {
		for i := len(spans) - 1; i >= 0; i-- {
			if !at.Before(spans[i].From) {
				return i
			}
		}
		return 0
	}
	for i := range spans {
		spans[i].WallSec = spans[i].To.Sub(spans[i].From).Seconds()
		spans[i].CallsKnown = in.CallsKnown
		spans[i].Tokens.Known = in.TokensKnown
	}

	// Agents: the dispatch running when the visit starts, and each in it.
	for i := range spans {
		last := i == len(spans)-1
		var agents []string
		for j, d := range in.Dispatches {
			starts := !d.At.After(spans[i].From) && (j+1 == len(in.Dispatches) || in.Dispatches[j+1].At.After(spans[i].From))
			if starts || d.At.After(spans[i].From) && (d.At.Before(spans[i].To) || last) {
				agents = addOnce(agents, agentOf(d, in.Agent))
			}
		}
		if len(agents) == 0 && in.Agent != "" {
			agents = []string{in.Agent}
		}
		spans[i].Agents = agents
		if allCodex(agents) {
			spans[i].Tokens.Known = false
		}
	}

	for _, b := range in.Briefings {
		s := &spans[idx(b.At)]
		s.Briefings++
		if s.Briefing == nil {
			bb := b
			s.Briefing = &bb
		}
	}
	for _, m := range in.Messages {
		s := &spans[idx(m.At)]
		s.Tokens.In += m.Input
		s.Tokens.Out += m.Output
		s.Tokens.CacheRead += m.CacheRead
		s.Tokens.CacheWrite += m.CacheWrite
		if m.Model != "" {
			s.Models = addOnce(s.Models, m.Model)
		}
		if m.Session != "" {
			s.Sessions = addOnce(s.Sessions, m.Session)
		}
	}
	for i := range spans {
		for _, d := range in.Dispatches {
			if d.Session != "" && !d.At.Before(spans[i].From) && (d.At.Before(spans[i].To) || i == len(spans)-1) {
				spans[i].Sessions = addOnce(spans[i].Sessions, d.Session)
			}
		}
	}

	refused := make([]int, len(spans))
	for _, r := range in.Calls {
		at := r.Start()
		if at.IsZero() {
			continue
		}
		i := idx(at)
		s := &spans[i]
		s.CLICalls++
		failed := r.Exit != nil && *r.Exit != 0
		if failed {
			s.CLIRefused++
		}
		args := clilog.StripGlobal(r.Args)
		if failed && s.Gate != nil && len(args) > 0 && args[0] == "transition" && !slices.Contains(args, "--dry-run") &&
			clilog.FlagValue(args, "--to") == s.Gate.To {
			refused[i]++
		}
	}
	if in.CallsKnown {
		for i := range spans {
			if g := spans[i].Gate; g != nil {
				g.Tries = refused[i] + 1
				first := refused[i] == 0
				g.FirstTry = &first
			}
		}
	}
	return spans
}

func agentOf(d tracker.Dispatch, fallback string) string {
	if d.Agent != "" {
		return d.Agent
	}
	if fallback != "" {
		return fallback
	}
	return "claude"
}

func allCodex(agents []string) bool {
	if len(agents) == 0 {
		return false
	}
	for _, a := range agents {
		if a != "codex" {
			return false
		}
	}
	return true
}

func addOnce(list []string, v string) []string {
	if slices.Contains(list, v) {
		return list
	}
	return append(list, v)
}

// firstRecorded is the earliest time anything in in was recorded.
func firstRecorded(in In) time.Time {
	var t time.Time
	early := func(c time.Time) {
		if !c.IsZero() && (t.IsZero() || c.Before(t)) {
			t = c
		}
	}
	for _, d := range in.Dispatches {
		early(d.At)
	}
	for _, b := range in.Briefings {
		early(b.At)
	}
	for _, r := range in.Calls {
		early(r.Start())
	}
	for _, m := range in.Messages {
		early(m.At)
	}
	if len(in.Transitions) > 0 {
		early(in.Transitions[0].TS)
	}
	if t.IsZero() {
		t = in.End
	}
	return t
}

// Path is issue slug's trace file under the issues tree issuesDir.
func Path(issuesDir, slug string) string {
	return filepath.Join(issuesDir, Dir, filepath.FromSlash(slug)+".jsonl")
}

// Write replaces the trace file at path with spans, one a line, written
// aside and renamed: a trace is derived, so it is rewritten whole.
func Write(path string, spans []Span) error {
	var buf bytes.Buffer
	for _, s := range spans {
		line, err := json.Marshal(s)
		if err != nil {
			return err
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return gitfile.WriteAtomic(path, buf.Bytes(), 0o644)
}

// Read is the trace file at path.
func Read(path string) ([]Span, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var spans []Span
	for i, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var s Span
		if err := json.Unmarshal(line, &s); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, i+1, err)
		}
		spans = append(spans, s)
	}
	return spans, nil
}
