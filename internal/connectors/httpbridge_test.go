package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/connectors/connectortest"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

const bridgeAuth = "Bearer fake-bridge-token-91c2"

func runBridge(t *testing.T, url string, h http.Header, lines ...string) []map[string]any {
	t.Helper()
	var out, errw strings.Builder
	b := &Bridge{URL: url, Header: h, Stderr: &errw}
	if err := b.Run(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(out.String()+errw.String(), "fake-bridge-token") {
		t.Errorf("the bridge printed the header:\n%s%s", out.String(), errw.String())
	}
	var msgs []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not one JSON message per line: %q", l)
		}
		msgs = append(msgs, m)
	}
	return msgs
}

func authHeader() http.Header {
	h := http.Header{}
	h.Set("Authorization", bridgeAuth)
	return h
}

const (
	initMsg  = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	initedNo = `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	listMsg  = `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
)

// The session flow: initialize as JSON, the session id and protocol version
// on later requests, tools/list as SSE (a notification, then the answer),
// the header on every request, DELETE at the end.
func TestBridgeListsTools(t *testing.T) {
	s := connectortest.New(t, bridgeAuth)
	msgs := runBridge(t, s.MCPURL(), authHeader(), initMsg, initedNo, listMsg)
	if len(msgs) != 3 {
		t.Fatalf("messages = %v", msgs)
	}
	if msgs[0]["id"] != 1.0 || msgs[1]["method"] != "notifications/message" || msgs[2]["id"] != 2.0 {
		t.Fatalf("messages = %v", msgs)
	}
	tools := msgs[2]["result"].(map[string]any)["tools"].([]any)
	if tools[0].(map[string]any)["name"] != connectortest.ToolName {
		t.Errorf("tools = %v", tools)
	}
	reqs := s.Requests()
	if len(reqs) != 4 || reqs[3].Method != http.MethodDelete {
		t.Fatalf("requests = %+v", reqs)
	}
	for i, r := range reqs {
		if r.Header.Get("Authorization") != bridgeAuth {
			t.Errorf("request %d (%s) has no auth header", i, r.RPC)
		}
		if i > 0 && (r.Session != connectortest.Session || r.Protocol != connectortest.Protocol) {
			t.Errorf("request %d (%s): session %q protocol %q", i, r.RPC, r.Session, r.Protocol)
		}
	}
	if reqs[0].Session != "" || !strings.Contains(reqs[0].Header.Get("Accept"), "text/event-stream") {
		t.Errorf("initialize: %+v", reqs[0])
	}
}

// A refused or failed request gets a JSON-RPC error with its id; a request
// answered with nothing gets one too; a notification's failure does not.
func TestBridgeAnswersFailures(t *testing.T) {
	s := connectortest.New(t, bridgeAuth)
	msgs := runBridge(t, s.MCPURL(), http.Header{}, initMsg, initedNo)
	if len(msgs) != 1 || msgs[0]["id"] != 1.0 || !strings.Contains(msgs[0]["error"].(map[string]any)["message"].(string), "401 Unauthorized") {
		t.Fatalf("no auth: %v", msgs)
	}
	msgs = runBridge(t, s.MCPURL(), authHeader(), initMsg,
		`{"jsonrpc":"2.0","id":"a","method":"fail"}`, `{"jsonrpc":"2.0","id":"b","method":"silent"}`)
	errs := map[string]string{}
	for _, m := range msgs[1:] {
		errs[m["id"].(string)] = m["error"].(map[string]any)["message"].(string)
	}
	if !strings.Contains(errs["a"], "500") || !strings.Contains(errs["b"], "held no reply") {
		t.Errorf("errors = %v", errs)
	}
	// Nothing listens: an error, never the header.
	dead := connectortest.New(t, "")
	url := dead.MCPURL()
	dead.Close()
	msgs = runBridge(t, url, authHeader(), initMsg)
	if len(msgs) != 1 || !strings.Contains(msgs[0]["error"].(map[string]any)["message"].(string), "could not reach the server") {
		t.Errorf("dead server: %v", msgs)
	}
}

// A redirect is answered as an error and not followed: the header would
// go wherever it points.
func TestBridgeNoRedirect(t *testing.T) {
	s := connectortest.New(t, bridgeAuth)
	msgs := runBridge(t, s.Server.URL+"/moved", authHeader(), initMsg)
	if len(msgs) != 1 || !strings.Contains(msgs[0]["error"].(map[string]any)["message"].(string), "never follows one") {
		t.Fatalf("redirect: %v", msgs)
	}
	if reqs := s.Requests(); len(reqs) != 1 {
		t.Errorf("the redirect was followed: %+v", reqs)
	}
}

// Headers reads each header's secret now, with its literal prefix; a
// missing one names the reference and never a value.
func TestHeaders(t *testing.T) {
	present, value := tracker.SecretPresent, tracker.SecretValue
	t.Cleanup(func() { tracker.SecretPresent, tracker.SecretValue = present, value })
	set := map[string]string{"env:ACME_SEO": "fake-bridge-token-91c2"}
	tracker.SecretPresent = func(r tracker.SecretRef) bool { _, ok := set[r.String()]; return ok }
	tracker.SecretValue = func(r tracker.SecretRef) (string, bool) { v, ok := set[r.String()]; return v, ok }

	c := &tracker.Connector{Name: "openseo", Kind: tracker.ConnectorMCP, URL: "http://localhost:1/mcp",
		Headers: map[string]string{"Authorization": "Bearer env:ACME_SEO"}}
	h, err := Headers(c)
	if err != nil || h.Get("Authorization") != bridgeAuth {
		t.Fatalf("headers = %v, %v", h, err)
	}
	c.Headers["X-Api-Key"] = "keychain:acme-missing"
	if _, err := Headers(c); err == nil || !strings.Contains(err.Error(), "header X-Api-Key (keychain:acme-missing) is missing on this machine: the owner adds it with security add-generic-password -s acme-missing") {
		t.Errorf("missing: %v", err)
	}
	// Env never gets a header's secret.
	env, err := Env(c, nil)
	if err != nil || len(env) != 0 {
		t.Errorf("env = %v, %v", env, err)
	}
	if _, err := Command(c, []string{"sh"}); err == nil || !strings.Contains(err.Error(), "bridges it itself") {
		t.Errorf("command: %v", err)
	}
}

// The hold is asked at each tools/call, not once at start: an approval
// during the session opens the next call. A held call never reaches the
// server, a read tool never asks, and other methods pass.
func TestBridgeAsksTheHoldAtEachCall(t *testing.T) {
	s := connectortest.New(t, "")
	asked := 0
	var out strings.Builder
	b := &Bridge{URL: s.MCPURL(), ReadTools: []string{"look"}, Hold: func() error {
		asked++
		if asked == 1 {
			return errors.New("held for the test")
		}
		return nil
	}}
	call := func(id int, tool string) string {
		return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q}}`, id, tool)
	}
	in := strings.Join([]string{initMsg, initedNo, listMsg, call(3, "post"), call(4, "look"), call(5, "post")}, "\n") + "\n"
	if err := b.Run(context.Background(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if asked != 2 {
		t.Errorf("the hold was asked %d times, want 2 (once per held tool's call)", asked)
	}
	if !strings.Contains(out.String(), `{"error":{"code":-32000,"message":"connector bridge: held for the test"},"id":3,`) {
		t.Errorf("call 3 not refused:\n%s", out.String())
	}
	posts := 0
	for _, r := range s.Requests() {
		if r.Tool == "post" {
			posts++
		}
	}
	if posts != 1 || !strings.Contains(out.String(), "called post") || !strings.Contains(out.String(), "called look") {
		t.Errorf("post reached the server %d times (want 1, the call after the hold opened):\n%s", posts, out.String())
	}
}

func TestHoldError(t *testing.T) {
	shelf := errors.New("no output on the shelf for seo/page")
	if HoldError("marketing/post", Caller{Owner: true}, shelf) != nil {
		t.Error("the owner is held")
	}
	if HoldError("marketing/post", Caller{Agent: true, Issue: "seo/page"}, nil) != nil {
		t.Error("an approved issue is held")
	}
	if err := HoldError("marketing/post", Caller{Agent: true, Issue: "seo/page"}, shelf); err == nil || !strings.Contains(err.Error(), "and is held: no output on the shelf") {
		t.Errorf("worker: %v", err)
	}
	if err := HoldError("marketing/post", Caller{Agent: true, Lead: true, Department: "Marketing"}, nil); err == nil || !strings.Contains(err.Error(), "--as-owner") {
		t.Errorf("lead: %v", err)
	}
}

// The gate is asked at every tools/call with the tool and its arguments,
// before the hold; a refused call never reaches the server.
func TestBridgeAsksTheGateWithArguments(t *testing.T) {
	s := connectortest.New(t, "")
	var seen []string
	var out strings.Builder
	b := &Bridge{URL: s.MCPURL(), Gate: func(tool string, args json.RawMessage) error {
		seen = append(seen, tool+" "+string(args))
		if tool == "post" {
			return errors.New("gated for the test")
		}
		return nil
	}}
	in := strings.Join([]string{initMsg, initedNo, listMsg,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"post","arguments":{"type":"now"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"look","arguments":{"q":1}}}`}, "\n") + "\n"
	if err := b.Run(context.Background(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen, "|") != `post {"type":"now"}|look {"q":1}` {
		t.Errorf("gate saw %q", seen)
	}
	if !strings.Contains(out.String(), "connector bridge: gated for the test") || !strings.Contains(out.String(), "called look") {
		t.Errorf("out:\n%s", out.String())
	}
	for _, r := range s.Requests() {
		if r.Tool == "post" {
			t.Error("the gated call reached the server")
		}
	}
}
