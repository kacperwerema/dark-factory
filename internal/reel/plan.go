package reel

import (
	"fmt"
	"math"
	"strings"
)

// Plan is what the kit works out from a storyboard: when each scene
// starts and how long it runs, every narrated word on the reel's clock,
// the caption cues and their boxes. Render writes it as the template's
// props (<reel>.plan.json); QA reads it back, so the captions it checks are
// the ones the template drew.
type Plan struct {
	Version     int     `json:"version"`
	Shape       string  `json:"shape"`
	Composition string  `json:"composition,omitempty"`
	Title       string  `json:"title"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	FPS         int     `json:"fps"`
	Duration    float64 `json:"duration"`
	// Frames is Duration at FPS, what the template's composition runs.
	Frames   int          `json:"frames"`
	Brand    Brand        `json:"brand"`
	CTA      CTA          `json:"cta"`
	Captions PlanCaptions `json:"captions"`
	Scenes   []PlanScene  `json:"scenes"`
	Cues     []Cue        `json:"cues"`
	Music    *PlanMusic   `json:"music,omitempty"`
}

// PlanCaptions is the caption style the template draws with.
type PlanCaptions struct {
	Style    string `json:"style"`
	Position string `json:"position"`
	FontSize int    `json:"font_size"`
}

// PlanScene is a scene on the reel's clock.
type PlanScene struct {
	ID        string         `json:"id"`
	Role      string         `json:"role"`
	Start     float64        `json:"start"`
	Duration  float64        `json:"duration"`
	From      int            `json:"from"`
	Frames    int            `json:"frames"`
	Text      string         `json:"text,omitempty"`
	Clips     []PlanClip     `json:"clips,omitempty"`
	Narration *PlanNarration `json:"narration,omitempty"`
}

// PlanClip is a clip; Src is relative to the template's public folder
// once render copied it (Remotion's staticFile()), File the file on disk.
type PlanClip struct {
	Source      string  `json:"source"`
	Src         string  `json:"src"`
	File        string  `json:"file"`
	Start       float64 `json:"start,omitempty"`
	End         float64 `json:"end,omitempty"`
	AIGenerated bool    `json:"ai_generated,omitempty"`
}

// PlanNarration is a scene's narration placed on the reel's clock.
type PlanNarration struct {
	Text  string  `json:"text"`
	Voice string  `json:"voice"`
	File  string  `json:"file"`
	Start float64 `json:"start"`
	// Seconds is the audio file's length.
	Seconds float64 `json:"seconds"`
	Words   []Word  `json:"words"`
}

// PlanMusic is the music bed.
type PlanMusic struct {
	File     string  `json:"file"`
	VolumeDB float64 `json:"volume_db"`
	Duck     bool    `json:"duck"`
}

// Cue is one caption on screen: its words (indexes into the scene's
// narration words), its time on the reel's clock and its box.
type Cue struct {
	Scene string  `json:"scene"`
	Text  string  `json:"text"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	From  int     `json:"from_word"`
	To    int     `json:"to_word"`
	Box   Box     `json:"box"`
}

// Caption layout on the 1080x1920 frame.
const (
	captionX      = 90
	captionW      = 840
	captionPad    = 24
	captionUpperY = 300
	captionLowerB = 520 // a lower box ends this far above the frame's bottom
	tailSeconds   = 0.4 // silence after a narration before the next scene
	defaultFont   = 64
	defaultFPS    = 30
)

// AudioSeconds is how long an audio file is (ffprobe in render and QA, a
// fixed table in tests).
type AudioSeconds func(path string) (float64, error)

// BuildPlan works out the plan. A scene runs its planned duration, or its
// narration plus a short tail when that is longer.
func BuildPlan(s *Storyboard, audio AudioSeconds) (*Plan, error) {
	p := &Plan{
		Version: Version, Shape: s.Shape, Title: s.Title,
		Width: FrameWidth, Height: FrameHeight, FPS: defaultFPS,
		Brand: s.Brand, CTA: s.CTA,
		Captions: PlanCaptions{Style: s.Captions.Style, Position: s.Captions.Position, FontSize: s.Captions.FontSize},
	}
	if p.Captions.Style == "" {
		p.Captions.Style = "words"
	}
	if p.Captions.Position == "" {
		p.Captions.Position = "lower"
	}
	if p.Captions.FontSize == 0 {
		p.Captions.FontSize = defaultFont
	}
	if p.Brand.Logo != "" {
		p.Brand.Logo = s.Resolve(p.Brand.Logo)
	}
	maxWords := s.Captions.MaxWords
	if maxWords == 0 {
		maxWords = 3
		if p.Captions.Style == "lines" {
			maxWords = 8
		}
	}
	start := 0.0
	for _, sc := range s.Scenes {
		ps := PlanScene{ID: sc.ID, Role: sc.Role, Start: start, Duration: sc.Duration, Text: sc.Text}
		for _, c := range sc.Clips {
			ps.Clips = append(ps.Clips, PlanClip{Source: c.Source, File: s.Resolve(c.Path), Src: c.Path, Start: c.Start, End: c.End, AIGenerated: c.AIGenerated})
		}
		if n := sc.Narration; n != nil && n.Audio != "" && n.Alignment != "" {
			file := s.Resolve(n.Audio)
			secs, err := audio(file)
			if err != nil {
				return nil, fmt.Errorf("scene %s: narration audio %s: %w", sc.ID, n.Audio, err)
			}
			words, err := LoadWords(s.Resolve(n.Alignment))
			if err != nil {
				return nil, fmt.Errorf("scene %s: %w", sc.ID, err)
			}
			on := make([]Word, len(words))
			for i, w := range words {
				on[i] = Word{Word: w.Word, Start: round3(start + w.Start), End: round3(start + w.End)}
			}
			ps.Narration = &PlanNarration{Text: n.Text, Voice: n.Voice, File: file, Start: start, Seconds: secs, Words: on}
			if need := secs + tailSeconds; need > ps.Duration {
				ps.Duration = need
			}
			p.Cues = append(p.Cues, cues(sc.ID, on, maxWords, p.Captions, s.Captions.Box)...)
		}
		ps.Duration = round3(ps.Duration)
		ps.From = int(math.Round(start * float64(p.FPS)))
		ps.Frames = int(math.Round(ps.Duration * float64(p.FPS)))
		p.Scenes = append(p.Scenes, ps)
		start = round3(start + ps.Duration)
	}
	p.Duration = start
	p.Frames = int(math.Round(start * float64(p.FPS)))
	if m := s.Music; m != nil {
		vol := m.VolumeDB
		if vol == 0 {
			vol = -18
		}
		p.Music = &PlanMusic{File: s.Resolve(m.Path), VolumeDB: vol, Duck: m.Duck}
	}
	return p, nil
}

// cues groups a scene's words into captions of up to max words, breaking
// after a word that ends a phrase.
func cues(scene string, words []Word, max int, style PlanCaptions, custom *Box) []Cue {
	var out []Cue
	from := 0
	for i := range words {
		end := i == len(words)-1 || i-from+1 >= max || endsPhrase(words[i].Word)
		if !end {
			continue
		}
		var parts []string
		for _, w := range words[from : i+1] {
			parts = append(parts, w.Word)
		}
		text := strings.Join(parts, " ")
		out = append(out, Cue{Scene: scene, Text: text, Start: words[from].Start, End: words[i].End, From: from, To: i, Box: captionBox(text, style, custom)})
		from = i + 1
	}
	return out
}

func endsPhrase(w string) bool {
	return strings.ContainsAny(w[len(w)-1:], ".,!?;:")
}

// captionBox is where a cue's text sits: lines wrap at the box's width
// (an average glyph is about 0.55 of the font size).
func captionBox(text string, style PlanCaptions, custom *Box) Box {
	if style.Position == "custom" && custom != nil {
		return *custom
	}
	fs := float64(style.FontSize)
	perLine := math.Max(1, math.Floor((captionW-2*captionPad)/(fs*0.55)))
	lines := math.Max(1, math.Ceil(float64(len([]rune(text)))/perLine))
	h := math.Round(lines*fs*1.25 + 2*captionPad)
	b := Box{X: captionX, W: captionW, H: h}
	switch style.Position {
	case "upper":
		b.Y = captionUpperY
	case "middle":
		b.Y = math.Round((FrameHeight - h) / 2)
	default:
		b.Y = FrameHeight - captionLowerB - h
	}
	return b
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
