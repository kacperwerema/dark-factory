// Package connectors is a department's outside tools at run time: who may
// use one (Allowed), the child environment with its secrets (Env), the
// read-only health checks and their log (Check, LoadChecks), the listing the
// CLI and the viewer show (List), and the MCP config an agent is launched
// with (MCPConfig, CodexFlags). Config is tracker.Connector; secrets are
// only ever referenced (factory/department-connectors#D-3).
package connectors

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// CheckTimeout is how long a check: may run before it counts as failing.
var CheckTimeout = 20 * time.Second

// Check results.
const (
	ResultOK      = "ok"
	ResultFailing = "failing"
	ResultMissing = "missing"
	ResultNoCheck = "no-check"
	// ResultStopped is a per_run: tool no run holds: down on purpose, not run.
	ResultStopped = "stopped"
)

// Find is the connector addr names (<dept key>/<name>) and its department.
// The error lists what the department has.
func Find(p *tracker.Project, addr string) (*tracker.Department, *tracker.Connector, error) {
	key, name, ok := strings.Cut(addr, "/")
	if !ok || key == "" || name == "" {
		return nil, nil, fmt.Errorf("name a connector as <department>/<name>, e.g. marketing/search-console (issue-cli connectors)")
	}
	d := p.DepartmentByKey(key)
	if d == nil {
		return nil, nil, fmt.Errorf("no department %q with connectors (issue-cli connectors)", key)
	}
	for i := range d.Connectors {
		if d.Connectors[i].Name == name {
			return d, &d.Connectors[i], nil
		}
	}
	var names []string
	for _, c := range d.Connectors {
		names = append(names, tracker.ConnectorAddress(d.Name, c.Name))
	}
	if len(names) == 0 {
		return d, nil, fmt.Errorf("%s has no connectors (issue-cli connectors)", d.Name)
	}
	return d, nil, fmt.Errorf("%s has no connector %q; it has: %s", d.Name, name, strings.Join(names, ", "))
}

// Caller is who runs a connector: a department's lead or worker, the CEO, a
// Product worker, or the owner's own shell.
type Caller struct {
	// Department is the caller's department ("" for Product or the CEO).
	Department string
	// Lead marks a lead (the CEO when Department is "").
	Lead bool
	// Owner is the owner's shell (--as-owner outside any agent session).
	Owner bool
	// Agent marks any agent session (a worker or a lead).
	Agent bool
	// Issue is a worker's issue slug ("" for a lead or the owner): the
	// shelf a needs_approved_output: connector reads.
	Issue string
}

// HoldError is the needs_approved_output: refusal of connector addr for
// caller, given the shelf's answer for the caller's issue (shelf, nil when
// it has an approved output); nil when nothing holds it. Only a worker has
// an issue: a lead or the CEO is refused, naming the two ways through.
func HoldError(addr string, caller Caller, shelf error) error {
	switch {
	case caller.Owner:
		return nil
	case caller.Issue == "":
		return fmt.Errorf("connector %s needs an approved output (needs_approved_output: true): it runs for a worker whose issue's output the owner approved on the shelf, or for the owner with --as-owner in their own shell", addr)
	case shelf != nil:
		return fmt.Errorf("connector %s needs an approved output (needs_approved_output: true) and is held: %w", addr, shelf)
	}
	return nil
}

// Allowed refuses a caller outside connector owner's department, naming
// the department and its lead (factory/department-connectors#D-4, D-7).
func Allowed(p *tracker.Project, owner *tracker.Department, c Caller) error {
	if c.Owner {
		return nil
	}
	lead := tracker.DepartmentLeadName(p, owner.Name)
	switch {
	case !c.Agent:
		return fmt.Errorf("connectors are for %s's agents: run it from its lead (%s) or one of its workers, or add --as-owner in your own shell", owner.Name, lead)
	case c.Department == "" && c.Lead:
		return fmt.Errorf("the CEO does not use %s's connectors: its lead %s or its workers do (issue-cli connectors %s)", owner.Name, lead, tracker.DepartmentKey(owner.Name))
	case !strings.EqualFold(c.Department, owner.Name):
		from := "Product"
		if c.Department != "" {
			from = c.Department
		}
		return fmt.Errorf("this connector belongs to %s; a %s agent may not use it. Ask %s's lead %s, or the CEO to route the work there", owner.Name, from, owner.Name, lead)
	}
	return nil
}

// Env is base plus c's secrets, read now. A secret that is missing or whose
// reference is invalid is an error naming the reference, never the value;
// an optional secret that isn't set is left out.
func Env(c *tracker.Connector, base []string) ([]string, error) {
	env := append([]string{}, base...)
	for _, s := range c.SecretStates() {
		if s.Header {
			continue
		}
		if s.Err != "" {
			return nil, fmt.Errorf("secret %s: %s", s.Var, s.Err)
		}
		ref, _ := tracker.ParseSecretRef(c.Secrets[s.Var])
		v, ok := tracker.SecretValue(ref)
		if !ok && s.Optional {
			continue
		}
		if !ok {
			return nil, fmt.Errorf("secret %s (%s) is missing on this machine: %s", s.Var, s.Ref, howToSet(ref))
		}
		env = append(env, s.Var+"="+v)
	}
	return env, nil
}

// Headers are c's headers: with their secrets read now, for the HTTP
// bridge only. Like Env, the error names the reference, never the value.
func Headers(c *tracker.Connector) (http.Header, error) {
	h := http.Header{}
	for _, s := range c.SecretStates() {
		if !s.Header {
			continue
		}
		if s.Err != "" {
			return nil, fmt.Errorf("header %s: %s", s.Var, s.Err)
		}
		hv, _ := tracker.ParseHeaderValue(c.Headers[s.Var])
		v, ok := tracker.SecretValue(hv.Ref)
		if !ok {
			return nil, fmt.Errorf("header %s (%s) is missing on this machine: %s", s.Var, s.Ref, howToSet(hv.Ref))
		}
		if strings.ContainsAny(v, "\r\n") {
			return nil, fmt.Errorf("header %s (%s): the value holds a line break", s.Var, s.Ref)
		}
		h.Set(s.Var, hv.Prefix+v)
	}
	return h, nil
}

func howToSet(ref tracker.SecretRef) string {
	if ref.Scheme == "keychain" {
		acct := ""
		if ref.Account != "" {
			acct = " -a " + ref.Account
		}
		return fmt.Sprintf("the owner adds it with security add-generic-password -s %s%s -w", ref.Name, acct)
	}
	return fmt.Sprintf("the owner exports %s in the shell profile agents start from", ref.Name)
}

// Command is what exec runs for c: the configured command and args for an
// mcp connector, else args (after --), else the configured command.
func Command(c *tracker.Connector, args []string) ([]string, error) {
	if c.URL != "" {
		return nil, fmt.Errorf("connector %s is a remote MCP server (url:): connector exec bridges it itself, no command after --", c.Name)
	}
	if c.Kind == tracker.ConnectorMCP || len(args) == 0 {
		if strings.TrimSpace(c.Command) == "" {
			return nil, fmt.Errorf("connector %s has no command: in projects.yaml; pass one after --", c.Name)
		}
		return append([]string{c.Command}, append(append([]string{}, c.Args...), args...)...), nil
	}
	return args, nil
}

// Record is one health check in <issues>/.connectors/checks.jsonl.
type Record struct {
	Connector string    `json:"connector"`
	Date      time.Time `json:"date"`
	Result    string    `json:"result"`
	Exit      int       `json:"exit"`
	MS        int64     `json:"ms"`
	// Optional is each optional secret, set or not, when the check ran.
	Optional []OptionalSecret `json:"optional,omitempty"`
}

// OptionalSecret is one optional secret's env var and whether it was set.
type OptionalSecret struct {
	Var string `json:"var"`
	Set bool   `json:"set"`
}

// LogPath is the checks log, with the work files.
func LogPath(p *tracker.Project) string {
	return filepath.Join(p.IssueDir, ".connectors", "checks.jsonl")
}

// RunCheck runs c's check: with its secrets, CheckTimeout and its output
// thrown away (factory/department-connectors#D-5).
func RunCheck(ctx context.Context, dept string, c *tracker.Connector, now func() time.Time) Record {
	r := Record{Connector: tracker.ConnectorAddress(dept, c.Name), Date: now().UTC()}
	for _, s := range c.SecretStates() {
		if s.Optional && s.Err == "" {
			r.Optional = append(r.Optional, OptionalSecret{Var: s.Var, Set: s.Set})
		}
	}
	if strings.TrimSpace(c.Check) == "" {
		r.Result = ResultNoCheck
		return r
	}
	env, err := Env(c, os.Environ())
	if err != nil {
		r.Result = ResultMissing
		return r
	}
	ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", c.Check)
	cmd.Env = env
	cmd.WaitDelay = time.Second
	start := time.Now()
	err = cmd.Run()
	r.MS = time.Since(start).Milliseconds()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		r.Result = ResultOK
	case errors.As(err, &exitErr):
		r.Result, r.Exit = ResultFailing, exitErr.ExitCode()
	default:
		r.Result, r.Exit = ResultFailing, -1
	}
	if ctx.Err() != nil {
		r.Exit = -1
	}
	return r
}

// Append adds records to the checks log.
func Append(p *tracker.Project, recs ...Record) error {
	path := LogPath(p)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, r := range recs {
		b, _ := json.Marshal(r)
		if _, err := f.Write(append(b, '\n')); err != nil {
			return err
		}
	}
	return nil
}

// LoadChecks is the newest check of each connector, by address. Lines that
// don't parse are skipped.
func LoadChecks(p *tracker.Project) map[string]Record {
	out := map[string]Record{}
	f, err := os.Open(LogPath(p))
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) != nil || r.Connector == "" {
			continue
		}
		if old, ok := out[r.Connector]; !ok || !r.Date.Before(old.Date) {
			out[r.Connector] = r
		}
	}
	return out
}

// Status is one connector as the CLI and the viewer list it.
type Status struct {
	Department string                `json:"department"`
	Key        string                `json:"department_key"`
	Name       string                `json:"name"`
	Address    string                `json:"address"`
	Kind       string                `json:"kind"`
	Secrets    []tracker.SecretState `json:"secrets"`
	Missing    bool                  `json:"missing"`
	Sends      bool                  `json:"sends"`
	DenyTools  []string              `json:"deny_tools,omitempty"`
	// Get are a kind: rest connector's get: patterns: what it may read.
	Get []string `json:"get,omitempty"`
	// NeedsApprovedOutput is held by connector exec until the issue's output
	// is approved; ReadTools pass before.
	NeedsApprovedOutput bool     `json:"needs_approved_output,omitempty"`
	ReadTools           []string `json:"read_tools,omitempty"`
	// At is the connector's at: block: the tools each status allows.
	At       map[string]tracker.ConnectorAt `json:"at,omitempty"`
	Note     string                         `json:"note,omitempty"`
	HasCheck bool                           `json:"has_check"`
	Last     *Record                        `json:"last_check,omitempty"`
	// PerRun is a tool started per run; Running and Holders say how it is
	// now, LastRun what its last start or stop did.
	PerRun  bool       `json:"per_run,omitempty"`
	Running bool       `json:"running,omitempty"`
	Holders int        `json:"holders,omitempty"`
	LastRun *RunRecord `json:"last_run,omitempty"`
	// ToolsSaved is when a url: tool's copy of its server's tools was last
	// saved: with one, a session starts the tool at its first call.
	ToolsSaved *time.Time `json:"tools_saved,omitempty"`
}

// RunWords is a per_run: tool's state: "starts per run, stopped" or
// "starts per run, running (2 runs)"; "" for any other connector.
func (s Status) RunWords() string {
	switch {
	case !s.PerRun:
		return ""
	case s.Holders > 1:
		return fmt.Sprintf("starts per run, running (%d runs)", s.Holders)
	case s.Running:
		return "starts per run, running"
	case s.ToolsSaved != nil:
		return "starts per run at the first call, stopped"
	}
	return "starts per run, stopped"
}

// Failing is a connector the owner should look at: a secret missing, its
// last check failing, or its last start or stop failing.
func (s Status) Failing() bool {
	return s.Problem() != ""
}

// Problem says what is wrong in a few words, "" when nothing is.
func (s Status) Problem() string {
	switch {
	case s.Missing:
		return "secret missing"
	case s.LastRun != nil && s.LastRun.Event == RunStopFailed:
		return "stop failing"
	case s.LastRun != nil && s.LastRun.Event == RunStartFailed:
		return "start failing"
	case s.Last != nil && (s.Last.Result == ResultFailing || s.Last.Result == ResultMissing):
		return "check failing"
	}
	return ""
}

// List is every connector of department dept ("" for all), in
// projects.yaml order.
func List(p *tracker.Project, dept string) []Status {
	checks := LoadChecks(p)
	runs := LoadRuns(p)
	var out []Status
	for _, d := range p.Departments {
		if dept != "" && tracker.DepartmentKey(d.Name) != tracker.DepartmentKey(dept) {
			continue
		}
		for _, c := range d.Connectors {
			addr := tracker.ConnectorAddress(d.Name, c.Name)
			s := Status{Department: d.Name, Key: tracker.DepartmentKey(d.Name), Name: c.Name, Address: addr, Kind: c.Kind,
				Secrets: c.SecretStates(), Sends: c.Sends, DenyTools: c.DeniedTools(), Note: c.Note,
				NeedsApprovedOutput: c.NeedsApprovedOutput, ReadTools: c.ReadToolNames(), At: c.At, HasCheck: strings.TrimSpace(c.Check) != ""}
			s.Missing = len(c.Missing()) > 0
			if c.Kind == tracker.ConnectorRest {
				s.Get = RestPatterns(&c)
			}
			if r, ok := checks[addr]; ok {
				s.Last = &r
			}
			if c.PerRun != nil {
				s.PerRun = true
				s.Running, s.Holders = Up(p, d.Name, &c)
				if r, ok := runs[addr]; ok {
					s.LastRun = &r
				}
				if t := ListingSaved(p, d.Name, &c); !t.IsZero() {
					s.ToolsSaved = &t
				}
			}
			out = append(out, s)
		}
	}
	return out
}

// Server is one MCP server an agent is launched with: always the wrapper,
// so no config file holds a secret.
type Server struct {
	Name    string            `json:"-"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
	// DenyTools are the server's tools the agent CLI switches off; never
	// in mcp.json (claude reads them from --disallowedTools).
	DenyTools []string `json:"-"`
	// StartSeconds is how long a per_run: tool may take to come up, 0 for
	// any other: the agent CLI must wait that long for the server.
	StartSeconds int `json:"-"`
}

// Servers are department dept's mcp connectors whose secrets are set, each
// run through issue-cli connector exec. env is the session's own
// ISSUE_VIEWER_* variables, passed on so the wrapper knows its caller.
func Servers(p *tracker.Project, dept string, env map[string]string) []Server {
	d := p.DepartmentNamed(dept)
	if d == nil {
		return nil
	}
	var out []Server
	for _, c := range d.Connectors {
		if c.Kind != tracker.ConnectorMCP || len(c.Missing()) > 0 {
			continue
		}
		args := []string{}
		if p.Slug != "" {
			args = append(args, "--project", p.Slug)
		}
		args = append(args, "connector", "exec", tracker.ConnectorAddress(d.Name, c.Name))
		s := Server{Name: c.Name, Command: "issue-cli", Args: args, Env: env, DenyTools: c.DeniedTools()}
		if c.PerRun != nil {
			s.StartSeconds = int(c.PerRun.Wait().Seconds())
		}
		out = append(out, s)
	}
	return out
}

// MCPConfig is claude's --mcp-config file for servers.
func MCPConfig(servers []Server) []byte {
	m := map[string]Server{}
	for _, s := range servers {
		m[s.Name] = s
	}
	b, _ := json.MarshalIndent(map[string]any{"mcpServers": m}, "", "  ")
	return append(b, '\n')
}

// CodexFlags are codex's -c overrides for servers.
func CodexFlags(servers []Server) []string {
	var out []string
	for _, s := range servers {
		args, _ := json.Marshal(s.Args)
		out = append(out, "-c", fmt.Sprintf("mcp_servers.%s.command=%q", s.Name, s.Command),
			"-c", fmt.Sprintf("mcp_servers.%s.args=%s", s.Name, args))
		for k, v := range s.Env {
			out = append(out, "-c", fmt.Sprintf("mcp_servers.%s.env.%s=%q", s.Name, k, v))
		}
		if len(s.DenyTools) > 0 {
			off, _ := json.Marshal(s.DenyTools)
			out = append(out, "-c", fmt.Sprintf("mcp_servers.%s.disabled_tools=%s", s.Name, off))
		}
		if s.StartSeconds > 0 {
			// A url: server's tool may start at its first call, so that
			// call gets codex's own allowance plus the start.
			out = append(out, "-c", fmt.Sprintf("mcp_servers.%s.startup_timeout_sec=%d", s.Name, s.StartSeconds+startMargin),
				"-c", fmt.Sprintf("mcp_servers.%s.tool_timeout_sec=%d", s.Name, codexToolTimeout+s.StartSeconds+startMargin))
		}
	}
	return out
}

// startMargin is seconds an agent CLI waits past a tool's own timeout, so a
// start that fails is reported by the tool's run, not the CLI's clock.
const startMargin = 10

// codexToolTimeout is codex's default tool_timeout_sec.
const codexToolTimeout = 60

// ClaudeStartEnv is the session env that lets claude wait for servers'
// per_run: tools (MCP_TIMEOUT, its one setting for all servers, in ms); nil
// when no server starts a tool.
func ClaudeStartEnv(servers []Server) []string {
	most := 0
	for _, s := range servers {
		most = max(most, s.StartSeconds)
	}
	if most == 0 {
		return nil
	}
	return []string{fmt.Sprintf("MCP_TIMEOUT=%d", (most+startMargin)*1000)}
}

// ClaudeDisallowed is claude's --disallowedTools list for servers' denied
// tools (mcp__<server>__<tool>, comma-separated); "" when none.
func ClaudeDisallowed(servers []Server) string {
	var out []string
	for _, s := range servers {
		for _, t := range s.DenyTools {
			out = append(out, "mcp__"+s.Name+"__"+t)
		}
	}
	return strings.Join(out, ",")
}

// DeniedCount is how many tools servers switch off.
func DeniedCount(servers []Server) int {
	n := 0
	for _, s := range servers {
		n += len(s.DenyTools)
	}
	return n
}

// KnownRisky are tools of outside MCP servers the engine knows that send
// outside the machine or start paid recurring work, by server id
// (factory/turn-off-single-tools-on-a-connector-deny-tools-mapped-to-the-agent-clis-disallowed-tools#D-1, #D-2).
// A row is data about an outside server, not code for one department.
var KnownRisky = map[string][]string{
	// OpenSEO (MIT): IndexNow pings search engines, report sharing makes a
	// report public, the AI visibility schedule is paid and recurring.
	"openseo": {"submit_indexnow_urls", "set_report_sharing", "set_ai_visibility_schedule"},
}

// KnownServer is the KnownRisky id c runs: its name, or a word of its
// command, args or url: that names one; "" when the engine doesn't know it.
func KnownServer(c tracker.Connector) string {
	words := append([]string{c.Name, c.Command, c.URL}, c.Args...)
	for _, w := range words {
		lw := strings.ToLower(w)
		for id := range KnownRisky {
			if lw == id || strings.Contains(lw, id) {
				return id
			}
		}
	}
	return ""
}

// RiskyAllowed are c's known sending or spending tools left on: none for a
// sends: true connector (an owner_only gate covers it), one that isn't
// mcp, or one the engine doesn't know.
func RiskyAllowed(c tracker.Connector) []string {
	if c.Sends || c.Kind != tracker.ConnectorMCP {
		return nil
	}
	id := KnownServer(c)
	if id == "" {
		return nil
	}
	off := map[string]bool{}
	for _, t := range c.DeniedTools() {
		off[t] = true
	}
	var out []string
	for _, t := range KnownRisky[id] {
		if !off[t] {
			out = append(out, t)
		}
	}
	return out
}
