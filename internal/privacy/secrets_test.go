package privacy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeKeys are key-shaped values built at run time, so this file carries
// none itself (make leak-check reads it too). Each is random-looking
// enough to pass the placeholder test.
func fakeKeys() map[string]string {
	r32 := "Q7wz" + "K2mP9xLr4TbV8nYc3HsJ6dFg1AeU5oWi"
	r40 := r32 + "Zq0Xv8Rt"
	return map[string]string{
		"an ElevenLabs key (sk_…)":                            "sk" + "_" + r32,
		"a Stripe live key (sk_live_… / rk_live_…)":           "rk" + "_live_" + r32[:24],
		"a GitHub token (ghp_…)":                              "gh" + "p_" + r40[:36],
		"a GitHub token (github_pat_…)":                       "github" + "_pat_" + r40 + "_" + r32,
		"an AWS access key (AKIA…)":                           "AK" + "IA" + "Q7WZK2MP9XLR4TBV",
		"a Slack token (xox…)":                                "xo" + "xb-" + "123456789012-" + r32[:24],
		"an OpenAI or Anthropic key (sk-proj-… / sk-ant-…)":   "sk" + "-ant-" + r40,
		"a private key block (-----BEGIN … PRIVATE KEY-----)": "-----BEGIN " + "RSA PRIVATE" + " KEY-----",
		"a JWT (eyJ….eyJ….…)":                                 "ey" + "JhbGciOiJIUzI1NiJ9." + "ey" + "JzdWIiOiIxMjM0NTY3ODkwIn0." + r32,
	}
}

func TestSecretsInTextFindsEveryKind(t *testing.T) {
	for what, key := range fakeKeys() {
		got := SecretsInText(`KEY="` + key + `"`)
		if len(got) != 1 || got[0] != what {
			t.Errorf("%s: got %v", what, got)
		}
	}
}

func TestScanSecretsPassesExamplesAndPlaceholders(t *testing.T) {
	for _, line := range []string{
		"ELEVENLABS_API_KEY=sk_…",
		"sk_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
		"AKIA" + "IOSFODNN7EXAMPLE",
		"ghp_" + strings.Repeat("0", 36),
		"xoxb-your-token-here",
		"a task_sk_live note", "eyJ alone",
		"sk-ant-api03-" + strings.Repeat("x", 40),
	} {
		if got := SecretsInText(line); len(got) != 0 {
			t.Errorf("%q: %v, want none (a placeholder)", line, got)
		}
	}
	for p, want := range map[string]bool{
		".env": true, "app/.env": true, ".env.local": true, ".env.production": true,
		".env.example": false, "web/.env.sample": false, ".env.template": false,
		"env.go": false, ".envrc": false, "docs/.env.md.txt": true,
	} {
		if EnvFile(p) != want {
			t.Errorf("EnvFile(%q) = %v, want %v", p, !want, want)
		}
	}
	dir := leakRepo(t, map[string]string{
		".env.example": "ELEVENLABS_API_KEY=\nPORT=3000\n",
		"README.md":    "set ELEVENLABS_API_KEY=sk_… in Keychain\n",
	})
	if leaks, err := ScanSecrets(dir, ""); err != nil || len(leaks) != 0 {
		t.Errorf("an example env file with empty values and a doc: %v %v, want none", leaks, err)
	}
}

func TestScanSecretsNamesFileAndLine(t *testing.T) {
	keys := fakeKeys()
	gh := keys["a GitHub token (ghp_…)"]
	dir := leakRepo(t, map[string]string{
		"cfg/app.yaml":  "name: x\ntoken: " + gh + "\n",
		".env":          "",
		"web/.env.prod": "A=1\n",
		".env.example":  "ELEVENLABS_API_KEY=" + keys["an ElevenLabs key (sk_…)"] + "\n",
	})
	leaks, err := ScanSecrets(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, l := range leaks {
		got[l.String()] = true
	}
	for _, w := range []string{
		"cfg/app.yaml:2: a GitHub token (ghp_…)",
		".env:1: " + envFileWhat,
		"web/.env.prod:1: " + envFileWhat,
		".env.example:1: an ElevenLabs key (sk_…)",
	} {
		if !got[w] {
			t.Errorf("missing %q in %v", w, leaks)
		}
	}
	if len(leaks) != 4 {
		t.Errorf("%d hits, want 4: %v", len(leaks), leaks)
	}
}

func TestSecretRefusalNeverPrintsTheValue(t *testing.T) {
	var files = map[string]string{}
	var body strings.Builder
	for _, key := range fakeKeys() {
		body.WriteString("x = " + key + "\n")
	}
	files["leak.txt"] = body.String()
	dir := leakRepo(t, files)
	leaks, err := ScanSecrets(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(leaks) != len(fakeKeys()) {
		t.Fatalf("%d hits, want %d: %v", len(leaks), len(fakeKeys()), leaks)
	}
	msg := SecretRefusal("secret guard: this push", leaks)
	for what, key := range fakeKeys() {
		// the PEM header is a fixed line, not a value; every other key's
		// variable part must not show, not even its first characters.
		if strings.HasPrefix(key, "-----") {
			continue
		}
		if strings.Contains(msg, key[len(key)-12:]) || strings.Contains(msg, key[:len(key)/2]) {
			t.Errorf("the refusal shows part of %s: %s", what, msg)
		}
	}
	for _, w := range []string{"leak.txt:1:", "docs/departments.md#secrets", "rotate", "Keychain"} {
		if !strings.Contains(msg, w) {
			t.Errorf("the refusal lacks %q: %s", w, msg)
		}
	}
}

// pushRepo is a repo with a bare remote "origin" holding its first commit.
func pushRepo(t *testing.T, files map[string]string) (dir string, git func(...string) string) {
	t.Helper()
	dir = leakRepo(t, files)
	remote := t.TempDir()
	git = func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "--bare", remote)
	git("remote", "add", "origin", remote)
	git("push", "-q", "--no-verify", "origin", "main")
	return dir, git
}

func TestPushSecretsReadsOnlyWhatThePushSends(t *testing.T) {
	keys := fakeKeys()
	// An old key, already on the remote: this push doesn't touch it.
	dir, git := pushRepo(t, map[string]string{"old.txt": "k=" + keys["an AWS access key (AKIA…)"] + "\n", "a.txt": "a\n"})
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	refs := func() []PushRef {
		return []PushRef{{"refs/heads/main", git("rev-parse", "HEAD"), "refs/heads/main", git("rev-parse", "origin/main")}}
	}

	write("a.txt", "a\nb\n")
	git("commit", "-q", "-am", "clean")
	if leaks, err := PushSecrets(dir, "origin", refs()); err != nil || len(leaks) != 0 {
		t.Fatalf("a clean push beside an old key: %v %v, want none", leaks, err)
	}

	// A key added then removed again still travels in the pushed history.
	write("a.txt", "a\nb\ntoken: "+keys["a Slack token (xox…)"]+"\n")
	git("commit", "-q", "-am", "add")
	write("a.txt", "a\nb\n")
	write(".env.local", "")
	git("add", ".env.local")
	git("commit", "-q", "-am", "remove")
	leaks, err := PushSecrets(dir, "origin", refs())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, l := range leaks {
		got = append(got, l.String())
	}
	want := []string{".env.local:1: " + envFileWhat, "a.txt:3: a Slack token (xox…)"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// A delete sends nothing.
	if leaks, _ := PushSecrets(dir, "origin", []PushRef{{"(delete)", strings.Repeat("0", 40), "refs/heads/x", git("rev-parse", "HEAD")}}); len(leaks) != 0 {
		t.Errorf("a delete: %v", leaks)
	}
}

func TestScanStagedSecrets(t *testing.T) {
	dir, git := pushRepo(t, map[string]string{"a.txt": "a\n"})
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"+fakeKeys()["a JWT (eyJ….eyJ….…)"]+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	leaks, err := ScanStagedSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(leaks) != 2 || leaks[0].Path != ".env" || leaks[1].String() != "a.txt:2: a JWT (eyJ….eyJ….…)" {
		t.Errorf("got %v", leaks)
	}
}

func TestTrackedFilesCarryNoSecrets(t *testing.T) {
	root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skip("not a git checkout")
	}
	leaks, err := ScanSecrets(strings.TrimSpace(string(root)), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(leaks) > 0 {
		t.Error(SecretRefusal("make leak-check", leaks))
	}
}

// This repo's .gitignore keeps env files out and lets .env.example in.
func TestRepoIgnoresEnvFiles(t *testing.T) {
	root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skip("not a git checkout")
	}
	dir := strings.TrimSpace(string(root))
	for p, ignored := range map[string]bool{".env": true, ".env.local": true, "web/.env.production": true, ".env.example": false} {
		cmd := exec.Command("git", "check-ignore", "-q", "--no-index", p)
		cmd.Dir = dir
		if got := cmd.Run() == nil; got != ignored {
			t.Errorf("git check-ignore %s: ignored %v, want %v", p, got, ignored)
		}
	}
}
