package privacy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// leakRepo makes a git repo with the given files committed.
func leakRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	// {u} and {uid} become a real-looking user and uid only here, so this
	// file itself carries no leak.
	fill := strings.NewReplacer("{u}", "j"+"doe", "{uid}", "5"+"01")
	for name, body := range files {
		body = fill.Replace(body)
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", "-A")
	run("-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "x")
	return dir
}

func TestScanLeaksFindsHomePathsScratchpadsAndNames(t *testing.T) {
	dir := leakRepo(t, map[string]string{
		"a/result.json": `{"output": "file:///private/tmp/claude-{uid}/-Users-{u}-Repo-x/scratchpad/out.txt"}` + "\n",
		"b/notes.md":    "ran it from /Users/{u}/Repo/x\nand /home/{u}/work too\n",
		"c/win.txt":     `C:\Users\{u}\repo` + "\n",
		"d/brand.md":    "the board runs the engine, Secret-Game and more\n",
		"e/clean.go":    "home := \"/home/someone/project\"\nx := \"/Users/a/Repo/x.y\" // -Users-a-Repo-x-y\n$stage/home/.local and $sb/home/secret.txt\nhttps://example.com/news/home/2025/x\n",
		"f/img.bin":     "\x00\x01/Users/{u}/\x00",
	})
	leaks, err := ScanLeaks(dir, "", []string{"secret-game"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, l := range leaks {
		got[l.Path] = append(got[l.Path], l.What)
	}
	want := map[string]int{"a/result.json": 2, "b/notes.md": 2, "c/win.txt": 1, "d/brand.md": 1}
	for p, n := range want {
		if len(got[p]) != n {
			t.Errorf("%s: %d leaks %v, want %d", p, len(got[p]), got[p], n)
		}
	}
	for _, p := range []string{"e/clean.go", "f/img.bin"} {
		if len(got[p]) != 0 {
			t.Errorf("%s: leaks %v, want none (placeholders, variables, URLs, binaries)", p, got[p])
		}
	}
	if s := strings.Join(got["d/brand.md"], ""); !strings.Contains(s, "private name") || strings.Contains(s, "Secret-Game") {
		t.Errorf("a name hit should say what kind it is without repeating the name: %q", s)
	}
}

func TestScanLeaksAtARevision(t *testing.T) {
	dir := leakRepo(t, map[string]string{"x.md": "/Users/{u}/x\n"})
	if err := os.WriteFile(filepath.Join(dir, "x.md"), []byte("clean\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if l, _ := ScanLeaks(dir, "", nil); len(l) != 0 {
		t.Errorf("working tree is clean, got %v", l)
	}
	if l, _ := ScanLeaks(dir, "HEAD", nil); len(l) != 1 || l[0].Path != "x.md" || l[0].Line != 1 {
		t.Errorf("HEAD has the leak, got %+v", l)
	}
}

func TestPrivateNamesFile(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	if names, path := PrivateNames(); len(names) != 0 || path != "" {
		t.Errorf("no file: %v %q", names, path)
	}
	p := filepath.Join(cfg, "ai-workflow-engine", "private-names.txt")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("# the owner's private projects\nSecret Game\n\n  secret-game \n"), 0o600)
	names, path := PrivateNames()
	if path != p || len(names) != 2 || names[0] != "Secret Game" || names[1] != "secret-game" {
		t.Errorf("got %q from %q", names, path)
	}
}

// TestTrackedFilesCarryNoLeaks scans this repository's tracked files: no
// user home path or scratchpad path, and none of the names in the owner's
// private-names list (skipped part when the list is absent). A test input
// that needs a fake home path uses a placeholder user (placeholderUsers in
// leaks.go): add a name there, never a real one.
func TestTrackedFilesCarryNoLeaks(t *testing.T) {
	root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skip("not a git checkout")
	}
	names, _ := PrivateNames()
	leaks, err := ScanLeaks(strings.TrimSpace(string(root)), "", names)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range leaks {
		t.Errorf("%s", l)
	}
}
