package viewer

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// newcomer is a clean machine for one test: an empty HOME, no XDG
// variables, a PATH with only the built binaries, a stand-in agent and the
// system tools, and a private tmux server. Nothing of the developer's
// machine (rtk, mem-guard, ~/.local/bin, a running viewer, the worker
// guard's ISSUE_VIEWER_* variables) reaches it.
type newcomer struct {
	t      *testing.T
	home   string
	bin    string
	tmux   string // TMUX_TMPDIR
	socket string
	env    []string
}

func newNewcomer(t *testing.T) *newcomer {
	t.Helper()
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not installed")
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	root := realTempDirRoot(t)
	n := &newcomer{t: t, home: filepath.Join(root, "home"), bin: filepath.Join(root, "bin")}
	for _, d := range []string{n.home, n.bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Not under t.TempDir(): macOS's deep temp dir passes tmux's ~104-byte
	// socket path limit.
	n.tmux, err = os.MkdirTemp("/tmp", "nwc-")
	if err != nil {
		t.Fatal(err)
	}
	n.socket = filepath.Join(n.tmux, fmt.Sprintf("tmux-%d", os.Getuid()), "default")
	for _, tool := range []string{tmuxPath, gitPath} {
		if err := os.Symlink(tool, filepath.Join(n.bin, filepath.Base(tool))); err != nil {
			t.Fatal(err)
		}
	}
	n.env = []string{
		"HOME=" + n.home,
		"PATH=" + n.bin + ":/usr/bin:/bin:/usr/sbin:/sbin",
		"SHELL=/bin/sh",
		"TMUX_TMPDIR=" + n.tmux,
		"TERM=xterm-256color",
		"LANG=C.UTF-8",
		"GIT_CONFIG_NOSYSTEM=1",
		"ISSUE_CLI_TELEMETRY=off",
	}
	if tmp := os.Getenv("TMPDIR"); tmp != "" {
		n.env = append(n.env, "TMPDIR="+tmp)
	}
	t.Cleanup(func() {
		// Only this test's server: an explicit socket, and no TMUX in the
		// environment (TMUX would point tmux at the developer's server).
		kill := exec.Command(tmuxPath, "-S", n.socket, "kill-server")
		kill.Env = n.env
		_ = kill.Run()
		_ = os.RemoveAll(n.tmux)
	})
	n.build()
	return n
}

// realTempDirRoot is t.TempDir() with links resolved (macOS's /var is
// /private/var), so paths the binaries print compare equal.
func realTempDirRoot(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// build compiles issue-cli and issue-viewer from this checkout into the
// newcomer's bin, as make install would into GOBIN.
func (n *newcomer) build() {
	n.t.Helper()
	for _, b := range []struct{ out, pkg string }{
		{"issue-cli", "./cmd/issue-cli/"},
		{"issue-viewer", "./cmd/issue-viewer/"},
	} {
		cmd := exec.Command("go", "build", "-o", filepath.Join(n.bin, b.out), b.pkg)
		cmd.Dir = repoRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			n.t.Fatalf("go build %s: %v\n%s", b.pkg, err, out)
		}
	}
}

// standInAgent puts a fake `name` CLI on PATH. Like an interactive agent it
// shows its prompt in the pane and stays up; it also records its arguments
// in <home>/<name>-args.txt (written aside and renamed into place, so a
// poll never reads half a file).
func (n *newcomer) standInAgent(name string) string {
	n.t.Helper()
	rec := filepath.Join(n.home, name+"-args.txt")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q.tmp && mv %q.tmp %q\nprintf '%%s\\n' \"$@\"\nexec sleep 300\n", rec, rec, rec)
	if err := os.WriteFile(filepath.Join(n.bin, name), []byte(script), 0o755); err != nil {
		n.t.Fatal(err)
	}
	return rec
}

// run runs a command in dir as the newcomer and returns its combined output.
func (n *newcomer) run(dir, name string, args ...string) (string, error) {
	n.t.Helper()
	path := name
	if !strings.Contains(name, "/") {
		path = filepath.Join(n.bin, name)
		if _, err := os.Stat(path); err != nil {
			path = name
		}
	}
	cmd := exec.Command(path, args...)
	cmd.Dir = dir
	cmd.Env = n.env
	out, err := cmd.CombinedOutput()
	n.t.Logf("$ %s %s\n%s", name, strings.Join(args, " "), out)
	return string(out), err
}

func (n *newcomer) must(dir, name string, args ...string) string {
	n.t.Helper()
	out, err := n.run(dir, name, args...)
	if err != nil {
		n.t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return out
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// waitFor polls cond every 100ms until it holds or the timeout passes.
func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return cond()
}

// TestNewcomerWalkthrough walks docs/getting-started.md as a newcomer on a
// clean machine (no terminal: set, no i3, no rtk or mem-guard): project
// init, git init, the viewer, a first issue, a first dispatch to a stand-in
// agent, a department from a template and the CEO. Each step is one a
// friend's fresh fork broke on, or could.
func TestNewcomerWalkthrough(t *testing.T) {
	if testing.Short() {
		t.Skip("builds both binaries")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Linux and macOS only")
	}
	n := newNewcomer(t)
	if _, err := exec.LookPath("i3-msg"); err == nil {
		t.Skip("i3-msg on PATH: the unset terminal: opens alacritty here")
	}
	claudeArgs := n.standInAgent("claude")

	config := filepath.Join(n.home, ".config", "ai-workflow-engine", "projects.yaml")
	shop := filepath.Join(n.home, "src", "shop")

	// 1. project init: folders, workflow.yaml, the projects.yaml entry.
	out := n.must(n.home, "issue-cli", "project", "init", shop, "--name", "Shop", "--config", config)
	for _, want := range []string{"workflow.yaml: wrote", "init", "issue-viewer -config"} {
		if !strings.Contains(out, want) {
			t.Errorf("project init output lacks %q", want)
		}
	}

	// 2. git init, as its Next lines say.
	git := func(args ...string) string {
		t.Helper()
		return n.must(shop, "git", append([]string{"-c", "user.name=Friend", "-c", "user.email=friend@example.invalid"}, args...)...)
	}
	git("init", "-q")
	git("add", "-A")
	git("commit", "-q", "-m", "init")

	// 3. The viewer.
	port := freePort(t)
	viewer := exec.Command(filepath.Join(n.bin, "issue-viewer"), "-config", config, "-port", fmt.Sprint(port))
	viewer.Dir = n.home
	viewer.Env = n.env
	var vlog strings.Builder
	viewer.Stdout, viewer.Stderr = &vlog, &vlog
	if err := viewer.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = viewer.Process.Kill()
		_, _ = viewer.Process.Wait()
		if t.Failed() {
			t.Logf("viewer log:\n%s", vlog.String())
		}
	})
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	get := func(path string) (int, string) {
		r, err := http.Get(base + path)
		if err != nil {
			return 0, ""
		}
		defer r.Body.Close()
		var b strings.Builder
		buf := make([]byte, 32<<10)
		for {
			k, err := r.Body.Read(buf)
			b.Write(buf[:k])
			if err != nil {
				break
			}
		}
		return r.StatusCode, b.String()
	}
	if !waitFor(20*time.Second, func() bool { c, _ := get("/"); return c == 200 }) {
		t.Fatalf("viewer never answered on %s", base)
	}
	for _, p := range []string{"/p/shop/company", "/p/shop/hall", "/p/shop/status", "/p/shop/needs-you"} {
		if c, _ := get(p); c != 200 {
			t.Errorf("GET %s = %d", p, c)
		}
	}

	// 4. A first issue, from any folder: issue-cli finds the running
	// viewer's config.
	n.must(n.home, "issue-cli", "--project", "shop", "create", "--title", "Add a dark mode toggle", "--system", "UI")

	// 5. A first dispatch. With no terminal: and no i3 it is headless and
	// says how to attach, instead of failing on alacritty.
	out = n.must(shop, "issue-cli", "--project", "shop", "dispatch", "ui/add-a-dark-mode-toggle", "--quote", "try the dark mode toggle")
	if !strings.Contains(out, "headless") || !strings.Contains(out, "tmux attach") {
		t.Errorf("dispatch should say it is headless and how to attach:\n%s", out)
	}
	if !waitFor(15*time.Second, func() bool { _, e := os.Stat(claudeArgs); return e == nil }) {
		t.Fatal("the stand-in agent never started")
	}
	if b, _ := os.ReadFile(claudeArgs); !strings.Contains(string(b), "ui/add-a-dark-mode-toggle") {
		t.Errorf("the agent's prompt doesn't name the issue:\n%.400s", b)
	}

	// 6. A department from a template, then the CEO.
	n.must(shop, "issue-cli", "--project", "shop", "department", "add", "marketing")
	if b, _ := os.ReadFile(config); !strings.Contains(string(b), "name: Marketing") {
		t.Errorf("projects.yaml has no Marketing department:\n%s", b)
	}
	if _, err := os.Stat(filepath.Join(shop, "workflows", "marketing.yaml")); err != nil {
		t.Errorf("department add: %v", err)
	}
	out = n.must(shop, "issue-cli", "--project", "shop", "lead", "spawn")
	if strings.Contains(out, "✗") {
		t.Errorf("lead spawn had a failed step:\n%s", out)
	}
	if !waitFor(10*time.Second, func() bool { _, body := get("/p/shop/company"); return strings.Contains(body, "Marketing") }) {
		t.Error("the company page doesn't show the new Marketing department")
	}

	// 7. Nothing the tools wrote for themselves shows in git status.
	status := git("status", "--porcelain")
	for _, line := range strings.Split(status, "\n") {
		if strings.Contains(line, ".agent-logs") || strings.Contains(line, ".health") {
			t.Errorf("tool state is untracked in git: %q", line)
		}
	}
}

// TestNewcomerWalkthroughNamedWhereItBlocks keeps the settled rule and the
// worker briefing pointing at this file's test: renaming it breaks both.
func TestNewcomerWalkthroughNamedWhereItBlocks(t *testing.T) {
	for _, f := range []string{repoPath("docs", "departments.md"), repoPath("internal", "tracker", "dispatch_prompt.go")} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "TestNewcomerWalkthrough") || !strings.Contains(string(b), "newcomer_test.go") {
			t.Errorf("%s no longer names TestNewcomerWalkthrough in newcomer_test.go", f)
		}
	}
	src, err := os.ReadFile("newcomer_test.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "func TestNewcomerWalkthrough(t *testing.T)") {
		t.Error("TestNewcomerWalkthrough is gone: update docs/departments.md's Settled rules and the briefing")
	}
}
