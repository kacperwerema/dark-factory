package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/cmd/issue-cli/templates"
	"github.com/kacperwerema/dark-factory/internal/jsonfile"
	"github.com/kacperwerema/dark-factory/internal/memguard"
	"github.com/kacperwerema/dark-factory/internal/reel"
)

var reelCommand = &Command{
	Name:      "reel",
	ShortHelp: "The reel kit: storyboard, render, QA and the shelf for reels in 9:16, 4:5 or 1:1 (reel init|schema|validate|render|qa|shelve)",
	LongHelp: `Make a short video (a reel) from a storyboard JSON: the kit fills
the project's Remotion template with it, encodes the MP4 with FFmpeg, checks
it and puts it with its QA report on the shelf for the owner's review.
The kit reads files only: the narration audio, its word timings and any AI
clips come from the project's connectors first, so the kit spends nothing.
The storyboard's format: is the frame: 9:16 1080x1920 (the default), 4:5
1080x1350 (a feed post) or 1:1 1080x1080; render, the captions and QA
follow it. docs/reels.md is the page.

  reel init [<dir>]
      Copy the starter Remotion template (shapes HookList, BeforeAfter,
      AppDemo) into <dir> (default reels/template). Refuses when any of
      its files exists already. The copy is the project's to change; then
      npm install there (Remotion is its npm dependency, under Remotion's
      licence: free for companies of up to 3 people).
  reel schema
      Print the storyboard's JSON Schema.
  reel validate <storyboard.json> [--render]
      Every problem, each at its JSON path; exit 1 on any. --render also
      wants each narration's audio and word timings, as render does.
  reel render <storyboard.json> [--template <dir>] [--out <file.mp4>]
      Work out the plan (scene starts, word timings, caption cues and
      boxes), run the template's render command from its reel.json, then
      FFmpeg: narration at its scene starts, music under it, -14 LUFS,
      H.264/AAC at the format's frame. Writes <out>, <name>.plan.json and
      <name>.narration.wav. Runs mem-guard check first (Remotion starts
      Chrome) and refuses when it is blocked. Default --out: the
      storyboard's name with .mp4, beside it.
  reel qa <file.mp4> --storyboard <storyboard.json> [--template <dir>] [--plan <file>] [--narration <file>]
      Duration, aspect (the storyboard's format), loudness (-14 LUFS
      ±2), black frames, captions in the format's safe zones, captions
      in sync with the audio.
      One line per check; writes <file.mp4>.qa.json; exit 1 on a fail.
      Bounds: the kit's, then the template's reel.json qa:, then the
      storyboard's qa:.
  reel shelve <file.mp4> --storyboard <storyboard.json> --title "..." [--issue <slug>] [--department <name>] [--template <dir>]
      Run QA; refuse a reel that fails it; else put it on the shelf as one
      video output in review, the QA summary as its note and the report
      beside it (the shelf shows it under the player).

Examples:
  issue-cli reel init
  issue-cli reel validate reels/week-plan/storyboard.json
  issue-cli reel render reels/week-plan/storyboard.json
  issue-cli reel qa reels/week-plan/storyboard.mp4 --storyboard reels/week-plan/storyboard.json
  issue-cli reel shelve reels/week-plan/storyboard.mp4 --storyboard reels/week-plan/storyboard.json --title "Reel: a week's plan in three taps"`,
	Run:         runReel,
	Subcommands: []string{"init", "schema", "validate", "render", "qa", "shelve"},
}

func init() { registerCommand(reelCommand) }

// reelNeedsNoProject: every reel subcommand but shelve works on the files
// it is given, so it runs outside a project too (a template folder, a
// scratch copy); shelve writes to a project's shelf.
func reelNeedsNoProject(args []string) bool {
	return len(args) == 0 || args[0] != "shelve"
}

// reelMemCheck is mem-guard check (a test stands in for it).
var reelMemCheck = memguard.Check

func runReel(ctx *Context, args []string) error {
	if len(args) == 0 {
		return &usageError{msg: "usage: issue-cli reel init|schema|validate|render|qa|shelve ... (issue-cli help reel)"}
	}
	switch args[0] {
	case "init":
		return runReelInit(ctx, args[1:])
	case "schema":
		fset := newFlagSet("reel schema", ctx)
		if err := parseFlagsArgs(ctx, fset, args[1:]); err != nil {
			return err
		}
		if fset.NArg() > 0 {
			return &usageError{msg: "usage: issue-cli reel schema"}
		}
		_, err := ctx.Stdout.Write(reel.Schema)
		return err
	case "validate":
		return runReelValidate(ctx, args[1:])
	case "render":
		return runReelRender(ctx, args[1:])
	case "qa":
		return runReelQA(ctx, args[1:])
	case "shelve":
		return runReelShelve(ctx, args[1:])
	}
	return &usageError{msg: fmt.Sprintf("unknown reel command %q: init, schema, validate, render, qa or shelve", args[0])}
}

func runReelInit(ctx *Context, args []string) error {
	fset := newFlagSet("reel init", ctx)
	if err := parseFlagsArgs(ctx, fset, args); err != nil {
		return err
	}
	if fset.NArg() > 1 {
		return &usageError{msg: "usage: issue-cli reel init [<dir>]"}
	}
	dir := reel.DefaultTemplateDir
	if fset.NArg() == 1 {
		dir = fset.Arg(0)
	}
	var files, there []string
	err := fs.WalkDir(templates.FS, "reel", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(p, "reel/")
		files = append(files, rel)
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err == nil {
			there = append(there, rel)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(there) > 0 {
		return fmt.Errorf("%s already has %s: reel init never overwrites a file (the copy there is the project's own); pick another <dir>, or move them aside first", dir, strings.Join(there, ", "))
	}
	for _, rel := range files {
		data, err := templates.FS.ReadFile(path.Join("reel", rel))
		if err != nil {
			return err
		}
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		if _, err := f.Write(data); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	fmt.Fprintf(ctx.Stdout, "✓ Wrote the reel template (%d files) to %s: shapes %s\n", len(files), dir, strings.Join(reel.Shapes, ", "))
	fmt.Fprintln(ctx.Stdout, "It is this project's copy now: change it freely (README.md there says what the kit expects of it).")
	fmt.Fprintln(ctx.Stdout, "Licence: Remotion is not in the copy; npm install fetches it under Remotion's licence (free for individuals and companies of up to 3 people, https://www.remotion.dev/license).")
	fmt.Fprintf(ctx.Stdout, "Next: (cd %s && npm install), then issue-cli reel validate %s\n", dir, filepath.Join(dir, "examples", "hook-list.json"))
	return nil
}

func loadStoryboard(fset *flag.FlagSet, usage string) (*reel.Storyboard, string, error) {
	if fset.NArg() != 1 {
		return nil, "", &usageError{msg: usage}
	}
	s, err := reel.Load(fset.Arg(0))
	return s, fset.Arg(0), err
}

func runReelValidate(ctx *Context, args []string) error {
	fset := newFlagSet("reel validate", ctx)
	forRender := fset.Bool("render", false, "also want each narration's audio and word timings, as render does")
	if err := parseFlagsArgs(ctx, fset, args); err != nil {
		return err
	}
	s, file, err := loadStoryboard(fset, "usage: issue-cli reel validate <storyboard.json> [--render]")
	if err != nil {
		return err
	}
	ps := s.Problems()
	if *forRender {
		ps = s.RenderProblems()
	}
	if ctx.JSONOutput {
		if ps == nil {
			ps = []reel.Problem{}
		}
		if err := writeJSON(ctx.Stdout, map[string]any{"file": file, "ok": len(ps) == 0, "problems": ps}); err != nil {
			return err
		}
		if len(ps) > 0 {
			return &exitCodeError{Code: 1}
		}
		return nil
	}
	if len(ps) > 0 {
		fmt.Fprintf(ctx.Stdout, "✗ %s: %d problem(s)\n", file, len(ps))
		for _, p := range ps {
			fmt.Fprintf(ctx.Stdout, "  %s\n", p)
		}
		fmt.Fprintln(ctx.Stdout, "The format: issue-cli reel schema; the shapes' roles: issue-cli help reel")
		return &exitCodeError{Code: 1}
	}
	fmt.Fprintf(ctx.Stdout, "✓ %s: a valid %s storyboard (%d scenes; roles %s)\n", file, s.Shape, len(s.Scenes), reel.ShapeRoles(s.Shape))
	if !*forRender {
		if rp := s.RenderProblems(); len(rp) > 0 {
			fmt.Fprintf(ctx.Stdout, "Before render: %d narration file(s) to make with the TTS connector (issue-cli reel validate --render lists them)\n", len(rp))
			return nil
		}
	}
	fmt.Fprintf(ctx.Stdout, "Next: issue-cli reel render %s\n", file)
	return nil
}

func runReelRender(ctx *Context, args []string) error {
	fset := newFlagSet("reel render", ctx)
	tmpl := fset.String("template", reel.DefaultTemplateDir, "the template project (its reel.json)")
	out := fset.String("out", "", "the MP4 to write (default: the storyboard's name with .mp4, beside it)")
	if err := parseFlagsArgs(ctx, fset, args); err != nil {
		return err
	}
	s, file, err := loadStoryboard(fset, "usage: issue-cli reel render <storyboard.json> [--template <dir>] [--out <file.mp4>]")
	if err != nil {
		return err
	}
	if *out == "" {
		*out = strings.TrimSuffix(file, filepath.Ext(file)) + ".mp4"
	}
	if ps := s.RenderProblems(); len(ps) > 0 {
		return reel.ProblemsError(ps)
	}
	if found, blocked, msg := reelMemCheck(); found && blocked {
		return fmt.Errorf("mem-guard check is blocked (%s): a render starts Chrome; wait for a worker to finish, then run it again", msg)
	}
	tools, err := reel.FindTools()
	if err != nil {
		return err
	}
	res, err := reel.Render(context.Background(), s, reel.RenderOptions{Template: *tmpl, Out: *out, Tools: tools, Log: ctx.Stderr})
	if err != nil {
		return err
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, map[string]any{"out": res.Out, "plan": res.PlanFile, "narration": res.Narration, "duration": res.Plan.Duration, "cues": len(res.Plan.Cues)})
	}
	fmt.Fprintf(ctx.Stdout, "✓ Rendered %s: %s, %d scenes, %.2f s, %d caption cues\n", res.Out, s.Shape, len(res.Plan.Scenes), res.Plan.Duration, len(res.Plan.Cues))
	fmt.Fprintf(ctx.Stdout, "  plan       %s (the template's props; QA reads its captions)\n", res.PlanFile)
	fmt.Fprintf(ctx.Stdout, "  narration  %s (QA's sync check reads it)\n", res.Narration)
	fmt.Fprintf(ctx.Stdout, "Next: issue-cli reel qa %s --storyboard %s\n", res.Out, file)
	return nil
}

// reelQA runs QA on mp4 for the storyboard and writes the report beside it.
func reelQA(ctx *Context, mp4, sbFile, tmpl, planFile, narration string, tmplSet bool) (*reel.Report, error) {
	s, err := reel.Load(sbFile)
	if err != nil {
		return nil, err
	}
	if ps := s.RenderProblems(); len(ps) > 0 {
		return nil, reel.ProblemsError(ps)
	}
	if _, err := os.Stat(mp4); err != nil {
		return nil, err
	}
	b := reel.DefaultBoundsFor(s.Frame())
	if tc, err := reel.LoadTemplate(tmpl); err == nil {
		b.Apply(tc.QA, filepath.Join(tmpl, reel.TemplateConfigFile))
	} else if tmplSet {
		return nil, err
	}
	b.Apply(s.QA, "storyboard")
	tools, err := reel.FindTools()
	if err != nil {
		return nil, err
	}
	bg := context.Background()
	fresh, err := reel.BuildPlan(s, tools.AudioSeconds(bg))
	if err != nil {
		return nil, err
	}
	in := reel.QAInput{File: mp4, Storyboard: sbFile, Fresh: fresh, Bounds: b}
	if planFile == "" {
		if p := reel.PlanPath(mp4); reelFileExists(p) {
			planFile = p
		}
	}
	if planFile != "" {
		if in.Drawn, err = reel.LoadPlan(planFile); err != nil {
			return nil, err
		}
		in.PlanFile = planFile
	}
	if narration == "" {
		if p := reel.NarrationPath(mp4); reelFileExists(p) {
			narration = p
		}
	}
	in.Narration = narration
	r, err := reel.RunQA(bg, tools, in, ctx.now())
	if err != nil {
		return nil, err
	}
	if err := jsonfile.Write(reel.ReportPath(mp4), r, 0o644); err != nil {
		return nil, err
	}
	return r, nil
}

func runReelQA(ctx *Context, args []string) error {
	fset := newFlagSet("reel qa", ctx)
	sb := fset.String("storyboard", "", "the storyboard the reel was rendered from (required)")
	tmpl := fset.String("template", reel.DefaultTemplateDir, "the template project, for its reel.json qa: bounds")
	plan := fset.String("plan", "", "the plan render wrote (default <name>.plan.json beside the MP4)")
	narration := fset.String("narration", "", "the narration stem (default <name>.narration.wav beside the MP4; without one the reel's own audio)")
	if err := parseFlagsArgs(ctx, fset, args); err != nil {
		return err
	}
	if fset.NArg() != 1 || *sb == "" {
		return &usageError{msg: "usage: issue-cli reel qa <file.mp4> --storyboard <storyboard.json> [--template <dir>] [--plan <file>] [--narration <file>]"}
	}
	mp4 := fset.Arg(0)
	r, err := reelQA(ctx, mp4, *sb, *tmpl, *plan, *narration, flagWasSet(fset, "template"))
	if err != nil {
		return err
	}
	if ctx.JSONOutput {
		if err := writeJSON(ctx.Stdout, r); err != nil {
			return err
		}
	} else {
		fmt.Fprint(ctx.Stdout, r.Summary())
		fmt.Fprintf(ctx.Stdout, "Report: %s\n", reel.ReportPath(mp4))
	}
	if !r.Pass {
		if !ctx.JSONOutput {
			fmt.Fprintf(ctx.Stdout, "Fix the storyboard or the template, then issue-cli reel render %s again.\n", *sb)
		}
		return &exitCodeError{Code: 1}
	}
	if !ctx.JSONOutput {
		fmt.Fprintf(ctx.Stdout, "Next: issue-cli reel shelve %s --storyboard %s --title \"...\"\n", mp4, *sb)
	}
	return nil
}

func runReelShelve(ctx *Context, args []string) error {
	fset := newFlagSet("reel shelve", ctx)
	sb := fset.String("storyboard", "", "the storyboard the reel was rendered from (required)")
	title := fset.String("title", "", "the output's title on the shelf (required)")
	slug := fset.String("issue", "", "the issue that made it (default: ISSUE_VIEWER_ISSUE_SLUG)")
	dept := fset.String("department", "", "the department, when there is no --issue")
	tmpl := fset.String("template", reel.DefaultTemplateDir, "the template project, for its reel.json qa: bounds")
	if err := parseFlagsArgs(ctx, fset, args); err != nil {
		return err
	}
	if fset.NArg() != 1 || *sb == "" || strings.TrimSpace(*title) == "" {
		return &usageError{msg: `usage: issue-cli reel shelve <file.mp4> --storyboard <storyboard.json> --title "..." [--issue <slug>] [--department <name>] [--template <dir>]`}
	}
	if ctx.Project == nil {
		return errors.New("reel shelve puts the reel on a project's shelf: run it in a project (--project <slug>)")
	}
	mp4 := fset.Arg(0)
	r, err := reelQA(ctx, mp4, *sb, *tmpl, "", "", flagWasSet(fset, "template"))
	if err != nil {
		return err
	}
	if !r.Pass {
		fmt.Fprint(ctx.Stderr, r.Summary())
		return &exitCodeError{Code: 1, Msg: fmt.Sprintf("✗ Not shelved: %s failed QA (report %s). The owner sees only reels that pass: fix it, render it again, then shelve it.", mp4, reel.ReportPath(mp4))}
	}
	md := mp4 + ".qa.md"
	if err := os.WriteFile(md, []byte(r.Markdown()), 0o644); err != nil {
		return err
	}
	add := []string{"--title", *title, "--kind", "video", "--file", mp4, "--report", md, "--note", qaNote(r)}
	if *slug != "" {
		add = append(add, "--issue", *slug)
	}
	if *dept != "" {
		add = append(add, "--department", *dept)
	}
	return runOutputAdd(ctx, add)
}

// qaNote is the QA verdict in one line, the output's note.
func qaNote(r *reel.Report) string {
	parts := []string{fmt.Sprintf("QA pass (%d checks)", len(r.Checks))}
	for _, c := range r.Checks {
		switch c.Name {
		case reel.CheckDuration, reel.CheckLoudness:
			parts = append(parts, c.Name+" "+c.Measured)
		}
	}
	return strings.Join(parts, " · ")
}

func reelFileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
