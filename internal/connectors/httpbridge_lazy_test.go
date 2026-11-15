package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/connectors/connectortest"
)

// stubListing is the copy a first session against connectortest saves.
func stubListing() *Listing {
	return &Listing{Answers: map[string]json.RawMessage{
		"initialize " + connectortest.Protocol: json.RawMessage(`{"protocolVersion":"` + connectortest.Protocol + `","capabilities":{"tools":{}},"serverInfo":{"name":"stub","version":"1"}}`),
		"tools/list":                           json.RawMessage(`{"tools":[{"name":"` + connectortest.ToolName + `"}]}`),
	}}
}

// lineSink collects the bridge's output lines for a test reading while it runs.
type lineSink struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (s *lineSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

// answer waits for the reply to id.
func (s *lineSink) answer(t *testing.T, id int) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		out := s.buf.String()
		s.mu.Unlock()
		for _, line := range strings.Split(out, "\n") {
			var head struct {
				ID *float64 `json:"id"`
			}
			if json.Unmarshal([]byte(line), &head) == nil && head.ID != nil && int(*head.ID) == id {
				return line
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no answer to %d:\n%s", id, s.buf.String())
	return ""
}

func call(id int) string {
	return `{"jsonrpc":"2.0","id":` + itoa(id) + `,"method":"tools/call","params":{"name":"` + connectortest.ToolName + `","arguments":{}}}`
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// TestBridgeWakesOnce: a down bridge answers initialize, tools/list and ping
// from its copy, and keeps answering them while a wake runs; two calls side
// by side wake it once; the server sees initialize before the first call,
// and every call carries its session.
func TestBridgeWakesOnce(t *testing.T) {
	s := connectortest.New(t, "")
	release := make(chan struct{})
	var wakes atomic.Int32
	b := &Bridge{URL: s.MCPURL(), Lazy: stubListing(), Wake: func(context.Context) error {
		wakes.Add(1)
		<-release
		return nil
	}}
	in, w := io.Pipe()
	out := &lineSink{}
	done := make(chan error, 1)
	go func() { done <- b.Run(context.Background(), in, out) }()
	send := func(m string) { io.WriteString(w, m+"\n") }

	send(initMsg)
	send(initedNo)
	send(listMsg)
	if a := out.answer(t, 1); !strings.Contains(a, connectortest.Protocol) {
		t.Errorf("initialize from the copy: %s", a)
	}
	if a := out.answer(t, 2); !strings.Contains(a, connectortest.ToolName) {
		t.Errorf("tools/list from the copy: %s", a)
	}
	send(call(3))
	send(call(4))
	// While the wake waits, the copy still answers.
	send(`{"jsonrpc":"2.0","id":5,"method":"ping"}`)
	send(`{"jsonrpc":"2.0","id":6,"method":"tools/list"}`)
	if a := out.answer(t, 5); !strings.Contains(a, `"result":{}`) {
		t.Errorf("ping while down: %s", a)
	}
	out.answer(t, 6)
	if n := len(s.Requests()); n != 0 {
		t.Fatalf("the server saw %d requests before the wake: %+v", n, s.Requests())
	}
	close(release)
	for _, id := range []int{3, 4} {
		if a := out.answer(t, id); !strings.Contains(a, "called "+connectortest.ToolName) {
			t.Errorf("call %d: %s", id, a)
		}
	}
	w.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := wakes.Load(); n != 1 {
		t.Errorf("woke %d times", n)
	}
	reqs := s.Requests()
	if len(reqs) < 4 || reqs[0].RPC != "initialize" || reqs[1].RPC != "notifications/initialized" {
		t.Fatalf("requests = %+v", reqs)
	}
	calls := 0
	for _, r := range reqs[2:] {
		if r.RPC == "tools/call" {
			calls++
			if r.Session != connectortest.Session || r.Protocol != connectortest.Protocol {
				t.Errorf("a call went without its session: %+v", r)
			}
		}
	}
	if calls != 2 {
		t.Errorf("calls = %d", calls)
	}
	// The wake's own initialize never reaches the client.
	if strings.Count(out.buf.String(), connectortest.Protocol) != 1 {
		t.Errorf("the client got the wake's initialize:\n%s", out.buf.String())
	}
}

// A wake that fails answers that call with its words and leaves the bridge
// down; the next call wakes it.
func TestBridgeWakeFailureIsTheCallsAnswer(t *testing.T) {
	s := connectortest.New(t, "")
	var wakes atomic.Int32
	b := &Bridge{URL: s.MCPURL(), Lazy: stubListing(), Wake: func(context.Context) error {
		if wakes.Add(1) == 1 {
			return errors.New("connector marketing/x stays stopped, nothing was started: BLOCKED: memory used 80%")
		}
		return nil
	}}
	in, w := io.Pipe()
	out := &lineSink{}
	done := make(chan error, 1)
	go func() { done <- b.Run(context.Background(), in, out) }()
	io.WriteString(w, initMsg+"\n"+initedNo+"\n"+call(3)+"\n")
	if a := out.answer(t, 3); !strings.Contains(a, `"error"`) || !strings.Contains(a, "BLOCKED: memory used 80%") {
		t.Errorf("held call: %s", a)
	}
	io.WriteString(w, listMsg+"\n")
	out.answer(t, 2)
	if len(s.Requests()) != 0 {
		t.Errorf("a held wake reached the server: %+v", s.Requests())
	}
	io.WriteString(w, call(4)+"\n")
	if a := out.answer(t, 4); !strings.Contains(a, "called ") {
		t.Errorf("second call: %s", a)
	}
	w.Close()
	<-done
}

// The copy's keys: initialize by protocol version, a list by its params
// without _meta, nothing for a call.
func TestAnswerKey(t *testing.T) {
	for _, c := range []struct{ method, params, want string }{
		{"initialize", `{"protocolVersion":"2025-06-18","clientInfo":{"name":"codex"}}`, "initialize 2025-06-18"},
		{"tools/list", ``, "tools/list"},
		{"tools/list", `{}`, "tools/list"},
		{"tools/list", `{"_meta":{"progressToken":1}}`, "tools/list"},
		{"tools/list", `{"cursor":"p2"}`, `tools/list {"cursor":"p2"}`},
		{"prompts/list", `null`, "prompts/list"},
		{"tools/call", `{"name":"x"}`, ""},
		{"resources/read", `{}`, ""},
	} {
		if got := AnswerKey(c.method, json.RawMessage(c.params)); got != c.want {
			t.Errorf("AnswerKey(%s, %s) = %q, want %q", c.method, c.params, got, c.want)
		}
	}
}
