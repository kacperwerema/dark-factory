package reel

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// plWords is a made-up Polish narration: seven words, one phrase break.
var plWords = []Word{
	{"Ryba?", 0.10, 0.40}, {"Nie.", 0.50, 0.80},
	{"Dziś", 1.00, 1.20}, {"jemy", 1.25, 1.50}, {"zupę", 1.55, 1.90},
	{"z", 1.95, 2.00}, {"dyni.", 2.05, 2.60},
}

func cueTimes(cs []Cue) string {
	var b []string
	for _, c := range cs {
		b = append(b, fmt.Sprintf("%s %.2f-%.2f", c.Text, c.Start, c.End))
	}
	return strings.Join(b, " | ")
}

// AC1: word timings into caption spans, max_words 1 and 3, every span's
// start and end.
func TestCuesMaxWordsSpans(t *testing.T) {
	style := PlanCaptions{Position: "lower", FontSize: 64}
	for _, c := range []struct {
		max  int
		want string
	}{
		{1, "Ryba? 0.10-0.40 | Nie. 0.50-0.80 | Dziś 1.00-1.20 | jemy 1.25-1.50 | zupę 1.55-1.90 | z 1.95-2.00 | dyni. 2.05-2.60"},
		// 3 words a span, broken after a word ending a phrase.
		{3, "Ryba? 0.10-0.40 | Nie. 0.50-0.80 | Dziś jemy zupę 1.00-1.90 | z dyni. 1.95-2.60"},
	} {
		if got := cueTimes(cues("s", plWords, c.max, style, nil, Formats[0])); got != c.want {
			t.Errorf("max_words %d:\n got %s\nwant %s", c.max, got, c.want)
		}
	}
}

// captionedStoryboard is a hook-list reel whose voice is Polish and whose
// captions are English, each scene with its own lines.
func captionedStoryboard(t *testing.T, edit func(m map[string]any)) (*Storyboard, *Plan) {
	t.Helper()
	m := storyboardFor(ShapeHookList)
	m["language"] = "pl"
	m["captions"] = map[string]any{"style": "words", "position": "lower", "language": "en"}
	scenes := m["scenes"].([]any)
	lines := [][]any{
		// spans over the voice words
		{map[string]any{"text": "Fish?", "words": []int{0, 0}}, map[string]any{"text": "No.", "words": []int{1, 1}}, map[string]any{"text": "Pumpkin soup today.", "words": []int{2, 6}}},
		// plain strings share the words by their word counts
		{"Fish? No.", "Today: pumpkin soup."},
		// one span, then a plain line taking the rest
		{map[string]any{"text": "Fish? No.", "words": []int{0, 1}}, "Soup today."},
		// the cta: a line timed by seconds
		{map[string]any{"text": "Follow for more.", "start": 0.2, "end": 1.5}},
	}
	for i, sc := range scenes {
		sc.(map[string]any)["captions"] = lines[i]
	}
	if edit != nil {
		edit(m)
	}
	dir := t.TempDir()
	path := saveStoryboard(t, dir, m)
	pl, _ := json.Marshal(plWords)
	writeFile(t, dir, "w.json", string(pl))
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if ps := s.RenderProblems(); len(ps) > 0 {
		t.Fatalf("problems: %v", ps)
	}
	p, err := BuildPlan(s, func(string) (float64, error) { return 2.7, nil })
	if err != nil {
		t.Fatal(err)
	}
	return s, p
}

// AC2: English lines over a Polish voice, timed to the scenes' words; the
// voice's own words make no cue.
func TestCaptionLinesOverAnotherVoice(t *testing.T) {
	_, p := captionedStoryboard(t, nil)
	if p.Captions.Language != "en" {
		t.Errorf("plan captions language = %q, want en", p.Captions.Language)
	}
	// Scenes run 2.7 + 0.4 = 3.1 s: hook 0, a 3.1, b 6.2, cta 9.3.
	want := strings.Join([]string{
		"Fish? 0.10-0.40", "No. 0.50-0.80", "Pumpkin soup today. 1.00-2.60",
		// 7 words shared 2:3 by word count: 3 and 4.
		"Fish? No. 3.20-4.30", "Today: pumpkin soup. 4.35-5.70",
		"Fish? No. 6.30-7.00", "Soup today. 7.20-8.80",
		"Follow for more. 9.50-10.80",
	}, " | ")
	if got := cueTimes(p.Cues); got != want {
		t.Fatalf("cues:\n got %s\nwant %s", got, want)
	}
	for _, q := range p.Cues {
		if strings.ContainsAny(q.Text, "ęą") || strings.Contains(q.Text, "Ryba") {
			t.Errorf("a voice word in the English captions: %q", q.Text)
		}
	}
	last := p.Cues[len(p.Cues)-1]
	if !last.Timed || last.From != -1 {
		t.Errorf("a seconds-timed line is not marked timed: %+v", last)
	}
	// The drawn plan passes caption-scene and the word-anchored cues
	// caption-sync's cue check.
	if c := judgeInScene(p); !c.OK {
		t.Errorf("caption-scene = %+v", c)
	}
	m := Measured{Probe: Probe{HasVideo: true, HasAudio: true}, Speech: []Span{{0.1, 2.6}, {3.2, 5.7}, {6.3, 8.8}, {9.4, 11.9}}}
	if c := judgeSync(m, p, p, DefaultBounds()); !c.OK {
		t.Errorf("caption-sync = %+v", c)
	}
}

// AC2: QA's caption-scene fails a line that runs past its scene.
func TestCaptionSceneFailsPastScene(t *testing.T) {
	_, p := captionedStoryboard(t, func(m map[string]any) {
		cta := m["scenes"].([]any)[3].(map[string]any)
		cta["captions"] = []any{map[string]any{"text": "Follow for more.", "start": 0.2, "end": 4.0}}
	})
	c := judgeInScene(p)
	if c.OK || !strings.Contains(strings.Join(c.Details, "\n"), `cue "Follow for more." shows 9.50-13.30 s, its scene cta runs 9.30-12.40 s`) {
		t.Fatalf("caption-scene = %+v", c)
	}
	if c.Measured != "1 of 8 captions outside their scene" {
		t.Errorf("measured = %q", c.Measured)
	}
	m, good := goodCase()
	if f := failed(Judge(m, good, good, DefaultBounds())); len(f) > 0 {
		t.Errorf("the good case fails %v", f)
	}
	f := failed(Judge(m, p, p, DefaultBounds()))
	if !strings.Contains(strings.Join(f, ","), CheckInScene) {
		t.Errorf("Judge failed %v, want %s among them", f, CheckInScene)
	}
}

// AC4: a storyboard without caption lines gets the cues it always got.
func TestCuesUnchangedWithoutLines(t *testing.T) {
	s, err := Load(saveStoryboard(t, t.TempDir(), storyboardFor(ShapeHookList)))
	if err != nil {
		t.Fatal(err)
	}
	p, err := BuildPlan(s, func(string) (float64, error) { return 1.4, nil })
	if err != nil {
		t.Fatal(err)
	}
	var want []Cue
	for _, sc := range p.Scenes {
		want = append(want, cues(sc.ID, sc.Narration.Words, 3, p.Captions, nil, Formats[0])...)
	}
	got, _ := json.Marshal(p.Cues)
	exp, _ := json.Marshal(want)
	if string(got) != string(exp) || strings.Contains(string(got), "timed") {
		t.Errorf("cues changed:\n got %s\nwant %s", got, exp)
	}
}

func TestCaptionLineProblems(t *testing.T) {
	// Captions in English over a Polish voice with a scene left without
	// lines: validate names the scene.
	m := storyboardFor(ShapeHookList)
	m["language"] = "pl"
	m["captions"] = map[string]any{"language": "en"}
	m["scenes"].([]any)[0].(map[string]any)["captions"] = []any{
		map[string]any{"text": "x", "words": []int{2, 1}},
		map[string]any{"text": "y", "words": []int{0, 0}, "start": 0, "end": 1},
		map[string]any{"text": "", "start": 1},
	}
	s, err := Load(saveStoryboard(t, t.TempDir(), m))
	if err != nil {
		t.Fatal(err)
	}
	ps := s.Problems()
	for _, path := range []string{"scenes[0].captions[0].words", "scenes[0].captions[1]", "scenes[0].captions[2].text", "scenes[0].captions[2]", "scenes[1].captions", "scenes[3].captions"} {
		if !hasProblem(ps, path) {
			t.Errorf("no problem at %s: %v", path, ps)
		}
	}
	// The same language for both, or none: no lines needed.
	m["captions"] = map[string]any{"language": "PL"}
	delete(m["scenes"].([]any)[0].(map[string]any), "captions")
	if s, err = Load(saveStoryboard(t, t.TempDir(), m)); err != nil {
		t.Fatal(err)
	}
	if ps := s.Problems(); len(ps) > 0 {
		t.Errorf("captions in the voice's language: %v", ps)
	}
	// An unknown field in a line is refused, as elsewhere.
	m["scenes"].([]any)[0].(map[string]any)["captions"] = []any{map[string]any{"text": "x", "from": 1}}
	if _, err := Load(saveStoryboard(t, t.TempDir(), m)); err == nil || !strings.Contains(err.Error(), "from") {
		t.Errorf("unknown field: %v", err)
	}
}

func TestCaptionSpanErrorsAtRender(t *testing.T) {
	for name, c := range map[string]struct {
		lines []any
		want  string
	}{
		"past the words": {[]any{map[string]any{"text": "x", "words": []int{3, 9}}}, "names words 3-9, but the voice has 7 words"},
		"overlap":        {[]any{map[string]any{"text": "a", "words": []int{0, 3}}, map[string]any{"text": "b", "words": []int{2, 4}}}, "before the previous caption's last word 3"},
		"too many lines": {[]any{map[string]any{"text": "a", "words": []int{0, 5}}, "b", "c"}, "2 caption lines (from \"b\") share 1 voice words"},
	} {
		t.Run(name, func(t *testing.T) {
			m := storyboardFor(ShapeHookList)
			m["scenes"].([]any)[1].(map[string]any)["captions"] = c.lines
			dir := t.TempDir()
			path := saveStoryboard(t, dir, m)
			pl, _ := json.Marshal(plWords)
			writeFile(t, dir, "w.json", string(pl))
			s, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			_, err = BuildPlan(s, func(string) (float64, error) { return 2.7, nil })
			if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "scene a") {
				t.Errorf("err = %v, want %q", err, c.want)
			}
		})
	}
}

// A scene with no narration shares its planned duration among plain lines.
func TestCaptionLinesWithoutNarration(t *testing.T) {
	got, err := lineCues("x", []CaptionLine{{Text: "One"}, {Text: "two three"}}, nil, 10, 3, PlanCaptions{Position: "lower", FontSize: 64}, nil, Formats[0])
	if err != nil {
		t.Fatal(err)
	}
	if s := cueTimes(got); s != "One 10.00-11.00 | two three 11.00-13.00" {
		t.Errorf("cues = %s", s)
	}
}

func TestParseWordsDropsV3AudioTags(t *testing.T) {
	chars := []string{"[", "l", "a", "u", "g", "h", "s", "]", " ", "H", "i", " ", "y", "o", "u"}
	var st, en []float64
	for i := range chars {
		st = append(st, float64(i)/10)
		en = append(en, float64(i+1)/10)
	}
	data, _ := json.Marshal(map[string]any{"characters": chars, "character_start_times_seconds": st, "character_end_times_seconds": en})
	ws, err := ParseWords(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 2 || ws[0].Word != "Hi" || ws[0].Start != 0.9 || ws[1].Word != "you" {
		t.Errorf("words = %+v", ws)
	}
}
