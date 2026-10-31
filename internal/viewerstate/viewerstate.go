// Package viewerstate records the running viewer for issue-cli: which config
// it serves, its URL, pid and start time. issue-viewer --config writes it at
// start; issue-cli reads it to find the config from any folder without
// --config or $ISSUE_VIEWER_CONFIG.
package viewerstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// State is the content of viewer.json.
type State struct {
	Config  string    `json:"config"` // absolute path to projects.yaml
	URL     string    `json:"url"`
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
}

// Dir is $XDG_STATE_HOME/ai-workflow-engine, default
// ~/.local/state/ai-workflow-engine. Returns "" without a home directory.
func Dir() string {
	base := strings.TrimSpace(os.Getenv("XDG_STATE_HOME"))
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "ai-workflow-engine")
}

// Path is the viewer.json file, "" when Dir is.
func Path() string {
	dir := Dir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "viewer.json")
}

// Write replaces viewer.json with s. The temp-file rename keeps a reader from
// seeing half a file.
func Write(s State) error {
	path := Path()
	if path == "" {
		return errors.New("no home directory for the viewer state")
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "viewer-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Read loads viewer.json. A missing file is an error wrapping os.ErrNotExist.
func Read() (State, error) {
	var s State
	path := Path()
	if path == "" {
		return s, errors.New("no home directory for the viewer state")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(data, &s)
	return s, err
}

// DefaultURL is the viewer's address when nothing says otherwise: the
// default -port on localhost.
const DefaultURL = "http://localhost:8080"

// BaseURL is the viewer's base URL, without a trailing slash: configured
// (projects.yaml's viewer_url) when set, else the URL in viewer.json when
// that viewer serves configPath, else DefaultURL.
func BaseURL(configured, configPath string) string {
	if u := strings.TrimRight(strings.TrimSpace(configured), "/"); u != "" {
		return u
	}
	if configPath != "" {
		if s, err := Read(); err == nil && s.URL != "" && sameFile(s.Config, configPath) {
			return strings.TrimRight(s.URL, "/")
		}
	}
	return DefaultURL
}

// sameFile reports whether a and b name the same file, comparing absolute
// paths when the files can't be stat'ed.
func sameFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ai, aerr := os.Stat(a)
	bi, berr := os.Stat(b)
	if aerr == nil && berr == nil {
		return os.SameFile(ai, bi)
	}
	aa, _ := filepath.Abs(a)
	ba, _ := filepath.Abs(b)
	return aa == ba
}

// SandboxRoots are the temp folders a sandbox config lives under. A var so
// tests can say which folder counts.
var SandboxRoots = func() []string {
	return []string{os.TempDir(), "/tmp", "/private/tmp", "/var/folders", "/private/var/folders"}
}

// InSandbox reports whether path lies under one of SandboxRoots, comparing
// resolved paths (on a Mac /tmp and /var are links into /private).
func InSandbox(path string) bool {
	if path == "" {
		return false
	}
	p := resolve(path)
	for _, root := range SandboxRoots() {
		if root == "" {
			continue
		}
		r := resolve(root)
		if p == r || strings.HasPrefix(p, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// resolve makes p absolute and resolves symlinks in its longest existing
// prefix, so a file that doesn't exist yet still compares by its real folder.
func resolve(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	p = filepath.Clean(p)
	rest := ""
	for dir := p; ; dir = filepath.Dir(dir) {
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(r, rest)
		}
		if filepath.Dir(dir) == dir {
			return p
		}
		rest = filepath.Join(filepath.Base(dir), rest)
	}
}

// Alive reports whether pid is a running process. A pid of 0 or less (a
// record without one) is not.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// Claim says whether a viewer serving config, running as pid self, may write
// viewer.json; when it may not, why. It may not when the config is in a temp
// folder while viewer.json is in the shared state dir (a sandbox viewer
// started without XDG_STATE_HOME of its own), nor when another live viewer
// holds the record for a different config. issue-viewer -record-viewer
// writes it anyway.
func Claim(config string, self int) (bool, string) {
	if InSandbox(config) && !InSandbox(Dir()) {
		return false, fmt.Sprintf("config %s is in a temp folder and %s is the shared state", config, Path())
	}
	s, err := Read()
	if err == nil && s.PID != self && Alive(s.PID) && s.Config != "" && !sameFile(s.Config, config) {
		return false, fmt.Sprintf("the viewer at %s (pid %d) holds it for %s", s.URL, s.PID, s.Config)
	}
	return true, ""
}
