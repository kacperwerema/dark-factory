package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Where a task's worker gets its HOME (the benchmark's worker_home:).
const (
	// WorkerHomeSandbox (the default): HOME is <sandbox>/home with only
	// what Claude Code needs to find its login handed in (LoginLinks), and
	// the usage is read from the transcripts there.
	WorkerHomeSandbox = "sandbox"
	// WorkerHomeOwner: the worker keeps the owner's HOME (the parent's
	// D-10), its settings, hooks and history included.
	WorkerHomeOwner = "owner"
)

// HomeMode is the benchmark's worker_home:, WorkerHomeSandbox when unset.
func (b *Benchmark) HomeMode() string {
	if b.WorkerHome == "" {
		return WorkerHomeSandbox
	}
	return b.WorkerHome
}

func validWorkerHome(v string) error {
	switch v {
	case "", WorkerHomeSandbox, WorkerHomeOwner:
		return nil
	}
	return fmt.Errorf("worker_home: %q: use %q (the default) or %q", v, WorkerHomeSandbox, WorkerHomeOwner)
}

// LoginLink is one thing of the owner's home a sandbox HOME links to, so
// the worker's CLI finds its login. Only links: the engine never reads,
// copies or prints a credential, and a token refresh stays in the owner's
// one store.
type LoginLink struct {
	Name string // what it is, for the run's log
	Path string // relative to the sandbox HOME
	// Target is where it points, from the owner's home (and the config dir
	// variables the owner set).
	Target func(ownerHome string) string
}

// LoginLinks is everything a sandbox HOME gets from the owner's, apart from
// the .claude.json written by writeClaudeJSON. docs/benchmark.md "The
// worker's home" lists the same.
var LoginLinks = []LoginLink{
	// macOS: the login keychain is found through $HOME/Library/Keychains.
	{Name: "keychain", Path: filepath.Join("Library", "Keychains"), Target: func(h string) string { return filepath.Join(h, "Library", "Keychains") }},
	// Linux (and a file login anywhere): Claude Code's credentials file.
	{Name: "credentials file", Path: filepath.Join(".claude", ".credentials.json"), Target: func(h string) string { return filepath.Join(ownerConfigDir(h), ".credentials.json") }},
	// A codex worker: its login file in $CODEX_HOME, default ~/.codex.
	{Name: "codex login", Path: filepath.Join(".codex", "auth.json"), Target: func(h string) string { return filepath.Join(ownerCodexHome(h), "auth.json") }},
}

// ownerCodexHome is the owner's codex folder: $CODEX_HOME, else
// <home>/.codex.
func ownerCodexHome(ownerHome string) string {
	if d := strings.TrimSpace(os.Getenv("CODEX_HOME")); d != "" {
		return d
	}
	return filepath.Join(ownerHome, ".codex")
}

// claudeJSONKeys are the keys of the owner's ~/.claude.json the sandbox's
// own copy keeps: account metadata, no token, history or MCP servers.
var claudeJSONKeys = []string{"oauthAccount", "userID"}

// ownerConfigDir is the owner's Claude Code config dir: $CLAUDE_CONFIG_DIR,
// else <home>/.claude.
func ownerConfigDir(ownerHome string) string {
	if d := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); d != "" {
		return d
	}
	return filepath.Join(ownerHome, ".claude")
}

// prepareWorkerHome makes home and puts in it the LoginLinks whose target
// exists and a .claude.json; it returns the names of what it linked. An
// empty ownerHome links nothing.
func prepareWorkerHome(home, ownerHome string) ([]string, error) {
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		return nil, err
	}
	var linked []string
	for _, l := range loginTargets(ownerHome) {
		dst := filepath.Join(home, l.Path)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return linked, err
		}
		if err := os.Symlink(l.target, dst); err != nil {
			return linked, err
		}
		linked = append(linked, l.Name)
	}
	return linked, writeClaudeJSON(home, ownerHome)
}

type loginTarget struct {
	LoginLink
	target string
}

// loginTargets are the LoginLinks whose target exists in ownerHome; it
// only looks whether each is there, never inside.
func loginTargets(ownerHome string) []loginTarget {
	if ownerHome == "" {
		return nil
	}
	var out []loginTarget
	for _, l := range LoginLinks {
		target := l.Target(ownerHome)
		if _, err := os.Lstat(target); err == nil {
			out = append(out, loginTarget{l, target})
		}
	}
	return out
}

// HomeNote is the run's first log line about the worker's HOME: the mode
// and what login a sandbox HOME gets.
func HomeNote(mode, ownerHome string) string {
	if mode == WorkerHomeOwner {
		return "worker home: owner's (worker_home: owner)"
	}
	var names []string
	for _, l := range loginTargets(ownerHome) {
		names = append(names, l.Name)
	}
	if len(names) == 0 {
		return "worker home: sandbox, no login found to link (the worker needs a token in its environment, e.g. CLAUDE_CODE_OAUTH_TOKEN; see docs/benchmark.md#the-workers-home)"
	}
	return "worker home: sandbox, login linked: " + strings.Join(names, ", ")
}

// writeClaudeJSON writes the sandbox's ~/.claude.json: onboarding done, and
// claudeJSONKeys from the owner's when it has them.
func writeClaudeJSON(home, ownerHome string) error {
	out := map[string]json.RawMessage{"hasCompletedOnboarding": json.RawMessage("true")}
	if ownerHome != "" {
		if data, err := os.ReadFile(filepath.Join(ownerHome, ".claude.json")); err == nil {
			var owner map[string]json.RawMessage
			if json.Unmarshal(data, &owner) == nil {
				for _, k := range claudeJSONKeys {
					if v, ok := owner[k]; ok {
						out[k] = v
					}
				}
			}
		}
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(home, ".claude.json"), append(data, '\n'), 0o600)
}

// cacheEnv pins the Go and npm caches to the owner's when they aren't set,
// so a sandbox HOME doesn't re-download modules or rebuild every task.
func cacheEnv(ownerHome string) []string {
	if ownerHome == "" {
		return nil
	}
	var out []string
	set := func(k, v string) {
		if os.Getenv(k) == "" && v != "" {
			out = append(out, k+"="+v)
		}
	}
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		gopath = filepath.Join(ownerHome, "go")
		set("GOPATH", gopath)
	}
	set("GOMODCACHE", filepath.Join(strings.Split(gopath, string(os.PathListSeparator))[0], "pkg", "mod"))
	if c, err := os.UserCacheDir(); err == nil {
		set("GOCACHE", filepath.Join(c, "go-build"))
	}
	set("npm_config_cache", filepath.Join(ownerHome, ".npm"))
	return out
}

var noLogin = regexp.MustCompile(`(?i)not logged in|invalid api key|please run /login`)

// NoLoginReason is the reason of a task whose worker found no login.
const NoLoginReason = "the worker found no login in its sandbox HOME: see docs/benchmark.md#the-workers-home (worker_home: owner keeps the owner's HOME)"

// saysNoLogin reports whether the worker's output says it is not logged in.
func saysNoLogin(out ...[]byte) bool {
	for _, o := range out {
		if noLogin.Match(o) {
			return true
		}
	}
	return false
}
