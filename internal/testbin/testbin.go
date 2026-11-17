// Package testbin builds a command of this module once per test binary and
// hands every test that asks for it the same file.
//
// A test that runs issue-cli as a real process (its own git cache, its own
// environment, the worker guard's view of a fresh shell) needs a built
// binary, not the test binary. Building it per test paid go build's link
// step each time, and on a loaded machine one such build took most of a
// 55.8 s test (factory/tests-build-issue-cli-once-per-test-binary-not-once-per-test-one-takes-55-8-s).
// Path builds each package the first time a test asks, and later callers
// wait for that build and get its path. The file is also one macOS checks
// on its first exec only once.
//
//	cli := testbin.Path(t, "./cmd/issue-cli")
//
// The binaries live in one folder per test binary, which Cleanup removes;
// tmuxguard's cleanup calls it, so every TestMain removes it after m.Run.
// TestEveryGoBuildInATestGoesThroughTestbin holds every _test.go in the
// module to Path.
package testbin

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// build is one package's build: done once, read by every caller.
type build struct {
	once sync.Once
	bin  string
	err  error
}

var (
	mu     sync.Mutex
	dir    string
	builds = map[string]*build{}
	count  int // go builds run, for Builds
)

// startDir and startEnv are the working directory and go's own settings
// when the binary started: a test that later sets HOME (where go keeps its
// build cache by default), PATH or the working directory still builds as
// go test did, from a warm cache.
var startDir, startEnv = snapshot()

// startTemp is os.TempDir at start: the build folder outlives any test
// that points TMPDIR at its own t.TempDir.
var startTemp = os.TempDir()

var goVars = []string{"HOME", "PATH", "GOCACHE", "GOPATH", "GOMODCACHE", "GOFLAGS", "GOTOOLCHAIN", "XDG_CACHE_HOME", "TMPDIR"}

func snapshot() (string, []string) {
	wd, _ := os.Getwd()
	var env []string
	for _, k := range goVars {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return wd, env
}

// Path is the binary built from pkg, a package path relative to the module
// root ("./cmd/issue-cli"). The first call builds it with go build; every
// later call, from any test or goroutine, returns the same file. A failed
// build fails each test that asks, with go build's output. Callers must
// not write to or remove the file: link or copy it (Link) to put it under
// a name of their own.
func Path(t testing.TB, pkg string) string {
	t.Helper()
	bin, err := get(pkg)
	if err != nil {
		t.Fatalf("testbin: %v", err)
	}
	return bin
}

// Link puts the binary built from pkg at dst (whose folder must exist): a
// hard link, so the file macOS already checked, or a copy where a link
// can't be made.
func Link(t testing.TB, pkg, dst string) {
	t.Helper()
	src := Path(t, pkg)
	if os.Link(src, dst) == nil {
		return
	}
	b, err := os.ReadFile(src)
	if err == nil {
		err = os.WriteFile(dst, b, 0o755)
	}
	if err != nil {
		t.Fatalf("testbin: link %s to %s: %v", pkg, dst, err)
	}
}

// Builds is how many go builds this test binary has run.
func Builds() int {
	mu.Lock()
	defer mu.Unlock()
	return count
}

// Cleanup removes the folder the binaries were built into. Call it after
// m.Run (tmuxguard's cleanup does); a Path after it builds again.
func Cleanup() {
	mu.Lock()
	defer mu.Unlock()
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
	dir = ""
	builds = map[string]*build{}
}

func get(pkg string) (string, error) {
	key := path.Clean(filepath.ToSlash(pkg))
	mu.Lock()
	b := builds[key]
	if b == nil {
		b = &build{}
		builds[key] = b
	}
	mu.Unlock()
	b.once.Do(func() { b.bin, b.err = run(key) })
	return b.bin, b.err
}

func run(pkg string) (string, error) {
	root, err := moduleRoot()
	if err != nil {
		return "", err
	}
	mu.Lock()
	if dir == "" {
		dir, err = os.MkdirTemp(startTemp, "testbin-")
	}
	out := dir
	count++
	mu.Unlock()
	if err != nil {
		return "", err
	}
	name := path.Base(pkg)
	if name == "." || name == "/" {
		name = filepath.Base(root)
	}
	sub, err := os.MkdirTemp(out, name+"-")
	if err != nil {
		return "", err
	}
	bin := filepath.Join(sub, name)
	if !strings.HasPrefix(pkg, ".") {
		pkg = "./" + pkg
	}
	cmd := exec.Command("go", "build", "-o", bin, pkg)
	cmd.Dir = root
	cmd.Env = buildEnv()
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build %s: %v\n%s", pkg, err, b)
	}
	return bin, nil
}

// buildEnv is this process's environment with go's own settings as they
// were at start: a variable a test set since is put back, one it added is
// dropped.
func buildEnv() []string {
	start := map[string]bool{}
	for _, k := range goVars {
		start[k] = true
	}
	var env []string
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); !start[k] {
			env = append(env, kv)
		}
	}
	return append(env, startEnv...)
}

// moduleRoot is the folder holding go.mod, found from the working
// directory the binary started in up: go test starts it in its package's
// folder.
func moduleRoot() (string, error) {
	wd := startDir
	if wd == "" {
		return "", fmt.Errorf("no working directory at start")
	}
	for d := wd; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d, nil
		}
		if filepath.Dir(d) == d {
			return "", fmt.Errorf("no go.mod above %s", wd)
		}
	}
}
