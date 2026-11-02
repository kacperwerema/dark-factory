package viewer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/queue"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/viewerstate"
)

// The Crew page across projects (GET /crew, class R): every project's leads
// and workers with their state, the machine's memory and load at the top,
// and the owner's actions, each behind the owner gate (ownerOnlyRoute:
// crew/*, queue/*) and logged with who and when:
//
//	POST /p/<project>/crew/lead[?dept=]   start a lead (spawnLead)
//	POST /p/<project>/crew/stop           end one worker's tmux session
//	POST /p/<project>/crew/nudge          paste one line into a worker
//	GET  /p/<project>/crew/tail?session=  the pane's last lines
//	POST /p/<project>/queue/pause|resume  the owner's queue pause
//	(auto-start's pause and resume are the existing autostart/* routes)
//
// A worker mem-guard paused (a process in state T) shows as paused and is
// never stopped or nudged. Stop ends exactly one session (=name: target, or
// one window of a shared session); never the server, never the worktree.

const (
	// crewWorkersShown is how many workers a project's card lists before
	// "N more · open the project's Crew".
	crewWorkersShown = 8
	// crewNudgeMax caps a nudge's text.
	crewNudgeMax = 300
	// crewTailLines is how much of a pane Tail shows.
	crewTailLines = 60
	// crewLogShown is how many past actions the page lists.
	crewLogShown = 5
	// crewNudgePrefix tells the worker who speaks and from where.
	crewNudgePrefix = "Message from the owner (Crew page): "
)

// CrewAllData is the whole page.
type CrewAllData struct {
	Header   WbHeader
	Machine  CrewMachine
	SignedIn bool
	Projects []CrewProject
	Log      []CrewAction
	// Checked is when tmux was read (unix ms).
	Checked int64
}

// CrewMachine is the machine's chips: memory, load, mem-guard's pauses.
type CrewMachine struct {
	Memory, MemoryTone string
	Load, LoadTone     string
	Paused             int
}

// Chips are the machine's state as pills.
func (m CrewMachine) Chips() []WbChip {
	out := []WbChip{{Tone: m.MemoryTone, Text: m.Memory}, {Tone: m.LoadTone, Text: m.Load}}
	if m.Paused > 0 {
		out = append(out, WbChip{Tone: "paused", Text: fmt.Sprintf("mem-guard paused %d", m.Paused)})
	}
	return out
}

// CrewProject is one project's card.
type CrewProject struct {
	Name, Slug, Color, Prefix string
	// CrewURL is the project's own Crew page.
	CrewURL string
	Leads   []CrewRow
	Workers []CrewRow
	// More are live workers beyond those listed; Stopped the jobs with no
	// agent on them (shown on the project's Crew page).
	More, Stopped int
	AutoStart     *CrewSwitch
	Queue         *CrewSwitch
	Error         string
	// Act: the owner is signed in, so the buttons show.
	Act bool
}

// CrewSwitch is auto-start's or the queue's chip and its Pause / Resume.
type CrewSwitch struct {
	Key, State, Tone string
	// Action is "pause", "resume" or "" (no button); URL its route.
	Action, URL string
	// Act: the owner is signed in, so the button shows.
	Act bool
}

// Verb is the button's word.
func (c CrewSwitch) Verb() string {
	if c.Action == "pause" {
		return "Pause"
	}
	return "Resume"
}

// What is the confirm's words ("Pause auto-start", "Resume the queue").
func (c CrewSwitch) What() string {
	if c.Key == "queue" {
		return c.Verb() + " the queue"
	}
	return c.Verb() + " auto-start"
}

// Chip is the switch's state as a pill.
func (c CrewSwitch) Chip() WbChip { return WbChip{Tone: c.Tone, Text: c.State} }

// CrewRow is one lead or worker.
type CrewRow struct {
	Name string
	// Kind is lead or worker; Label names a lead ("CEO", "GTM lead").
	Kind, Label string
	Title       string
	IssueURL    string
	State, Tone string
	Note        string
	Live        bool
	Paused      bool
	// ChatURL is a live lead's chat; StartURL starts a lead that is not
	// running, StartWhat names it.
	ChatURL, StartURL, StartWhat string
	// Prefix is the project's, for the action routes.
	Prefix string
	// Act: the owner is signed in, so the buttons show.
	Act bool
}

// Chip is the row's state as a pill.
func (c CrewRow) Chip() WbChip { return WbChip{Tone: c.Tone, Text: c.State} }

// CanStop and CanNudge: a live worker that mem-guard has not paused.
func (c CrewRow) CanStop() bool  { return c.Kind == "worker" && c.Live && !c.Paused }
func (c CrewRow) CanNudge() bool { return c.CanStop() }

// CanTail is any live session.
func (c CrewRow) CanTail() bool { return c.Live }

// CrewAction is one logged owner action.
type CrewAction struct {
	At      time.Time `json:"at"`
	Who     string    `json:"who"`
	Project string    `json:"project"`
	Action  string    `json:"action"`
	Target  string    `json:"target,omitempty"`
	OK      bool      `json:"ok"`
	Detail  string    `json:"detail,omitempty"`
}

// When is the action's time on the page.
func (a CrewAction) When() string { return a.At.Local().Format("Mon 15:04") }

// Line is the action in words.
func (a CrewAction) Line() string {
	s := a.Action
	if a.Target != "" {
		s += " " + a.Target
	}
	if !a.OK {
		s += " (refused"
		if a.Detail != "" {
			s += ": " + a.Detail
		}
		s += ")"
	}
	return s
}

// crewSnapshot reads panes and processes once per page; a var so tests can
// pin it.
var crewSnapshot = agentrun.TakeSnapshot

func (s *Server) handleCrewAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	d := s.buildCrewAll(time.Now())
	d.SignedIn = s.fromOwner(r)
	for i := range d.Projects {
		d.Projects[i].Act = d.SignedIn
		for _, sw := range []*CrewSwitch{d.Projects[i].AutoStart, d.Projects[i].Queue} {
			if sw != nil {
				sw.Act = d.SignedIn
			}
		}
		for j := range d.Projects[i].Leads {
			d.Projects[i].Leads[j].Act = d.SignedIn
		}
		for j := range d.Projects[i].Workers {
			d.Projects[i].Workers[j].Act = d.SignedIn
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "crew_all.html", d); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// buildCrewAll builds every project's card in parallel (one slow project
// doesn't hold the others) over one pane and process snapshot.
func (s *Server) buildCrewAll(now time.Time) CrewAllData {
	snap := crewSnapshot()
	d := CrewAllData{Checked: now.UnixMilli(), Machine: crewMachine()}
	d.Projects = make([]CrewProject, len(s.projects))
	waiting := make([]int, len(s.projects))
	var wg sync.WaitGroup
	for i := range s.projects {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := &s.projects[i]
			d.Projects[i] = s.crewProject(p, snap, now)
			waiting[i] = len(waitingOnOwner(p, openIssuesOf(p)))
		}(i)
	}
	wg.Wait()
	total := 0
	for i, p := range d.Projects {
		total += waiting[i]
		for _, row := range append(append([]CrewRow(nil), p.Leads...), p.Workers...) {
			if row.Paused {
				d.Machine.Paused++
			}
		}
	}
	d.Header = WbHeader{Crumbs: []WbCrumb{{Label: "Map", URL: "/"}, {Label: "Crew", Current: true}}, Waiting: total, WaitingURL: "/needs-you", Quiet: true}
	d.Log = crewLog(crewLogShown)
	return d
}

// crewMachine is the memory and load chips, from mem-guard and the load
// average (the readings auto-start uses).
func crewMachine() CrewMachine {
	m := CrewMachine{Memory: "memory unknown (no mem-guard)", MemoryTone: "idle"}
	if mem := autostart.ReadMemory(); mem.Found {
		m.Memory, m.MemoryTone = fmt.Sprintf("memory %d%% · can start", mem.Used), "green"
		if !mem.CanStart {
			m.Memory, m.MemoryTone = fmt.Sprintf("memory %d%% · start blocked", mem.Used), "sand"
		}
	}
	cpu := autostart.ReadLoad()
	m.Load, m.LoadTone = "load unknown", "idle"
	if cpu.Found {
		m.Load, m.LoadTone = fmt.Sprintf("load %s on %d cores", autostart.FormatLoad(cpu.Avg1), cpu.Cores), "green"
		if cpu.Avg1 >= float64(cpu.Cores) {
			m.LoadTone = "sand"
		}
	}
	return m
}

// crewProject is one project's card from its agents model.
func (s *Server) crewProject(p *tracker.Project, snap *agentrun.Snapshot, now time.Time) CrewProject {
	prefix := "/p/" + p.Slug
	c := CrewProject{Name: p.Name, Slug: p.Slug, Color: s.projectColor(p), Prefix: prefix, CrewURL: prefix + "/agents"}
	m, err := s.buildAgentsModel(p, prefix)
	if err != nil {
		c.Error = "could not read its agents: " + err.Error()
		return c
	}
	lead := func(n AgentNode, label, deptKey string) CrewRow {
		r := CrewRow{Name: n.Name, Kind: "lead", Label: label, Live: n.Live, Prefix: prefix, State: "live", Tone: "green"}
		if n.Live {
			r.ChatURL = prefix + "/lead"
			if deptKey != "" {
				r.ChatURL += "?dept=" + url.QueryEscape(deptKey)
			}
			if snap.Paused(p, n.Name) {
				r.Paused, r.State, r.Tone = true, "paused by mem-guard", "paused"
			}
		} else {
			r.State, r.Tone = "not running", "idle"
			r.StartURL = prefix + "/crew/lead"
			r.StartWhat = "the CEO"
			if deptKey != "" {
				r.StartURL += "?dept=" + url.QueryEscape(deptKey)
				r.StartWhat = "the " + label
			}
		}
		return r
	}
	c.Leads = append(c.Leads, lead(m.ProjectLead, "CEO", ""))
	for _, dl := range m.DepartmentLeads {
		c.Leads = append(c.Leads, lead(dl, dl.Department+" lead", dl.Department))
	}
	var workers []CrewRow
	for _, n := range m.Workers {
		card := crewCard(n, "worker", prefix, now)
		r := CrewRow{Name: n.Name, Kind: "worker", Live: true, Prefix: prefix, State: card.State, Tone: card.Tone, Note: card.LastCall}
		if n.Issue != nil {
			r.Title, r.IssueURL = n.Issue.Title, n.Issue.URL
		}
		if snap.Paused(p, n.Name) {
			r.Paused, r.State, r.Tone, r.Note = true, "paused by mem-guard", "paused", "resumes by itself when memory falls"
		}
		workers = append(workers, r)
	}
	rank := func(r CrewRow) int {
		switch {
		case r.Paused:
			return 4
		case r.Tone == "you":
			return 0
		case r.Tone == "green":
			return 1
		case r.Tone == "sand":
			return 2
		}
		return 3
	}
	sort.SliceStable(workers, func(a, b int) bool { return rank(workers[a]) < rank(workers[b]) })
	if len(workers) > crewWorkersShown {
		c.More = len(workers) - crewWorkersShown
		workers = workers[:crewWorkersShown]
	}
	c.Workers = workers
	c.Stopped = len(m.Unfinished)

	issues := openIssuesOf(p)
	if a := autostartView(p, issues, prefix, now); a != nil {
		c.AutoStart = &CrewSwitch{Key: "autostart", State: "on", Tone: "green", Action: "pause", URL: prefix + "/autostart/pause"}
		if a.Paused {
			c.AutoStart.State, c.AutoStart.Tone, c.AutoStart.Action, c.AutoStart.URL = "paused", "sand", "resume", prefix+"/autostart/resume"
		}
	} else {
		c.AutoStart = &CrewSwitch{Key: "autostart", State: "off", Tone: "idle"}
	}
	if queue.Exists(p.IssueDir) {
		c.Queue = &CrewSwitch{Key: "queue", Tone: "idle", Action: "pause", URL: prefix + "/queue/pause"}
		if q, err := queue.Load(p.IssueDir); err != nil {
			c.Queue.State, c.Queue.Action, c.Queue.URL = "can't read", "", ""
		} else if q.Paused {
			c.Queue.State, c.Queue.Tone, c.Queue.Action, c.Queue.URL = "paused", "sand", "resume", prefix+"/queue/resume"
		} else {
			c.Queue.State = fmt.Sprintf("%d open", len(q.ActiveItems()))
		}
	}
	return c
}

// crewWho is who acted: the verified identity on a public name, else the
// owner's signed-in browser on loopback.
func crewWho(r *http.Request) string {
	if id := verifiedIdentity(r); id != "" {
		return id
	}
	return "owner (signed-in browser)"
}

// crewLogPath is the machine's action log, outside every repository.
func crewLogPath() string {
	d := viewerstate.Dir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "crew", "actions.jsonl")
}

var crewLogMu sync.Mutex

// crewRecord appends one action to the log, one write per line.
func crewRecord(a CrewAction) {
	path := crewLogPath()
	if path == "" {
		return
	}
	line, err := json.Marshal(a)
	if err != nil {
		return
	}
	crewLogMu.Lock()
	defer crewLogMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		log.Printf("crew log: %v", err)
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		log.Printf("crew log: %v", err)
		return
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		log.Printf("crew log: %v", err)
	}
}

// crewLog is the last n actions, newest first.
func crewLog(n int) []CrewAction {
	path := crewLogPath()
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var all []CrewAction
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var a CrewAction
		if json.Unmarshal(sc.Bytes(), &a) == nil {
			all = append(all, a)
		}
	}
	var out []CrewAction
	for i := len(all) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, all[i])
	}
	return out
}

// crewResult answers an action as JSON and logs it.
func crewResult(w http.ResponseWriter, r *http.Request, proj *tracker.Project, action, target string, code int, msg string) {
	ok := code < 300
	a := CrewAction{At: time.Now(), Who: crewWho(r), Project: proj.Slug, Action: action, Target: target, OK: ok}
	if !ok {
		a.Detail = msg
	}
	crewRecord(a)
	out := map[string]any{"ok": ok, "message": msg, "at": a.At.Local().Format("15:04")}
	writeJSONStatus(w, code, out)
}

// crewWorker finds the live worker called name in proj's agents model;
// any other name (another project's, a lead, a non-agent session) is nil.
func (s *Server) crewWorker(proj *tracker.Project, name string, leadsToo bool) (*AgentNode, error) {
	if !validAgentName(name) {
		return nil, nil
	}
	m, err := s.buildAgentsModel(proj, "/p/"+proj.Slug)
	if err != nil {
		return nil, err
	}
	for i := range m.Workers {
		if m.Workers[i].Name == name {
			return &m.Workers[i], nil
		}
	}
	if leadsToo {
		for _, n := range append([]AgentNode{m.ProjectLead}, m.DepartmentLeads...) {
			if n.Name == name && n.Live {
				n := n
				return &n, nil
			}
		}
	}
	return nil, nil
}

// crewKill ends one session; a var so a test can watch the call.
var crewKill = agentrun.KillTmux

// handleCrewAction serves the crew/* routes (the owner gate ran before).
func (s *Server) handleCrewAction(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix, action string) {
	var body struct {
		Session string `json:"session"`
		Text    string `json:"text"`
	}
	if r.Method == http.MethodPost {
		_ = json.NewDecoder(r.Body).Decode(&body)
	} else {
		body.Session = r.URL.Query().Get("session")
	}
	switch action {
	case "lead":
		s.crewStartLead(w, r, proj)
	case "stop", "nudge":
		node, err := s.crewWorker(proj, body.Session, false)
		if err != nil {
			crewResult(w, r, proj, action, body.Session, http.StatusInternalServerError, err.Error())
			return
		}
		if node == nil {
			crewResult(w, r, proj, action, body.Session, http.StatusNotFound, "no live worker called "+body.Session+" in "+proj.Name)
			return
		}
		if crewSnapshot().Paused(proj, node.Name) {
			crewResult(w, r, proj, action, node.Name, http.StatusConflict, node.Name+" is paused by mem-guard: it resumes when memory falls; try again then")
			return
		}
		if action == "stop" {
			s.crewStop(w, r, proj, node)
		} else {
			s.crewNudge(w, r, proj, node, body.Text)
		}
	case "tail":
		node, err := s.crewWorker(proj, body.Session, true)
		if err != nil || node == nil {
			http.Error(w, "no live agent called "+body.Session+" in "+proj.Name, http.StatusNotFound)
			return
		}
		out, err := agentrun.CapturePane(agentrun.Target(proj, node.Name))
		if err != nil {
			http.Error(w, "could not read the pane: "+err.Error(), http.StatusBadGateway)
			return
		}
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if len(lines) > crewTailLines {
			lines = lines[len(lines)-crewTailLines:]
		}
		writeJSONStatus(w, http.StatusOK, map[string]any{"session": node.Name, "lines": lines, "at": time.Now().Format("15:04:05")})
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) crewStartLead(w http.ResponseWriter, r *http.Request, proj *tracker.Project) {
	dept := r.URL.Query().Get("dept")
	name := tracker.LeadSessionName(proj)
	if dept != "" {
		d := proj.DepartmentByKey(dept)
		if d == nil {
			crewResult(w, r, proj, "start lead", dept, http.StatusNotFound, "no department "+dept+" in "+proj.Name)
			return
		}
		name = tracker.DepartmentLeadName(proj, d.Name)
	}
	if agentrun.IsLive(proj, name) {
		crewResult(w, r, proj, "start lead", name, http.StatusConflict, name+" is already running")
		return
	}
	resp, code := spawnLead(proj, dept, "")
	if code != http.StatusOK || resp.Status == "error" {
		msg := "could not start " + name
		for _, st := range resp.Steps {
			if st.Status == "error" {
				msg += ": " + st.Name
				if st.Detail != "" {
					msg += " (" + st.Detail + ")"
				}
				break
			}
		}
		if code == http.StatusOK {
			code = http.StatusInternalServerError
		}
		crewResult(w, r, proj, "start lead", name, code, msg)
		return
	}
	crewResult(w, r, proj, "started", name, http.StatusOK, "Started "+name)
}

func (s *Server) crewStop(w http.ResponseWriter, r *http.Request, proj *tracker.Project, node *AgentNode) {
	if err := crewKill(proj, node.Name); err != nil {
		crewResult(w, r, proj, "stop", node.Name, http.StatusInternalServerError, "tmux could not end "+node.Name+": "+err.Error())
		return
	}
	if node.Issue != nil {
		if issue := s.findIssueBySlug(proj, node.Issue.Slug); issue != nil {
			text := "stopped: the owner stopped " + node.Name + " from the Crew page (its tmux session ended; worktree and branch kept). Start it again with issue-cli dispatch " + issue.Slug
			if err := tracker.AddCommentBy(issue.FilePath, 0, text, "app", tracker.OwnerAuthor); err != nil {
				log.Printf("crew stop: comment on %s: %v", issue.Slug, err)
			}
		}
	}
	crewResult(w, r, proj, "stopped", node.Name, http.StatusOK, "Stopped "+node.Name+" · worktree kept")
}

func (s *Server) crewNudge(w http.ResponseWriter, r *http.Request, proj *tracker.Project, node *AgentNode, text string) {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		crewResult(w, r, proj, "nudge", node.Name, http.StatusBadRequest, "write a message first")
		return
	}
	if n := len([]rune(text)); n > crewNudgeMax {
		crewResult(w, r, proj, "nudge", node.Name, http.StatusBadRequest, fmt.Sprintf("a nudge is at most %d characters (this one has %d)", crewNudgeMax, n))
		return
	}
	if err := crewNudgeSend(agentrun.Target(proj, node.Name), crewNudgePrefix+text); err != nil {
		crewResult(w, r, proj, "nudge", node.Name, http.StatusBadGateway, err.Error())
		return
	}
	crewResult(w, r, proj, "nudged", node.Name, http.StatusOK, "Nudge delivered to "+node.Name)
}

// crewNudgeSend delivers a nudge; a var so tests skip the delivery wait.
var crewNudgeSend = agentrun.Nudge

// handleQueuePause serves queue/pause and queue/resume.
func (s *Server) handleQueuePause(w http.ResponseWriter, r *http.Request, proj *tracker.Project, action string) {
	if action != "pause" && action != "resume" {
		http.NotFound(w, r)
		return
	}
	if !queue.Exists(proj.IssueDir) {
		crewResult(w, r, proj, action+" queue", "", http.StatusNotFound, proj.Name+" has no queue")
		return
	}
	now := time.Now()
	if err := queue.SetPaused(proj.IssueDir, action == "pause", "the owner (Crew page)", now); err != nil {
		crewResult(w, r, proj, action+" queue", "", http.StatusInternalServerError, err.Error())
		return
	}
	word := "Paused"
	if action == "resume" {
		word = "Resumed"
	}
	crewResult(w, r, proj, strings.ToLower(word)+" queue", "", http.StatusOK, word+" the queue")
}
