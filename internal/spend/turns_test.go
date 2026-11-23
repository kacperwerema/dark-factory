package spend

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// toolTurn is an assistant line calling one tool (Bash with a command, or
// Read), with usage.
func toolTurn(id, tool string, at time.Time, context int64, input string) string {
	key := "command"
	if tool == "Read" {
		key = "file_path"
	}
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"id":%q,"model":"claude-test","usage":{"input_tokens":%d,"output_tokens":1},"content":[{"type":"tool_use","id":"tu-%s","name":%q,"input":{%q:%q}}]}}`,
		at.Format(time.RFC3339Nano), id, context, id, tool, key, input)
}

// toolResult is the user line answering toolTurn id.
func toolResult(id string, at time.Time, isErr bool, text string) string {
	return fmt.Sprintf(`{"type":"user","timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu-%s","is_error":%t,"content":[{"type":"text","text":%q}]}]}}`,
		at.Format(time.RFC3339Nano), id, isErr, text)
}

func TestLoadTurnsSplitsTurnsByCause(t *testing.T) {
	f := kindsFixture(t)
	at := func(m int) time.Time { return time.Date(2026, 10, 28, 9, m, 0, 0, time.UTC) }
	is := f.issues[0] // seo/page-refresh, with a stats sidecar
	// A recorded red gate round from minute 20 to its green at minute 30.
	stats := `{"rounds":[{"check":"gate:tests","red":true,"at":"2026-10-28T09:20:00Z","head":"abc"},{"check":"gate:tests","red":false,"at":"2026-10-28T09:30:00Z","head":"def"}]}`
	if err := os.WriteFile(tracker.StatsSidecarPath(is.FilePath), []byte(stats), 0o644); err != nil {
		t.Fatal(err)
	}
	secret := "cat /etc/secret-token-abc123 | grep password"
	f.write(f.worktree(is.Slug), "w.jsonl", "agent-seo-page-refresh",
		toolTurn("t1", "Bash", at(1), 100, `./issue-cli --project acme transition --to testing 2>&1 | tail -20`),
		toolResult("t1", at(1), false, "Error: failed to transition: Validate section Design"),
		toolTurn("t2", "Bash", at(2), 100, `issue-cli transition seo/page-refresh --to testing`),
		toolResult("t2", at(2), false, "✓ in progress → testing"),
		toolTurn("t3", "Bash", at(3), 100, `make validate > /tmp/v.log 2>&1`),
		toolTurn("t4", "Bash", at(4), 100, `tail -3 /tmp/validate.log`),
		toolResult("t4", at(4), false, "validate: red so far: test\nvalidate: FAIL (test)"),
		// Red from minute 4 (validate) to its PASS at minute 8.
		toolTurn("t5", "Bash", at(5), 100, `go test ./internal/x`),
		toolTurn("t6", "Read", at(6), 100, "/repo/a.go"),
		toolTurn("t7", "Bash", at(7), 100, `cd /repo && sed -n '1,80p' internal/x/a.go | cut -c1-200`),
		toolTurn("t8", "Bash", at(8), 100, `tail -1 /tmp/validate.log`),
		toolResult("t8", at(8), false, "validate: PASS"),
		toolTurn("t9", "Bash", at(9), 100, `sed -n '1,8p' a.go; sed -n '1,8p' b.go`), // two files: rest
		toolTurn("t10", "Bash", at(10), 100, secret),
		// In the recorded gate round's red span.
		toolTurn("t21", "Bash", at(21), 100, `git diff`),
		toolTurn("t31", "Bash", at(31), 100, `git status`),
	)
	// The critic in the same worktree is not the worker's.
	f.write(f.worktree(is.Slug), "c.jsonl", "critic-page-refresh", toolTurn("c1", "Read", at(5), 100, "/x"))
	rep := LoadTurns(f.p, f.issues, "", kSince, kUntil, Options{ProjectsDir: f.projects})
	if len(rep.Issues) != 1 {
		t.Fatalf("issues: %+v", rep.Issues)
	}
	it := rep.Issues[0]
	want := map[string]int{CauseTransition: 2, CauseValidate: 3, CauseRead: 2, CauseRedRound: 2, CauseRest: 3}
	for c, n := range want {
		if it.By[c].Turns != n {
			t.Errorf("%s: %d turns, want %d (%+v)", c, it.By[c].Turns, n, it.By)
		}
	}
	if it.Turns != 12 || it.Sessions != 1 || it.Refused != 1 || it.By[CauseTransition].Context != 200 {
		t.Errorf("issue: %+v", it)
	}
	// The recorded gate round and the session's red validate run.
	if it.RedRounds != 2 {
		t.Errorf("red rounds: %d", it.RedRounds)
	}
	data, _ := json.Marshal(rep)
	for _, s := range []string{"secret-token", "password", "internal/x", "failed to transition"} {
		if strings.Contains(string(data), s) {
			t.Errorf("command or result text %q in the report", s)
		}
	}
	if one := LoadTurns(f.p, f.issues, "seo/blog", kSince, kUntil, Options{ProjectsDir: f.projects}); len(one.Issues) != 0 {
		t.Errorf("another issue: %+v", one.Issues)
	}
}

func TestSingleFileRead(t *testing.T) {
	for cmd, want := range map[string]bool{
		"cat a.go":                                     true,
		"sed -n '1,80p' internal/x/a.go":               true,
		`sed -n "/func X/,/^}/p" a.go`:                 true,
		"cd /repo && sed -n 10,20p a.go | cut -c1-200": true,
		"head -50 a.go":                                true,
		"tail -n 20 /tmp/log.txt":                      true,
		"/usr/bin/grep -n 'func X' a.go | head -5":     true,
		"rtk read a.go":                                true,
		"grep -A3 -n foo a.go 2>/dev/null":             true,
		"cat a.go b.go":                                false,
		"sed -n '1,8p' a.go; sed -n '1,8p' b.go":       false,
		"sed -i '' 's/a/b/' a.go":                      false,
		"sed 's/a/b/' a.go":                            false,
		"grep -rn foo internal":                        false,
		"grep foo *.go":                                false,
		"cat a.go > b.go":                              false,
		"cat $F":                                       false,
		"cat a.go | python3 x.py":                      false,
		"go test ./...":                                false,
		"cat <<EOF\nx\nEOF":                            false,
		"cat 'unclosed":                                false,
	} {
		if got := singleFileRead(cmd); got != want {
			t.Errorf("%q: %v, want %v", cmd, got, want)
		}
	}
}

func TestToolCause(t *testing.T) {
	for _, c := range []struct{ tool, cmd, want string }{
		{"Bash", "./issue-cli --project p transition --to x", CauseTransition},
		{"Bash", "rtk issue-cli transition s --to x --check A1", CauseTransition},
		{"Bash", "issue-cli process transitions", CauseRest},
		{"Bash", "make validate > /tmp/x 2>&1", CauseValidate},
		{"Bash", "VALIDATE_STEPS=vet make validate", CauseValidate},
		{"Bash", "grep -c FAIL /tmp/validate-run.log", CauseValidate},
		{"Read", "", CauseRead},
		{"Edit", "", CauseRest},
		{"Bash", "go build ./...", CauseRest},
	} {
		if got := toolCause(c.tool, c.cmd); got != c.want {
			t.Errorf("%s %q: %s, want %s", c.tool, c.cmd, got, c.want)
		}
	}
	if red, ok := validateVerdict("x\nvalidate: total 3s\nvalidate: PASS\n"); !ok || red {
		t.Error("PASS is green")
	}
	if red, ok := validateVerdict("validate: red so far: vet"); !ok || !red {
		t.Error("red so far is red")
	}
	if _, ok := validateVerdict("validate: vet 2s"); ok {
		t.Error("a step line is no verdict")
	}
}

// AC5: dry runs and follow-on reads, per issue and in the total, with no
// command text kept.
func TestLoadTurnsCountsDryRunsAndFollowOnReads(t *testing.T) {
	f := kindsFixture(t)
	at := func(m int) time.Time { return time.Date(2026, 10, 28, 9, m, 0, 0, time.UTC) }
	is := f.issues[0]
	f.write(f.worktree(is.Slug), "a.jsonl", "agent-seo-page-refresh",
		toolTurn("d1", "Bash", at(1), 100, `./issue-cli --project acme transition --to testing --dry-run 2>&1 | head -12`),
		toolTurn("d2", "Bash", at(2), 100, `issue-cli transition seo/page-refresh --to testing --check I1`),
		toolTurn("r1", "Read", at(3), 100, "/repo/a.go"),
		toolTurn("r2", "Bash", at(4), 100, `sed -n '1,80p' /repo/b-dry-run-notes.go`), // follow-on
		toolTurn("r3", "Bash", at(5), 100, `cat /repo/c.go`),                          // follow-on
		toolTurn("x1", "Bash", at(6), 100, `go test ./internal/x`),
		toolTurn("r4", "Read", at(7), 100, "/repo/d.go"), // after rest: not one
	)
	// A second session: its first read follows nothing of the first's.
	f.write(f.worktree(is.Slug), "b.jsonl", "agent-seo-page-refresh",
		toolTurn("r5", "Read", at(8), 100, "/repo/e.go"),
		toolTurn("d3", "Bash", at(9), 100, `issue-cli transition --to done --dry-run`),
	)
	rep := LoadTurns(f.p, f.issues, "", kSince, kUntil, Options{ProjectsDir: f.projects})
	if len(rep.Issues) != 1 {
		t.Fatalf("issues: %+v", rep.Issues)
	}
	it := rep.Issues[0]
	if it.DryRuns != 2 || it.FollowOnReads != 2 {
		t.Errorf("dry runs %d (want 2), follow-on reads %d (want 2): %+v", it.DryRuns, it.FollowOnReads, it)
	}
	// One-call read-only turns straight after another: r2 and r3; r1
	// follows a transition, r4 a go test.
	if it.FollowOnReadOnly != 2 || it.FollowOnReadOnlyContext != 200 {
		t.Errorf("follow-on read-only %d / %d (want 2 / 200)", it.FollowOnReadOnly, it.FollowOnReadOnlyContext)
	}
	if rep.Total.DryRuns != 2 || rep.Total.FollowOnReads != 2 {
		t.Errorf("total: %+v", rep.Total)
	}
	data, _ := json.Marshal(rep)
	for _, s := range []string{`"dry_runs":2`, `"follow_on_reads":2`, `"follow_on_read_only":2`} {
		if !strings.Contains(string(data), s) {
			t.Errorf("JSON lacks %s: %s", s, data)
		}
	}
	for _, s := range []string{"notes", "/repo", "--check"} {
		if strings.Contains(string(data), s) {
			t.Errorf("command text %q in the report", s)
		}
	}
}
