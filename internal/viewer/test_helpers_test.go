package viewer

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func init() {
	// Dispatch tests launch stand-in agents (true, echo, cat) that never show
	// a Claude pane; don't let each one wait out the production timeout.
	agentrun.PromptDeliveryTimeout = 300 * time.Millisecond
}

// withPromptDeliveryTimeout sets how long a dispatch waits for the prompt to
// show in the pane. Where delivery should succeed, leave room for a fresh
// tmux server under a full parallel test run on a laptop.
func withPromptDeliveryTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	original := agentrun.PromptDeliveryTimeout
	agentrun.PromptDeliveryTimeout = d
	t.Cleanup(func() { agentrun.PromptDeliveryTimeout = original })
}

func setupTestProject(t *testing.T) (tracker.Project, string) {
	t.Helper()
	tmpDir := t.TempDir()
	issueDir := filepath.Join(tmpDir, "issues")
	docsDir := filepath.Join(tmpDir, "docs")
	os.MkdirAll(issueDir, 0755)
	os.MkdirAll(docsDir, 0755)

	// Create test issues
	issues := map[string]string{
		"bug-in-login.md": `---
title: "Bug in login"
status: "in progress"
system: "Auth"
version: "1.0"
labels:
  - bug
  - urgent
priority: "high"
assignee: "alice"
created: "2025-01-15"
---

Login page crashes on submit.
`,
		"add-dark-mode.md": `---
title: "Add dark mode"
status: "backlog"
system: "UI"
version: "2.0"
labels:
  - enhancement
priority: "medium"
assignee: "bob"
created: "2025-01-10"
---

We need dark mode support.
`,
		"fix-typo.md": `---
title: "Fix typo"
status: "done"
system: "Docs"
priority: "low"
created: "2025-01-05"
---

Fix typo in readme.
`,
	}

	for name, content := range issues {
		if err := os.WriteFile(filepath.Join(issueDir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Create test doc
	doc := `---
title: "Getting Started"
order: 1
---

Welcome to the project.
`
	if err := os.WriteFile(filepath.Join(docsDir, "getting-started.md"), []byte(doc), 0644); err != nil {
		t.Fatal(err)
	}

	proj := tracker.Project{
		Name:     "Test Project",
		Slug:     "test-project",
		IssueDir: issueDir,
		DocsDir:  docsDir,
		// The repo's own workflow (template-based, with workbench.systems):
		// the pages it unlocks are what these tests look at.
		WorkflowFile: repoWorkflow(t),
	}
	return proj, tmpDir
}

func newTestServer(t *testing.T, projects []tracker.Project) *httptest.Server {
	t.Helper()
	srv, err := NewServer(projects)
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(asOwner(t, srv))
}

// strangerHeader marks a test request that must not carry the owner's
// session: curl, an agent's script. Without it, newTestServer's requests come
// from the owner's signed-in browser.
const strangerHeader = "X-Test-Stranger"

// asOwner is srv's handler with every request signed in as the owner (the
// session cookie added), unless it carries strangerHeader or a cookie of
// its own.
func asOwner(t *testing.T, srv *Server) http.Handler {
	t.Helper()
	token, err := srv.owner.newSession()
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Routes()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(strangerHeader) == "" {
			if _, err := r.Cookie(ownerCookie); err != nil {
				r.AddCookie(&http.Cookie{Name: ownerCookie, Value: token})
			}
		}
		h.ServeHTTP(w, r)
	})
}

func withMockTmuxSessions(t *testing.T, sessions []AgentSession) {
	t.Helper()
	original := listTmuxSessions
	listTmuxSessions = func() []AgentSession { return sessions }
	t.Cleanup(func() {
		listTmuxSessions = original
	})
}

func withMockTmuxSendKeys(t *testing.T, fn func(target string, lines []string) error) {
	t.Helper()
	original := tmuxSendKeys
	tmuxSendKeys = fn
	t.Cleanup(func() {
		tmuxSendKeys = original
	})
}

func withMockTmuxHasSession(t *testing.T, fn func(session string) bool) {
	t.Helper()
	original := agentrun.HasSession
	agentrun.HasSession = fn
	t.Cleanup(func() {
		agentrun.HasSession = original
	})
}

func withWorkingDir(t *testing.T, dir string) {
	t.Helper()
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(original); err != nil {
			t.Fatal(err)
		}
	})
}

// repoRoot is the repository root seen from this package's folder, where
// go test runs: README.md, docs/, playtest/, workflow.yaml and the Makefile
// live there, not next to the viewer's code.
const repoRoot = "../.."

// repoPath joins parts under repoRoot.
func repoPath(parts ...string) string {
	return filepath.Join(append([]string{repoRoot}, parts...)...)
}

// repoWorkflow is the repo's own workflow.yaml, absolute: a project with no
// workflow: file of its own would otherwise read the one in the working
// folder, which for this package's tests is not the repo root.
func repoWorkflow(t testing.TB) string {
	t.Helper()
	abs, err := filepath.Abs(repoPath("workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
