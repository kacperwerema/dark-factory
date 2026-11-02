package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/ci"
	"github.com/kacperwerema/dark-factory/internal/milestone"
	"github.com/kacperwerema/dark-factory/internal/privacy"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/yolo"
)

var releaseCommand = &Command{
	Name:      "release",
	ShortHelp: "Cut, soak and publish a release on its branch (sub: setup|cut|pick|publish)",
	LongHelp: `A release is cut as a branch, soaks there as release candidates, and is
published from it (docs/roadmap.md, Cutting a release). With privacy.code:
snapshots the development history stays in a private dev repo (origin) and the
public repo (remote public) gets ONE commit per release.

Subcommands:
  setup --dev <url>
      Once, for code: snapshots. origin becomes the remote public (its tags never
      fetched), <url> (the private dev repo the owner created) becomes origin and
      gets every branch and every non-candidate tag. Installs the pre-push guard.
      Nothing is pushed to public; cleaning its old history is a separate step.
  cut <version>
      When Waiting for release says the milestone is ready (or a pick needs the
      next candidate): creates release/vX.Y from main with its worktree
      .worktrees/release-vX.Y, and tags the next <version>-rc.N on it (never
      pushed). With code: snapshots the branch goes to origin. Main moves on.
      A patch (v0.N.1) tags v0.N.1-rc.K on the existing release/v0.N, with no
      milestone check, once go build and go vet pass in its worktree.
  pick [--release <version>] <slug|sha>...
      Cherry-picks (with -x) an issue's commits (work/<slug>, or for a done issue
      the commits on main with Refs: <slug>) or a commit onto the release branch,
      in its worktree. A conflict names the commit and files and leaves the
      branch as it was; the branch's ## Unreleased gets only the picked lines.
      Then go build and go vet run there. Fixes land on main first; nothing is
      fixed on the branch itself. A pick after the newest candidate needs the
      next one.
  publish <version> [--first] [--dry-run]
      Only on the owner's ok on a soaked candidate, from the release worktree.
      Names the changelog heading on the branch, pushes the branch tip's tree as
      ONE commit onto public's branch (subject "<version>", body the release's
      changelog section; the public repo's release Action tags and builds it),
      tags <version> on the dev branch, pushes both to origin, and moves the
      released lines of main's ## Unreleased under the version. No release PR:
      the snapshot is the bundle. --first accepts a public tip that is the old
      history. --dry-run runs the checks and shows the parent, the message and
      what the commit changes, and writes nothing (YOLO and the card are
      reported, not refused). Both refuse a tree with a user home path,
      a scratchpad path or a name from the private-names list (docs/workflow.md,
      The leak scan).

cut and publish read the viewer's Waiting for release card and refuse unless it
says the same (ready to cut; soaking). A patch's publish reads the card's patch
row; its cut reads no card.`,
	Run:         runRelease,
	Subcommands: []string{"setup", "cut", "pick", "publish"},
}

func init() {
	registerCommand(releaseCommand)
}

func runRelease(ctx *Context, args []string) error {
	if len(args) == 0 {
		return &usageError{msg: "release requires a subcommand\n\nUsage:\n  issue-cli release setup --dev <url>\n  issue-cli release cut <version>\n  issue-cli release pick [--release <version>] <slug|sha>...\n  issue-cli release publish <version> [--first] [--dry-run]"}
	}
	if ctx.Project == nil {
		return fmt.Errorf("release needs a project — pass --project <slug> or run from a project root")
	}
	switch args[0] {
	case "setup":
		return runReleaseSetup(ctx, args[1:])
	case "cut":
		return runReleaseCut(ctx, args[1:])
	case "pick":
		return runReleasePick(ctx, args[1:])
	case "publish":
		return runReleasePublish(ctx, args[1:])
	}
	return &usageError{msg: fmt.Sprintf("unknown release subcommand %q (setup, cut, pick, publish)", args[0])}
}

const snapshotsNotChosen = `privacy.code is not snapshots in workflow.yaml: the owner has not chosen private development.
Ask them, then record it:
  privacy:
    code: snapshots
    decided: "<date>, owner: \"<their words>\""
(issue-cli workflow upgrade shows the privacy entry and its steps)`

func runReleaseSetup(ctx *Context, args []string) error {
	fs := newFlagSet("release setup", ctx)
	dev := fs.String("dev", "", "URL of the private dev repo (the owner creates it)")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	cfg := ctx.Project.LoadWorkflow().Privacy
	if !cfg.Snapshots() {
		return fmt.Errorf("%s", snapshotsNotChosen)
	}
	if strings.TrimSpace(*dev) == "" {
		return &usageError{msg: "release setup needs --dev <url>: the private dev repo the owner created (agents never create repos)"}
	}
	root := privacy.MainCheckout(ctx.Project.TelemetryRoot())
	res, err := privacy.Setup(root, strings.TrimSpace(*dev), cfg)
	if err != nil {
		return err
	}
	out := ctx.Stdout
	if res.RenamedOrigin {
		fmt.Fprintf(out, "✓ The old origin is now the remote %s (tags never fetched from it)\n", cfg.Remote())
	}
	fmt.Fprintf(out, "✓ origin is the dev repo %s and has all the %s\n", *dev, strings.Join(res.Pushed, " and "))
	if res.SkippedRC > 0 {
		fmt.Fprintf(out, "✓ %d release candidate tags stay local\n", res.SkippedRC)
	}
	if res.Base != "" {
		fmt.Fprintf(out, "✓ %s/%s is at %s: the first snapshot goes on top of it\n", cfg.Remote(), cfg.Branch(), shortSha(res.Base))
	}
	if err := installPrivacyHook(ctx, root); err != nil {
		return err
	}
	fmt.Fprintf(out, `Next, with the owner:
  - The public repo keeps the history it has: the first snapshot goes on top of
    its tip, and every release after is one commit. See it first, from the
    release worktree: issue-cli release publish <version> --dry-run.
  - Only if that old history must go: clean it once from a mirror, never in
    this checkout: git clone --mirror <public url>, git filter-repo --path issues
    --path sessions --invert-paths (and the other work paths), git push --force
    --mirror. Then: git fetch %s && issue-cli release publish <version> --first.
  - The release workflow must skip the private dev repo, or it builds releases
    there too: if: ${{ !github.event.repository.private }} on its job.
  - Other machines and cloud sessions clone the dev repo, never the public one.
`, cfg.Remote())
	return nil
}

// releaseCard is the viewer's Waiting for release milestone (release.json).
type releaseCard struct {
	Version   string   `json:"version"`
	Cut       string   `json:"cut"`
	RC        bool     `json:"rc"`
	Ready     bool     `json:"ready"`
	CutNext   string   `json:"cut_next"`
	Soaking   bool     `json:"soaking"`
	Candidate string   `json:"candidate"`
	Branch    string   `json:"branch"`
	Unpicked  []string `json:"unpicked"`
	Checks    []struct {
		Key    string `json:"key"`
		Text   string `json:"text"`
		Detail string `json:"detail"`
		OK     bool   `json:"ok"`
	} `json:"checks"`
}

// fetchReleaseCard reads the card's milestone from the running viewer.
func fetchReleaseCard(ctx *Context) (*releaseCard, error) {
	ms, _, err := fetchReleaseRows(ctx)
	if err != nil {
		return nil, err
	}
	if ms == nil {
		return nil, fmt.Errorf("Waiting for release has no milestone to cut (every version is released, or no issue has a version:)")
	}
	return ms, nil
}

// fetchPatchCard reads the card's patch candidate (a vX.Y.Z-rc.N on an
// existing release branch); nil without one.
func fetchPatchCard(ctx *Context) (*releaseCard, error) {
	_, patch, err := fetchReleaseRows(ctx)
	return patch, err
}

// fetchReleaseRows reads release.json: the milestone and the patch row.
func fetchReleaseRows(ctx *Context) (*releaseCard, *releaseCard, error) {
	url := fmt.Sprintf("%s/p/%s/release.json", viewerBaseURL(ctx.Project), ctx.Project.Slug)
	body, err := viewerGet(url)
	if err != nil {
		return nil, nil, fmt.Errorf("can't read Waiting for release from the viewer at %s: %v\n\nStart it: issue-viewer --config %s\nRunning elsewhere? Set viewer_url in projects.yaml.", url, err, valueOr(ctx.ConfigPath, "<projects.yaml>"))
	}
	var doc struct {
		Milestone *releaseCard `json:"milestone"`
		Patch     *releaseCard `json:"patch"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", url, err)
	}
	return doc.Milestone, doc.Patch, nil
}

// unmet lists the card's failing checks, one per line.
func (c *releaseCard) unmet() string {
	var b strings.Builder
	if c.Cut == "" && len(c.Checks) == 0 {
		return "  ✗ no candidate of it on the card: cut one first (issue-cli release cut <version>)\n"
	}
	for _, ch := range c.Checks {
		if ch.OK {
			continue
		}
		fmt.Fprintf(&b, "  ✗ %s", ch.Text)
		if ch.Detail != "" {
			fmt.Fprintf(&b, ": %s", ch.Detail)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func runReleaseCut(ctx *Context, args []string) error {
	fs := newFlagSet("release cut", ctx)
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return &usageError{msg: "release cut takes the version: issue-cli release cut v0.54.0"}
	}
	cut, err := privacy.ParseVersion(fs.Arg(0))
	if err != nil {
		return &usageError{msg: err.Error()}
	}
	if milestone.IsPatch(cut) {
		return runReleaseCutPatch(ctx, cut)
	}
	card, err := fetchReleaseCard(ctx)
	if err != nil {
		return err
	}
	if milestone.Norm(card.Cut) != milestone.Norm(cut) {
		return fmt.Errorf("the milestone to cut is %s (version %s), not %s", card.Cut, card.Version, cut)
	}
	if card.CutNext == "" {
		why := card.unmet()
		if card.Soaking {
			why = "  " + card.Candidate + " is soaking and nothing was picked after it: publish it on the owner's ok, or pick a fix first\n"
		}
		return fmt.Errorf("Waiting for release does not say ready to cut %s:\n%s", cut, why)
	}
	wf := ctx.Project.LoadWorkflow()
	root := privacy.MainCheckout(ctx.Project.TelemetryRoot())
	from, err := ci.ResolveBranch(root, wf.CI)
	if err != nil || from == "" {
		from = "main"
	}
	res, err := privacy.Cut(root, cut, from, wf.Privacy)
	if err != nil {
		return err
	}
	out := ctx.Stdout
	if res.Created {
		fmt.Fprintf(out, "✓ Created %s from %s, worktree %s\n", res.Branch, from, res.Worktree)
	}
	fmt.Fprintf(out, "✓ Tagged %s on %s (local; never push a candidate tag)\n", res.Tag, res.Branch)
	if res.Pushed {
		fmt.Fprintf(out, "✓ Pushed %s to origin (the dev repo)\n", res.Branch)
	}
	fmt.Fprintf(out, `Next:
  - Install the candidate from the release worktree: cd %s && make install, then restart what runs it.
  - Tell the leads of the projects that run it; their issue-cli report-bug files join %s.
  - Fixes land on main as usual; pick them onto the branch: issue-cli release pick <slug>. Each pick needs the next candidate (issue-cli release cut %s).
`, res.Worktree, card.Version, cut)
	return nil
}

// runReleaseCutPatch tags the next candidate of a patch release (vX.Y.Z,
// Z > 0) on the existing release/vX.Y: no new branch, no milestone check.
// go build and go vet run on the branch first; a failure stops the cut.
func runReleaseCutPatch(ctx *Context, cut string) error {
	wf := ctx.Project.LoadWorkflow()
	root := privacy.MainCheckout(ctx.Project.TelemetryRoot())
	branch := milestone.ReleaseBranch(cut)
	if _, err := os.Stat(filepath.Join(root, ".worktrees", milestone.ReleaseWorktreeName(cut))); err == nil {
		if ran, failed := privacy.CheckBuild(root, cut); ran && failed != "" {
			return fmt.Errorf("not cutting %s: %s does not build: %s\nPick the missing commit from main (issue-cli release pick <sha>), then cut again", cut, branch, failed)
		}
	}
	res, err := privacy.Cut(root, cut, branch, wf.Privacy)
	if err != nil {
		return err
	}
	out := ctx.Stdout
	fmt.Fprintf(out, "✓ Tagged %s on %s (a patch release on the existing branch; local, never push a candidate tag)\n", res.Tag, res.Branch)
	if res.Pushed {
		fmt.Fprintf(out, "✓ Pushed %s to origin (the dev repo)\n", res.Branch)
	}
	fmt.Fprintf(out, `Next:
  - Install the candidate from the release worktree: cd %s && make install, then restart what runs it.
  - Waiting for release shows the patch candidate and its checks; publish only on the owner's ok: cd %s && issue-cli release publish %s.
  - More fixes: issue-cli release pick <slug|sha>, then cut the next candidate (issue-cli release cut %s).
`, res.Worktree, res.Worktree, cut, cut)
	return nil
}

// releaseWorktrees are the release branches' worktrees under .worktrees/.
func releaseWorktrees(root string) []string {
	matches, _ := filepath.Glob(filepath.Join(root, ".worktrees", "release-v*"))
	sort.Strings(matches)
	return matches
}

func runReleasePick(ctx *Context, args []string) error {
	fs := newFlagSet("release pick", ctx)
	rel := fs.String("release", "", "the release to pick onto (v0.54.0); needed only with more than one release worktree")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return &usageError{msg: "release pick takes issue slugs or commits: issue-cli release pick <slug|sha>..."}
	}
	root := privacy.MainCheckout(ctx.Project.TelemetryRoot())
	cut := ""
	if *rel != "" {
		v, err := privacy.ParseVersion(*rel)
		if err != nil {
			return &usageError{msg: err.Error()}
		}
		cut = v
	} else {
		wts := releaseWorktrees(root)
		switch len(wts) {
		case 0:
			return fmt.Errorf("no release worktree under %s: cut the release first (issue-cli release cut <version>)", filepath.Join(root, ".worktrees"))
		case 1:
			cut = strings.TrimPrefix(filepath.Base(wts[0]), "release-") + ".0"
		default:
			var names []string
			for _, w := range wts {
				names = append(names, filepath.Base(w))
			}
			return &usageError{msg: fmt.Sprintf("more than one release worktree (%s): pass --release <version>", strings.Join(names, ", "))}
		}
	}
	res, err := privacy.Pick(root, cut, fs.Args(), privacy.PickOptions{Changelog: ctx.Project.LoadWorkflow().Workbench.Changelog})
	if err != nil {
		return err
	}
	branch := milestone.ReleaseBranch(cut)
	for _, a := range res.Already {
		fmt.Fprintf(ctx.Stdout, "✓ %s is already on %s\n", a, branch)
	}
	if len(res.Picked) == 0 {
		fmt.Fprintln(ctx.Stdout, "Nothing to pick.")
		return nil
	}
	fmt.Fprintf(ctx.Stdout, "✓ Picked %d %s onto %s: %s\n", len(res.Picked), pluralWord(len(res.Picked), "commit", "commits"), branch, strings.Join(res.Picked, " "))
	if res.Changelog {
		fmt.Fprintf(ctx.Stdout, "✓ The changelog's ## Unreleased on %s kept its own lines plus the picked ones (main's other entries left out)\n", branch)
	}
	if res.BuildFailed != "" {
		return fmt.Errorf("the commits are picked, but %s does not build: %s\nHint: %s; cut no candidate until it builds", branch, res.BuildFailed, res.BuildHint())
	}
	if res.Built {
		fmt.Fprintf(ctx.Stdout, "✓ go build and go vet pass on %s\n", branch)
	}
	fmt.Fprintf(ctx.Stdout, "Next: cut the next candidate so the fix soaks: issue-cli release cut <version> (Waiting for release names it).\n")
	return nil
}

func runReleasePublish(ctx *Context, args []string) error {
	fs := newFlagSet("release publish", ctx)
	first := fs.Bool("first", false, "the public tip is the old history (cleaned once), not a snapshot: put the first snapshot on it")
	dryRun := fs.Bool("dry-run", false, "run every check and show the public commit (parent, message, what it changes); commit, tag and push nothing")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return &usageError{msg: "release publish takes the version: issue-cli release publish v0.54.0 [--dry-run]"}
	}
	cut, err := privacy.ParseVersion(fs.Arg(0))
	if err != nil {
		return &usageError{msg: err.Error()}
	}
	dryRunHint := tracker.InjectProject(ctx.Project, "issue-cli release publish "+cut+" --dry-run")
	var waits []string
	if st, on, _ := yolo.Active(yoloRoot(ctx.Project), ctx.Project.Slug, ctx.now()); on && !st.Valves.Releases {
		msg := fmt.Sprintf("YOLO is on: external releases wait for the owner. Publish after YOLO ends (%s), or the owner unlocks releases when turning YOLO on (--allow-releases)",
			tracker.InjectProject(ctx.Project, "issue-cli yolo off"))
		if !*dryRun {
			return fmt.Errorf("%s. See what it would publish: %s", msg, dryRunHint)
		}
		waits = append(waits, msg)
	}
	wf := ctx.Project.LoadWorkflow()
	if !wf.Privacy.Snapshots() {
		return fmt.Errorf("privacy.code is not snapshots: this project publishes by pushing main (docs/roadmap.md, Cutting a release)")
	}
	var card *releaseCard
	if milestone.IsPatch(cut) {
		card, err = fetchPatchCard(ctx)
		if err == nil && card == nil {
			card = &releaseCard{}
		}
	} else {
		card, err = fetchReleaseCard(ctx)
	}
	switch {
	case err != nil && !*dryRun:
		return err
	case err != nil:
		waits = append(waits, "the Waiting for release card could not be read: "+err.Error())
	case milestone.Norm(card.Cut) != milestone.Norm(cut) || !card.Soaking:
		msg := fmt.Sprintf("Waiting for release does not say a %s candidate is soaking with every check met:\n%s", cut, card.unmet())
		if !*dryRun {
			return fmt.Errorf("%sPublish only a soaked candidate, on the owner's ok. See what it would publish: %s", msg, dryRunHint)
		}
		waits = append(waits, strings.TrimRight(msg, "\n"))
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	root := privacy.MainCheckout(ctx.Project.TelemetryRoot())
	privateNames, _ := privacy.PrivateNames()
	res, err := privacy.Publish(privacy.PublishInput{
		Root: root, Worktree: cwd, Cut: cut, Cfg: wf.Privacy, Changelog: wf.Workbench.Changelog, First: *first, DryRun: *dryRun, Now: ctx.Now(), PrivateNames: privateNames,
	})
	if err != nil {
		return err
	}
	out := ctx.Stdout
	if *dryRun {
		printPublishDryRun(ctx, cut, wf.Privacy, res, waits)
		return nil
	}
	fmt.Fprintf(out, "✓ Published %s: one commit %s on %s/%s", cut, shortSha(res.Snapshot), wf.Privacy.Remote(), wf.Privacy.Branch())
	if res.Parent != "" {
		fmt.Fprintf(out, " (on top of %s)", shortSha(res.Parent))
	}
	if res.Signed {
		fmt.Fprint(out, ", signed with user.signingkey")
	} else {
		fmt.Fprint(out, ", unsigned (no user.signingkey: GitHub shows it Unverified)")
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "✓ Tagged %s on %s; pushed both to origin\n", cut, milestone.ReleaseBranch(cut))
	if res.MainMoved {
		fmt.Fprintf(out, "✓ Moved the released lines of ## Unreleased on %s under ## %s (committed; push main when you push)\n", res.MainBranch, cut)
	}
	fmt.Fprintf(out, "Next: the public repo's release Action tags %s and builds it. Then make install from main.\n", cut)
	return nil
}

// printPublishDryRun says what release publish would put on the public
// repo, and what still stands in the way of the real run.
func printPublishDryRun(ctx *Context, cut string, cfg *tracker.PrivacyConfig, res privacy.PublishResult, waits []string) {
	out := ctx.Stdout
	remote, branch := cfg.Remote(), cfg.Branch()
	fmt.Fprintf(out, "Dry run: nothing was committed, tagged or pushed.\n\n")
	switch {
	case res.Parent == "":
		fmt.Fprintf(out, "Parent: none (%s/%s is empty): %s is its first commit\n", remote, branch, cut)
	case res.IsBase:
		fmt.Fprintf(out, "Parent: %s, %s/%s's tip, the old history recorded at setup: %s goes on top of it\n", shortSha(res.Parent), remote, branch, cut)
	default:
		fmt.Fprintf(out, "Parent: %s, the last release snapshot on %s/%s\n", shortSha(res.Parent), remote, branch)
	}
	fmt.Fprintf(out, "\nThe one public commit's message:\n")
	for _, l := range strings.Split(strings.TrimRight(res.Message, "\n"), "\n") {
		fmt.Fprintf(out, "  | %s\n", l)
	}
	fmt.Fprintf(out, "\nWhat it changes on %s/%s:\n", remote, branch)
	if strings.TrimSpace(res.DiffStat) == "" {
		fmt.Fprintln(out, "  nothing (the same tree)")
	}
	for _, l := range strings.Split(res.DiffStat, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			fmt.Fprintf(out, "  %s\n", l)
		}
	}
	rb := milestone.ReleaseBranch(cut)
	fmt.Fprintf(out, "\nA real publish would:\n")
	fmt.Fprintf(out, "  1. commit ## Unreleased renamed to ## %s on %s (its only direct commit)\n", cut, rb)
	fmt.Fprintf(out, "  2. push that tree as the one commit above to %s/%s, signed when user.signingkey is set\n", remote, branch)
	fmt.Fprintf(out, "  3. tag %s on %s and push both to origin (the dev repo)\n", cut, rb)
	fmt.Fprintf(out, "  4. move the released lines of main's ## Unreleased under ## %s and commit them\n", cut)
	if len(waits) > 0 {
		fmt.Fprintf(out, "\nThe real run would refuse now:\n")
		for _, w := range waits {
			for i, l := range strings.Split(strings.TrimSpace(w), "\n") {
				switch {
				case i == 0:
					fmt.Fprintf(out, "  - %s\n", l)
				case strings.TrimSpace(l) != "":
					fmt.Fprintf(out, "    %s\n", strings.TrimSpace(l))
				}
			}
		}
	}
}

func shortSha(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}
