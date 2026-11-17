// Command test-shards is go test with some packages split across several
// processes. make test runs it:
//
//	go run ./tools/test-shards -shard ./cmd/issue-cli=3 -- ./... -count=1 [-json]
//
// Everything after -- is go test's. The packages it names, minus the sharded
// ones, run in one go test as before, its output streamed. Each sharded
// package is listed (go test -list), split by recorded time
// (internal/testshard) and run as N go test -run processes beside it; a
// shard's output is printed whole when it ends. Every listed test is in
// exactly one shard, or nothing runs. A -coverprofile=<file> is written per
// process and merged into <file>.
//
// Recorded times come from -times: a go test -json file, or "auto" (the
// default): the newest local CI unit.json (.ci/results/*/unit.json beside the
// primary checkout) that has the package. Without one each test counts the
// same. With -run, -list or -bench in go test's flags nothing is sharded.
//
// -cache-list <file> (scripts/test-cache.txt, internal/testshard.CacheList)
// says which packages may come from go test's cache: when go test's flags
// set no -count, the cached ones run without it, the fresh ones (and any the
// list doesn't name, said on stderr) with -count=1 in a process of their
// own, and each fresh-test line's test in one more -count=1 process. The
// cached processes get LIVE_BOARD_DEFER (internal/liveboard) so a test that
// reads the live board skips there.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kacperwerema/dark-factory/internal/liveboard"
	"github.com/kacperwerema/dark-factory/internal/testshard"
)

type shardFlags []string

func (s *shardFlags) String() string     { return strings.Join(*s, ",") }
func (s *shardFlags) Set(v string) error { *s = append(*s, v); return nil }

func main() {
	var shards shardFlags
	fs := flag.NewFlagSet("test-shards", flag.ExitOnError)
	fs.Var(&shards, "shard", "<package>=<n>: run that package's tests in n processes (repeatable)")
	timesFlag := fs.String("times", "auto", "go test -json file with recorded test times, or auto")
	plansFlag := fs.String("plans", "auto", "folder keeping each sharded package's last plan, auto (the user cache folder, per checkout) or none")
	cacheFlag := fs.String("cache-list", "", "file naming each test package cached or fresh (scripts/test-cache.txt); used when go test's flags set no -count")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: test-shards [-shard <pkg>=<n>]... [-times <file>|auto] [-plans <dir>|auto|none] [-cache-list <file>] -- <go test args>")
		fs.PrintDefaults()
	}
	fs.Parse(os.Args[1:])
	code, err := run(shards, *timesFlag, *plansFlag, *cacheFlag, fs.Args(), os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "test-shards:", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// valueFlags are go test flags whose value may be the next argument.
var valueFlags = map[string]bool{
	"run": true, "skip": true, "list": true, "bench": true, "count": true, "timeout": true,
	"p": true, "parallel": true, "cpu": true, "coverprofile": true, "coverpkg": true,
	"covermode": true, "tags": true, "o": true, "exec": true, "vet": true, "ldflags": true,
	"gcflags": true, "benchtime": true, "shuffle": true, "outputdir": true, "C": true,
	"cpuprofile": true, "memprofile": true, "blockprofile": true, "mutexprofile": true, "trace": true,
}

// goTestArgs is go test's command line split into flags (each with its
// value joined by =) and package patterns.
type goTestArgs struct {
	flags    []string
	patterns []string
}

func splitArgs(args []string) goTestArgs {
	var a goTestArgs
	for i := 0; i < len(args); i++ {
		s := args[i]
		if !strings.HasPrefix(s, "-") {
			a.patterns = append(a.patterns, s)
			continue
		}
		name := strings.TrimLeft(s, "-")
		if !strings.Contains(name, "=") && valueFlags[name] && i+1 < len(args) {
			s = s + "=" + args[i+1]
			i++
		}
		a.flags = append(a.flags, s)
	}
	return a
}

// flag returns a flag's value and whether it is set.
func (a goTestArgs) flag(name string) (string, bool) {
	for _, f := range a.flags {
		n, v, _ := strings.Cut(strings.TrimLeft(f, "-"), "=")
		if n == name {
			return v, true
		}
	}
	return "", false
}

// without returns the flags minus the named ones.
func (a goTestArgs) without(names ...string) []string {
	var out []string
	for _, f := range a.flags {
		n, _, _ := strings.Cut(strings.TrimLeft(f, "-"), "=")
		drop := false
		for _, x := range names {
			drop = drop || n == x
		}
		if !drop {
			out = append(out, f)
		}
	}
	return out
}

type shardSpec struct {
	pkg string // import path
	n   int
}

func run(shards []string, timesArg, plansArg, cacheListPath string, args []string, stdout, stderr io.Writer) (int, error) {
	a := splitArgs(args)
	_, hasRun := a.flag("run")
	_, hasList := a.flag("list")
	_, hasBench := a.flag("bench")
	_, hasCount := a.flag("count")
	useCache := cacheListPath != "" && !hasCount
	if (len(shards) == 0 && !useCache) || hasRun || hasList || hasBench {
		return goTest(args, stdout, stderr)
	}
	patterns := a.patterns
	if len(patterns) == 0 {
		patterns = []string{"."}
	}
	// One go list per pattern: go list sorts what it prints, and the
	// order the patterns name packages in is the order go test starts them.
	var pkgs []string
	seenPkg := map[string]bool{}
	for _, pat := range patterns {
		paths, err := goList([]string{pat})
		if err != nil {
			return 1, err
		}
		for _, p := range paths {
			if !seenPkg[p] {
				seenPkg[p] = true
				pkgs = append(pkgs, p)
			}
		}
	}
	inRun := map[string]bool{}
	for _, p := range pkgs {
		inRun[p] = true
	}
	var specs []shardSpec
	sharded := map[string]bool{}
	for _, s := range shards {
		pat, nStr, ok := strings.Cut(s, "=")
		n, err := strconv.Atoi(nStr)
		if !ok || err != nil || n < 1 {
			return 2, fmt.Errorf("-shard %q: want <package>=<n>", s)
		}
		paths, err := goList([]string{pat})
		if err != nil {
			return 1, err
		}
		for _, p := range paths {
			if inRun[p] && !sharded[p] { // shard only what the run names
				sharded[p] = true
				specs = append(specs, shardSpec{p, n})
			}
		}
	}
	// fresh is every package that always runs: all of them without a
	// cache list (go test's own -count, if any, applies).
	fresh := map[string]bool{}
	var freshTests map[string][]string
	var deferEnv, freshEnv []string
	if useCache {
		cl, err := loadCacheList(cacheListPath, pkgs, stderr)
		if err != nil {
			return 1, err
		}
		fresh, freshTests = cl.fresh, cl.tests
		deferEnv = []string{liveboard.DeferEnv + "=" + cl.abs}
		freshEnv = []string{liveboard.DeferEnv + "="} // never one inherited from a run around this one
	} else {
		for _, p := range pkgs {
			fresh[p] = true
		}
	}
	var rest, restFresh []string
	for _, p := range pkgs {
		switch {
		case sharded[p]:
		case useCache && fresh[p]:
			restFresh = append(restFresh, p)
		default:
			rest = append(rest, p)
		}
	}
	profile, cover := a.flag("coverprofile")
	base := a.without("coverprofile")
	out := &lockedOut{stdout: stdout, stderr: stderr}

	type proc struct {
		name    string
		args    []string
		env     []string
		profile string
		buffer  bool
	}
	// Each process's profile goes to a temp folder, not beside <file>: a
	// test that lists the repo would see them come and go, and go test's
	// cache would rerun it next time.
	profDir := ""
	if cover {
		d, err := os.MkdirTemp("", "test-shards-cover-")
		if err != nil {
			return 1, err
		}
		defer os.RemoveAll(d)
		profDir = d
	}
	var procs []proc
	add := func(p proc) {
		if cover {
			p.profile = filepath.Join(profDir, fmt.Sprintf("p%d.out", len(procs)))
			p.args = append([]string{"-coverprofile=" + p.profile}, p.args...)
		}
		procs = append(procs, p)
	}
	countOne := func(flags []string) []string {
		if !useCache {
			return flags
		}
		return append([]string{"-count=1"}, flags...)
	}
	if len(rest) > 0 {
		p := proc{name: "go test", args: append(append([]string{}, base...), rest...)}
		if useCache {
			p.env = deferEnv
		}
		add(p)
	}
	if len(restFresh) > 0 {
		add(proc{name: "go test (fresh)", env: freshEnv, args: append(countOne(base), restFresh...)})
	}
	if len(freshTests) > 0 {
		var names, tpkgs []string
		for _, p := range pkgs {
			if ts := freshTests[p]; len(ts) > 0 {
				names = append(names, ts...)
				tpkgs = append(tpkgs, p)
			}
		}
		add(proc{name: "go test (fresh tests)", env: freshEnv, buffer: true,
			args: append(append(countOne(base), "-run="+testshard.FreshTestRegex(names)), tpkgs...)})
	}
	listFlags := a.without("coverprofile", "json", "count", "v", "failfast", "shuffle")
	// Each sharded package's test binary is built to list its tests: all at
	// once, so the run waits on the slowest build, not their sum.
	listed := make([][]string, len(specs))
	listErr := make([]error, len(specs))
	var lwg sync.WaitGroup
	for i, s := range specs {
		lwg.Add(1)
		go func() {
			defer lwg.Done()
			listed[i], listErr[i] = goListTests(listFlags, s.pkg)
		}()
	}
	lwg.Wait()
	for _, err := range listErr {
		if err != nil {
			return 1, err
		}
	}
	for si, s := range specs {
		listed := listed[si]
		times, from := loadTimes(timesArg, s.pkg)
		groups, kept := keptPlan(plansArg, s.pkg, listed, s.n)
		if kept != "" {
			from = kept
		} else {
			groups = testshard.Plan(listed, times, s.n)
			savePlan(plansArg, s.pkg, s.n, groups, stderr)
		}
		var regs []string
		for _, g := range groups {
			regs = append(regs, testshard.Regex(g))
		}
		if bad := testshard.Check(listed, regs); len(bad) > 0 {
			return 1, fmt.Errorf("%s: shards don't run every test once:\n  %s", s.pkg, strings.Join(bad, "\n  "))
		}
		switch {
		case kept != "":
			from = "plan kept from " + kept + ": same tests"
		case from == "":
			from = "no recorded times: each test counts the same"
		default:
			from = "times from " + from
		}
		fmt.Fprintf(stderr, "test-shards: %s: %d tests in %d shards (%s)\n", s.pkg, len(listed), len(groups), from)
		for i, re := range regs {
			p := proc{name: fmt.Sprintf("%s shard %d/%d", s.pkg, i+1, len(regs)), buffer: true}
			if useCache && fresh[s.pkg] {
				p.args, p.env = append(countOne(base), "-run="+re, s.pkg), freshEnv
			} else {
				p.args = append(append([]string{}, base...), "-run="+re, s.pkg)
				if useCache {
					p.env = deferEnv
				}
			}
			add(p)
		}
	}

	codes := make([]int, len(procs))
	var wg sync.WaitGroup
	for i, p := range procs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = out.run(append([]string{"test"}, p.args...), p.env, p.buffer)
		}()
	}
	wg.Wait()
	code := 0
	for i, c := range codes {
		if c != 0 {
			if procs[i].buffer || procs[i].name != "go test" {
				fmt.Fprintf(stderr, "test-shards: %s exited %d\n", procs[i].name, c)
			}
			code = max(code, c)
		}
	}
	if cover {
		var parts [][]byte
		for _, p := range procs {
			data, err := os.ReadFile(p.profile)
			if err == nil {
				parts = append(parts, data)
			}
			os.Remove(p.profile)
		}
		merged, err := testshard.MergeProfiles(parts)
		if err != nil {
			return max(code, 1), err
		}
		if err := os.WriteFile(profile, merged, 0o644); err != nil {
			return max(code, 1), err
		}
	}
	return code, nil
}

// lockedOut serialises what the processes print: the streamed one line by
// line, a buffered one whole when it ends, so lines never tear (a go test
// -json reader sees whole events).
type lockedOut struct {
	mu             sync.Mutex
	stdout, stderr io.Writer
}

func (o *lockedOut) run(args, env []string, buffer bool) int {
	cmd := exec.Command("go", args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	// Output still open after go test exits (a process left behind holding
	// it) is cut off rather than waited for.
	cmd.WaitDelay = 10 * time.Second
	var outBuf, errBuf bytes.Buffer
	var so, se *lineWriter
	if buffer {
		cmd.Stdout, cmd.Stderr = &outBuf, &errBuf
	} else {
		so, se = &lineWriter{o: o, w: o.stdout}, &lineWriter{o: o, w: o.stderr}
		cmd.Stdout, cmd.Stderr = so, se
	}
	err := cmd.Run()
	if buffer {
		o.mu.Lock()
		o.stdout.Write(outBuf.Bytes())
		o.stderr.Write(errBuf.Bytes())
		o.mu.Unlock()
	} else {
		so.flush()
		se.flush()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		o.write(o.stderr, []byte(err.Error()+"\n"))
		return 1
	}
	return 0
}

// lineWriter passes whole lines on under the lock and keeps a partial one
// until its newline (or flush).
type lineWriter struct {
	o       *lockedOut
	w       io.Writer
	partial []byte
}

func (l *lineWriter) Write(b []byte) (int, error) {
	l.partial = append(l.partial, b...)
	if i := bytes.LastIndexByte(l.partial, '\n'); i >= 0 {
		l.o.write(l.w, l.partial[:i+1])
		l.partial = append([]byte(nil), l.partial[i+1:]...)
	}
	return len(b), nil
}

func (l *lineWriter) flush() {
	if len(l.partial) > 0 {
		l.o.write(l.w, l.partial)
		l.partial = nil
	}
}

func (o *lockedOut) write(w io.Writer, b []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	w.Write(b)
}

func goTest(args []string, stdout, stderr io.Writer) (int, error) {
	cmd := exec.Command("go", append([]string{"test"}, args...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, stdout, stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	return 0, err
}

// goList expands package patterns to import paths, each once.
func goList(patterns []string) ([]string, error) {
	var stderr bytes.Buffer
	cmd := exec.Command("go", append([]string{"list"}, patterns...)...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list %s: %v\n%s", strings.Join(patterns, " "), err, stderr.String())
	}
	var pkgs []string
	seen := map[string]bool{}
	for _, l := range strings.Fields(string(out)) {
		if !seen[l] {
			seen[l] = true
			pkgs = append(pkgs, l)
		}
	}
	return pkgs, nil
}

// goListTests lists a package's tests with the run's build flags, so the
// build it makes is the one the shards then find in the cache.
func goListTests(flags []string, pkg string) ([]string, error) {
	var stderr bytes.Buffer
	args := append(append([]string{"test"}, flags...), "-list=.", pkg)
	cmd := exec.Command("go", args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go %s: %v\n%s%s", strings.Join(args, " "), err, out, stderr.String())
	}
	tests := testshard.ListedTests(out)
	if len(tests) == 0 {
		return nil, fmt.Errorf("go test -list found no tests in %s", pkg)
	}
	return tests, nil
}

// loadTimes reads recorded times for pkg and says where from ("" for none).
func loadTimes(arg, pkg string) (map[string]float64, string) {
	if arg == "" || arg == "none" {
		return nil, ""
	}
	files := []string{arg}
	if arg == "auto" {
		files = ciResults()
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if t := testshard.Times(data, pkg); len(t) > 0 {
			return t, f
		}
	}
	return nil, ""
}

// ciResults is local CI's unit.json files, newest first: .ci/results beside
// the git common dir, so a worktree finds the primary checkout's.
func ciResults() []string {
	out, err := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return nil
	}
	dir := filepath.Join(filepath.Dir(strings.TrimSpace(string(out))), ".ci", "results")
	files, _ := filepath.Glob(filepath.Join(dir, "*", "unit.json"))
	type f struct {
		path string
		mod  int64
	}
	var fs []f
	for _, p := range files {
		if st, err := os.Stat(p); err == nil {
			fs = append(fs, f{p, st.ModTime().UnixNano()})
		}
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].mod > fs[j].mod })
	var paths []string
	for i, x := range fs {
		if i == 5 {
			break
		}
		paths = append(paths, x.path)
	}
	return paths
}

// cacheList is the cache list resolved to import paths for one run.
type cacheList struct {
	abs   string              // the list's absolute path
	fresh map[string]bool     // always runs: a fresh line, or not listed
	tests map[string][]string // cached package → its fresh-test tests
}

// loadCacheList reads the list and maps its folders onto the run's
// packages. A package of the run the list doesn't name runs fresh, said on
// stderr (TestEveryTestPackageIsInTheCacheList fails on it).
func loadCacheList(path string, pkgs []string, stderr io.Writer) (*cacheList, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("-cache-list: %v", err)
	}
	list, err := testshard.ParseCacheList(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	modOut, err := exec.Command("go", "list", "-m").Output()
	if err != nil {
		return nil, fmt.Errorf("go list -m: %v", err)
	}
	mod := strings.TrimSpace(string(modOut))
	importPath := func(dir string) string {
		if dir == "." {
			return mod
		}
		return mod + "/" + strings.TrimPrefix(dir, "./")
	}
	cached := map[string]bool{}
	for d := range list.Cached {
		cached[importPath(d)] = true
	}
	tests := map[string][]string{}
	for d, ts := range list.FreshTests {
		for name := range ts {
			tests[importPath(d)] = append(tests[importPath(d)], name)
		}
	}
	// A package with no tests has nothing to cache wrongly: it needs no line.
	hasTests := map[string]bool{}
	out, err := exec.Command("go", append([]string{"list", "-f", "{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}"}, pkgs...)...).Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %v", err)
	}
	for _, p := range strings.Fields(string(out)) {
		hasTests[p] = true
	}
	c := &cacheList{abs: abs, fresh: map[string]bool{}, tests: tests}
	var unlisted []string
	nCached := 0
	for _, p := range pkgs {
		dir := "." + strings.TrimPrefix(p, mod) // "./internal/x", or "." for the module root
		_, isFresh := list.Fresh[dir]
		switch {
		case cached[p] || (!isFresh && !hasTests[p]):
			nCached++
		case isFresh:
			c.fresh[p] = true
		default:
			c.fresh[p] = true
			unlisted = append(unlisted, p)
		}
	}
	fmt.Fprintf(stderr, "test-shards: %d packages may come from go test's cache, %d always run (%s)\n", nCached, len(c.fresh), path)
	if len(unlisted) > 0 {
		fmt.Fprintf(stderr, "test-shards: not in %s, so they run fresh: %s\n", path, strings.Join(unlisted, " "))
	}
	return c, nil
}

// A shard's -run pattern is part of go test's cache key, and recorded times
// change with every local CI run, so a plan made fresh each time would move
// tests between shards and rerun cached ones. The last plan for a package is
// kept (-plans, per checkout) and used again while the package lists the
// same tests at the same count; a test added or removed makes a new plan.
type savedPlan struct {
	N      int        `json:"n"`
	Groups [][]string `json:"groups"`
}

// plansDir is the folder -plans names: auto is the user cache folder, one
// per checkout (by its path), outside the checkout so no test that walks
// the repo sees it change.
func plansDir(arg string) string {
	switch arg {
	case "", "none":
		return ""
	case "auto":
		cache, err := os.UserCacheDir()
		if err != nil {
			return ""
		}
		wd, err := os.Getwd()
		if err != nil {
			return ""
		}
		return filepath.Join(cache, "dark-factory", "test-shards", fmt.Sprintf("%x", crc32.ChecksumIEEE([]byte(wd))))
	}
	return arg
}

func planFile(dir, pkg string) string {
	return filepath.Join(dir, strings.NewReplacer("/", "_", ".", "_").Replace(pkg)+".json")
}

// keptPlan returns the kept plan for pkg and its file when it holds exactly
// the listed tests in n groups, and none otherwise.
func keptPlan(arg, pkg string, listed []string, n int) ([][]string, string) {
	dir := plansDir(arg)
	if dir == "" {
		return nil, ""
	}
	f := planFile(dir, pkg)
	data, err := os.ReadFile(f)
	if err != nil {
		return nil, ""
	}
	var p savedPlan
	if json.Unmarshal(data, &p) != nil || p.N != n || len(p.Groups) != min(n, len(listed)) {
		return nil, ""
	}
	want := map[string]bool{}
	for _, t := range listed {
		want[t] = true
	}
	count := 0
	for _, g := range p.Groups {
		if len(g) == 0 {
			return nil, ""
		}
		for _, t := range g {
			if !want[t] {
				return nil, ""
			}
			count++
		}
	}
	if count != len(listed) {
		return nil, ""
	}
	return p.Groups, f
}

// savePlan keeps a new plan for the next run; a plan it can't write is said
// and the run goes on.
func savePlan(arg, pkg string, n int, groups [][]string, stderr io.Writer) {
	dir := plansDir(arg)
	if dir == "" {
		return
	}
	data, _ := json.Marshal(savedPlan{N: n, Groups: groups})
	f := planFile(dir, pkg)
	tmp := f + fmt.Sprintf(".%d.tmp", os.Getpid())
	err := os.MkdirAll(dir, 0o755)
	if err == nil {
		err = os.WriteFile(tmp, data, 0o644)
	}
	if err == nil {
		err = os.Rename(tmp, f)
	}
	if err != nil {
		os.Remove(tmp)
		fmt.Fprintf(stderr, "test-shards: can't keep the plan for %s: %v\n", pkg, err)
	}
}
