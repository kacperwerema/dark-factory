package reel

import "fmt"

// The frame every plan and caption box is in.
const (
	FrameWidth  = 1080
	FrameHeight = 1920
)

// QABounds are QA bound overrides, in a template's reel.json qa: or a
// storyboard's qa:. A field left out keeps the bound before it.
type QABounds struct {
	MinSeconds   *float64  `json:"min_seconds,omitempty"`
	MaxSeconds   *float64  `json:"max_seconds,omitempty"`
	PlanSlack    *float64  `json:"plan_slack_seconds,omitempty"`
	LUFS         *float64  `json:"lufs,omitempty"`
	LUFSSlack    *float64  `json:"lufs_slack,omitempty"`
	BlackSeconds *float64  `json:"black_seconds,omitempty"`
	CueSlack     *float64  `json:"cue_slack_seconds,omitempty"`
	SpeechSlack  *float64  `json:"speech_slack_seconds,omitempty"`
	SafeZone     *SafeZone `json:"safe_zone,omitempty"`
}

// SafeZone is the margins, in pixels on the 1080x1920 frame, that the
// platforms' own UI covers: captions stay inside them.
type SafeZone struct {
	Top    float64 `json:"top"`
	Bottom float64 `json:"bottom"`
	Left   float64 `json:"left"`
	Right  float64 `json:"right"`
}

// Bounds are the QA bounds in force and where each came from.
type Bounds struct {
	MinSeconds   float64           `json:"min_seconds"`
	MaxSeconds   float64           `json:"max_seconds"`
	PlanSlack    float64           `json:"plan_slack_seconds"`
	LUFS         float64           `json:"lufs"`
	LUFSSlack    float64           `json:"lufs_slack"`
	BlackSeconds float64           `json:"black_seconds"`
	CueSlack     float64           `json:"cue_slack_seconds"`
	SpeechSlack  float64           `json:"speech_slack_seconds"`
	SafeZone     SafeZone          `json:"safe_zone"`
	From         map[string]string `json:"from"`
}

// DefaultSafeZone is the union of what Instagram Reels and TikTok cover on
// a 1080x1920 frame: the top bar, the caption, music and button area at the
// bottom, and the like/comment/share column on the right.
var DefaultSafeZone = SafeZone{Top: 220, Bottom: 420, Left: 60, Right: 140}

// DefaultBounds are the kit's bounds (docs/reels.md#qa).
func DefaultBounds() Bounds {
	b := Bounds{
		MinSeconds: 5, MaxSeconds: 90, PlanSlack: 0.5,
		LUFS: -14, LUFSSlack: 2, BlackSeconds: 0.2,
		CueSlack: 0.15, SpeechSlack: 0.25, SafeZone: DefaultSafeZone,
		From: map[string]string{},
	}
	for _, k := range boundKeys {
		b.From[k] = "default"
	}
	return b
}

var boundKeys = []string{"min_seconds", "max_seconds", "plan_slack_seconds", "lufs", "lufs_slack", "black_seconds", "cue_slack_seconds", "speech_slack_seconds", "safe_zone"}

// Apply lays overrides from src (e.g. "reel.json", "storyboard") over b.
func (b *Bounds) Apply(o *QABounds, src string) {
	if o == nil {
		return
	}
	set := func(dst *float64, v *float64, key string) {
		if v != nil {
			*dst = *v
			b.From[key] = src
		}
	}
	set(&b.MinSeconds, o.MinSeconds, "min_seconds")
	set(&b.MaxSeconds, o.MaxSeconds, "max_seconds")
	set(&b.PlanSlack, o.PlanSlack, "plan_slack_seconds")
	set(&b.LUFS, o.LUFS, "lufs")
	set(&b.LUFSSlack, o.LUFSSlack, "lufs_slack")
	set(&b.BlackSeconds, o.BlackSeconds, "black_seconds")
	set(&b.CueSlack, o.CueSlack, "cue_slack_seconds")
	set(&b.SpeechSlack, o.SpeechSlack, "speech_slack_seconds")
	if o.SafeZone != nil {
		b.SafeZone = *o.SafeZone
		b.From["safe_zone"] = src
	}
}

func (o *QABounds) problems(at string) []Problem {
	var ps []Problem
	for _, f := range []struct {
		key string
		v   *float64
	}{{"min_seconds", o.MinSeconds}, {"max_seconds", o.MaxSeconds}, {"plan_slack_seconds", o.PlanSlack}, {"lufs_slack", o.LUFSSlack}, {"black_seconds", o.BlackSeconds}, {"cue_slack_seconds", o.CueSlack}, {"speech_slack_seconds", o.SpeechSlack}} {
		if f.v != nil && *f.v < 0 {
			ps = append(ps, Problem{at + "." + f.key, fmt.Sprintf("want 0 or above, got %g", *f.v)})
		}
	}
	if o.MinSeconds != nil && o.MaxSeconds != nil && *o.MaxSeconds < *o.MinSeconds {
		ps = append(ps, Problem{at + ".max_seconds", "below min_seconds"})
	}
	if z := o.SafeZone; z != nil && (z.Top < 0 || z.Bottom < 0 || z.Left < 0 || z.Right < 0 || z.Top+z.Bottom >= FrameHeight || z.Left+z.Right >= FrameWidth) {
		ps = append(ps, Problem{at + ".safe_zone", "margins are 0 or above and leave room on the 1080x1920 frame"})
	}
	return ps
}
