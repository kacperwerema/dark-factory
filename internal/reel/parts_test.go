package reel

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// partsStoryboard is a hook-list using every part: a label over the reel,
// a hook of three trimmed clips with a stamp on each "no", an item with an
// app recording over an ai clip (the phone), in format f.
func partsStoryboard(f string) map[string]any {
	m := storyboardFor(ShapeHookList)
	m["format"] = f
	m["label"] = "Acted scene, AI voice and video"
	m["brand"].(map[string]any)["colors"].(map[string]any)["accent"] = "#E63946"
	sc := m["scenes"].([]any)
	hook := sc[0].(map[string]any)
	hook["narration"].(map[string]any)["alignment"] = "no.json"
	hook["clips"] = []any{
		map[string]any{"source": "ai", "path": "media/ai.mp4", "ai_generated": true, "tool": "kling", "start": 0, "end": 0.5},
		map[string]any{"source": "ai", "path": "media/ai.mp4", "ai_generated": true, "tool": "kling", "start": 1, "end": 1.5},
		map[string]any{"source": "ai", "path": "media/ai.mp4", "ai_generated": true, "tool": "kling", "start": 2, "end": 3},
	}
	hook["stamp"] = map[string]any{"text": "NO"}
	sc[1].(map[string]any)["clips"] = []any{
		map[string]any{"source": "app-recording", "path": "media/app.mp4"},
		map[string]any{"source": "ai", "path": "media/ai.mp4", "ai_generated": true, "tool": "kling"},
	}
	return m
}

// savePartsStoryboard writes partsStoryboard with the hook's word timings:
// "Fish, no. Carrots, no. Spinach, no."
func savePartsStoryboard(t *testing.T, dir, f string) string {
	t.Helper()
	writeFile(t, dir, "no.json", `[{"word":"Fish,","start":0.1,"end":0.3},{"word":"no.","start":0.35,"end":0.5},`+
		`{"word":"Carrots,","start":0.6,"end":0.8},{"word":"no.","start":0.85,"end":1.0},`+
		`{"word":"Spinach,","start":1.1,"end":1.3},{"word":"No!","start":1.35,"end":1.5}]`)
	return saveStoryboard(t, dir, partsStoryboard(f))
}

func TestPlanPartsClipsStampsLabel(t *testing.T) {
	for _, f := range Formats {
		t.Run(f.Name, func(t *testing.T) {
			s, err := Load(savePartsStoryboard(t, t.TempDir(), f.Name))
			if err != nil {
				t.Fatal(err)
			}
			if ps := s.RenderProblems(); len(ps) > 0 {
				t.Fatalf("problems: %v", ps)
			}
			p, err := BuildPlan(s, func(string) (float64, error) { return 1.6, nil })
			if err != nil {
				t.Fatal(err)
			}
			hook := p.Scenes[0] // 1.6 s + 0.4 s tail = 2 s, 60 frames
			if hook.Frames != 60 {
				t.Fatalf("hook frames %d", hook.Frames)
			}
			// Clips: trimmed 0.5, 0.5 and 1 s share 60 frames by length;
			// 0.5 s is 15 frames, so each plays at its speed.
			var got []int
			for _, c := range hook.Clips {
				got = append(got, c.From, c.Frames)
				if c.Rate != 1 {
					t.Errorf("clip at %d: rate %g, want 1", c.From, c.Rate)
				}
			}
			if !reflect.DeepEqual(got, []int{0, 15, 15, 15, 30, 30}) {
				t.Errorf("hook clips from/frames = %v, want 0 15, 15 15, 30 30", got)
			}
			// Untrimmed clips share evenly; the phone's backdrop is one of them.
			item := p.Scenes[1]
			if len(item.Clips) != 2 || item.Clips[0].Frames+item.Clips[1].Frames != item.Frames || item.Clips[1].From != item.Clips[0].Frames {
				t.Errorf("item clips = %+v (scene %d frames)", item.Clips, item.Frames)
			}
			// Stamps: three "no"s for three cuts, each on its word.
			if len(hook.Stamps) != 3 {
				t.Fatalf("stamps = %+v", hook.Stamps)
			}
			for i, want := range []int{11, 26, 41} { // 0.35, 0.85, 1.35 s at 30 fps
				st := hook.Stamps[i]
				if st.From != want || st.Text != "NO" || st.Start != round3(float64(want)/30) {
					t.Errorf("stamp %d = %+v, want from %d", i, st, want)
				}
			}
			if last := hook.Stamps[2]; last.From+last.Frames != hook.Frames {
				t.Errorf("last stamp ends at %d, scene at %d", last.From+last.Frames, hook.Frames)
			}
			if p.Label == nil || p.Label.Text != "Acted scene, AI voice and video" || p.Label.Box.W <= 0 {
				t.Fatalf("label = %+v", p.Label)
			}
			// The phone fits the frame; the label and stamps pass safe-zone.
			ph := p.Layout.Phone
			if ph.X < 0 || ph.Y < 0 || ph.X+ph.W > float64(f.Width) || ph.Y+ph.H > float64(f.Height) || ph.Y < p.Label.Box.Y+p.Label.Box.H {
				t.Errorf("phone %+v off the %s frame or over the label %+v", ph, f.Size(), p.Label.Box)
			}
			c := judgeSafeZone(p, DefaultBoundsFor(f))
			if !c.OK || !strings.Contains(c.Measured, "1 label, 3 stamp") {
				t.Errorf("safe-zone = %+v", c)
			}
		})
	}
}

// A stamp whose word the narration doesn't say once per cut lands a few
// frames into each cut; a scene with no clips gets one.
func TestStampsFallBackToCuts(t *testing.T) {
	m := partsStoryboard("9:16")
	m["scenes"].([]any)[0].(map[string]any)["stamp"] = map[string]any{"text": "NO", "on": "never"}
	m["scenes"].([]any)[2].(map[string]any)["stamp"] = map[string]any{"text": "YES"}
	dir := t.TempDir()
	writeFile(t, dir, "no.json", `[{"word":"no","start":0.1,"end":0.3}]`)
	s, err := Load(saveStoryboard(t, dir, m))
	if err != nil {
		t.Fatal(err)
	}
	p, err := BuildPlan(s, func(string) (float64, error) { return 1.6, nil })
	if err != nil {
		t.Fatal(err)
	}
	var from []int
	for _, st := range p.Scenes[0].Stamps {
		from = append(from, st.From)
	}
	if !reflect.DeepEqual(from, []int{4, 19, 34}) {
		t.Errorf("stamps from %v, want 4 frames into each cut (4 19 34)", from)
	}
	if st := p.Scenes[2].Stamps; len(st) != 1 || st[0].From != 4 || st[0].From+st[0].Frames != p.Scenes[2].Frames {
		t.Errorf("a clipless scene's stamps = %+v", st)
	}
}

// A short trimmed clip in a long share plays slower, never below 0.75.
func TestShortClipsPlaySlowerNotFrozen(t *testing.T) {
	clips := []PlanClip{{}, {}}
	splitClips(clips, []float64{1, 1}, 90, 30) // 1 s each in 1.5 s shares
	if clips[0].Rate != 0.75 || clips[0].Frames != 45 {
		t.Errorf("1 s in 45 frames = %+v, want rate 0.75", clips[0])
	}
	splitClips(clips, []float64{1.3, 1.3}, 90, 30)
	if clips[1].Rate != 0.867 {
		t.Errorf("1.3 s in 45 frames = rate %g, want 0.867", clips[1].Rate)
	}
}

func TestPartsProblems(t *testing.T) {
	for _, c := range []struct {
		name, path string
		edit       func(m map[string]any)
	}{
		{"stamp on cta", "scenes[3].stamp", func(m map[string]any) {
			m["scenes"].([]any)[3].(map[string]any)["stamp"] = map[string]any{"text": "NO"}
		}},
		{"blank stamp", "scenes[0].stamp.text", func(m map[string]any) {
			m["scenes"].([]any)[0].(map[string]any)["stamp"] = map[string]any{"text": " "}
		}},
		{"stamp too wide", "scenes[0].stamp.text", func(m map[string]any) {
			m["scenes"].([]any)[0].(map[string]any)["stamp"] = map[string]any{"text": "NEVER EVER"}
		}},
		{"label too wide", "label", func(m map[string]any) {
			m["label"] = strings.Repeat("Acted scene, AI voice and video. ", 3)
		}},
		{"blank label", "label", func(m map[string]any) { m["label"] = "  " }},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := partsStoryboard("4:5")
			c.edit(m)
			dir := t.TempDir()
			writeFile(t, dir, "no.json", `[{"word":"no","start":0.1,"end":0.3}]`)
			s, err := Load(saveStoryboard(t, dir, m))
			if err != nil {
				t.Fatal(err)
			}
			if !hasProblem(s.Problems(), c.path) {
				t.Errorf("no problem at %s: %v", c.path, s.Problems())
			}
		})
	}
}

// QA's safe-zone names a label or stamp the platforms' UI would cover.
func TestSafeZoneChecksLabelAndStamps(t *testing.T) {
	_, p := goodCase()
	p.Label = &PlanLabel{Text: "AI video", Box: Box{X: 70, Y: 120, W: 200, H: 61}}
	p.Scenes[1].Stamps = []PlanStamp{{Text: "NO", Start: 4.1, Box: stampBox("NO", Formats[0])}}
	if c := judgeSafeZone(p, DefaultBounds()); c.OK || !strings.Contains(strings.Join(c.Details, "\n"), `label "AI video"`) || !strings.Contains(c.Measured, "1 of 4 boxes") {
		t.Errorf("a label under the top bar = %+v", c)
	}
	p.Label = nil
	p.Scenes[1].Stamps[0].Box.X = 900
	if c := judgeSafeZone(p, DefaultBounds()); c.OK || !strings.Contains(strings.Join(c.Details, "\n"), `stamp "NO" in scene b`) {
		t.Errorf("a stamp under the right column = %+v", c)
	}
}

// A 9:16 storyboard without the new parts plans as it did before them:
// the same frame, scenes, clips, cues and boxes as main's plan (golden,
// written by the code before this change); the plan only gains the layout
// and each clip's share.
func TestPlanNineSixteenUnchanged(t *testing.T) {
	m := storyboardFor(ShapeHookList)
	sc := m["scenes"].([]any)
	sc[1].(map[string]any)["clips"] = []any{map[string]any{"source": "owner", "path": "media/stock.mp4", "start": 1, "end": 2.5}}
	sc[2].(map[string]any)["clips"] = []any{map[string]any{"source": "ai", "path": "media/ai.mp4", "ai_generated": true, "tool": "kling"}}
	s, err := Load(saveStoryboard(t, t.TempDir(), m))
	if err != nil {
		t.Fatal(err)
	}
	p, err := BuildPlan(s, func(string) (float64, error) { return 1.4, nil })
	if err != nil {
		t.Fatal(err)
	}
	if p.Label != nil {
		t.Errorf("label = %+v", p.Label)
	}
	for i := range p.Scenes {
		if len(p.Scenes[i].Stamps) > 0 {
			t.Errorf("scene %s stamps = %+v", p.Scenes[i].ID, p.Scenes[i].Stamps)
		}
		for j := range p.Scenes[i].Clips {
			p.Scenes[i].Clips[j].File = ""
		}
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var now, before map[string]any
	_ = json.Unmarshal([]byte(strings.ReplaceAll(string(data), s.Dir()+string(os.PathSeparator), "")), &now)
	golden, err := os.ReadFile("testdata/plan-9x16-before-parts.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(golden, &before); err != nil {
		t.Fatal(err)
	}
	delete(now, "layout")
	for _, s := range now["scenes"].([]any) {
		clips, _ := s.(map[string]any)["clips"].([]any)
		for _, c := range clips {
			for _, k := range []string{"from", "frames", "rate"} {
				delete(c.(map[string]any), k)
			}
		}
	}
	if !reflect.DeepEqual(now, before) {
		a, _ := json.MarshalIndent(now, "", "  ")
		t.Errorf("the 9:16 plan changed:\n%s\nwant testdata/plan-9x16-before-parts.json", a)
	}
}
