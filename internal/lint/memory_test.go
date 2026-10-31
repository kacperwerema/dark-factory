package lint

import (
	"strings"
	"testing"
	"time"
)

func memoryFindings(t *testing.T, raw string) []string {
	t.Helper()
	got, err := File("/nowhere/workflow.yaml", []byte(routingBase+raw), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range got {
		if f.Check == "memory" {
			out = append(out, f.Message)
		}
	}
	return out
}

func TestMemoryLint(t *testing.T) {
	if got := memoryFindings(t, ""); len(got) != 0 {
		t.Fatalf("no memory: block: %v", got)
	}
	ok := "memory:\n  embedder:\n    url: http://127.0.0.1:11434/api/embed\n    model: nomic-embed-text\n    timeout: 3s\n"
	if got := memoryFindings(t, ok); len(got) != 0 {
		t.Fatalf("local embedder: %v", got)
	}
	cmd := "memory:\n  embedder:\n    command: [swift, scripts/memory-embed-macos.swift]\n    model: nl-sentence-en\n"
	if got := memoryFindings(t, cmd); len(got) != 0 {
		t.Fatalf("command embedder: %v", got)
	}
	hosted := "memory:\n  embedder:\n    url: https://api.example.com/v1/embed\n    model: big\n"
	if got := strings.Join(memoryFindings(t, hosted), "\n"); !strings.Contains(got, "leaves this machine") || !strings.Contains(got, "owner") {
		t.Fatalf("hosted embedder not flagged: %q", got)
	}
	bad := "memory:\n  embedder:\n    url: http://localhost:11434/api/embed\n    command: [x]\n    timeout: soon\n    min_similarity: 2\n"
	got := strings.Join(memoryFindings(t, bad), "\n")
	for _, want := range []string{"not both", "needs model", "timeout \"soon\"", "outside 0-1"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
