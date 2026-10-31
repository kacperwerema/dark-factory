package spend

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// codexRollout writes a stand-in Codex session log (the RolloutLine shape of
// codex-rs/protocol: session_meta with the cwd, then token_count events with
// last_token_usage) under dir.
func codexRollout(t *testing.T, dir, name, cwd string, events ...string) {
	t.Helper()
	lines := []string{fmt.Sprintf(`{"timestamp":"2026-10-28T09:00:00.000Z","type":"session_meta","payload":{"id":"x","timestamp":"2026-10-28T09:00:00.000Z","cwd":%q,"originator":"codex_cli_rs"}}`, cwd),
		`{"timestamp":"2026-10-28T09:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"secret words"}]}}`}
	lines = append(lines, events...)
	path := filepath.Join(dir, "2026", "10", "28", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func tokenCount(at time.Time, in, cached, out int64) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":999999,"cached_input_tokens":0,"output_tokens":999999,"reasoning_output_tokens":0,"total_tokens":1},"last_token_usage":{"input_tokens":%d,"cached_input_tokens":%d,"output_tokens":%d,"reasoning_output_tokens":0,"total_tokens":0}},"rate_limits":null}}`,
		at.UTC().Format(time.RFC3339Nano), in, cached, out)
}

func TestSpendSplitsBySubscriptionClaudeAndCodex(t *testing.T) {
	f := newFixture(t)
	day := now.Add(-24 * time.Hour)
	f.write(f.worktree("seo/page-refresh"), "a.jsonl", "", msg("m1", day, "claude-test", 100, 10, 0, 0))
	codex := t.TempDir()
	// A Codex worker in the project's worktree: counted, input without the cached part.
	codexRollout(t, codex, "rollout-2026-10-28T09-00-00-a.jsonl", f.worktree("ui/button"),
		tokenCount(day, 500, 200, 50), tokenCount(day.Add(time.Minute), 100, 0, 10))
	// Another project's Codex session: not ours.
	codexRollout(t, codex, "rollout-2026-10-28T09-00-00-b.jsonl", "/elsewhere/project", tokenCount(day, 7777, 0, 7777))
	// Not a rollout file: never opened.
	os.WriteFile(filepath.Join(codex, "auth.json"), []byte(`{"tokens":"never read"}`), 0o600)

	rep := Load(f.p, f.issues, now, Options{ProjectsDir: f.projects, Prices: prices, CodexDir: codex})
	if len(rep.Subscriptions) != 2 {
		t.Fatalf("subscriptions = %+v", rep.Subscriptions)
	}
	cl, cx := rep.Subscriptions[0], rep.Subscriptions[1]
	if cl.Agent != "claude" || cl.ThisWeek().Tokens() != 110 || !cl.Read {
		t.Errorf("claude = %+v", cl)
	}
	w := cx.ThisWeek()
	if cx.Agent != "codex" || !cx.Read || w.Input != 400 || w.CacheRead != 200 || w.Output != 60 || w.Unpriced != w.Tokens() {
		t.Errorf("codex = %+v (this week %+v)", cx, w)
	}
	if empty := Load(f.p, f.issues, now, Options{ProjectsDir: f.projects, Prices: prices, CodexDir: filepath.Join(codex, "none")}); empty.Subscriptions[1].Read {
		t.Error("a missing Codex folder reads as read")
	}
}
