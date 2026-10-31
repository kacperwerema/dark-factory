// Package spend is what each box of a project (HQ, Product, a department, a
// group) spent: tokens and an estimated cost, by week, read from Claude
// Code's transcripts on this machine with internal/bench's reader. Only
// numbers are read and kept; no transcript text leaves the file, and nothing
// leaves the machine. Budgets (tracker.Budget) are shown against the spend,
// never enforced. See docs/departments.md#spend-and-budgets.
package spend

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/michal-franc/issue-viewer/internal/bench"
	"github.com/michal-franc/issue-viewer/internal/goals"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// Weeks is how many weeks a box shows, this week first.
const Weeks = 4

// TopIssues is how many issues a box lists.
const TopIssues = 5

// Box keys for HQ and Product (Settled rules: Box keys).
const (
	HQ      = goals.HQKey
	Product = goals.ProductKey
)

// Tally is tokens and their estimated cost.
type Tally struct {
	Input      int64 `json:"input_tokens"`
	Output     int64 `json:"output_tokens"`
	CacheRead  int64 `json:"cache_read_tokens"`
	CacheWrite int64 `json:"cache_write_tokens"`
	// USD is the estimated cost of the priced tokens.
	USD float64 `json:"usd_estimate"`
	// Unpriced is the tokens of models with no price: their cost is unknown.
	Unpriced int64 `json:"unpriced_tokens,omitempty"`
}

// Tokens is every token counted.
func (t Tally) Tokens() int64 { return t.Input + t.Output + t.CacheRead + t.CacheWrite }

// CostKnown reports whether every token has a price.
func (t Tally) CostKnown() bool { return t.Unpriced == 0 }

// Priced reports whether some tokens have a price.
func (t Tally) Priced() bool { return t.Unpriced < t.Tokens() }

// Empty reports whether nothing was counted.
func (t Tally) Empty() bool { return t.Tokens() == 0 }

// Add adds o to t.
func (t *Tally) Add(o Tally) {
	t.Input += o.Input
	t.Output += o.Output
	t.CacheRead += o.CacheRead
	t.CacheWrite += o.CacheWrite
	t.USD += o.USD
	t.Unpriced += o.Unpriced
}

// Week is one week's tally; Start is its Monday (YYYY-MM-DD).
type Week struct {
	Start string `json:"start"`
	Tally
}

// IssueSpend is one issue's tally over the box's weeks.
type IssueSpend struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
	Tally
}

// Box is one box's spend.
type Box struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	// Parent is the group's key for a department in a group.
	Parent string `json:"parent,omitempty"`
	// Weeks are this week first; a group's and HQ's include what rolls up.
	Weeks []Week `json:"weeks"`
	// Month is this calendar month so far, for a monthly budget.
	Month Tally `json:"month"`
	// Top are its issues with the most tokens over the weeks (rolled up for
	// a group or HQ).
	Top []IssueSpend `json:"top,omitempty"`
	// RolledFrom are the boxes whose spend is in this one's (a group's
	// departments; for HQ every other box).
	RolledFrom []string     `json:"rolled_from,omitempty"`
	Budget     *BudgetState `json:"budget,omitempty"`

	own    []Week
	ownTop map[string]*IssueSpend
	month  Tally
}

// ThisWeek is the box's tally for the current week.
func (b *Box) ThisWeek() Tally { return b.Weeks[0].Tally }

// Total is the box's tally over all its weeks.
func (b *Box) Total() Tally {
	var t Tally
	for _, w := range b.Weeks {
		t.Add(w.Tally)
	}
	return t
}

// Empty reports whether the box spent nothing in its weeks.
func (b *Box) Empty() bool { return b.Total().Empty() }

// BudgetState is a budget beside what was spent in its period.
type BudgetState struct {
	tracker.Budget
	// Spent is the period's tally: this week's or this month's.
	Spent Tally `json:"spent"`
	// Over is spent beyond the budget; shown only, never enforced.
	Over bool `json:"over"`
	// Text is "x of y" with the period ("$3.40 of $50 this week (est.)").
	Text string `json:"text"`
}

// Report is a project's spend.
type Report struct {
	// Since is the first day counted (Monday Weeks-1 weeks ago, or the
	// month's first day when earlier).
	Since time.Time `json:"since"`
	// Boxes are HQ, Product, then the departments in projects.yaml order.
	Boxes []*Box `json:"boxes"`
	// Unattributed is what sessions in the project's folder without an
	// agent name (the owner's own) spent; never in a box.
	Unattributed []Week `json:"unattributed"`
	// PricesPath is where prices are read from; Unpriced the models seen
	// without one.
	PricesPath string   `json:"prices_path"`
	Unpriced   []string `json:"unpriced_models,omitempty"`
	// Folders is how many transcript folders were read.
	Folders int `json:"folders"`
}

// Find is the box with key or name (any case), or nil.
func (r *Report) Find(key string) *Box {
	k := tracker.DepartmentKey(key)
	for _, b := range r.Boxes {
		if b.Key == k || strings.EqualFold(b.Name, key) {
			return b
		}
	}
	return nil
}

// Empty reports whether nothing at all was counted.
func (r *Report) Empty() bool {
	for _, b := range r.Boxes {
		if !b.Empty() {
			return false
		}
	}
	for _, w := range r.Unattributed {
		if !w.Empty() {
			return false
		}
	}
	return true
}

// Options are the seams: where transcripts and prices are.
type Options struct {
	// ProjectsDir is Claude Code's projects folder; "" is the default
	// ($CLAUDE_CONFIG_DIR/projects, else ~/.claude/projects).
	ProjectsDir string
	// Prices, when not nil, are used instead of LoadPrices.
	Prices map[string]bench.Price
}

// DefaultProjectsDir is where Claude Code keeps transcripts.
func DefaultProjectsDir() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "projects")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// WeekStart is the Monday 00:00 (local) of t's week.
func WeekStart(t time.Time) time.Time {
	d := (int(t.Weekday()) + 6) % 7
	y, m, day := t.Date()
	return time.Date(y, m, day-d, 0, 0, 0, 0, t.Location())
}

// Load reads p's spend for the Weeks weeks up to now.
func Load(p *tracker.Project, issues []*tracker.Issue, now time.Time, opt Options) *Report {
	if opt.ProjectsDir == "" {
		opt.ProjectsDir = DefaultProjectsDir()
	}
	prices, path := opt.Prices, PricesPath()
	if prices == nil {
		prices = LoadPrices()
	}
	monday := WeekStart(now)
	starts := make([]time.Time, Weeks)
	for i := range starts {
		starts[i] = monday.AddDate(0, 0, -7*i)
	}
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	since := starts[Weeks-1]
	if monthStart.Before(since) {
		since = monthStart
	}
	rep := &Report{Since: since, PricesPath: path, Unattributed: emptyWeeks(starts)}

	// The boxes, and who owns what.
	boxes := map[string]*Box{}
	add := func(key, name, parent string, budget *tracker.Budget) {
		b := &Box{Key: key, Name: name, Parent: parent, own: emptyWeeks(starts), ownTop: map[string]*IssueSpend{}}
		if budget != nil {
			bs := BudgetState{Budget: *budget}
			b.Budget = &bs
		}
		boxes[key] = b
		rep.Boxes = append(rep.Boxes, b)
	}
	add(HQ, "HQ", "", nil)
	if p.Company != nil {
		boxes[HQ].Budget = budgetOf(p.Company.Budget)
	}
	add(Product, tracker.ProductName, "", nil)
	if p.Product != nil {
		boxes[Product].Budget = budgetOf(p.Product.Budget)
	}
	for _, d := range p.Departments {
		parent := ""
		if g := p.GroupOf(d.Name); g != nil {
			parent = tracker.DepartmentKey(g.Name)
		}
		add(tracker.DepartmentKey(d.Name), d.Name, parent, d.Budget)
	}
	boxOf := func(issue *tracker.Issue) string {
		if d := p.DepartmentOf(issue); d != "" {
			return tracker.DepartmentKey(d)
		}
		return Product
	}
	names := map[string]owner{tracker.LeadSessionName(p): {box: HQ}}
	for _, d := range p.Departments {
		names[tracker.DepartmentLeadName(p, d.Name)] = owner{box: tracker.DepartmentKey(d.Name)}
	}
	for _, issue := range issues {
		o := owner{box: boxOf(issue), issue: issue}
		names[tracker.AgentSessionName(issue.Slug)] = o
		if short := "agent-" + filepath.Base(issue.Slug); names[short].box == "" {
			names[short] = o
		}
	}

	unpriced := map[string]bool{}
	count := func(o owner, recs []record) {
		var b *Box
		if o.box != "" {
			b = boxes[o.box]
		}
		for _, r := range recs {
			if r.at.Before(since) || r.at.After(now) {
				continue
			}
			t := r.tally(prices, unpriced)
			if b == nil {
				if i := weekIndex(starts, r.at); i >= 0 {
					rep.Unattributed[i].Add(t)
				}
				continue
			}
			if !r.at.Before(monthStart) {
				b.month.Add(t)
			}
			i := weekIndex(starts, r.at)
			if i < 0 {
				continue
			}
			b.own[i].Add(t)
			if o.issue != nil {
				is := b.ownTop[o.issue.Slug]
				if is == nil {
					is = &IssueSpend{Slug: o.issue.Slug, Title: o.issue.Title}
					b.ownTop[o.issue.Slug] = is
				}
				is.Add(t)
			}
		}
	}

	root := p.Root()
	// Workers in their issue's worktree: every session there is the issue's.
	for _, issue := range issues {
		wt := filepath.Join(root, ".worktrees", issue.Slug)
		for _, folder := range bench.TranscriptFolders(opt.ProjectsDir, wt) {
			if recs, ok := folderRecords(folder, since, false); ok {
				rep.Folders++
				for _, s := range recs {
					count(owner{box: boxOf(issue), issue: issue}, s.recs)
				}
			}
		}
	}
	// The project folder: leads, workers without a worktree, the owner.
	for _, folder := range bench.TranscriptFolders(opt.ProjectsDir, root) {
		recs, ok := folderRecords(folder, since, true)
		if !ok {
			continue
		}
		rep.Folders++
		for _, s := range recs {
			o, known := names[s.agent]
			switch {
			case known:
				count(o, s.recs)
			case s.agent == "":
				count(owner{}, s.recs)
			}
			// A session named for another project's lead or worker (projects
			// sharing a folder) is theirs: skipped.
		}
	}
	for m := range unpriced {
		rep.Unpriced = append(rep.Unpriced, m)
	}
	sort.Strings(rep.Unpriced)
	rollUp(p, rep, boxes, starts)
	return rep
}

// budgetOf is a copy of b as a BudgetState, or nil.
func budgetOf(b *tracker.Budget) *BudgetState {
	if b == nil {
		return nil
	}
	return &BudgetState{Budget: *b}
}

// owner is who a session's spend goes to: a box, and the issue when a
// worker spent it.
type owner struct {
	box   string
	issue *tracker.Issue
}

func emptyWeeks(starts []time.Time) []Week {
	out := make([]Week, len(starts))
	for i, s := range starts {
		out[i].Start = s.Format("2006-01-02")
	}
	return out
}

// weekIndex is the week at falls in (0 = this week), or -1.
func weekIndex(starts []time.Time, at time.Time) int {
	for i, s := range starts {
		if !at.Before(s) {
			return i
		}
	}
	return -1
}

// rollUp fills each box's Weeks, Month and Top: its own, plus every
// department's under it for a group, plus every box's for HQ; then its budget.
func rollUp(p *tracker.Project, rep *Report, boxes map[string]*Box, starts []time.Time) {
	members := map[string][]*Box{}
	for _, b := range rep.Boxes {
		members[b.Key] = []*Box{b}
	}
	for _, b := range rep.Boxes {
		// Every group up the chain counts b (nested groups,
		// factory/nested-groups-…#D-1); Parent is set only for a good
		// chain, and the walk is bounded anyway.
		for up, n := b.Parent, 0; up != "" && boxes[up] != nil && n <= len(rep.Boxes); n++ {
			members[up] = append(members[up], b)
			boxes[up].RolledFrom = append(boxes[up].RolledFrom, b.Key)
			up = boxes[up].Parent
		}
		if b.Key != HQ {
			members[HQ] = append(members[HQ], b)
			boxes[HQ].RolledFrom = append(boxes[HQ].RolledFrom, b.Key)
		}
	}
	for _, b := range rep.Boxes {
		b.Weeks = emptyWeeks(starts)
		top := map[string]*IssueSpend{}
		for _, m := range members[b.Key] {
			for i := range m.own {
				b.Weeks[i].Add(m.own[i].Tally)
			}
			b.Month.Add(m.month)
			for slug, is := range m.ownTop {
				if top[slug] == nil {
					cp := IssueSpend{Slug: is.Slug, Title: is.Title}
					top[slug] = &cp
				}
				top[slug].Add(is.Tally)
			}
		}
		for _, is := range top {
			b.Top = append(b.Top, *is)
		}
		sort.Slice(b.Top, func(i, j int) bool {
			if b.Top[i].Tokens() != b.Top[j].Tokens() {
				return b.Top[i].Tokens() > b.Top[j].Tokens()
			}
			return b.Top[i].Slug < b.Top[j].Slug
		})
		if len(b.Top) > TopIssues {
			b.Top = b.Top[:TopIssues]
		}
		if b.Budget != nil {
			b.Budget.fill(b)
		}
	}
}

// fill sets the budget's period tally and its "x of y".
func (s *BudgetState) fill(b *Box) {
	if s.Err != "" {
		s.Text = "unreadable (" + s.Err + ")"
		return
	}
	period := "this week"
	s.Spent = b.ThisWeek()
	if s.Period == "month" {
		period, s.Spent = "this month", b.Month
	}
	if s.Tokens > 0 {
		s.Over = s.Spent.Tokens() > s.Tokens
		s.Text = FormatTokens(s.Spent.Tokens()) + " of " + FormatTokens(s.Tokens) + " tokens " + period
		return
	}
	s.Over = s.Spent.USD > s.USD
	spent := FormatUSD(s.Spent.USD)
	if !s.Spent.CostKnown() {
		spent = "≥ " + spent
	}
	s.Text = spent + " of " + FormatUSD(s.USD) + " " + period + " (est.)"
}

// record is one counted message: numbers only.
type record struct {
	id                             string
	at                             time.Time
	model                          string
	in, out, cacheRead, cacheWrite int64
}

func (r record) tally(prices map[string]bench.Price, unpriced map[string]bool) Tally {
	t := Tally{Input: r.in, Output: r.out, CacheRead: r.cacheRead, CacheWrite: r.cacheWrite}
	if pr, ok := bench.PriceFor(r.model, prices); ok {
		t.USD = pr.USD(r.in, r.out, r.cacheRead, r.cacheWrite)
	} else {
		t.Unpriced = t.Tokens()
		if r.model != "" {
			unpriced[r.model] = true
		}
	}
	return t
}

// session is one session's counted messages and its agent name ("" when
// it has none).
type session struct {
	agent string
	recs  []record
}

// folderRecords reads a transcript folder's sessions changed since since,
// each message id once across the folder (a resumed session repeats the
// messages it carries over). named also reads each session's agent name.
// ok is false for a folder that isn't there.
func folderRecords(folder string, since time.Time, named bool) ([]session, bool) {
	if st, err := os.Stat(folder); err != nil || !st.IsDir() {
		return nil, false
	}
	seen := map[string]bool{}
	files := bench.SessionFiles(folder)
	tops := make([]string, 0, len(files))
	for top := range files {
		tops = append(tops, top)
	}
	sort.Strings(tops)
	var out []session
	for _, top := range tops {
		var s session
		fresh := false
		for _, f := range files[top] {
			st, err := os.Stat(f)
			if err != nil || st.ModTime().Before(since) {
				continue
			}
			fresh = true
			fr := readFile(f, st)
			for _, r := range fr.recs {
				if r.id != "" && seen[r.id] {
					continue
				}
				seen[r.id] = true
				s.recs = append(s.recs, r)
			}
		}
		if !fresh {
			continue
		}
		if named {
			if st, err := os.Stat(top); err == nil {
				s.agent = readFile(top, st).agent
			}
		}
		out = append(out, s)
	}
	return out, true
}

// fileRead is what one transcript file gives, cached until the file's
// size or modification time changes.
type fileRead struct {
	size  int64
	mod   time.Time
	recs  []record
	agent string
}

var (
	cacheMu sync.Mutex
	cache   = map[string]*fileRead{}
)

func readFile(path string, st os.FileInfo) *fileRead {
	cacheMu.Lock()
	if c := cache[path]; c != nil && c.size == st.Size() && c.mod.Equal(st.ModTime()) {
		cacheMu.Unlock()
		return c
	}
	cacheMu.Unlock()
	fr := &fileRead{size: st.Size(), mod: st.ModTime()}
	// Each message id once within the file; the folder dedups across files.
	bench.EachMessage(path, nil, func(m bench.Message) {
		if !m.Counted || m.Timestamp.IsZero() {
			return
		}
		fr.recs = append(fr.recs, record{id: m.ID, at: m.Timestamp, model: m.Model,
			in: m.Input, out: m.Output, cacheRead: m.CacheRead, cacheWrite: m.CacheWrite})
	})
	fr.agent = agentName(path)
	cacheMu.Lock()
	cache[path] = fr
	cacheMu.Unlock()
	return fr
}

// nameRecordRe is a session's name record: agent-name (a launched lead or
// worker) or custom-title (/rename); the last one wins. Only whole records
// match, never a message quoting one.
var nameRecordRe = regexp.MustCompile(`^\{\s*"type"\s*:\s*"(?:agent-name|custom-title)"`)

// nameRe is the name in such a record.
var nameRe = regexp.MustCompile(`"(?:agentName|customTitle)"\s*:\s*"([^"\\]+)"`)

// agentName is the last name record in the file, or "".
func agentName(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	name := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) > 4096 || !bytes.Contains(line, []byte(`"type"`)) || !nameRecordRe.Match(line) {
			continue
		}
		if m := nameRe.FindSubmatch(line); m != nil {
			name = string(m[1])
		}
	}
	return name
}

// ResetCache drops the cached reads (tests).
func ResetCache() {
	cacheMu.Lock()
	cache = map[string]*fileRead{}
	cacheMu.Unlock()
}
