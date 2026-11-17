package spend

import (
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/period"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// ContextDays is how many calendar days spend --context covers by default.
const ContextDays = 7

// TopSessions is how many sessions a box and the report list.
const TopSessions = 5

// A turn's context is the prompt one API call sent: input + cache read +
// cache write of that assistant message. Every turn re-reads it, so the
// cost of a session is its context × turns. The floor is the first turn's
// context: CLAUDE.md, the briefing, tools and skills, before any work.
// Only a session's main transcript is read (a subagent has its own,
// smaller context), and only numbers. See docs/CLI/overview.md, "spend".

// ContextSession is one session's context numbers in the window.
type ContextSession struct {
	// Box is the box its spend goes to; "" for the owner's own sessions.
	Box   string `json:"box,omitempty"`
	Issue string `json:"issue,omitempty"`
	// Agent is the name it runs under ("" in a worktree or the owner's).
	Agent string    `json:"agent,omitempty"`
	Start time.Time `json:"start"`
	// Worker is a session in an issue's worktree.
	Worker bool `json:"worker"`
	// Resumed is a session continuing an earlier one: its first turn carries
	// the old conversation, so it has no floor of its own.
	Resumed bool `json:"resumed,omitempty"`
	Turns   int  `json:"turns"`
	// Floor is the first turn's context; 0 when resumed or when the first
	// turn is before the window.
	Floor       int64 `json:"floor_tokens"`
	Median      int64 `json:"median_turn_tokens"`
	Max         int64 `json:"max_turn_tokens"`
	Compactions int   `json:"compactions"`
	// Context is the context tokens of all its turns in the window.
	Context int64 `json:"context_tokens"`

	turns []int64
	file  string
}

// ContextStats sums sessions: a box's, the owner's own, or all.
type ContextStats struct {
	Sessions int `json:"sessions"`
	// Floored are the sessions with a floor (not resumed, started in the
	// window); Floor is their median floor.
	Floored     int   `json:"floored_sessions"`
	Resumed     int   `json:"resumed_sessions"`
	Turns       int   `json:"turns"`
	Floor       int64 `json:"floor_tokens"`
	Median      int64 `json:"median_turn_tokens"`
	Max         int64 `json:"max_turn_tokens"`
	Compactions int   `json:"compactions"`
	Context     int64 `json:"context_tokens"`
	// FloorTokens is Σ floor × turns over the floored sessions, and
	// FloorShare that over their context tokens: how much of what they read
	// the floor alone accounts for.
	FloorTokens int64   `json:"floor_x_turns_tokens"`
	FloorShare  float64 `json:"floor_share"`
}

// ContextBox is one box's context numbers, rolled up like spend's.
type ContextBox struct {
	Key        string   `json:"key"`
	Name       string   `json:"name"`
	Parent     string   `json:"parent,omitempty"`
	RolledFrom []string `json:"rolled_from,omitempty"`
	ContextStats
	Top []ContextSession `json:"top,omitempty"`
}

// ContextReport is a project's context numbers over Days calendar days.
type ContextReport struct {
	Since time.Time     `json:"since"`
	Days  int           `json:"days"`
	Boxes []*ContextBox `json:"boxes"`
	// Unattributed is the owner's own sessions in the project folder.
	Unattributed ContextStats `json:"unattributed"`
	// All is every session counted, Workers those in an issue's worktree.
	All     ContextStats     `json:"all"`
	Workers ContextStats     `json:"workers"`
	Top     []ContextSession `json:"top,omitempty"`
}

// Find is the box with key or name, or nil.
func (r *ContextReport) Find(key string) *ContextBox {
	k := tracker.DepartmentKey(key)
	for _, b := range r.Boxes {
		if b.Key == k || b.Name == key {
			return b
		}
	}
	return nil
}

// LoadContext reads p's sessions with a turn in the days calendar days up
// to now (today included) and sums them per box.
func LoadContext(p *tracker.Project, issues []*tracker.Issue, now time.Time, days int, opt Options) *ContextReport {
	if days < 1 {
		days = ContextDays
	}
	since := period.DaysBack(now, days)
	rep := &ContextReport{Since: since, Days: days}
	sessions := ContextSessions(p, issues, since, now, opt)

	at := attributionOf(p, issues)
	keys, rolled := members(at.boxes)
	own := map[string][]ContextSession{}
	var owners []ContextSession
	for _, s := range sessions {
		if s.Box == "" {
			owners = append(owners, s)
			continue
		}
		own[s.Box] = append(own[s.Box], s)
	}
	for _, d := range at.boxes {
		b := &ContextBox{Key: d.key, Name: d.name, Parent: d.parent, RolledFrom: rolled[d.key]}
		var in []ContextSession
		for _, k := range keys[d.key] {
			in = append(in, own[k]...)
		}
		b.ContextStats = SumContext(in)
		b.Top = topSessions(in)
		rep.Boxes = append(rep.Boxes, b)
	}
	rep.Unattributed = SumContext(owners)
	rep.All = SumContext(sessions)
	var workers []ContextSession
	for _, s := range sessions {
		if s.Worker {
			workers = append(workers, s)
		}
	}
	rep.Workers = SumContext(workers)
	rep.Top = topSessions(sessions)
	return rep
}

// ContextSessions are p's sessions with a turn in [since, until]: workers
// in their issue's worktree, then leads, workers and the owner in the
// project folder (a session named for another project's agent is skipped,
// as spend skips it).
func ContextSessions(p *tracker.Project, issues []*tracker.Issue, since, until time.Time, opt Options) []ContextSession {
	if opt.ProjectsDir == "" {
		opt.ProjectsDir = DefaultProjectsDir()
	}
	at := attributionOf(p, issues)
	root := p.Root()
	var out []ContextSession
	for _, issue := range issues {
		wt := filepath.Join(root, ".worktrees", issue.Slug)
		for _, folder := range bench.TranscriptFolders(opt.ProjectsDir, wt) {
			for _, s := range folderContext(folder, since, until) {
				s.Box, s.Issue, s.Worker = at.boxOf(issue), issue.Slug, true
				out = append(out, s)
			}
		}
	}
	for _, folder := range bench.TranscriptFolders(opt.ProjectsDir, root) {
		for _, s := range folderContext(folder, since, until) {
			o, known := at.names[s.Agent]
			switch {
			case known:
				s.Box = o.box
				if o.issue != nil {
					s.Issue = o.issue.Slug
				}
			case s.Agent != "":
				continue
			}
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// folderContext reads the main transcript of each session in folder that
// changed since since. Files are taken oldest first turn first: a file
// whose first message an earlier file already has is a resumed session,
// and each message counts once across the folder, as spend counts it.
func folderContext(folder string, since, until time.Time) []ContextSession {
	if st, err := os.Stat(folder); err != nil || !st.IsDir() {
		return nil
	}
	type file struct {
		path string
		fr   *fileRead
	}
	var files []file
	for top := range bench.SessionFiles(folder) {
		st, err := os.Stat(top)
		if err != nil || st.ModTime().Before(since) {
			continue
		}
		fr := readFile(top, st)
		if len(fr.recs) == 0 {
			continue
		}
		files = append(files, file{top, fr})
	}
	sort.Slice(files, func(i, j int) bool {
		a, b := files[i].fr.recs[0].at, files[j].fr.recs[0].at
		if !a.Equal(b) {
			return a.Before(b)
		}
		return files[i].path < files[j].path
	})
	seen := map[string]bool{}
	var out []ContextSession
	for _, f := range files {
		s := ContextSession{Agent: f.fr.agent, file: f.path}
		first := f.fr.recs[0]
		s.Resumed = first.id != "" && seen[first.id]
		for i, r := range f.fr.recs {
			if r.id != "" && seen[r.id] {
				continue
			}
			if r.id != "" {
				seen[r.id] = true
			}
			if r.at.Before(since) || r.at.After(until) {
				continue
			}
			c := r.in + r.cacheRead + r.cacheWrite
			if s.Start.IsZero() {
				s.Start = r.at
			}
			if i == 0 && !s.Resumed {
				s.Floor = c
			}
			s.turns = append(s.turns, c)
			s.Context += c
		}
		if len(s.turns) == 0 {
			continue
		}
		s.Turns = len(s.turns)
		s.Median = median(s.turns)
		for _, c := range s.turns {
			s.Max = max(s.Max, c)
		}
		for _, at := range f.fr.compactions {
			if !at.Before(since) && !at.After(until) {
				s.Compactions++
			}
		}
		out = append(out, s)
	}
	return out
}

// SumContext sums sessions. The medians are over the floored sessions'
// floors and over every turn.
func SumContext(sessions []ContextSession) ContextStats {
	var st ContextStats
	var floors, turns []int64
	var flooredContext int64
	for _, s := range sessions {
		st.Sessions++
		st.Turns += s.Turns
		st.Compactions += s.Compactions
		st.Context += s.Context
		st.Max = max(st.Max, s.Max)
		turns = append(turns, s.turns...)
		if s.Resumed {
			st.Resumed++
		}
		if s.Floor > 0 {
			st.Floored++
			floors = append(floors, s.Floor)
			st.FloorTokens += s.Floor * int64(s.Turns)
			flooredContext += s.Context
		}
	}
	st.Floor = median(floors)
	st.Median = median(turns)
	if flooredContext > 0 {
		st.FloorShare = float64(st.FloorTokens) / float64(flooredContext)
	}
	return st
}

// topSessions are the TopSessions sessions that read the most context.
func topSessions(sessions []ContextSession) []ContextSession {
	out := append([]ContextSession(nil), sessions...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Context != out[j].Context {
			return out[i].Context > out[j].Context
		}
		return out[i].file < out[j].file
	})
	if len(out) > TopSessions {
		out = out[:TopSessions]
	}
	return out
}

// median of vs (the lower middle for an even count); 0 for none.
func median(vs []int64) int64 {
	if len(vs) == 0 {
		return 0
	}
	s := append([]int64(nil), vs...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[(len(s)-1)/2]
}
