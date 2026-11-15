package checks

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/standin"
)

// identityVars are the variables issue-cli's worker guard knows a worker or
// lead by once env -i has dropped the tmux name and the folder.
var identityVars = []string{"ISSUE_VIEWER_ISSUE_SLUG", "ISSUE_VIEWER_LEAD_SCOPE", "ISSUE_VIEWER_WORKER", "AGENT_NAME", "CLAUDECODE"}

// identityDrop matches the ways a script used to get past the worker guard:
// a bare env -i, unsetting or emptying an identity variable, and pin_slug.
var identityDrop = regexp.MustCompile(`\benv\s+(-\S+\s+)*-i\b` +
	`|\benv\s+(-\S+\s+)*-u\s*(ISSUE_VIEWER_\w+|AGENT_NAME|CLAUDECODE)\b` +
	`|\bunset\b[^;\n]*\b(ISSUE_VIEWER_ISSUE_SLUG|ISSUE_VIEWER_LEAD_SCOPE|ISSUE_VIEWER_WORKER|AGENT_NAME|CLAUDECODE)\b` +
	`|\b(ISSUE_VIEWER_ISSUE_SLUG|ISSUE_VIEWER_LEAD_SCOPE|ISSUE_VIEWER_WORKER|AGENT_NAME)=(''|""|\s|$)` +
	`|\bpin_slug\b`)

// AC3: no guided check script drops the worker's identity. The scan covers
// the project's scripts/checks (while an issue branch has them) and the
// ported examples; lib.sh may use env -i only in clean_shell's argv, which
// carries every identity variable.
func TestCheckScriptsKeepTheWorkerIdentity(t *testing.T) {
	var files []string
	for _, glob := range []string{filepath.Join("..", "..", "scripts", "checks", "*.sh"), filepath.Join("testdata", "ported", "*.sh")} {
		m, _ := filepath.Glob(glob)
		files = append(files, m...)
	}
	if len(files) == 0 {
		t.Fatal("no check scripts found to scan")
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if m := identityDrop.FindString(line); m != "" {
				t.Errorf("%s:%d: %q drops the worker's identity; use sic or clean_shell, which carry it (owner-only steps pass on the marked sandbox)", f, i+1, m)
			}
		}
	}

	lib := string(Lib)
	for i, line := range strings.Split(lib, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") || !identityDrop.MatchString(line) {
			continue
		}
		if !strings.Contains(line, "_ck_argv=(env -i") {
			t.Errorf("lib.sh:%d drops the worker's identity: %s", i+1, line)
		}
	}
	argv := lib[strings.Index(lib, "_ck_clean_argv() {"):]
	argv = argv[:strings.Index(argv, "\n}")]
	for _, v := range identityVars {
		if !strings.Contains(argv, v) {
			t.Errorf("clean_shell's environment drops %s", v)
		}
	}
	if strings.Contains(lib, "pin_slug()") {
		t.Error("lib.sh defines pin_slug: a sandbox issue never takes the worker's slug (the marked sandbox replaced it)")
	}
}

func TestIdentityDropPattern(t *testing.T) {
	for _, bad := range []string{
		"env -i PATH=/bin issue-cli list", "env -u ISSUE_VIEWER_ISSUE_SLUG issue-cli", "unset AGENT_NAME TMUX",
		"ISSUE_VIEWER_ISSUE_SLUG= sic transition x", "pin_slug other", `AGENT_NAME="" sic dispatch x`,
	} {
		if !identityDrop.MatchString(bad) {
			t.Errorf("missed: %s", bad)
		}
	}
	for _, ok := range []string{"sic transition x --to done", "clean_shell sh -c claude", "export ISSUE_VIEWER_ISSUE_SLUG=$slug", "env FOO=1 bar"} {
		if identityDrop.MatchString(ok) {
			t.Errorf("false hit: %s", ok)
		}
	}
}

// clean_shell carries a worker's identity: the slug it already has, and a
// worker known only by a signal env -i drops (here a whoami stand-in says
// "working directory under .worktrees/") as ISSUE_VIEWER_WORKER.
func TestLibCleanShellKeepsTheWorkerIdentity(t *testing.T) {
	dir := t.TempDir()
	who := filepath.Join(dir, "whoami-cli")
	standin.Write(t, who, "printf '{\\n  \"role\": \"worker\",\\n  \"signal\": \"working directory under .worktrees/\"\\n}\\n'\n")
	r := runMachine(t, `
stand_in claude
echo "SLUG=$(clean_shell sh -c 'echo "$ISSUE_VIEWER_ISSUE_SLUG"')"
`, "ISSUE_VIEWER_ISSUE_SLUG=cli/thing")
	contains(t, r.out, "SLUG=cli/thing")
	r = runMachine(t, `
stand_in claude
echo "WORKER=$(clean_shell sh -c 'echo "$ISSUE_VIEWER_WORKER"')"
`, "ISSUE_VIEWER_ISSUE_SLUG=", "CHECKS_ISSUE_CLI="+who)
	contains(t, r.out, "WORKER=working directory under .worktrees/")
}
