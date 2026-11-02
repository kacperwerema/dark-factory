package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// meaningServer is a stand-in local embedder on 127.0.0.1: each text's
// vector counts words of two meanings (load, release), so texts with no
// word in common can mean the same.
func meaningServer(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	meaning := map[string]int{"flaky": 0, "timing": 0, "busy": 0, "loaded": 0, "release": 1, "changelog": 1}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req struct{ Input []string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		var out [][]float32
		for _, text := range req.Input {
			v := []float32{0, 0, 0.05}
			for _, word := range strings.Fields(strings.ToLower(text)) {
				if i, ok := meaning[strings.Trim(word, ".,;")]; ok {
					v[i]++
				}
			}
			out = append(out, v)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func embedderProject(t *testing.T, block string) (*tracker.Project, *Context, *bytes.Buffer) {
	t.Helper()
	proj := goalsProject(t)
	if block != "" {
		wf := filepath.Join(proj.WorkDir, "workflow.yaml")
		mustWrite(t, wf, block)
		proj.WorkflowFile = wf
	}
	ctx, out := goalsCtx(t, proj, false)
	for _, text := range []string{"Timing tests fail on a busy machine", "Changelog entries go under the next version"} {
		if err := runMemory(ctx, []string{"add", text, "--kind", "pitfall"}); err != nil {
			t.Fatal(err)
		}
	}
	out.Reset()
	return proj, ctx, out
}

// AC1 end to end: memory search with a local embedder in workflow.yaml
// finds a memory by meaning and says so; without one, BM25 alone and no
// line.
func TestMemorySearchUsesTheLocalEmbedder(t *testing.T) {
	memoryAs(t, "")
	old := memory.MemoryUsed
	memory.MemoryUsed = func() (int, error) { return 20, nil }
	t.Cleanup(func() { memory.MemoryUsed = old })

	_, ctx, out := embedderProject(t, "")
	if err := runMemory(ctx, []string{"search", "flaky", "when", "loaded"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No memory matches") || strings.Contains(out.String(), "Ranked by") {
		t.Fatalf("without an embedder:\n%s", out)
	}

	srv, calls := meaningServer(t)
	proj, ctx, out := embedderProject(t, fmt.Sprintf("memory:\n  embedder:\n    url: %s/api/embed\n    model: stub\n", srv.URL))
	if err := runMemory(ctx, []string{"search", "flaky", "when", "loaded"}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	assertContains(t, s, "Timing tests fail on a busy machine")
	assertContains(t, s, "Ranked by words + meaning (stub).")
	if strings.Contains(s, "Changelog") || *calls != 1 {
		t.Fatalf("calls %d, output:\n%s", *calls, s)
	}
	if got, _ := filepath.Glob(filepath.Join(memory.VectorDir(proj.IssueDir), "stub", "*.json")); len(got) != 2 {
		t.Fatalf("vectors cached: %v", got)
	}

	// start's == Memories == ranks with it too.
	var w strings.Builder
	printStartMemories(&w, ctx, &tracker.Issue{Slug: "x", Title: "Flaky when loaded"})
	assertContains(t, w.String(), "Timing tests fail")
}

// AC2 end to end: at 70% search makes no request and says why; a hosted
// URL is refused the same way.
func TestMemorySearchFallsBackAtSeventyPercent(t *testing.T) {
	memoryAs(t, "")
	old := memory.MemoryUsed
	memory.MemoryUsed = func() (int, error) { return 70, nil }
	t.Cleanup(func() { memory.MemoryUsed = old })

	srv, calls := meaningServer(t)
	_, ctx, out := embedderProject(t, fmt.Sprintf("memory:\n  embedder:\n    url: %s/api/embed\n    model: stub\n", srv.URL))
	if err := runMemory(ctx, []string{"search", "changelog"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Changelog entries")
	assertContains(t, out.String(), "Ranked by words only: memory at 70% (embeds wait below 70%).")
	if *calls != 0 {
		t.Fatalf("embedder called %d times at 70%%", *calls)
	}

	memory.MemoryUsed = func() (int, error) { return 10, nil }
	_, ctx, out = embedderProject(t, "memory:\n  embedder:\n    url: https://embed.example.com/api/embed\n    model: big\n")
	if err := runMemory(ctx, []string{"search", "changelog"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Changelog entries")
	assertContains(t, out.String(), "Ranked by words only: url \"https://embed.example.com/api/embed\" leaves this machine")
}
