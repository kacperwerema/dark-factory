package memory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Usefulness (memory as a cache, the epic's D-2): a memory is useful at a
// status when the stages it reached pass their gate first try more often
// than stages at that status usually do. The outcome is the trace's
// (<issues>/.traces/<slug>.jsonl, one span per status visit, internal/trace);
// the injection is a use event that names its issue and status. A score
// only orders and filters: nothing here forgets or deletes a memory.
const (
	// UsefulWindow is how many of a memory's latest known injections count.
	UsefulWindow = 20
	// UsefulMin: a memory has no score until this many known injections.
	UsefulMin = 8
	// UsefulMargin: a measured memory helps when its lift is above this.
	UsefulMargin = 0.05
	// ForgetMin: an unhelpful memory measured over this many known
	// injections is proposed to the owner for forgetting.
	ForgetMin = 15
	// UnhelpfulFactor is what an unhelpful memory's strength is multiplied by.
	UnhelpfulFactor = 0.5
	// RoomMinFails: a status is scored only when this many of its visits
	// failed their gate first try; fewer is no baseline to beat.
	RoomMinFails = 3
	// useSlack: a use event may be stamped a moment before its span's start
	// (transition prints the memories as it writes the move).
	useSlack = time.Minute
)

// TraceDir is the traces' folder under the issues dir (internal/trace.Dir).
const TraceDir = ".traces"

// Outcome is one status visit of an issue, from its trace: whether the gate
// out of it passed first try (nil: not known, or the visit is still open).
type Outcome struct {
	Issue    string
	Status   string
	From     time.Time
	To       time.Time
	Open     bool
	FirstTry *bool
}

// traceSpan is the part of a trace span usefulness reads; the rest of the
// span (tokens, briefing, calls) is the trace's own.
type traceSpan struct {
	Status string    `json:"status"`
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
	Open   bool      `json:"open"`
	Gate   *struct {
		FirstTry *bool `json:"first_try"`
	} `json:"gate"`
}

// ReadOutcomes reads every trace under issueDir/.traces. A missing folder
// is no outcome; a line that does not parse is skipped (the trace is
// derived and rewritten whole, so a bad line is a reader's problem, not
// the data's).
func ReadOutcomes(issueDir string) ([]Outcome, error) {
	root := filepath.Join(issueDir, TraceDir)
	var out []Outcome
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return filepath.SkipDir
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		slug := filepath.ToSlash(strings.TrimSuffix(rel, ".jsonl"))
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, line := range bytes.Split(data, []byte("\n")) {
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			var s traceSpan
			if json.Unmarshal(line, &s) != nil || s.Status == "" {
				continue
			}
			o := Outcome{Issue: slug, Status: s.Status, From: s.From, To: s.To, Open: s.Open}
			if s.Gate != nil && !s.Open {
				o.FirstTry = s.Gate.FirstTry
			}
			out = append(out, o)
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return out, err
}

// Score is a memory's measure over its window of known injections.
type Score struct {
	// Known is how many injections with a known gate result count (at most
	// UsefulWindow), FirstTry how many of them passed first try.
	Known    int `json:"known"`
	FirstTry int `json:"first_try"`
	// Lift is the mean of (first try − the status's baseline rate) over
	// Known: above 0 the stages it reached did better than usual.
	Lift float64 `json:"lift"`
}

// Measured: enough known injections for the score to move anything.
func (s Score) Measured() bool { return s.Known >= UsefulMin }

// Helps: measured, and its lift is above the margin.
func (s Score) Helps() bool { return s.Measured() && s.Lift > UsefulMargin }

// Unhelpful: measured, and the stages it reached did no better than usual.
func (s Score) Unhelpful() bool { return s.Measured() && s.Lift <= 0 }

// Word is "helps", "no help", "neutral" or "not measured".
func (s Score) Word() string {
	switch {
	case !s.Measured():
		return "not measured"
	case s.Helps():
		return "helps"
	case s.Unhelpful():
		return "no help"
	}
	return "neutral"
}

// Usefulness is one memory's scores: overall and per status.
type Usefulness struct {
	Score
	// Injections is every injection joined to a stage, known or not.
	Injections int `json:"injections"`
	// NoRoom is how many of them were at a status with no room to lift
	// (LiftRoom): not scored.
	NoRoom int `json:"no_room,omitempty"`
	// NoRoomAt is NoRoom by status (lower-cased).
	NoRoomAt map[string]int `json:"no_room_at,omitempty"`
	// ByStatus is the score at each status it reached.
	ByStatus map[string]Score `json:"by_status,omitempty"`
}

// Mix is the status mix behind the score, so an empty one shows before
// anyone acts on it: each scored status with its known injections, the
// most first, then each status with no room to lift with its injections:
// "scored: in progress 12, shipping 5 · not scored: idea 20".
func (u Usefulness) Mix() string {
	var scored []string
	for _, st := range u.Statuses() {
		scored = append(scored, fmt.Sprintf("%s %d", st, u.ByStatus[st].Known))
	}
	out := "scored: none"
	if len(scored) > 0 {
		out = "scored: " + strings.Join(scored, ", ")
	}
	if nr := u.NoRoomMix(); nr != "" {
		out += " · not scored: " + nr
	}
	return out
}

// NoRoomMix is where the unscored injections were, the most first:
// "idea 18, testing 2" ("" when none).
func (u Usefulness) NoRoomMix() string {
	var keys []string
	for st := range u.NoRoomAt {
		keys = append(keys, st)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := u.NoRoomAt[keys[i]], u.NoRoomAt[keys[j]]
		if a != b {
			return a > b
		}
		return keys[i] < keys[j]
	})
	var out []string
	for _, st := range keys {
		out = append(out, fmt.Sprintf("%s %d", st, u.NoRoomAt[st]))
	}
	return strings.Join(out, ", ")
}

// ProposeForget: unhelpful over at least ForgetMin known injections. The
// caller leaves pinned memories out.
func (u Usefulness) ProposeForget() bool { return u.Unhelpful() && u.Known >= ForgetMin }

// Statuses is ByStatus's keys, the most known first.
func (u Usefulness) Statuses() []string {
	var out []string
	for s := range u.ByStatus {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := u.ByStatus[out[i]], u.ByStatus[out[j]]
		if a.Known != b.Known {
			return a.Known > b.Known
		}
		return out[i] < out[j]
	})
	return out
}

// Scores is usefulness by memory id. A nil Scores scores nothing.
type Scores map[string]Usefulness

// Of is id's usefulness (zero when it has none).
func (s Scores) Of(id string) Usefulness { return s[id] }

// Unhelpful reports whether memory m is measured unhelpful overall. A pinned
// memory never is: a lead or the owner chose it.
func (s Scores) Unhelpful(m Memory) bool {
	return !m.Pinned && s[m.ID].Unhelpful()
}

// UnhelpfulAt reports whether m is measured unhelpful at status.
func (s Scores) UnhelpfulAt(m Memory, status string) bool {
	if m.Pinned {
		return false
	}
	for st, sc := range s[m.ID].ByStatus {
		if strings.EqualFold(st, status) {
			return sc.Unhelpful()
		}
	}
	return false
}

// LiftAt is m's lift at status, else overall, else 0: the order of
// memories at one status.
func (s Scores) LiftAt(m Memory, status string) float64 {
	u := s[m.ID]
	for st, sc := range u.ByStatus {
		if strings.EqualFold(st, status) && sc.Measured() {
			return sc.Lift
		}
	}
	if u.Measured() {
		return u.Lift
	}
	return 0
}

// Baseline is the first-try rate of every closed visit with a known gate,
// by status (lower-cased).
func Baseline(outcomes []Outcome) map[string]float64 {
	pass, n := map[string]int{}, map[string]int{}
	for _, o := range outcomes {
		if o.FirstTry == nil {
			continue
		}
		k := strings.ToLower(o.Status)
		n[k]++
		if *o.FirstTry {
			pass[k]++
		}
	}
	out := map[string]float64{}
	for k, c := range n {
		out[k] = float64(pass[k]) / float64(c)
	}
	return out
}

// LiftRoom is, by status (lower-cased), whether a memory's lift can show
// there: its baseline is below 1 − UsefulMargin (so a memory could measure
// "helps") and at least RoomMinFails visits failed first try. At a status
// where stages nearly always pass (idea), first try − baseline is about 0
// for every memory, and scoring it would call them all "no help".
func LiftRoom(outcomes []Outcome) map[string]bool {
	fails := map[string]int{}
	for _, o := range outcomes {
		if o.FirstTry != nil && !*o.FirstTry {
			fails[strings.ToLower(o.Status)]++
		}
	}
	out := map[string]bool{}
	for k, rate := range Baseline(outcomes) {
		out[k] = rate < 1-UsefulMargin && fails[k] >= RoomMinFails
	}
	return out
}

// Injection is one memory reaching one stage: at most one per memory per
// status visit.
type Injection struct {
	ID       string
	Issue    string
	Status   string
	At       time.Time
	FirstTry *bool
}

// Injections joins use events that name an issue and status to that
// issue's visits: an event goes to the visit of its status whose start is
// the latest at or before it (with a minute's slack). Events with no
// issue or status (written before usefulness) and events no visit holds
// are left out. Oldest first.
func Injections(events []Event, outcomes []Outcome) []Injection {
	byIssue := map[string][]int{}
	for i, o := range outcomes {
		byIssue[o.Issue] = append(byIssue[o.Issue], i)
	}
	seen := map[string]bool{}
	var out []Injection
	for _, e := range events {
		if e.Op != OpUse || e.Issue == "" || e.Status == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339, e.At)
		if err != nil {
			continue
		}
		best := -1
		for _, i := range byIssue[e.Issue] {
			o := outcomes[i]
			if !strings.EqualFold(o.Status, e.Status) || o.From.After(at.Add(useSlack)) {
				continue
			}
			if !o.Open && !o.To.IsZero() && at.After(o.To) {
				continue
			}
			if best < 0 || o.From.After(outcomes[best].From) {
				best = i
			}
		}
		if best < 0 {
			continue
		}
		o := outcomes[best]
		for _, id := range e.IDs {
			key := id + "\x00" + o.Issue + "\x00" + o.Status + "\x00" + o.From.String()
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, Injection{ID: id, Issue: o.Issue, Status: o.Status, At: at, FirstTry: o.FirstTry})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// ScoreAll is every memory's usefulness from the injections and the
// baseline of all outcomes. Injections at a status with no room to lift
// (LiftRoom) count in Injections and NoRoom only: the window, the
// thresholds and ByStatus see the rest.
func ScoreAll(events []Event, outcomes []Outcome) Scores {
	base := Baseline(outcomes)
	room := LiftRoom(outcomes)
	byID := map[string][]Injection{}
	noRoom := map[string]int{}
	noRoomAt := map[string]map[string]int{}
	total := map[string]int{}
	for _, in := range Injections(events, outcomes) {
		total[in.ID]++
		if !room[strings.ToLower(in.Status)] {
			noRoom[in.ID]++
			if noRoomAt[in.ID] == nil {
				noRoomAt[in.ID] = map[string]int{}
			}
			noRoomAt[in.ID][strings.ToLower(in.Status)]++
			continue
		}
		byID[in.ID] = append(byID[in.ID], in)
	}
	out := Scores{}
	for id, n := range total {
		ins := byID[id]
		u := Usefulness{Injections: n, NoRoom: noRoom[id], NoRoomAt: noRoomAt[id], Score: window(ins, base, "")}
		statuses := map[string]string{}
		for _, in := range ins {
			statuses[strings.ToLower(in.Status)] = in.Status
		}
		for k, st := range statuses {
			if sc := window(ins, base, k); sc.Known > 0 {
				if u.ByStatus == nil {
					u.ByStatus = map[string]Score{}
				}
				u.ByStatus[st] = sc
			}
		}
		out[id] = u
	}
	return out
}

// window scores the latest UsefulWindow known injections of ins (at status
// when it is not "", lower-cased).
func window(ins []Injection, base map[string]float64, status string) Score {
	var sc Score
	sum := 0.0
	for i := len(ins) - 1; i >= 0 && sc.Known < UsefulWindow; i-- {
		in := ins[i]
		k := strings.ToLower(in.Status)
		if in.FirstTry == nil || (status != "" && k != status) {
			continue
		}
		v := 0.0
		if *in.FirstTry {
			v = 1
			sc.FirstTry++
		}
		sum += v - base[k]
		sc.Known++
	}
	if sc.Known > 0 {
		sc.Lift = sum / float64(sc.Known)
	}
	return sc
}

// Scores reads the store's events and joins them with the traces under
// issueDir. The machine store is joined with this project's traces only.
func (s Store) Scores(issueDir string) (Scores, error) {
	evs, err := s.readEvents()
	if err != nil {
		return nil, err
	}
	outs, err := ReadOutcomes(issueDir)
	if err != nil {
		return nil, err
	}
	return ScoreAll(evs, outs), nil
}

// Attach sets each memory's Usefulness from s (the ones s scores).
func (s Scores) Attach(mems []Memory) {
	for i := range mems {
		if u, ok := s[mems[i].ID]; ok {
			u := u
			mems[i].Usefulness = &u
		}
	}
}
