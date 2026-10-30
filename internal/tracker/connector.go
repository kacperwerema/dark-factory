package tracker

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"slices"
	"strings"
)

// Connector is one outside tool a department's agents may use: an MCP
// server they load, or a command run through issue-cli connector exec.
// Config holds names and secret references only, never a value
// (factory/department-connectors#D-2).
type Connector struct {
	Name string `yaml:"name"`
	// Kind is ConnectorMCP or ConnectorTool.
	Kind    string   `yaml:"kind"`
	Command string   `yaml:"command"`
	Args    []string `yaml:"args"`
	// Secrets maps the env var the child gets to where the owner keeps its
	// value: env:<VAR> or keychain:<service>[/<account>].
	Secrets map[string]string `yaml:"secrets"`
	// Check is a read-only shell command; exit 0 is healthy.
	Check string `yaml:"check"`
	// Sends marks a connector that can publish or spend: only after the
	// owner approved the output (factory/department-connectors#D-6).
	Sends bool   `yaml:"sends"`
	Note  string `yaml:"note"`
}

// Connector kinds.
const (
	ConnectorMCP  = "mcp"
	ConnectorTool = "tool"
)

// ConnectorKinds are the kinds lint accepts.
var ConnectorKinds = []string{ConnectorMCP, ConnectorTool}

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

// SecretPresent reports whether ref is set on this machine without reading
// its value; tests replace it.
var SecretPresent = func(ref SecretRef) bool {
	switch ref.Scheme {
	case "env":
		v, ok := os.LookupEnv(ref.Name)
		return ok && v != ""
	case "keychain":
		if runtime.GOOS != "darwin" {
			return false
		}
		// Without -w security prints the item's attributes, not its password.
		cmd := exec.Command("security", keychainArgs(ref, false)...)
		return cmd.Run() == nil
	}
	return false
}

// SecretValue reads ref's value. Only issue-cli connector exec and check
// call it, to put the value into a child's environment; tests replace it.
var SecretValue = func(ref SecretRef) (string, bool) {
	switch ref.Scheme {
	case "env":
		v, ok := os.LookupEnv(ref.Name)
		return v, ok && v != ""
	case "keychain":
		if runtime.GOOS != "darwin" {
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
	// Err is why the reference doesn't parse; it never holds the raw value.
	Err string `json:"error,omitempty"`
}

// SecretStates lists c's secrets by env var name, each set or missing.
func (c Connector) SecretStates() []SecretState {
	var out []SecretState
	for _, v := range slices.Sorted(maps.Keys(c.Secrets)) {
		ref, err := ParseSecretRef(c.Secrets[v])
		if err != nil {
			out = append(out, SecretState{Var: v, Ref: "(invalid)", Err: err.Error()})
			continue
		}
		out = append(out, SecretState{Var: v, Ref: ref.String(), Set: SecretPresent(ref)})
	}
	return out
}

// Missing lists the env vars of c whose secret is not set (or invalid).
func (c Connector) Missing() []string {
	var out []string
	for _, s := range c.SecretStates() {
		if !s.Set {
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
			s = c.Name + " (MCP, loaded in this session)"
		} else {
			s = fmt.Sprintf("%s (tool: issue-cli connector exec %s -- <cmd>)", c.Name, addr)
		}
		if c.Sends {
			s += " — it sends: use it only for an output the owner approved, then issue-cli output publish"
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
