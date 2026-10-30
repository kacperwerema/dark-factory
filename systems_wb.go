package main

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/chart"
	"github.com/michal-franc/issue-viewer/internal/health"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The Systems page (L2, /p/<project>/systems): every system is a workshop in
// the hall and every issue a crate in it, coloured by what it waits on. The
// right panel is the code map: which files the systems' issues name, which of
// them are hotspots (the health snapshot's shape family) and which belong to
// several systems. A system's files come from its paths: globs in
// workbench.systems; a system without them falls back to the files its
// issues name, and the map is a draft until every system has paths. Design: issues/Factory/designs/workshop-board/L2-systems.
// The classic index stays at ?classic=1 (systems.go, systems.html).

const (
	// systemsCrateMax is how many crates a workshop draws before "+N".
	systemsCrateMax = 30
	// systemsCodeChips is how many files a workshop's CODE row names.
	systemsCodeChips = 3
	// codeMapPackages and codeMapFiles cap the code map.
	codeMapPackages = 5
	codeMapFiles    = 7
	// codeMapSystems is how many system names a code-map row spells out
	// before it says "N systems".
	codeMapSystems = 2
	// sharedHotNamed is how many shared hot files the warning names.
	sharedHotNamed = 4
)

// WbFigure is a big number with its label; Tone "you" draws it orange.
type WbFigure struct {
	Value int
	Label string
	Tone  string
}

// WbCrateDot is one issue as a small crate: State is you (waits on the
// owner), working (an agent is on it), queue (open, nobody on it) or done.
type WbCrateDot struct {
	State, Title, URL string
}

// WbCrateKey is one entry of the crate legend.
type WbCrateKey struct {
	State, Label string
}

// Dot is the key's sample crate.
func (k WbCrateKey) Dot() WbCrateDot { return WbCrateDot{State: k.State} }

// wbCrateKeys is the legend of WbCrateDot's states, in the design's order.
func wbCrateKeys() []WbCrateKey {
	return []WbCrateKey{
		{State: "you", Label: "at your gate"},
		{State: "working", Label: "agent working"},
		{State: "queue", Label: "waiting in the queue"},
		{State: "done", Label: "done"},
	}
}

// WbCodeChip is a file on a workshop's CODE row; Hot marks a hotspot.
type WbCodeChip struct {
	Name, Path string
	Hot        bool
}

// WbWorkshop is one system as a workshop card. CodeNone is what the CODE
// row says when it has no file.
type WbWorkshop struct {
	Name, URL    string
	Open, Done   int
	Gates        int
	GatesTitle   string
	Crates       []WbCrateDot
	More         int
	Code         []WbCodeChip
	CodeNone     string
	Decisions    int
	DecisionsURL string
	LineURL      string
	Note         string
}

// Aria describes the crate row for screen readers.
func (w WbWorkshop) Aria() string {
	return fmt.Sprintf("%s: %d open, %d done", w.Name, w.Open, w.Done)
}

// WbCodeFile is one file on the code map with the systems that own it ("?"
// when none does: a hotspot nobody claims).
type WbCodeFile struct {
	Name, Path, Systems string
	// SystemsTitle names every system when Systems only counts them, and
	// says why nobody owns a "?" file.
	SystemsTitle string
	Hot, Shared  bool
}

// WbCodePackage is a package (a directory) on the code map.
type WbCodePackage struct {
	Name  string
	Files []WbCodeFile
	More  int
}

// WbCodeMap is the right panel: packages → files → systems. Draft: some
// system has no paths: in workbench.systems, so its files are read from its
// issues. Empty explains a map with nothing on it.
type WbCodeMap struct {
	Note      string
	Draft     bool
	Packages  []WbCodePackage
	More      int
	Empty     string
	HotSource string
}

// WbWarning is a red note: a title and a sentence.
type WbWarning struct {
	Title, Text string
	Files       []string
}

// WbListItem is one item of an inline list with the separator before it.
type WbListItem struct{ Sep, Text string }

// List is Files as "a, b and c".
func (w WbWarning) List() []WbListItem {
	out := make([]WbListItem, len(w.Files))
	for i, f := range w.Files {
		out[i].Text = f
		switch {
		case i == 0:
		case i == len(w.Files)-1:
			out[i].Sep = " and "
		default:
			out[i].Sep = ", "
		}
	}
	return out
}

// WbUnlockCard is a locked feature with the action that unlocks it: a
// message to the project's CEO (SendURL) prefilled with Message.
type WbUnlockCard struct {
	Title, Text, Button string
	SendURL, Message    string
	Live                bool
	Note                string
}

// SystemsWbData is the whole Systems page.
type SystemsWbData struct {
	Header     WbHeader
	Lot        MapLot
	Figures    []WbFigure
	Legend     []WbCrateKey
	Workshops  []WbWorkshop
	NoSystem   string
	CodeMap    WbCodeMap
	SharedHot  *WbWarning
	Unlock     *WbUnlockCard
	ClassicURL string
	// NotFound names the system or family a /systems/<name> link asked for
	// that does not exist (served with 404).
	NotFound string
}

func (s *Server) handleSystemsWb(w http.ResponseWriter, proj *tracker.Project, prefix string, issues []*tracker.Issue) {
	data := s.buildSystemsWb(proj, prefix, issues, time.Now())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "systems_wb.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleSystemsWbNotFound is the Systems page with a 404 and a note naming
// the system or family that does not exist.
func (s *Server) handleSystemsWbNotFound(w http.ResponseWriter, proj *tracker.Project, prefix string, issues []*tracker.Issue, name string) {
	data := s.buildSystemsWb(proj, prefix, issues, time.Now())
	data.NotFound = name
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	if err := s.tmpl.ExecuteTemplate(w, "systems_wb.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) buildSystemsWb(p *tracker.Project, prefix string, issues []*tracker.Issue, now time.Time) SystemsWbData {
	company := s.buildCompany(p, prefix, now)
	lot := company.Lot
	wf := p.LoadWorkflow()
	st := newStatusTones(wf)
	groups, none := groupSystems(issues, wf.Workbench)

	d := SystemsWbData{Lot: lot, Legend: wbCrateKeys(), ClassicURL: prefix + "/systems?classic=1"}
	d.Header = systemsHeader(p, prefix, company)

	waits, workerOf := s.systemsWho(p, prefix, st, groups)
	index := tracker.BuildDecisionIndex(issues, tracker.ClosedIssueFunc(p))
	decisionsOf := map[*tracker.Issue]int{}
	for _, e := range index.Entries {
		decisionsOf[e.Issue]++
	}

	root := ciRootFor(p)
	snap := latestHealth(p, now)
	code := newCodeIndex(root, snap)

	totalOpen, totalGates := 0, 0
	for _, g := range groups {
		ws := systemWorkshop(st, prefix, g, waits, workerOf, decisionsOf, code)
		totalOpen += ws.Open
		totalGates += ws.Gates
		d.Workshops = append(d.Workshops, ws)
	}
	sort.SliceStable(d.Workshops, func(i, j int) bool { return d.Workshops[i].Open > d.Workshops[j].Open })
	d.Figures = []WbFigure{
		{Value: len(d.Workshops), Label: "systems"},
		{Value: totalOpen, Label: "open"},
		{Value: totalGates, Label: "gates for you"},
	}
	if totalGates > 0 {
		d.Figures[2].Tone = "you"
	}
	if len(none) > 0 {
		o, dn, _ := st.counts(none)
		d.NoSystem = fmt.Sprintf("No system: %d open, %d done — issues without a system field are in no workshop.", o, dn)
	}

	d.CodeMap = code.codeMap(snap != nil)
	d.SharedHot = code.sharedHot()

	var missing []string
	for _, g := range groups {
		if !g.Has {
			missing = append(missing, g.Name)
		}
	}
	if len(missing) > 0 {
		u := &WbUnlockCard{
			Title:   "Unlock: workbench.systems",
			Text:    fmt.Sprintf("Add the systems to workflow.yaml and each workshop gets a summary, a design doc and its families; paths: per system confirm the code map. %s not there yet.", notThere(missing)),
			Button:  "Give the draft to an agent",
			SendURL: prefix + "/lead/send",
			Live:    lot.CEO.Live,
			Message: fmt.Sprintf("Draft workbench.systems in workflow.yaml for %s: a one-line summary, a design doc and paths: (repo globs of its code) for each, and families where an umbrella epic exists. Use the code map on the Systems page as the draft of which files belong where. Do not commit; show me the diff first.", strings.Join(missing, ", ")),
		}
		if !u.Live {
			u.Note = "Start the CEO on the Company page first: the draft goes to it."
		}
		d.Unlock = u
	} else if len(code.unpathed) > 0 {
		u := &WbUnlockCard{
			Title:   "Unlock: a confirmed code map",
			Text:    fmt.Sprintf("Give %s paths: in workbench.systems (repo globs of its code) and the code map stops being a draft.", joinAnd(code.unpathed)),
			Button:  "Give the draft to an agent",
			SendURL: prefix + "/lead/send",
			Live:    lot.CEO.Live,
			Message: fmt.Sprintf("Add paths: (repo globs of each system's code, relative to the project root) to workbench.systems in workflow.yaml for %s. Use the code map on the Systems page as the draft of which files belong where, and check with issue-cli health lint --check paths. Do not commit; show me the diff first.", strings.Join(code.unpathed, ", ")),
		}
		if !u.Live {
			u.Note = "Start the CEO on the Company page first: the draft goes to it."
		}
		d.Unlock = u
	}
	return d
}

// systemsHeader is the hall header with the Systems tab current, for the
// index and the Workshop page.
func systemsHeader(p *tracker.Project, prefix string, company CompanyData) WbHeader {
	lot := company.Lot
	h := WbHeader{WaitingURL: prefix + "/needs-you", Crumbs: wbCrumbs(2, p.Name, prefix+"/company"), Tabs: wbHallTabs(prefix, "systems"), Working: lot.Working, Idle: lot.Idle, AgentWait: lot.AgentWait, DoneWeek: lot.DoneWeek, Waiting: company.Waiting, HideDone: true}
	h.Crumbs[1].Color = lot.Color
	h.YOLO = headerYOLO(lot.YOLO, prefix)
	h.Crumbs[2] = WbCrumb{Label: "Product hall", Current: true, Color: companyProductColor}
	if c := wbCIChip(lot.CI, lot.CILine); c != nil {
		h.Chips = append(h.Chips, *c)
	}
	return h
}

// systemsWho is what each open issue of the groups needs: the owner's
// waiting items and the worker on it, both keyed by lower-case slug.
func (s *Server) systemsWho(p *tracker.Project, prefix string, st *statusTones, groups []*systemGroup) (map[string][]WaitingItem, map[string]AgentNode) {
	var open []*tracker.Issue
	for _, g := range groups {
		open = append(open, openIssues(st, g.Members)...)
	}
	waits := map[string][]WaitingItem{}
	for _, it := range waitingOnOwner(p, open) {
		k := strings.ToLower(it.Issue.Slug)
		waits[k] = append(waits[k], it)
	}
	workerOf := map[string]AgentNode{}
	if agents, _ := s.buildAgentsModel(p, prefix); agents != nil {
		for _, w := range agents.Workers {
			if w.Issue != nil {
				workerOf[strings.ToLower(w.Issue.Slug)] = w
			}
		}
	}
	return waits, workerOf
}

// systemWorkshop is one system's card: counts, crates, gates, code and
// decisions.
func systemWorkshop(st *statusTones, prefix string, g *systemGroup, waits map[string][]WaitingItem, workerOf map[string]AgentNode, decisionsOf map[*tracker.Issue]int, code *codeIndex) WbWorkshop {
	ws := WbWorkshop{
		Name: g.Name, URL: systemHref(prefix, g.Name),
		DecisionsURL: prefix + "/decisions?system=" + url.QueryEscape(g.Name),
		LineURL:      prefix + "/hall",
	}
	ws.Open, ws.Done, _ = st.counts(g.Members)
	for _, issue := range g.Members {
		ws.Decisions += decisionsOf[issue]
	}
	cs := systemCrates(st, prefix, g.Members, waits, workerOf)
	ws.Crates, ws.Gates = cs.Crates, cs.Gates
	if len(ws.Crates) > systemsCrateMax {
		ws.More = len(ws.Crates) - systemsCrateMax
		ws.Crates = ws.Crates[:systemsCrateMax]
	}
	ws.GatesTitle = cs.gatesTitle()

	ws.Code = code.chipsFor(g.Name, g.Config.Paths, g.Members)
	ws.CodeNone = "no file named in its issues"
	if len(g.Config.Paths) > 0 {
		ws.CodeNone = "no file matches its paths"
	}
	ws.Note = code.longFuncNote(g.Name)
	if ws.Note == "" && ws.Gates > cs.Gated && cs.Gated > 0 {
		ws.Note = fmt.Sprintf("%s on %s", plural(ws.Gates, "gate", "gates"), plural(cs.Gated, "issue", "issues"))
	}
	return ws
}

// wbCrates is a system's issues as crates, the owner's first, with how many
// gates wait on the owner (Gates), on how many issues (Gated) and of which
// kinds.
type wbCrates struct {
	Crates       []WbCrateDot
	Gates, Gated int
	kinds        map[string]int
}

// gatesTitle is the gates pill's tooltip: "Waiting on you: 2 gates, 1 question".
func (cs wbCrates) gatesTitle() string {
	if cs.Gates == 0 {
		return ""
	}
	var parts []string
	for _, k := range [][3]string{{"gate", "gate", "gates"}, {"playtest", "playtest", "playtests"}, {"checks", "check run", "check runs"}, {"question", "question", "questions"}} {
		if n := cs.kinds[k[0]]; n > 0 {
			parts = append(parts, plural(n, k[1], k[2]))
		}
	}
	return "Waiting on you: " + strings.Join(parts, ", ")
}

// systemCrates draws members (side statuses left out) as crates by what
// each waits on: the owner, an agent, the queue, or done.
func systemCrates(st *statusTones, prefix string, members []*tracker.Issue, waits map[string][]WaitingItem, workerOf map[string]AgentNode) wbCrates {
	rank := map[string]int{"you": 0, "working": 1, "queue": 2, "done": 3}
	cs := wbCrates{kinds: map[string]int{}}
	var crates []WbCrateDot
	for _, issue := range members {
		k := statusKey(issue.Status)
		if st.side[k] {
			continue
		}
		c := WbCrateDot{State: "queue", URL: prefix + "/issue/" + issue.Slug}
		key := strings.ToLower(issue.Slug)
		w, hasWorker := workerOf[key]
		switch {
		case k == st.final:
			c.State = "done"
		case len(waits[key]) > 0:
			c.State = "you"
			cs.Gated++
			for _, it := range waits[key] {
				cs.Gates++
				cs.kinds[it.Kind]++
			}
		case hasWorker && workerRobot(w).State == "you":
			c.State = "you"
		case hasWorker:
			c.State = "working"
		}
		c.Title = issue.Slug + " · " + issue.Status
		if hasWorker && c.State != "done" {
			c.Title += " · " + workerRobot(w).StateLabel()
		}
		crates = append(crates, c)
	}
	sort.SliceStable(crates, func(i, j int) bool { return rank[crates[i].State] < rank[crates[j].State] })
	cs.Crates = crates
	return cs
}

// filePattern finds file paths in issue text: a run of path characters
// ending in a source-file extension. Only paths that exist in the repo
// count.
var filePattern = regexp.MustCompile(`[A-Za-z0-9_@.\-/]+\.(?:tsx|ts|jsx|js|mjs|cjs|go|rs|py|css|scss|html|gd|tscn|cs|swift|kt|java|rb|sql|sh|vue|svelte|cpp|c|h)\b`)

// codeIndex is the code map: the files each system owns (its paths: globs,
// else the files its issues name), joined with the health snapshot's
// hotspots and longest functions.
type codeIndex struct {
	root   string
	exists map[string]bool
	// repo is the project's file list, read on the first system with paths.
	repo     []string
	repoRead bool
	// owned: system → files it owns; bySys: file → systems.
	owned map[string]map[string]bool
	bySys map[string][]string
	// pathed and unpathed are the systems with and without paths, in the
	// order chipsFor saw them.
	pathed, unpathed []string
	hot              map[string]bool
	hotOrder         []string
	long             []health.Row
}

func newCodeIndex(root string, snap *health.Snapshot) *codeIndex {
	c := &codeIndex{root: root, exists: map[string]bool{}, owned: map[string]map[string]bool{}, bySys: map[string][]string{}, hot: map[string]bool{}}
	if snap != nil {
		rows := snap.Rows("shape.hotspot")
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Value > rows[j].Value })
		for _, r := range rows {
			if p := r.Labels["path"]; p != "" {
				c.hot[p] = true
				c.hotOrder = append(c.hotOrder, p)
			}
		}
		c.long = snap.Rows("shape.long_func.lines")
		sort.SliceStable(c.long, func(i, j int) bool { return c.long[i].Value > c.long[j].Value })
	}
	return c
}

// fileExists is a repo-relative path that is a file in the repo.
func (c *codeIndex) fileExists(p string) bool {
	if ok, seen := c.exists[p]; seen {
		return ok
	}
	ok := false
	if c.root != "" && !strings.Contains(p, "..") && !strings.HasPrefix(p, "/") {
		if fi, err := os.Stat(filepath.Join(c.root, filepath.FromSlash(p))); err == nil && fi.Mode().IsRegular() {
			ok = true
		}
	}
	c.exists[p] = ok
	return ok
}

// chipsFor finds the files the system owns, the ones its globs match (all
// the files its issues name when it has no globs), and returns its CODE
// row: the files its issues name most first, then hotspots.
func (c *codeIndex) chipsFor(system string, globs []string, members []*tracker.Issue) []WbCodeChip {
	named := map[string]int{}
	for _, issue := range members {
		seen := map[string]bool{}
		for _, m := range filePattern.FindAllString(issue.BodyRaw, -1) {
			p := strings.TrimPrefix(strings.TrimLeft(m, "/"), "./")
			if seen[p] || !c.fileExists(p) {
				continue
			}
			seen[p] = true
			named[p]++
		}
	}
	owned := map[string]bool{}
	if len(globs) > 0 {
		c.pathed = append(c.pathed, system)
		if !c.repoRead {
			c.repo, c.repoRead = tracker.RepoFiles(c.root), true
		}
		sys := tracker.WorkbenchSystem{Paths: globs}
		for _, p := range c.repo {
			if sys.Owns(p) {
				owned[p] = true
			}
		}
	} else {
		c.unpathed = append(c.unpathed, system)
		for p := range named {
			owned[p] = true
		}
	}
	c.owned[system] = owned
	paths := make([]string, 0, len(owned))
	for p := range owned {
		c.bySys[p] = append(c.bySys[p], system)
		paths = append(paths, p)
	}
	sort.Slice(paths, func(i, j int) bool {
		if named[paths[i]] != named[paths[j]] {
			return named[paths[i]] > named[paths[j]]
		}
		if c.hot[paths[i]] != c.hot[paths[j]] {
			return c.hot[paths[i]]
		}
		return paths[i] < paths[j]
	})
	var out []WbCodeChip
	for i, p := range paths {
		if i == systemsCodeChips {
			break
		}
		_, name := codePackage(p)
		out = append(out, WbCodeChip{Name: name, Path: p, Hot: c.hot[p]})
	}
	return out
}

// longFuncNote is the longest function in a file the system owns:
// "Plan() has 982 lines".
func (c *codeIndex) longFuncNote(system string) string {
	files := c.owned[system]
	for _, r := range c.long {
		if files[r.Labels["path"]] && r.Labels["func"] != "" {
			return fmt.Sprintf("%s() has %s lines", r.Labels["func"], chart.Format(chart.Count, r.Value))
		}
	}
	return ""
}

// codePackage splits a path into its package and the file within it: up to
// and including a src/ directory, else the directory (at most three deep).
func codePackage(p string) (pkg, name string) {
	segs := strings.Split(p, "/")
	for i, s := range segs[:len(segs)-1] {
		if s == "src" {
			return strings.Join(segs[:i+1], "/"), strings.Join(segs[i+1:], "/")
		}
	}
	dir := path.Dir(p)
	if dir == "." {
		return "(repo root)", p
	}
	if parts := strings.Split(dir, "/"); len(parts) > 3 {
		dir = strings.Join(parts[:3], "/")
	}
	return dir, strings.TrimPrefix(p, dir+"/")
}

// codeMap groups the owned and hot files by package: hot files first, then
// those several systems share. It is a draft while a system has no paths.
func (c *codeIndex) codeMap(hasSnap bool) WbCodeMap {
	m := WbCodeMap{Draft: true, Note: "Which packages belong to which systems, read from the files each system's issues name. Add paths: per system in workbench.systems to confirm it."}
	unowned := "no system's issues name it"
	switch {
	case len(c.pathed) > 0 && len(c.unpathed) == 0:
		m.Draft, m.Note = false, "Which packages belong to which systems, from paths: in workbench.systems."
		unowned = "no system's paths: cover it"
	case len(c.pathed) > 0:
		m.Note = fmt.Sprintf("%s: read from the files their issues name until they have paths: in workbench.systems. The others come from their paths.", joinAnd(c.unpathed))
		unowned = "no system's paths: or issues claim it"
	}
	if hasSnap {
		m.HotSource = "hot = a hotspot in the latest health snapshot (a big file changed in many commits)"
	} else {
		m.HotSource = "No health snapshot yet, so no file is marked hot (issue-cli health takes one)."
	}
	all := map[string]bool{}
	for p := range c.bySys {
		all[p] = true
	}
	for _, p := range c.hotOrder {
		all[p] = true
	}
	if len(all) == 0 {
		m.Empty = "No code map yet: no issue names a file in the repo and there is no health snapshot with hotspots."
		return m
	}
	m.Packages, m.More = c.packages(all, unowned)
	return m
}

// codeMapOf is the code map of one system: the files it owns, by package.
// It is a draft when the system has no paths:. Call it after chipsFor ran
// for every system, so shared files know their other owners.
func (c *codeIndex) codeMapOf(system string, hasPaths, hasSnap bool) WbCodeMap {
	m := WbCodeMap{Note: "The files under this system's paths: in workbench.systems, by package."}
	if !hasPaths {
		m.Draft = true
		m.Note = fmt.Sprintf("The files %s's issues name. Add paths: under workbench.systems.%s to confirm it.", system, system)
	}
	if hasSnap {
		m.HotSource = "hot = a hotspot in the latest health snapshot; shared = another system owns it too"
	} else {
		m.HotSource = "No health snapshot yet, so no file is marked hot (issue-cli health takes one)."
	}
	owned := c.owned[system]
	if len(owned) == 0 {
		m.Empty = fmt.Sprintf("No files yet: no %s issue names a file in the repo.", system)
		if hasPaths {
			m.Empty = "No files yet: no file in the repo matches its paths:."
		}
		return m
	}
	m.Packages, m.More = c.packages(owned, "")
	return m
}

// packages groups files by package for the code map: packages with hot
// files first, then the biggest; files hot first, then the most shared.
// unowned is the tooltip of a file no system owns. More counts the files
// in the packages past codeMapPackages.
func (c *codeIndex) packages(all map[string]bool, unowned string) (out []WbCodePackage, more int) {
	byPkg := map[string][]WbCodeFile{}
	for p := range all {
		pkg, name := codePackage(p)
		systems := append([]string(nil), c.bySys[p]...)
		sort.Slice(systems, func(i, j int) bool { return strings.ToLower(systems[i]) < strings.ToLower(systems[j]) })
		f := WbCodeFile{Name: name, Path: p, Hot: c.hot[p], Shared: len(systems) > 1, Systems: strings.Join(systems, ", ")}
		switch {
		case len(systems) == 0:
			f.Systems, f.SystemsTitle = "?", unowned
		case len(systems) > codeMapSystems:
			f.SystemsTitle, f.Systems = f.Systems, fmt.Sprintf("%d systems", len(systems))
		}
		byPkg[pkg] = append(byPkg[pkg], f)
	}
	pkgs := make([]string, 0, len(byPkg))
	for k := range byPkg {
		pkgs = append(pkgs, k)
	}
	// Packages with hot files first, then those with the most files.
	hotIn := func(k string) int {
		n := 0
		for _, f := range byPkg[k] {
			if f.Hot {
				n++
			}
		}
		return n
	}
	sort.Slice(pkgs, func(i, j int) bool {
		a, b := pkgs[i], pkgs[j]
		if hotIn(a) != hotIn(b) {
			return hotIn(a) > hotIn(b)
		}
		if len(byPkg[a]) != len(byPkg[b]) {
			return len(byPkg[a]) > len(byPkg[b])
		}
		return a < b
	})
	for i, k := range pkgs {
		files := byPkg[k]
		if i == codeMapPackages {
			for _, rest := range pkgs[i:] {
				more += len(byPkg[rest])
			}
			break
		}
		sort.Slice(files, func(a, b int) bool {
			fa, fb := files[a], files[b]
			if fa.Hot != fb.Hot {
				return fa.Hot
			}
			na, nb := len(c.bySys[fa.Path]), len(c.bySys[fb.Path])
			if na != nb {
				return na > nb
			}
			return fa.Name < fb.Name
		})
		pk := WbCodePackage{Name: k, Files: files}
		if len(files) > codeMapFiles {
			pk.Files, pk.More = files[:codeMapFiles], len(files)-codeMapFiles
		}
		out = append(out, pk)
	}
	return out, more
}

// sharedHot warns about hot files that several systems name: a change there
// moves several workshops at once.
func (c *codeIndex) sharedHot() *WbWarning {
	var files []string
	for _, p := range c.hotOrder {
		if len(c.bySys[p]) > 1 {
			files = append(files, path.Base(p))
		}
	}
	if len(files) == 0 {
		return nil
	}
	w := &WbWarning{Title: "Shared hot files."}
	if len(files) > sharedHotNamed {
		files = files[:sharedHotNamed]
	}
	w.Files = files
	verb := "belong"
	if len(files) == 1 {
		verb = "belongs"
	}
	w.Text = verb + " to several systems at once. A change there can move several workshops, so the project's gate should show it."
	return w
}

// notThere is "UI is" or "UI, Docs and Auth are" (capped at four names).
func notThere(names []string) string {
	if len(names) == 1 {
		return names[0] + " is"
	}
	if len(names) > 4 {
		return fmt.Sprintf("%s and %d more are", strings.Join(names[:4], ", "), len(names)-4)
	}
	return joinAnd(names) + " are"
}
