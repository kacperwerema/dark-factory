package agentrun

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// tmux prints a tab inside a -F format as '_' without a UTF-8 locale (seen
// first on tmux 3.5a in a container), so a format split on tabs reads one
// field and every parser above drops the line. No format any
// binary hands tmux may carry a tab: they are built with Format and read
// with SplitFields.
func TestNoTmuxFormatUsesATab(t *testing.T) {
	// A tab next to a #{var} or #S-style alias, in Go source text.
	tabFormat := regexp.MustCompile(`#\{[^}"]*\}\\t|\\t#\{|#[A-Za-z]\\t|\\t#[A-Za-z]`)
	root := filepath.Join("..", "..")
	var bad []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".worktrees", "node_modules", "issues":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(src), "\n") {
			if tabFormat.MatchString(line) {
				bad = append(bad, path+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) > 0 {
		t.Errorf("tmux formats with a tab separator (tmux 3.5a prints it as '_'; use agentrun.Format):\n%s", strings.Join(bad, "\n"))
	}
}

// Against the installed tmux: every list agentrun reads from tmux finds the
// sessions, windows and panes a private server holds, with each field whole.
// tmux (3.5a and 3.8 alike) prints a tab in a format as '_' when the client
// has no UTF-8 locale (no LANG: a container, launchd, cron), so the test runs
// in the C locale with TMUX unset (a TMUX in the environment, even empty,
// tells the client it runs inside tmux and so in UTF-8): the old tab
// formats found none of them there.
func TestTmuxListsReadTheInstalledTmux(t *testing.T) {
	isolateTmux(t) // sets TMUX="" and restores it at cleanup
	os.Unsetenv("TMUX")
	t.Setenv("LANG", "")
	t.Setenv("LC_CTYPE", "")
	t.Setenv("LC_ALL", "C")
	dir := realDir(t)
	tmuxRun(t, "new-session", "-d", "-s", "agent-x", "-n", "agent-x", "-c", dir, "sleep 600")
	tmuxRun(t, "new-session", "-d", "-s", "work", "-n", "lead-p", "-c", dir, "sleep 600")
	tmuxRun(t, "new-window", "-d", "-t", "=work", "-n", "agent-y", "sleep 600")
	tmuxRun(t, "set-option", "-w", "-t", "=work:=agent-y", leadViewOption, "lead-p")

	byName := map[string]Session{}
	for _, s := range defaultListSessions() {
		byName[s.Name] = s
	}
	x, ok := byName["agent-x"]
	if !ok || x.Path != dir || x.StartTime == "" {
		t.Errorf("list-sessions: agent-x = %+v (ok %v), want path %s and a start time; all %+v", x, ok, dir, byName)
	}
	for _, name := range []string{"work", "work:lead-p", "work:agent-y"} {
		if _, ok := byName[name]; !ok {
			t.Errorf("list-sessions / list-windows: no %s in %+v", name, byName)
		}
	}

	panes := map[string]Pane{}
	for _, p := range defaultListPanes() {
		panes[p.Session+":"+p.Window] = p
	}
	for _, key := range []string{"agent-x:agent-x", "work:lead-p", "work:agent-y"} {
		if p, ok := panes[key]; !ok || p.PID <= 0 {
			t.Errorf("list-panes: %s = %+v (ok %v); all %+v", key, p, ok, panes)
		}
	}

	if index, ok := findLeadView("work", "lead-p"); !ok || index == "" {
		t.Errorf("findLeadView(work, lead-p) = %q, %v", index, ok)
	}
}

// realDir is a temp dir with links resolved, as tmux reports pane paths.
func realDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSplitFieldsKeepsTheLastFieldWhole(t *testing.T) {
	line := strings.Join([]string{"agent-x", "1792610000", "/p/odd" + FieldSep + "dir"}, FieldSep)
	got := SplitFields(line, 3)
	if len(got) != 3 || got[2] != "/p/odd"+FieldSep+"dir" {
		t.Errorf("SplitFields = %q", got)
	}
	if f := Format("session_name", "@opt"); f != "#{session_name}"+FieldSep+"#{@opt}" {
		t.Errorf("Format = %q", f)
	}
}
