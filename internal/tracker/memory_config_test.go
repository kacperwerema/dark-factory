package tracker

import (
	"strings"
	"testing"
	"time"
)

func TestLoopbackURL(t *testing.T) {
	for _, u := range []string{"http://127.0.0.1:11434/api/embed", "http://localhost:8080/x", "http://[::1]:1/api/embed", "https://127.0.0.2/e"} {
		if err := LoopbackURL(u); err != nil {
			t.Errorf("%s refused: %v", u, err)
		}
	}
	for _, u := range []string{"https://api.openai.com/v1/embeddings", "http://192.168.1.5:11434/api/embed", "http://0.0.0.0:11434/", "file:///etc/passwd", "127.0.0.1:11434", "http://localhost.example.com/"} {
		if err := LoopbackURL(u); err == nil {
			t.Errorf("%s allowed", u)
		}
	}
}

func TestEmbedderConfigDefaults(t *testing.T) {
	var wf *WorkflowConfig
	if wf.MemoryEmbedder() != nil {
		t.Fatal("nil workflow has an embedder")
	}
	e := &EmbedderConfig{URL: "http://127.0.0.1:11434/api/embed", Model: "m"}
	if e.TimeoutOrDefault() != 5*time.Second || e.MinSimilarityOrDefault() != 0.5 || len(e.Problems()) != 0 {
		t.Fatalf("defaults: %v %v %v", e.TimeoutOrDefault(), e.MinSimilarityOrDefault(), e.Problems())
	}
	e.Timeout, e.MinSimilarity = "800ms", 0.3
	if e.TimeoutOrDefault() != 800*time.Millisecond || e.MinSimilarityOrDefault() != 0.3 {
		t.Fatal("set values not used")
	}
	if p := (&EmbedderConfig{Model: "m"}).Problems(); len(p) != 1 || !strings.Contains(p[0], "needs url") {
		t.Fatalf("empty: %v", p)
	}
}
