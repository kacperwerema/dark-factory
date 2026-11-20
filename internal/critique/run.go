package critique

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// DefaultTimeout is how long a critic may run without critique: timeout:.
const DefaultTimeout = 15 * time.Minute

// Timeout is the config's timeout, or DefaultTimeout.
func Timeout(c *tracker.CritiqueConfig) (time.Duration, error) {
	if c == nil || strings.TrimSpace(c.Timeout) == "" {
		return DefaultTimeout, nil
	}
	d, err := time.ParseDuration(strings.TrimSpace(c.Timeout))
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("critique: timeout: %q is not a duration like 15m", c.Timeout)
	}
	return d, nil
}

// Argv is the read-only, headless command for agent: claude may only
// read (Read, Grep, Glob; edits, writes and shell refused), codex runs in
// its read-only sandbox. The brief goes on stdin.
func Argv(agent string) ([]string, error) {
	switch agent {
	case "claude":
		return []string{"claude", "-p", "--allowedTools", "Read,Grep,Glob", "--disallowedTools", "Edit,Write,NotebookEdit,Bash,WebFetch,WebSearch"}, nil
	case "codex":
		return []string{"codex", "exec", "--sandbox", "read-only", "-"}, nil
	}
	return nil, fmt.Errorf("no read-only command for agent %q (known: claude, codex); set critique: command: in workflow.yaml", agent)
}

// Exec runs the critic once in dir with brief on stdin and returns its
// stdout. command (critique: command:) runs under sh -c instead of argv;
// env adds CRITIQUE_* variables for it.
func Exec(ctx context.Context, dir, command string, argv []string, brief string, env []string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var cmd *exec.Cmd
	if strings.TrimSpace(command) != "" {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	} else {
		cmd = exec.CommandContext(ctx, argv[0], argv[1:]...)
	}
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(brief)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return out.String(), fmt.Errorf("timed out after %s", timeout)
	}
	if err != nil {
		msg := strings.TrimSpace(errb.String())
		if len(msg) > 300 {
			msg = msg[len(msg)-300:]
		}
		if msg != "" {
			return out.String(), fmt.Errorf("%v: %s", err, msg)
		}
		return out.String(), err
	}
	return out.String(), nil
}

// SessionPrefix starts a critic's Claude Code session name, so issue-cli
// spend --kinds can tell a critic's tokens from its worker's: the critic
// runs in the issue's worktree.
const SessionPrefix = "critic-"

// sessionUnsafe is what a session name never holds.
var sessionUnsafe = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// SessionName is the critic's session name for slug: critic-<slug's last
// part>, letters, digits, - and _ only.
func SessionName(slug string) string {
	s := slug
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.Trim(sessionUnsafe.ReplaceAllString(s, "-"), "-")
	if len(s) > 60 {
		s = strings.TrimRight(s[:60], "-")
	}
	return SessionPrefix + s
}

// WithName is argv with -n name after the claude command (a headless
// claude -p writes it as the transcript's agent-name record); any other
// agent's argv stays as it is.
func WithName(argv []string, name string) []string {
	if len(argv) == 0 || name == "" || filepath.Base(argv[0]) != "claude" || slices.Contains(argv, "-n") {
		return argv
	}
	out := append([]string{argv[0], "-n", name}, argv[1:]...)
	return out
}
