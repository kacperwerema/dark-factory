package bench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEachMessageCountsOnceWithTime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	lines := []string{
		`{"type":"assistant","timestamp":"2026-10-28T09:00:00.5Z","message":{"id":"a","model":"claude-x","usage":{"input_tokens":3,"output_tokens":4}}}`,
		`{"type":"assistant","timestamp":"2026-10-28T09:00:01Z","message":{"id":"a","model":"claude-x","usage":{"input_tokens":3,"output_tokens":4}}}`,
		`{"type":"user","message":{"content":"hello"}}`,
		`{"type":"assistant","timestamp":"2026-10-28T09:00:02Z","message":{"id":"b","model":"<synthetic>","usage":{"input_tokens":1}}}`,
	}
	os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
	var got []Message
	EachMessage(path, nil, func(m Message) { got = append(got, m) })
	if len(got) != 3 || !got[0].Counted || got[1].Counted || !got[2].Counted {
		t.Fatalf("messages = %+v", got)
	}
	if got[0].ID != "a" || got[0].Tokens() != 7 || got[0].Timestamp.Nanosecond() != 5e8 || got[2].Model != "" {
		t.Errorf("first = %+v, third model %q", got[0], got[2].Model)
	}
}

func TestTranscriptFoldersAndSessionFiles(t *testing.T) {
	root := t.TempDir()
	if got := TranscriptFolders(root, "/a/b.c"); len(got) != 1 || got[0] != filepath.Join(root, "-a-b-c") {
		t.Errorf("short cwd = %v", got)
	}
	long := "/" + strings.Repeat("x", 250)
	slug := "-" + strings.Repeat("x", 250)
	cut := filepath.Join(root, slug[:TranscriptSlugMax]+"-h4sh")
	os.MkdirAll(filepath.Join(cut, "s1", "subagents"), 0o755)
	if got := TranscriptFolders(root, long); len(got) != 1 || got[0] != cut {
		t.Errorf("long cwd = %v, want %s", got, cut)
	}
	os.WriteFile(filepath.Join(cut, "s1.jsonl"), nil, 0o644)
	os.WriteFile(filepath.Join(cut, "s1", "subagents", "a.jsonl"), nil, 0o644)
	files := SessionFiles(cut)
	if f := files[filepath.Join(cut, "s1.jsonl")]; len(files) != 1 || len(f) != 2 {
		t.Errorf("session files = %v", files)
	}
	if TranscriptFolders("", "/a") != nil {
		t.Error("no projects dir: none")
	}
}

func TestPriceFor(t *testing.T) {
	prices := map[string]Price{"claude-opus": {In: 1}, "claude-opus-5": {In: 2}, "claude-x-1": {In: 3}}
	for model, want := range map[string]float64{"claude-opus-5-5": 2, "claude-opus-4": 1, "claude-x-1": 3} {
		if p, ok := PriceFor(model, prices); !ok || p.In != want {
			t.Errorf("PriceFor(%s) = %v %v, want %v", model, p, ok, want)
		}
	}
	if _, ok := PriceFor("gpt", prices); ok {
		t.Error("no matching key")
	}
	if _, ok := PriceFor("", prices); ok {
		t.Error("no model")
	}
	if got := (Price{In: 1, Out: 10, CacheRead: 0.1, CacheWrite: 2}).USD(1e6, 1e6, 1e6, 1e6); got != 13.1 {
		t.Errorf("USD = %v", got)
	}
}
