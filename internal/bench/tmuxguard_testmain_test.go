package bench

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
)

// pkgDir is this test binary's own folder: the shared fixtures are built
// under it once, and $ISSUE_VIEWER_BENCH_DIR points into it, so a test
// that doesn't set its own bench folder never reaches the owner's.
var pkgDir string

// TestMain keeps every tmux these tests start off the developer's server
// (internal/tmuxguard), and gives the package its bench folder: one for
// every test, set before any runs, so the tests that run a benchmark can
// run in parallel (t.Setenv can't). It is set inside Run: tmuxguard's
// isolation unsets every ISSUE_VIEWER_* variable first.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "bench-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	pkgDir = dir
	tmuxguard.Main(cleanRun{m, dir})
}

// cleanRun points $ISSUE_VIEWER_BENCH_DIR at the package's folder, runs
// the tests, then removes the folder.
type cleanRun struct {
	m   *testing.M
	dir string
}

func (r cleanRun) Run() int {
	defer os.RemoveAll(r.dir)
	if err := os.Setenv("ISSUE_VIEWER_BENCH_DIR", pkgBench()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return r.m.Run()
}

// inBenchDir fails the test unless the bench folder is a test's own: the
// package's or one the test set. Never the owner's.
func inBenchDir(t *testing.T) {
	t.Helper()
	if strings.TrimSpace(os.Getenv("ISSUE_VIEWER_BENCH_DIR")) == "" {
		t.Fatalf("no ISSUE_VIEWER_BENCH_DIR: this test would write into the owner's bench folder (%s)", Dir())
	}
}

// shared is a fixture built once per test binary under pkgDir: the first
// test that needs it builds it, every other one waits for it. A build that
// failed fails each test that asks for it.
type shared[T any] struct {
	once sync.Once
	v    T
	err  error
}

func (s *shared[T]) get(t *testing.T, build func(dir string) (T, error)) T {
	t.Helper()
	inBenchDir(t)
	s.once.Do(func() {
		s.err = fmt.Errorf("the build stopped")
		dir, err := os.MkdirTemp(pkgDir, "shared-")
		if err != nil {
			s.err = err
			return
		}
		s.v, s.err = build(dir)
	})
	if s.err != nil {
		t.Fatalf("shared fixture: %v", s.err)
	}
	return s.v
}

// copyTree copies the folder src to dst (which must not exist): a test's
// own copy of a shared fixture, to write in as it likes. Git's objects,
// refs and index come along, so every commit has the same SHA.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatalf("copy %s: %v", src, err)
	}
}

// heavy caps how many tests run a benchmark (a sandbox clone and a go test
// each) at once in this binary: go test's own -parallel is the machine's
// CPU count, and make validate runs this package's shards beside the rest.
var heavy = make(chan struct{}, 4)

// parallel marks a test that builds sandboxes or runs go test as parallel,
// within heavy's cap. A test that sets the environment or times itself
// stays serial: go test runs the serial ones first, alone.
func parallel(t *testing.T) {
	t.Helper()
	t.Parallel()
	heavy <- struct{}{}
	t.Cleanup(func() { <-heavy })
}

// gitDateEnv is git's environment with the author and committer date set:
// what t.Setenv("GIT_COMMITTER_DATE") did, for a fixture built outside a
// test.
func gitDateEnv(date string) []string {
	return append(gitEnv(os.Environ()), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
}

var notName = regexp.MustCompile(`[^a-z0-9._-]+`)

// benchName is a benchmark name of the test's own: run IDs go by the
// second, so two tests running at once under one name would share a run.
func benchName(t *testing.T) string {
	return strings.Trim(notName.ReplaceAllString(strings.ToLower(t.Name()), "-"), "-.")
}

// pkgBench is the package's bench folder, where a shared fixture's task
// files go: a serial test may have moved $ISSUE_VIEWER_BENCH_DIR while it
// builds one.
func pkgBench() string { return filepath.Join(pkgDir, "bench") }

// inPkgBench is path (under Dir()) moved under pkgBench.
func inPkgBench(path string) string {
	rel, err := filepath.Rel(Dir(), path)
	if err != nil {
		return path
	}
	return filepath.Join(pkgBench(), rel)
}

// ownTaskFiles writes a shared fixture's task files again when the test
// has its own bench folder (a serial test that set one).
func ownTaskFiles(t *testing.T, files map[string]string) {
	t.Helper()
	if Dir() == pkgBench() {
		return
	}
	for path, data := range files {
		write(t, path, data)
	}
}

// fxb builds a shared fixture outside any test: the first error sticks
// and every later step is skipped.
type fxb struct {
	err error
	env []string // git's environment; nil: gitEnv's
}

func (b *fxb) git(dir string, args ...string) string {
	if b.err != nil {
		return ""
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = b.env
	if cmd.Env == nil {
		cmd.Env = gitEnv(os.Environ())
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		b.err = fmt.Errorf("git %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

func (b *fxb) write(path, data string) {
	if b.err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		b.err = err
		return
	}
	b.err = os.WriteFile(path, []byte(data), 0o644)
}
