package reel

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/jsonfile"
	"github.com/kacperwerema/dark-factory/internal/standin"
)

// These tests run the real ffmpeg (skipped without it, as on the public
// CI) and a stand-in renderer in place of Remotion: no Chrome, no network.

// standinRenderer is a template's render command: a flat frame of the
// plan's size for the plan's length, as Remotion would write the frames.
const standinRenderer = `exec ffmpeg -hide_banner -loglevel error -nostdin -y -f lavfi -i "color=c=0x335577:s=$4x$5:r=30" -t "$3" -c:v libx264 -preset ultrafast -pix_fmt yuv420p "$2"
`

func needFFmpeg(t *testing.T) Tools {
	t.Helper()
	tools, err := FindTools()
	if err != nil {
		t.Skip(err)
	}
	return tools
}

func ff(t *testing.T, args ...string) {
	t.Helper()
	out, err := exec.Command("ffmpeg", append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
	}
}

// fixtureTemplate is a template folder whose render command is the stand-in.
func fixtureTemplate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	standin.Write(t, filepath.Join(dir, "render.sh"), standinRenderer)
	writeFile(t, dir, TemplateConfigFile, `{"render": ["./render.sh", "{composition}", "{out}", "{duration}", "{width}", "{height}"],
		"compositions": {"hook-list": "HookList", "before-after": "BeforeAfter", "app-demo": "AppDemo"}}`)
	return dir
}

// fixtureReel is a hook-list storyboard with real audio: each narration a
// 1.2 s tone after 0.1 s of silence, timed by its word list, and a music bed.
func fixtureReel(t *testing.T) (dir, sb string) {
	t.Helper()
	dir = t.TempDir()
	fixtureMedia(t, dir)
	m := storyboardFor(ShapeHookList)
	m["music"] = map[string]any{"path": "bed.mp3", "licence": map[string]any{"source": "library", "ref": "t-1"}, "duck": true, "volume_db": -20}
	m["scenes"].([]any)[1].(map[string]any)["clips"] = []any{map[string]any{"source": "owner", "path": "clip.mp4"}}
	data, _ := json.Marshal(m)
	return dir, writeFile(t, dir, "sb.json", string(data))
}

// fixturePartsReel is partsStoryboard in format f with fixtureReel's real
// audio and clips: a label, a stamped hook of three cuts, a phone scene.
func fixturePartsReel(t *testing.T, f string) (dir, sb string) {
	t.Helper()
	dir = t.TempDir()
	fixtureMedia(t, dir)
	writeFile(t, dir, "no.json", `[{"word":"One,","start":0.1,"end":0.3},{"word":"no.","start":0.35,"end":0.5},{"word":"two,","start":0.55,"end":0.7},{"word":"no.","start":0.75,"end":0.9},{"word":"three,","start":0.95,"end":1.1},{"word":"no.","start":1.15,"end":1.3}]`)
	m := partsStoryboard(f)
	m["music"] = map[string]any{"path": "bed.mp3", "licence": map[string]any{"source": "library", "ref": "t-1"}, "duck": true, "volume_db": -20}
	data, _ := json.Marshal(m)
	data = []byte(strings.NewReplacer(`"media/ai.mp4"`, `"clip.mp4"`, `"media/app.mp4"`, `"clip.mp4"`).Replace(string(data)))
	return dir, writeFile(t, dir, "sb.json", string(data))
}

// fixtureMedia writes the narration tone, the music bed and a clip in dir.
func fixtureMedia(t *testing.T, dir string) {
	t.Helper()
	ff(t, "-f", "lavfi", "-i", "sine=f=330:d=1.2", "-af", "adelay=100|100,apad=pad_dur=0.1", "-ar", "44100", filepath.Join(dir, "n.mp3"))
	ff(t, "-f", "lavfi", "-i", "sine=f=110:d=3", "-ar", "44100", filepath.Join(dir, "bed.mp3"))
	ff(t, "-f", "lavfi", "-i", "color=c=0x885522:s=540x960:r=30", "-t", "2", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", filepath.Join(dir, "clip.mp4"))
	writeFile(t, dir, "w.json", `[{"word":"One","start":0.1,"end":0.4},{"word":"two","start":0.45,"end":0.8},{"word":"three.","start":0.85,"end":1.3}]`)
}

func renderFixture(t *testing.T, tools Tools) (*Storyboard, *Result, string) {
	t.Helper()
	return renderStoryboard(t, tools, fixtureReel)
}

func renderStoryboard(t *testing.T, tools Tools, fixture func(*testing.T) (string, string)) (*Storyboard, *Result, string) {
	t.Helper()
	dir, sb := fixture(t)
	s, err := Load(sb)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := fixtureTemplate(t)
	res, err := Render(context.Background(), s, RenderOptions{Template: tmpl, Out: filepath.Join(dir, "out", "reel.mp4"), Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	return s, res, tmpl
}

func qa(t *testing.T, tools Tools, s *Storyboard, file, plan, narration string) *Report {
	t.Helper()
	ctx := context.Background()
	fresh, err := BuildPlan(s, tools.AudioSeconds(ctx))
	if err != nil {
		t.Fatal(err)
	}
	in := QAInput{File: file, Fresh: fresh, Narration: narration, Bounds: DefaultBoundsFor(s.Frame())}
	if plan != "" {
		if in.Drawn, err = LoadPlan(plan); err != nil {
			t.Fatal(err)
		}
	}
	r, err := RunQA(ctx, tools, in, time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRenderWritesAQAPassingReel(t *testing.T) {
	tools := needFFmpeg(t)
	t.Run("9:16", func(t *testing.T) { renderPasses(t, tools) })
}

// A storyboard using every part (clips, phone, label, stamps) renders and
// passes QA in 9:16 and in 4:5; the 4:5 one at 1080x1350, its aspect checked.
func TestRenderPartsPassQAInEachFormat(t *testing.T) {
	tools := needFFmpeg(t)
	for _, c := range []struct {
		format        string
		width, height int
		aspect        string
	}{{"9:16", 1080, 1920, "0.5625"}, {"4:5", 1080, 1350, "0.8"}} {
		t.Run(c.format, func(t *testing.T) {
			s, res, _ := renderStoryboard(t, tools, func(t *testing.T) (string, string) { return fixturePartsReel(t, c.format) })
			pr, err := tools.Probe(context.Background(), res.Out)
			if err != nil {
				t.Fatal(err)
			}
			if pr.Width != c.width || pr.Height != c.height {
				t.Fatalf("%s rendered %dx%d", c.format, pr.Width, pr.Height)
			}
			plan, err := LoadPlan(res.PlanFile)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Label == nil || len(plan.Scenes[0].Stamps) != 3 || len(plan.Scenes[0].Clips) != 3 || plan.Scenes[1].Clips[0].Source != SourceAppRecording || plan.Layout.Phone.W == 0 {
				t.Fatalf("the plan lacks a part: label %+v, hook %+v, layout %+v", plan.Label, plan.Scenes[0], plan.Layout)
			}
			r := qa(t, tools, s, res.Out, res.PlanFile, res.Narration)
			if !r.Pass {
				t.Fatalf("QA failed:\n%s", r.Summary())
			}
			for _, ch := range r.Checks {
				if ch.Name == CheckAspect && !strings.Contains(ch.Bound+ch.Measured, c.aspect) {
					t.Errorf("aspect check = %+v, want %s", ch, c.aspect)
				}
				if ch.Name == CheckSafeZone && !strings.Contains(ch.Measured, "1 label, 3 stamp") {
					t.Errorf("safe-zone did not read the parts: %+v", ch)
				}
			}
		})
	}
}

func renderPasses(t *testing.T, tools Tools) {
	s, res, tmpl := renderFixture(t, tools)
	ctx := context.Background()
	pr, err := tools.Probe(ctx, res.Out)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Width != 1080 || pr.Height != 1920 || pr.VideoCodec != "h264" || pr.AudioCodec != "aac" {
		t.Fatalf("probe = %+v", pr)
	}
	if math.Abs(pr.Duration-res.Plan.Duration) > 0.1 {
		t.Errorf("duration %g, plan %g", pr.Duration, res.Plan.Duration)
	}
	out, err := tools.ffmpeg(ctx, "-i", res.Out, "-vn", "-af", "ebur128=framelog=quiet", "-f", "null", "-")
	if err != nil {
		t.Fatal(err)
	}
	if l, err := ParseLoudness(out); err != nil || math.Abs(l+14) > 1 {
		t.Errorf("loudness %g LUFS (%v), want -14 ±1", l, err)
	}
	// The narration starts at each scene's start (+0.1 s, the silence in
	// the audio file): the stem's speech onsets.
	sil, err := tools.ffmpeg(ctx, "-i", res.Narration, "-af", "silencedetect=noise=-40dB:d=0.1", "-f", "null", "-")
	if err != nil {
		t.Fatal(err)
	}
	speech := ParseSpeech(sil, res.Plan.Duration)
	for _, sc := range res.Plan.Scenes {
		if on := Onset(speech, sc.Start, 0.05); math.Abs(on-(sc.Start+0.1)) > 0.05 {
			t.Errorf("scene %s at %.2f s: speech from %.2f s", sc.ID, sc.Start, on)
		}
	}
	// The plan is the template's props: cues, frames, media staged in public/.
	plan, err := LoadPlan(res.PlanFile)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Composition != "HookList" || len(plan.Cues) != 4 || plan.Frames != res.Plan.Frames {
		t.Errorf("plan = %+v", plan)
	}
	if src := plan.Scenes[1].Clips[0].Src; src != "reel-reel/01-clip.mp4" {
		t.Errorf("clip src = %q", src)
	} else if _, err := os.Stat(filepath.Join(tmpl, "public", src)); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(strings.TrimSuffix(res.Out, ".mp4") + ".raw.mp4"); err == nil {
		t.Error("the raw render is left behind")
	}
	r := qa(t, tools, s, res.Out, res.PlanFile, res.Narration)
	if !r.Pass {
		t.Fatalf("QA failed:\n%s", r.Summary())
	}
}

func TestQAFailsBadReels(t *testing.T) {
	tools := needFFmpeg(t)
	s, res, _ := renderFixture(t, tools)
	dir := t.TempDir()
	derived := func(name string, args ...string) string {
		out := filepath.Join(dir, name+".mp4")
		ff(t, append(append([]string{"-i", res.Out}, args...), "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", out)...)
		return out
	}
	editPlan := func(name string, edit func(*Plan)) string {
		p, err := LoadPlan(res.PlanFile)
		if err != nil {
			t.Fatal(err)
		}
		edit(p)
		path := filepath.Join(dir, name+".plan.json")
		if err := jsonfile.Write(path, p, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	offset := filepath.Join(dir, "offset.mp4")
	ff(t, "-i", res.Out, "-i", res.Narration, "-filter_complex", "[1:a]adelay=500|500,volume=9.2dB,atrim=0:"+formatSecs(res.Plan.Duration)+"[a]", "-map", "0:v", "-map", "[a]", "-c:v", "copy", "-c:a", "aac", offset)
	for _, c := range []struct {
		name, file, plan, narration, want string
	}{
		{"too short", derived("short", "-t", "4"), res.PlanFile, res.Narration, CheckDuration},
		{"square", derived("square", "-vf", "scale=1080:1080,setsar=1"), res.PlanFile, res.Narration, CheckAspect},
		{"loud", derived("loud", "-af", "volume=8dB"), res.PlanFile, res.Narration, CheckLoudness},
		{"black", derived("black", "-vf", "drawbox=enable='between(t,2,2.6)':color=black:t=fill"), res.PlanFile, res.Narration, CheckBlack},
		{"caption under the bar", res.Out, editPlan("box", func(p *Plan) { p.Cues[0].Box.Y = 1650 }), res.Narration, CheckSafeZone},
		{"cue off its words", res.Out, editPlan("cue", func(p *Plan) { p.Cues[2].Start += 0.4 }), res.Narration, CheckSync},
		{"audio offset", offset, res.PlanFile, "", CheckSync},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := qa(t, tools, s, c.file, c.plan, c.narration)
			f := failed(r.Checks)
			if r.Pass || len(f) == 0 || f[0] != c.want {
				t.Fatalf("failed %v, want %s first:\n%s", f, c.want, r.Summary())
			}
		})
	}
}

func formatSecs(v float64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
