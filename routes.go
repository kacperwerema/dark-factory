package main

import (
	"context"
	"embed"
	"encoding/json"
	"github.com/michal-franc/issue-viewer/internal/agentrun"
	"html/template"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

type Server struct {
	projects []tracker.Project
	tmpl     *template.Template
	// allowedHosts are the Host names past loopback the viewer answers to
	// (allowedHostsFor); Routes refuses the rest with 421.
	allowedHosts []string
	// identity verifies public requests (identity.go); nil refuses them all.
	identity *identityChecker
	// bg runs work that outlives a request; run swaps in one tied to the
	// signal context so shutdown can wait for it.
	bg *background
	// stats caches the stats page's aggregate and CLI usage per project.
	stats *statsCache
	// engine is the engine project's slug (where report-bug files), whose
	// Status page shows a release candidate's soak; "" for none.
	engine string
	// telemetryData is projects.yaml's telemetry_data: the collector store
	// the /telemetry page reads; "" when not configured.
	telemetryData string
	// owner holds the owner's sign-in sessions (owner.go): the credential
	// every O route of docs/API/security.md needs on loopback.
	owner *ownerAuth
}

// AgentSession is one live tmux session (or shared-session window).
type AgentSession = agentrun.Session

type approvalResponse struct {
	Status              string `json:"status"`
	HumanApproval       string `json:"human_approval"`
	QueuedApproval      string `json:"queued_approval,omitempty"`
	NotifiedSession     string `json:"notified_session,omitempty"`
	NotificationMessage string `json:"notification_message,omitempty"`
	NotificationError   string `json:"notification_error,omitempty"`
	Label               string `json:"label,omitempty"`
	WordsSaved          bool   `json:"words_saved,omitempty"`
	WordsError          string `json:"words_error,omitempty"`
}

func NewServer(projects []tracker.Project) (*Server, error) {
	tmpl, err := template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}

	s := &Server{
		projects: projects,
		tmpl:     tmpl,
		bg:       newBackground(context.Background()),
		stats:    newStatsCache(),
		engine:   engineSlug(&tracker.ProjectsConfig{Projects: projects}),
		owner:    newOwnerAuth(),
	}
	tmpl.Funcs(template.FuncMap{"sidebar": s.sidebarNav})
	return s, nil
}

func (s *Server) findProject(slug string) *tracker.Project {
	for i := range s.projects {
		if s.projects[i].Slug == slug {
			return &s.projects[i]
		}
	}
	return nil
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleProjectList)
	mux.HandleFunc("/p/", s.handleProjectRoutes)
	mux.HandleFunc("/stats", s.handleStatsOverview)
	mux.HandleFunc("/telemetry", s.handleTelemetry)
	mux.HandleFunc("/updates", s.handleUpdatesOverview)
	mux.HandleFunc("/leads", s.handleLeads)
	mux.HandleFunc("/ceos", redirectTo("/leads"))
	mux.HandleFunc("/needs-you", s.handleNeedsYouAll)
	mux.HandleFunc("/project-for", s.handleProjectFor)
	mux.HandleFunc("/owner", s.handleOwner)
	mux.HandleFunc("/owner/", s.handleOwner)
	mux.Handle("/static/", http.FileServer(http.FS(staticFS)))
	// Outside in: headers on every answer, Host allowlist (DNS rebinding),
	// the identity gate for public requests, cross-origin writes refused
	// (docs/API/security.md).
	return securityHeaders(hostAllowed(s.allowedHosts, identityGate(s.identity, sameOriginOnly(mux))))
}

// redirectTo answers a GET with a redirect to path, keeping the query: the
// CEO's pages live at /lead and /leads, and /ceo and /ceos lead there.
func redirectTo(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		to := path
		if r.URL.RawQuery != "" {
			to += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, to, http.StatusFound)
	}
}

// handleProjectRoutes dispatches /p/<slug>/... to the right handler
func (s *Server) handleProjectRoutes(w http.ResponseWriter, r *http.Request) {
	// Parse /p/<slug>/...
	path := strings.TrimPrefix(r.URL.Path, "/p/")
	parts := strings.SplitN(path, "/", 2)
	projectSlug := parts[0]
	rest := ""
	if len(parts) > 1 {
		rest = parts[1]
	}

	proj := s.findProject(projectSlug)
	if proj == nil {
		http.NotFound(w, r)
		return
	}

	prefix := "/p/" + projectSlug

	// Owner decisions need the owner's credential, not a header any local
	// client can send (owner.go, docs/API/security.md).
	if ownerOnlyRoute(r.Method, rest) && !s.fromOwner(r) {
		refuseNonOwner(w, r, rest)
		return
	}

	switch {
	case rest == "" || rest == "/":
		if r.URL.Query().Get("classic") == "1" {
			s.handleList(w, r, proj, prefix)
		} else {
			s.handleIssuesWb(w, r, proj, prefix)
		}
	case rest == "hall" && r.Method == http.MethodGet:
		s.handleHall(w, r, proj, prefix)
	case rest == "hall/ci" && r.Method == http.MethodGet:
		s.handleHallPart(w, r, proj, prefix, "ci", "")
	case strings.HasPrefix(rest, "hall/station/") && strings.HasSuffix(rest, "/edit") && r.Method == http.MethodGet:
		s.handleHallEdit(w, r, proj, prefix, strings.TrimSuffix(strings.TrimPrefix(rest, "hall/station/"), "/edit"), "")
	case strings.HasPrefix(rest, "hall/station/") && (strings.HasSuffix(rest, "/edit/preview") || strings.HasSuffix(rest, "/edit/save")) && r.Method == http.MethodPost:
		key, action, _ := strings.Cut(strings.TrimPrefix(rest, "hall/station/"), "/edit/")
		s.handleHallEdit(w, r, proj, prefix, key, action)
	case (strings.HasPrefix(rest, "hall/station/") || strings.HasPrefix(rest, "hall/gate/")) && r.Method == http.MethodGet:
		kind, key, _ := strings.Cut(strings.TrimPrefix(rest, "hall/"), "/")
		s.handleHallPart(w, r, proj, prefix, kind, key)
	case rest == "company" && r.Method == http.MethodGet:
		s.handleCompanyFloor(w, r, proj, prefix)
	case rest == "hq/report" && r.Method == http.MethodGet:
		s.handleHQReport(w, r, proj, prefix)
	case rest == "hq/report.md" && r.Method == http.MethodGet:
		s.handleHQReportMD(w, r, proj, prefix)
	case rest == "needs-you" && r.Method == http.MethodGet:
		s.handleNeedsYou(w, r, proj, prefix)
	case rest == "needs-you.json" && r.Method == http.MethodGet:
		s.handleNeedsYouJSON(w, r, proj, prefix)
	case rest == "needs-you/approve" && r.Method == http.MethodPost:
		s.handleNeedsYouApprove(w, r, proj, prefix)
	case rest == "memory" && r.Method == http.MethodGet:
		s.handleMemory(w, r, proj, prefix)
	case rest == "memory.json" && r.Method == http.MethodGet:
		s.handleMemoryJSON(w, r, proj)
	case (rest == "memory/edit" || rest == "memory/delete" || rest == "memory/pin" || rest == "memory/unpin") && r.Method == http.MethodPost:
		s.handleMemoryWrite(w, r, proj, prefix, strings.TrimPrefix(rest, "memory/"))
	case strings.HasPrefix(rest, "postmortem/") && strings.HasSuffix(rest, "/decide") && r.Method == http.MethodPost:
		s.handlePostmortemDecide(w, r, proj, prefix, strings.TrimSuffix(strings.TrimPrefix(rest, "postmortem/"), "/decide"))
	case strings.HasPrefix(rest, "postmortem/") && r.Method == http.MethodGet:
		s.handlePostmortem(w, r, proj, prefix, strings.TrimPrefix(rest, "postmortem/"))
	case rest == "owner-checks" && r.Method == http.MethodGet:
		s.handleOwnerChecks(w, r, proj, prefix)
	case rest == "owner-checks/tick" && r.Method == http.MethodPost:
		s.handleOwnerCheckTick(w, r, proj, prefix)
	case rest == "connectors/check" && r.Method == http.MethodPost:
		s.handleConnectorsCheck(w, r, proj, prefix)
	case rest == "connectors.json" && r.Method == http.MethodGet:
		s.handleConnectorsJSON(w, r, proj)
	case rest == "connections" && r.Method == http.MethodGet:
		s.handleConnections(w, r, proj, prefix)
	case rest == "connections.json" && r.Method == http.MethodGet:
		s.handleConnectionsJSON(w, r, proj)
	case strings.HasPrefix(rest, "reports/") && r.Method == http.MethodGet:
		s.handleWeekReport(w, r, proj, prefix, strings.TrimPrefix(rest, "reports/"))
	case rest == "shelf" && r.Method == http.MethodGet:
		s.handleShelf(w, r, proj, prefix)
	case rest == "shelf/review" && r.Method == http.MethodPost:
		s.handleShelfReview(w, r, proj, prefix)
	case strings.HasPrefix(rest, "shelf/read/") && r.Method == http.MethodGet:
		s.handleShelfRead(w, r, proj, prefix, strings.TrimPrefix(rest, "shelf/read/"))
	case rest == "shelf/comment" && r.Method == http.MethodPost:
		s.handleShelfComment(w, r, proj, prefix)
	case rest == "shelf/comment/resolve" && r.Method == http.MethodPost:
		s.handleShelfCommentResolve(w, r, proj, prefix)
	case strings.HasPrefix(rest, "shelf/file/") && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		s.handleShelfFile(w, r, proj, strings.TrimPrefix(rest, "shelf/file/"))
	case (rest == "board" || rest == "workboard") && r.URL.Query().Get("classic") != "1":
		// The hall is the board now; the old pages answer at ?classic=1
		// (factory/retire-board-canvas-graph#D-1).
		http.Redirect(w, r, prefix+"/hall", http.StatusFound)
	case rest == "board":
		s.handleBoard(w, r, proj, prefix)
	case rest == "graph":
		s.handleGraph(w, r, proj, prefix)
	case rest == "workboard" && r.Method == http.MethodGet:
		s.handleWorkboard(w, r, proj, prefix)
	case rest == "canvas":
		s.handleCanvas(w, r, proj, prefix)
	case strings.HasPrefix(rest, "ci/log/") && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		s.handleCILog(w, r, proj, strings.TrimPrefix(rest, "ci/log/"))
	case rest == "status" && r.Method == http.MethodGet:
		s.handleStatus(w, r, proj, prefix)
	case rest == "status.md" && r.Method == http.MethodGet:
		s.handleStatusMD(w, r, proj, prefix)
	case rest == "status.json" && r.Method == http.MethodGet:
		s.handleStatusJSON(w, r, proj, prefix)
	case rest == "release.json" && r.Method == http.MethodGet:
		s.handleReleaseJSON(w, r, proj, prefix)
	case strings.HasPrefix(rest, "pages/") && r.Method == http.MethodGet:
		s.handleProjectPage(w, r, proj, prefix, rest)
	case strings.HasPrefix(rest, "page-raw/") && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		s.handlePageRaw(w, r, proj, prefix)
	case rest == "github" && r.Method == http.MethodGet:
		s.handleGitHub(w, r, proj, prefix)
	case rest == "github/fetch" && r.Method == http.MethodPost:
		s.handleGitHubFetch(w, r, proj)
	case rest == "github/import" && r.Method == http.MethodPost:
		s.handleGitHubImport(w, r, proj)
	case rest == "stats":
		s.handleStats(w, r, proj, prefix)
	case rest == "health" && r.Method == http.MethodGet:
		s.handleHealth(w, r, proj, prefix)
	case rest == "health/review" && r.Method == http.MethodPost:
		s.handleHealthReview(w, r, proj, prefix)
	case strings.HasPrefix(rest, "health/recommendation/") && strings.HasSuffix(rest, "/file") && r.Method == http.MethodPost:
		s.handleHealthFile(w, r, proj, prefix, rest)
	case strings.HasPrefix(rest, "health/recommendation/") && strings.HasSuffix(rest, "/dismiss") && r.Method == http.MethodPost:
		s.handleHealthDismiss(w, r, proj, prefix, rest)
	case rest == "agents" && r.Method == http.MethodGet:
		if r.URL.Query().Get("classic") == "1" {
			s.handleAgents(w, r, proj, prefix)
		} else {
			s.handleCrew(w, r, proj, prefix)
		}
	case rest == "agents.json" && r.Method == http.MethodGet:
		s.handleAgentsJSON(w, r, proj, prefix)
	case rest == "agents/detail" && r.Method == http.MethodGet:
		s.handleAgentDetail(w, r, proj, prefix)
	case rest == "agents/open" && r.Method == http.MethodPost:
		s.handleAgentOpen(w, r, proj, prefix)
	case rest == "lead/spawn" && r.Method == http.MethodPost:
		s.handleLeadSpawn(w, r, proj, prefix)
	case rest == "lead" && r.Method == http.MethodGet:
		s.handleCEOChat(w, r, proj, prefix)
	case rest == "ceo" && r.Method == http.MethodGet:
		redirectTo(prefix+"/lead")(w, r)
	case rest == "lead.json" && r.Method == http.MethodGet:
		s.handleLeadChatJSON(w, r, proj, prefix)
	case rest == "lead/send" && r.Method == http.MethodPost:
		s.handleLeadChatSend(w, r, proj, prefix)
	case rest == "lead/image" && r.Method == http.MethodPost:
		s.handleCEOImage(w, r, proj, prefix)
	case rest == "lead/file" && r.Method == http.MethodPost:
		s.handleCEOFile(w, r, proj, prefix)
	case strings.HasPrefix(rest, "lead/file/") && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		s.handleCEOFileGet(w, r, proj, rest)
	case rest == "lead/key" && r.Method == http.MethodPost:
		s.handleLeadChatKey(w, r, proj, prefix)
	case rest == "lead/answer" && r.Method == http.MethodPost:
		s.handleLeadAnswer(w, r, proj, prefix)
	case rest == "retros":
		s.handleRetros(w, r, proj, prefix)
	case rest == "retros/review" && r.Method == http.MethodPost:
		s.handleRetrosReviewDispatch(w, r, proj)
	case strings.HasPrefix(rest, "retros/retro/") && strings.HasSuffix(rest, "/status") && r.Method == http.MethodPost:
		s.handleUpdateRetroStatus(w, r, proj, prefix)
	case strings.HasPrefix(rest, "retros/bug/") && strings.HasSuffix(rest, "/status") && r.Method == http.MethodPost:
		s.handleUpdateBugStatus(w, r, proj, prefix)
	case rest == "decisions" && r.Method == http.MethodGet:
		if r.URL.Query().Get("classic") == "1" {
			s.handleDecisions(w, r, proj, prefix)
		} else {
			s.handleDecisionsWb(w, r, proj, prefix)
		}
	case strings.HasPrefix(rest, "decisions/") && strings.HasSuffix(rest, "/confirm") && r.Method == http.MethodPost:
		s.handleDecisionConfirm(w, r, proj, rest)
	case rest == "updates" && r.Method == http.MethodGet:
		s.handleUpdates(w, r, proj, prefix)
	case (rest == "updates/decline" || rest == "updates/reopen") && r.Method == http.MethodPost:
		s.handleUpdatesRecord(w, r, proj, prefix, strings.TrimPrefix(rest, "updates/"))
	case rest == "departments/new" && r.Method == http.MethodGet:
		s.handleDeptNew(w, r, proj, prefix)
	case rest == "departments/add" && r.Method == http.MethodPost:
		s.handleDeptAdd(w, r, proj, prefix)
	case rest == "updates/install" && r.Method == http.MethodPost:
		s.handleUpdatesInstall(w, r, proj, prefix)
	case rest == "metrics" && r.Method == http.MethodGet:
		s.handleMetrics(w, r, proj, prefix, false)
	case rest == "metrics.json" && r.Method == http.MethodGet:
		s.handleMetrics(w, r, proj, prefix, true)
	case rest == "balance" && r.Method == http.MethodGet:
		s.handleBalance(w, r, proj, prefix)
	case (rest == "systems" || strings.HasPrefix(rest, "systems/")) && r.Method == http.MethodGet:
		s.handleSystems(w, r, proj, prefix, rest)
	case rest == "hash":
		s.handleHash(w, r, proj)
	case rest == "issues.json":
		s.handleIssuesJSON(w, r, proj)
	case strings.HasPrefix(rest, "docs/") && strings.HasSuffix(rest, "/edit-in-nvim") && r.Method == http.MethodPost:
		s.handleDocEditInNvim(w, r, proj, prefix)
	case (rest == "docs" || strings.HasPrefix(rest, "docs/")) && r.URL.Query().Get("classic") == "1":
		if rest == "docs" {
			s.handleDocs(w, r, proj, prefix)
		} else {
			s.handleDocPage(w, r, proj, prefix)
		}
	case rest == "docs" || rest == "docs/":
		s.handleDocsWb(w, r, proj, prefix, "")
	case strings.HasPrefix(rest, "docs/"):
		s.handleDocsWb(w, r, proj, prefix, strings.TrimPrefix(rest, "docs/"))
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/comments") && r.Method == http.MethodGet:
		s.handleGetComments(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/comments") && r.Method == http.MethodPost:
		s.handleAddComment(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/comments/toggle") && r.Method == http.MethodPost:
		s.handleToggleComment(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/comments/delete") && r.Method == http.MethodPost:
		s.handleDeleteComment(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/data") && r.Method == http.MethodPost:
		s.handleDataAdd(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.Contains(rest, "/data/") && strings.HasSuffix(rest, "/status") && r.Method == http.MethodPost:
		s.handleDataSetStatus(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.Contains(rest, "/data/") && strings.HasSuffix(rest, "/tier") && r.Method == http.MethodPost:
		s.handleDataSetTier(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.Contains(rest, "/data/") && strings.HasSuffix(rest, "/comment") && r.Method == http.MethodPost:
		s.handleDataSetComment(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.Contains(rest, "/data/") && r.Method == http.MethodDelete:
		s.handleDataRemove(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/dispatch") && r.Method == http.MethodPost:
		s.handleDispatchAgent(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/dispatch/reprompt") && r.Method == http.MethodPost:
		s.handleDispatchReprompt(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/dispatch-prompt") && r.Method == http.MethodGet:
		s.handleDispatchPrompt(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.Contains(rest, "/action/") && r.Method == http.MethodPost:
		s.handleCustomAction(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/edit-in-nvim") && r.Method == http.MethodPost:
		s.handleEditIssueInNvim(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/approvals.json") && r.Method == http.MethodGet:
		s.handleApprovalsJSON(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/transition") && r.Method == http.MethodGet:
		s.handleTransitionPreview(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/type") && r.Method == http.MethodPost:
		s.handleSetIssueType(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/check-script") && r.Method == http.MethodPost:
		s.handleRunChecks(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/playtest") && (r.Method == http.MethodPost || r.Method == http.MethodGet):
		s.handlePlaytest(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/checkbox") && r.Method == http.MethodPost:
		s.handleToggleCheckbox(w, r, proj, prefix)
	case rest == "yolo/review" && r.Method == http.MethodGet:
		s.handleYOLOReviewPage(w, r, proj, prefix)
	case rest == "yolo/review" && r.Method == http.MethodPost:
		s.handleYOLOReviewIssue(w, r, proj)
	case rest == "yolo.json" && r.Method == http.MethodGet:
		s.handleYOLO(w, r, proj, "json")
	case strings.HasPrefix(rest, "yolo/") && r.Method == http.MethodPost:
		s.handleYOLO(w, r, proj, strings.TrimPrefix(rest, "yolo/"))
	case strings.HasPrefix(rest, "jobs/") && strings.HasSuffix(rest, "/run") && r.Method == http.MethodPost:
		s.handleJobRun(w, r, proj, strings.TrimSuffix(strings.TrimPrefix(rest, "jobs/"), "/run"))
	case strings.HasPrefix(rest, "autostart/") && r.Method == http.MethodPost:
		s.handleAutostart(w, r, proj, strings.TrimPrefix(rest, "autostart/"))
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/yolo-review") && r.Method == http.MethodPost:
		s.handleYOLOReview(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/clarify") && r.Method == http.MethodPost:
		s.handleClarifyAnswer(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/send-back") && r.Method == http.MethodPost:
		s.handleSendBack(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/approve") && r.Method == http.MethodPost:
		s.handleApproveIssue(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/delete") && r.Method == http.MethodPost:
		s.handleDeleteIssue(w, r, proj, prefix)
	case strings.HasPrefix(rest, "action/") && r.Method == http.MethodPost:
		s.handleProjectAction(w, r, proj, prefix)
	case rest == "issues/create" && r.Method == http.MethodPost:
		s.handleCreateIssue(w, r, proj, prefix)
	case rest == "upload" && r.Method == http.MethodPost:
		s.handleUpload(w, r, proj, prefix)
	case strings.HasPrefix(rest, "files/") && r.Method == http.MethodGet:
		s.handleProjectFile(w, r, proj, prefix)
	case strings.HasPrefix(rest, "attachments/") && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		handleAttachment(w, r, proj, strings.TrimPrefix(rest, "attachments/"))
	case strings.HasPrefix(rest, "issue/") && r.Method == http.MethodGet:
		s.handleDetail(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && r.Method == http.MethodPost:
		s.handleUpdateIssue(w, r, proj, prefix)
	default:
		http.NotFound(w, r)
	}
}

// --- Project List ---

// handleProjectList is the board's home: the map of projects (L0, map.go),
// also with a single project.
func (s *Server) handleProjectList(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.handleMap(w, r)
}

// handleProjectFor is GET /project-for?cwd=<path>: the slug of the project
// whose work directory holds path (the longest match wins), for a client that
// knows its folder but not its project, such as the issue-approve Claude Code
// mod in an agent session started before dispatch exported ISSUE_VIEWER_PROJECT.
func (s *Server) handleProjectFor(w http.ResponseWriter, r *http.Request) {
	cwd := filepath.Clean(r.URL.Query().Get("cwd"))
	best, bestLen := "", -1
	for i := range s.projects {
		root := filepath.Clean(projectRoot(&s.projects[i]))
		if (cwd == root || strings.HasPrefix(cwd, root+string(filepath.Separator))) && len(root) > bestLen {
			best, bestLen = s.projects[i].Slug, len(root)
		}
	}
	if best == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"project": best})
}
