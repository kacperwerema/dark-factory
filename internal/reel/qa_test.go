package reel

import (
	"math"
	"strings"
	"testing"
)

func TestParsers(t *testing.T) {
	p, err := ParseProbe([]byte(`{"streams":[{"codec_type":"video","codec_name":"h264","width":1080,"height":1920,"sample_aspect_ratio":"1:1","start_time":"0.000000"},
		{"codec_type":"audio","codec_name":"aac","start_time":"0.021000"}],"format":{"duration":"12.500000"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !p.HasVideo || !p.HasAudio || p.Duration != 12.5 || p.Aspect() != 0.5625 || p.AudioStart != 0.021 || p.VideoCodec != "h264" {
		t.Errorf("probe = %+v", p)
	}
	// Anamorphic: 1440x1920 stored, SAR 3:4, displays as 1080x1920.
	q, _ := ParseProbe([]byte(`{"streams":[{"codec_type":"video","width":1440,"height":1920,"sample_aspect_ratio":"3:4"}],"format":{"duration":"1"}}`))
	if math.Abs(q.Aspect()-0.5625) > 1e-9 {
		t.Errorf("anamorphic aspect = %g", q.Aspect())
	}

	l, err := ParseLoudness("[Parsed_ebur128_0 @ 0x1] Summary:\n\n  Integrated loudness:\n    I:         -14.2 LUFS\n    Threshold: -24.3 LUFS\n")
	if err != nil || l != -14.2 {
		t.Errorf("loudness = %g, %v", l, err)
	}
	if l, _ := ParseLoudness("Summary:\n    I:         -inf LUFS\n"); !math.IsInf(l, -1) {
		t.Errorf("silence = %g", l)
	}
	if _, err := ParseLoudness("nothing"); err == nil {
		t.Error("no summary: no error")
	}

	black := ParseBlack("[blackdetect @ 0x1] black_start:2 black_end:2.6 black_duration:0.6\n[blackdetect @ 0x1] black_start:5.1 black_end:5.2 black_duration:0.1\n")
	if len(black) != 2 || black[0] != (Span{2, 2.6}) {
		t.Errorf("black = %+v", black)
	}

	sp := ParseSpeech("[silencedetect @ 0x1] silence_start: 0\n[silencedetect @ 0x1] silence_end: 0.1 | silence_duration: 0.1\n[silencedetect @ 0x1] silence_start: 1.3\n[silencedetect @ 0x1] silence_end: 1.9 | silence_duration: 0.6\n", 3)
	if len(sp) != 2 || sp[0] != (Span{0.1, 1.3}) || sp[1] != (Span{1.9, 3}) {
		t.Fatalf("speech = %+v", sp)
	}
	if on := Onset(sp, 1.8, 0.3); on != 1.9 {
		t.Errorf("onset after 1.8 = %g", on)
	}
	if on := Onset(sp, 1.0, 0.3); on != 0.7 {
		t.Errorf("onset inside speech = %g (sound already playing reads as early)", on)
	}
	if on := Onset(sp[:1], 2, 0.3); on != -1 {
		t.Errorf("no speech after = %g", on)
	}
}

// goodCase is a plan of two narrated scenes and measurements that pass.
func goodCase() (Measured, *Plan) {
	words := func(at float64) []Word { return []Word{{"One", at + 0.1, at + 0.4}, {"two.", at + 0.45, at + 0.9}} }
	p := &Plan{Duration: 8, Scenes: []PlanScene{
		{ID: "a", Start: 0, Duration: 4, Narration: &PlanNarration{Words: words(0)}},
		{ID: "b", Start: 4, Duration: 4, Narration: &PlanNarration{Words: words(4)}},
	}}
	box := captionBox("One two.", PlanCaptions{Position: "lower", FontSize: 64}, nil, Formats[0])
	p.Cues = []Cue{{Scene: "a", Text: "One two.", Start: 0.1, End: 0.9, From: 0, To: 1, Box: box}, {Scene: "b", Text: "One two.", Start: 4.1, End: 4.9, From: 0, To: 1, Box: box}}
	m := Measured{
		Probe:    Probe{Duration: 8.01, Width: 1080, Height: 1920, SARNum: 1, SARDen: 1, HasVideo: true, HasAudio: true},
		Loudness: -14.3,
		Speech:   []Span{{0.1, 0.9}, {4.12, 4.9}},
	}
	return m, p
}

func failed(cs []Check) []string {
	var out []string
	for _, c := range cs {
		if !c.OK {
			out = append(out, c.Name)
		}
	}
	return out
}

func TestJudgeEachFail(t *testing.T) {
	b := DefaultBounds()
	m, p := goodCase()
	if f := failed(Judge(m, p, p, b)); len(f) > 0 {
		t.Fatalf("good case fails %v: %+v", f, Judge(m, p, p, b))
	}
	shift := func(p *Plan, d float64) *Plan {
		q := *p
		q.Cues = append([]Cue(nil), p.Cues...)
		q.Cues[1].Start += d
		return &q
	}
	box := func(p *Plan, y float64) *Plan {
		q := *p
		q.Cues = append([]Cue(nil), p.Cues...)
		q.Cues[0].Box.Y = y
		return &q
	}
	for _, c := range []struct {
		name   string
		edit   func(*Measured)
		drawn  *Plan
		want   string
		detail string
	}{
		{"too short", func(m *Measured) { m.Probe.Duration = 4 }, nil, CheckDuration, "outside 5-90 s"},
		{"off the plan", func(m *Measured) { m.Probe.Duration = 9.2 }, nil, CheckDuration, "+1.20 s off the storyboard's plan"},
		{"square", func(m *Measured) { m.Probe.Height = 1080 }, nil, CheckAspect, "format is 9:16 (1080x1920)"},
		{"loud", func(m *Measured) { m.Loudness = -11.5 }, nil, CheckLoudness, "+2.5 LU off -14"},
		{"quiet", func(m *Measured) { m.Loudness = -16.5 }, nil, CheckLoudness, "-2.5 LU off -14"},
		{"silent", func(m *Measured) { m.Loudness = math.Inf(-1) }, nil, CheckLoudness, "no sound"},
		{"black", func(m *Measured) { m.Black = []Span{{2, 2.5}} }, nil, CheckBlack, "black 2.00-2.50 s"},
		{"under the bottom bar", nil, box(p, 1600), CheckSafeZone, "into the bottom 420"},
		{"under the top bar", nil, box(p, 100), CheckSafeZone, "into the top 220"},
		{"cue late", nil, shift(p, 0.4), CheckSync, "(+0.40 s)"},
		{"audio offset", func(m *Measured) { m.Speech = []Span{{0.6, 1.4}, {4.6, 5.4}} }, nil, CheckSync, "speech starts at 0.60 s, the first word at 0.10 s (+0.50 s)"},
		{"encode offset", func(m *Measured) { m.Probe.AudioStart = 0.4 }, nil, CheckSync, "audio starts +0.40 s from the video"},
		{"no speech", func(m *Measured) { m.Speech = []Span{{0.1, 0.9}} }, nil, CheckSync, "scene b: no speech after 4.00 s"},
	} {
		t.Run(c.name, func(t *testing.T) {
			mm, pp := goodCase()
			if c.edit != nil {
				c.edit(&mm)
			}
			drawn := pp
			if c.drawn != nil {
				drawn = c.drawn
			}
			cs := Judge(mm, drawn, pp, b)
			f := failed(cs)
			if len(f) != 1 || f[0] != c.want {
				t.Fatalf("failed %v, want [%s]", f, c.want)
			}
			for _, ch := range cs {
				if ch.Name == c.want && !strings.Contains(strings.Join(ch.Details, "\n"), c.detail) {
					t.Errorf("details %q lack %q", ch.Details, c.detail)
				}
				if ch.Measured == "" || ch.Bound == "" {
					t.Errorf("%s: measured %q, bound %q", ch.Name, ch.Measured, ch.Bound)
				}
			}
		})
	}
}

func TestNarratedReelWithoutCuesFails(t *testing.T) {
	m, p := goodCase()
	q := *p
	q.Cues = nil
	if c := judgeSafeZone(&q, DefaultBounds()); c.OK || !strings.Contains(c.Measured, "no caption cues") {
		t.Errorf("no cues = %+v", c)
	}
	_ = m
}

func TestReportSummaryAndMarkdown(t *testing.T) {
	m, p := goodCase()
	m.Loudness = -10
	b := DefaultBounds()
	lufs := -14.0
	b.Apply(&QABounds{LUFS: &lufs}, "storyboard")
	r := &Report{File: "/x/reel.mp4", Checks: Judge(m, p, p, b), Bounds: b}
	r.Pass = len(r.Failed()) == 0
	s := r.Summary()
	for _, want := range []string{"QA: FAIL (1 of 7 checks)", "✗ loudness: -10.0 LUFS integrated (bound: -14 LUFS ±2)", "    +4.0 LU off -14", "✓ duration:"} {
		if !strings.Contains(s, want) {
			t.Errorf("summary lacks %q:\n%s", want, s)
		}
	}
	md := r.Markdown()
	for _, want := range []string{"## Reel QA: FAIL", "| loudness | **fail** | -10.0 LUFS integrated | -14 LUFS ±2 |", "reel.mp4,", "lufs (storyboard)"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
}

// The aspect check holds the file to the storyboard's format: a 1080x1350
// file passes a 4:5 plan and fails a 9:16 one, and the other way round.
func TestFormatAspectFollowsThePlan(t *testing.T) {
	m, p := goodCase()
	b := DefaultBounds()
	four := *p
	four.Format, four.Width, four.Height = "4:5", 1080, 1350
	four.Cues = append([]Cue(nil), p.Cues...)
	for i := range four.Cues {
		four.Cues[i].Box = captionBox(four.Cues[i].Text, PlanCaptions{Position: "lower", FontSize: 64}, nil, four.Frame())
	}
	post := m
	post.Probe.Width, post.Probe.Height = 1080, 1350
	if c := judgeAspect(post, four.Frame()); !c.OK {
		t.Errorf("1080x1350 against 4:5: %+v", c)
	}
	if f := failed(Judge(post, &four, &four, DefaultBoundsFor(four.Frame()))); len(f) > 0 {
		t.Errorf("a 4:5 post fails %v", f)
	}
	if f := failed(Judge(post, p, p, b)); len(f) != 1 || f[0] != CheckAspect {
		t.Errorf("1080x1350 against a 9:16 plan fails %v, want aspect", f)
	}
	if c := judgeAspect(m, four.Frame()); c.OK || !strings.Contains(c.Bound, "4:5") || !strings.Contains(strings.Join(c.Details, " "), "1080x1350") {
		t.Errorf("1080x1920 against 4:5: %+v", c)
	}
}
