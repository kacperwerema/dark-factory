package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeOwnerHome is an owner's home with stand-ins for what Claude Code
// keeps there: a keychain folder and a credentials file holding no real
// secret, settings with a hook, and a .claude.json with account metadata
// and the history and MCP servers a sandbox must not see.
func fakeOwnerHome(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	write(t, filepath.Join(h, "Library", "Keychains", "login.keychain-db"), "stand-in")
	write(t, filepath.Join(h, ".claude", ".credentials.json"), `{"stand-in":true}`)
	write(t, filepath.Join(h, ".codex", "auth.json"), `{"stand-in":true}`)
	write(t, filepath.Join(h, ".codex", "config.toml"), "stand-in")
	write(t, filepath.Join(h, ".claude", "settings.json"), `{"hooks":{"SessionStart":[]}}`)
	write(t, filepath.Join(h, ".claude.json"), `{"oauthAccount":{"emailAddress":"owner@example.com"},"userID":"u1","projects":{"/x":{}},"mcpServers":{"m":{}}}`)
	return h
}

// homeWorker records what it sees of its HOME (links by readlink, never by
// reading what they point at), writes its transcript there and prints no
// result, so the usage can only come from that transcript.
const homeWorker = `echo "$HOME" > home.seen; echo "cfg=$CLAUDE_CONFIG_DIR" > cfg.seen; ` +
	`test -e "$HOME/.claude/settings.json" && echo yes > settings.seen; test -e "$HOME/.codex/config.toml" && echo yes >> settings.seen; ` +
	`readlink "$HOME/.codex/auth.json" > codex.seen; echo "codex=$CODEX_HOME" >> cfg.seen; ` +
	`readlink "$HOME/Library/Keychains" > keychain.seen; readlink "$HOME/.claude/.credentials.json" > creds.seen; ` +
	`cp "$HOME/.claude.json" claude-json.seen; echo "$GOMODCACHE" > gomod.seen; ` +
	`d="$HOME/.claude/projects/$(pwd | sed 's/[^A-Za-z0-9]/-/g')"; mkdir -p "$d"; ` +
	`echo '{"type":"assistant","sessionId":"s","message":{"id":"m1","model":"claude-opus-5-5","usage":{"input_tokens":700,"output_tokens":7}}}' > "$d/s.jsonl"; ` +
	`printf 'package calc\n\nfunc Add(a, b int) int { return a + b }\n' > calc.go`

func seen(t *testing.T, repo, name string) string {
	t.Helper()
	data, _ := os.ReadFile(filepath.Join(repo, name))
	return strings.TrimSpace(string(data))
}

func TestTheWorkerRunsInItsOwnHomeWithOnlyTheLoginLinked(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	f := newFixture(t)
	env := f.env(t)
	env.Keep = true
	env.Home = fakeOwnerHome(t)
	var log bytes.Buffer
	env.Log = &log
	run, err := Execute(context.Background(), f.benchmark(homeWorker, f.task), env, Options{})
	if err != nil {
		t.Fatal(err)
	}
	r := run.Tasks[0]
	if r.Outcome != OutcomePass {
		t.Fatalf("record = %+v", r)
	}
	if run.Setup.WorkerHome != WorkerHomeSandbox {
		t.Errorf("setup worker_home = %q", run.Setup.WorkerHome)
	}
	if !strings.Contains(log.String(), "worker home: sandbox, login linked: keychain, credentials file, codex login") {
		t.Errorf("log = %q", log.String())
	}
	dir := filepath.Join(env.TmpRoot, run.ID, "01")
	repo := filepath.Join(dir, "repo")
	if got := seen(t, repo, "home.seen"); got != filepath.Join(dir, "home") {
		t.Errorf("worker HOME = %q", got)
	}
	if got := seen(t, repo, "codex.seen"); got != filepath.Join(env.Home, ".codex", "auth.json") {
		t.Errorf("codex login link = %q", got)
	}
	if got := seen(t, repo, "cfg.seen"); got != "cfg=\ncodex=" {
		t.Errorf("worker saw %s", got)
	}
	if got := seen(t, repo, "settings.seen"); got != "" {
		t.Errorf("the owner's settings.json or codex config was visible")
	}
	if got := seen(t, repo, "keychain.seen"); got != filepath.Join(env.Home, "Library", "Keychains") {
		t.Errorf("keychain link = %q", got)
	}
	if got := seen(t, repo, "creds.seen"); got != filepath.Join(env.Home, ".claude", ".credentials.json") {
		t.Errorf("credentials link = %q", got)
	}
	var cj map[string]any
	if err := json.Unmarshal([]byte(seen(t, repo, "claude-json.seen")), &cj); err != nil {
		t.Fatal(err)
	}
	if len(cj) != 3 || cj["hasCompletedOnboarding"] != true || cj["userID"] != "u1" || cj["oauthAccount"] == nil {
		t.Errorf(".claude.json = %v", cj)
	}
	if seen(t, repo, "gomod.seen") == "" {
		t.Errorf("GOMODCACHE not pinned for the sandbox HOME")
	}
	// The usage is the transcript in the sandbox HOME.
	if !r.Usage.Known || r.Usage.Tokens() != 707 || r.Usage.Source != "transcript" {
		t.Errorf("usage = %+v", r.Usage)
	}
	if _, err := os.Stat(filepath.Join(env.Home, ".claude", "projects")); err == nil {
		t.Errorf("a transcript landed in the owner's home")
	}
}

func TestAClaudeConfigDirIsWhereTheCredentialsLinkPoints(t *testing.T) {
	cfg := t.TempDir()
	write(t, filepath.Join(cfg, ".credentials.json"), "stand-in")
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv("CODEX_HOME", t.TempDir())
	home := filepath.Join(t.TempDir(), "home")
	linked, err := prepareWorkerHome(home, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(linked) != 1 || linked[0] != "credentials file" {
		t.Fatalf("linked = %v", linked)
	}
	if got, _ := os.Readlink(filepath.Join(home, ".claude", ".credentials.json")); got != filepath.Join(cfg, ".credentials.json") {
		t.Errorf("link = %q", got)
	}
}

func TestWorkerHomeOwnerKeepsTheOwnersHome(t *testing.T) {
	f := newFixture(t)
	env := f.env(t)
	env.Keep = true
	env.Home = t.TempDir()
	// Transcripts are read from Env.Home under worker_home: owner; the
	// worker writes there through HOME_T, never into the real HOME.
	worker := `echo "$HOME" > home.seen; d="$HOME_T/.claude/projects/$(pwd | sed 's/[^A-Za-z0-9]/-/g')"; mkdir -p "$d"; ` +
		`echo '{"type":"assistant","sessionId":"s","message":{"id":"m1","usage":{"input_tokens":40,"output_tokens":2}}}' > "$d/s.jsonl"`
	b := f.benchmark("HOME_T="+env.Home+" sh -c '"+strings.ReplaceAll(worker, "'", `'\''`)+"'", f.task)
	b.WorkerHome = WorkerHomeOwner
	run, err := Execute(context.Background(), b, env, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if run.Setup.WorkerHome != WorkerHomeOwner {
		t.Errorf("setup worker_home = %q", run.Setup.WorkerHome)
	}
	repo := filepath.Join(env.TmpRoot, run.ID, "01", "repo")
	if got := seen(t, repo, "home.seen"); got != os.Getenv("HOME") {
		t.Errorf("worker HOME = %q, want the owner's", got)
	}
	if r := run.Tasks[0]; r.Usage.Tokens() != 42 {
		t.Errorf("usage = %+v", r.Usage)
	}
	if _, err := os.Stat(filepath.Join(env.TmpRoot, run.ID, "01", "home")); err == nil {
		t.Errorf("a sandbox home was made under worker_home: owner")
	}
}

func TestLoadRefusesAnUnknownWorkerHome(t *testing.T) {
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", t.TempDir())
	write(t, BenchmarkPath("w"), "name: w\nworker_home: none\ntasks: []\n")
	if _, err := Load("w"); err == nil || !strings.Contains(err.Error(), `worker_home: "none"`) {
		t.Fatalf("err = %v", err)
	}
	write(t, BenchmarkPath("w"), "name: w\nworker_home: owner\ntasks: []\n")
	if b, err := Load("w"); err != nil || b.HomeMode() != WorkerHomeOwner {
		t.Fatalf("b = %+v, err = %v", b, err)
	}
}

func TestAWorkerWithNoLoginEndsAsErrorNamingTheDoc(t *testing.T) {
	f := newFixture(t)
	env := f.env(t)
	env.Home = t.TempDir()
	var log bytes.Buffer
	env.Log = &log
	worker := `echo '{"type":"result","is_error":true,"result":"Not logged in · Please run /login"}'; exit 1`
	run, err := Execute(context.Background(), f.benchmark(worker, f.task), env, Options{})
	if err != nil {
		t.Fatal(err)
	}
	r := run.Tasks[0]
	if r.Outcome != OutcomeError || r.Reason != NoLoginReason {
		t.Fatalf("record = %+v", r)
	}
	if !strings.Contains(r.Reason, "docs/benchmark.md#the-workers-home") || !strings.Contains(r.Reason, "worker_home: owner") {
		t.Errorf("reason = %q", r.Reason)
	}
	if !strings.Contains(log.String(), "no login found to link") {
		t.Errorf("log = %q", log.String())
	}
}

func TestAFailingWorkerThatMentionsLoginInPassingIsNotANoLoginError(t *testing.T) {
	if saysNoLogin([]byte("fixed the login page")) {
		t.Error("a plain mention of login counted")
	}
	if !saysNoLogin(nil, []byte("Invalid API key · Please run /login")) {
		t.Error("stderr's no-login message missed")
	}
}
