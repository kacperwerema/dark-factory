package main

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/agentrun"
	"github.com/michal-franc/issue-viewer/internal/routing"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The agents canvas (/p/<project>/agents) shows the human → project lead →
// worker hierarchy built from tmux and the issues' parent links, plus the
// jobs that have work in flight but no live agent.

// AgentIssue is the issue card an agent node links to.
type AgentIssue struct {
	Slug   string `json:"slug"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Color  string `json:"color"`
	URL    string `json:"url"`
}

// AgentNode is one lead or worker on the canvas.
type AgentNode struct {
	// Name is the bare session name (lead-x, agent-y).
	Name string `json:"name"`
	// Display is where it lives in tmux (shared:name in shared mode).
	Display string `json:"display"`
	// Kind is project-lead, department-lead, worker or other (an agent
	// session not bound to an issue, e.g. a project action).
	Kind string `json:"kind"`
	// Department is a department lead's department, or a worker's issue's
	// ("" for Product).
	Department string `json:"department,omitempty"`
	Live       bool   `json:"live"`
	// State is working, idle, at-gate, question or stopped for workers; live
	// or not-running for leads. Idle: live, but no issue-cli call for
	// agentIdleAfter.
	State string `json:"state"`
	// LastCall is the worker's newest issue-cli call (RFC 3339), LastCmd what
	// it ran; empty when its clilog can't be read.
	LastCall string `json:"last_call,omitempty"`
	LastCmd  string `json:"last_cmd,omitempty"`
	// Gate is the status awaiting the human's approval when State is at-gate.
	Gate          string      `json:"gate,omitempty"`
	Epic          string      `json:"epic,omitempty"`
	Issue         *AgentIssue `json:"issue,omitempty"`
	RetroNotSwept bool        `json:"retro_not_swept,omitempty"`
	AttachCmd     string      `json:"attach_cmd"`
	// SwitchCmd reaches the agent from inside tmux (tmux switch-client).
	SwitchCmd string `json:"switch_cmd"`
	StartTime string `json:"start_time,omitempty"`
	// Agent is the CLI the live session runs (claude, codex), read from
	// its pane's start command; "" when unknown.
	Agent string `json:"agent,omitempty"`
	// Limit is the usage-limit message its pane shows now, with the reset
	// ("You've hit your 5-hour limit · resets 5pm … — resets 17:00"); the
	// agent is left running.
	Limit string `json:"limit,omitempty"`
}

// SubscriptionView is one agent CLI's subscription on this machine.
type SubscriptionView struct {
	Agent     string `json:"agent"`
	Installed bool   `json:"installed"`
	// State is working, resting until HH:MM (…) or unknown.
	State   string `json:"state"`
	Resting bool   `json:"resting"`
	Live    int    `json:"live"`
}

// EpicGroup is one open epic under the project lead and how its children
// are doing.
type EpicGroup struct {
	Issue           AgentIssue `json:"issue"`
	ChildrenRunning int        `json:"children_running"`
	ChildrenTotal   int        `json:"children_total"`
}

// AgentsModel is the whole canvas, also served as /p/<project>/agents.json.
type AgentsModel struct {
	Project  string `json:"project"`
	Prefix   string `json:"prefix"`
	ListedAt string `json:"listed_at"`
	// TerminalNone is the project's terminal: none. Its owner works inside
	// tmux, so the panel's copy button copies switch_cmd instead of
	// attach_cmd.
	TerminalNone bool      `json:"terminal_none"`
	ProjectLead  AgentNode `json:"project_lead"`
	// DepartmentLeads report to the CEO, one per department, live or not;
	// workers name their department.
	DepartmentLeads []AgentNode `json:"department_leads"`
	Epics           []EpicGroup `json:"epics"`
	// Workers are the live workers; each names its epic, if any.
	Workers []AgentNode `json:"workers"`
	// Unfinished are issues with a worktree or an active status and no live
	// agent (the "stopped" workers).
	Unfinished []AgentNode `json:"unfinished"`
	// Others are live agent sessions not bound to an issue.
	Others []AgentNode `json:"others"`
	// Subscriptions are the agent CLIs' states (routing); the same words
	// issue-cli agents prints.
	Subscriptions []SubscriptionView `json:"subscriptions"`
}

func statusColorOf(status string) string {
	return funcMap["statusColor"].(func(string) string)(status)
}

func agentIssueOf(prefix string, issue *tracker.Issue) *AgentIssue {
	return &AgentIssue{
		Slug:   issue.Slug,
		Title:  issue.Title,
		Status: issue.Status,
		Color:  statusColorOf(issue.Status),
		URL:    prefix + "/issue/" + issue.Slug,
	}
}

// activeStatus reports whether status is one where work is in flight: from
// "in progress" (else the second status) up to, not including, "done" (else
// the last status).
func activeStatus(wf *tracker.WorkflowConfig, status string) bool {
	order := wf.GetStatusOrder()
	idx := wf.GetStatusIndex(status)
	if idx < 0 || len(order) < 3 {
		return false
	}
	start, end := 1, len(order)-1
	if i := wf.GetStatusIndex("in progress"); i >= 0 {
		start = i
	}
	if i := wf.GetStatusIndex("done"); i >= 0 {
		end = i
	}
	return idx >= start && idx < end
}

func (s *Server) buildAgentsModel(proj *tracker.Project, prefix string) (*AgentsModel, error) {
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		return nil, err
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].Slug < issues[j].Slug })
	workDir := resolveProjectWorkDir(proj)
	live := map[string]AgentSession{}
	for _, sess := range projectSessions(proj) {
		live[agentrun.BareName(sess.Name)] = sess
	}
	retros, _ := loadRetrospectives(proj)
	unswept := map[string]bool{}
	for _, r := range retros {
		if r.IssueSlug != "" && r.ReviewStatus != "processed" && !strings.Contains(filepath.ToSlash(r.FilePath), "/archive/") {
			unswept[strings.ToLower(r.IssueSlug)] = true
		}
	}

	now := time.Now()
	limits := map[string]agentrun.PaneLimit{}
	for _, l := range scanPaneLimits(proj, workDir, now) {
		limits[l.Session] = l
	}
	node := func(name, kind string) AgentNode {
		sess, ok := live[name]
		n := AgentNode{Name: name, Display: agentDisplayName(proj, name), Kind: kind, Live: ok, AttachCmd: agentAttachCmd(proj, name), SwitchCmd: agentrun.SwitchCmd(proj, name), StartTime: sess.StartTime}
		if ok {
			n.Agent = sessionAgent(proj, name)
			if l, hit := limits[name]; hit {
				n.Limit = l.Limit.Line + " — resets " + routing.RestUntilText(routing.Rest{Until: l.Limit.Until, KnownReset: l.Limit.KnownReset}, now)
				if n.Agent == "" {
					n.Agent = l.Limit.Agent
				}
			}
		}
		if kind == "project-lead" || kind == "department-lead" {
			n.State = "not-running"
			if ok {
				n.State = "live"
			}
		}
		return n
	}

	model := &AgentsModel{
		Project:      proj.Name,
		Prefix:       prefix,
		ListedAt:     time.Now().Format(time.RFC3339),
		TerminalNone: agentrun.TerminalNone(proj),
		ProjectLead:  node(tracker.LeadSessionName(proj), "project-lead"),
	}
	claimed := map[string]bool{model.ProjectLead.Name: true}
	model.DepartmentLeads = []AgentNode{}
	for _, d := range proj.Departments {
		n := node(tracker.DepartmentLeadName(proj, d.Name), "department-lead")
		n.Department = d.Name
		claimed[n.Name] = true
		model.DepartmentLeads = append(model.DepartmentLeads, n)
	}

	for _, issue := range issues {
		if issue.Status == "done" || !tracker.IsEpic(issue, issues) {
			continue
		}
		g := EpicGroup{Issue: *agentIssueOf(prefix, issue)}
		for _, child := range tracker.EpicChildren(issues, issue.Slug) {
			g.ChildrenTotal++
			if _, ok := live[tracker.AgentSessionName(child.Slug)]; ok {
				g.ChildrenRunning++
			}
		}
		model.Epics = append(model.Epics, g)
	}

	wfs := proj.WorkflowMemo()
	for _, issue := range issues {
		name := tracker.AgentSessionName(issue.Slug)
		claimed[name] = true
		n := node(name, "worker")
		n.Issue = agentIssueOf(prefix, issue)
		n.Department = proj.DepartmentOf(issue)
		if parent := tracker.FindIssue(issues, tracker.ParentSlug(issue)); parent != nil {
			n.Epic = parent.Slug
		} else if tracker.IsEpic(issue, issues) {
			n.Epic = issue.Slug
		}
		// A done issue without a live session is skipped below; skip it
		// before parsing its workflow (800 done issues took 1.4 s).
		if !n.Live && issue.Status == "done" {
			continue
		}
		wf := wfs.ForIssue(issue)
		wt, _, _ := resolveWorktree(workDir, issue.Slug, wf)
		if n.Live {
			comments, _ := tracker.LoadComments(issue.FilePath)
			call, known := lastCliCall(proj.Slug, agentLogPaths(workDir, wt, name, issue.Assignee)...)
			n.State, n.Gate = workerState(wf, issue, comments, call, known, time.Now())
			if known {
				n.LastCall, n.LastCmd = call.At.Format(time.RFC3339), call.Cmd
			}
			model.Workers = append(model.Workers, n)
			continue
		}
		if activeStatus(wf, issue.Status) || (wt != "" && fileExists(wt)) {
			n.State = "stopped"
			n.RetroNotSwept = unswept[strings.ToLower(issue.Slug)]
			if gate := pendingGate(wf, issue); gate != "" {
				n.Gate = gate
			}
			model.Unfinished = append(model.Unfinished, n)
		}
	}

	var others []string
	for name := range live {
		if !claimed[name] && (strings.HasPrefix(name, "agent-") || strings.HasPrefix(name, "lead-")) {
			others = append(others, name)
		}
	}
	sort.Strings(others)
	for _, name := range others {
		n := node(name, "other")
		n.State = "working"
		model.Others = append(model.Others, n)
	}
	model.Subscriptions = subscriptionViews(model, now)
	return model, nil
}

// scanPaneLimits and sessionAgent read tmux; vars so tests need none.
var (
	scanPaneLimits = agentrun.ScanLimits
	sessionAgent   = agentrun.SessionAgent
)

// subscriptionViews is each agent CLI's state for the Crew page, status.md
// and agents.json: resting (from the machine's subscription state), working
// when a live session of the model runs it, else unknown.
func subscriptionViews(m *AgentsModel, now time.Time) []SubscriptionView {
	live := map[string]int{}
	count := func(n AgentNode) {
		if n.Live && n.Agent != "" {
			live[n.Agent]++
		}
	}
	count(m.ProjectLead)
	for _, group := range [][]AgentNode{m.DepartmentLeads, m.Workers, m.Others} {
		for _, n := range group {
			count(n)
		}
	}
	st := agentrun.Subscriptions()
	var out []SubscriptionView
	for _, a := range routing.KnownAgents {
		_, resting := st.Resting(a, now)
		out = append(out, SubscriptionView{Agent: a, Installed: routing.Installed(a), Live: live[a], Resting: resting,
			State: routing.StateWords(a, st, live[a] > 0, now)})
	}
	return out
}

type AgentsPageData struct {
	Prefix      string
	ProjectName string
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	if err := s.tmpl.ExecuteTemplate(w, "agents.html", AgentsPageData{
		Prefix:      prefix,
		ProjectName: proj.Name,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleAgentsJSON(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	model, err := s.buildAgentsModel(proj, prefix)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(model)
}

// AgentDetail is the selected-agent panel: the issue's recent comments and
// the files its branch touches. The pane itself is seen by opening the
// session in a terminal (POST agents/open), not in the page.
type AgentDetail struct {
	Session  string            `json:"session"`
	Comments []tracker.Comment `json:"comments"`
	Branch   string            `json:"branch,omitempty"`
	Against  string            `json:"against,omitempty"`
	DiffStat string            `json:"diff_stat,omitempty"`
}

// branchDiffStat is `git diff --stat against...branch`. A var for tests.
var branchDiffStat = func(workdir, against, branch string) string {
	out, err := exec.Command("git", "-C", workdir, "diff", "--stat", against+"..."+branch).Output()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}

func validAgentName(name string) bool {
	if !strings.HasPrefix(name, "agent-") && !strings.HasPrefix(name, "lead-") {
		return false
	}
	return !strings.ContainsAny(name, " :;'\"`$\\")
}

func (s *Server) handleAgentDetail(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	name := r.URL.Query().Get("session")
	if !validAgentName(name) {
		http.Error(w, "session must be an agent-* or lead-* name", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.agentDetailOf(proj, name, r.URL.Query().Get("issue")))
}

// agentDetailOf is what the panel shows below an agent: its issue's last
// five comments and, for a worker, its branch against main and the diffstat
// (one git call). The Crew page's panel and agents/detail share it.
func (s *Server) agentDetailOf(proj *tracker.Project, name, slug string) AgentDetail {
	detail := AgentDetail{Session: name}
	if slug != "" {
		if issue := s.findIssueBySlug(proj, slug); issue != nil {
			comments, _ := tracker.LoadComments(issue.FilePath)
			if len(comments) > 5 {
				comments = comments[len(comments)-5:]
			}
			detail.Comments = comments
			workDir := resolveProjectWorkDir(proj)
			wf := proj.LoadWorkflowForIssue(issue)
			if !strings.HasPrefix(name, "lead-") {
				if wt := tracker.IssueWorktree(workDir, issue, wf); wt.Branch != "" {
					detail.Branch = wt.Branch
					detail.Against = mainBranch(workDir)
				}
			}
			if detail.Branch != "" && detail.Against != "" {
				detail.DiffStat = branchDiffStat(workDir, detail.Against, detail.Branch)
			}
		}
	}
	return detail
}

// mainBranch names the project's main branch. A var so tests don't run git.
var mainBranch = agentrun.MainBranch

// handleAgentOpen opens a live agent's or lead's tmux session in a terminal,
// the way a dispatch re-attach does: a terminal from the project's terminal
// setting, or in shared mode a select-window plus a terminal only when no
// client is attached. A session that already has a terminal gets no second
// window: its window is brought to the front instead (i3, by title), or the
// answer says it is already open. With terminal: none nothing opens: in
// shared mode the agent's window is selected (status "selected"), and the
// answer carries the switch and attach commands.
func (s *Server) handleAgentOpen(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	var body struct {
		Session string `json:"session"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !validAgentName(body.Session) {
		http.Error(w, `{"error":"session (agent-* or lead-*) required"}`, http.StatusBadRequest)
		return
	}
	resp := DispatchResponse{Status: "opened", Session: agentDisplayName(proj, body.Session), AttachCmd: agentAttachCmd(proj, body.Session), SwitchCmd: agentrun.SwitchCmd(proj, body.Session)}
	w.Header().Set("Content-Type", "application/json")
	if !agentSessionLive(proj, body.Session) {
		resp.Status = "no-session"
		resp.Steps = []DispatchStep{{Name: body.Session + " is not running", Status: "error"}}
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(resp)
		return
	}
	// The terminal shows the agent's own session, or in shared mode the
	// shared session switched to the agent's window.
	shared := agentrun.SharedSession(proj)
	// A lead follows lead_terminal: (headless when unset), workers terminal:.
	lead := strings.HasPrefix(body.Session, "lead-")
	headless := agentrun.TerminalNone(proj)
	if lead {
		headless = agentrun.LeadHeadless(proj)
	}
	if headless {
		// Nothing to open or focus: in shared mode select the agent's window
		// so whoever is in the shared session sees it, and hand back the
		// commands for reaching it from elsewhere.
		if shared != "" && agentrun.RunStep(&resp.Steps, "Select window "+body.Session+" in session "+shared,
			exec.Command("tmux", "select-window", "-t", agentTmuxTarget(proj, body.Session))) {
			resp.Status = "selected"
		}
		resp.Steps = append(resp.Steps, DispatchStep{Name: "Terminal: headless — attach manually", Status: "ok"})
		json.NewEncoder(w).Encode(resp)
		return
	}
	if lead {
		ok, shown := leadTerminal(proj, body.Session, &resp.Steps)
		if !ok {
			resp.Status = "error"
		} else if !shown {
			resp.Steps = append(resp.Steps, DispatchStep{Name: "Terminal: headless — attach manually", Status: "ok"})
		}
		json.NewEncoder(w).Encode(resp)
		return
	}
	home := body.Session
	if shared != "" {
		home = shared
	}
	if !agentrun.SessionAttached(home) {
		if !attachAgent(proj, body.Session, &resp.Steps) {
			resp.Status = "error"
		}
		json.NewEncoder(w).Encode(resp)
		return
	}
	if shared != "" {
		agentrun.RunStep(&resp.Steps, "Select window "+body.Session,
			exec.Command("tmux", "select-window", "-t", agentTmuxTarget(proj, body.Session)))
	}
	if agentrun.FocusTerminal(home) {
		resp.Status = "focused"
		resp.Steps = append(resp.Steps, DispatchStep{Name: "Focused the open terminal for " + home, Status: "ok"})
	} else {
		resp.Status = "already-open"
		resp.Steps = append(resp.Steps, DispatchStep{Name: home + " is already open in a terminal — no new window", Status: "ok"})
	}
	json.NewEncoder(w).Encode(resp)
}

// agentSessionLive and attachAgent are seams over tmux for tests.
var agentSessionLive = func(proj *tracker.Project, name string) bool {
	return agentrun.HasSession(agentTmuxTarget(proj, name))
}
var attachAgent = agentrun.AttachStep
var leadTerminal = agentrun.LeadTerminalStep

// startLead is a seam over agentrun for tests.
var startLead = agentrun.Start

// handleLeadSpawn starts the project lead, exactly like `issue-cli lead
// spawn`; with ?dept=<name> that department's lead, like `issue-cli lead
// spawn --department` (an unknown name is 404). A request naming an epic is
// refused: sub-leads were removed.
func (s *Server) handleLeadSpawn(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	var body struct {
		Epic  string `json:"epic"`
		Agent string `json:"agent"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if strings.TrimSpace(body.Epic) != "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(DispatchResponse{Status: "error", Steps: []DispatchStep{{Name: "Spawn CEO", Status: "error", Detail: "sub-leads were removed (one CEO per project)"}}})
		return
	}
	spec := agentrun.LeadSpec(proj, proj.LoadWorkflow(), projectRoot(proj), time.Now())
	if key := r.URL.Query().Get("dept"); key != "" {
		dept := proj.DepartmentByKey(key)
		if dept == nil {
			http.NotFound(w, r)
			return
		}
		wf, err := proj.WorkflowFor(dept.Name)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			json.NewEncoder(w).Encode(DispatchResponse{Status: "error", Steps: []DispatchStep{{Name: "Spawn " + dept.Name + " lead", Status: "error", Detail: err.Error()}}})
			return
		}
		spec = agentrun.DepartmentLeadSpec(proj, dept, wf, projectRoot(proj), time.Now())
	}
	explicit := strings.TrimSpace(body.Agent)
	if explicit != "" && refuseAgentType(w, explicit) {
		return
	}
	deptKey := ""
	if spec.LeadScope != tracker.LeadScopeProject {
		deptKey = strings.TrimPrefix(spec.LeadScope, "department:")
	}
	route := agentrun.RouteAgent(proj, spec.Workflow, projectRoot(proj), routing.Subject{Lead: true, Department: tracker.DepartmentKey(deptKey)}, explicit, time.Now())
	spec.AgentType, spec.Route = route.Agent, &route
	resp := startLead(proj, spec)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// agentsStatusLine is status.md's "Agents:" line: each installed agent CLI's
// subscription state ("Agents: claude working · codex resting until 18:30
// (limit seen in agent-x)"); "" when none is installed.
func agentsStatusLine(subs []SubscriptionView) string {
	var parts []string
	for _, sv := range subs {
		if sv.Installed || sv.Resting {
			parts = append(parts, sv.Agent+" "+sv.State)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "Agents: " + strings.Join(parts, " · ") + " (issue-cli agents)"
}
