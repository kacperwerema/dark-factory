package bench

import "testing"

func TestWorkerAgentIsTheCommandsFirstWord(t *testing.T) {
	for in, want := range map[string]string{
		DefaultWorker: "claude",
		"codex exec --json --sandbox workspace-write -": "codex",
		"/opt/bin/codex exec -":                         "codex",
		"":                                              "",
	} {
		if got := WorkerAgent(in); got != want {
			t.Errorf("WorkerAgent(%q) = %q, want %q", in, got, want)
		}
	}
}
