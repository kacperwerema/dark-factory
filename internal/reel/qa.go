package reel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"time"
)

// The QA checks, in report order.
const (
	CheckDuration = "duration"
	CheckAspect   = "aspect"
	CheckLoudness = "loudness"
	CheckBlack    = "black-frames"
	CheckSafeZone = "safe-zone"
	CheckSync     = "caption-sync"
)

// CheckNames are the checks in order.
var CheckNames = []string{CheckDuration, CheckAspect, CheckLoudness, CheckBlack, CheckSafeZone, CheckSync}

// Check is one QA check's verdict.
type Check struct {
	Name     string   `json:"name"`
	OK       bool     `json:"ok"`
	Measured string   `json:"measured"`
	Bound    string   `json:"bound"`
	Details  []string `json:"details,omitempty"`
}

// Report is a reel's QA.
type Report struct {
	File       string    `json:"file"`
	Storyboard string    `json:"storyboard"`
	Plan       string    `json:"plan"`
	Date       time.Time `json:"date"`
	Pass       bool      `json:"pass"`
	Checks     []Check   `json:"checks"`
	Bounds     Bounds    `json:"bounds"`
}

// Failed is the checks that failed.
func (r *Report) Failed() []Check {
	var out []Check
	for _, c := range r.Checks {
		if !c.OK {
			out = append(out, c)
		}
	}
	return out
}

// Summary is the report in a few lines: the verdict, then one per check.
func (r *Report) Summary() string {
	var b strings.Builder
	if r.Pass {
		fmt.Fprintf(&b, "QA: pass (%d checks)\n", len(r.Checks))
	} else {
		fmt.Fprintf(&b, "QA: FAIL (%d of %d checks)\n", len(r.Failed()), len(r.Checks))
	}
	for _, c := range r.Checks {
		mark := "✓"
		if !c.OK {
			mark = "✗"
		}
		fmt.Fprintf(&b, "%s %s: %s (bound: %s)\n", mark, c.Name, c.Measured, c.Bound)
		if !c.OK {
			for _, d := range c.Details {
				fmt.Fprintf(&b, "    %s\n", d)
			}
		}
	}
	return b.String()
}

// Markdown is the report as the shelf shows it beside the video.
func (r *Report) Markdown() string {
	var b strings.Builder
	verdict := "pass"
	if !r.Pass {
		verdict = "FAIL"
	}
	fmt.Fprintf(&b, "## Reel QA: %s\n\n", verdict)
	fmt.Fprintf(&b, "%s, %s\n\n", filepathBase(r.File), r.Date.Local().Format("2006-01-02 15:04"))
	b.WriteString("| Check | Result | Measured | Bound |\n|---|---|---|---|\n")
	for _, c := range r.Checks {
		res := "pass"
		if !c.OK {
			res = "**fail**"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", c.Name, res, c.Measured, c.Bound)
	}
	for _, c := range r.Checks {
		if len(c.Details) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n**%s**\n\n", c.Name)
		for _, d := range c.Details {
			fmt.Fprintf(&b, "- %s\n", d)
		}
	}
	b.WriteString("\nBounds not from the kit's defaults:")
	any := false
	for _, k := range boundKeys {
		if src := r.Bounds.From[k]; src != "default" {
			fmt.Fprintf(&b, " %s (%s)", k, src)
			any = true
		}
	}
	if !any {
		b.WriteString(" none")
	}
	b.WriteString(".\n")
	return b.String()
}

func filepathBase(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// QAInput is what QA reads: the MP4, the plan render wrote (its captions
// are what the template drew), the plan worked out from the storyboard
// now (the words as the TTS timed them), the narration stem and bounds.
type QAInput struct {
	File       string
	Storyboard string
	PlanFile   string
	Drawn      *Plan
	Fresh      *Plan
	// Narration is the narration stem; "" reads the reel's own audio
	// (music in the mix then counts as sound).
	Narration string
	Bounds    Bounds
}

// LoadPlan reads a plan render wrote.
func LoadPlan(path string) (*Plan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Plan
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &p, nil
}

// RunQA checks the reel with ffprobe and ffmpeg.
func RunQA(ctx context.Context, t Tools, in QAInput, now time.Time) (*Report, error) {
	if in.Drawn == nil {
		in.Drawn = in.Fresh
	}
	if in.Fresh == nil {
		return nil, errors.New("QA needs the storyboard's plan")
	}
	probe, err := t.Probe(ctx, in.File)
	if err != nil {
		return nil, err
	}
	var loud float64
	var loudErr error
	if probe.HasAudio {
		out, err := t.ffmpeg(ctx, "-i", in.File, "-vn", "-af", "ebur128=framelog=quiet", "-f", "null", "-")
		if err != nil {
			return nil, err
		}
		loud, loudErr = ParseLoudness(out)
	} else {
		loudErr = errors.New("the reel has no audio stream")
	}
	var black []Span
	if probe.HasVideo {
		out, err := t.ffmpeg(ctx, "-i", in.File, "-an", "-vf", fmt.Sprintf("blackdetect=d=%g:pix_th=0.10", in.Bounds.BlackSeconds), "-f", "null", "-")
		if err != nil {
			return nil, err
		}
		black = ParseBlack(out)
	}
	src := in.Narration
	if src == "" {
		src = in.File
	}
	var speech []Span
	speechErr := error(nil)
	if probe.HasAudio || in.Narration != "" {
		out, err := t.ffmpeg(ctx, "-i", src, "-vn", "-af", "silencedetect=noise=-40dB:d=0.1", "-f", "null", "-")
		if err != nil {
			speechErr = err
		} else {
			speech = ParseSpeech(out, probe.Duration)
		}
	}
	m := Measured{Probe: probe, Loudness: loud, LoudnessErr: loudErr, Black: black, Speech: speech, SpeechErr: speechErr}
	r := &Report{File: in.File, Storyboard: in.Storyboard, Plan: in.PlanFile, Date: now, Bounds: in.Bounds}
	r.Checks = Judge(m, in.Drawn, in.Fresh, in.Bounds)
	r.Pass = len(r.Failed()) == 0
	return r, nil
}

// Measured is what the tools measured on a reel.
type Measured struct {
	Probe       Probe
	Loudness    float64
	LoudnessErr error
	Black       []Span
	Speech      []Span
	SpeechErr   error
}

// Judge holds the measurements to the bounds: drawn is the plan the
// template rendered, fresh the plan from the storyboard now.
func Judge(m Measured, drawn, fresh *Plan, b Bounds) []Check {
	return []Check{
		judgeDuration(m, fresh, b),
		judgeAspect(m),
		judgeLoudness(m, b),
		judgeBlack(m, b),
		judgeSafeZone(drawn, b),
		judgeSync(m, drawn, fresh, b),
	}
}

func judgeDuration(m Measured, p *Plan, b Bounds) Check {
	d := m.Probe.Duration
	c := Check{Name: CheckDuration, Measured: fmt.Sprintf("%.2f s (plan %.2f s)", d, p.Duration),
		Bound: fmt.Sprintf("%g-%g s, within %g s of the plan", b.MinSeconds, b.MaxSeconds, b.PlanSlack)}
	c.OK = d >= b.MinSeconds && d <= b.MaxSeconds && math.Abs(d-p.Duration) <= b.PlanSlack
	if d < b.MinSeconds || d > b.MaxSeconds {
		c.Details = append(c.Details, fmt.Sprintf("%.2f s is outside %g-%g s", d, b.MinSeconds, b.MaxSeconds))
	}
	if diff := d - p.Duration; math.Abs(diff) > b.PlanSlack {
		c.Details = append(c.Details, fmt.Sprintf("%+.2f s off the storyboard's plan: re-render, or fix the scene durations", diff))
	}
	return c
}

func judgeAspect(m Measured) Check {
	p := m.Probe
	c := Check{Name: CheckAspect, Bound: "9:16 (0.5625)"}
	if !p.HasVideo {
		c.Measured = "no video stream"
		return c
	}
	a := p.Aspect()
	c.Measured = fmt.Sprintf("%dx%d, SAR %d:%d, %.4f", p.Width, p.Height, p.SARNum, p.SARDen, a)
	c.OK = math.Abs(a-9.0/16.0) < 0.005
	if !c.OK {
		c.Details = append(c.Details, "a reel is vertical 9:16 (1080x1920): the template's composition or the encode is another shape")
	} else if p.Width < FrameWidth {
		c.Details = append(c.Details, fmt.Sprintf("%dx%d is below 1080x1920: the platforms upscale it", p.Width, p.Height))
	}
	return c
}

func judgeLoudness(m Measured, b Bounds) Check {
	c := Check{Name: CheckLoudness, Bound: fmt.Sprintf("%g LUFS ±%g", b.LUFS, b.LUFSSlack)}
	if m.LoudnessErr != nil {
		c.Measured = m.LoudnessErr.Error()
		return c
	}
	if math.IsInf(m.Loudness, -1) {
		c.Measured = "silent (-inf LUFS)"
		c.Details = append(c.Details, "no sound: give the storyboard narration or music")
		return c
	}
	c.Measured = fmt.Sprintf("%.1f LUFS integrated", m.Loudness)
	c.OK = math.Abs(m.Loudness-b.LUFS) <= b.LUFSSlack
	if !c.OK {
		c.Details = append(c.Details, fmt.Sprintf("%+.1f LU off %g: render encodes to %g LUFS; a file encoded elsewhere needs loudnorm", m.Loudness-b.LUFS, b.LUFS, b.LUFS))
	}
	return c
}

func judgeBlack(m Measured, b Bounds) Check {
	c := Check{Name: CheckBlack, Bound: fmt.Sprintf("no black stretch of %g s or more", b.BlackSeconds)}
	var long []Span
	for _, s := range m.Black {
		if s.End-s.Start >= b.BlackSeconds-0.001 {
			long = append(long, s)
		}
	}
	c.OK = len(long) == 0 && m.Probe.HasVideo
	switch {
	case !m.Probe.HasVideo:
		c.Measured = "no video stream"
	case len(long) == 0:
		c.Measured = "none"
	default:
		c.Measured = fmt.Sprintf("%d black stretch(es)", len(long))
		for _, s := range long {
			c.Details = append(c.Details, fmt.Sprintf("black %.2f-%.2f s (%.2f s): a clip missing, shorter than its scene, or a fade too long", s.Start, s.End, s.End-s.Start))
		}
	}
	return c
}

func judgeSafeZone(p *Plan, b Bounds) Check {
	z := b.SafeZone
	c := Check{Name: CheckSafeZone, Bound: fmt.Sprintf("inside top %g, bottom %g, left %g, right %g px of 1080x1920", z.Top, z.Bottom, z.Left, z.Right)}
	narrated := false
	for _, s := range p.Scenes {
		if s.Narration != nil {
			narrated = true
		}
	}
	if len(p.Cues) == 0 {
		if narrated {
			c.Measured = "no caption cues in the plan"
			c.Details = append(c.Details, "the reel has narration but its plan has no captions: re-render with issue-cli reel render")
			return c
		}
		c.OK, c.Measured = true, "no captions (no narration)"
		return c
	}
	out := 0
	for _, q := range p.Cues {
		x := q.Box
		var where []string
		if x.Y < z.Top {
			where = append(where, fmt.Sprintf("top %g px into the top %g", z.Top-x.Y, z.Top))
		}
		if x.Y+x.H > FrameHeight-z.Bottom {
			where = append(where, fmt.Sprintf("bottom %g px into the bottom %g", x.Y+x.H-(FrameHeight-z.Bottom), z.Bottom))
		}
		if x.X < z.Left {
			where = append(where, fmt.Sprintf("left %g px into the left %g", z.Left-x.X, z.Left))
		}
		if x.X+x.W > FrameWidth-z.Right {
			where = append(where, fmt.Sprintf("right %g px into the right %g", x.X+x.W-(FrameWidth-z.Right), z.Right))
		}
		if len(where) > 0 {
			out++
			c.Details = append(c.Details, fmt.Sprintf("%q at %.2f s, box %gx%g at %g,%g: %s", q.Text, q.Start, x.W, x.H, x.X, x.Y, strings.Join(where, ", ")))
		}
	}
	c.OK = out == 0
	c.Measured = fmt.Sprintf("%d of %d caption boxes outside the safe zone", out, len(p.Cues))
	return c
}

func judgeSync(m Measured, drawn, fresh *Plan, b Bounds) Check {
	c := Check{Name: CheckSync, Bound: fmt.Sprintf("cues within %g s of their words; speech within %g s of the first word; audio and video start together", b.CueSlack, b.SpeechSlack)}
	words := map[string][]Word{}
	firstWord := map[string]float64{}
	for _, s := range fresh.Scenes {
		if s.Narration != nil {
			words[s.ID] = s.Narration.Words
			if len(s.Narration.Words) > 0 {
				firstWord[s.ID] = s.Narration.Words[0].Start
			}
		}
	}
	worstCue := 0.0
	for _, q := range drawn.Cues {
		ws := words[q.Scene]
		if q.From < 0 || q.To >= len(ws) || q.From > q.To {
			c.Details = append(c.Details, fmt.Sprintf("cue %q names words %d-%d, but scene %s now has %d words: re-render after a new narration", q.Text, q.From, q.To, q.Scene, len(ws)))
			worstCue = math.Inf(1)
			continue
		}
		ds, de := q.Start-ws[q.From].Start, q.End-ws[q.To].End
		off := math.Max(math.Abs(ds), math.Abs(de))
		worstCue = math.Max(worstCue, off)
		if off > b.CueSlack {
			c.Details = append(c.Details, fmt.Sprintf("cue %q shows %.2f-%.2f s, its words are %.2f-%.2f s (%+.2f s)", q.Text, q.Start, q.End, ws[q.From].Start, ws[q.To].End, pick(ds, de)))
		}
	}
	worstSpeech := 0.0
	scenes := 0
	switch {
	case m.SpeechErr != nil:
		c.Details = append(c.Details, "speech detection failed: "+m.SpeechErr.Error())
		worstSpeech = math.Inf(1)
	default:
		for _, s := range fresh.Scenes {
			fw, ok := firstWord[s.ID]
			if !ok {
				continue
			}
			scenes++
			on := Onset(m.Speech, s.Start, b.SpeechSlack+0.05)
			if on < 0 {
				c.Details = append(c.Details, fmt.Sprintf("scene %s: no speech after %.2f s, the first word is at %.2f s", s.ID, s.Start, fw))
				worstSpeech = math.Inf(1)
				continue
			}
			off := on - fw
			worstSpeech = math.Max(worstSpeech, math.Abs(off))
			if math.Abs(off) > b.SpeechSlack {
				c.Details = append(c.Details, fmt.Sprintf("scene %s: speech starts at %.2f s, the first word at %.2f s (%+.2f s): the audio is offset or the timings belong to another take", s.ID, on, fw, off))
			}
		}
	}
	av := 0.0
	if m.Probe.HasAudio && m.Probe.HasVideo {
		av = m.Probe.AudioStart - m.Probe.VideoStart
		if math.Abs(av) > b.CueSlack {
			c.Details = append(c.Details, fmt.Sprintf("audio starts %+.2f s from the video in the file: the encode is offset", av))
		}
	}
	if len(fresh.Cues) == 0 && scenes == 0 {
		c.OK, c.Measured = true, "no narration"
		return c
	}
	c.OK = len(c.Details) == 0
	c.Measured = fmt.Sprintf("cues off by %s s at most, speech by %s s, audio/video %+.2f s", fmtWorst(worstCue), fmtWorst(worstSpeech), av)
	return c
}

func pick(a, b float64) float64 {
	if math.Abs(a) >= math.Abs(b) {
		return a
	}
	return b
}

func fmtWorst(v float64) string {
	if math.IsInf(v, 1) {
		return "?"
	}
	return fmt.Sprintf("%.2f", v)
}
