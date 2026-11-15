// Package connectortest is a stand-in remote MCP server (Streamable HTTP)
// for tests of the connector bridge: it answers initialize as JSON with a
// session id, tools/list as an SSE stream, records each request's headers,
// and refuses a request without the expected auth header.
package connectortest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Session is the id the stub hands out at initialize.
const Session = "stub-session-1"

// Protocol is the protocol version the stub answers with.
const Protocol = "2025-06-18"

// ToolName is the one tool the stub lists.
const ToolName = "keyword_research"

// Request is one request the stub saw.
type Request struct {
	Method   string // HTTP method
	RPC      string // JSON-RPC method
	Tool     string // a tools/call's params.name
	Header   http.Header
	Session  string
	Protocol string
}

// Server is the stub.
type Server struct {
	*httptest.Server
	// Auth is the Authorization value every request must carry ("" any).
	Auth string

	mu   sync.Mutex
	reqs []Request
}

// New starts the stub on loopback; it stops at the end of the test.
func New(t testing.TB, auth string) *Server {
	s := &Server{Auth: auth}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// URL is the stub's MCP endpoint.
func (s *Server) MCPURL() string { return s.Server.URL + "/mcp" }

// Requests are the requests seen so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request{}, s.reqs...)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	if r.Method == http.MethodPost {
		json.NewDecoder(r.Body).Decode(&msg)
	}
	s.mu.Lock()
	s.reqs = append(s.reqs, Request{Method: r.Method, RPC: msg.Method, Tool: msg.Params.Name, Header: r.Header.Clone(), Session: r.Header.Get("Mcp-Session-Id"), Protocol: r.Header.Get("MCP-Protocol-Version")})
	s.mu.Unlock()
	if r.URL.Path == "/moved" {
		http.Redirect(w, r, "/mcp", http.StatusFound)
		return
	}
	if s.Auth != "" && r.Header.Get("Authorization") != s.Auth {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodDelete {
		w.WriteHeader(http.StatusOK)
		return
	}
	if msg.Method != "initialize" && r.Header.Get("Mcp-Session-Id") != Session {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	switch msg.Method {
	case "initialize":
		w.Header().Set("Mcp-Session-Id", Session)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":%q,"capabilities":{"tools":{}},"serverInfo":{"name":"stub","version":"1"}}}`, msg.ID, Protocol)
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": keep-alive\n\n")
		fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\",\n")
		fmt.Fprint(w, "data:  \"params\":{\"level\":\"info\",\"data\":\"listing\"}}\n\n")
		fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"tools\":[{\"name\":%q,\"description\":\"Keyword ideas\",\"inputSchema\":{\"type\":\"object\"}}]}}\n\n", msg.ID, ToolName)
	case "tools/call":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"called %s"}]}}`, msg.ID, msg.Params.Name)
	case "fail":
		http.Error(w, "boom", http.StatusInternalServerError)
	case "silent":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	default:
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"method not found"}}`, msg.ID)
	}
}
