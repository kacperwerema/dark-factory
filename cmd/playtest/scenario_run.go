package main

import (
	"bytes"
	"context"
	"fmt"

	"io/fs"
	"net"
	"net/http"

	"os"
	"os/exec"
	"path/filepath"

	"strings"

	"syscall"
	"time"

	"github.com/kacperwerema/dark-factory/internal/recent"

	"gopkg.in/yaml.v3"
)

// Env is the environment every scenario process runs with. A Scenario
// rebuilt from only a Root (stopRecord) never ran the stager, so it builds
// the same one here: a nil cmd.Env would inherit the caller's $TMUX and reach
// the shared server.
func (s *Scenario) Env() []string {
	if s.env == nil {
		return scenarioEnv(os.Environ(), s)
	}
	return append([]string(nil), s.env...)
}

// isolatedVars are dropped from the caller's environment: they would point a
// scenario process at the real config, the real tmux server, or tag it as a
// dispatched agent.
var isolatedVars = []string{
	"TMUX", "TMUX_PANE", "TMUX_TMPDIR", "HOME", "XDG_STATE_HOME", "XDG_CONFIG_HOME",
	"XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_RUNTIME_DIR", "ISSUE_VIEWER_CONFIG", "ISSUE_VIEWER_URL",
	"ISSUE_VIEWER_ISSUE_SLUG", "ISSUE_VIEWER_SERVER_PWD", "ISSUE_CLI_LOG", "AGENT_NAME",
	"AI_AGENT", "CLAUDECODE", "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE",
	"CLAUDE_CONFIG_DIR",
}

// scenarioEnv builds a scenario's environment from base: the isolated
// variables are replaced so nothing reaches ~/Work/projects.yaml, the user's
// state, or the default tmux socket.
func scenarioEnv(base []string, s *Scenario) []string {
	drop := map[string]bool{}
	for _, k := range isolatedVars {
		drop[k] = true
	}
	var env []string
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if drop[k] || strings.HasPrefix(k, "GIT_AUTHOR_") || strings.HasPrefix(k, "GIT_COMMITTER_") {
			continue
		}
		if k == "PATH" && s.bins != nil {
			kv = "PATH=" + s.bins.Dir + string(os.PathListSeparator) + strings.TrimPrefix(kv, "PATH=")
		}
		env = append(env, kv)
	}
	return append(env,
		"HOME="+filepath.Join(s.Root, "home"),
		"XDG_STATE_HOME="+filepath.Join(s.Root, "state"),
		"XDG_CONFIG_HOME="+filepath.Join(s.Root, "config"),
		"XDG_CACHE_HOME="+filepath.Join(s.Root, "cache"),
		"TMUX_TMPDIR="+s.TmuxDir(),
		"ISSUE_VIEWER_CONFIG="+s.ConfigPath(),
		recent.OffEnv+"=1",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=playtest", "GIT_AUTHOR_EMAIL=playtest@example.invalid",
		"GIT_COMMITTER_NAME=playtest", "GIT_COMMITTER_EMAIL=playtest@example.invalid",
		"TZ=UTC",
	)
}

// command builds a process that runs inside the scenario: its env, cwd in
// the project (the Root when the Scenario was rebuilt from only that).
func (s *Scenario) command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Dir = s.Proj
	if cmd.Dir == "" {
		cmd.Dir = s.Root
	}
	cmd.Env = s.Env()
	return cmd
}

// Run runs a command in the scenario and returns its combined output and
// exit code.
func (s *Scenario) Run(ctx context.Context, name string, args ...string) (string, int, error) {
	cmd := s.command(name, args...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return "", -1, err
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		code := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				return buf.String(), -1, err
			}
		}
		return buf.String(), code, nil
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return buf.String(), -1, ctx.Err()
	}
}

// CLI runs the scenario's branch-built issue-cli.
func (s *Scenario) CLI(ctx context.Context, args ...string) (string, int, error) {
	return s.Run(ctx, s.bins.CLI, args...)
}

// Tmux runs tmux against the scenario's private server.
func (s *Scenario) Tmux(ctx context.Context, args ...string) (string, int, error) {
	if err := s.guardTmux(); err != nil {
		return "", -1, err
	}
	if err := os.MkdirAll(filepath.Dir(s.TmuxSocket()), 0o700); err != nil {
		return "", -1, err
	}
	return s.Run(ctx, "tmux", append([]string{"-S", s.TmuxSocket()}, args...)...)
}

// guardTmux refuses to talk to a tmux server whose socket dir is not this
// scenario's marked private dir.
func (s *Scenario) guardTmux() error {
	dir := s.TmuxDir()
	if !strings.HasPrefix(dir, s.Root+string(os.PathSeparator)) {
		return fmt.Errorf("tmux dir %s is outside the scenario", dir)
	}
	if _, err := os.Stat(filepath.Join(dir, markerName)); err != nil {
		return fmt.Errorf("tmux dir %s has no %s marker; refusing to touch it", dir, markerName)
	}
	return nil
}

// Expand replaces {project}, {base} and {dir} in a session string.
func (s *Scenario) Expand(v string) string {
	return strings.NewReplacer("{project}", s.Spec.Project.Slug, "{base}", s.Base, "{dir}", s.Proj).Replace(v)
}

// Stop kills the board and the private tmux server, then removes the temp
// dir unless keep is set.
func (s *Scenario) Stop(keep bool) {
	if s.board != nil && s.board.Process != nil {
		_ = syscall.Kill(-s.board.Process.Pid, syscall.SIGKILL)
		_ = s.board.Wait()
		s.board = nil
	}
	if s.guardTmux() == nil {
		if _, err := exec.LookPath("tmux"); err == nil {
			// Name the private socket and run in the scenario's env: an
			// inherited $TMUX sends kill-server to the shared server
			// (2026-10-31, 2026-11-01).
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			cmd := exec.CommandContext(ctx, "tmux", "-S", s.TmuxSocket(), "kill-server")
			cmd.Env = s.Env()
			_ = cmd.Run()
			cancel()
		}
	}
	if keep || s.Root == "" {
		return
	}
	if _, err := os.Stat(filepath.Join(s.Root, markerName)); err == nil {
		_ = os.RemoveAll(s.Root)
	}
}

// scenarioConfig is the staged projects.yaml: extra_config: on top, then
// the telemetry line, viewer_url and the one project's entry.
func scenarioConfig(spec *ScenarioSpec, base, proj, telemetryLine string) (string, error) {
	cfg := telemetryLine + fmt.Sprintf("viewer_url: %q\nprojects:\n  - name: %q\n    slug: %q\n    issues: %q\n    docs: %q\n    workdir: %q\n    terminal: \"none\"\n",
		base, spec.Project.Name, spec.Project.Slug, filepath.Join(proj, "issues"), filepath.Join(proj, "docs"), proj)
	if spec.Project.Repo != "" {
		cfg += fmt.Sprintf("    repo: %q\n", spec.Project.Repo)
	}
	if extra := strings.TrimRight(spec.Project.Extra, "\n"); extra != "" {
		for _, line := range strings.Split(extra, "\n") {
			cfg += "    " + line + "\n"
		}
	}
	if len(spec.ExtraConfig) > 0 {
		extra, err := yaml.Marshal(spec.ExtraConfig)
		if err != nil {
			return "", fmt.Errorf("extra_config: %v", err)
		}
		cfg = string(extra) + cfg
	}
	return cfg, nil
}

// pickPort is want when nothing listens on it, else a free port and want as
// the one that was taken. want 0 is any free port.
func pickPort(want int) (port, taken int, err error) {
	if want > 0 {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", want))
		if err == nil {
			l.Close()
			return want, 0, nil
		}
		taken = want
	}
	port, err = freePort()
	return port, taken, err
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// boardReadyBudget is how long a scenario board gets to answer. It answers
// in about a second on a quiet machine; under make validate beside other
// workers it has taken over 10 s. waitHTTP returns as soon as it answers.
const boardReadyBudget = time.Minute

// waitHTTP polls url until it answers below 500. Each request has its own
// short timeout, so a board that accepts but hangs can't outlast budget.
func waitHTTP(url string, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	client := &http.Client{Timeout: 2 * time.Second}
	var last error
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
			last = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			last = err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("not ready after %s: %v", budget, last)
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		return copyFile(p, filepath.Join(dst, rel))
	})
}

// writeExclude adds paths to the project repository's .git/info/exclude,
// keeping what is there.
func writeExclude(proj string, paths ...string) error {
	file := filepath.Join(proj, ".git", "info", "exclude")
	old, _ := os.ReadFile(file)
	text := string(old)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	for _, p := range paths {
		text += p + "\n"
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, []byte(text), 0o644)
}
