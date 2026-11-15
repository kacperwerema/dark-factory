package connectors

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Bridge joins an MCP client on stdin/stdout (newline-delimited JSON-RPC)
// to a remote server over Streamable HTTP: each message is one POST, and
// the answer (a JSON body, or the data: events of an SSE stream) goes back
// as one line per message. Header holds the connector's secret headers; it
// lives only in memory and the requests, and no error repeats it. Not
// covered: the server-initiated GET stream, resumption, OAuth and the
// legacy HTTP+SSE transport.
type Bridge struct {
	URL    string
	Header http.Header
	// Client is the HTTP client; nil is one that never follows a redirect
	// (a 3xx would carry the header elsewhere).
	Client *http.Client
	// Stderr gets the bridge's own notes (a notification the server
	// refused); nil discards them.
	Stderr io.Writer
	// Hold, when set, is asked at each tools/call whose tool isn't in
	// ReadTools (needs_approved_output:): an error is the call's answer and
	// the call never reaches the server. Asked per call, so an approval
	// during the session opens it.
	Hold      func() error
	ReadTools []string
	// Gate, when set, is asked at every tools/call with the tool's name and
	// its raw arguments, before Hold (at:): an error is the call's answer
	// and the call never reaches the server.
	Gate func(tool string, args json.RawMessage) error
	// Lazy and Wake, both set, start the bridge down (a per_run: tool with a
	// saved copy): what Lazy holds is answered from it, ping locally, and
	// notifications are dropped; the first other request (after Gate and
	// Hold pass) calls Wake, which starts the tool, then opens the server's
	// session with the client's own initialize, and goes on. A Wake that
	// fails is that request's answer, and the next request tries again.
	Lazy *Listing
	Wake func(ctx context.Context) error
	// Save, when set, gets each live answer the copy keeps (AnswerKey),
	// result only: an error answer isn't saved.
	Save func(key string, result json.RawMessage)

	// wakeMu keeps to one wake at a time; stateMu guards down and initMsg,
	// so a list is answered from the copy while a wake runs.
	wakeMu   sync.Mutex
	stateMu  sync.Mutex
	down     bool
	initMsg  []byte
	mu       sync.Mutex
	session  string
	protocol string
	outMu    sync.Mutex
	out      io.Writer
}

// BridgeClient is the bridge's default HTTP client: no redirects, bounded
// dial and TLS handshake, and no overall timeout, since a tool call may
// stream for as long as it runs.
func BridgeClient() *http.Client {
	t := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{
		Transport:     t,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// rpcHead is what the bridge reads of a client message.
type rpcHead struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// Run copies messages from in to the server and its answers to out until
// in ends, then waits for the requests still running and ends the session
// (DELETE, best effort). The initialize request runs alone, so every later
// request carries its Mcp-Session-Id, and notifications and replies go in
// the order they came (initialized reaches the server before the next
// request); only requests run side by side, so a long tool call doesn't
// hold up a ping.
func (b *Bridge) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	if b.Client == nil {
		b.Client = BridgeClient()
	}
	b.out = out
	b.down = b.Lazy != nil && b.Wake != nil
	r := bufio.NewReaderSize(in, 64*1024)
	var wg sync.WaitGroup
	for {
		line, err := r.ReadBytes('\n')
		if msg := bytes.TrimSpace(line); len(msg) > 0 {
			var head rpcHead
			_ = json.Unmarshal(msg, &head)
			msg = append([]byte{}, msg...)
			if b.fromCopy(msg, head) {
				// answered while the tool stays down
			} else if err := b.held(msg, head); err != nil {
				b.fail(head, err.Error())
			} else if head.Method == "initialize" || !head.hasID() {
				if b.wake(ctx, head) {
					b.send(ctx, msg, head)
				}
			} else {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if b.wake(ctx, head) {
						b.send(ctx, msg, head)
					}
				}()
			}
		}
		if err != nil {
			wg.Wait()
			b.close(ctx)
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// isDown reports whether the bridge still answers from its copy.
func (b *Bridge) isDown() bool {
	b.stateMu.Lock()
	defer b.stateMu.Unlock()
	return b.down
}

// fromCopy answers msg while the tool is down, when it can: a request the
// copy holds, a ping, or a notification (dropped: initialized is sent again
// at the wake). It keeps the client's initialize for the wake.
func (b *Bridge) fromCopy(msg []byte, head rpcHead) bool {
	if !b.isDown() {
		return false
	}
	if !head.hasID() {
		return true
	}
	var result json.RawMessage
	if head.Method == "ping" {
		result = json.RawMessage(`{}`)
	} else if r, ok := b.Lazy.Answer(head.Method, head.Params); ok {
		result = r
	} else {
		return false
	}
	if head.Method == "initialize" {
		b.stateMu.Lock()
		b.initMsg = msg
		b.stateMu.Unlock()
	}
	m, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": head.ID, "result": result})
	b.write(m)
	return true
}

// wakeID is the id of the bridge's own initialize at a wake.
const wakeID = `"connector-bridge-wake"`

// wake brings a down bridge up before head is sent: Wake starts the tool,
// and the client's initialize, if the copy answered it, opens the server's
// session (its answer read, never written out) with initialized after it.
// One wake at a time; false means head got the failure as its answer and
// the bridge stays down.
func (b *Bridge) wake(ctx context.Context, head rpcHead) bool {
	b.wakeMu.Lock()
	defer b.wakeMu.Unlock()
	b.stateMu.Lock()
	down, initMsg := b.down, b.initMsg
	b.stateMu.Unlock()
	if !down {
		return true
	}
	if !head.hasID() {
		return false // a notification while down: dropped
	}
	if err := b.Wake(ctx); err != nil {
		b.fail(head, err.Error())
		return false
	}
	if initMsg != nil {
		var m map[string]json.RawMessage
		if json.Unmarshal(initMsg, &m) != nil {
			b.fail(head, "the client's initialize could not be read again")
			return false
		}
		m["id"] = json.RawMessage(wakeID)
		msg, _ := json.Marshal(m)
		ih := rpcHead{ID: json.RawMessage(wakeID), Method: "initialize", Params: m["params"]}
		var reply []byte
		if why := b.post(ctx, msg, ih, func(r []byte) { reply = r }); why != "" {
			b.fail(head, "the tool started, and its server's initialize failed: "+why)
			return false
		}
		var got struct {
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(reply, &got) == nil && got.Error != nil {
			b.fail(head, "the tool started, and its server refused initialize: "+got.Error.Message)
			return false
		}
		note := []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
		if why := b.post(ctx, note, rpcHead{Method: "notifications/initialized"}, func([]byte) {}); why != "" && b.Stderr != nil {
			fmt.Fprintf(b.Stderr, "connector bridge: notifications/initialized: %s\n", why)
		}
	}
	b.stateMu.Lock()
	b.down = false
	b.stateMu.Unlock()
	return true
}

// held is the per-call checks' answer for msg: nil unless msg is a
// tools/call that Gate refuses, or that names a tool outside ReadTools
// while Hold refuses. A call whose tool can't be read is held too.
func (b *Bridge) held(msg []byte, head rpcHead) error {
	if (b.Hold == nil && b.Gate == nil) || head.Method != "tools/call" {
		return nil
	}
	var call struct {
		Params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"params"`
	}
	_ = json.Unmarshal(msg, &call)
	if b.Gate != nil {
		if err := b.Gate(call.Params.Name, call.Params.Arguments); err != nil {
			return err
		}
	}
	if b.Hold == nil {
		return nil
	}
	for _, t := range b.ReadTools {
		if call.Params.Name != "" && call.Params.Name == t {
			return nil
		}
	}
	return b.Hold()
}

// hasID reports whether head is a request (not a notification or answer).
func (h rpcHead) hasID() bool {
	return len(h.ID) > 0 && string(h.ID) != "null" && h.Method != ""
}

func (b *Bridge) newRequest(ctx context.Context, method string, body []byte) (*http.Request, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, b.URL, rd)
	if err != nil {
		return nil, err
	}
	for k, vs := range b.Header {
		req.Header[k] = vs
	}
	b.mu.Lock()
	if b.session != "" {
		req.Header.Set("Mcp-Session-Id", b.session)
	}
	if b.protocol != "" {
		req.Header.Set("MCP-Protocol-Version", b.protocol)
	}
	b.mu.Unlock()
	return req, nil
}

// send posts one message and writes what comes back. A request whose call
// fails gets a JSON-RPC error answer, so the client never waits forever.
func (b *Bridge) send(ctx context.Context, msg []byte, head rpcHead) {
	if why := b.post(ctx, msg, head, b.write); why != "" {
		b.fail(head, why)
	}
}

// post posts one message and gives each message that comes back to sink;
// it returns why the call failed, "" when it didn't. A live answer the copy
// keeps goes to Save.
func (b *Bridge) post(ctx context.Context, msg []byte, head rpcHead, sink func([]byte)) string {
	req, err := b.newRequest(ctx, http.MethodPost, msg)
	if err != nil {
		return "the request could not be built"
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := b.Client.Do(req)
	if err != nil {
		return transportWords(err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return fmt.Sprintf("the server answered a redirect (%d); connector exec never follows one", resp.StatusCode)
	case resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent:
		return ""
	case resp.StatusCode >= 400:
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return fmt.Sprintf("the server answered %s", resp.Status)
	}
	if head.Method == "initialize" {
		if s := resp.Header.Get("Mcp-Session-Id"); s != "" {
			b.mu.Lock()
			b.session = s
			b.mu.Unlock()
		}
	}
	answered := false
	emit := func(m []byte) {
		var c bytes.Buffer
		if json.Compact(&c, m) != nil {
			return
		}
		var got struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
		}
		_ = json.Unmarshal(c.Bytes(), &got)
		if got.Method == "" && bytes.Equal(got.ID, head.ID) {
			answered = true
			if head.Method == "initialize" {
				var r struct {
					ProtocolVersion string `json:"protocolVersion"`
				}
				if json.Unmarshal(got.Result, &r) == nil && r.ProtocolVersion != "" {
					b.mu.Lock()
					b.protocol = r.ProtocolVersion
					b.mu.Unlock()
				}
			}
			if b.Save != nil && len(got.Result) > 0 && string(got.Result) != "null" {
				if k := AnswerKey(head.Method, head.Params); k != "" {
					b.Save(k, got.Result)
				}
			}
		}
		sink(c.Bytes())
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	switch {
	case strings.HasPrefix(ct, "text/event-stream"):
		readSSE(resp.Body, emit)
	default:
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "the answer could not be read"
		}
		body = bytes.TrimSpace(body)
		if len(body) == 0 {
			break
		}
		if body[0] == '[' {
			var list []json.RawMessage
			if json.Unmarshal(body, &list) == nil {
				for _, m := range list {
					emit(m)
				}
				break
			}
		}
		emit(body)
	}
	if head.hasID() && !answered {
		return "the server's answer held no reply to this request"
	}
	return ""
}

// readSSE calls emit with each event's data (data: lines joined by \n).
func readSSE(r io.Reader, emit func([]byte)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 64<<20)
	var data [][]byte
	event := ""
	flush := func() {
		if len(data) > 0 && (event == "" || event == "message") {
			emit(bytes.Join(data, []byte("\n")))
		}
		data, event = nil, ""
	}
	for sc.Scan() {
		line := sc.Bytes()
		switch {
		case len(line) == 0:
			flush()
		case line[0] == ':':
		case bytes.HasPrefix(line, []byte("data:")):
			data = append(data, append([]byte{}, bytes.TrimPrefix(bytes.TrimPrefix(line, []byte("data:")), []byte(" "))...))
		case bytes.HasPrefix(line, []byte("event:")):
			event = strings.TrimSpace(string(line[len("event:"):]))
		}
	}
	flush()
}

// fail answers request head with a JSON-RPC error; a notification's
// failure goes to Stderr only.
func (b *Bridge) fail(head rpcHead, why string) {
	if !head.hasID() {
		if b.Stderr != nil {
			fmt.Fprintf(b.Stderr, "connector bridge: %s: %s\n", head.Method, why)
		}
		return
	}
	m, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      head.ID,
		"error":   map[string]any{"code": -32000, "message": "connector bridge: " + why},
	})
	b.write(m)
}

func (b *Bridge) write(m []byte) {
	b.outMu.Lock()
	defer b.outMu.Unlock()
	b.out.Write(append(m, '\n'))
}

// close ends the session the server opened, if any.
func (b *Bridge) close(ctx context.Context) {
	b.mu.Lock()
	open := b.session != ""
	b.mu.Unlock()
	if !open {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := b.newRequest(ctx, http.MethodDelete, nil)
	if err != nil {
		return
	}
	if resp, err := b.Client.Do(req); err == nil {
		resp.Body.Close()
	}
}

// transportWords is a failed call as words: the kind of failure, never
// the request (url.Error would repeat the URL, which may hold a query).
func transportWords(err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, context.Canceled):
		return "the call was cancelled"
	case errors.As(err, &ne) && ne.Timeout():
		return "the server did not answer in time"
	}
	var op *net.OpError
	if errors.As(err, &op) {
		return "could not reach the server (" + op.Op + ")"
	}
	return "could not reach the server"
}
