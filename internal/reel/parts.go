package reel

import (
	"fmt"
	"math"
	"strings"
)

// The template's parts beside the captions: a scene's clips played one
// after another (Clips), an app recording in a phone frame (Phone), one
// disclosure label over the whole reel (SceneLabel) and stamp words that
// land on a scene's cuts (Stamps). The plan carries their timing and boxes,
// so tests and QA see them without Remotion and the template only draws.

// Layout is where the parts sit on the plan's frame.
type Layout struct {
	// Label is the corner the disclosure label (or the per-clip AI badge)
	// starts in; its width is the label's own (Plan.Label.Box).
	Label Box `json:"label"`
	// Phone is the phone frame's outer box; the screen is PhoneBezel inside.
	Phone Box `json:"phone"`
}

// PlanLabel is the label drawn over the whole reel.
type PlanLabel struct {
	Text string `json:"text"`
	Box  Box    `json:"box"`
}

// PlanStamp is one stamp on a scene: From and Frames inside the scene,
// Start and End on the reel's clock, Box at rest (after its land-in).
type PlanStamp struct {
	Text   string  `json:"text"`
	From   int     `json:"from"`
	Frames int     `json:"frames"`
	Start  float64 `json:"start"`
	End    float64 `json:"end"`
	Box    Box     `json:"box"`
}

// Sizes the template draws the parts at; the boxes here follow them.
const (
	labelFont   = 36
	labelPadX   = 16
	labelPadY   = 8
	stampFont   = 170
	stampBorder = 12
	stampPadX   = 40
	stampPadY   = 4
	stampSpace  = 8 // letter spacing
	// PhoneBezel is the phone frame around the screen; the screen is a
	// 780x1688 recording's shape.
	PhoneBezel = 16
	phoneRatio = 1688.0 / 780.0
	// stampLead is how many frames into a cut a stamp lands when the
	// narration doesn't say its word once per cut.
	stampLead = 4
	// minRate is the slowest a clip plays to fill its share of a scene,
	// instead of freezing on its last frame.
	minRate = 0.75
)

// layoutFor is a format's layout. The label sits just inside the safe zone's
// top-left corner; the phone, its screen PhoneScreenW wide, from PhoneTop.
func layoutFor(f Format, z SafeZone) Layout {
	sw := f.PhoneScreenW
	sh := math.Round(sw * phoneRatio)
	ow, oh := sw+2*PhoneBezel, sh+2*PhoneBezel
	return Layout{
		Label: Box{X: z.Left + 10, Y: z.Top + 20, H: labelFont*1.25 + 2*labelPadY},
		Phone: Box{X: math.Round((float64(f.Width) - ow) / 2), Y: f.PhoneTop, W: ow, H: oh},
	}
}

// textWidth is about how wide text is at a font size (an average glyph is
// about 0.55 of it; capitals in a heavy face, which stamps use, 0.65).
func textWidth(text string, size, glyph, spacing float64) float64 {
	n := float64(len([]rune(text)))
	return n*(size*glyph+spacing) - spacing
}

// labelBox is the label's box at the layout's corner.
func labelBox(text string, l Layout) Box {
	b := l.Label
	b.W = math.Round(textWidth(text, labelFont, 0.55, 0) + 2*labelPadX)
	return b
}

// stampBox is a stamp's box at rest, centred on the frame.
func stampBox(text string, f Format) Box {
	w := math.Round(textWidth(strings.ToUpper(text), stampFont, 0.65, stampSpace) + 2*stampPadX + 2*stampBorder)
	h := math.Round(stampFont*1.2 + 2*stampPadY + 2*stampBorder)
	return Box{X: math.Round((float64(f.Width) - w) / 2), Y: math.Round((float64(f.Height) - h) / 2), W: w, H: h}
}

// outside says where box b crosses safe zone z on a w x h frame; none is inside.
func outside(b Box, z SafeZone, w, h float64) []string {
	var where []string
	if b.Y < z.Top {
		where = append(where, fmt.Sprintf("top %g px into the top %g", z.Top-b.Y, z.Top))
	}
	if b.Y+b.H > h-z.Bottom {
		where = append(where, fmt.Sprintf("bottom %g px into the bottom %g", b.Y+b.H-(h-z.Bottom), z.Bottom))
	}
	if b.X < z.Left {
		where = append(where, fmt.Sprintf("left %g px into the left %g", z.Left-b.X, z.Left))
	}
	if b.X+b.W > w-z.Right {
		where = append(where, fmt.Sprintf("right %g px into the right %g", b.X+b.W-(w-z.Right), z.Right))
	}
	return where
}

// clipLength is a trimmed clip's seconds; 0 for one of unknown length.
func clipLength(c Clip) float64 {
	if c.End > c.Start {
		return c.End - c.Start
	}
	return 0
}

// splitClips shares a scene's frames among its clips, in order: in
// proportion to their lengths when every clip is trimmed, else evenly.
// A clip shorter than its share plays slower (down to minRate).
func splitClips(clips []PlanClip, lens []float64, frames, fps int) {
	if len(clips) == 0 {
		return
	}
	total := frames
	if total < 1 {
		total = 1
	}
	weights := make([]float64, len(clips))
	known := true
	for i, l := range lens {
		weights[i] = l
		if l <= 0 {
			known = false
		}
	}
	if !known {
		for i := range weights {
			weights[i] = 1
		}
	}
	sum := 0.0
	for _, w := range weights {
		sum += w
	}
	starts := make([]int, len(clips))
	acc := 0.0
	for i, w := range weights {
		starts[i] = int(math.Round(acc / sum * float64(total)))
		acc += w
	}
	for i := range clips {
		end := total
		if i < len(clips)-1 {
			end = starts[i+1]
		}
		n := end - starts[i]
		if n < 1 {
			n = 1
		}
		clips[i].From, clips[i].Frames, clips[i].Rate = starts[i], n, 1
		if l := lens[i]; l > 0 {
			clips[i].Rate = round3(math.Min(1, math.Max(minRate, l*float64(fps)/float64(n))))
		}
	}
}

// stampWord is how a narration word compares with a stamp's on word.
func stampWord(w string) string {
	return strings.ToLower(strings.Trim(w, ".,!?;:\"'“”„«»…"))
}

// planStamps puts one stamp on each of the scene's cuts (one when it has no
// clips). When the narration says the on word once per cut, each lands on
// its word (kept inside its cut); else stampLead frames into the cut.
func planStamps(ps *PlanScene, st *Stamp, f Format, fps int) {
	cuts := []int{0}
	if len(ps.Clips) > 0 {
		cuts = cuts[:0]
		for _, c := range ps.Clips {
			cuts = append(cuts, c.From)
		}
	}
	on := stampWord(st.On)
	if on == "" {
		on = stampWord(st.Text)
	}
	var said []int
	if ps.Narration != nil {
		for _, w := range ps.Narration.Words {
			if stampWord(w.Word) == on {
				said = append(said, int(math.Round((w.Start-ps.Start)*float64(fps))))
			}
		}
	}
	box := stampBox(st.Text, f)
	for i, cut := range cuts {
		end := ps.Frames
		if i < len(cuts)-1 {
			end = cuts[i+1]
		}
		from := cut + stampLead
		if len(said) == len(cuts) {
			from = max(cut, said[i])
		}
		from = max(cut, min(from, end-1))
		n := max(1, end-from)
		ps.Stamps = append(ps.Stamps, PlanStamp{
			Text: st.Text, From: from, Frames: n, Box: box,
			Start: round3(ps.Start + float64(from)/float64(fps)),
			End:   round3(ps.Start + float64(from+n)/float64(fps)),
		})
	}
}
