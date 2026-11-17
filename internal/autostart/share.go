package autostart

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The share: when the gap opens and more than one project is waiting, the
// starts go to the projects by their start_weight: (projects.yaml), a
// smooth weighted round robin (the nginx upstream one) over the projects
// that are waiting right now. Each lead's tick records its ask; the turn
// goes to the contender with the most credit + weight; a start moves the
// credits. A project that stops asking drops out and its credit with it,
// so nothing is saved up. The state sits beside last.json, read and written
// under the same lock (WithShare).

// Ask is a project's last evaluation as the share sees it.
type Ask struct {
	At     time.Time `json:"at"`
	Weight int       `json:"weight"`
	// Waiting: it had a ready item and nothing of its own held it (only
	// the gap, the machine's memory or load, or another project's turn).
	Waiting bool `json:"waiting"`
	// Below, MaxLoad and GapMinutes are its own lines: another project
	// counts it as a contender only while today's reading is under them.
	Below      int     `json:"below,omitempty"`
	MaxLoad    float64 `json:"max_load,omitempty"`
	GapMinutes int     `json:"gap_minutes,omitempty"`
}

// MinWeight is the least start_weight: a project counts with; unset or
// lower is this.
const MinWeight = 1

// StaleGaps is how many of its own gaps an ask stays in the turn: older,
// the project drops out and its credit with it.
const StaleGaps = 2

// stale is whether the ask is older than StaleGaps of its own gaps.
func (a Ask) stale(now time.Time) bool {
	gap := a.GapMinutes
	if gap <= 0 {
		gap = DefaultGapMinutes
	}
	return now.Sub(a.At) > StaleGaps*time.Duration(gap)*time.Minute
}

// ShareStart is one start of the last day, for the share line.
type ShareStart struct {
	Project string    `json:"project"`
	Slug    string    `json:"slug"`
	At      time.Time `json:"at"`
}

// Share is share.json in the budget's folder.
type Share struct {
	Asks   map[string]Ask `json:"asks,omitempty"`
	Credit map[string]int `json:"credit,omitempty"`
	Starts []ShareStart   `json:"starts,omitempty"`
}

// Contender is one project in the turn: its weight and credit now.
type Contender struct {
	Project string
	Weight  int
	Credit  int
}

// ShareWindow is how far back the share line counts starts.
const ShareWindow = 24 * time.Hour

// ShareLinePrefix opens the share line, naming ShareWindow: "share of the
// start gap (24h, by start_weight:): ".
func ShareLinePrefix() string {
	return fmt.Sprintf("share of the start gap (%dh, by start_weight:): ", int(ShareWindow/time.Hour))
}

func sharePath(dir string) string { return filepath.Join(dir, "share.json") }

// LoadShare reads the share; a missing file is an empty one.
func LoadShare(dir string) (*Share, error) {
	s := &Share{}
	if dir == "" {
		return s, nil
	}
	data, err := os.ReadFile(sharePath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		return &Share{}, fmt.Errorf("%s: %w", sharePath(dir), err)
	}
	return s, nil
}

// Contenders is who takes part in the turn for project self (weight
// selfWeight) now: self, and every other project whose last ask was
// waiting, is within StaleGaps of its gaps, and whose own memory and load lines
// the reading is under. Sorted by name.
func (s *Share) Contenders(self string, selfWeight int, mem Memory, cpu CPU, now time.Time) []Contender {
	out := []Contender{{Project: self, Weight: max(selfWeight, MinWeight), Credit: s.credit(self)}}
	for p, a := range s.Asks {
		if p == self || !a.Waiting || a.stale(now) || !a.couldStart(mem, cpu) {
			continue
		}
		out = append(out, Contender{Project: p, Weight: max(a.Weight, MinWeight), Credit: s.credit(p)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Project < out[j].Project })
	recentre(out)
	return out
}

// recentre shifts the credits to sum to zero, the round robin's invariant:
// credit left by a project that dropped out (or kept while a project was
// alone) would otherwise be a saved-up turn. The remainder goes to the
// first ones by name.
func recentre(cs []Contender) {
	sum := 0
	for _, c := range cs {
		sum += c.Credit
	}
	if sum == 0 || len(cs) == 0 {
		return
	}
	n := len(cs)
	q, r := sum/n, sum%n
	for i := range cs {
		cs[i].Credit -= q
		if r > 0 && i < r {
			cs[i].Credit--
		} else if r < 0 && i < -r {
			cs[i].Credit++
		}
	}
}

// couldStart is whether the reading is under the ask's own lines.
func (a Ask) couldStart(mem Memory, cpu CPU) bool {
	if a.Below > 0 && MemoryHolds(mem, a.Below) {
		return false
	}
	return LoadHold(cpu, a.MaxLoad) == ""
}

func (s *Share) credit(p string) int {
	if s.Credit == nil {
		return 0
	}
	return s.Credit[p]
}

// next is the index of the contender whose turn it is: the most credit +
// weight, ties to the higher weight, then the name.
func next(cs []Contender) int {
	best := -1
	for i, c := range cs {
		if best < 0 {
			best = i
			continue
		}
		b := cs[best]
		cv, bv := c.Credit+c.Weight, b.Credit+b.Weight
		if cv > bv || (cv == bv && (c.Weight > b.Weight || (c.Weight == b.Weight && c.Project < b.Project))) {
			best = i
		}
	}
	return best
}

// step moves the credits for a start by cs[i]: every contender gains its
// weight, the starter gives up the total, each kept within ± the total.
func step(cs []Contender, i int) {
	total := 0
	for _, c := range cs {
		total += c.Weight
	}
	for j := range cs {
		cs[j].Credit += cs[j].Weight
	}
	cs[i].Credit -= total
	for j := range cs {
		cs[j].Credit = min(max(cs[j].Credit, -total), total)
	}
}

// Turn is whose turn it is among cs and how many starts go before self:
// 0 when it is self's.
func Turn(cs []Contender, self string) (holder Contender, before int) {
	sim := append([]Contender(nil), cs...)
	holder = sim[next(sim)]
	total := 0
	for _, c := range sim {
		total += c.Weight
	}
	for before = 0; before <= 2*total; before++ {
		i := next(sim)
		if sim[i].Project == self {
			return holder, before
		}
		step(sim, i)
	}
	return holder, before
}

// TurnHold is the turn rule's words, "" when it is self's turn (or no one
// else is waiting): "atlas' turn (weight 3; dark-factory next in 2 starts)".
func TurnHold(cs []Contender, self string) string {
	if len(cs) < 2 {
		return ""
	}
	holder, before := Turn(cs, self)
	if before == 0 {
		return ""
	}
	starts := "starts"
	if before == 1 {
		starts = "start"
	}
	return fmt.Sprintf("%s turn (weight %d; %s next in %d %s)", Possessive(holder.Project), holder.Weight, self, before, starts)
}

// Possessive is "atlas'" or "dark-factory's".
func Possessive(name string) string {
	if strings.HasSuffix(name, "s") {
		return name + "'"
	}
	return name + "'s"
}

// Note records project's ask (its start_weight: weight) from verdict v. A project coming back after
// its ask went stale starts again from no credit, and stale projects lose
// theirs: an unused share isn't saved up.
func (s *Share) Note(project string, weight int, v Verdict, now time.Time) {
	if s.Asks == nil {
		s.Asks = map[string]Ask{}
	}
	if prev, ok := s.Asks[project]; !ok || prev.stale(now) {
		delete(s.Credit, project)
	}
	a := v.Ask(now)
	a.Weight = max(weight, MinWeight)
	s.Asks[project] = a
	s.forget(now)
}

// Started records a start by project: the credits move over the
// contenders the verdict was made with, and the start joins the day's list.
func (s *Share) Started(project, slug string, cs []Contender, now time.Time) {
	if len(cs) > 0 {
		cs = append([]Contender(nil), cs...)
		for i, c := range cs {
			if c.Project == project {
				step(cs, i)
				break
			}
		}
		if s.Credit == nil {
			s.Credit = map[string]int{}
		}
		for _, c := range cs {
			s.Credit[c.Project] = c.Credit
		}
	}
	s.Starts = append(s.Starts, ShareStart{Project: project, Slug: slug, At: now})
	s.forget(now)
}

// forget drops what no longer counts: credit of projects whose ask went
// stale, asks and starts older than the window.
func (s *Share) forget(now time.Time) {
	for p := range s.Credit {
		if a, ok := s.Asks[p]; !ok || a.stale(now) {
			delete(s.Credit, p)
		}
	}
	for p, a := range s.Asks {
		if now.Sub(a.At) > ShareWindow {
			delete(s.Asks, p)
		}
	}
	kept := s.Starts[:0]
	for _, st := range s.Starts {
		if now.Sub(st.At) <= ShareWindow {
			kept = append(kept, st)
		}
	}
	s.Starts = kept
}

// ShareRow is one project on the share line.
type ShareRow struct {
	Project string `json:"project"`
	Weight  int    `json:"weight"`
	Starts  int    `json:"starts"`
}

// Rows is every project that asked or started in the last day: its weight
// (self's from weight, others' from their last ask) and its starts, by
// weight then name.
func (s *Share) Rows(self string, weight int, now time.Time) []ShareRow {
	by := map[string]*ShareRow{}
	row := func(p string) *ShareRow {
		if r, ok := by[p]; ok {
			return r
		}
		r := &ShareRow{Project: p, Weight: 1}
		by[p] = r
		return r
	}
	for p, a := range s.Asks {
		if now.Sub(a.At) <= ShareWindow {
			row(p).Weight = max(a.Weight, 1)
		}
	}
	if self != "" {
		row(self).Weight = max(weight, 1)
	}
	for _, st := range s.Starts {
		if now.Sub(st.At) <= ShareWindow {
			row(st.Project).Starts++
		}
	}
	out := make([]ShareRow, 0, len(by))
	for _, r := range by {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Weight != out[j].Weight {
			return out[i].Weight > out[j].Weight
		}
		return out[i].Project < out[j].Project
	})
	return out
}

// ShareLine is "atlas ×3 6 starts · dark-factory ×1 2 starts", "" when
// only self is in it with no start (nothing to share yet).
func ShareLine(rows []ShareRow) string {
	if len(rows) == 0 || (len(rows) == 1 && rows[0].Starts == 0) {
		return ""
	}
	parts := make([]string, len(rows))
	for i, r := range rows {
		n := "starts"
		if r.Starts == 1 {
			n = "start"
		}
		parts[i] = fmt.Sprintf("%s ×%d %d %s", r.Project, r.Weight, r.Starts, n)
	}
	return strings.Join(parts, " · ")
}
