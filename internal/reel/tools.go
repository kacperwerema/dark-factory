package reel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Tools runs ffmpeg and ffprobe. The parsers below read their text, so a
// test feeds them fixed output.
type Tools struct {
	FFmpeg, FFprobe string
}

// FindTools looks ffmpeg and ffprobe up on PATH.
func FindTools() (Tools, error) {
	var t Tools
	var missing []string
	for _, c := range []struct {
		name string
		dst  *string
	}{{"ffmpeg", &t.FFmpeg}, {"ffprobe", &t.FFprobe}} {
		p, err := exec.LookPath(c.name)
		if err != nil {
			missing = append(missing, c.name)
			continue
		}
		*c.dst = p
	}
	if len(missing) > 0 {
		return t, fmt.Errorf("%s not on PATH: the reel kit needs FFmpeg (brew install ffmpeg, apt install ffmpeg)", strings.Join(missing, " and "))
	}
	return t, nil
}

// ffmpeg runs ffmpeg and returns what it wrote to stderr (its filters'
// reports go there).
func (t Tools) ffmpeg(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, t.FFmpeg, append([]string{"-hide_banner", "-nostdin"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stderr.String(), fmt.Errorf("ffmpeg %s: %w\n%s", strings.Join(args, " "), err, tail(stderr.String(), 12))
	}
	return stderr.String(), nil
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// Probe is what ffprobe says about a file.
type Probe struct {
	Duration   float64
	Width      int
	Height     int
	SARNum     int
	SARDen     int
	VideoCodec string
	AudioCodec string
	VideoStart float64
	AudioStart float64
	HasVideo   bool
	HasAudio   bool
}

// Probe runs ffprobe on a file.
func (t Tools) Probe(ctx context.Context, path string) (Probe, error) {
	out, err := exec.CommandContext(ctx, t.FFprobe, "-v", "error", "-show_entries",
		"format=duration:stream=codec_type,codec_name,width,height,sample_aspect_ratio,start_time", "-of", "json", path).Output()
	if err != nil {
		return Probe{}, fmt.Errorf("ffprobe %s: %w", path, err)
	}
	return ParseProbe(out)
}

// AudioSeconds is a file's length by ffprobe.
func (t Tools) AudioSeconds(ctx context.Context) AudioSeconds {
	return func(path string) (float64, error) {
		p, err := t.Probe(ctx, path)
		if err != nil {
			return 0, err
		}
		if !p.HasAudio {
			return 0, errors.New("no audio stream")
		}
		return p.Duration, nil
	}
}

// ParseProbe reads ffprobe's JSON.
func ParseProbe(data []byte) (Probe, error) {
	var doc struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			Type  string `json:"codec_type"`
			Codec string `json:"codec_name"`
			W     int    `json:"width"`
			H     int    `json:"height"`
			SAR   string `json:"sample_aspect_ratio"`
			Start string `json:"start_time"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return Probe{}, fmt.Errorf("reading ffprobe's output: %w", err)
	}
	p := Probe{SARNum: 1, SARDen: 1}
	p.Duration, _ = strconv.ParseFloat(doc.Format.Duration, 64)
	for _, s := range doc.Streams {
		start, _ := strconv.ParseFloat(s.Start, 64)
		switch s.Type {
		case "video":
			if p.HasVideo {
				continue
			}
			p.HasVideo, p.Width, p.Height, p.VideoCodec, p.VideoStart = true, s.W, s.H, s.Codec, start
			if n, d, ok := ratio(s.SAR); ok {
				p.SARNum, p.SARDen = n, d
			}
		case "audio":
			if p.HasAudio {
				continue
			}
			p.HasAudio, p.AudioCodec, p.AudioStart = true, s.Codec, start
		}
	}
	return p, nil
}

func ratio(s string) (int, int, bool) {
	a, b, ok := strings.Cut(s, ":")
	if !ok {
		return 0, 0, false
	}
	n, err1 := strconv.Atoi(a)
	d, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil || n <= 0 || d <= 0 {
		return 0, 0, false
	}
	return n, d, true
}

// Aspect is the display aspect ratio, width over height.
func (p Probe) Aspect() float64 {
	if p.Height == 0 || p.SARDen == 0 {
		return 0
	}
	return float64(p.Width*p.SARNum) / float64(p.Height*p.SARDen)
}

var integrated = regexp.MustCompile(`(?s)Summary:.*?I:\s+(-?[\d.]+|-inf)\s+LUFS`)

// ParseLoudness reads the integrated loudness from ffmpeg's ebur128
// summary; -Inf for silence.
func ParseLoudness(stderr string) (float64, error) {
	m := integrated.FindStringSubmatch(stderr)
	if m == nil {
		return 0, errors.New("no ebur128 summary in ffmpeg's output")
	}
	if m[1] == "-inf" {
		return math.Inf(-1), nil
	}
	return strconv.ParseFloat(m[1], 64)
}

// Span is a stretch of time in seconds.
type Span struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

var blackLine = regexp.MustCompile(`black_start:\s*(-?[\d.]+)\s+black_end:\s*(-?[\d.]+)`)

// ParseBlack reads blackdetect's black stretches.
func ParseBlack(stderr string) []Span {
	var out []Span
	for _, m := range blackLine.FindAllStringSubmatch(stderr, -1) {
		a, _ := strconv.ParseFloat(m[1], 64)
		b, _ := strconv.ParseFloat(m[2], 64)
		out = append(out, Span{a, b})
	}
	return out
}

var (
	silenceStart = regexp.MustCompile(`silence_start:\s*(-?[\d.]+)`)
	silenceEnd   = regexp.MustCompile(`silence_end:\s*(-?[\d.]+)`)
)

// ParseSpeech turns silencedetect's silences into the stretches with
// sound, over a file of total seconds.
func ParseSpeech(stderr string, total float64) []Span {
	type ev struct {
		t     float64
		start bool
	}
	var evs []ev
	for _, line := range strings.Split(stderr, "\n") {
		if m := silenceStart.FindStringSubmatch(line); m != nil {
			v, _ := strconv.ParseFloat(m[1], 64)
			evs = append(evs, ev{math.Max(0, v), true})
		}
		if m := silenceEnd.FindStringSubmatch(line); m != nil {
			v, _ := strconv.ParseFloat(m[1], 64)
			evs = append(evs, ev{v, false})
		}
	}
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].t < evs[j].t })
	var out []Span
	soundFrom, silent := 0.0, false
	for _, e := range evs {
		if e.start && !silent {
			if e.t > soundFrom {
				out = append(out, Span{soundFrom, e.t})
			}
			silent = true
		} else if !e.start && silent {
			soundFrom, silent = e.t, false
		}
	}
	if !silent && total > soundFrom {
		out = append(out, Span{soundFrom, total})
	}
	return out
}

// Onset is when sound starts at or after t (within early seconds before
// it, which counts as sound already playing at t-early): -1 for none.
func Onset(speech []Span, t, early float64) float64 {
	for _, s := range speech {
		if s.End <= t-early {
			continue
		}
		if s.Start < t-early {
			return t - early
		}
		return s.Start
	}
	return -1
}

// loudnormStats is loudnorm's first-pass measurement.
type loudnormStats struct {
	InputI      string `json:"input_i"`
	InputTP     string `json:"input_tp"`
	InputLRA    string `json:"input_lra"`
	InputThresh string `json:"input_thresh"`
	Offset      string `json:"target_offset"`
}

// parseLoudnorm reads loudnorm's print_format=json block, the last {...}
// in ffmpeg's stderr.
func parseLoudnorm(stderr string) (loudnormStats, error) {
	var st loudnormStats
	i := strings.LastIndex(stderr, "{")
	j := strings.LastIndex(stderr, "}")
	if i < 0 || j < i {
		return st, errors.New("no loudnorm measurement in ffmpeg's output")
	}
	err := json.Unmarshal([]byte(stderr[i:j+1]), &st)
	return st, err
}
