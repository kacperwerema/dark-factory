package viewer

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/timing"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// bigSystemsProject is the shape that made /systems slow: 800 issues over
// seven systems with bodies as long as the real board's (8 KB of prose,
// which filePattern used to walk word by word) naming a thousand paths, a
// third of them gone, in a git repo of 3,000 files, five systems with
// paths:, and decisions for the company's connections to read.
func bigSystemsProject(t *testing.T) tracker.Project {
	t.Helper()
	proj := bigProject(t, 800)
	root := proj.WorkDir
	for d := 0; d < 100; d++ {
		dir := filepath.Join(root, "src", fmt.Sprintf("pkg%02d", d))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for f := 0; f < 30; f++ {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file%02d.go", f)), []byte("package x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for i := 0; i < 800; i++ {
		var body strings.Builder
		for k := 0; k < 12; k++ {
			n := i*7 + k*131
			fmt.Fprintf(&body, "See `src/pkg%02d/file%02d.go` and `src/pkg%02d/gone%03d.go`.\n", n%100, n%30, n%100, n%400)
		}
		for k := 0; k < 100; k++ {
			body.WriteString("The worker reads the brief, measures the page on the unchanged code and records what it found before it moves on to the next step.\n")
		}
		fmt.Fprintf(&body, "\n## Decisions\n\n- **D-1** · 2026-11-01 · Keep issue-%04d as it is.\n- **D-2** · 2026-11-02 · Name issue-%04d#D-1 here.\n", i, (i+1)%800)
		path := filepath.Join(proj.IssueDir, "Core", fmt.Sprintf("issue-%04d.md", i))
		front := fmt.Sprintf("---\ntitle: \"Issue %d\"\nstatus: \"%s\"\nsystem: \"S%d\"\n---\n\n", i, []string{"done", "done", "done", "in progress"}[i%4], i%7)
		if err := os.WriteFile(path, []byte(front+body.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var wf strings.Builder
	wf.WriteString("statuses:\n  - name: \"in progress\"\n  - name: \"done\"\nworkbench:\n  systems:\n")
	for i := 0; i < 5; i++ {
		fmt.Fprintf(&wf, "    S%d:\n      paths: [\"src/pkg%d*/\", \"docs/%d/\"]\n", i, i, i)
	}
	proj.WorkflowFile = filepath.Join(root, "workflow.yaml")
	if err := os.WriteFile(proj.WorkflowFile, []byte(wf.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"config", "maintenance.auto", "false"}, {"config", "gc.auto", "0"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return proj
}

// B1: on a big project the page renders in under 300 ms, held the
// way TestStatsPageFastOnABigProject holds its page: under load, to
// statsVsIssues times the best /issues.json timed in turns with it
// (timing.Check: one sample in a burst fails nothing). 300 ms and not the
// 500 ms line, because this fixture has none of the real board's tmux
// sessions, jobs or shelf.
func TestSystemsPageFastOnABigProject(t *testing.T) {
	if testing.Short() {
		t.Skip("builds an 800-issue project")
	}
	withMockTmuxSessions(t, nil)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	proj := bigSystemsProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	get := func(path string) time.Duration {
		start := time.Now()
		resp, err := http.Get(ts.URL + "/p/big/" + path)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d", path, resp.StatusCode)
		}
		return time.Since(start)
	}
	cold := get("systems")
	r := timing.Check{Limit: 300 * time.Millisecond, Ratio: statsVsIssues}.Run(
		func() time.Duration { return get("systems") }, func() time.Duration { return get("issues.json") })
	if r.Slow {
		t.Errorf("/systems warm: %s (baseline: /issues.json)", r)
	}
	t.Logf("800 issues: cold %v, warm %s", cold, r)
}

// statExists is fileExists's rule, one stat per path.
func statExists(root, p string) bool {
	if root == "" || strings.Contains(p, "..") || strings.HasPrefix(p, "/") {
		return false
	}
	fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(p)))
	return err == nil && fi.Mode().IsRegular()
}

// B2: the CODE row and the owned files on a repo with what a
// shortcut could get wrong: ignored and untracked files, a tracked file
// since deleted, links to a file, a folder and nothing, a folder named like
// a file, a file used as a folder, and paths that aren't clean. The want
// lists are the row today's stat-per-path code gives: this test passed
// unchanged on the commit before filePaths.
func TestSystemsCodeRowUnchanged(t *testing.T) {
	root := t.TempDir()
	write := func(rel string) {
		t.Helper()
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	for _, rel := range []string{"a/one.go", "a/two.go", "b/deep/three.go", "root.go", "gone.go", "a/dir.go/inner.go"} {
		write(rel)
	}
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored/\n"), 0o644)
	git("init", "-q", "-b", "main")
	git("config", "maintenance.auto", "false")
	git("config", "gc.auto", "0")
	git("add", ".")
	git("commit", "-q", "-m", "init")
	os.Remove(filepath.Join(root, "gone.go"))
	write("ignored/built.go")
	write("a/untracked.go")
	for link, target := range map[string]string{"a/link.go": "one.go", "a/linkdir.go": "../b", "a/dangling.go": "nowhere.go"} {
		if err := os.Symlink(target, filepath.Join(root, filepath.FromSlash(link))); err != nil {
			t.Fatal(err)
		}
	}

	paths := []string{
		"a/one.go", "a/two.go", "b/deep/three.go", "root.go", "gone.go", "ignored/built.go", "a/untracked.go",
		"a/link.go", "a/linkdir.go", "a/dangling.go", "a/dir.go", "a/dir.go/inner.go", "root.go/under.go",
		"a//two.go", "a/./two.go", "b/deep/../deep/three.go", "/etc/hosts.go", "missing/folder/x.go",
	}
	body := "Prose with no path in it, e.g. this one.\n"
	for _, p := range paths {
		body += "See `" + p + "`, and (./" + p + ").\n"
	}
	members := []*tracker.Issue{{Slug: "one", BodyRaw: body}, {Slug: "two", BodyRaw: "Only a/two.go and root.go, named again.\n"}}

	names := func(chips []WbCodeChip) []string {
		var out []string
		for _, c := range chips {
			out = append(out, c.Path)
		}
		return out
	}
	for _, tc := range []struct {
		name  string
		globs []string
		chips []string
		owned []string
	}{
		{
			name:  "files its issues name",
			chips: []string{"a/two.go", "root.go", "a/./two.go"},
			owned: []string{"a/./two.go", "a//two.go", "a/dir.go/inner.go", "a/link.go", "a/one.go", "a/two.go", "a/untracked.go", "b/deep/three.go", "ignored/built.go", "root.go"},
		},
		{
			name:  "its paths:",
			globs: []string{"a/", "*.go"},
			chips: []string{"a/two.go", "root.go", "a/dir.go/inner.go"},
			owned: []string{"a/dangling.go", "a/dir.go/inner.go", "a/link.go", "a/linkdir.go", "a/one.go", "a/two.go", "a/untracked.go", "b/deep/three.go", "gone.go", "root.go"},
		},
	} {
		c := newCodeIndex(root, nil)
		if got := names(c.chipsFor("Sys", tc.globs, members)); !reflect.DeepEqual(got, tc.chips) {
			t.Errorf("%s: row %q, want %q", tc.name, got, tc.chips)
		}
		var owned []string
		for p := range c.owned["Sys"] {
			owned = append(owned, p)
		}
		sort.Strings(owned)
		if !reflect.DeepEqual(owned, tc.owned) {
			t.Errorf("%s: owned %q, want %q", tc.name, owned, tc.owned)
		}
		for p, ok := range c.exists {
			if want := statExists(root, p); ok != want {
				t.Errorf("%s: exists[%q] = %v, a stat says %v", tc.name, p, ok, want)
			}
		}
	}
}

// filePaths finds what the pattern finds over the whole text, on the live
// shapes: paths at either end of the text, beside word bytes the pattern
// stops at, several in a run, and non-ASCII beside a path.
func TestFilePathsMatchThePattern(t *testing.T) {
	texts := []string{
		"", "no paths here", "a.go", "a.go b.ts", " a.go", "a.go ", "(a.go)", "`a/b.go`:12", "a.go.txt", "a.go/b.go", "a.go.b.ts",
		"foo.gox foo.go_ foo.go1 foo.go-1 foo.go@x", "a.tsx.ts a.c.cpp a.h", "é.go a.goé żółw/b.py żółw", "https://x.io/a/b.html?q=1#c.go",
		"e.g. i.e. v0.61 1.2.3.go ..go ...sh", ".go", "/a.go", "./a.go", "a.b\nc.go\r\nd.rs\te.py", "a\x00b.go\xffc.go",
		"mixed.GO upper.Go lower.go", "trailing.go.", "trailing.go/", "a..go a/.go a@b.go",
	}
	for _, f := range []string{"systems_wb.go", "../../CLAUDE.md", "../../docs/workflow.md", "../../docs/CLI/overview.md"} {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		texts = append(texts, string(data))
	}
	for _, text := range texts {
		if got, want := filePaths(text), filePattern.FindAllString(text, -1); !slices.Equal(got, want) {
			t.Errorf("%.60q:\n got  %q\n want %q", text, got, want)
		}
	}
}

// The lot the hall pages' headers read gives the numbers the company page
// gives: the pages stopped building the company for it, not reading it.
func TestHeaderLotMatchesTheCompany(t *testing.T) {
	withMockTmuxSessions(t, nil)
	proj, _ := setupTestProject(t)
	srv, err := NewServer([]tracker.Project{proj})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	prefix := "/p/" + proj.Slug
	company := srv.buildCompany(&proj, prefix, now)
	lot := srv.headerLot(&proj, now)
	if lot.Waiting != company.Waiting {
		t.Errorf("Waiting: %d, the company says %d", lot.Waiting, company.Waiting)
	}
	got, want := systemsHeader(&proj, prefix, lot), systemsHeader(&proj, prefix, company.Lot)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("header:\n got  %+v\n want %+v", got, want)
	}
	if got, want := ceoMessageBox(lot, prefix), company.Message; got != want {
		t.Errorf("message box: %+v, the company says %+v", got, want)
	}
}
