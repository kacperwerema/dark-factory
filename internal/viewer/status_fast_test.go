package viewer

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/queue"
	"github.com/kacperwerema/dark-factory/internal/recent"
	"github.com/kacperwerema/dark-factory/internal/standin"
	"github.com/kacperwerema/dark-factory/internal/timing"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// statusBigProject is bigProject made a git repository the way the engine
// board is one: this repo's workflow.yaml (release candidates on), a
// release/v0.9 branch with a v0.9.1-rc.1 patch candidate and its release
// worktree, and a queue of started items and backlog candidates, each asked
// about tmux. Its refs are aged past refsKeptYoung, so a kept reading
// stands.
func statusBigProject(t *testing.T) tracker.Project {
	t.Helper()
	proj := bigProject(t, 800)
	root := proj.WorkDir
	wf, err := os.ReadFile(filepath.Join("..", "..", "workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	proj.WorkflowFile = filepath.Join(root, "workflow.yaml")
	writeFile(t, proj.WorkflowFile, string(wf))
	writeFile(t, filepath.Join(root, "cmd", "issue-cli", "CHANGELOG.md"), "# Changelog\n\n## Unreleased\n\n- a fix\n\n## v0.9.0\n\n- first\n")
	q := queue.Queue{NextID: 1}
	for i := 0; i < 30; i++ {
		slug := fmt.Sprintf("queued-%02d", i)
		status, state := "in progress", queue.Started
		if i >= 20 {
			status, state = "backlog", queue.Open
		}
		writeFile(t, filepath.Join(proj.IssueDir, "Core", slug+".md"), "---\ntitle: \""+slug+"\"\nstatus: \""+status+"\"\ntype: \"feature\"\n---\n\nBody.\n")
		q.Items = append(q.Items, queue.Item{ID: q.NextID, Slug: "core/" + slug, State: state, Quote: "go", AddedAt: time.Now().Add(-time.Hour)})
		q.NextID++
	}
	data, _ := json.Marshal(q)
	writeFile(t, queue.Path(proj.IssueDir), string(data))

	gitRun(t, root, "init", "-q", "-b", "main")
	gitRun(t, root, "add", "-A")
	gitRun(t, root, "commit", "-q", "-m", "init")
	gitRun(t, root, "tag", "v0.9.0")
	gitRun(t, root, "branch", "release/v0.9")
	gitRun(t, root, "tag", "v0.9.1-rc.1", "release/v0.9")
	gitRun(t, root, "commit", "-q", "--allow-empty", "-m", "later")
	for i := 0; i < 5; i++ {
		gitRun(t, root, "branch", fmt.Sprintf("work/core/queued-%02d", i))
	}
	writeFile(t, filepath.Join(root, ".worktrees", "release-v0.9", "go.mod"), "module x\n")
	ageGitRefs(t, root)
	return proj
}

// ageGitRefs dates the repository's refs, HEAD and config an hour back.
func ageGitRefs(t *testing.T, root string) {
	t.Helper()
	old := time.Now().Add(-time.Hour)
	git := filepath.Join(root, ".git")
	filepath.WalkDir(filepath.Join(git, "refs"), func(p string, _ fs.DirEntry, err error) error {
		if err == nil {
			os.Chtimes(p, old, old)
		}
		return nil
	})
	for _, f := range []string{"HEAD", "config", "packed-refs"} {
		os.Chtimes(filepath.Join(git, f), old, old)
	}
}

// statusVsBoard is how many times /board a warm /status may take on a slow
// machine: the brief's 3×.
const statusVsBoard = 3

// statusLimit is a warm /status here: 150 ms, not the 500 ms the real board
// is held to, because this fixture's issues carry none of the real board's
// comments, decisions or departments to pay for, and 500 ms passed with a
// git and a tmux call per reading (337 ms here then, 43 ms kept; the real
// board: 0.6–0.75 s then). On a slow machine it answers to statusVsBoard.
const statusLimit = 150 * time.Millisecond

// statusRounds is how many /board and /status pairs a page gets before it
// fails: enough that a burst of load (CI beside several validates) misses at
// least one /status sample.
const statusRounds = 8

// statusPages are the warm pages statusTooSlow times.
var statusPages = []string{"status", "status.md", "status", "status"}

// statusTooSlow times each warm page in statusPages against /board
// (timing.Check: best of statusRounds, in turns) and returns a line per
// page that stays too slow. A page passes when its best time is under
// statusLimit, or under statusVsBoard× the best /board.
func statusTooSlow(timePage func(path string) time.Duration) (slow []string, best time.Duration) {
	best = 1<<63 - 1
	check := timing.Check{Limit: statusLimit, Ratio: statusVsBoard, Rounds: statusRounds}
	for _, page := range statusPages {
		r := check.Run(func() time.Duration { return timePage(page) }, func() time.Duration { return timePage("board") })
		best = min(best, r.Best)
		if r.Slow {
			slow = append(slow, fmt.Sprintf("%s warm: %s (baseline: /board)", page, r))
		}
	}
	return slow, best
}

// TestStatusPageFastOnABigProject: a warm /status (the Report) renders in
// under statusLimit, or, on a loaded machine, no slower than 3× /board
// timed beside it (statusTooSlow). Machine readings are kept as viewer.Run
// keeps them. A /status made 5× slower fails it.
func TestStatusPageFastOnABigProject(t *testing.T) {
	if testing.Short() {
		t.Skip("builds an 800-issue project")
	}
	proj := statusBigProject(t)
	recent.Keep(machineReadingKeep)
	t.Cleanup(func() { recent.Keep(0); recent.Forget() }) // no reading outlives the test
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
	board := get("board")
	cold := get("status")
	slow, best := statusTooSlow(get)
	for _, s := range slow {
		t.Error(s)
	}
	t.Logf("830 issues: /status cold %v, warm best %v; /board cold %v", cold, best, board)

	// The same check on a /status 5× slower must fail: a stand-in timer that
	// reads the real request and counts it five times.
	slower := func(path string) time.Duration {
		d := get(path)
		if strings.HasPrefix(path, "status") {
			d *= 5
		}
		return d
	}
	if slow, _ := statusTooSlow(slower); len(slow) == 0 {
		t.Errorf("a /status 5× slower passed: the check catches no real slowdown")
	} else {
		t.Logf("5× slower: %s", slow[0])
	}
}

// TestStatusTooSlowRidesOutABurst: load that lands on a page's first
// samples (three in a row, the most the old one-pair-per-try check took)
// fails no page while later samples are back to normal; a page slow in
// every sample still fails.
func TestStatusTooSlowRidesOutABurst(t *testing.T) {
	timer := func(spiked int, factor time.Duration) func(string) time.Duration {
		n := map[string]int{}
		return func(path string) time.Duration {
			if path == "board" {
				return 80 * time.Millisecond
			}
			n[path]++
			d := 200 * time.Millisecond // over statusLimit: only the ratio passes it
			if spiked < 0 || n[path] <= spiked {
				d *= factor
			}
			return d
		}
	}
	if slow, _ := statusTooSlow(timer(3, 5)); len(slow) > 0 {
		t.Errorf("a burst on the first three samples failed the check:\n%s", strings.Join(slow, "\n"))
	}
	if slow, _ := statusTooSlow(timer(-1, 5)); len(slow) == 0 {
		t.Errorf("a /status slow in every sample passed")
	}
}

// spawnLog puts git and tmux stand-ins first on PATH that log each call
// and run the real one; it returns a func that reads and clears the log.
func spawnLog(t *testing.T) func() []string {
	t.Helper()
	bin, log := t.TempDir(), filepath.Join(t.TempDir(), "spawns.log")
	for _, name := range []string{"git", "tmux"} {
		real, err := exec.LookPath(name)
		if err != nil {
			t.Skip(name + " not on PATH")
		}
		t.Setenv("SPAWN_REAL_"+strings.ToUpper(name), real)
		standin.Write(t, filepath.Join(bin, name), `n=$(basename "$0")
printf '%s %s\n' "$n" "$*" >> "$SPAWN_LOG"
if [ "$n" = git ]; then exec "$SPAWN_REAL_GIT" "$@"; fi
exec "$SPAWN_REAL_TMUX" "$@"
`)
	}
	t.Setenv("SPAWN_LOG", log)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		b, _ := os.ReadFile(log)
		os.Remove(log)
		return strings.FieldsFunc(string(b), func(r rune) bool { return r == '\n' })
	}
}

// TestStatusPageSpawnsNoGitWhenRefsUnchanged: with the refs as they were,
// a warm /status runs no git and no tmux has-session (one session list
// answers every queue item); a new tag makes it read git again.
func TestStatusPageSpawnsNoGitWhenRefsUnchanged(t *testing.T) {
	if testing.Short() {
		t.Skip("builds an 800-issue project")
	}
	proj := statusBigProject(t)
	spawns := spawnLog(t)
	recent.Keep(machineReadingKeep)
	t.Cleanup(func() { recent.Keep(0); recent.Forget() }) // no reading outlives the test
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	get := func() {
		resp, err := http.Get(ts.URL + "/p/big/status")
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d", resp.StatusCode)
		}
	}
	get()
	first := spawns()
	if !hasSpawn(first, "git ") {
		t.Fatalf("the first /status ran no git: the fixture doesn't reach the release card\n%s", strings.Join(first, "\n"))
	}
	get()
	var bad []string
	for _, s := range spawns() {
		if strings.HasPrefix(s, "git ") && !mainBranchLookup(s) || strings.HasPrefix(s, "tmux has-session") {
			bad = append(bad, s)
		}
	}
	if len(bad) > 0 {
		t.Errorf("a warm /status with the refs unchanged spawned %d git and has-session calls:\n%s", len(bad), strings.Join(bad, "\n"))
	}

	gitRun(t, proj.WorkDir, "tag", "v0.9.1-rc.2", "release/v0.9")
	get()
	if after := spawns(); !hasSpawn(after, "git ") {
		t.Errorf("a new tag read no git: the card would show the old candidate")
	}
}

// mainBranchLookup is gitmain's one call a request for the branch local CI
// tests (symbolic-ref HEAD, or the for-each-ref of main, master and
// origin's HEAD): not one of the page's ref readings.
func mainBranchLookup(call string) bool {
	return strings.Contains(call, "symbolic-ref") || strings.Contains(call, "refs/remotes/origin/HEAD")
}

func hasSpawn(spawns []string, prefix string) bool {
	for _, s := range spawns {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}
