package reel

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"
)

// Word is one spoken word and when it is said, in seconds.
type Word struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// elevenAlignment is ElevenLabs' with-timestamps alignment: one entry per
// character.
type elevenAlignment struct {
	Characters []string  `json:"characters"`
	Starts     []float64 `json:"character_start_times_seconds"`
	Ends       []float64 `json:"character_end_times_seconds"`
}

// LoadWords reads a narration's word timings: ElevenLabs' with-timestamps
// response (its alignment, or the alignment object alone) or the kit's own
// list, [{word,start,end}] or {"words": [...]}. Times are seconds into the
// narration's audio file. An eleven_v3 audio tag in the alignment
// ([laughs]) is left out: it is not a spoken word.
func LoadWords(path string) ([]Word, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	ws, err := ParseWords(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return ws, nil
}

// ParseWords is LoadWords on the file's bytes.
func ParseWords(data []byte) ([]Word, error) {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "[") {
		var ws []Word
		if err := json.Unmarshal(data, &ws); err != nil {
			return nil, err
		}
		return checkWords(ws)
	}
	var doc struct {
		Words      []Word           `json:"words"`
		Alignment  *elevenAlignment `json:"alignment"`
		Normalized *elevenAlignment `json:"normalized_alignment"`
		elevenAlignment
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	switch {
	case len(doc.Words) > 0:
		return checkWords(doc.Words)
	case doc.Alignment != nil:
		return fromCharacters(*doc.Alignment)
	case doc.Normalized != nil:
		return fromCharacters(*doc.Normalized)
	case len(doc.Characters) > 0:
		return fromCharacters(doc.elevenAlignment)
	}
	return nil, errors.New("no word timings: want ElevenLabs' alignment (characters, character_start_times_seconds, character_end_times_seconds) or [{word,start,end}]")
}

func fromCharacters(a elevenAlignment) ([]Word, error) {
	if len(a.Characters) != len(a.Starts) || len(a.Characters) != len(a.Ends) {
		return nil, fmt.Errorf("alignment has %d characters, %d starts and %d ends", len(a.Characters), len(a.Starts), len(a.Ends))
	}
	var ws []Word
	var cur *Word
	inTag := false
	for i, ch := range a.Characters {
		// An eleven_v3 audio tag ([laughs], [whispers]) is not spoken.
		if ch == "[" {
			inTag = true
			continue
		}
		if inTag {
			if ch == "]" {
				inTag = false
			}
			continue
		}
		if strings.TrimFunc(ch, unicode.IsSpace) == "" {
			cur = nil
			continue
		}
		if cur == nil {
			ws = append(ws, Word{Start: a.Starts[i]})
			cur = &ws[len(ws)-1]
		}
		cur.Word += ch
		cur.End = a.Ends[i]
	}
	return checkWords(ws)
}

func checkWords(ws []Word) ([]Word, error) {
	if len(ws) == 0 {
		return nil, errors.New("no words")
	}
	prev := 0.0
	for i, w := range ws {
		if w.End < w.Start || w.Start < prev-0.001 {
			return nil, fmt.Errorf("word %d (%q): times out of order (%g to %g after %g)", i, w.Word, w.Start, w.End, prev)
		}
		prev = w.Start
	}
	return ws, nil
}
