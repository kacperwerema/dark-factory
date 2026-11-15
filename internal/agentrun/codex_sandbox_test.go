package agentrun

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// worktreeRepo makes a repo with a linked worktree and returns the resolved
// common dir, the worktree and the worktree's own git dir.
func worktreeRepo(t *testing.T) (common, wt, wtGit string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(dir, "repo")
	wt = filepath.Join(dir, "wt")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.email=a@b", "-c", "user.name=a", "-c", "gc.auto=0", "-c", "maintenance.auto=false"}, args...)...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run("init", "-q")
	run("commit", "-q", "--allow-empty", "-m", "init")
	run("worktree", "add", "-q", wt, "-b", "w")
	common = filepath.Join(repo, ".git")
	return common, wt, filepath.Join(common, "worktrees", "wt")
}

func TestCodexSandboxFlags(t *testing.T) {
	common, wt, wtGit := worktreeRepo(t)
	issues := filepath.Join(filepath.Dir(common), "issues")
	if err := os.MkdirAll(issues, 0o755); err != nil {
		t.Fatal(err)
	}
	ws := []string{"--sandbox", "workspace-write"}
	review := []string{"--ask-for-approval", "on-request", "-c", `approvals_reviewer="auto_review"`}
	never := []string{"--ask-for-approval", "never"}
	cases := []struct {
		name string
		cfg  tracker.CodexSandbox
		want []string
	}{
		{"issues", tracker.CodexSandbox{Writable: []string{"issues"}}, slices.Concat(ws, []string{"--add-dir", issues})},
		{"git", tracker.CodexSandbox{Writable: []string{"git"}}, slices.Concat(ws, []string{"--add-dir", common, "--add-dir", wtGit})},
		{"both", tracker.CodexSandbox{Writable: []string{"issues", "git"}}, slices.Concat(ws, []string{"--add-dir", issues, "--add-dir", common, "--add-dir", wtGit})},
		{"twice", tracker.CodexSandbox{Writable: []string{"git", "git"}}, slices.Concat(ws, []string{"--add-dir", common, "--add-dir", wtGit})},
		{"auto-review", tracker.CodexSandbox{Approval: "auto-review"}, slices.Concat(ws, review)},
		{"never", tracker.CodexSandbox{Approval: "never"}, slices.Concat(ws, never)},
		{"unknown beside known", tracker.CodexSandbox{Writable: []string{"home", "issues"}, Approval: "yolo"}, slices.Concat(ws, []string{"--add-dir", issues})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CodexSandboxFlags(&c.cfg, issues, wt)
			if !slices.Equal(got, c.want) {
				t.Fatalf("flags:\n got %q\nwant %q", got, c.want)
			}
			for _, a := range got {
				for _, bad := range []string{"dangerously", "danger-full-access", "network"} {
					if strings.Contains(a, bad) {
						t.Fatalf("forbidden %q in %q", bad, got)
					}
				}
			}
		})
	}
}

// A checkout that isn't a worktree has one git dir to name.
func TestCodexSandboxGitInAPlainCheckout(t *testing.T) {
	common, _, _ := worktreeRepo(t)
	got := CodexSandboxFlags(&tracker.CodexSandbox{Writable: []string{"git"}}, "", filepath.Dir(common))
	want := []string{"--sandbox", "workspace-write", "--add-dir", common}
	if !slices.Equal(got, want) {
		t.Fatalf("flags = %q, want %q", got, want)
	}
}

func TestCodexSandboxUnsetAddsNothing(t *testing.T) {
	_, wt, _ := worktreeRepo(t)
	for name, cfg := range map[string]*tracker.CodexSandbox{
		"no block":        nil,
		"empty block":     {},
		"ask":             {Approval: "ask"},
		"unknown":         {Writable: []string{"home"}, Approval: "yolo"},
		"no issue folder": {Writable: []string{"issues"}},
	} {
		if got := CodexSandboxFlags(cfg, "/no/such/issues", wt); got != nil {
			t.Errorf("%s: flags = %q, want none", name, got)
		}
	}
}

// The flags reach the agent as separate words, the reviewer's quotes intact.
func TestCodexSandboxReachesTheStart(t *testing.T) {
	record := standInAgent(t, "codex")
	isolateTmux(t)
	t.Setenv("SHELL", "/bin/sh")
	orig := PromptDeliveryTimeout
	PromptDeliveryTimeout = 0
	t.Cleanup(func() { PromptDeliveryTimeout = orig })

	dir := t.TempDir()
	wf := &tracker.WorkflowConfig{Routing: &tracker.RoutingConfig{Codex: &tracker.CodexSandbox{Approval: "auto-review", Writable: []string{"issues"}}}}
	proj := &tracker.Project{Slug: "p", Terminal: "none", WorkDir: dir, IssueDir: dir}
	resp := Start(proj, Spec{Session: "agent-x", Prompt: "the brief", AgentType: "codex", Headless: true, Workflow: wf})
	if resp.Status != "dispatched" {
		t.Fatalf("start: %q\n%s", resp.Status, stepNames(resp.Steps))
	}
	argv := readArgv(t, record)
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--sandbox", "workspace-write", "--add-dir", resolved, "--ask-for-approval", "on-request", "-c", `approvals_reviewer="auto_review"`, "the brief"}
	if len(argv) < len(want) || !slices.Equal(argv[len(argv)-len(want):], want) {
		t.Fatalf("argv:\n got %q\nwant tail %q", argv, want)
	}
}

// A department file with no block follows the project; its own block wins.
func TestCodexSandboxFollowsTheProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "workflow.yaml"), []byte("statuses: [{name: idea}]\nrouting:\n  codex:\n    approval: never\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	proj := &tracker.Project{Slug: "p", WorkDir: dir, IssueDir: dir, WorkflowFile: filepath.Join(dir, "workflow.yaml")}
	dept := &tracker.WorkflowConfig{Routing: &tracker.RoutingConfig{Default: "codex"}}
	if got := codexSandbox(proj, dept); got == nil || got.Approval != "never" {
		t.Fatalf("department with no block: %+v", got)
	}
	own := &tracker.CodexSandbox{Approval: "ask"}
	dept.Routing.Codex = own
	if got := codexSandbox(proj, dept); got != own {
		t.Fatalf("department's own block: %+v", got)
	}
}

// With the real codex, from scripts/checks (CHECK_REAL_CODEX=1). It spends
// nothing: debug prompt-input and codex sandbox make no model call. It pins
// what the flags are built on, so a codex update that changes it fails here:
// --add-dir reaches the permission profile, the network stays closed, and a
// commit from a worktree needs the worktree's own git dir as well as the
// common dir. /tmp and $TMPDIR are closed so the repo under them counts.
func TestRealCodexSandboxFlags(t *testing.T) {
	if os.Getenv("CHECK_REAL_CODEX") == "" {
		t.Skip("set CHECK_REAL_CODEX=1 to run the real codex")
	}
	common, wt, wtGit := worktreeRepo(t)
	issues := filepath.Join(filepath.Dir(common), "issues")
	if err := os.MkdirAll(issues, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &tracker.CodexSandbox{Writable: []string{"issues", "git"}, Approval: "auto-review"}
	args := slices.Concat(CodexStartFlags(wt), CodexSandboxFlags(cfg, issues, wt), []string{"debug", "prompt-input", "x"})
	cmd := exec.Command("codex", args...)
	cmd.Dir = wt
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("codex %q: %v", args, err)
	}
	prompt := string(out)
	for _, want := range []string{"`sandbox_mode` is `workspace-write`", "Network access is restricted", "`approvals_reviewer` is `auto_review`"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt input lacks %q", want)
		}
	}
	for _, dir := range []string{issues, common, wtGit} {
		if !strings.Contains(prompt, `<entry access=\"write\"><path>`+dir+`</path>`) {
			t.Errorf("no write entry for %s", dir)
		}
	}

	commit := func(msg string, roots ...string) error {
		quoted := make([]string, len(roots))
		for i, r := range roots {
			quoted[i] = `"` + r + `"`
		}
		cmd := exec.Command("codex", "sandbox",
			"-c", `sandbox_mode="workspace-write"`,
			"-c", "sandbox_workspace_write.exclude_slash_tmp=true",
			"-c", "sandbox_workspace_write.exclude_tmpdir_env_var=true",
			"-c", "sandbox_workspace_write.writable_roots=["+strings.Join(quoted, ",")+"]",
			"--", "git", "-c", "user.email=a@b", "-c", "user.name=a", "commit", "-q", "--allow-empty", "-m", msg)
		cmd.Dir = wt
		out, err := cmd.CombinedOutput()
		t.Logf("commit with %d roots: %v\n%s", len(roots), err, out)
		return err
	}
	if err := commit("common only", common); err == nil {
		t.Error("a commit passed with only the common dir: codex no longer protects the worktree's git dir, so writable: git can drop it")
	}
	if err := commit("both", common, wtGit); err != nil {
		t.Errorf("a commit failed with both git dirs: %v", err)
	}
}
