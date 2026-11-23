package reel

import (
	"strings"
	"testing"
)

func TestParseWords(t *testing.T) {
	eleven := `{"audio_base64":"…","alignment":{"characters":["H","i"," ","y","o","u","."],
		"character_start_times_seconds":[0.1,0.2,0.3,0.4,0.5,0.6,0.7],
		"character_end_times_seconds":[0.2,0.3,0.4,0.5,0.6,0.7,0.8]}}`
	for name, src := range map[string]string{
		"elevenlabs response": eleven,
		"alignment alone":     `{"characters":["H","i"," ","y","o","u","."],"character_start_times_seconds":[0.1,0.2,0.3,0.4,0.5,0.6,0.7],"character_end_times_seconds":[0.2,0.3,0.4,0.5,0.6,0.7,0.8]}`,
		"kit list":            `[{"word":"Hi","start":0.1,"end":0.3},{"word":"you.","start":0.4,"end":0.8}]`,
		"kit object":          `{"words":[{"word":"Hi","start":0.1,"end":0.3},{"word":"you.","start":0.4,"end":0.8}]}`,
	} {
		ws, err := ParseWords([]byte(src))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(ws) != 2 || ws[0].Word != "Hi" || ws[0].Start != 0.1 || ws[0].End != 0.3 || ws[1].Word != "you." || ws[1].End != 0.8 {
			t.Errorf("%s: %+v", name, ws)
		}
	}
	for name, src := range map[string]string{
		"mismatched":   `{"characters":["a","b"],"character_start_times_seconds":[0],"character_end_times_seconds":[0.1,0.2]}`,
		"out of order": `[{"word":"b","start":1,"end":1.2},{"word":"a","start":0.2,"end":0.4}]`,
		"empty":        `{}`,
	} {
		if _, err := ParseWords([]byte(src)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestPlanStretchesScenesAndPlacesCues(t *testing.T) {
	m := storyboardFor(ShapeHookList)
	m["scenes"].([]any)[0].(map[string]any)["duration"] = 0.5 // shorter than its narration
	s, err := Load(saveStoryboard(t, t.TempDir(), m))
	if err != nil {
		t.Fatal(err)
	}
	p, err := BuildPlan(s, func(string) (float64, error) { return 1.4, nil })
	if err != nil {
		t.Fatal(err)
	}
	// Scene 0: 1.4 s of audio + the tail stretches 0.5 to 1.8; the rest
	// keep their 2 s.
	if p.Scenes[0].Duration != 1.8 || p.Scenes[1].Start != 1.8 || p.Scenes[1].Duration != 2 || p.Duration != 7.8 {
		t.Fatalf("scenes = %+v, duration %g", p.Scenes, p.Duration)
	}
	if p.Frames != 234 || p.Scenes[1].From != 54 {
		t.Errorf("frames %d, scene 1 from %d", p.Frames, p.Scenes[1].From)
	}
	n := p.Scenes[1].Narration
	if n == nil || n.Words[0].Start != 1.9 || n.Start != 1.8 {
		t.Fatalf("narration = %+v", n)
	}
	// 3 words a cue, so one cue a scene, on the reel's clock, inside the
	// safe zone by default.
	if len(p.Cues) != 4 || p.Cues[1].Text != "One two three." || p.Cues[1].Start != 1.9 || p.Cues[1].End != 3.1 || p.Cues[1].Scene != "a" {
		t.Fatalf("cues = %+v", p.Cues)
	}
	if c := judgeSafeZone(p, DefaultBounds()); !c.OK {
		t.Errorf("default boxes outside the safe zone: %+v", c)
	}
	for _, f := range Formats {
		z := f.SafeZone
		for _, pos := range []string{"upper", "middle", "lower"} {
			b := captionBox(strings.Repeat("word ", 12), PlanCaptions{Position: pos, FontSize: 64}, nil, f)
			if b.Y < z.Top || b.Y+b.H > float64(f.Height)-z.Bottom || b.X < z.Left || b.X+b.W > float64(f.Width)-z.Right {
				t.Errorf("%s: %s box %+v outside the safe zone %+v", f.Name, pos, b, z)
			}
		}
	}
}

func TestCuesBreakAtPhrases(t *testing.T) {
	ws := []Word{{"Hi,", 0, 0.2}, {"one", 0.3, 0.4}, {"two", 0.5, 0.6}, {"three", 0.7, 0.8}, {"four.", 0.9, 1}}
	got := cues("s", ws, 3, PlanCaptions{Position: "lower", FontSize: 64}, nil, Formats[0])
	var texts []string
	for _, c := range got {
		texts = append(texts, c.Text)
	}
	if strings.Join(texts, "|") != "Hi,|one two three|four." {
		t.Errorf("cues = %v", texts)
	}
}

func TestBoundsLayering(t *testing.T) {
	b := DefaultBounds()
	max, lufs := 60.0, -16.0
	b.Apply(&QABounds{MaxSeconds: &max}, "reel.json")
	b.Apply(&QABounds{LUFS: &lufs, SafeZone: &SafeZone{Top: 100, Bottom: 300, Left: 40, Right: 100}}, "storyboard")
	if b.MaxSeconds != 60 || b.LUFS != -16 || b.MinSeconds != 5 || b.SafeZone.Top != 100 {
		t.Fatalf("bounds = %+v", b)
	}
	if b.From["max_seconds"] != "reel.json" || b.From["lufs"] != "storyboard" || b.From["safe_zone"] != "storyboard" || b.From["min_seconds"] != "default" {
		t.Errorf("from = %v", b.From)
	}
	neg := -1.0
	if ps := (&QABounds{LUFSSlack: &neg}).problems("qa", Formats[0]); len(ps) != 1 || ps[0].Path != "qa.lufs_slack" {
		t.Errorf("problems = %v", ps)
	}
}

// A 4:5 storyboard plans a 1080x1350 frame, its caption boxes inside it,
// and the safe-zone check passes them against 4:5's own margins.
func TestFormatFourFivePlansItsOwnFrame(t *testing.T) {
	m := storyboardFor(ShapeHookList)
	m["format"] = "4:5"
	s, err := Load(saveStoryboard(t, t.TempDir(), m))
	if err != nil {
		t.Fatal(err)
	}
	if ps := s.Problems(); len(ps) > 0 {
		t.Fatalf("a 4:5 storyboard has problems: %v", ps)
	}
	p, err := BuildPlan(s, func(string) (float64, error) { return 1.4, nil })
	if err != nil {
		t.Fatal(err)
	}
	if p.Format != "4:5" || p.Width != 1080 || p.Height != 1350 {
		t.Fatalf("plan frame = %s %dx%d, want 4:5 1080x1350", p.Format, p.Width, p.Height)
	}
	for _, q := range p.Cues {
		if q.Box.Y+q.Box.H > 1350 {
			t.Errorf("cue %q box %+v runs off the 1350 px frame", q.Text, q.Box)
		}
	}
	f, _ := LookupFormat("4:5")
	if c := judgeSafeZone(p, DefaultBoundsFor(f)); !c.OK || !strings.Contains(c.Bound, "1080x1350") {
		t.Errorf("4:5 safe zone = %+v", c)
	}
	// A plan written before formats (no format, 1080x1920) is 9:16.
	if f := (&Plan{Width: 1080, Height: 1920}).Frame(); f.Name != "9:16" {
		t.Errorf("old plan frame = %s", f.Name)
	}
}

func TestFormatUnknownAndBoxOffFrameAreProblems(t *testing.T) {
	m := storyboardFor(ShapeHookList)
	m["format"] = "16:9"
	s, err := Load(saveStoryboard(t, t.TempDir(), m))
	if err != nil {
		t.Fatal(err)
	}
	if !hasProblem(s.Problems(), "format") {
		t.Errorf("16:9 is not a problem: %v", s.Problems())
	}
	m = storyboardFor(ShapeHookList)
	m["format"] = "1:1"
	m["captions"] = map[string]any{"position": "custom", "box": map[string]any{"x": 90, "y": 1500, "w": 840, "h": 200}}
	if s, err = Load(saveStoryboard(t, t.TempDir(), m)); err != nil {
		t.Fatal(err)
	}
	if !hasProblem(s.Problems(), "captions.box") {
		t.Errorf("a box below a 1:1 frame is not a problem: %v", s.Problems())
	}
}

func hasProblem(ps []Problem, path string) bool {
	for _, p := range ps {
		if p.Path == path {
			return true
		}
	}
	return false
}
