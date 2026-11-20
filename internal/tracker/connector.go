package tracker

import (
	"fmt"
	"maps"
	"net"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Connector is one outside tool a department's agents may use: an MCP
// server they load, or a command run through issue-cli connector exec.
// Config holds names and secret references only, never a value
// (factory/department-connectors#D-2).
type Connector struct {
	Name string `yaml:"name"`
	// Kind is ConnectorMCP, ConnectorTool or ConnectorRest.
	Kind    string   `yaml:"kind"`
	Command string   `yaml:"command"`
	Args    []string `yaml:"args"`
	// URL is a remote MCP server (Streamable HTTP) instead of a command:
	// connector exec bridges stdio to it in-process. For kind: rest it is the
	// API's base the get: paths are read under. https, or http on a
	// loopback host (CheckConnectorURL).
	URL string `yaml:"url"`
	// Get is a kind: rest connector's allow-list: the paths under URL that
	// connector exec may read, each a path.Match pattern ("*" is one
	// segment). GET is the only method there is.
	Get []string `yaml:"get"`
	// Headers maps an HTTP header to where its value is kept, optionally
	// after a literal prefix: "Bearer keychain:openseo" (ParseHeaderValue).
	// Only for a connector with url:.
	Headers map[string]string `yaml:"headers"`
	// Secrets maps the env var the child gets to where the owner keeps its
	// value: env:<VAR> or keychain:<service>[/<account>].
	Secrets map[string]string `yaml:"secrets"`
	// Optional names secrets (by env var) the connector runs without: one
	// that isn't set is skipped, not missing.
	Optional []string `yaml:"optional"`
	// Check is a read-only shell command; exit 0 is healthy.
	Check string `yaml:"check"`
	// Sends marks a connector that can publish or spend: only after the
	// owner approved the output (factory/department-connectors#D-6).
	Sends bool `yaml:"sends"`
	// NeedsApprovedOutput makes connector exec hold the connector until the
	// caller's issue has an approved output on the shelf (the newest version,
	// none still in review): a tool doesn't start, a url: server's tools/call
	// is refused unless it names one of ReadTools. Opt-in, since sends: alone
	// also marks tools run on the owner's word before any output exists.
	NeedsApprovedOutput bool `yaml:"needs_approved_output"`
	// ReadTools are a url: server's tools the hold lets through: the ones
	// that only read, which the work needs before there is an output.
	ReadTools []string `yaml:"read_tools"`
	// At limits a url: server's tools/call by the caller's issue status,
	// read at each call: a status it doesn't name allows no tool. Only the
	// bridge sees the calls, so it does nothing on a command: server.
	At map[string]ConnectorAt `yaml:"at"`
	// DenyTools are an mcp connector's tools the agent CLI switches off at
	// launch (claude --disallowedTools, codex disabled_tools): a research
	// server's few tools that send or spend.
	DenyTools []string `yaml:"deny_tools"`
	// PerRun makes a heavy tool run only while a run needs it: connector
	// exec starts it under mem-guard and stops it after.
	PerRun *PerRun `yaml:"per_run"`
	// Exception is the decision ref (<slug>#D-<n>) recording the owner's
	// licence exception for a tool the partners catalog knows as AGPL:
	// lint check departments warns without it (docs/departments.md#the-licence-rule).
	Exception string `yaml:"exception"`
	Note      string `yaml:"note"`
}

// DefaultRunTimeout is per_run: timeout: when it isn't set, in seconds.
const DefaultRunTimeout = 60

// PerRun is how to start, probe and stop a connector's tool, three shell
// commands the owner writes. They get the connector's secrets, and their
// output is thrown away.
type PerRun struct {
	// Start launches the tool and returns.
	Start string `yaml:"start"`
	// Ready is read-only: exit 0 once the tool answers. Polled after Start.
	Ready string `yaml:"ready"`
	Stop  string `yaml:"stop"`
	// Timeout is seconds for start and ready together, and for stop.
	Timeout int `yaml:"timeout"`
	// Tool names the tool when other connectors start it too, in this
	// project or another: connectors with one Tool share one lease, so the
	// last run out stops it. Empty: the connector's lease is its own.
	Tool string `yaml:"tool"`
}

// ToolIDRe is what per_run: tool: may be: it becomes a folder name.
var ToolIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ToolProblem says why per_run: tool: can't be used, "" when it is unset
// or fine.
func (r *PerRun) ToolProblem() string {
	if r.Tool == "" || ToolIDRe.MatchString(r.Tool) {
		return ""
	}
	return "must be a short id ([a-z0-9._-], starting with a letter or digit, up to 64)"
}

// Wait is the block's timeout.
func (r *PerRun) Wait() time.Duration {
	if r.Timeout > 0 {
		return time.Duration(r.Timeout) * time.Second
	}
	return DefaultRunTimeout * time.Second
}

// MissingSteps names the commands the block lacks.
func (r *PerRun) MissingSteps() []string {
	var out []string
	for _, s := range []struct{ name, v string }{{"start:", r.Start}, {"ready:", r.Ready}, {"stop:", r.Stop}} {
		if strings.TrimSpace(s.v) == "" {
			out = append(out, s.name)
		}
	}
	return out
}

// Connector kinds.
const (
	ConnectorMCP  = "mcp"
	ConnectorTool = "tool"
	// ConnectorRest is a read-only REST API: connector exec makes GETs on
	// its get: paths in-process, with its headers: secrets.
	ConnectorRest = "rest"
)

// ToolNameRe is what a deny_tools: entry may be: an MCP tool name, safe in
// a shell word and a TOML string.
var ToolNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// ArgPathRe is a deny_args: argument key written as a path into the call's
// arguments: names joined by ".", each name followed by any number of "[]"
// (every item of an array), e.g. socialPost[].type.
var ArgPathRe = regexp.MustCompile(`^[A-Za-z0-9_-]+(\[\])*(\.[A-Za-z0-9_-]+(\[\])*)*$`)

// IsArgPath says whether a deny_args: key is meant as a path: it holds "."
// or a bracket. Any other key is a top-level argument's name, matched as
// written.
func IsArgPath(key string) bool {
	return strings.ContainsAny(key, ".[]")
}

// ArgPathError is what is wrong with a deny_args: key: nil for a plain
// argument name or a well-formed path.
func ArgPathError(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("an empty argument name")
	}
	if !IsArgPath(key) || ArgPathRe.MatchString(key) {
		return nil
	}
	return fmt.Errorf("%q is not an argument path (name, name.sub, name[].sub: letters, digits, _ or - in each name)", key)
}

// DeniedTools are c's deny_tools: that are tool names, each once, in
// config order; nil for a connector that isn't mcp. Lint reports the rest.
func (c Connector) DeniedTools() []string {
	if c.Kind != ConnectorMCP {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, t := range c.DenyTools {
		t = strings.TrimSpace(t)
		if !ToolNameRe.MatchString(t) || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// ReadToolNames are c's read_tools: that are tool names, each once, in
// config order. Lint reports the rest.
func (c Connector) ReadToolNames() []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range c.ReadTools {
		t = strings.TrimSpace(t)
		if !ToolNameRe.MatchString(t) || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// ConnectorAt is one status's entry in a connector's at:: Allow, when
// set, is every tool callable at that status (empty: every tool);
// DenyArgs refuses a tool's call whose argument holds one of the values
// (tool → argument → values); the argument is a top-level name or a path
// into nested objects and arrays (ArgPathRe).
type ConnectorAt struct {
	Allow    []string                       `yaml:"allow" json:"allow,omitempty"`
	DenyArgs map[string]map[string][]string `yaml:"deny_args" json:"deny_args,omitempty"`
}

// AtStatuses are c's at: keys, sorted.
func (c Connector) AtStatuses() []string {
	return slices.Sorted(maps.Keys(c.At))
}

// ConnectorKinds are the kinds lint accepts.
var ConnectorKinds = []string{ConnectorMCP, ConnectorTool, ConnectorRest}

// ConnectorNameRe is what a connector's name may be.
var ConnectorNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// SecretRef is a parsed secret reference.
type SecretRef struct {
	// Scheme is "env" or "keychain".
	Scheme string
	// Name is the env var, or the Keychain service.
	Name string
	// Account is the Keychain account; optional.
	Account string
}

func (r SecretRef) String() string {
	if r.Account != "" {
		return r.Scheme + ":" + r.Name + "/" + r.Account
	}
	return r.Scheme + ":" + r.Name
}

var envVarRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ParseSecretRef parses env:<VAR> or keychain:<service>[/<account>]. The
// error never repeats raw, which may be a pasted value.
func ParseSecretRef(raw string) (SecretRef, error) {
	raw = strings.TrimSpace(raw)
	scheme, rest, ok := strings.Cut(raw, ":")
	switch {
	case !ok:
		return SecretRef{}, fmt.Errorf("not env:<VAR> or keychain:<service>: it looks like a secret value, keep the value in env: or keychain:")
	case scheme == "env":
		if !envVarRe.MatchString(rest) {
			return SecretRef{}, fmt.Errorf("env: wants a variable name ([A-Za-z_][A-Za-z0-9_]*)")
		}
		return SecretRef{Scheme: "env", Name: rest}, nil
	case scheme == "keychain":
		svc, acct, _ := strings.Cut(rest, "/")
		if strings.TrimSpace(svc) == "" {
			return SecretRef{}, fmt.Errorf("keychain: wants a service name")
		}
		return SecretRef{Scheme: "keychain", Name: svc, Account: acct}, nil
	}
	return SecretRef{}, fmt.Errorf("not env:<VAR> or keychain:<service>")
}

// HeaderValue is one parsed headers: entry: a literal prefix sent as is
// ("Bearer ", or "") and the reference its secret comes from.
type HeaderValue struct {
	Prefix string
	Ref    SecretRef
}

// ParseHeaderValue parses "[<prefix> ]env:<VAR>|keychain:<service>[/<account>]":
// the last word is the reference, the words before it are sent as is. The
// error never repeats raw.
func ParseHeaderValue(raw string) (HeaderValue, error) {
	raw = strings.TrimSpace(raw)
	prefix, last := "", raw
	if i := strings.LastIndexAny(raw, " \t"); i >= 0 {
		prefix, last = raw[:i+1], raw[i+1:]
	}
	ref, err := ParseSecretRef(last)
	if err != nil {
		return HeaderValue{}, err
	}
	return HeaderValue{Prefix: prefix, Ref: ref}, nil
}

// CheckConnectorURL accepts https on any host and http only on a loopback
// host (localhost, 127.0.0.0/8, ::1), so a secret header never crosses the
// network in clear text. The error never repeats a query string.
func CheckConnectorURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return fmt.Errorf("url: is not an absolute http(s) URL")
	}
	if u.User != nil {
		return fmt.Errorf("url: holds a user or password: put the credential in headers: as a secret reference")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopbackHost(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("url: http is allowed only on this machine (localhost, 127.0.0.1, ::1); use https for %s", u.Hostname())
	}
	return fmt.Errorf("url: wants https:// (or http:// on localhost), not %s://", u.Scheme)
}

func isLoopbackHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// SecretPresent reports whether ref is set on this machine without reading
// its value; tests replace it.
var SecretPresent = func(ref SecretRef) bool {
	switch ref.Scheme {
	case "env":
		v, ok := os.LookupEnv(ref.Name)
		return ok && v != ""
	case "keychain":
		if KeychainOS != "darwin" {
			return false
		}
		// Without -w security prints the item's attributes, not its password.
		cmd := exec.Command("security", keychainArgs(ref, false)...)
		return cmd.Run() == nil
	}
	return false
}

// SecretValue reads ref's value. issue-cli connector exec and check call
// it, to put the value into a child's environment, and cms up, to sign in
// to the Ghost it set up with the password it wrote; tests replace it.
var SecretValue = func(ref SecretRef) (string, bool) {
	switch ref.Scheme {
	case "env":
		v, ok := os.LookupEnv(ref.Name)
		return v, ok && v != ""
	case "keychain":
		if KeychainOS != "darwin" {
			return "", false
		}
		out, err := exec.Command("security", keychainArgs(ref, true)...).Output()
		if err != nil {
			return "", false
		}
		v := strings.TrimRight(string(out), "\n")
		return v, v != ""
	}
	return "", false
}

func keychainArgs(ref SecretRef, value bool) []string {
	args := []string{"find-generic-password", "-s", ref.Name}
	if ref.Account != "" {
		args = append(args, "-a", ref.Account)
	}
	if value {
		args = append(args, "-w")
	}
	return args
}

// SecretState is one secret of a connector as the owner sees it.
type SecretState struct {
	Var string `json:"var"`
	Ref string `json:"ref"`
	Set bool   `json:"set"`
	// Optional is a secret the connector runs without when it isn't set.
	Optional bool `json:"optional,omitempty"`
	// Header marks a headers: entry (Var is then the header name).
	Header bool `json:"header,omitempty"`
	// Err is why the reference doesn't parse; it never holds the raw value.
	Err string `json:"error,omitempty"`
}

// IsOptional reports whether c runs without secret v (an env var name).
func (c Connector) IsOptional(v string) bool {
	return slices.Contains(c.Optional, v)
}

// SecretStates lists c's secrets by env var name, each set or missing.
func (c Connector) SecretStates() []SecretState {
	var out []SecretState
	for _, v := range slices.Sorted(maps.Keys(c.Secrets)) {
		ref, err := ParseSecretRef(c.Secrets[v])
		if err != nil {
			out = append(out, SecretState{Var: v, Ref: "(invalid)", Optional: c.IsOptional(v), Err: err.Error()})
			continue
		}
		out = append(out, SecretState{Var: v, Ref: ref.String(), Set: SecretPresent(ref), Optional: c.IsOptional(v)})
	}
	for _, h := range slices.Sorted(maps.Keys(c.Headers)) {
		hv, err := ParseHeaderValue(c.Headers[h])
		if err != nil {
			out = append(out, SecretState{Var: h, Ref: "(invalid)", Header: true, Err: err.Error()})
			continue
		}
		out = append(out, SecretState{Var: h, Ref: hv.Prefix + hv.Ref.String(), Set: SecretPresent(hv.Ref), Header: true})
	}
	return out
}

// Missing lists the env vars of c whose secret is not set (or invalid). An
// optional secret that isn't set is not missing; an invalid one still is.
func (c Connector) Missing() []string {
	var out []string
	for _, s := range c.SecretStates() {
		if !s.Set && (!s.Optional || s.Err != "") {
			out = append(out, s.Var)
		}
	}
	return out
}

// ConnectorAddress is <dept key>/<name>, how commands name a connector.
func ConnectorAddress(dept, name string) string {
	return DepartmentKey(dept) + "/" + name
}

// ConnectorsBrief is the briefing line on department dept's connectors for
// a worker or its lead (lead true); "" when it has none.
func ConnectorsBrief(p *Project, dept string, lead bool) string {
	d := p.DepartmentNamed(dept)
	if d == nil || len(d.Connectors) == 0 {
		return ""
	}
	var parts []string
	for _, c := range d.Connectors {
		addr := ConnectorAddress(d.Name, c.Name)
		if miss := c.Missing(); len(miss) > 0 {
			who := "ask your lead"
			if lead {
				who = "ask the owner"
			}
			parts = append(parts, fmt.Sprintf("%s: secret %s missing on this machine, %s (issue-cli connectors %s)", c.Name, strings.Join(miss, ", "), who, DepartmentKey(d.Name)))
			continue
		}
		var s string
		if c.Kind == ConnectorMCP {
			s = c.Name + " (MCP, loaded in this session"
			if off := c.DeniedTools(); len(off) > 0 {
				s += "; tools off: " + strings.Join(off, ", ")
			}
			s += ")"
		} else {
			s = fmt.Sprintf("%s (tool: issue-cli connector exec %s -- <cmd>)", c.Name, addr)
		}
		switch {
		case c.PerRun != nil && c.URL != "":
			s += " — it starts at your first call to one of its tools, stopped after; a call answered \"stays stopped\" means mem-guard held it (go on without it, a later call tries again): issue-cli connectors " + DepartmentKey(d.Name)
		case c.PerRun != nil:
			s += " — it starts per run, stopped after; not there means mem-guard held it or it failed to start: issue-cli connectors " + DepartmentKey(d.Name)
		}
		switch {
		case c.NeedsApprovedOutput:
			s += " — it sends: connector exec refuses it until the owner approves your issue's newest output on the shelf"
			if r := c.ReadToolNames(); c.URL != "" && len(r) > 0 {
				s += " (" + strings.Join(r, ", ") + " work before)"
			}
			s += "; then issue-cli output publish"
		case c.Sends:
			s += " — it sends: use it only for an output the owner approved, then issue-cli output publish"
		}
		if len(c.At) > 0 && c.URL != "" {
			s += "; its tools are limited per status (at: " + strings.Join(c.AtStatuses(), ", ") + "): issue-cli connectors " + DepartmentKey(d.Name) + " lists them"
		}
		parts = append(parts, s)
	}
	return "- Connectors: " + strings.Join(parts, " · ") + ".\n"
}

// ConnectorsLine is issue-cli start / show's line for department dept:
// "Connectors: 2 (1 missing) — issue-cli connectors marketing"; "" when it
// has none.
func ConnectorsLine(p *Project, dept string) string {
	d := p.DepartmentNamed(dept)
	if d == nil || len(d.Connectors) == 0 {
		return ""
	}
	missing := 0
	for _, c := range d.Connectors {
		if len(c.Missing()) > 0 {
			missing++
		}
	}
	s := fmt.Sprintf("Connectors: %d", len(d.Connectors))
	if missing > 0 {
		s += fmt.Sprintf(" (%d missing)", missing)
	}
	return s + " — issue-cli connectors " + DepartmentKey(d.Name)
}
