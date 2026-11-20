package checks

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

var buildNoon = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func writeAt(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestBuildStaleFile(t *testing.T) {
	root := t.TempDir()
	build := filepath.Join(root, "issue-cli")
	writeAt(t, build, buildNoon)
	writeAt(t, filepath.Join(root, "cmd", "issue-cli", "main.go"), buildNoon.Add(-time.Hour))
	writeAt(t, filepath.Join(root, "docs", "later.md"), buildNoon.Add(time.Hour)) // not code

	if s, err := BuildStale(root, build); err != nil || s != nil {
		t.Fatalf("older code: %+v, %v", s, err)
	}

	// The same instant as the build is not newer.
	writeAt(t, filepath.Join(root, "internal", "same.go"), buildNoon)
	if s, err := BuildStale(root, build); err != nil || s != nil {
		t.Fatalf("same time: %+v, %v", s, err)
	}

	// The newest of several, under either folder, and not only .go files: the
	// binary embeds templates.
	writeAt(t, filepath.Join(root, "cmd", "issue-cli", "edited.go"), buildNoon.Add(time.Minute))
	writeEmbedding(t, filepath.Join(root, "internal", "viewer", "embed.go"), "viewer", "templates/*.html", buildNoon.Add(-time.Hour))
	writeAt(t, filepath.Join(root, "internal", "viewer", "templates", "page.html"), buildNoon.Add(time.Hour))
	s, err := BuildStale(root, build)
	if err != nil || s == nil {
		t.Fatalf("newer file: %+v, %v", s, err)
	}
	if s.What != filepath.Join("internal", "viewer", "templates", "page.html") || !s.Changed.Equal(buildNoon.Add(time.Hour)) || !s.Built.Equal(buildNoon) {
		t.Fatalf("got %+v", s)
	}
}

// writeEmbedding writes a Go file whose go:embed line names patterns.
func writeEmbedding(t *testing.T, path, pkg, patterns string, at time.Time) {
	t.Helper()
	src := "package " + pkg + "\n\nimport \"embed\"\n\n//go:embed " + patterns + "\nvar files embed.FS\n"
	writeAt(t, path, at)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// A run leaves files beside the code: none of them is in the build.
func TestBuildStaleIgnoresWhatARunLeaves(t *testing.T) {
	root := t.TempDir()
	build := filepath.Join(root, "issue-cli")
	writeAt(t, build, buildNoon)
	cli := filepath.Join(root, "cmd", "issue-cli")
	writeAt(t, filepath.Join(cli, "main.go"), buildNoon.Add(-time.Hour))
	later := buildNoon.Add(time.Hour)
	writeAt(t, filepath.Join(cli, ".agent-logs", "clilog.jsonl"), later)
	writeAt(t, filepath.Join(cli, "run.clilog"), later)
	writeAt(t, filepath.Join(cli, "cover.out"), later)
	writeAt(t, filepath.Join(cli, "testdata", "fixture.go"), later)
	writeAt(t, filepath.Join(cli, "main_test.go"), later) // tests are in no build
	writeAt(t, filepath.Join(root, "internal", "health", "snapshot.json"), later)
	if s, err := BuildStale(root, build); err != nil || s != nil {
		t.Fatalf("what a run left: %+v, %v", s, err)
	}

	// The same folders still hold the code: a changed .go file reads stale.
	writeAt(t, filepath.Join(cli, "main.go"), later)
	s, err := BuildStale(root, build)
	if err != nil || s == nil {
		t.Fatalf("changed source: %+v, %v", s, err)
	}
	if s.What != filepath.Join("cmd", "issue-cli", "main.go") {
		t.Fatalf("got %+v", s)
	}
}

func TestBuildStaleEmbeddedFiles(t *testing.T) {
	later := buildNoon.Add(time.Hour)
	tests := []struct {
		name     string
		patterns string
		file     string
		stale    bool
	}{
		{"one file", "CHANGELOG.md", "CHANGELOG.md", true},
		{"glob", "static/*.css", "static/a.css", true},
		{"outside the glob", "static/*.css", "static/a.log", false},
		{"under a folder", "guides", "guides/deep/a.md", true},
		{"hidden in a folder", "guides", "guides/.tmp.md", false},
		{"hidden folder in a folder", "guides", "guides/.logs/a.md", false},
		{"underscore in a folder", "guides", "guides/_draft.md", false},
		{"hidden with all:", "all:guides", "guides/.tmp.md", true},
		{"hidden named by a glob", "static/*", "static/.keep", true},
		{"second pattern", "a.txt b.txt", "b.txt", true},
		{"quoted pattern", `"with space.txt"`, "with space.txt", true},
		{"not named", "CHANGELOG.md", "OTHER.md", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A [ in the checkout's path is no glob: patterns start at the package.
			root := filepath.Join(t.TempDir(), "check[out")
			build := filepath.Join(root, "issue-cli")
			writeAt(t, build, buildNoon)
			pkg := filepath.Join(root, "internal", "pkg")
			writeEmbedding(t, filepath.Join(pkg, "embed.go"), "pkg", tt.patterns, buildNoon.Add(-time.Hour))
			want := filepath.Join(pkg, filepath.FromSlash(tt.file))
			writeAt(t, want, later)

			s, err := BuildStale(root, build)
			if err != nil {
				t.Fatal(err)
			}
			if !tt.stale {
				if s != nil {
					t.Fatalf("got %+v", s)
				}
				return
			}
			rel, _ := filepath.Rel(root, want)
			if s == nil || s.What != rel {
				t.Fatalf("got %+v, want %s", s, rel)
			}
		})
	}
}

// The patterns of a package with a broken file still count: the worker is
// mid-edit and the embed in the other file is still in the build.
func TestBuildStaleEmbedBesideABrokenFile(t *testing.T) {
	root := t.TempDir()
	build := filepath.Join(root, "issue-cli")
	writeAt(t, build, buildNoon)
	pkg := filepath.Join(root, "internal", "pkg")
	writeEmbedding(t, filepath.Join(pkg, "embed.go"), "pkg", "a.txt", buildNoon.Add(-time.Hour))
	writeAt(t, filepath.Join(pkg, "broken.go"), buildNoon.Add(-time.Hour)) // "x", no package line
	writeAt(t, filepath.Join(pkg, "a.txt"), buildNoon.Add(time.Hour))

	s, err := BuildStale(root, build)
	if err != nil || s == nil || s.What != filepath.Join("internal", "pkg", "a.txt") {
		t.Fatalf("got %+v, %v", s, err)
	}
}

// The real tree: every embedded file of the viewer, the CLI and the checks is
// found, or a go:embed line this reader can't follow would hide stale code.
func TestEmbeddedFilesOfThisRepo(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ dir, file string }{
		{"internal/checks", "lib.sh"},
		{"internal/viewer", "templates/detail.html"},
		{"internal/viewer", "static/workshop.css"},
		{"cmd/issue-cli", "CHANGELOG.md"},
		{"cmd/issue-cli/templates", "workflow/development.yaml"},
		{"cmd/playtest", "assets/checks.js"},
	} {
		dir := filepath.Join(root, filepath.FromSlash(tt.dir))
		want := filepath.Join(dir, filepath.FromSlash(tt.file))
		if !slices.Contains(embeddedFiles(dir), want) {
			t.Errorf("%s: %s not found", tt.dir, tt.file)
		}
	}
}

func TestBuildStaleCommit(t *testing.T) {
	root := t.TempDir()
	gitRun(t, root, "init", "-q")
	gitRun(t, root, "config", "maintenance.auto", "false")
	gitRun(t, root, "config", "gc.auto", "0")
	gitRun(t, root, "config", "user.email", "t@example.com")
	gitRun(t, root, "config", "user.name", "t")
	commit := func(at time.Time, msg string) {
		t.Helper()
		t.Setenv("GIT_COMMITTER_DATE", strconv.FormatInt(at.Unix(), 10)+" +0000")
		gitRun(t, root, "add", "-A")
		gitRun(t, root, "commit", "-q", "-m", msg)
	}
	build := filepath.Join(root, "issue-cli")
	writeAt(t, filepath.Join(root, "cmd", "deleted.go"), buildNoon.Add(-2*time.Hour))
	commit(buildNoon.Add(-2*time.Hour), "first")
	writeAt(t, build, buildNoon)
	if err := os.WriteFile(filepath.Join(root, ".git", "info", "exclude"), []byte("issue-cli\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if s, err := BuildStale(root, build); err != nil || s != nil {
		t.Fatalf("commit before the build: %+v, %v", s, err)
	}

	// A later commit elsewhere is no change to the code.
	writeAt(t, filepath.Join(root, "docs", "a.md"), buildNoon.Add(-time.Minute))
	commit(buildNoon.Add(time.Hour), "docs")
	if s, err := BuildStale(root, build); err != nil || s != nil {
		t.Fatalf("docs commit: %+v, %v", s, err)
	}

	// A commit that deletes a file leaves no mtime to find: only the commit
	// says the code moved.
	if err := os.Remove(filepath.Join(root, "cmd", "deleted.go")); err != nil {
		t.Fatal(err)
	}
	writeAt(t, filepath.Join(root, "cmd", "kept.go"), buildNoon.Add(-time.Minute))
	commit(buildNoon.Add(2*time.Hour), "delete")
	s, err := BuildStale(root, build)
	if err != nil || s == nil {
		t.Fatalf("commit after the build: %+v, %v", s, err)
	}
	if !strings.HasPrefix(s.What, "commit ") || !s.Changed.Equal(buildNoon.Add(2*time.Hour)) {
		t.Fatalf("got %+v", s)
	}
}

func TestBuildStaleNoCodeFolders(t *testing.T) {
	root := t.TempDir()
	build := filepath.Join(root, "issue-cli")
	writeAt(t, build, buildNoon)
	if s, err := BuildStale(root, build); err != nil || s != nil {
		t.Fatalf("%+v, %v", s, err)
	}
}
