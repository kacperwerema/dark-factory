package memory

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// concepts is the stub embedder's model: each word maps to one meaning,
// so texts that share no word can share a meaning.
var concepts = map[string]int{
	"flaky": 0, "timing": 0, "busy": 0, "load": 0, "slow": 0, "loaded": 0,
	"release": 1, "ship": 1, "changelog": 1, "unreleased": 1,
	"tmux": 2, "socket": 2, "server": 2,
}

// conceptVector embeds text as counts of its words' meanings, plus a small
// constant so no vector is all zero.
func conceptVector(text string) []float32 {
	v := []float32{0, 0, 0, 0.05}
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return r < 'a' || r > 'z' }) {
		if i, ok := concepts[w]; ok {
			v[i]++
		}
	}
	return v
}

// stubEmbedder embeds with conceptVector and counts its calls and texts.
type stubEmbedder struct {
	calls, texts int
	err          error
}

func (s *stubEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	s.calls++
	s.texts += len(texts)
	if s.err != nil {
		return nil, s.err
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = conceptVector(t)
	}
	return out, nil
}

// memUsed stubs mem-guard's reading for one test.
func memUsed(t *testing.T, used int, err error) {
	t.Helper()
	old := MemoryUsed
	MemoryUsed = func() (int, error) { return used, err }
	t.Cleanup(func() { MemoryUsed = old })
}

func meaningMems() []Memory {
	mk := func(id, text string) Memory {
		return Memory{ID: id, Text: text, Kind: KindPitfall, Scope: ScopeProject, Created: "2026-10-31T10:00:00Z", Hash: Hash(text)}
	}
	return []Memory{
		mk("m-1", "Timing tests fail on a busy machine; compare against a cheaper request"),
		mk("m-2", "Changelog entries go under Unreleased"),
		mk("m-3", "Throwaway tmux servers use their own socket"),
	}
}

func ids(hits []Hit) string {
	var out []string
	for _, h := range hits {
		out = append(out, h.ID)
	}
	return strings.Join(out, ",")
}

// AC1: with an embedder, a query that shares no word with a memory finds
// it by meaning; BM25 alone finds nothing.
func TestHybridRanksByMeaning(t *testing.T) {
	memUsed(t, 30, nil)
	mems := meaningMems()
	q := Query{Text: "flaky when loaded", Limit: 5}
	if hits := (BM25{}).Rank(q, mems); len(hits) != 0 {
		t.Fatalf("BM25 should find no word in common, got %s", ids(hits))
	}
	emb := &stubEmbedder{}
	h := &Hybrid{Embedder: emb, Cache: VectorCache{Dir: t.TempDir(), Model: "stub"}, MinSimilarity: 0.5}
	hits := h.Rank(q, mems)
	if ids(hits) != "m-1" {
		t.Fatalf("hybrid hits = %q, want m-1 only", ids(hits))
	}
	if h.Note != "words + meaning (stub)" || h.Embedded != 3 {
		t.Fatalf("note %q, embedded %d", h.Note, h.Embedded)
	}

	// Words still count: a word match and a meaning match both rank, the
	// one with both first.
	hits = h.Rank(Query{Text: "release the changelog", Limit: 5}, mems)
	if len(hits) == 0 || hits[0].ID != "m-2" {
		t.Fatalf("release query = %q, want m-2 first", ids(hits))
	}
}

// The vectors are cached per memory hash and model: a second search embeds
// only the query, a new model embeds again, and MaxEmbed caps a call.
func TestHybridCachesVectorsPerHashAndModel(t *testing.T) {
	memUsed(t, 30, nil)
	dir := t.TempDir()
	mems := meaningMems()
	emb := &stubEmbedder{}
	h := &Hybrid{Embedder: emb, Cache: VectorCache{Dir: dir, Model: "stub:v1"}}
	h.Rank(Query{Text: "flaky load"}, mems)
	if emb.texts != 4 {
		t.Fatalf("first call embedded %d texts, want query + 3", emb.texts)
	}
	if _, err := os.Stat(filepath.Join(dir, "stub_v1", mems[0].Hash+".json")); err != nil {
		t.Fatalf("vector not cached: %v", err)
	}
	h.Rank(Query{Text: "flaky load"}, mems)
	if emb.texts != 5 || h.Embedded != 0 {
		t.Fatalf("second call embedded %d texts in all (%d memories), want only the query", emb.texts, h.Embedded)
	}
	other := &Hybrid{Embedder: emb, Cache: VectorCache{Dir: dir, Model: "stub:v2"}, MaxEmbed: 2}
	other.Rank(Query{Text: "flaky load"}, mems)
	if other.Embedded != 2 {
		t.Fatalf("new model with MaxEmbed 2 embedded %d memories", other.Embedded)
	}
	other.Rank(Query{Text: "flaky load"}, mems)
	if other.Embedded != 1 {
		t.Fatalf("the rest fill in on the next call: embedded %d", other.Embedded)
	}
}

// AC2: mem-guard is read before each embed; at 70% or more, no request is
// made and the call is BM25 with the reason in Note. Under 70% it embeds.
func TestHybridFallsBackToBM25WhenMemoryIsHigh(t *testing.T) {
	mems := meaningMems()
	q := Query{Text: "changelog for the release", Limit: 5}
	want := ids((BM25{}).Rank(q, mems))
	for _, used := range []int{70, 85} {
		memUsed(t, used, nil)
		emb := &stubEmbedder{}
		h := &Hybrid{Embedder: emb, Cache: VectorCache{Dir: t.TempDir(), Model: "stub"}}
		got := h.Rank(q, mems)
		if emb.calls != 0 {
			t.Fatalf("at %d%% the embedder was called %d times", used, emb.calls)
		}
		if ids(got) != want || !strings.Contains(h.Note, "words only: memory at") {
			t.Fatalf("at %d%%: hits %q (BM25 %q), note %q", used, ids(got), want, h.Note)
		}
	}
	memUsed(t, 69, nil)
	emb := &stubEmbedder{}
	h := &Hybrid{Embedder: emb, Cache: VectorCache{Dir: t.TempDir(), Model: "stub"}}
	h.Rank(q, mems)
	if emb.calls != 1 {
		t.Fatalf("at 69%% the embedder was called %d times, want 1", emb.calls)
	}

	// Asked before each request, not once per process.
	calls := 0
	old := MemoryUsed
	MemoryUsed = func() (int, error) { calls++; return 10, nil }
	t.Cleanup(func() { MemoryUsed = old })
	h.Rank(q, mems)
	h.Rank(q, mems)
	if calls != 2 {
		t.Fatalf("mem-guard read %d times for 2 searches", calls)
	}
}

// No mem-guard, a down embedder or a refused config: BM25, and why.
func TestHybridFallsBackWhenItCannotEmbed(t *testing.T) {
	mems := meaningMems()
	q := Query{Text: "tmux socket", Limit: 5}
	want := ids((BM25{}).Rank(q, mems))

	memUsed(t, 0, errors.New("mem-guard not found"))
	emb := &stubEmbedder{}
	h := &Hybrid{Embedder: emb, Cache: VectorCache{Dir: t.TempDir(), Model: "stub"}}
	if got := ids(h.Rank(q, mems)); got != want || emb.calls != 0 || h.Note != "words only: mem-guard not found" {
		t.Fatalf("no mem-guard: %q calls %d note %q", got, emb.calls, h.Note)
	}

	memUsed(t, 20, nil)
	emb.err = errors.New("connection refused")
	if got := ids(h.Rank(q, mems)); got != want || !strings.Contains(h.Note, "embedder: connection refused") {
		t.Fatalf("down: %q note %q", got, h.Note)
	}

	r := &Hybrid{Refused: "url leaves this machine", Embedder: &stubEmbedder{}}
	if got := ids(r.Rank(q, mems)); got != want || r.Note != "words only: url leaves this machine" {
		t.Fatalf("refused: %q note %q", got, r.Note)
	}
}

// The HTTP embedder speaks Ollama's /api/embed and checks the answer.
func TestHTTPEmbedderSpeaksOllamaEmbed(t *testing.T) {
	var got embedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		ans := embedAnswer{}
		for _, t := range got.Input {
			ans.Embeddings = append(ans.Embeddings, conceptVector(t))
		}
		if got.Model == "short" {
			ans.Embeddings = ans.Embeddings[:1]
		}
		_ = json.NewEncoder(w).Encode(ans)
	}))
	defer srv.Close()
	e := HTTPEmbedder{URL: srv.URL + "/api/embed", Model: "nomic", Timeout: time.Second}
	vecs, err := e.Embed(context.Background(), []string{"flaky", "release"})
	if err != nil || len(vecs) != 2 || got.Model != "nomic" || strings.Join(got.Input, "|") != "flaky|release" {
		t.Fatalf("vecs %v err %v request %+v", vecs, err, got)
	}
	e.Model = "short"
	if _, err := e.Embed(context.Background(), []string{"a", "b"}); err == nil || !strings.Contains(err.Error(), "1 vectors for 2 texts") {
		t.Fatalf("short answer: %v", err)
	}
	e.URL = srv.URL + "/nope"
	if _, err := e.Embed(context.Background(), []string{"a"}); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("404: %v", err)
	}
}

// The command embedder gets the request on stdin, in its dir, and is
// stopped at its timeout.
func TestCommandEmbedder(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "embed.sh")
	body := "#!/bin/sh\ncat > req.json\nprintf '{\"embeddings\":[[1,0],[0,1]]}'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	c := CommandEmbedder{Argv: []string{script}, Dir: dir, Model: "m", Timeout: 5 * time.Second}
	vecs, err := c.Embed(context.Background(), []string{"x", "y"})
	if err != nil || len(vecs) != 2 {
		t.Fatalf("vecs %v err %v", vecs, err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "req.json"))
	if string(raw) != `{"model":"m","input":["x","y"]}` {
		t.Fatalf("request on stdin = %s", raw)
	}
	slow := CommandEmbedder{Argv: []string{"sleep", "5"}, Timeout: 100 * time.Millisecond}
	if _, err := slow.Embed(context.Background(), []string{"x"}); err == nil || !strings.Contains(err.Error(), "no answer in time") {
		t.Fatalf("slow: %v", err)
	}
	bad := CommandEmbedder{Argv: []string{"sh", "-c", "echo boom >&2; exit 3"}, Timeout: time.Second}
	if _, err := bad.Embed(context.Background(), []string{"x"}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("failing command: %v", err)
	}
}

func TestCosine(t *testing.T) {
	if c := Cosine([]float32{1, 0}, []float32{1, 0}); c < 0.999 {
		t.Fatalf("same = %v", c)
	}
	if c := Cosine([]float32{1, 0}, []float32{0, 1}); c != 0 {
		t.Fatalf("orthogonal = %v", c)
	}
	if Cosine([]float32{1}, []float32{1, 2}) != 0 || Cosine([]float32{0, 0}, []float32{1, 1}) != 0 {
		t.Fatal("mismatched or zero vectors should be 0")
	}
}
