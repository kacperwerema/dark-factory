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
// reference is invalid is an error naming the reference, never the value.
func Env(c *tracker.Connector, base []string) ([]string, error) {
	env := append([]string{}, base...)
	for _, s := range c.SecretStates() {
		if s.Err != "" {
			return nil, fmt.Errorf("secret %s: %s", s.Var, s.Err)
		}
		ref, _ := tracker.ParseSecretRef(c.Secrets[s.Var])
		v, ok := tracker.SecretValue(ref)
		if !ok {
			return nil, fmt.Errorf("secret %s (%s) is missing on this machine: %s", s.Var, s.Ref, howToSet(ref))
		}
		env = append(env, s.Var+"="+v)
	}
	return env, nil
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
}

// LogPath is the checks log, with the work files.
func LogPath(p *tracker.Project) string {
	return filepath.Join(p.IssueDir, ".connectors", "checks.jsonl")
}

// RunCheck runs c's check: with its secrets, CheckTimeout and its output
// thrown away (factory/department-connectors#D-5).
func RunCheck(ctx context.Context, dept string, c *tracker.Connector, now func() time.Time) Record {
	r := Record{Connector: tracker.ConnectorAddress(dept, c.Name), Date: now().UTC()}
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
	Note       string                `json:"note,omitempty"`
	HasCheck   bool                  `json:"has_check"`
	Last       *Record               `json:"last_check,omitempty"`
}

// Failing is a connector the owner should look at: a secret missing, or
// its last check failing.
func (s Status) Failing() bool {
	return s.Missing || (s.Last != nil && (s.Last.Result == ResultFailing || s.Last.Result == ResultMissing))
}

// List is every connector of department dept ("" for all), in
// projects.yaml order.
func List(p *tracker.Project, dept string) []Status {
	checks := LoadChecks(p)
	var out []Status
	for _, d := range p.Departments {
		if dept != "" && tracker.DepartmentKey(d.Name) != tracker.DepartmentKey(dept) {
			continue
		}
		for _, c := range d.Connectors {
			addr := tracker.ConnectorAddress(d.Name, c.Name)
			s := Status{Department: d.Name, Key: tracker.DepartmentKey(d.Name), Name: c.Name, Address: addr, Kind: c.Kind,
				Secrets: c.SecretStates(), Sends: c.Sends, Note: c.Note, HasCheck: strings.TrimSpace(c.Check) != ""}
			s.Missing = len(c.Missing()) > 0
			if r, ok := checks[addr]; ok {
				s.Last = &r
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
		out = append(out, Server{Name: c.Name, Command: "issue-cli", Args: args, Env: env})
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
	}
	return out
}
