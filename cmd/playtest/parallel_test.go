package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/testresults"
)

// TestUXWorkersFollowMemGuard: mem-guard BLOCKED at the start runs one
// worker and says so; no mem-guard runs -jobs workers; -headed runs one.
func TestUXWorkersFollowMemGuard(t *testing.T) {
	old := memCheck
	t.Cleanup(func() { memCheck = old })
	for _, c := range []struct {
		name           string
		found, blocked bool
		msg            string
		jobs           int
		headed         bool
		want           int
		note           string
	}{
		{"no mem-guard", false, false, "", 3, false, 3, "ux-test: 3 workers"},
		{"quiet", true, false, "OK: used 45%", 3, false, 3, "ux-test: 3 workers (mem-guard: OK: used 45%)"},
		{"blocked", true, true, "BLOCKED: used 80%", 3, false, 1, "ux-test: 1 worker, not 3 (mem-guard: BLOCKED: used 80%)"},
		{"headed", true, false, "OK", 3, true, 1, "ux-test: 1 worker (-headed shows one window)"},
		{"one asked", true, true, "BLOCKED", 1, false, 1, ""},
		{"zero asked", false, false, "", 0, false, 1, ""},
	} {
		memCheck = func() (bool, bool, string) { return c.found, c.blocked, c.msg }
		n, note := uxWorkers(c.jobs, c.headed)
		if n != c.want || note != c.note {
			t.Errorf("%s: uxWorkers = %d %q, want %d %q", c.name, n, note, c.want, c.note)
		}
	}
}

// TestRunPoolRunsEveryItemOnce: every item runs exactly once over the
// workers; a later worker stops when more says no, and worker 0 still
// finishes the rest; stop hands out nothing new.
func TestRunPoolRunsEveryItemOnce(t *testing.T) {
	var mu sync.Mutex
	seen := map[int]int{}
	byWorker := map[int]int{}
	runPool(3, 50, func() bool { return true }, nil, func(w, i int) {
		mu.Lock()
		seen[i]++
		byWorker[w]++
		mu.Unlock()
	})
	for i := range 50 {
		if seen[i] != 1 {
			t.Fatalf("item %d ran %d times", i, seen[i])
		}
	}

	seen = map[int]int{}
	byWorker = map[int]int{}
	runPool(3, 20, func() bool { return false }, nil, func(w, i int) {
		mu.Lock()
		seen[i]++
		byWorker[w]++
		mu.Unlock()
	})
	if len(seen) != 20 || byWorker[0] != 20 {
		t.Fatalf("blocked machine: worker 0 ran %d of 20 (by worker %v)", byWorker[0], byWorker)
	}

	var ran atomic.Int64
	var stopped atomic.Bool
	runPool(3, 100, nil, stopped.Load, func(w, i int) {
		if ran.Add(1) == 2 {
			stopped.Store(true)
		}
	})
	if n := ran.Load(); n > 4 {
		t.Fatalf("stop: %d items ran after the stop, want at most the ones in flight", n)
	}
}

// TestFreePortNeverHandsOutAPortTwice: workers staging scenarios at the same
// time never get the same port.
func TestFreePortNeverHandsOutAPortTwice(t *testing.T) {
	var mu sync.Mutex
	got := map[int]bool{}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 25 {
				p, err := freePort()
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				if got[p] {
					t.Errorf("port %d handed out twice", p)
				}
				got[p] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
}

// TestUXTestParallelMatchesSequential: three sessions on three workers give
// the same e2e.json sessions (in the selection's order) and the same FAIL
// lines as one worker, and each session's block prints whole.
func TestUXTestParallelMatchesSequential(t *testing.T) {
	needChrome(t)
	uxTestBins = buildTestBinaries(t)
	t.Cleanup(func() { uxTestBins = nil })
	old := memCheck
	t.Cleanup(func() { memCheck = old })
	memCheck = func() (bool, bool, string) { return false, false, "" }

	sessions := t.TempDir()
	for i, check := range []string{
		`visible: board.search`,
		`text_equals: { target: board.column.backlog.count, text: "99" }`,
		`visible: board.search`,
	} {
		name := fmt.Sprintf("par-%d", i+1)
		body := fmt.Sprintf(`session: %s
issue: x/y
scenario: basic
session_checks:
  viewports: [1440x900]
steps:
  - id: TP1
    title: step of %s
    setup:
      - open: /p/{project}/board?classic=1
    checks:
      - %s
`, name, name, check)
		if err := os.WriteFile(filepath.Join(sessions, name+".yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runUX := func(jobs int) (testresults.Playtest, string, string, int) {
		t.Helper()
		results := filepath.Join(t.TempDir(), "e2e.json")
		var out, errw bytes.Buffer
		code := run([]string{"ux-test", "-sessions", sessions, "-runs", t.TempDir(), "-results", results, "-jobs", fmt.Sprint(jobs)}, &out, &errw)
		var p testresults.Playtest
		data, err := os.ReadFile(results)
		if err != nil {
			t.Fatalf("jobs=%d: %v\n%s%s", jobs, err, out.String(), errw.String())
		}
		if err := json.Unmarshal(data, &p); err != nil {
			t.Fatal(err)
		}
		return p, out.String(), errw.String(), code
	}
	seq, _, seqErr, seqCode := runUX(1)
	par, parOut, parErr, parCode := runUX(3)

	if seqCode != 1 || parCode != 1 {
		t.Fatalf("exit codes: jobs=1 %d, jobs=3 %d, want 1 and 1\n%s", seqCode, parCode, parErr)
	}
	order := func(p testresults.Playtest) []string {
		var names []string
		for _, s := range p.Sessions {
			st := make([]string, 0, len(s.Steps))
			for _, x := range s.Steps {
				st = append(st, x.ID+"="+x.Status)
			}
			names = append(names, s.Session+":"+strings.Join(st, ","))
		}
		return names
	}
	want := "par-1:TP1=pass par-2:TP1=fail par-3:TP1=pass"
	if got := strings.Join(order(par), " "); got != want || strings.Join(order(seq), " ") != want {
		t.Errorf("e2e sessions: jobs=3 %q, jobs=1 %q, want %q", got, strings.Join(order(seq), " "), want)
	}
	fails := regexp.MustCompile(`(?m)^FAIL .*$`)
	if a, b := fails.FindAllString(seqErr, -1), fails.FindAllString(parErr, -1); strings.Join(a, "\n") != strings.Join(b, "\n") || len(a) != 1 || a[0] != "FAIL par-2/TP1 step of par-2" {
		t.Errorf("FAIL lines: jobs=1 %q, jobs=3 %q", a, b)
	}
	if !strings.Contains(parOut, "ux-test: 3 workers") {
		t.Errorf("no workers line:\n%s", parOut)
	}
	// Each session's block: its header, then only its own step and results
	// lines until the next header.
	var cur string
	for _, l := range strings.Split(parOut, "\n") {
		if m := regexp.MustCompile(`^== ux-test (par-\d) `).FindStringSubmatch(l); m != nil {
			cur = m[1]
			continue
		}
		if strings.Contains(l, "step of par-") && !strings.Contains(l, "step of "+cur) {
			t.Errorf("line %q sits in %s's block:\n%s", l, cur, parOut)
		}
		if strings.HasPrefix(l, "results: ") && !strings.Contains(l, string(filepath.Separator)+cur+string(filepath.Separator)) {
			t.Errorf("line %q sits in %s's block", l, cur)
		}
	}
}
