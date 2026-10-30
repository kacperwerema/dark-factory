package tracker

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestMatchGlobDirectory(t *testing.T) {
	for _, c := range []struct {
		pattern, path string
		want          bool
	}{
		{"internal/lint/", "internal/lint/paths.go", true},
		{"internal/lint/", "internal/lint/sub/x.go", true},
		{"internal/lint/", "internal/linter/x.go", false},
		{"systems*.go", "systems_wb.go", true},
		{"templates/_workshop_*.html", "templates/_workshop_systems.html", true},
		{"cmd/**/*.go", "cmd/issue-cli/main.go", true},
		{"", "a.go", false},
	} {
		if got := MatchGlob(c.pattern, c.path); got != c.want {
			t.Errorf("MatchGlob(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
	sys := WorkbenchSystem{Paths: []string{"a/", "*.md"}}
	if !sys.Owns("a/b.go") || !sys.Owns("docs/x.md") || sys.Owns("b/c.go") {
		t.Error("Owns matches any of the system's globs")
	}
}

func TestRepoFiles(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"a.go", "src/b.go", ".hidden/c.go", "ignored/d.go"} {
		os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0755)
		os.WriteFile(filepath.Join(dir, p), []byte("x\n"), 0644)
	}
	got := RepoFiles(dir)
	sort.Strings(got)
	if strings.Join(got, ",") != "a.go,ignored/d.go,src/b.go" {
		t.Errorf("walk without git skips dot-folders: %v", got)
	}

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored/\n"), 0644)
	got = RepoFiles(dir)
	sort.Strings(got)
	if strings.Join(got, ",") != ".gitignore,.hidden/c.go,a.go,src/b.go" {
		t.Errorf("in git: tracked and untracked files, nothing ignored: %v", got)
	}
	if RepoFiles("") != nil {
		t.Error("no root, no files")
	}
}
