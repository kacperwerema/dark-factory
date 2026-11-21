// Package reel is the reel kit: a storyboard JSON a project's agent fills,
// the plan the kit works out from it (scene starts, word timings, caption
// cues and their boxes), the render that fills a project's Remotion
// template and encodes the MP4 with FFmpeg, and the QA that fails a reel
// before it reaches the owner's shelf. issue-cli reel is its command
// (cmd/issue-cli/cmd_reel.go); the starter template is
// cmd/issue-cli/templates/reel; docs/reels.md is the page.
//
// The kit never calls a TTS or video-generation tool: the storyboard names
// the files the connectors wrote, so a render spends no money.
package reel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Version is the storyboard format the kit reads.
const Version = 1

// The shapes: one composition each in the starter template.
const (
	ShapeHookList    = "hook-list"
	ShapeBeforeAfter = "before-after"
	ShapeAppDemo     = "app-demo"
)

// Shapes are the shapes in order, for help texts and the schema.
var Shapes = []string{ShapeHookList, ShapeBeforeAfter, ShapeAppDemo}

// Clip sources.
const (
	SourceAppRecording = "app-recording"
	SourceOwner        = "owner"
	SourceStock        = "stock"
	SourceAI           = "ai"
)

// Sources are the clip sources in order.
var Sources = []string{SourceAppRecording, SourceOwner, SourceStock, SourceAI}

// Caption positions; custom takes captions.box.
var Positions = []string{"lower", "middle", "upper", "custom"}

// Storyboard is one reel: what each scene shows and says, the captions,
// the music, the call to action and the brand.
type Storyboard struct {
	Version  int       `json:"version"`
	Shape    string    `json:"shape"`
	Title    string    `json:"title"`
	Language string    `json:"language,omitempty"`
	Brand    Brand     `json:"brand"`
	Scenes   []Scene   `json:"scenes"`
	Captions Captions  `json:"captions"`
	Music    *Music    `json:"music,omitempty"`
	CTA      CTA       `json:"cta"`
	QA       *QABounds `json:"qa,omitempty"`

	// dir is the storyboard's folder: paths in it are relative to it.
	dir string
}

// Brand is how the reel looks.
type Brand struct {
	Name   string `json:"name"`
	Colors Colors `json:"colors"`
	Font   string `json:"font,omitempty"`
	Logo   string `json:"logo,omitempty"`
}

// Colors are CSS colours.
type Colors struct {
	Primary    string `json:"primary"`
	Accent     string `json:"accent,omitempty"`
	Text       string `json:"text"`
	Background string `json:"background"`
}

// Scene is one beat of the reel.
type Scene struct {
	ID   string `json:"id"`
	Role string `json:"role"`
	// Duration is the planned length in seconds; narration longer than it
	// stretches the scene.
	Duration  float64    `json:"duration"`
	Text      string     `json:"text,omitempty"`
	Narration *Narration `json:"narration,omitempty"`
	Clips     []Clip     `json:"clips,omitempty"`
}

// Narration is what a scene says. Voice is a placement name the project's
// TTS connector resolves to a voice; Audio and Alignment are the files
// that connector wrote (the kit never calls it).
type Narration struct {
	Text  string `json:"text"`
	Voice string `json:"voice"`
	Audio string `json:"audio,omitempty"`
	// Alignment is the word timings: ElevenLabs' with-timestamps JSON
	// (characters and their start and end seconds) or [{word,start,end}].
	Alignment string `json:"alignment,omitempty"`
}

// Clip is footage a scene shows.
type Clip struct {
	Source string `json:"source"`
	Path   string `json:"path"`
	// Start and End trim the clip (seconds into it); 0 and 0 is all of it.
	Start       float64 `json:"start,omitempty"`
	End         float64 `json:"end,omitempty"`
	AIGenerated bool    `json:"ai_generated,omitempty"`
	Prompt      string  `json:"prompt,omitempty"`
	Tool        string  `json:"tool,omitempty"`
	Licence     string  `json:"licence,omitempty"`
}

// Captions are how the narration's words show on screen.
type Captions struct {
	// Style: words (a few words at a time) or lines (a sentence at a time).
	Style    string `json:"style,omitempty"`
	Position string `json:"position,omitempty"`
	MaxWords int    `json:"max_words,omitempty"`
	FontSize int    `json:"font_size,omitempty"`
	// Box is the caption box on a 1080x1920 frame, for position custom.
	Box *Box `json:"box,omitempty"`
}

// Box is a rectangle in pixels on the 1080x1920 frame.
type Box struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// Music is the bed under the narration.
type Music struct {
	Path     string  `json:"path"`
	Licence  Licence `json:"licence"`
	VolumeDB float64 `json:"volume_db,omitempty"`
	// Duck lowers the music under the narration.
	Duck bool `json:"duck,omitempty"`
}

// Licence says where a track comes from and under what terms.
type Licence struct {
	Source string `json:"source"`
	Ref    string `json:"ref"`
}

// CTA is the call to action the cta scene shows.
type CTA struct {
	Text   string `json:"text"`
	URL    string `json:"url,omitempty"`
	Handle string `json:"handle,omitempty"`
}

// shapeRoles are the roles each shape wants, in order: a role with min 0
// may be left out.
var shapeRoles = map[string][]roleRule{
	ShapeHookList:    {{"hook", 1, 1}, {"item", 2, 6}, {"cta", 1, 1}},
	ShapeBeforeAfter: {{"hook", 1, 1}, {"before", 1, 1}, {"after", 1, 1}, {"cta", 1, 1}},
	ShapeAppDemo:     {{"hook", 1, 1}, {"step", 1, 6}, {"cta", 1, 1}},
}

type roleRule struct {
	role     string
	min, max int
}

// Problem is one thing wrong with a storyboard, at its JSON path.
type Problem struct {
	Path string `json:"path"`
	Msg  string `json:"msg"`
}

func (p Problem) String() string { return p.Path + ": " + p.Msg }

// Load reads a storyboard; unknown fields are refused, so a typo is not a
// silent default.
func Load(path string) (*Storyboard, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var s Storyboard
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("%s: %w (issue-cli reel schema prints the format)", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	s.dir = filepath.Dir(abs)
	return &s, nil
}

// Dir is the folder the storyboard's paths are relative to.
func (s *Storyboard) Dir() string { return s.dir }

// Resolve is a storyboard path as a path on disk.
func (s *Storyboard) Resolve(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(s.dir, filepath.FromSlash(p))
}

// Problems lists everything wrong with the storyboard, in document order.
// render also wants every narration's audio and alignment (RenderProblems).
func (s *Storyboard) Problems() []Problem {
	var ps []Problem
	add := func(path, format string, a ...any) { ps = append(ps, Problem{path, fmt.Sprintf(format, a...)}) }
	if s.Version != Version {
		add("version", "want %d, got %d", Version, s.Version)
	}
	rules, known := shapeRoles[s.Shape]
	if !known {
		add("shape", "want one of %s, got %q", strings.Join(Shapes, ", "), s.Shape)
	}
	if strings.TrimSpace(s.Title) == "" {
		add("title", "missing")
	}
	if strings.TrimSpace(s.Brand.Name) == "" {
		add("brand.name", "missing")
	}
	for _, c := range []struct{ k, v string }{{"primary", s.Brand.Colors.Primary}, {"text", s.Brand.Colors.Text}, {"background", s.Brand.Colors.Background}} {
		if strings.TrimSpace(c.v) == "" {
			add("brand.colors."+c.k, "missing")
		}
	}
	s.fileProblem(&ps, "brand.logo", s.Brand.Logo)
	if len(s.Scenes) == 0 {
		add("scenes", "no scenes")
	}
	ids := map[string]bool{}
	counts := map[string]int{}
	for i, sc := range s.Scenes {
		at := fmt.Sprintf("scenes[%d]", i)
		if sc.ID == "" {
			add(at+".id", "missing")
		} else if ids[sc.ID] {
			add(at+".id", "%q is used twice", sc.ID)
		}
		ids[sc.ID] = true
		counts[sc.Role]++
		if known && !hasRole(rules, sc.Role) {
			add(at+".role", "%s wants roles %s, got %q", s.Shape, roleNames(rules), sc.Role)
		}
		if sc.Duration <= 0 {
			add(at+".duration", "want seconds above 0, got %g", sc.Duration)
		}
		if n := sc.Narration; n != nil {
			if strings.TrimSpace(n.Text) == "" {
				add(at+".narration.text", "missing")
			}
			if strings.TrimSpace(n.Voice) == "" {
				add(at+".narration.voice", "missing: a placement name from the TTS connector's config (e.g. narrator)")
			}
			s.fileProblem(&ps, at+".narration.audio", n.Audio)
			s.fileProblem(&ps, at+".narration.alignment", n.Alignment)
		}
		if s.Shape == ShapeAppDemo && sc.Role == "step" && !hasSource(sc.Clips, SourceAppRecording) {
			add(at+".clips", "an app-demo step shows the app: add a clip with source app-recording")
		}
		for j, c := range sc.Clips {
			cat := fmt.Sprintf("%s.clips[%d]", at, j)
			if !contains(Sources, c.Source) {
				add(cat+".source", "want one of %s, got %q", strings.Join(Sources, ", "), c.Source)
			}
			if c.Path == "" {
				add(cat+".path", "missing")
			} else {
				s.fileProblem(&ps, cat+".path", c.Path)
			}
			if c.End != 0 && c.End <= c.Start {
				add(cat+".end", "want above start (%g), got %g", c.Start, c.End)
			}
			switch c.Source {
			case SourceAI:
				if !c.AIGenerated {
					add(cat+".ai_generated", "an ai clip must say ai_generated: true (the AI-content label reads it)")
				}
				if c.Tool == "" {
					add(cat+".tool", "an ai clip names the tool that made it (e.g. kling)")
				}
			case SourceStock:
				if strings.TrimSpace(c.Licence) == "" {
					add(cat+".licence", "a stock clip names its licence")
				}
			}
		}
	}
	if known {
		for _, r := range rules {
			if n := counts[r.role]; n < r.min || n > r.max {
				add("scenes", "%s wants %s role %q, got %d", s.Shape, span(r), r.role, n)
			}
		}
	}
	c := s.Captions
	if c.Style != "" && c.Style != "words" && c.Style != "lines" {
		add("captions.style", "want words or lines, got %q", c.Style)
	}
	if c.Position != "" && !contains(Positions, c.Position) {
		add("captions.position", "want one of %s, got %q", strings.Join(Positions, ", "), c.Position)
	}
	if c.Position == "custom" && c.Box == nil {
		add("captions.box", "position custom wants a box {x, y, w, h} on the 1080x1920 frame")
	}
	if c.MaxWords < 0 || c.FontSize < 0 {
		add("captions", "max_words and font_size are 0 (the default) or above")
	}
	if m := s.Music; m != nil {
		if m.Path == "" {
			add("music.path", "missing")
		} else {
			s.fileProblem(&ps, "music.path", m.Path)
		}
		if strings.TrimSpace(m.Licence.Source) == "" || strings.TrimSpace(m.Licence.Ref) == "" {
			add("music.licence", "licensed music only: name its source and ref (a licence id or URL)")
		}
	}
	if strings.TrimSpace(s.CTA.Text) == "" {
		add("cta.text", "missing")
	}
	if s.QA != nil {
		ps = append(ps, s.QA.problems("qa")...)
	}
	return ps
}

// RenderProblems are Problems plus what render needs on top: each
// narration's audio and word timings.
func (s *Storyboard) RenderProblems() []Problem {
	ps := s.Problems()
	for i, sc := range s.Scenes {
		if sc.Narration == nil {
			continue
		}
		at := fmt.Sprintf("scenes[%d].narration", i)
		if sc.Narration.Audio == "" {
			ps = append(ps, Problem{at + ".audio", "missing: make it with the project's TTS connector, then name the file here"})
		}
		if sc.Narration.Alignment == "" {
			ps = append(ps, Problem{at + ".alignment", "missing: the TTS call's word timings (ElevenLabs with-timestamps JSON, or [{word,start,end}])"})
		}
	}
	return ps
}

func (s *Storyboard) fileProblem(ps *[]Problem, path, p string) {
	if p == "" {
		return
	}
	if _, err := os.Stat(s.Resolve(p)); err != nil {
		*ps = append(*ps, Problem{path, fmt.Sprintf("no file %s (paths are relative to the storyboard's folder)", p)})
	}
}

func hasRole(rules []roleRule, role string) bool {
	for _, r := range rules {
		if r.role == role {
			return true
		}
	}
	return false
}

func roleNames(rules []roleRule) string {
	var n []string
	for _, r := range rules {
		n = append(n, r.role)
	}
	return strings.Join(n, ", ")
}

func span(r roleRule) string {
	if r.min == r.max {
		return fmt.Sprint(r.min)
	}
	return fmt.Sprintf("%d to %d", r.min, r.max)
}

func hasSource(clips []Clip, src string) bool {
	for _, c := range clips {
		if c.Source == src {
			return true
		}
	}
	return false
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// ShapeRoles describes a shape's roles, e.g. "hook (1), item (2 to 6), cta (1)".
func ShapeRoles(shape string) string {
	var parts []string
	for _, r := range shapeRoles[shape] {
		parts = append(parts, fmt.Sprintf("%s (%s)", r.role, span(r)))
	}
	return strings.Join(parts, ", ")
}

// sortedKeys is m's keys in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
