package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/ci"
	"github.com/kacperwerema/dark-factory/internal/clean"
	"github.com/kacperwerema/dark-factory/internal/diskuse"
	"github.com/kacperwerema/dark-factory/internal/gitmain"
	"github.com/kacperwerema/dark-factory/internal/health"
	"github.com/kacperwerema/dark-factory/internal/playtestruns"
	"github.com/kacperwerema/dark-factory/internal/procs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var cleanCommand = &Command{
	Name:      "clean",
	ShortHelp: "The nightly clean: trim the Go build cache to its cap, old agent transcripts, health snapshots, merged worktrees, playtest runs",
	LongHelp: `Free the disk the factory fills, once for the whole machine (every
project in projects.yaml). The viewer runs it every night at clean.at
(04:00) and early while the data volume is over 85% used; by hand:

  issue-cli clean --dry-run    what each area would free; deletes nothing
  issue-cli clean              clean now

Areas, one report line each with the MB freed, then the volume's use
before and after:
  gocache       Go's build cache over clean.gocache_cap_gb (40): the
                oldest entries go until it is under the cap
  transcripts   *.jsonl under Claude Code's projects/ and Codex's
                sessions/ older than clean.transcript_days (14); never a
                memory/ folder, never another file
  snapshots     .health/snapshots: the last clean.snapshot_days (7) in
                full, one per older day
  worktrees     as issue-cli worktrees prune (merged, clean, idle)
  playtest      as issue-cli playtest prune --all

It changes nothing and says why while a local CI run is live, a go build
or test runs, or mem-guard says no; the viewer tries again on its next
tick. Live workers don't hold it: the cache loses only its oldest
entries, as Go's own trim does beside other builds. projects.yaml:

  clean:
    off: true          # no clean at all
    at: "04:00"
    gocache_cap_gb: 40
    transcript_days: 14
    snapshot_days: 7
  projects:
    - slug: x
      clean_off: true  # leave this project's snapshots, worktrees, runs

  --dry-run   report what would go; delete nothing (holds don't apply)
  --if-due    only when due (the viewer's call): no clean since tonight's
              slot, or the volume over 85% and the last clean 6 h ago
  --json      the result as JSON

The last clean and its result show on the Needs you disk row and the
storage health family (storage.clean_freed_mb).`,
	Run: runClean,
}

func init() {
	registerCommand(cleanCommand)
}

// Vars for tests: where the machine's areas are and what holds a clean.
var (
	// cleanMachine is false in a test binary and a playtest scenario
	// (diskuse.Live): a clean then refuses unless a test points it at
	// temp dirs and sets it.
	cleanMachine        = diskuse.Live
	cleanGoCacheDir     = diskuse.GoCacheDir
	cleanTranscriptDirs = diskuse.TranscriptDirs
	cleanVolume         = cleanVolumeNow
	cleanHolds          = cleanHoldsNow
	cleanGoBusy         = func() string { ps, _ := procs.Mine(); return clean.GoBusy(ps) }
	cleanStatePath      = clean.Path
)

func runClean(ctx *Context, args []string) error {
	fs := newFlagSet("clean", ctx)
	dry := fs.Bool("dry-run", false, "report what would go; delete nothing")
	ifDue := fs.Bool("if-due", false, "only when due (the viewer's call)")
	asJSON := fs.Bool("json", false, "the result as JSON")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return unexpectedArgError(fs, fs.Arg(0))
	}
	if !cleanMachine {
		return fmt.Errorf("clean: not in a test binary or a playtest scenario (it would clean this machine)")
	}
	cfg := cleanConfig(ctx)
	w := ctx.Stdout
	if !cfg.Enabled() && !*dry {
		fmt.Fprintln(w, "clean: off (clean.off in projects.yaml)")
		return nil
	}
	if !*dry && ctx.Project != nil {
		issues, _ := tracker.LoadIssues(ctx.Project.IssueDir)
		if me := guardWho(ctx, issues, "clean"); me.Role == tracker.RoleWorker {
			guardNever(ctx, "clean")
			return fmt.Errorf("workers don't clean the machine (you are %s): --dry-run only reports; the viewer runs it every night", valueOr(me.Name, "a worker"))
		}
	}
	path := cleanStatePath()
	now := ctx.now()
	before := cleanVolume()
	why := "by hand"
	if *ifDue {
		h, m := cfg.Clock()
		due, dueWhy := clean.Due(clean.Load(path), h, m, before, now)
		if !due {
			return nil
		}
		why = dueWhy
	}
	if !*dry {
		release, err := clean.Lock(path)
		if err != nil {
			fmt.Fprintf(w, "clean: %v\n", err)
			return nil
		}
		defer release()
		if holds := cleanHolds(ctx); len(holds) > 0 {
			held := strings.Join(holds, "; ")
			_ = clean.RecordHold(path, clean.Hold{At: now, Why: held})
			fmt.Fprintf(w, "clean waits: %s\n", held)
			return nil
		}
	}
	res := clean.Result{At: now, Why: why, Before: before}
	capBytes := int64(cfg.CapGB() * (1 << 30))
	if busy := cleanGoBusy(); busy != "" && !*dry {
		res.Areas = append(res.Areas, clean.Area{Name: "gocache", Note: "left alone: " + busy})
	} else {
		res.Areas = append(res.Areas, clean.GoCache(cleanGoCacheDir(), capBytes, *dry))
	}
	res.Areas = append(res.Areas, clean.Transcripts(cleanTranscriptDirs(), cfg.Transcripts(), now, *dry))
	res.Areas = append(res.Areas, cleanProjects(ctx, cfg, *dry)...)
	res.After = cleanVolume()
	res.Done = ctx.now()
	if !*dry {
		if err := clean.Record(path, res); err != nil {
			fmt.Fprintf(ctx.stderr(), "clean: the result wasn't recorded: %v\n", err)
		}
	}
	if *asJSON {
		return writeJSON(w, res)
	}
	title := "Clean"
	if *dry {
		title += " (dry run)"
	}
	fmt.Fprintf(w, "== %s: %s ==\n", title, why)
	for _, a := range res.Areas {
		fmt.Fprintln(w, a.Line())
	}
	fmt.Fprintf(w, "%-28s freed %9s", "total", clean.MB(res.Freed()))
	if res.Before.OK && res.After.OK {
		fmt.Fprintf(w, "  the volume %.0f%% → %.0f%% used (%.0f GB free)", res.Before.UsedPct, res.After.UsedPct, res.After.FreeGB)
	}
	fmt.Fprintln(w)
	return nil
}

// cleanConfig is projects.yaml's clean: block; nil (the defaults) without
// one or a readable file.
func cleanConfig(ctx *Context) *tracker.CleanConfig {
	if ctx.ConfigPath == "" {
		return nil
	}
	cfg, err := tracker.LoadProjectsConfig(ctx.ConfigPath)
	if err != nil {
		return nil
	}
	return cfg.Clean
}

// cleanProjects runs the per-project areas, once per main checkout, for
// every project without clean_off.
func cleanProjects(ctx *Context, cfg *tracker.CleanConfig, dry bool) []clean.Area {
	projects := ctx.AllProjects
	if len(projects) == 0 && ctx.Project != nil {
		projects = []tracker.Project{*ctx.Project}
	}
	var out []clean.Area
	seen := map[string]bool{}
	for i := range projects {
		proj := &projects[i]
		if proj.CleanOff {
			continue
		}
		dir := projectRoot(proj)
		if dir == "" {
			continue
		}
		root := gitmain.Root(dir)
		if root == "" || seen[root] {
			continue
		}
		seen[root] = true
		pctx := *ctx
		pctx.Project = proj
		out = append(out, clean.Snapshots("snapshots "+proj.Slug, health.SnapshotsDir(root), cfg.Snapshots(), ctx.now(), dry))
		out = append(out, cleanWorktrees(&pctx, dry))
		out = append(out, cleanPlaytest(&pctx, root, dry))
	}
	return out
}

// cleanWorktrees is worktrees prune's sweep, with the MB the removed trees
// held.
func cleanWorktrees(ctx *Context, dry bool) clean.Area {
	a := clean.Area{Name: "worktrees " + ctx.Project.Slug}
	in, err := pruneInput(ctx)
	if err != nil {
		a.Err = err.Error()
		return a
	}
	plan, err := agentrun.Sweep(in, true)
	if err != nil {
		a.Err = err.Error()
		return a
	}
	sizes := map[string]int64{}
	for _, v := range plan {
		if v.Kind == agentrun.TreeRemoved {
			sizes[v.Path] = treeBytes(v.Path)
		}
	}
	verdicts := plan
	if !dry && len(sizes) > 0 {
		if verdicts, err = agentrun.Sweep(in, false); err != nil {
			a.Err = err.Error()
			return a
		}
	}
	kept := 0
	for _, v := range verdicts {
		if v.Kind == agentrun.TreeRemoved {
			a.Files++
			a.FreedBytes += sizes[v.Path]
		} else {
			kept++
		}
	}
	a.Note = fmt.Sprintf("%d worktrees removed, %d kept", a.Files, kept)
	a.Files = 0
	return a
}

// cleanPlaytest is playtest prune --all.
func cleanPlaytest(ctx *Context, root string, dry bool) clean.Area {
	a := clean.Area{Name: "playtest " + ctx.Project.Slug}
	targets, _, err := playtestPruneAll(ctx.Project, root)
	if err != nil {
		a.Err = err.Error()
		return a
	}
	for _, is := range targets {
		rep, err := playtestruns.Prune(root, is.Slug, ctx.Project.IssueDone(is), dry)
		if err != nil {
			a.Err = is.Slug + ": " + err.Error()
			continue
		}
		a.Files += rep.Deleted
		a.FreedBytes += rep.DeletedBytes + rep.CacheBytes
	}
	a.Note = fmt.Sprintf("%d worktrees' runs", len(targets))
	return a
}

// treeBytes is the size of every file under dir (no symlinks followed).
func treeBytes(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

// cleanVolumeNow is the data volume the cache is on, else the home's.
func cleanVolumeNow() diskuse.Volume {
	if dir := cleanGoCacheDir(); dir != "" {
		if v := diskuse.VolumeOf(dir); v.OK {
			return v
		}
	}
	home, _ := os.UserHomeDir()
	return diskuse.VolumeOf(home)
}

// cleanHoldsNow is what holds a clean on this machine: a live local CI
// run in any project, a go build or test, mem-guard. Live workers don't
// (factory/a-nightly-clean-job-…#D-6): auto-start keeps them live around
// the clock, and the cache trim deletes only the oldest entries, as Go's
// own trim does beside other builds.
func cleanHoldsNow(ctx *Context) []string {
	var holds []string
	projects := ctx.AllProjects
	if len(projects) == 0 && ctx.Project != nil {
		projects = []tracker.Project{*ctx.Project}
	}
	seen := map[string]bool{}
	for i := range projects {
		proj := &projects[i]
		root := ci.RepoRoot(projectRoot(proj))
		if root == "" || seen[root] {
			continue
		}
		seen[root] = true
		if l, alive := ci.ReadLock(root); alive {
			holds = append(holds, fmt.Sprintf("a local CI run is live in %s (pid %d)", proj.Slug, l.PID))
		}
	}
	if busy := cleanGoBusy(); busy != "" {
		holds = append(holds, busy)
	}
	if ok, why := autostart.MemGuardAllows(); !ok {
		holds = append(holds, why)
	}
	return holds
}
