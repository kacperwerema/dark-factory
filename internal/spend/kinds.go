package spend

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/critique"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// spend --kinds: where the tokens go by project and kind of session, over
// a window. A session's kind comes from its agent name first (lead-*,
// agent-<slug>, postmortem-*, critic-*, bench-*, review-*), then from its
// folder (an issue's worktree, the project folder). A worker's issue gives
// its type, its stage (the issue's status at each turn) and, for a job's
// run issue (job: lead-flush, a sweep), the job. Numbers and names only.

// Kinds of session.
const (
	KindWorker     = "worker"
	KindLead       = "lead"
	KindSubagent   = "subagent"
	KindPostmortem = "post-mortem"
	KindCritic     = "critic"
	KindSweep      = "sweep"
	KindFlush      = "flush"
	KindJob        = "job"
	KindBenchmark  = "benchmark"
	KindReview     = "review"
	KindOwner      = "owner"
	KindOther      = "other"
)

// OtherProject is the project of transcript folders that belong to none.
const OtherProject = "other"

// KindRow is one row: a project's kind of session (and, in Workers, a
// worker type at a stage).
type KindRow struct {
	Project string `json:"project"`
	Kind    string `json:"kind"`
	// Job is a job run's job id (kind job, sweep, flush).
	Job string `json:"job,omitempty"`
	// Type and Stage split workers: the issue's type of work and its
	// status when the turn ran.
	Type     string `json:"type,omitempty"`
	Stage    string `json:"stage,omitempty"`
	Sessions int    `json:"sessions"`
	Turns    int    `json:"turns"`
	Tally
	// Share is the row's share of the window's estimated USD (of its
	// tokens when nothing is priced: KindsReport.ShareOf).
	Share float64 `json:"share"`
	// LimitShare is the row's share of the weekly limit, pro rata for the
	// window's days; nil without weekly_limit: in prices.yaml.
	LimitShare *float64 `json:"weekly_limit_share,omitempty"`

	seen map[string]bool
}

// Label is the row's kind as the table shows it.
func (r KindRow) Label() string {
	switch {
	case r.Kind == KindWorker && r.Type != "" && r.Stage != "":
		return "worker " + r.Type + " · " + r.Stage
	case r.Kind == KindWorker && r.Type != "":
		return "worker " + r.Type
	case r.Job != "" && r.Kind == KindJob:
		return "job " + r.Job
	case r.Kind == KindOwner:
		return "owner's own"
	}
	return r.Kind
}

func (r *KindRow) add(session string, t Tally) {
	if r.seen == nil {
		r.seen = map[string]bool{}
	}
	if !r.seen[session] {
		r.seen[session] = true
		r.Sessions++
	}
	r.Turns++
	r.Add(t)
}

// KindsReport is spend --kinds.
type KindsReport struct {
	Since time.Time `json:"since"`
	Until time.Time `json:"until"`
	// Days is the window's length in days (fractional for --since/--until).
	Days     float64  `json:"days"`
	Projects []string `json:"projects"`
	// ByProject is each project's total; Rows each project's kinds; Workers
	// each project's workers by type and stage.
	ByProject []KindRow `json:"by_project"`
	Rows      []KindRow `json:"rows"`
	Workers   []KindRow `json:"workers"`
	Total     KindRow   `json:"total"`
	// ShareOf is what Share divides: "usd", or "tokens" when nothing has a
	// price.
	ShareOf    string       `json:"share_of"`
	Prices     []PriceState `json:"prices"`
	PricesPath string       `json:"prices_path"`
	PricesErr  string       `json:"prices_error,omitempty"`
	Unpriced   []string     `json:"unpriced_models,omitempty"`
	// WeeklyLimit is prices.yaml's weekly_limit:; LimitNote says what to
	// set when there is none.
	WeeklyLimit *WeeklyLimit `json:"weekly_limit,omitempty"`
	LimitNote   string       `json:"weekly_limit_note,omitempty"`
	// Folders is how many transcript folders were read; BenchRuns how many
	// benchmark tasks were counted from their run records.
	Folders    int `json:"folders"`
	BenchTasks int `json:"bench_tasks"`
}

// KindsOptions are spend --kinds' inputs.
type KindsOptions struct {
	Options
	// Projects are the projects read; Others also counts the folders that
	// belong to none of them, as project "other".
	Projects []*tracker.Project
	Issues   map[string][]*tracker.Issue
	Others   bool
	// Book is the prices; nil reads LoadPriceBook("").
	Book *PriceBook
	// BenchRuns are the benchmark runs; nil reads every benchmark's.
	BenchRuns func() []*bench.Run
}

// folderNonAlnum is how Claude Code names a transcript folder: the cwd
// with every non-alphanumeric as -.
var folderNonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// FolderName is the transcript folder name of cwd, before truncation.
func FolderName(cwd string) string { return folderNonAlnum.ReplaceAllString(cwd, "-") }

// sessOwner is who a session is: its kind and, for a worker, its issue.
type sessOwner struct {
	kind  string
	issue *tracker.Issue
}

// projKinds is one project's names and folders.
type projKinds struct {
	slug   string
	folder string // the project folder's transcript folder name
	names  map[string]sessOwner
	wt     map[string]*tracker.Issue   // a worktree's full folder name → issue
	trunc  map[string][]*tracker.Issue // a truncated one's prefix → issues
	stages map[string][]tracker.TransitionStat
}

func newProjKinds(p *tracker.Project, issues []*tracker.Issue) *projKinds {
	pk := &projKinds{slug: p.Slug, folder: FolderName(p.Root()), names: map[string]sessOwner{},
		wt: map[string]*tracker.Issue{}, trunc: map[string][]*tracker.Issue{}, stages: map[string][]tracker.TransitionStat{}}
	pk.names[tracker.LeadSessionName(p)] = sessOwner{kind: KindLead}
	for _, d := range p.Departments {
		pk.names[tracker.DepartmentLeadName(p, d.Name)] = sessOwner{kind: KindLead}
	}
	for _, is := range issues {
		o := sessOwner{kind: KindWorker, issue: is}
		pk.names[tracker.AgentSessionName(is.Slug)] = o
		pk.names[tracker.LimitedSessionName(is.Slug)] = o
		if short := "agent-" + filepath.Base(is.Slug); pk.names[short].issue == nil {
			pk.names[short] = o
		}
		name := FolderName(filepath.Join(p.Root(), ".worktrees", is.Slug))
		if len(name) <= bench.TranscriptSlugMax {
			pk.wt[name] = is
		} else {
			pk.trunc[name[:bench.TranscriptSlugMax]] = append(pk.trunc[name[:bench.TranscriptSlugMax]], is)
		}
	}
	return pk
}

// place is where a folder is in a project.
type place int

const (
	placeRoot place = iota
	placeWorktree
	placeElsewhere
)

// where is the folder's place in pk and its worktree's issue (nil when
// not one issue's).
func (pk *projKinds) where(folder string) (place, *tracker.Issue) {
	if folder == pk.folder {
		return placeRoot, nil
	}
	if !strings.HasPrefix(folder, pk.folder+"--worktrees-") {
		return placeElsewhere, nil
	}
	if is := pk.wt[folder]; is != nil {
		return placeWorktree, is
	}
	if len(folder) > bench.TranscriptSlugMax {
		if c := pk.trunc[folder[:bench.TranscriptSlugMax]]; len(c) == 1 {
			return placeWorktree, c[0]
		}
	}
	return placeWorktree, nil
}

// classify is a main session's owner from its agent name, then its place.
func (pk *projKinds) classify(name string, pl place, wtIssue *tracker.Issue) sessOwner {
	if o, ok := pk.names[name]; ok {
		return o
	}
	switch {
	case strings.HasPrefix(name, "postmortem-"):
		return sessOwner{kind: KindPostmortem}
	case strings.HasPrefix(name, critique.SessionPrefix):
		return sessOwner{kind: KindCritic}
	case strings.HasPrefix(name, bench.SessionPrefix):
		return sessOwner{kind: KindBenchmark}
	case strings.HasPrefix(name, "review-"):
		return sessOwner{kind: KindReview}
	case strings.HasPrefix(name, "lead-"):
		return sessOwner{kind: KindLead}
	case strings.HasPrefix(name, "agent-"):
		return sessOwner{kind: KindWorker, issue: wtIssue}
	case name != "":
		return sessOwner{kind: KindOther}
	}
	switch pl {
	case placeWorktree:
		return sessOwner{kind: KindWorker, issue: wtIssue}
	case placeRoot:
		return sessOwner{kind: KindOwner}
	}
	return sessOwner{kind: KindOther}
}

// jobKind is a job run issue's kind: sweep, flush or job.
func jobKind(job string) string {
	j := strings.ToLower(job)
	switch {
	case strings.Contains(j, "flush"):
		return KindFlush
	case strings.Contains(j, "sweep"):
		return KindSweep
	}
	return KindJob
}

// stageAt is the issue's status at t: the last transition's to at or
// before t, else the first one's from, else its status now.
func (pk *projKinds) stageAt(is *tracker.Issue, t time.Time) string {
	ts, ok := pk.stages[is.Slug]
	if !ok {
		if st, err := tracker.LoadStats(is.FilePath); err == nil {
			ts = append(ts, st.Transitions...)
			sort.SliceStable(ts, func(i, j int) bool { return ts[i].TS.Before(ts[j].TS) })
		}
		pk.stages[is.Slug] = ts
	}
	if len(ts) == 0 {
		return is.Status
	}
	i := sort.Search(len(ts), func(i int) bool { return ts[i].TS.After(t) })
	if i == 0 {
		return ts[0].From
	}
	return ts[i-1].To
}

// LoadKinds reads the window [since, until] for opt.Projects.
func LoadKinds(since, until time.Time, opt KindsOptions) *KindsReport {
	if opt.ProjectsDir == "" {
		opt.ProjectsDir = DefaultProjectsDir()
	}
	book := opt.Book
	if book == nil {
		b := LoadPriceBook("")
		book = &b
	}
	rep := &KindsReport{Since: since, Until: until, Days: until.Sub(since).Hours() / 24,
		PricesPath: book.Path, PricesErr: book.Err, WeeklyLimit: book.Limit}
	var pks []*projKinds
	for _, p := range opt.Projects {
		pks = append(pks, newProjKinds(p, opt.Issues[p.Slug]))
		rep.Projects = append(rep.Projects, p.Slug)
	}
	if opt.Others {
		rep.Projects = append(rep.Projects, OtherProject)
	}

	type rowKey struct{ project, kind, job string }
	type workerKey struct{ project, typ, stage string }
	rows := map[rowKey]*KindRow{}
	workers := map[workerKey]*KindRow{}
	projects := map[string]*KindRow{}
	models := map[string]bool{}
	unpriced := map[string]bool{}
	add := func(project string, o sessOwner, job string, session string, r record, stage string) {
		t := r.tally(book.Prices, unpriced)
		if r.model != "" {
			models[r.model] = true
		}
		k := rowKey{project, o.kind, job}
		if rows[k] == nil {
			rows[k] = &KindRow{Project: project, Kind: o.kind, Job: job}
		}
		rows[k].add(session, t)
		if projects[project] == nil {
			projects[project] = &KindRow{Project: project}
		}
		projects[project].add(session, t)
		rep.Total.add(session, t)
		if o.kind == KindWorker {
			typ := "untyped"
			if o.issue == nil {
				typ = "unknown issue"
			} else if o.issue.Type != "" {
				typ = o.issue.Type
			}
			wk := workerKey{project, typ, stage}
			if workers[wk] == nil {
				workers[wk] = &KindRow{Project: project, Kind: KindWorker, Type: typ, Stage: stage}
			}
			workers[wk].add(session, t)
		}
	}
	inWindow := func(r record) bool { return !r.at.Before(since) && !r.at.After(until) }

	entries, _ := os.ReadDir(opt.ProjectsDir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		var pk *projKinds
		for _, c := range pks {
			if (e.Name() == c.folder || strings.HasPrefix(e.Name(), c.folder+"-")) && (pk == nil || len(c.folder) > len(pk.folder)) {
				pk = c
			}
		}
		if pk == nil && !opt.Others {
			continue
		}
		folder := filepath.Join(opt.ProjectsDir, e.Name())
		sessions := kindSessions(folder, since)
		if len(sessions) == 0 {
			continue
		}
		rep.Folders++
		for _, s := range sessions {
			project, o, job := OtherProject, sessOwner{kind: KindOther}, ""
			if pk != nil {
				project = pk.slug
				pl, wtIssue := pk.where(e.Name())
				o = pk.classify(s.agent, pl, wtIssue)
				if o.kind == KindWorker && o.issue != nil {
					if j := o.issue.Field("job"); j != "" {
						o.kind, job = jobKind(j), j
					}
				}
			} else if strings.HasPrefix(s.agent, bench.SessionPrefix) {
				o.kind = KindBenchmark
			} else if strings.HasPrefix(s.agent, critique.SessionPrefix) {
				o.kind = KindCritic
			}
			for _, r := range s.main {
				if !inWindow(r) {
					continue
				}
				stage := ""
				if o.kind == KindWorker {
					stage = "unknown"
					if o.issue != nil && pk != nil {
						stage = pk.stageAt(o.issue, r.at)
					}
				}
				add(project, o, job, s.id, r, stage)
			}
			for _, r := range s.subs {
				if inWindow(r) {
					add(project, sessOwner{kind: KindSubagent}, "", s.id+"/sub", r, "")
				}
			}
		}
	}

	// Benchmark tasks whose worker had a sandbox HOME: their transcripts are
	// not in the projects folder, so their run records count. A run on the
	// owner's HOME (or from before worker_home) is read from its transcripts
	// above, never twice.
	runs := opt.BenchRuns
	if runs == nil {
		runs = allBenchRuns
	}
	known := map[string]bool{}
	for _, p := range rep.Projects {
		known[p] = true
	}
	for _, run := range runs() {
		if run.Setup.WorkerHome != bench.WorkerHomeSandbox {
			continue
		}
		for i, task := range run.Tasks {
			u := task.Usage
			if !u.Known || task.Started.Before(since) || task.Started.After(until) {
				continue
			}
			project := task.Project
			if !known[project] {
				if !opt.Others {
					continue
				}
				project = OtherProject
			}
			r := record{at: task.Started, model: u.Model, in: u.Input, out: u.Output, cacheRead: u.CacheRead, cacheWrite: u.CacheWrite}
			t := r.tally(book.Prices, unpriced)
			if strings.Contains(u.Model, ",") || r.model == "" {
				// Several models (or none): one count has no one price.
				t.USD, t.Unpriced = 0, t.Tokens()
				if u.USDKnown {
					t.USD, t.Unpriced = u.USD, 0
				}
			}
			if r.model != "" && !strings.Contains(r.model, ",") {
				models[r.model] = true
			}
			session := run.ID + "/" + task.Task + "/" + strconv.Itoa(i)
			k := rowKey{project, KindBenchmark, ""}
			if rows[k] == nil {
				rows[k] = &KindRow{Project: project, Kind: KindBenchmark}
			}
			turns := u.Turns
			if turns < 1 {
				turns = 1
			}
			for _, row := range []*KindRow{rows[k], projectRow(projects, project), &rep.Total} {
				row.add(session, t)
				row.Turns += turns - 1
			}
			rep.BenchTasks++
		}
	}

	for m := range unpriced {
		rep.Unpriced = append(rep.Unpriced, m)
	}
	sort.Strings(rep.Unpriced)
	rep.Prices = book.States(models)
	rep.ShareOf = "usd"
	if rep.Total.USD == 0 {
		rep.ShareOf = "tokens"
	}
	order := map[string]int{}
	for i, p := range rep.Projects {
		order[p] = i
	}
	finish := func(r *KindRow) KindRow {
		switch {
		case rep.ShareOf == "usd" && rep.Total.USD > 0:
			r.Share = r.USD / rep.Total.USD
		case rep.Total.Tokens() > 0:
			r.Share = float64(r.Tokens()) / float64(rep.Total.Tokens())
		}
		if s, ok := rep.WeeklyLimit.Share(r.Tally, rep.Days); ok {
			r.LimitShare = &s
		}
		return *r
	}
	sorted := func(m []*KindRow) []KindRow {
		sort.Slice(m, func(i, j int) bool {
			if m[i].Project != m[j].Project {
				return order[m[i].Project] < order[m[j].Project]
			}
			if m[i].USD != m[j].USD {
				return m[i].USD > m[j].USD
			}
			if m[i].Tokens() != m[j].Tokens() {
				return m[i].Tokens() > m[j].Tokens()
			}
			return m[i].Label() < m[j].Label()
		})
		out := make([]KindRow, 0, len(m))
		for _, r := range m {
			out = append(out, finish(r))
		}
		return out
	}
	var rs, ws, ps []*KindRow
	for _, r := range rows {
		rs = append(rs, r)
	}
	for _, r := range workers {
		ws = append(ws, r)
	}
	for _, p := range rep.Projects {
		if r := projects[p]; r != nil {
			ps = append(ps, r)
		}
	}
	rep.Rows, rep.Workers, rep.ByProject = sorted(rs), sorted(ws), sorted(ps)
	rep.Total.Project, rep.Total.Kind = "all", "all"
	rep.Total = finish(&rep.Total)
	if rep.WeeklyLimit == nil {
		rep.LimitNote = "% of weekly limit: set " + LimitSnippet + " in " + book.Path
	}
	return rep
}

func projectRow(m map[string]*KindRow, p string) *KindRow {
	if m[p] == nil {
		m[p] = &KindRow{Project: p}
	}
	return m[p]
}

func allBenchRuns() []*bench.Run {
	var out []*bench.Run
	for _, name := range bench.List() {
		out = append(out, bench.Runs(name)...)
	}
	return out
}

// kindSession is one session of a folder: its main transcript's records
// and its subagents', apart, each message id once across the folder.
type kindSession struct {
	id, agent string
	main      []record
	subs      []record
}

// kindSessions reads a folder's sessions with a file changed since since.
func kindSessions(folder string, since time.Time) []kindSession {
	files := bench.SessionFiles(folder)
	tops := make([]string, 0, len(files))
	for top := range files {
		tops = append(tops, top)
	}
	sort.Strings(tops)
	seen := map[string]bool{}
	var out []kindSession
	for _, top := range tops {
		s := kindSession{id: top}
		fresh := false
		for i, f := range files[top] {
			st, err := os.Stat(f)
			if err != nil || st.ModTime().Before(since) {
				continue
			}
			fresh = true
			fr := readFile(f, st)
			if i == 0 {
				s.agent = fr.agent
			}
			for _, r := range fr.recs {
				if r.id != "" && seen[r.id] {
					continue
				}
				seen[r.id] = true
				if i == 0 {
					s.main = append(s.main, r)
				} else {
					s.subs = append(s.subs, r)
				}
			}
		}
		if fresh {
			if s.agent == "" {
				if st, err := os.Stat(top); err == nil {
					s.agent = readFile(top, st).agent
				}
			}
			out = append(out, s)
		}
	}
	return out
}
