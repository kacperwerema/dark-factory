package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGitHubToken is built at run time, so no tracked line holds one.
func fakeGitHubToken() string {
	return "gh" + "p_" + "Q7wzK2mP9xLr4TbV8nYc3HsJ6dFg1AeU5oWiZq0X"
}

// A project with no privacy: block still gets the secret check from its
// pre-push guard: a key or an .env file in a pushed commit is refused with
// file and line, never the value; a clean push goes out. privacy check
// shows the guard's state with its fix.
func TestPrivacyPrePushRefusesSecretsInEveryProject(t *testing.T) {
	proj, root, _ := makePrivacyFixture(t, "")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runPrivacy(ctx, []string{"check"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "secret amber  Secrets unguarded")
	assertContains(t, stdout.String(), "fix: issue-cli privacy hook")
	ctx, _, _ = newTestContext(proj, false)
	if err := runPrivacy(ctx, []string{"hook"}); err != nil {
		t.Fatal(err)
	}
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runPrivacy(ctx, []string{"check"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "secret green  Secrets guarded")

	base := gitOut(t, root, "rev-parse", "HEAD")
	write := func(rel, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, rel), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	push := func() error {
		ctx, _, _ := newTestContext(proj, false)
		ctx.Stdin = strings.NewReader("refs/heads/main " + gitOut(t, root, "rev-parse", "HEAD") + " refs/heads/main " + base + "\n")
		return runPrivacy(ctx, []string{"pre-push", "origin", "git@example:o/r.git"})
	}

	write("config.go", "package main\n\nconst token = \""+fakeGitHubToken()+"\"\n")
	write(".env", "ELEVENLABS_API_KEY=x\n")
	gitIn(t, root, "add", "-f", "config.go", ".env")
	gitIn(t, root, "commit", "-q", "-m", "oops")
	err := push()
	var code *exitCodeError
	if !errors.As(err, &code) || code.Code != 1 {
		t.Fatalf("pre-push = %v, want a refusal", err)
	}
	msg := err.Error()
	for _, w := range []string{"secret guard: this push: 2 secret(s)", "config.go:3: a GitHub token (ghp_…)", ".env:1: an env file", "docs/departments.md#secrets", "rotate"} {
		assertContains(t, msg, w)
	}
	if strings.Contains(msg, fakeGitHubToken()[4:]) {
		t.Errorf("the refusal shows the token: %s", msg)
	}

	gitIn(t, root, "reset", "-q", "--hard", base)
	write(".env.example", "ELEVENLABS_API_KEY=\n")
	gitIn(t, root, "add", ".env.example")
	gitIn(t, root, "commit", "-q", "-m", "example")
	if err := push(); err != nil {
		t.Errorf("an .env.example with empty values refused: %v", err)
	}
}

// The commit-msg hook refuses a commit whose staged lines hold a key,
// naming file and line, never the value.
func TestCommitMsgRefusesAStagedSecret(t *testing.T) {
	proj, root, _ := makePrivacyFixture(t, "")
	onWorkBranch(t, root)
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\n// "+fakeGitHubToken()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "add", "main.go")
	ctx, _, stderr := newTestContext(proj, false)
	err := runCommitMsg(ctx, []string{commitMsgFile(t, "fix: x\n")})
	var code *exitCodeError
	if !errors.As(err, &code) || code.Code != 1 {
		t.Fatalf("commit-msg = %v, want a refusal\n%s", err, stderr)
	}
	assertContains(t, stderr.String(), "✗ commit refused: 1 secret(s)")
	assertContains(t, stderr.String(), "main.go:3: a GitHub token (ghp_…)")
	if strings.Contains(stderr.String(), fakeGitHubToken()[4:]) {
		t.Errorf("the refusal shows the token: %s", stderr)
	}

	// Without the key the same commit goes through.
	gitIn(t, root, "reset", "-q", "--hard")
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\n// clean\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "add", "main.go")
	ctx, _, stderr = newTestContext(proj, false)
	if err := runCommitMsg(ctx, []string{commitMsgFile(t, "fix: x\n")}); err != nil {
		t.Errorf("a clean commit refused: %v\n%s", err, stderr)
	}
}
