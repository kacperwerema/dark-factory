package reel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// CaptionLine is one caption a scene shows in place of its voice's words:
// in captions.language when the captions are in another language than the
// voice (English lines over a Polish voice), or the voice's own words
// spelled as the captions want them. A line is timed in one of three ways:
//
//   - Words [first, last]: indexes into the scene's voice words; the line
//     shows from the first word's start to the last one's end, so the
//     voice's timings stay the anchor.
//   - Start and End: seconds into the scene, for a scene with no narration
//     or a line the voice doesn't cover.
//   - neither (a plain string in the storyboard): the scene's voice words
//     are shared out in order by each line's word count; with no narration,
//     the scene's planned duration is.
type CaptionLine struct {
	Text  string   `json:"text"`
	Words []int    `json:"words,omitempty"`
	Start *float64 `json:"start,omitempty"`
	End   *float64 `json:"end,omitempty"`
}

// UnmarshalJSON takes a string ("Fish?") or an object
// ({"text": "Fish?", "words": [0, 0]}); unknown fields are refused, as in
// the rest of the storyboard.
func (l *CaptionLine) UnmarshalJSON(data []byte) error {
	if t := bytes.TrimSpace(data); len(t) > 0 && t[0] == '"' {
		*l = CaptionLine{}
		return json.Unmarshal(t, &l.Text)
	}
	type plain CaptionLine
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var p plain
	if err := dec.Decode(&p); err != nil {
		return fmt.Errorf("a caption line is a string or {text, words: [first, last]} or {text, start, end}: %w", err)
	}
	*l = CaptionLine(p)
	return nil
}

// timed is a line with its own seconds.
func (l CaptionLine) timed() bool { return l.Start != nil || l.End != nil }

// lineProblems are a scene's caption lines' problems; spans are checked
// against the words at render, when the alignment is read.
func lineProblems(at string, lines []CaptionLine) []Problem {
	var ps []Problem
	add := func(path, format string, a ...any) { ps = append(ps, Problem{path, fmt.Sprintf(format, a...)}) }
	for i, l := range lines {
		lat := fmt.Sprintf("%s.captions[%d]", at, i)
		if strings.TrimSpace(l.Text) == "" {
			add(lat+".text", "missing")
		}
		if l.Words != nil {
			if len(l.Words) != 2 || l.Words[0] < 0 || l.Words[1] < l.Words[0] {
				add(lat+".words", "want [first, last], indexes into the scene's voice words (0 is the first), got %v", l.Words)
			}
			if l.timed() {
				add(lat, "words or start/end, not both")
			}
		}
		if l.timed() {
			switch {
			case l.Start == nil || l.End == nil:
				add(lat, "start and end go together (seconds into the scene)")
			case *l.Start < 0 || *l.End <= *l.Start:
				add(lat+".end", "want above start (%g), got %g", *l.Start, *l.End)
			}
		}
	}
	return ps
}

// lineCues are the cues of a scene that has caption lines: one a line.
// words are the scene's voice words on the reel's clock (nil with no
// narration), start and duration the scene's own.
func lineCues(scene string, lines []CaptionLine, words []Word, start, duration float64, style PlanCaptions, custom *Box, f Format) ([]Cue, error) {
	// The plain lines share what the spans and timed lines leave: the voice
	// words between them, or the scene's time when it has no voice.
	share, err := shareOut(lines, words, start, duration)
	if err != nil {
		return nil, fmt.Errorf("scene %s: %w", scene, err)
	}
	var out []Cue
	next := 0
	for i, l := range lines {
		c := Cue{Scene: scene, Text: l.Text, From: -1, To: -1, Box: captionBox(l.Text, style, custom, f)}
		switch {
		case l.timed():
			c.Start, c.End, c.Timed = round3(start+*l.Start), round3(start+*l.End), true
		case share[i].timed:
			c.Start, c.End, c.Timed = share[i].start, share[i].end, true
		default:
			from, to := share[i].from, share[i].to
			if l.Words != nil {
				from, to = l.Words[0], l.Words[1]
			}
			if to >= len(words) {
				return nil, fmt.Errorf("scene %s: caption %q names words %d-%d, but the voice has %d words (0 to %d)", scene, l.Text, from, to, len(words), len(words)-1)
			}
			if from < next {
				return nil, fmt.Errorf("scene %s: caption %q starts at word %d, before the previous caption's last word %d: spans go in order and don't overlap", scene, l.Text, from, next-1)
			}
			c.From, c.To, c.Start, c.End = from, to, words[from].Start, words[to].End
			next = to + 1
		}
		out = append(out, c)
	}
	return out, nil
}

type shared struct {
	from, to   int
	start, end float64
	timed      bool
}

// shareOut gives each plain line (no words, no start/end) its part: a run
// of the voice words the spans around it leave, by the lines' word counts,
// or, with no voice, a part of the scene's duration.
func shareOut(lines []CaptionLine, words []Word, start, duration float64) ([]shared, error) {
	out := make([]shared, len(lines))
	if len(words) == 0 {
		var plain []int
		for i, l := range lines {
			if l.Words != nil {
				return nil, fmt.Errorf("caption %q names voice words, but the scene has no narration: time it with start and end", l.Text)
			}
			if !l.timed() {
				plain = append(plain, i)
			}
		}
		bounds := split(len(plain), weights(lines, plain), duration)
		for k, i := range plain {
			out[i] = shared{start: round3(start + bounds[k]), end: round3(start + bounds[k+1]), timed: true}
		}
		return out, nil
	}
	// Runs of plain lines between anchors (a span's words, or the scene's
	// ends) share the words in between.
	lo := 0
	var run []int
	flush := func(hi int) error {
		if len(run) == 0 {
			return nil
		}
		n := hi - lo
		if n < len(run) {
			return fmt.Errorf("%d caption lines (from %q) share %d voice words: give each line at least one word, or time some with words [first, last]", len(run), lines[run[0]].Text, max(n, 0))
		}
		cuts := splitInts(n, weights(lines, run))
		for k, i := range run {
			out[i] = shared{from: lo + cuts[k], to: lo + cuts[k+1] - 1}
		}
		run = nil
		return nil
	}
	for i, l := range lines {
		switch {
		case l.Words != nil && len(l.Words) == 2:
			if err := flush(l.Words[0]); err != nil {
				return nil, err
			}
			lo = l.Words[1] + 1
		case l.timed():
		default:
			run = append(run, i)
		}
	}
	if err := flush(len(words)); err != nil {
		return nil, err
	}
	return out, nil
}

// weights are the lines' word counts (at least 1 each).
func weights(lines []CaptionLine, idx []int) []int {
	w := make([]int, len(idx))
	for k, i := range idx {
		w[k] = max(1, len(strings.Fields(lines[i].Text)))
	}
	return w
}

// split cuts total into n parts by weight: n+1 bounds from 0 to total.
func split(n int, w []int, total float64) []float64 {
	sum := 0
	for _, x := range w {
		sum += x
	}
	b := make([]float64, n+1)
	acc := 0
	for k := 0; k < n; k++ {
		acc += w[k]
		b[k+1] = total * float64(acc) / float64(sum)
	}
	return b
}

// splitInts cuts n words into len(w) runs by weight, each at least one
// word: len(w)+1 cut points from 0 to n.
func splitInts(n int, w []int) []int {
	f := split(len(w), w, float64(n))
	c := make([]int, len(w)+1)
	c[len(w)] = n
	for k := 1; k < len(w); k++ {
		v := int(f[k] + 0.5)
		lo, hi := c[k-1]+1, n-(len(w)-k)
		c[k] = min(max(v, lo), hi)
	}
	return c
}

// languageProblems: captions in another language than the voice need
// every narrated scene's lines, or the voice's words would show.
func (s *Storyboard) languageProblems() []Problem {
	cl := strings.TrimSpace(s.Captions.Language)
	if cl == "" || strings.EqualFold(cl, strings.TrimSpace(s.Language)) {
		return nil
	}
	var ps []Problem
	for i, sc := range s.Scenes {
		if sc.Narration != nil && len(sc.Captions) == 0 {
			voice := s.Language
			if voice == "" {
				voice = "the voice's language (language: unset)"
			}
			ps = append(ps, Problem{fmt.Sprintf("scenes[%d].captions", i), fmt.Sprintf("captions are %s, the voice is %s: write this scene's lines in %s (each on a span of voice words: {\"text\": \"...\", \"words\": [first, last]})", cl, voice, cl)})
		}
	}
	return ps
}
