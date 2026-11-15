package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/connectors"
	"github.com/kacperwerema/dark-factory/internal/connectors/connectortest"
	"github.com/kacperwerema/dark-factory/internal/standin"
)

// lazyRun is marketing/openseo (url: at the stub, a header secret) with a
// per_run: block counting its starts, and a stand-in mem-guard whose answer
// the test sets.
type lazyRun struct {
	t     *testing.T
	root  string
	stub  *connectortest.Server
	up    string
	guard string
	args  []string
	env   map[string]string
}

func lazyFixture(t *testing.T, extra string) *lazyRun {
	t.Helper()
	root, stub := httpConnectorFixture(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	r := &lazyRun{t: t, root: root, stub: stub, up: filepath.Join(root, "up"), guard: filepath.Join(root, "guard.txt")}
	bin := filepath.Join(root, "bin")
	os.MkdirAll(bin, 0755)
	standin.Write(t, filepath.Join(bin, "mem-guard"), "cat \"${0%/bin/*}/guard.txt\"\n")
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	r.memGuard("ok: memory used 50% (< 75%)")
	cfg := os.Getenv("ISSUE_VIEWER_CONFIG")
	b, _ := os.ReadFile(cfg)
	mustWrite(t, cfg, string(b)+extra+`            per_run:
              start: 'echo x >> `+r.up+`.starts; touch `+r.up+`'
              ready: 'test -f `+r.up+`'
              stop: 'rm -f `+r.up+`'
              timeout: 20
`)
	_, servers := launchMCP(t, root)
	srv, ok := servers["openseo"]
	if !ok {
		t.Fatalf("mcp.json has no openseo: %v", servers)
	}
	r.args, r.env = srv.Args, srv.Env
	return r
}

func (r *lazyRun) memGuard(line string) { mustWrite(r.t, r.guard, line+"\n") }

func (r *lazyRun) starts() int {
	b, _ := os.ReadFile(r.up + ".starts")
	return strings.Count(string(b), "\n")
}

// session runs one agent session (connector exec as mcp.json names it) fed
// msgs, and returns its answers by id.
func (r *lazyRun) session(msgs ...string) map[int]string {
	r.t.Helper()
	out, err := runMCPServer(r.t, r.args, r.env, strings.Join(msgs, "\n")+"\n")
	if err != nil {
		r.t.Fatalf("exec: %s", out)
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
	if fileExists(r.up) {
		r.t.Error("the tool is still up after the session")
	}
	return got
}

func (r *lazyRun) listingPath() string {
	m, _ := filepath.Glob(filepath.Join(os.Getenv("XDG_STATE_HOME"), "*", "connectors", "*", "marketing-openseo", "listing.json"))
	if len(m) != 1 {
		return filepath.Join(r.root, "no-listing.json")
	}
	return m[0]
}

func (r *lazyRun) runLog() string {
	b, _ := os.ReadFile(filepath.Join(r.root, "issues", ".connectors", "runs.jsonl"))
	return string(b)
}

const (
	clientPing = `{"jsonrpc":"2.0","id":5,"method":"ping"}`
	clientCall = `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"` + connectortest.ToolName + `","arguments":{}}}`
)

// B1/AC1/AC2: once a session saved the server's tools, a session that
// lists and pings starts nothing and answers from the copy; the first
// tools/call starts the tool, opens the server's session and is answered,
// and the tool stops when the session ends.
func TestPerRunToolStaysDownUntilTheFirstCall(t *testing.T) {
	r := lazyFixture(t, "")
	r.session(clientInit, clientInited, clientList)
	if r.starts() != 1 {
		t.Fatalf("the first session (no copy) started %d times", r.starts())
	}
	seen := len(r.stub.Requests())

	got := r.session(clientInit, clientInited, clientList, clientPing)
	if r.starts() != 1 {
		t.Errorf("a session that called no tool started the tool")
	}
	if n := len(r.stub.Requests()); n != seen {
		t.Errorf("the server saw %d requests from a session that called nothing", n-seen)
	}
	if !strings.Contains(got[1], `"protocolVersion":"`+connectortest.Protocol+`"`) || !strings.Contains(got[2], connectortest.ToolName) || !strings.Contains(got[5], `"result":{}`) {
		t.Errorf("answers from the copy = %v", got)
	}
	if strings.Count(r.runLog(), `"event":"started"`) != 1 {
		t.Errorf("run log:\n%s", r.runLog())
	}
	list, _ := runCLI(t, "connectors", "marketing")
	assertContains(t, list, "marketing/openseo (mcp) · starts per run at the first call, stopped")

	got = r.session(clientInit, clientInited, clientList, clientCall)
	if r.starts() != 2 {
		t.Errorf("the first call started the tool %d times", r.starts()-1)
	}
	if !strings.Contains(got[3], "called "+connectortest.ToolName) {
		t.Errorf("the call = %q", got[3])
	}
	reqs := r.stub.Requests()[seen:]
	if len(reqs) < 3 || reqs[0].RPC != "initialize" || reqs[1].RPC != "notifications/initialized" || reqs[2].RPC != "tools/call" || reqs[2].Session != connectortest.Session {
		t.Errorf("the woken session's requests = %+v", reqs)
	}
	if strings.Count(r.runLog(), `"event":"stopped"`) != 2 {
		t.Errorf("run log:\n%s", r.runLog())
	}
}

// B2/AC3: with mem-guard BLOCKED the first call is answered with a JSON-RPC
// error holding mem-guard's line; nothing starts, the session goes on, and
// exec ends cleanly.
func TestFirstCallHeldByMemGuardAnswersAnError(t *testing.T) {
	r := lazyFixture(t, "")
	r.session(clientInit, clientInited, clientList)
	const blocked = "BLOCKED: memory used 80% (start limit 75%, hard limit 85%), paused: 0."
	r.memGuard(blocked)
	got := r.session(clientInit, clientInited, clientCall, `{"jsonrpc":"2.0","id":4,"method":"tools/list"}`, clientPing)
	if !strings.Contains(got[3], `"error"`) || !strings.Contains(got[3], blocked) || !strings.Contains(got[3], "connector marketing/openseo stays stopped, nothing was started") {
		t.Errorf("the held call = %q", got[3])
	}
	if !strings.Contains(got[4], connectortest.ToolName) || !strings.Contains(got[5], `"result"`) {
		t.Errorf("the session did not go on: %v", got)
	}
	if r.starts() != 1 {
		t.Errorf("a held call started the tool")
	}
	assertContains(t, r.runLog(), `"event":"held"`)
}

// B3/AC4/AC8: with no saved copy the tool is held at launch, as before,
// even for a session that sends only initialize; that session's answers
// are saved (0600, no header) for the next. A copy from another url: is
// not used.
func TestPerRunWithNoSavedToolListStartsAtLaunch(t *testing.T) {
	r := lazyFixture(t, "")
	r.session(clientInit)
	if r.starts() != 1 {
		t.Fatalf("no copy: started %d times at launch", r.starts())
	}
	r.session(clientInit, clientInited, clientList)
	if r.starts() != 2 {
		t.Fatalf("the copy was used before it held tools/list")
	}
	fi, err := os.Stat(r.listingPath())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Errorf("listing.json mode = %v", fi.Mode().Perm())
	}
	b, _ := os.ReadFile(r.listingPath())
	if strings.Contains(string(b), fakeHeaderValue) || !strings.Contains(string(b), connectortest.ToolName) {
		t.Errorf("listing.json = %s", b)
	}

	// The copy names the url: it came from.
	other := connectortest.New(t, "Bearer "+fakeHeaderValue)
	cfg := os.Getenv("ISSUE_VIEWER_CONFIG")
	cb, _ := os.ReadFile(cfg)
	mustWrite(t, cfg, strings.Replace(string(cb), r.stub.MCPURL(), other.MCPURL(), 1))
	r.session(clientInit, clientInited)
	if r.starts() != 3 {
		t.Errorf("a copy from another url: kept the tool down")
	}
	if len(other.Requests()) == 0 {
		t.Error("the new url: saw nothing")
	}
}

// AC5: a call needs_approved_output: refuses in a down session starts
// nothing; a read tool, which passes, starts it.
func TestRefusedCallKeepsAPerRunToolDown(t *testing.T) {
	r := lazyFixture(t, "            needs_approved_output: true\n            read_tools: ["+connectortest.ToolName+"]\n")
	r.session(clientInit, clientInited, clientList)
	got := r.session(clientInit, clientInited, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"create_post","arguments":{}}}`)
	if !strings.Contains(got[3], "needs an approved output") {
		t.Errorf("create_post = %q", got[3])
	}
	if r.starts() != 1 {
		t.Errorf("a refused call started the tool")
	}
	r.session(clientInit, clientInited, clientCall)
	if r.starts() != 2 {
		t.Errorf("a read tool's call did not start it")
	}
}

// The listing's state word, read from the same copy.
func TestConnectorsListToolsSaved(t *testing.T) {
	r := lazyFixture(t, "")
	list, _ := runCLI(t, "connectors", "marketing", "--json")
	if strings.Contains(list, "tools_saved") {
		t.Errorf("no copy yet: %s", list)
	}
	r.session(clientInit, clientInited, clientList)
	p := holdProject(t)
	d := p.DepartmentByKey("marketing")
	for i := range d.Connectors {
		if c := &d.Connectors[i]; c.Name == "openseo" && connectors.ListingSaved(p, d.Name, c).IsZero() {
			t.Error("ListingSaved is zero after a listing session")
		}
	}
	list, _ = runCLI(t, "connectors", "marketing", "--json")
	assertContains(t, list, `"tools_saved"`)
}
