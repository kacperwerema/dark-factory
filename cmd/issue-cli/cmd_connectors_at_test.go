package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/connectors/connectortest"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// atFixture is httpConnectorFixture plus marketing/postiz, a url:
// connector at a stand-in server (no auth) with the at: block given, and
// seo/page at backlog, approved for in progress so its worker can move it.
func atFixture(t *testing.T, at string) (root string, stub *connectortest.Server) {
	t.Helper()
	root, _ = httpConnectorFixture(t)
	stub = connectortest.New(t, "")
	cfg := os.Getenv("ISSUE_VIEWER_CONFIG")
	b, _ := os.ReadFile(cfg)
	mustWrite(t, cfg, string(b)+`          - name: postiz
            kind: mcp
            url: `+stub.MCPURL()+`
            sends: true
            at:
`+at)
	mustWrite(t, filepath.Join(root, "issues", "SEO", "page.md"), "---\ntitle: \"Page\"\nstatus: \"backlog\"\nsystem: \"SEO\"\nhuman_approval: \"in progress\"\n---\n\nBody.\n")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "seo/page")
	return root, stub
}

// mcpSession is one MCP client session with connector exec kept open:
// send writes a message, answer waits for the reply to id.
type mcpSession struct {
	t    *testing.T
	in   *io.PipeWriter
	mu   sync.Mutex
	out  strings.Builder
	done chan error
}

// syncWriter guards the session's output, written by the bridge's
// goroutines and read by the test.
type syncWriter struct{ s *mcpSession }

func (w syncWriter) Write(p []byte) (int, error) {
	w.s.mu.Lock()
	defer w.s.mu.Unlock()
	return w.s.out.Write(p)
}

func openMCPSession(t *testing.T, args []string, env map[string]string) *mcpSession {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
	r, w := io.Pipe()
	s := &mcpSession{t: t, in: w, done: make(chan error, 1)}
	go func() {
		var errw strings.Builder
		err := runTraced(args, r, syncWriter{s}, &errw, &invocation{})
		if err != nil {
			err = fmt.Errorf("%w: %s", err, errw.String())
		}
		s.done <- err
	}()
	t.Cleanup(func() { w.Close() })
	return s
}

func (s *mcpSession) send(msg string) {
	s.t.Helper()
	if _, err := io.WriteString(s.in, msg+"\n"); err != nil {
		s.t.Fatal(err)
	}
}

// answer is the reply line to id, waited for.
func (s *mcpSession) answer(id int) string {
	s.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		out := s.out.String()
		s.mu.Unlock()
		for _, line := range strings.Split(out, "\n") {
			var head struct {
				ID     *float64 `json:"id"`
				Method string   `json:"method"`
			}
			if json.Unmarshal([]byte(line), &head) == nil && head.ID != nil && int(*head.ID) == id && head.Method == "" {
				return line
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.t.Fatalf("no answer to %d:\n%s", id, s.out.String())
	return ""
}

func (s *mcpSession) close() {
	s.t.Helper()
	s.in.Close()
	if err := <-s.done; err != nil {
		s.t.Fatalf("exec: %v", err)
	}
}

func (s *mcpSession) start() {
	s.t.Helper()
	s.send(clientInit)
	s.answer(1)
	s.send(clientInited)
	s.send(clientList)
	s.answer(2)
}

func toolCall(id int, tool, args string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, id, tool, args)
}

func stubCalls(stub *connectortest.Server, tool string) int {
	n := 0
	for _, r := range stub.Requests() {
		if r.RPC == "tools/call" && r.Tool == tool {
			n++
		}
	}
	return n
}

// TestBridgeAllowsOnlyThisStatusTools: in one session through connector
// exec, a tool at: allows at backlog reaches the server; after the worker
// moves the issue to in progress (issue-cli transition, the file on disk),
// the same tool is answered with a JSON-RPC error naming the status and
// what it allows, and never reaches the server. A status at: doesn't name
// allows nothing.
func TestBridgeAllowsOnlyThisStatusTools(t *testing.T) {
	root, stub := atFixture(t, `              backlog:
                allow: [`+connectortest.ToolName+`]
              in progress:
                allow: [create_post]
`)
	_, servers := launchMCP(t, root)
	srv := servers["postiz"]
	s := openMCPSession(t, srv.Args, srv.Env)
	s.start()

	s.send(toolCall(3, connectortest.ToolName, `{}`))
	if a := s.answer(3); !strings.Contains(a, "called "+connectortest.ToolName) {
		t.Fatalf("at backlog the allowed tool was refused: %s", a)
	}
	s.send(toolCall(4, "create_post", `{}`))
	if a := s.answer(4); !strings.Contains(a, "connector marketing/postiz at backlog: create_post is not allowed (at: backlog allows "+connectortest.ToolName+")") {
		t.Errorf("at backlog create_post: %s", a)
	}

	if out, err := runCLI(t, "transition", "seo/page", "--to", "in progress"); err != nil {
		t.Fatalf("transition: %s", out)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "issues", "SEO", "page.md")); !strings.Contains(string(b), `status: "in progress"`) && !strings.Contains(string(b), "status: in progress") {
		t.Fatalf("the issue did not move:\n%s", b)
	}

	s.send(toolCall(5, connectortest.ToolName, `{}`))
	a := s.answer(5)
	if !strings.Contains(a, `"error"`) || !strings.Contains(a, "connector marketing/postiz at in progress: "+connectortest.ToolName+" is not allowed (at: in progress allows create_post)") {
		t.Errorf("after the move the tool passed: %s", a)
	}
	s.send(toolCall(6, "create_post", `{}`))
	if a := s.answer(6); !strings.Contains(a, "called create_post") {
		t.Errorf("at in progress create_post: %s", a)
	}
	s.close()
	if n := stubCalls(stub, connectortest.ToolName); n != 1 {
		t.Errorf("%s reached the server %d times, want 1 (at backlog only)", connectortest.ToolName, n)
	}
	if n := stubCalls(stub, "create_post"); n != 1 {
		t.Errorf("create_post reached the server %d times, want 1 (at in progress only)", n)
	}
	if strings.Contains(s.out.String(), fakeHeaderValue) {
		t.Error("a refusal printed the header secret")
	}

	// A status at: doesn't name allows no tool.
	mustWrite(t, filepath.Join(root, "issues", "SEO", "page.md"), "---\ntitle: \"Page\"\nstatus: \"in design\"\nsystem: \"SEO\"\n---\n\nBody.\n")
	out, err := runMCPServer(t, srv.Args, srv.Env, clientInit+"\n"+clientInited+"\n"+toolCall(3, "create_post", `{}`)+"\n")
	if err != nil || !strings.Contains(out, "connector marketing/postiz at in design: no tools are allowed (at: names backlog, in progress)") {
		t.Errorf("unnamed status (%v): %s", err, out)
	}
}

// TestBridgeRefusesADeniedArgumentValue: deny_args: refuses the call whose
// argument holds a listed value, naming it; the same tool with another
// value of that argument, or without it, reaches the server.
func TestBridgeRefusesADeniedArgumentValue(t *testing.T) {
	root, stub := atFixture(t, `              backlog:
                allow: [create_post]
                deny_args:
                  create_post:
                    type: [now]
`)
	_, servers := launchMCP(t, root)
	srv := servers["postiz"]
	session := strings.Join([]string{clientInit, clientInited,
		toolCall(3, "create_post", `{"type":"now","text":"hi"}`),
		toolCall(4, "create_post", `{"type":"draft","text":"hi"}`),
		toolCall(5, "create_post", `{"text":"hi"}`),
	}, "\n") + "\n"
	out, err := runMCPServer(t, srv.Args, srv.Env, session)
	if err != nil {
		t.Fatalf("exec: %s", out)
	}
	got := map[int]string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var head struct {
			ID *float64 `json:"id"`
		}
		if json.Unmarshal([]byte(line), &head) == nil && head.ID != nil {
			got[int(*head.ID)] = line
		}
	}
	if !strings.Contains(got[3], `"error"`) || !strings.Contains(got[3], "connector marketing/postiz at backlog: create_post with type: now is refused (at: backlog: deny_args:); another value of type passes") {
		t.Errorf("type: now was not refused: %s", got[3])
	}
	for _, id := range []int{4, 5} {
		if !strings.Contains(got[id], "called create_post") {
			t.Errorf("call %d was refused: %s", id, got[id])
		}
	}
	if n := stubCalls(stub, "create_post"); n != 2 {
		t.Errorf("create_post reached the server %d times, want 2 (the refused one never)", n)
	}
}

// TestConnectorAtOwnerAndLead: the owner's shell passes every call, a lead
// has no issue status and is refused naming --as-owner, and a connector
// without at: is not filtered.
func TestConnectorAtOwnerAndLead(t *testing.T) {
	_, stub := atFixture(t, `              in progress:
                allow: [create_post]
`)
	session := clientInit + "\n" + clientInited + "\n" + toolCall(3, connectortest.ToolName, `{}`) + "\n"

	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	out, err := runMCPServer(t, []string{"--project", "demo", "connector", "exec", "marketing/postiz", "--as-owner"}, nil, session)
	if err != nil || !strings.Contains(out, "called "+connectortest.ToolName) {
		t.Errorf("--as-owner (%v): %s", err, out)
	}

	asLeadOf(t, "marketing")
	out, err = runMCPServer(t, []string{"--project", "demo", "connector", "exec", "marketing/postiz"}, nil, session)
	if err != nil || !strings.Contains(out, "connector marketing/postiz allows its tools per issue status (at:)") || !strings.Contains(out, "--as-owner") {
		t.Errorf("lead (%v): %s", err, out)
	}
	if n := stubCalls(stub, connectortest.ToolName); n != 1 {
		t.Errorf("%s reached the server %d times, want 1 (the owner's)", connectortest.ToolName, n)
	}

	// openseo has no at:: a worker at backlog calls any tool.
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "seo/page")
	out, err = runMCPServer(t, []string{"--project", "demo", "connector", "exec", "marketing/openseo"}, nil, session)
	if err != nil || !strings.Contains(out, "called "+connectortest.ToolName) {
		t.Errorf("a connector without at: was filtered (%v): %s", err, out)
	}
}

// TestConnectorsListShowsToolsByStatus: the listing, its JSON and the
// worker's briefing line name the at: statuses.
func TestConnectorsListShowsToolsByStatus(t *testing.T) {
	atFixture(t, `              backlog:
                allow: [create_post]
                deny_args:
                  create_post: {type: [now]}
              in progress:
                allow: [create_post]
`)
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	out, err := runCLI(t, "connectors", "marketing")
	if err != nil {
		t.Fatal(out)
	}
	assertContains(t, out, "marketing/postiz (mcp) · sends · tools by status (at: backlog, in progress)")
	if strings.Contains(out, "marketing/openseo (mcp) · tools by status") {
		t.Errorf("a connector without at: lists statuses:\n%s", out)
	}
	out, _ = runCLI(t, "connectors", "marketing", "--json")
	assertContains(t, out, `"at": {`)
	assertContains(t, out, `"deny_args": {`)

	projects, err := tracker.LoadProjects(os.Getenv("ISSUE_VIEWER_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	brief := tracker.ConnectorsBrief(&projects[0], "Marketing", false)
	assertContains(t, brief, "postiz (MCP, loaded in this session) — it sends: use it only for an output the owner approved, then issue-cli output publish; its tools are limited per status (at: backlog, in progress): issue-cli connectors marketing lists them")
}

// TestBridgeRefusesANestedArgumentValue (AC1): a deny_args: path reaches
// into every item of an array; a call with one matching item is answered
// with an error naming where it sits and never reaches the server, and a
// call whose items all hold other values does.
func TestBridgeRefusesANestedArgumentValue(t *testing.T) {
	root, stub := atFixture(t, `              backlog:
                allow: [create_post]
                deny_args:
                  create_post:
                    "posts[].type": [now]
`)
	_, servers := launchMCP(t, root)
	srv := servers["postiz"]
	session := strings.Join([]string{clientInit, clientInited,
		toolCall(3, "create_post", `{"posts":[{"type":"schedule"},{"type":"now"}]}`),
		toolCall(4, "create_post", `{"posts":[{"type":"schedule"},{"type":"draft"}]}`),
	}, "\n") + "\n"
	out, err := runMCPServer(t, srv.Args, srv.Env, session)
	if err != nil {
		t.Fatalf("exec: %s", out)
	}
	got := map[int]string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var head struct {
			ID *float64 `json:"id"`
		}
		if json.Unmarshal([]byte(line), &head) == nil && head.ID != nil {
			got[int(*head.ID)] = line
		}
	}
	if !strings.Contains(got[3], `"error"`) || !strings.Contains(got[3], "create_post with posts[1].type: now is refused (at: backlog: deny_args:); another value of posts[].type passes") {
		t.Errorf("an item's type: now was not refused: %s", got[3])
	}
	if !strings.Contains(got[4], "called create_post") {
		t.Errorf("schedule and draft items were refused: %s", got[4])
	}
	if n := stubCalls(stub, "create_post"); n != 1 {
		t.Errorf("create_post reached the server %d times, want 1 (the refused one never)", n)
	}
}
