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
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/goals"
	"github.com/kacperwerema/dark-factory/internal/period"
	"github.com/kacperwerema/dark-factory/internal/tokens"
	"github.com/kacperwerema/dark-factory/internal/tracker"
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
	// Subscriptions split the spend by agent CLI: claude (every box and
	// the owner's own sessions) and codex (Codex's session logs in the
	// project).
	Subscriptions []Subscription `json:"subscriptions"`
}

// IssueTokens is the tokens each issue's sessions spent since r.Since, by
// slug: every issue some transcript was counted for, not only each box's
// top ones. An issue with no transcript read is missing, never 0.
func (r *Report) IssueTokens() map[string]int64 {
	out := map[string]int64{}
	for _, b := range r.Boxes {
		for slug, is := range b.ownTop {
			out[slug] += is.Tokens()
		}
	}
	return out
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
	// CodexDir is Codex's sessions folder; "" is DefaultCodexSessionsDir.
	CodexDir string
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

// Load reads p's spend for the Weeks weeks up to now.
func Load(p *tracker.Project, issues []*tracker.Issue, now time.Time, opt Options) *Report {
	if opt.ProjectsDir == "" {
		opt.ProjectsDir = DefaultProjectsDir()
	}
	prices, path := opt.Prices, PricesPath()
	if prices == nil {
		prices = LoadPrices()
	}
	monday := period.WeekStart(now)
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
	at := attributionOf(p, issues)
	for _, d := range at.boxes {
		b := &Box{Key: d.key, Name: d.name, Parent: d.parent, own: emptyWeeks(starts), ownTop: map[string]*IssueSpend{}}
		if d.budget != nil {
			bs := BudgetState{Budget: *d.budget}
			b.Budget = &bs
		}
		boxes[d.key] = b
		rep.Boxes = append(rep.Boxes, b)
	}
	boxOf := at.boxOf
	names := at.names

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
	claude := Subscription{Agent: "claude", Weeks: emptyWeeks(starts), Read: rep.Folders > 0}
	for _, b := range boxes {
		for i := range b.own {
			claude.Weeks[i].Add(b.own[i].Tally)
		}
	}
	for i := range rep.Unattributed {
		claude.Weeks[i].Add(rep.Unattributed[i].Tally)
	}
	if opt.CodexDir == "" {
		opt.CodexDir = DefaultCodexSessionsDir()
	}
	codex := Subscription{Agent: "codex"}
	codex.Weeks, codex.Read = codexWeeks(opt.CodexDir, root, starts, since, now)
	rep.Subscriptions = []Subscription{claude, codex}
	rollUp(rep, boxes, starts)
	return rep
}

// owner is who a session's spend goes to: a box, and the issue when a
// worker spent it.
type owner struct {
	box   string
	issue *tracker.Issue
	// lead marks the CEO's or a department or group lead's session name.
	lead bool
}

// boxDef is one box of a project: key, name, group and budget.
type boxDef struct {
	key, name, parent string
	budget            *tracker.Budget
}

// attribution is a project's boxes (HQ, Product, the departments in
// projects.yaml order), the box each issue's spend goes to, and the owner
// of each agent name a session in the project folder can run under. spend
// and spend --context both read it, so they never disagree.
type attribution struct {
	boxes []boxDef
	boxOf func(*tracker.Issue) string
	names map[string]owner
}

func attributionOf(p *tracker.Project, issues []*tracker.Issue) attribution {
	var at attribution
	var hq, prod *tracker.Budget
	if p.Company != nil {
		hq = p.Company.Budget
	}
	if p.Product != nil {
		prod = p.Product.Budget
	}
	at.boxes = append(at.boxes, boxDef{key: HQ, name: "HQ", budget: hq}, boxDef{key: Product, name: tracker.ProductName, budget: prod})
	for _, d := range p.Departments {
		parent := ""
		if g := p.GroupOf(d.Name); g != nil {
			parent = tracker.DepartmentKey(g.Name)
		}
		at.boxes = append(at.boxes, boxDef{key: tracker.DepartmentKey(d.Name), name: d.Name, parent: parent, budget: d.Budget})
	}
	at.boxOf = func(issue *tracker.Issue) string {
		if d := p.DepartmentOf(issue); d != "" {
			return tracker.DepartmentKey(d)
		}
		return Product
	}
	at.names = map[string]owner{tracker.LeadSessionName(p): {box: HQ, lead: true}}
	for _, d := range p.Departments {
		at.names[tracker.DepartmentLeadName(p, d.Name)] = owner{box: tracker.DepartmentKey(d.Name), lead: true}
	}
	for _, issue := range issues {
		o := owner{box: at.boxOf(issue), issue: issue}
		at.names[tracker.AgentSessionName(issue.Slug)] = o
		if short := "agent-" + filepath.Base(issue.Slug); at.names[short].box == "" {
			at.names[short] = o
		}
	}
	return at
}

// members are, per box key, the boxes whose spend is in it: itself, every
// department under a group (nested groups count up the chain), and every
// box for HQ. rolled are the keys rolled into each box, in order.
func members(defs []boxDef) (members, rolled map[string][]string) {
	members, rolled = map[string][]string{}, map[string][]string{}
	parent := map[string]string{}
	for _, d := range defs {
		members[d.key] = []string{d.key}
		parent[d.key] = d.parent
	}
	for _, d := range defs {
		// Every group up the chain counts d (nested groups,
		// factory/nested-groups-…#D-1); parent is set only for a good
		// chain, and the walk is bounded anyway.
		for up, n := d.parent, 0; up != "" && members[up] != nil && n <= len(defs); n++ {
			members[up] = append(members[up], d.key)
			rolled[up] = append(rolled[up], d.key)
			up = parent[up]
		}
		if d.key != HQ {
			members[HQ] = append(members[HQ], d.key)
			rolled[HQ] = append(rolled[HQ], d.key)
		}
	}
	return members, rolled
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
func rollUp(rep *Report, boxes map[string]*Box, starts []time.Time) {
	var defs []boxDef
	for _, b := range rep.Boxes {
		defs = append(defs, boxDef{key: b.Key, parent: b.Parent})
	}
	keys, rolled := members(defs)
	for _, b := range rep.Boxes {
		b.Weeks = emptyWeeks(starts)
		b.RolledFrom = rolled[b.Key]
		top := map[string]*IssueSpend{}
		for _, k := range keys[b.Key] {
			m := boxes[k]
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
		s.Text = tokens.Format(s.Spent.Tokens()) + " of " + tokens.Format(s.Tokens) + " tokens " + period
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
	// compactions are when the session's context was compacted
	// (compact_boundary records).
	compactions []time.Time
	// chars is the first request by part, read on first use (spend
	// --context only).
	chars     *floorChars
	charsRead bool
}

// floorChars is the file's first request by part, read once.
func (fr *fileRead) floorChars(path string) *floorChars {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if !fr.charsRead {
		fr.chars, fr.charsRead = readFloorChars(path), true
	}
	return fr.chars
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
	fr.agent, fr.compactions = scanMeta(path)
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

// AgentName is the name a transcript file's session runs under: its last
// name record, or "".
func AgentName(path string) string {
	name, _ := scanMeta(path)
	return name
}

// compactRecordRe is a compaction record; like a name record, only a whole
// record matches.
var compactRecordRe = regexp.MustCompile(`"type"\s*:\s*"system"\s*,\s*"subtype"\s*:\s*"compact_boundary"`)

// scanMeta is a transcript file's session name (its last name record, or
// "") and the times of its compactions. Numbers and names only: no message
// text is kept.
func scanMeta(path string) (name string, compactions []time.Time) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if !bytes.Contains(line, []byte(`"type"`)) {
			continue
		}
		if bytes.Contains(line, []byte("compact_boundary")) && compactRecordRe.Match(line) {
			var rec struct {
				Type, Subtype, Timestamp string
			}
			if json.Unmarshal(line, &rec) == nil && rec.Type == "system" && rec.Subtype == "compact_boundary" {
				if at, err := time.Parse(time.RFC3339Nano, rec.Timestamp); err == nil {
					compactions = append(compactions, at)
				}
			}
			continue
		}
		if len(line) > 4096 || !nameRecordRe.Match(line) {
			continue
		}
		if m := nameRe.FindSubmatch(line); m != nil {
			name = string(m[1])
		}
	}
	return name, compactions
}

// ResetCache drops the cached reads (tests).
func ResetCache() {
	cacheMu.Lock()
	cache = map[string]*fileRead{}
	cacheMu.Unlock()
}
