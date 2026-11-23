package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/reel"
	"github.com/kacperwerema/dark-factory/internal/standin"
)

func readText(t *testing.T, p string) string {
	t.Helper()
	return string(mustRead(t, p))
}

func reelExitCode(err error) int {
	var c *exitCodeError
	if errors.As(err, &c) {
		return c.Code
	}
	return -1
}

func TestReelInitWritesTheTemplateAndNeverOverwrites(t *testing.T) {
	proj := goalsProject(t)
	ctx, out := goalsCtx(t, proj, false)
	dir := filepath.Join(t.TempDir(), "reels", "template")
	if err := runReel(ctx, []string{"init", dir}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	assertContains(t, s, "✓ Wrote the reel template")
	assertContains(t, s, "shapes hook-list, before-after, app-demo")
	assertContains(t, s, "free for individuals and companies of up to 3 people")
	for _, f := range []string{"reel.json", "package.json", "README.md", "src/Root.tsx", "src/shapes/HookList.tsx", "src/shapes/BeforeAfter.tsx", "src/shapes/AppDemo.tsx", "src/parts/Captions.tsx", "src/parts/Clips.tsx", "src/parts/Phone.tsx", "src/parts/SceneLabel.tsx", "src/parts/Stamps.tsx", "src/frame.ts", "examples/hook-list.json", "examples/parts.json"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	root, _ := os.ReadFile(filepath.Join(dir, "src", "Root.tsx"))
	for _, id := range []string{"'HookList'", "'BeforeAfter'", "'AppDemo'"} {
		assertContains(t, string(root), id)
	}
	readme, _ := os.ReadFile(filepath.Join(dir, "README.md"))
	assertContains(t, string(readme), "https://www.remotion.dev/license")
	// Remotion is a dependency, never vendored.
	pkg, _ := os.ReadFile(filepath.Join(dir, "package.json"))
	assertContains(t, string(pkg), `"remotion": "^4`)
	if _, err := os.Stat(filepath.Join(dir, "node_modules")); err == nil {
		t.Error("the template ships node_modules")
	}
	// Its .gitignore keeps env files (keys) and node_modules out of the
	// project's repo; .env.example stays trackable.
	ignore, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	for _, l := range []string{"node_modules/", ".env", ".env.*", "!.env.example"} {
		assertContains(t, "\n"+string(ignore), "\n"+l+"\n")
	}

	// A second init over the copy refuses and writes nothing.
	mustWrite(t, filepath.Join(dir, "reel.json"), "mine")
	ctx, _ = goalsCtx(t, proj, false)
	err := runReel(ctx, []string{"init", dir})
	if err == nil || !strings.Contains(err.Error(), "never overwrites") || !strings.Contains(err.Error(), "reel.json") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "reel.json")); string(b) != "mine" {
		t.Errorf("reel.json = %q", b)
	}
}

func TestReelValidateListsEveryProblem(t *testing.T) {
	proj := goalsProject(t)
	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	mustWrite(t, good, `{"version":1,"shape":"before-after","title":"T","brand":{"name":"B","colors":{"primary":"#123","text":"#fff","background":"#000"}},
		"scenes":[{"id":"h","role":"hook","duration":2,"narration":{"text":"Hi.","voice":"narrator"}},{"id":"b","role":"before","duration":2},{"id":"a","role":"after","duration":2},{"id":"c","role":"cta","duration":2}],
		"cta":{"text":"Go"}}`)
	ctx, out := goalsCtx(t, proj, false)
	if err := runReel(ctx, []string{"validate", good}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "a valid before-after storyboard (4 scenes; roles hook (1), before (1), after (1), cta (1))")
	assertContains(t, out.String(), "Before render: 2 narration file(s) to make with the TTS connector")

	bad := filepath.Join(dir, "bad.json")
	mustWrite(t, bad, `{"version":1,"shape":"app-demo","title":"T","brand":{"name":"B","colors":{"primary":"#123","text":"#fff","background":"#000"}},
		"scenes":[{"id":"h","role":"hook","duration":2},{"id":"s","role":"step","duration":2,"clips":[{"source":"ai","path":"gone.mp4"}]}],
		"music":{"path":"bed.mp3","licence":{"source":"","ref":""}},"cta":{"text":"Go"}}`)
	ctx, out = goalsCtx(t, proj, false)
	err := runReel(ctx, []string{"validate", bad})
	if reelExitCode(err) != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
	s := out.String()
	for _, want := range []string{
		"scenes[1].clips: an app-demo step shows the app",
		"scenes[1].clips[0].path: no file gone.mp4",
		"scenes[1].clips[0].ai_generated: an ai clip must say ai_generated: true",
		`scenes: app-demo wants 1 role "cta", got 0`,
		"music.path: no file bed.mp3",
		"music.licence: licensed music only",
	} {
		assertContains(t, s, want)
	}

	ctx, out = goalsCtx(t, proj, true)
	if reelExitCode(runReel(ctx, []string{"validate", bad})) != 1 {
		t.Fatal("--json: want exit 1")
	}
	var doc struct {
		OK       bool `json:"ok"`
		Problems []struct{ Path, Msg string }
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil || doc.OK || len(doc.Problems) != 7 {
		t.Fatalf("json = %s (%v)", out, err)
	}

	ctx, out = goalsCtx(t, proj, false)
	if err := runReel(ctx, []string{"schema"}); err != nil || !json.Valid(out.Bytes()) {
		t.Fatalf("schema: %v", err)
	}
}

func TestReelRenderRefusesWhenMemGuardIsBlocked(t *testing.T) {
	proj := goalsProject(t)
	dir := t.TempDir()
	sb := filepath.Join(dir, "sb.json")
	mustWrite(t, sb, `{"version":1,"shape":"before-after","title":"T","brand":{"name":"B","colors":{"primary":"#123","text":"#fff","background":"#000"}},
		"scenes":[{"id":"h","role":"hook","duration":2},{"id":"b","role":"before","duration":2},{"id":"a","role":"after","duration":2},{"id":"c","role":"cta","duration":2}],"cta":{"text":"Go"}}`)
	old := reelMemCheck
	t.Cleanup(func() { reelMemCheck = old })
	reelMemCheck = func() (bool, bool, string) { return true, true, "BLOCKED: memory used 80% (>= 75%)" }
	ctx, _ := goalsCtx(t, proj, false)
	err := runReel(ctx, []string{"render", sb, "--template", dir})
	if err == nil || !strings.Contains(err.Error(), "mem-guard check is blocked (BLOCKED: memory used 80%") || !strings.Contains(err.Error(), "starts Chrome") {
		t.Fatalf("err = %v", err)
	}
	// A storyboard missing its narration files is refused before mem-guard.
	mustWrite(t, sb, strings.Replace(readText(t, sb), `"duration":2}`, `"duration":2,"narration":{"text":"Hi.","voice":"narrator"}}`, 1))
	ctx, _ = goalsCtx(t, proj, false)
	if err := runReel(ctx, []string{"render", sb}); err == nil || !strings.Contains(err.Error(), "scenes[0].narration.audio: missing: make it with the project's TTS connector") {
		t.Fatalf("err = %v", err)
	}
}

func TestOutputAddKeepsAReportBesideTheFile(t *testing.T) {
	asLead(t)
	proj := outputsProject(t)
	t.Setenv("PATH", t.TempDir()) // no ffmpeg: no poster, the add still works
	dir := t.TempDir()
	video := filepath.Join(dir, "reel.mp4")
	report := filepath.Join(dir, "reel.mp4.qa.md")
	mustWrite(t, video, "mp4")
	mustWrite(t, report, "# Reel QA: pass\n")
	o := addOutput(t, proj, "--department", "Marketing", "--title", "Reel", "--kind", "video", "--file", video, "--report", report)
	if o.Report == "" || !strings.HasPrefix(o.Report, "marketing/"+o.ID+"-report-") {
		t.Fatalf("report = %q", o.Report)
	}
	if b, err := os.ReadFile(filepath.Join(outputs.FilesDir(proj), o.Report)); err != nil || string(b) != "# Reel QA: pass\n" {
		t.Errorf("report file: %q %v", b, err)
	}
	ctx, _ := goalsCtx(t, proj, false)
	err := runOutput(ctx, []string{"add", "--department", "Marketing", "--title", "x", "--kind", "page", "--url", "https://x.example/", "--report", report})
	if err == nil || !strings.Contains(err.Error(), "use it with --file") {
		t.Fatalf("err = %v", err)
	}
}

// TestReelShelveRefusesARedReelAndShelvesAGreenOne renders with the real
// ffmpeg and a stand-in renderer (no Remotion, no Chrome).
func TestReelShelveRefusesARedReelAndShelvesAGreenOne(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not on PATH")
	}
	asLead(t)
	proj := outputsProject(t)
	old := reelMemCheck
	t.Cleanup(func() { reelMemCheck = old })
	reelMemCheck = func() (bool, bool, string) { return true, false, "ok" }
	dir := t.TempDir()
	ff := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("ffmpeg", append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg: %v\n%s", err, out)
		}
	}
	ff("-f", "lavfi", "-i", "sine=f=330:d=1.2", "-af", "adelay=100|100", "-ar", "44100", filepath.Join(dir, "n.mp3"))
	mustWrite(t, filepath.Join(dir, "w.json"), `[{"word":"One","start":0.1,"end":0.4},{"word":"two.","start":0.45,"end":1.3}]`)
	scene := func(id, role string) string {
		return `{"id":"` + id + `","role":"` + role + `","duration":1.6,"narration":{"text":"One two.","voice":"narrator","audio":"n.mp3","alignment":"w.json"}}`
	}
	sb := filepath.Join(dir, "sb.json")
	mustWrite(t, sb, `{"version":1,"shape":"before-after","title":"T","brand":{"name":"B","colors":{"primary":"#123","text":"#fff","background":"#000"}},
		"scenes":[`+scene("h", "hook")+`,`+scene("b", "before")+`,`+scene("a", "after")+`,`+scene("c", "cta")+`],"cta":{"text":"Go"}}`)
	tmpl := t.TempDir()
	standin.Write(t, filepath.Join(tmpl, "render.sh"), `exec ffmpeg -hide_banner -loglevel error -nostdin -y -f lavfi -i "color=c=0x335577:s=$4x$5:r=30" -t "$3" -c:v libx264 -preset ultrafast -pix_fmt yuv420p "$2"
`)
	mustWrite(t, filepath.Join(tmpl, "reel.json"), `{"render":["./render.sh","{composition}","{out}","{duration}","{width}","{height}"],"compositions":{"before-after":"BeforeAfter"}}`)

	ctx, out := goalsCtx(t, proj, false)
	if err := runReel(ctx, []string{"render", sb, "--template", tmpl}); err != nil {
		t.Fatal(err)
	}
	mp4 := filepath.Join(dir, "sb.mp4")
	assertContains(t, out.String(), "✓ Rendered "+mp4+": before-after, 4 scenes, 6.80 s, 4 caption cues")
	assertContains(t, out.String(), "Next: issue-cli reel qa "+mp4+" --storyboard "+sb)

	// Red: the storyboard asks for at least 20 s.
	red := filepath.Join(dir, "red.json")
	mustWrite(t, red, strings.Replace(readText(t, sb), `"cta":{"text":"Go"}`, `"cta":{"text":"Go"},"qa":{"min_seconds":20}`, 1))
	ctx, out = goalsCtx(t, proj, false)
	err := runReel(ctx, []string{"qa", mp4, "--storyboard", red, "--template", tmpl})
	if reelExitCode(err) != 1 {
		t.Fatalf("qa err = %v, want exit 1", err)
	}
	assertContains(t, out.String(), "QA: FAIL (1 of 7 checks)")
	assertContains(t, out.String(), "✗ duration: 6.80 s (plan 6.80 s) (bound: 20-90 s")
	if _, err := os.Stat(mp4 + ".qa.json"); err != nil {
		t.Error(err)
	}
	ctx, _ = goalsCtx(t, proj, false)
	err = runReel(ctx, []string{"shelve", mp4, "--storyboard", red, "--template", tmpl, "--title", "Reel", "--department", "Marketing"})
	var ce *exitCodeError
	if !errors.As(err, &ce) || ce.Code != 1 || !strings.Contains(ce.Msg, "Not shelved") {
		t.Fatalf("shelve red: %v", err)
	}
	if list, _ := outputs.Load(proj); len(list) != 0 {
		t.Fatalf("a red reel reached the shelf: %+v", list)
	}

	// Green: on the shelf in review, the QA in its note and beside it.
	ctx, out = goalsCtx(t, proj, false)
	if err := runReel(ctx, []string{"shelve", mp4, "--storyboard", sb, "--template", tmpl, "--title", "Reel: before and after", "--department", "Marketing"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "✓ On Marketing's shelf: ")
	assertContains(t, out.String(), "(shown under the player)")
	list, _ := outputs.Load(proj)
	if len(list) != 1 {
		t.Fatalf("shelf = %+v", list)
	}
	o := list[0]
	if o.Kind != "video" || o.State != outputs.StateReview || !strings.HasPrefix(o.Note, "QA pass (7 checks) · duration 6.80 s") || o.Report == "" {
		t.Fatalf("output = %+v", o)
	}
	md := readText(t, filepath.Join(outputs.FilesDir(proj), o.Report))
	assertContains(t, md, "## Reel QA: pass")
	assertContains(t, md, "| caption-sync | pass |")

	// A 4:5 feed post from the same material: rendered at 1080x1350,
	// held to 4:5 and shelved like a reel.
	post := filepath.Join(dir, "post.json")
	mustWrite(t, post, strings.Replace(readText(t, sb), `"shape":"before-after",`, `"shape":"before-after","format":"4:5",`, 1))
	ctx, _ = goalsCtx(t, proj, false)
	if err := runReel(ctx, []string{"render", post, "--template", tmpl}); err != nil {
		t.Fatal(err)
	}
	postMP4 := filepath.Join(dir, "post.mp4")
	if p, err := reel.LoadPlan(reel.PlanPath(postMP4)); err != nil || p.Format != "4:5" || p.Width != 1080 || p.Height != 1350 {
		t.Fatalf("4:5 plan = %+v, %v", p, err)
	}
	ctx, out = goalsCtx(t, proj, false)
	if err := runReel(ctx, []string{"shelve", postMP4, "--storyboard", post, "--template", tmpl, "--title", "Post: before and after", "--department", "Marketing"}); err != nil {
		t.Fatalf("shelve 4:5: %v\n%s", err, readText(t, postMP4+".qa.json"))
	}
	if list, _ := outputs.Load(proj); len(list) != 2 {
		t.Fatalf("shelf after the 4:5 post = %+v", list)
	}
	// The 9:16 file against the 4:5 storyboard fails its aspect.
	ctx, out = goalsCtx(t, proj, false)
	if err := runReel(ctx, []string{"qa", mp4, "--storyboard", post, "--template", tmpl}); reelExitCode(err) != 1 {
		t.Fatalf("qa 9:16 file on a 4:5 storyboard: %v", err)
	}
	assertContains(t, out.String(), "✗ aspect: 1080x1920")
	assertContains(t, out.String(), "(bound: 4:5 (0.8000))")
}

// TestReelDocNamesEveryCommand keeps docs/reels.md naming each reel
// subcommand and the QA checks.
func TestReelDocNamesEveryCommand(t *testing.T) {
	doc := readText(t, filepath.Join("..", "..", "docs", "reels.md"))
	for _, sub := range reelCommand.Subcommands {
		if !strings.Contains(doc, "issue-cli reel "+sub) {
			t.Errorf("docs/reels.md does not name issue-cli reel %s", sub)
		}
	}
	for _, c := range []string{"duration", "aspect", "loudness", "black-frames", "safe-zone", "caption-sync", "caption-scene", "remotion.dev/license"} {
		if !strings.Contains(doc, c) {
			t.Errorf("docs/reels.md does not name %s", c)
		}
	}
}
