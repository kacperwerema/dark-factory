package reel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/jsonfile"
)

// TemplateConfigFile is the kit's config in a template project.
const TemplateConfigFile = "reel.json"

// DefaultTemplateDir is where reel init puts the starter template.
const DefaultTemplateDir = "reels/template"

// TemplateConfig is a template project's reel.json.
type TemplateConfig struct {
	// Render is the command that renders a composition, run in the
	// template's folder. Placeholders: {composition} {out} {props}
	// {duration} {frames} {fps} {width} {height}.
	Render []string `json:"render"`
	// Compositions maps each shape to its composition id.
	Compositions map[string]string `json:"compositions"`
	// Public is the folder the template's staticFile() reads (public).
	Public string    `json:"public,omitempty"`
	QA     *QABounds `json:"qa,omitempty"`
}

// LoadTemplate reads a template folder's reel.json.
func LoadTemplate(dir string) (*TemplateConfig, error) {
	path := filepath.Join(dir, TemplateConfigFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no %s in %s: copy the starter template there with issue-cli reel init %s, or pass --template <dir>", TemplateConfigFile, dir, dir)
	}
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var c TemplateConfig
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(c.Render) == 0 {
		return nil, fmt.Errorf("%s: render is empty: name the command that renders a composition", path)
	}
	if c.Public == "" {
		c.Public = "public"
	}
	if c.QA != nil {
		if ps := c.QA.problems("qa"); len(ps) > 0 {
			return nil, fmt.Errorf("%s: %s", path, ps[0])
		}
	}
	return &c, nil
}

// RenderOptions are a render's inputs.
type RenderOptions struct {
	Template string
	// Out is the MP4 to write; beside it go <name>.plan.json (the props),
	// <name>.narration.wav (the narration alone, which QA's sync check
	// reads) and <name>.raw.mp4 (the template's frames, removed at the end).
	Out   string
	Tools Tools
	// Log gets the commands render runs and the renderer's own output.
	Log io.Writer
}

// Result is what a render wrote.
type Result struct {
	Out       string
	PlanFile  string
	Narration string
	Plan      *Plan
}

// Sidecar paths next to a reel.
func PlanPath(out string) string { return strings.TrimSuffix(out, filepath.Ext(out)) + ".plan.json" }
func NarrationPath(out string) string {
	return strings.TrimSuffix(out, filepath.Ext(out)) + ".narration.wav"
}
func ReportPath(out string) string { return out + ".qa.json" }

// Render fills the template with the storyboard and encodes the reel.
func Render(ctx context.Context, s *Storyboard, o RenderOptions) (*Result, error) {
	if ps := s.RenderProblems(); len(ps) > 0 {
		return nil, ProblemsError(ps)
	}
	tc, err := LoadTemplate(o.Template)
	if err != nil {
		return nil, err
	}
	comp := tc.Compositions[s.Shape]
	if comp == "" {
		return nil, fmt.Errorf("%s: no composition for shape %s (compositions: %v)", filepath.Join(o.Template, TemplateConfigFile), s.Shape, sortedKeys(tc.Compositions))
	}
	plan, err := BuildPlan(s, o.Tools.AudioSeconds(ctx))
	if err != nil {
		return nil, err
	}
	plan.Composition = comp
	if err := os.MkdirAll(filepath.Dir(o.Out), 0o755); err != nil {
		return nil, err
	}
	name := strings.TrimSuffix(filepath.Base(o.Out), filepath.Ext(o.Out))
	if err := stageMedia(plan, filepath.Join(o.Template, tc.Public), "reel-"+name); err != nil {
		return nil, err
	}
	res := &Result{Out: o.Out, PlanFile: PlanPath(o.Out), Narration: NarrationPath(o.Out), Plan: plan}
	if err := jsonfile.Write(res.PlanFile, plan, 0o644); err != nil {
		return nil, err
	}
	raw := strings.TrimSuffix(o.Out, filepath.Ext(o.Out)) + ".raw.mp4"
	defer os.Remove(raw)
	if err := runRenderer(ctx, tc, o, plan, res.PlanFile, raw); err != nil {
		return nil, err
	}
	if _, err := os.Stat(raw); err != nil {
		return nil, fmt.Errorf("the template's render command wrote no %s: %v", raw, err)
	}
	if err := encode(ctx, o, plan, raw, res.Narration); err != nil {
		return nil, err
	}
	return res, nil
}

// ProblemsError is one error naming every problem.
func ProblemsError(ps []Problem) error {
	var b strings.Builder
	fmt.Fprintf(&b, "the storyboard has %d problem(s):", len(ps))
	for _, p := range ps {
		b.WriteString("\n  " + p.String())
	}
	return errors.New(b.String())
}

// stageMedia copies the clips and the logo into the template's public
// folder (dir/<sub>), so the template reads them with staticFile(), and
// points the plan's Src at them.
func stageMedia(p *Plan, public, sub string) error {
	dst := filepath.Join(public, sub)
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	n := 0
	copyIn := func(file string) (string, error) {
		n++
		base := fmt.Sprintf("%02d-%s", n, filepath.Base(file))
		if err := copyFile(file, filepath.Join(dst, base)); err != nil {
			return "", err
		}
		return sub + "/" + base, nil
	}
	for i := range p.Scenes {
		for j := range p.Scenes[i].Clips {
			c := &p.Scenes[i].Clips[j]
			src, err := copyIn(c.File)
			if err != nil {
				return err
			}
			c.Src = src
		}
	}
	if p.Brand.Logo != "" {
		src, err := copyIn(p.Brand.Logo)
		if err != nil {
			return err
		}
		p.Brand.Logo = src
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func runRenderer(ctx context.Context, tc *TemplateConfig, o RenderOptions, p *Plan, props, raw string) error {
	absProps, _ := filepath.Abs(props)
	absRaw, _ := filepath.Abs(raw)
	r := strings.NewReplacer(
		"{composition}", p.Composition, "{out}", absRaw, "{props}", absProps,
		"{duration}", strconv.FormatFloat(p.Duration, 'f', 3, 64), "{frames}", strconv.Itoa(p.Frames),
		"{fps}", strconv.Itoa(p.FPS), "{width}", strconv.Itoa(p.Width), "{height}", strconv.Itoa(p.Height),
	)
	args := make([]string, len(tc.Render))
	for i, a := range tc.Render {
		args[i] = r.Replace(a)
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = o.Template
	var buf bytes.Buffer
	w := io.Writer(&buf)
	if o.Log != nil {
		fmt.Fprintf(o.Log, "$ %s\n", strings.Join(args, " "))
		w = io.MultiWriter(&buf, o.Log)
	}
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("the template's render command failed (%s): %w\n%s", strings.Join(args, " "), err, tail(buf.String(), 12))
	}
	return nil
}

// encode lays the narration at its scene starts, the music under it,
// brings the mix to -14 LUFS (loudnorm, two passes) and encodes H.264/AAC
// 9:16 with the index first.
func encode(ctx context.Context, o RenderOptions, p *Plan, raw, stem string) error {
	total := strconv.FormatFloat(p.Duration, 'f', 3, 64)
	mix := strings.TrimSuffix(o.Out, filepath.Ext(o.Out)) + ".mix.wav"
	defer os.Remove(mix)
	run := func(args ...string) (string, error) {
		if o.Log != nil {
			fmt.Fprintf(o.Log, "$ ffmpeg %s\n", strings.Join(args, " "))
		}
		return o.Tools.ffmpeg(ctx, args...)
	}
	// 1. The narration stem.
	args := []string{"-y"}
	var chains, labels []string
	for _, sc := range p.Scenes {
		if sc.Narration == nil {
			continue
		}
		i := len(labels)
		args = append(args, "-i", sc.Narration.File)
		ms := int(math.Round(sc.Narration.Start * 1000))
		chains = append(chains, fmt.Sprintf("[%d:a]aresample=48000,aformat=channel_layouts=stereo,adelay=%d|%d[n%d]", i, ms, ms, i))
		labels = append(labels, fmt.Sprintf("[n%d]", i))
	}
	if len(labels) == 0 {
		args = append(args, "-f", "lavfi", "-t", total, "-i", "anullsrc=r=48000:cl=stereo")
		chains = append(chains, "[0:a]anull[stem]")
	} else if len(labels) == 1 {
		chains = append(chains, labels[0]+"apad,atrim=0:"+total+"[stem]")
	} else {
		chains = append(chains, fmt.Sprintf("%samix=inputs=%d:normalize=0:duration=longest,apad,atrim=0:%s[stem]", strings.Join(labels, ""), len(labels), total))
	}
	args = append(args, "-filter_complex", strings.Join(chains, ";"), "-map", "[stem]", "-ar", "48000", "-ac", "2", stem)
	if _, err := run(args...); err != nil {
		return err
	}
	// 2. Music under it.
	if m := p.Music; m != nil {
		vol := strconv.FormatFloat(m.VolumeDB, 'f', 1, 64)
		f := "[1:a]aresample=48000,aformat=channel_layouts=stereo,volume=" + vol + "dB,atrim=0:" + total + "[m];"
		if m.Duck {
			f += "[0:a]asplit=2[s1][s2];[m][s1]sidechaincompress=threshold=0.03:ratio=8:attack=20:release=400[md];[s2][md]amix=inputs=2:normalize=0:duration=first[mix]"
		} else {
			f += "[0:a][m]amix=inputs=2:normalize=0:duration=first[mix]"
		}
		if _, err := run("-y", "-i", stem, "-stream_loop", "-1", "-i", m.File, "-filter_complex", f, "-map", "[mix]", "-ar", "48000", "-ac", "2", "-t", total, mix); err != nil {
			return err
		}
	} else if err := copyFile(stem, mix); err != nil {
		return err
	}
	// 3. Measure, then 4. encode with the measured values.
	const target = "I=-14:TP=-1:LRA=11"
	audio := "anull"
	if out, err := run("-i", mix, "-af", "loudnorm="+target+":print_format=json", "-f", "null", "-"); err != nil {
		return err
	} else if st, err := parseLoudnorm(out); err == nil && st.InputI != "-inf" && st.InputI != "" {
		audio = fmt.Sprintf("loudnorm=%s:measured_I=%s:measured_TP=%s:measured_LRA=%s:measured_thresh=%s:offset=%s:linear=true",
			target, st.InputI, st.InputTP, st.InputLRA, st.InputThresh, st.Offset)
	}
	w, h := strconv.Itoa(p.Width), strconv.Itoa(p.Height)
	video := fmt.Sprintf("[0:v]scale=%s:%s:force_original_aspect_ratio=decrease,pad=%s:%s:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=%d,format=yuv420p[v]", w, h, w, h, p.FPS)
	_, err := run("-y", "-i", raw, "-i", mix, "-filter_complex", video+";[1:a]"+audio+",aresample=48000[a]",
		"-map", "[v]", "-map", "[a]", "-c:v", "libx264", "-preset", "medium", "-profile:v", "high", "-crf", "20",
		"-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-t", total, "-movflags", "+faststart", o.Out)
	return err
}
