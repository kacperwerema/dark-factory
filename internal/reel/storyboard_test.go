package reel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeFile writes body at dir/name, making its folder.
func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// storyboardFor is a valid storyboard of shape, as a map a test can break.
func storyboardFor(shape string) map[string]any {
	scene := func(id, role string, extra map[string]any) map[string]any {
		m := map[string]any{"id": id, "role": role, "duration": 2.0, "text": id,
			"narration": map[string]any{"text": "One two three.", "voice": "narrator", "audio": "n.mp3", "alignment": "w.json"}}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	app := map[string]any{"clips": []any{map[string]any{"source": "app-recording", "path": "media/app.mp4"}}}
	var scenes []any
	switch shape {
	case ShapeHookList:
		scenes = []any{scene("hook", "hook", nil), scene("a", "item", nil), scene("b", "item", nil), scene("cta", "cta", nil)}
	case ShapeBeforeAfter:
		scenes = []any{scene("hook", "hook", nil), scene("before", "before", nil), scene("after", "after", nil), scene("cta", "cta", nil)}
	case ShapeAppDemo:
		scenes = []any{scene("hook", "hook", nil), scene("pick", "step", app), scene("cta", "cta", nil)}
	}
	return map[string]any{
		"version": 1, "shape": shape, "title": "A reel",
		"brand":    map[string]any{"name": "Brand", "colors": map[string]any{"primary": "#123456", "text": "#fff", "background": "#000"}},
		"scenes":   scenes,
		"captions": map[string]any{"style": "words", "position": "lower"},
		"music":    map[string]any{"path": "media/bed.mp3", "licence": map[string]any{"source": "library", "ref": "track-42"}, "duck": true},
		"cta":      map[string]any{"text": "Try it", "handle": "@brand"},
	}
}

// saveStoryboard writes m as dir/sb.json with the media it names.
func saveStoryboard(t *testing.T, dir string, m map[string]any) string {
	t.Helper()
	for _, f := range []string{"n.mp3", "media/app.mp4", "media/bed.mp3", "media/ai.mp4", "media/stock.mp4"} {
		writeFile(t, dir, f, "x")
	}
	writeFile(t, dir, "w.json", `[{"word":"One","start":0.1,"end":0.4},{"word":"two","start":0.45,"end":0.8},{"word":"three.","start":0.85,"end":1.3}]`)
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return writeFile(t, dir, "sb.json", string(data))
}

func TestThreeShapesValidate(t *testing.T) {
	for _, shape := range Shapes {
		t.Run(shape, func(t *testing.T) {
			s, err := Load(saveStoryboard(t, t.TempDir(), storyboardFor(shape)))
			if err != nil {
				t.Fatal(err)
			}
			if ps := s.RenderProblems(); len(ps) > 0 {
				t.Fatalf("problems: %v", ps)
			}
		})
	}
}

func TestProblemsNameEachPath(t *testing.T) {
	m := storyboardFor(ShapeHookList)
	scenes := m["scenes"].([]any)
	// One item only (hook-list wants 2 to 6), a before scene that isn't a
	// hook-list role, an ai clip with no label, a stock clip with no
	// licence, a missing file, music with no licence ref.
	scenes[2] = map[string]any{"id": "x", "role": "before", "duration": 1.0, "clips": []any{
		map[string]any{"source": "ai", "path": "media/ai.mp4"},
		map[string]any{"source": "stock", "path": "media/stock.mp4"},
		map[string]any{"source": "owner", "path": "media/gone.mp4"},
	}}
	m["music"].(map[string]any)["licence"] = map[string]any{"source": "library"}
	m["cta"] = map[string]any{"text": ""}
	s, err := Load(saveStoryboard(t, t.TempDir(), m))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range s.Problems() {
		got = append(got, p.String())
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		`scenes[2].role: hook-list wants roles hook, item, cta, got "before"`,
		"scenes[2].clips[0].ai_generated: an ai clip must say ai_generated: true",
		"scenes[2].clips[0].tool: an ai clip names the tool",
		"scenes[2].clips[1].licence: a stock clip names its licence",
		"scenes[2].clips[2].path: no file media/gone.mp4",
		`scenes: hook-list wants 2 to 6 role "item", got 1`,
		"music.licence: licensed music only",
		"cta.text: missing",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
}

func TestAppDemoStepWantsAnAppRecording(t *testing.T) {
	m := storyboardFor(ShapeAppDemo)
	m["scenes"].([]any)[1].(map[string]any)["clips"] = []any{map[string]any{"source": "stock", "path": "media/stock.mp4", "licence": "cc0"}}
	s, err := Load(saveStoryboard(t, t.TempDir(), m))
	if err != nil {
		t.Fatal(err)
	}
	ps := s.Problems()
	if len(ps) != 1 || ps[0].Path != "scenes[1].clips" || !strings.Contains(ps[0].Msg, "app-recording") {
		t.Fatalf("problems = %v", ps)
	}
}

func TestUnknownFieldIsRefused(t *testing.T) {
	m := storyboardFor(ShapeHookList)
	m["titel"] = "typo"
	_, err := Load(saveStoryboard(t, t.TempDir(), m))
	if err == nil || !strings.Contains(err.Error(), `unknown field "titel"`) || !strings.Contains(err.Error(), "issue-cli reel schema") {
		t.Fatalf("err = %v", err)
	}
}

func TestRenderWantsTheTTSFiles(t *testing.T) {
	m := storyboardFor(ShapeHookList)
	n := m["scenes"].([]any)[0].(map[string]any)["narration"].(map[string]any)
	delete(n, "audio")
	delete(n, "alignment")
	s, err := Load(saveStoryboard(t, t.TempDir(), m))
	if err != nil {
		t.Fatal(err)
	}
	if ps := s.Problems(); len(ps) != 0 {
		t.Fatalf("validate wants no audio yet: %v", ps)
	}
	ps := s.RenderProblems()
	if len(ps) != 2 || ps[0].Path != "scenes[0].narration.audio" || !strings.Contains(ps[0].Msg, "TTS connector") || ps[1].Path != "scenes[0].narration.alignment" {
		t.Fatalf("render problems = %v", ps)
	}
}

// TestSchemaMatchesTypes keeps schema.json and the Go types in step: every
// json field of Storyboard is a property at the same place in the schema,
// and the schema has no property the Go types lack.
func TestSchemaMatchesTypes(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(Schema, &schema); err != nil {
		t.Fatal(err)
	}
	defs := schema["$defs"].(map[string]any)
	var walk func(at string, typ reflect.Type, node map[string]any)
	walk = func(at string, typ reflect.Type, node map[string]any) {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice {
			typ = typ.Elem()
			if items, ok := node["items"].(map[string]any); ok {
				node = items
			}
		}
		if ref, ok := node["$ref"].(string); ok {
			node = defs[strings.TrimPrefix(ref, "#/$defs/")].(map[string]any)
		}
		if typ.Kind() != reflect.Struct {
			return
		}
		props, _ := node["properties"].(map[string]any)
		if node["additionalProperties"] != false {
			t.Errorf("%s: the schema allows other properties; Load refuses them", at)
		}
		seen := map[string]bool{}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "" || name == "-" || !f.IsExported() {
				continue
			}
			seen[name] = true
			child, ok := props[name].(map[string]any)
			if !ok {
				t.Errorf("%s.%s: in the Go types, not in schema.json", at, name)
				continue
			}
			walk(at+"."+name, f.Type, child)
		}
		for name := range props {
			if !seen[name] {
				t.Errorf("%s.%s: in schema.json, not in the Go types", at, name)
			}
		}
	}
	walk("storyboard", reflect.TypeOf(Storyboard{}), schema)
	shapes := schema["properties"].(map[string]any)["shape"].(map[string]any)["enum"].([]any)
	if len(shapes) != len(Shapes) {
		t.Errorf("schema shapes %v, Go %v", shapes, Shapes)
	}
}
