package bench

import "testing"

func TestBenchWorkerSessionIsNamed(t *testing.T) {
	name := SessionName(Task{Project: "acme", Slug: "ui/fix-the-button"})
	if name != "bench-fix-the-button" {
		t.Errorf("name %q", name)
	}
	for in, want := range map[string]string{
		DefaultWorker:                "claude -n bench-x -p --output-format json --permission-mode bypassPermissions --disallowedTools SendMessage,ListAgents",
		"  /usr/local/bin/claude -p": "  /usr/local/bin/claude -n bench-x -p",
		"claude -p --name mine":      "claude -p --name mine",
		"codex exec --json":          "codex exec --json",
		"sh -c 'claude -p'":          "sh -c 'claude -p'",
	} {
		if got := NamedWorker(in, "bench-x"); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	if got := NamedWorker(DefaultWorker, ""); got != DefaultWorker {
		t.Errorf("no name: %q", got)
	}
}
