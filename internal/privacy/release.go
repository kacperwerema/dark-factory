package privacy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/milestone"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Private development, public releases (privacy.code: snapshots): origin is
// a private dev repo with every branch and tag; the public repo is the
// remote `public` and gets ONE commit per release, the release branch tip's
// tree with the subject vX.Y.Z and the release's changelog section as the
// body, built with git commit-tree so no dev commit, message or branch name
// travels with it. A release is cut first as
// release/vX.Y with rc tags on it, soaks there (fixes are cherry-picked from
// main), and is published from that branch.

// publicBaseKey is the git config key holding the public branch's tip when
// release setup ran: the old history the first snapshot goes on top of.
const publicBaseKey = "issue-cli.publicbase"

var snapshotSubjectRe = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// IsSnapshot reports whether sha is a release snapshot commit: the subject
// is exactly vX.Y.Z and it has at most one parent.
func IsSnapshot(root, sha string) bool {
	out, err := git(root, "log", "-1", "--format=%P%x00%B", sha)
	if err != nil {
		return false
	}
	parents, msg, _ := strings.Cut(out, "\x00")
	if len(strings.Fields(parents)) > 1 {
		return false
	}
	subject, _, _ := strings.Cut(strings.TrimSpace(msg), "\n")
	return snapshotSubjectRe.MatchString(strings.TrimSpace(subject))
}

// SetupResult says what Setup did.
type SetupResult struct {
	RenamedOrigin bool     // origin became the public remote
	Base          string   // the public branch's tip, recorded as the base
	Pushed        []string // what went to the dev remote: "branches", "tags"
	SkippedRC     int      // candidate tags kept local
}

// Setup makes devURL the origin and the old origin the public remote (tags
// never fetched from it), pushes every branch and every non-candidate tag to
// the dev repo, and records the public branch's tip as the base the first
// snapshot goes on. It never pushes anything to the public remote.
func Setup(root, devURL string, cfg *tracker.PrivacyConfig) (SetupResult, error) {
	var res SetupResult
	remote := cfg.Remote()
	origin, _ := git(root, "remote", "get-url", "origin")
	public, _ := git(root, "remote", "get-url", remote)
	switch {
	case public == "" && origin == "":
		return res, fmt.Errorf("no origin and no %s remote: add the public repo first (git remote add %s <url>)", remote, remote)
	case public == "" && origin == devURL:
		return res, fmt.Errorf("origin is already the dev repo %s and there is no %s remote: add the public repo (git remote add %s <url>)", devURL, remote, remote)
	case public == "":
		if _, err := git(root, "remote", "rename", "origin", remote); err != nil {
			return res, err
		}
		res.RenamedOrigin = true
		origin = ""
	}
	if origin == "" {
		if _, err := git(root, "remote", "add", "origin", devURL); err != nil {
			return res, err
		}
	} else if origin != devURL {
		return res, fmt.Errorf("origin is %s, not the dev repo %s: fix it with git remote set-url origin <url>", origin, devURL)
	}
	if _, err := git(root, "config", "remote."+remote+".tagOpt", "--no-tags"); err != nil {
		return res, err
	}
	branch := cfg.Branch()
	if _, err := git(root, "fetch", "-q", "--no-tags", remote, "+refs/heads/"+branch+":refs/remotes/"+remote+"/"+branch); err == nil {
		res.Base, _ = git(root, "rev-parse", "refs/remotes/"+remote+"/"+branch)
		if res.Base != "" {
			if _, err := git(root, "config", publicBaseKey, res.Base); err != nil {
				return res, err
			}
		}
	}
	if _, err := git(root, "push", "-q", "origin", "--all"); err != nil {
		return res, fmt.Errorf("pushing the branches to the dev repo: %w", err)
	}
	res.Pushed = append(res.Pushed, "branches")
	tags, _ := git(root, "tag", "-l")
	var refs []string
	for _, t := range lines(tags) {
		if milestone.IsPreRelease(t) {
			res.SkippedRC++
			continue
		}
		refs = append(refs, "refs/tags/"+t)
	}
	if len(refs) > 0 {
		if _, err := git(root, append([]string{"push", "-q", "origin"}, refs...)...); err != nil {
			return res, fmt.Errorf("pushing the tags to the dev repo: %w", err)
		}
		res.Pushed = append(res.Pushed, "tags")
	}
	return res, nil
}

// CutResult says what Cut did.
type CutResult struct {
	Branch   string
	Worktree string
	Created  bool   // the branch is new
	Tag      string // the candidate tag
	Pushed   bool   // the branch went to origin
}

// Cut tags the next release candidate of cut on its release branch,
// creating the branch from from (dev main) and its worktree when they are
// missing. Under code: snapshots it sends the branch to origin (the dev
// repo), and refuses before doing anything while setup hasn't run: origin
// may still be the public repo (NotSwitched).
func Cut(root, cut, from string, cfg *tracker.PrivacyConfig) (CutResult, error) {
	res := CutResult{Branch: milestone.ReleaseBranch(cut), Worktree: filepath.Join(root, ".worktrees", milestone.ReleaseWorktreeName(cut))}
	push := cfg.Snapshots()
	if err := NotSwitched(root, cfg); err != nil {
		return res, fmt.Errorf("not cutting %s: %w", cut, err)
	}
	if _, err := git(root, "rev-parse", "-q", "--verify", "refs/heads/"+res.Branch); err != nil {
		if milestone.IsPatch(cut) {
			return res, fmt.Errorf("%s is a patch release: it goes on %s, which does not exist. Cut %s.0 first", cut, res.Branch, strings.TrimPrefix(res.Branch, "release/"))
		}
		if _, err := git(root, "branch", res.Branch, from); err != nil {
			return res, err
		}
		res.Created = true
	}
	if _, err := os.Stat(res.Worktree); err != nil {
		if _, err := git(root, "worktree", "add", "-q", res.Worktree, res.Branch); err != nil {
			return res, err
		}
	} else if b, _ := git(res.Worktree, "rev-parse", "--abbrev-ref", "HEAD"); b != res.Branch {
		return res, fmt.Errorf("%s is on %s, not %s: switch it back first", res.Worktree, b, res.Branch)
	}
	tags, _ := git(root, "tag", "-l", cut+"-rc.*")
	rc, have := milestone.NewestRC(lines(tags), cut)
	n := 1
	if have {
		tagSha, _ := git(root, "rev-parse", rc.Tag+"^{commit}")
		tip, _ := git(root, "rev-parse", res.Branch)
		if tagSha == tip {
			return res, fmt.Errorf("%s already tags %s's tip: pick a fix first (issue-cli release pick <slug>), or soak %s", rc.Tag, res.Branch, rc.Tag)
		}
		n = rc.N + 1
	}
	res.Tag = milestone.RCTag(cut, n)
	if _, err := git(root, "tag", "-a", res.Tag, "-m", res.Tag, res.Branch); err != nil {
		return res, err
	}
	if push {
		if _, err := git(root, "push", "-q", "origin", "refs/heads/"+res.Branch); err != nil {
			return res, fmt.Errorf("%s is tagged; pushing %s to origin failed: %w", res.Tag, res.Branch, err)
		}
		res.Pushed = true
	}
	return res, nil
}

// NotSwitched is the error while privacy.code is snapshots but release
// setup hasn't run (no public remote): origin may still be the public repo,
// so nothing may be pushed to it. The CodeBadge calls this "Not switched".
func NotSwitched(root string, cfg *tracker.PrivacyConfig) error {
	if !cfg.Snapshots() {
		return nil
	}
	remote := cfg.Remote()
	if url, _ := git(root, "remote", "get-url", remote); url != "" {
		return nil
	}
	origin, _ := git(root, "remote", "get-url", "origin")
	return fmt.Errorf("privacy.code is snapshots, but there is no %s remote, so origin (%s) may still be the public repo and nothing is pushed to it. Switch first: issue-cli release setup --dev <private dev repo url>", remote, valueOr(origin, "none"))
}

// PickOptions are Pick's settings; the zero value is main and CHANGELOG.md.
type PickOptions struct {
	Main      string // the branch done issues landed on (default main)
	Changelog string // the changelog, relative to the root (default CHANGELOG.md)
}

// PickResult is the commits Pick applied and what it checked after.
type PickResult struct {
	Picked  []string // short shas, in order
	Already []string // items whose commits the branch already has
	// Changelog is set when a pick's changelog conflict was resolved by
	// keeping the branch's ## Unreleased plus the picked commit's own lines.
	Changelog bool
	// Built is set when go build and go vet ran in the release worktree (it
	// has a go.mod); BuildFailed is their output when they failed.
	Built       bool
	BuildFailed string
}

// BuildHint is what to do when the branch no longer builds after a pick.
func (r PickResult) BuildHint() string {
	return "a picked commit needs one that is not on the release branch: find it on main (git log main -- <file named above>) and pick it too (issue-cli release pick <sha>), or drop the pick (git reset --hard on the release branch)"
}

// Pick cherry-picks onto the release branch, in its worktree, each item's
// commits the branch lacks: for an issue slug the commits of work/<slug>
// (git cherry), else the item as a commit, else (a done issue, its branch
// deleted at close) the commits on main whose Refs: trailer names the
// slug, oldest first, merges skipped. Commits apply one by one; a conflict
// puts the branch back where it was and names the commit and files, except
// one only in the changelog, which keeps the branch's ## Unreleased plus
// the commit's own lines. With a go.mod in the worktree, go build and go
// vet run after the pick (PickResult.BuildFailed).
func Pick(root, cut string, items []string, opts PickOptions) (PickResult, error) {
	var res PickResult
	branch := milestone.ReleaseBranch(cut)
	wt := filepath.Join(root, ".worktrees", milestone.ReleaseWorktreeName(cut))
	mainBranch := valueOr(opts.Main, "main")
	changelog := valueOr(opts.Changelog, "CHANGELOG.md")
	if b, err := git(wt, "rev-parse", "--abbrev-ref", "HEAD"); err != nil || b != branch {
		return res, fmt.Errorf("no release worktree on %s at %s: cut the release first (issue-cli release cut %s)", branch, wt, cut)
	}
	if st, _ := git(wt, "status", "--porcelain", "--untracked-files=no"); st != "" {
		return res, fmt.Errorf("%s has uncommitted changes: commit or discard them first", wt)
	}
	var shas []string
	for _, item := range items {
		ref := "work/" + item
		if _, err := git(root, "rev-parse", "-q", "--verify", "refs/heads/"+ref); err == nil {
			mine, err := cherryNew(root, branch, ref)
			if err != nil {
				return res, err
			}
			if len(mine) == 0 {
				res.Already = append(res.Already, item)
			}
			shas = append(shas, mine...)
			continue
		}
		if sha, err := git(root, "rev-parse", "-q", "--verify", item+"^{commit}"); err == nil {
			if _, err := git(root, "merge-base", "--is-ancestor", sha, branch); err == nil {
				res.Already = append(res.Already, item)
				continue
			}
			shas = append(shas, sha)
			continue
		}
		refs, err := refsCommits(root, mainBranch, item)
		if err != nil {
			return res, err
		}
		if len(refs) == 0 {
			return res, fmt.Errorf("%s is not a commit, has no work/%s branch, and no commit on %s has the trailer Refs: %s", item, item, mainBranch, item)
		}
		lacking, err := cherryNew(root, branch, mainBranch)
		if err != nil {
			return res, err
		}
		want := map[string]bool{}
		for _, s := range lacking {
			want[s] = true
		}
		var mine []string
		for _, s := range refs {
			if want[s] {
				mine = append(mine, s)
			}
		}
		if len(mine) == 0 {
			res.Already = append(res.Already, item)
		}
		shas = append(shas, mine...)
	}
	if len(shas) == 0 {
		return res, nil
	}
	start, err := git(wt, "rev-parse", "HEAD")
	if err != nil {
		return res, err
	}
	for _, sha := range shas {
		if _, err := git(wt, "-c", "commit.gpgsign=false", "cherry-pick", "-x", sha); err != nil {
			out, _ := git(wt, "diff", "--name-only", "--diff-filter=U")
			conflicted := lines(out)
			if len(conflicted) == 1 && conflicted[0] == changelog {
				if rerr := resolveChangelogPick(wt, changelog, sha); rerr == nil {
					res.Changelog = true
					res.Picked = append(res.Picked, short(sha))
					continue
				}
			}
			git(wt, "cherry-pick", "--abort")
			git(wt, "reset", "-q", "--hard", start)
			subj, _ := git(root, "log", "-1", "--format=%s", sha)
			why := "in " + strings.Join(conflicted, ", ")
			if len(conflicted) == 0 {
				why = "(" + err.Error() + ")"
			}
			return PickResult{Already: res.Already}, fmt.Errorf("%s %q conflicts with %s %s: nothing picked, %s is as it was. Pick the commits it builds on first, or apply it by hand in %s", short(sha), subj, branch, why, branch, wt)
		}
		res.Picked = append(res.Picked, short(sha))
	}
	res.Built, res.BuildFailed = CheckBuild(root, cut)
	return res, nil
}

// builtKey is the git config key holding the release branch tip that last
// passed go build and go vet (CheckBuild): the Status card's build check.
func builtKey(branch string) string { return "issue-cli-release." + branch + ".built" }

// CheckBuild runs go build and go vet in cut's release worktree when it has
// a go.mod (ran false without one) and records the tip that passed, or
// clears the record when they fail (failed is their output).
func CheckBuild(root, cut string) (ran bool, failed string) {
	branch := milestone.ReleaseBranch(cut)
	wt := filepath.Join(root, ".worktrees", milestone.ReleaseWorktreeName(cut))
	if !fileExistsAt(filepath.Join(wt, "go.mod")) {
		return false, ""
	}
	failed = buildCheck(wt)
	if failed != "" {
		git(root, "config", "--unset", builtKey(branch))
		return true, failed
	}
	if tip, err := git(wt, "rev-parse", "HEAD"); err == nil {
		git(root, "config", builtKey(branch), tip)
	}
	return true, ""
}

// BuiltTip is the release branch tip CheckBuild last passed; "" for none.
func BuiltTip(root, branch string) string {
	tip, _ := git(root, "config", "--get", builtKey(branch))
	return tip
}

// cherryNew is the commits of ref, oldest first, that branch has no
// equivalent of (git cherry's + lines).
func cherryNew(root, branch, ref string) ([]string, error) {
	out, err := git(root, "cherry", branch, ref)
	if err != nil {
		return nil, err
	}
	var mine []string
	for _, l := range lines(out) {
		if sha, ok := strings.CutPrefix(l, "+ "); ok {
			mine = append(mine, sha)
		}
	}
	return mine, nil
}

// refsCommits is the non-merge commits on mainBranch, oldest first, with a
// Refs: trailer that is exactly slug.
func refsCommits(root, mainBranch, slug string) ([]string, error) {
	out, err := git(root, "log", "-z", "--reverse", "--no-merges", "--format=%H%n%(trailers:key=Refs,valueonly,separator=%x1f)", mainBranch)
	if err != nil {
		return nil, err
	}
	var shas []string
	for _, rec := range strings.Split(out, "\x00") {
		sha, vals, _ := strings.Cut(strings.TrimSpace(rec), "\n")
		for _, v := range strings.Split(vals, "\x1f") {
			if strings.TrimSpace(v) == slug {
				shas = append(shas, sha)
				break
			}
		}
	}
	return shas, nil
}

// unreleasedEntries is the non-blank lines of text's ## Unreleased section
// (the heading left out).
func unreleasedEntries(text string) []string {
	s, e, ok := section(text, func(h string) bool { return unreleasedRe.MatchString(h) })
	if !ok {
		return nil
	}
	var out []string
	for _, l := range strings.Split(text[s:e], "\n")[1:] {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// addedEntries is the ## Unreleased lines sha added to changelog, in its
// order.
func addedEntries(dir, sha, changelog string) []string {
	after, _ := git(dir, "show", sha+":"+changelog)
	before, _ := git(dir, "show", sha+"^:"+changelog)
	had := map[string]int{}
	for _, l := range unreleasedEntries(before) {
		had[l]++
	}
	var out []string
	for _, l := range unreleasedEntries(after) {
		if had[l] > 0 {
			had[l]--
			continue
		}
		out = append(out, l)
	}
	return out
}

// resolveChangelogPick ends a cherry-pick stopped on a changelog conflict:
// the branch's changelog with the commit's own ## Unreleased lines put at
// the top of the section, nothing else from the commit's side.
func resolveChangelogPick(wt, changelog, sha string) error {
	ours, err := git(wt, "show", "HEAD:"+changelog)
	if err != nil {
		return err
	}
	text, ok := insertUnreleased(ours+"\n", addedEntries(wt, sha, changelog))
	if !ok {
		return fmt.Errorf("no ## Unreleased in the branch's %s", changelog)
	}
	if err := os.WriteFile(filepath.Join(wt, changelog), []byte(text), 0o644); err != nil {
		return err
	}
	if _, err := git(wt, "add", "--", changelog); err != nil {
		return err
	}
	if _, err := git(wt, "-c", "commit.gpgsign=false", "-c", "core.editor=true", "cherry-pick", "--continue"); err != nil {
		return err
	}
	return nil
}

// insertUnreleased puts entries at the top of text's ## Unreleased
// section, after the heading's blank line; without that heading it opens
// one above the first ## heading. ok is false with no ## heading at all.
func insertUnreleased(text string, entries []string) (string, bool) {
	loc := unreleasedRe.FindStringIndex(text)
	if loc == nil {
		// A branch that was published has no ## Unreleased: open one above
		// the newest release's heading.
		at := regexp.MustCompile(`(?m)^## `).FindStringIndex(text)
		if at == nil {
			return text, false
		}
		if len(entries) == 0 {
			return text, true
		}
		return text[:at[0]] + "## Unreleased\n\n" + strings.Join(entries, "\n") + "\n\n" + text[at[0]:], true
	}
	if len(entries) == 0 {
		return text, true
	}
	pos := loc[1]
	for pos < len(text) && (text[pos] == '\n' || text[pos] == '\r') {
		pos++
	}
	add := strings.Join(entries, "\n") + "\n"
	if pos >= len(text) || strings.HasPrefix(text[pos:], "## ") {
		add += "\n"
	}
	return text[:loc[1]] + "\n\n" + add + text[pos:], true
}

// buildCheck runs go build and go vet in dir; it returns their output when
// either fails, "" when both pass.
var buildCheck = func(dir string) string {
	for _, args := range [][]string{{"build", "./..."}, {"vet", "./..."}} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		cmd := exec.CommandContext(ctx, "go", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			return "go " + strings.Join(args, " ") + ": " + firstLines(msg, 12)
		}
	}
	return ""
}

func firstLines(s string, n int) string {
	ls := strings.Split(s, "\n")
	if len(ls) > n {
		ls = append(ls[:n], "…")
	}
	return strings.Join(ls, "\n")
}

func fileExistsAt(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// PublishInput is what Publish needs.
type PublishInput struct {
	Root      string // the main checkout
	Worktree  string // where publish runs: must be on the release branch
	Cut       string // "v0.54.0"
	Cfg       *tracker.PrivacyConfig
	Changelog string // the changelog's path relative to the root, "" for none
	First     bool   // accept a public tip that isn't a snapshot (the old history)
	DryRun    bool   // run the checks and say what would go public; write and push nothing
	// PrivateNames is the owner's private-names list (PrivateNames()): a
	// tree naming one is refused, as is one with a user home path.
	PrivateNames []string
	Now          time.Time
}

// PublishResult says what Publish did.
type PublishResult struct {
	Snapshot   string // the public commit
	Parent     string // the public tip it went on ("" for the first commit of an empty repo)
	MainMoved  bool   // main's changelog got the version section
	MainBranch string
	Signed     bool // the snapshot was signed with user.signingkey

	// Filled on every run, and all a dry run gives.
	Message  string // the snapshot's message
	IsBase   bool   // Parent is the old history recorded at setup, not a snapshot
	DiffStat string // git diff --stat from Parent to the snapshot's tree
}

// Publish pushes the release branch tip as one snapshot commit to the
// public branch, then tags the release on the dev branch and pushes both to
// origin. See the package comment on release.go.
func Publish(in PublishInput) (PublishResult, error) {
	var res PublishResult
	if !in.Cfg.Snapshots() {
		return res, fmt.Errorf("privacy.code is not snapshots: this project publishes by pushing main (docs/roadmap.md, Cutting a release)")
	}
	branch := milestone.ReleaseBranch(in.Cut)
	want := filepath.Join(in.Root, ".worktrees", milestone.ReleaseWorktreeName(in.Cut))
	if b, _ := git(in.Worktree, "rev-parse", "--abbrev-ref", "HEAD"); b != branch {
		return res, fmt.Errorf("publish runs on %s, in its worktree: cd %s (HEAD here is %s)", branch, want, valueOr(b, "no branch"))
	}
	if st, _ := git(in.Worktree, "status", "--porcelain", "--untracked-files=no"); st != "" {
		return res, fmt.Errorf("%s has uncommitted changes: commit or discard them first", in.Worktree)
	}
	if _, err := git(in.Root, "rev-parse", "-q", "--verify", "refs/tags/"+in.Cut); err == nil {
		return res, fmt.Errorf("%s is already tagged in the dev repo: it was published (or tag it by hand only if you know why)", in.Cut)
	}
	remote, pubBranch := in.Cfg.Remote(), in.Cfg.Branch()
	if url, _ := git(in.Root, "remote", "get-url", remote); url == "" {
		return res, fmt.Errorf("no %s remote: issue-cli release setup --dev <url> first", remote)
	}
	paths := in.Cfg.Paths()
	if out, _ := git(in.Worktree, append([]string{"ls-tree", "-r", "--name-only", "HEAD", "--"}, paths...)...); out != "" {
		return res, fmt.Errorf("work files are in %s's tree (%s): they would go public. Untrack them on main (git rm -r --cached), pick that commit, cut a new rc", branch, firstFew(lines(out), 3))
	}
	leaks, err := ScanLeaks(in.Worktree, "HEAD", in.PrivateNames)
	if err != nil {
		return res, err
	}
	// The upstream author's name, outside LICENSE.md (D-4): refused even
	// with no private-names list on this machine.
	upstream, err := ScanUpstreamName(in.Worktree, "HEAD")
	if err != nil {
		return res, err
	}
	leaks = append(leaks, upstream...)
	if len(leaks) > 0 {
		return res, leakError(branch, leaks)
	}
	tracking := "refs/remotes/" + remote + "/" + pubBranch
	if _, err := git(in.Root, "fetch", "-q", "--no-tags", remote, "+refs/heads/"+pubBranch+":"+tracking); err != nil && !strings.Contains(err.Error(), "couldn't find remote ref") {
		return res, fmt.Errorf("fetching %s/%s: %w", remote, pubBranch, err)
	}
	res.Parent, _ = git(in.Root, "rev-parse", "-q", "--verify", tracking)
	if res.Parent != "" && !IsSnapshot(in.Root, res.Parent) {
		base, _ := git(in.Root, "config", publicBaseKey)
		if !in.First && res.Parent != base {
			subj, _ := git(in.Root, "log", "-1", "--format=%s", res.Parent)
			return res, fmt.Errorf("%s/%s's tip %s (%q) is not a release snapshot: dev history reached the public repo. Clean it (docs/workflow.md, Private work and code), or pass --first if this is the old history the first snapshot goes on", remote, pubBranch, short(res.Parent), subj)
		}
	}

	if res.Parent != "" && !IsSnapshot(in.Root, res.Parent) {
		res.IsBase = true
	}

	// The changelog heading, on the branch: the only direct commit there. A
	// dry run renames it in memory only.
	res.Message = in.Cut
	renamed := false
	if in.Changelog != "" {
		path := filepath.Join(in.Worktree, in.Changelog)
		if data, err := os.ReadFile(path); err == nil {
			text := string(data)
			heading := "## " + in.Cut + " — " + in.Now.Format("2006-01-02")
			_, _, written := section(text, func(h string) bool { return strings.HasPrefix(h, "## "+in.Cut+" ") || h == "## "+in.Cut })
			if t, ok := renameUnreleased(text, heading); ok && !written {
				text, renamed = t, true
				if !in.DryRun {
					if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
						return res, err
					}
					if _, err := git(in.Worktree, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "docs(changelog): "+in.Cut, "--", in.Changelog); err != nil {
						return res, err
					}
				}
			}
			res.Message = snapshotMessage(in.Cut, text)
		}
	}

	tree, err := git(in.Worktree, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return res, err
	}
	if in.DryRun {
		from := res.Parent
		if from == "" {
			from = emptyTree
		}
		res.DiffStat, _ = git(in.Worktree, "diff", "--stat", "--summary", from, tree)
		if renamed {
			res.DiffStat = strings.TrimSpace(res.DiffStat + "\n(and " + in.Changelog + "'s ## Unreleased renamed to ## " + in.Cut + ")")
		}
		return res, nil
	}
	args := []string{"commit-tree", tree, "-m", res.Message}
	if res.Parent != "" {
		args = []string{"commit-tree", tree, "-p", res.Parent, "-m", res.Message}
	}
	// The public commit is signed with the owner's user.signingkey, so the
	// public repo shows it Verified. commit-tree signs only with -S (it
	// ignores commit.gpgsign). A key that fails to sign stops the publish:
	// nothing unsigned goes public by accident.
	if key, _ := git(in.Root, "config", "user.signingkey"); key != "" {
		args = append([]string{"commit-tree", "-S" + key}, args[1:]...)
		res.Signed = true
	}
	res.Snapshot, err = git(in.Root, args...)
	if err != nil {
		if res.Signed {
			return res, fmt.Errorf("signing the %s snapshot with user.signingkey failed, so nothing was published (unlock the key, or check gpg / gpg.format): %w", in.Cut, err)
		}
		return res, err
	}
	if _, err := git(in.Root, "push", "-q", remote, res.Snapshot+":refs/heads/"+pubBranch); err != nil {
		return res, fmt.Errorf("pushing the snapshot %s to %s: %w", short(res.Snapshot), remote, err)
	}
	git(in.Root, "update-ref", tracking, res.Snapshot)

	if _, err := git(in.Root, "tag", "-a", in.Cut, "-m", in.Cut, branch); err != nil {
		return res, fmt.Errorf("%s is public; tagging the dev branch failed: %w", in.Cut, err)
	}
	if _, err := git(in.Root, "push", "-q", "origin", "refs/heads/"+branch, "refs/tags/"+in.Cut); err != nil {
		return res, fmt.Errorf("%s is public and tagged; pushing %s and the tag to origin failed: %w", in.Cut, branch, err)
	}

	res.MainBranch, _ = git(in.Root, "rev-parse", "--abbrev-ref", "HEAD")
	if in.Changelog != "" {
		moved, err := moveReleasedLines(in.Root, in.Worktree, in.Changelog, in.Cut)
		if err != nil {
			return res, fmt.Errorf("%s is published; moving its changelog section on %s: %w", in.Cut, res.MainBranch, err)
		}
		res.MainMoved = moved
	}
	return res, nil
}

// emptyTree is git's empty tree: what a first publish to an empty public
// repo diffs from.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// snapshotMessage is the public commit's message: the version as the
// subject, then the changelog's section for it (its lines without the
// heading) as the body, so the public log reads as release notes.
func snapshotMessage(cut, changelog string) string {
	start, end, ok := section(changelog, func(h string) bool { return strings.HasPrefix(h, "## "+cut+" ") || h == "## "+cut })
	if !ok {
		return cut
	}
	_, body, _ := strings.Cut(changelog[start:end], "\n")
	if body = strings.TrimSpace(body); body == "" {
		return cut
	}
	return cut + "\n\n" + body + "\n"
}

func valueOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

var unreleasedRe = regexp.MustCompile(`(?mi)^##[ \t]+\[?unreleased\]?[ \t]*$`)

// renameUnreleased replaces the ## Unreleased heading with heading; ok is
// false without one.
func renameUnreleased(text, heading string) (string, bool) {
	loc := unreleasedRe.FindStringIndex(text)
	if loc == nil {
		return text, false
	}
	return text[:loc[0]] + heading + text[loc[1]:], true
}

// section is the lines of the "## <prefix>…" section of text (the heading
// included), and where it starts and ends.
func section(text string, match func(heading string) bool) (start, end int, ok bool) {
	ls := strings.SplitAfter(text, "\n")
	pos := 0
	start = -1
	for _, l := range ls {
		if strings.HasPrefix(l, "## ") {
			if start >= 0 {
				return start, pos, true
			}
			if match(strings.TrimSpace(l)) {
				start = pos
			}
		}
		pos += len(l)
	}
	if start >= 0 {
		return start, len(text), true
	}
	return 0, 0, false
}

// moveReleasedLines puts the branch's "## <cut> — date" section into main's
// changelog below ## Unreleased, and drops its lines from main's Unreleased
// (entries landed after the cut stay). It commits only the changelog.
func moveReleasedLines(root, worktree, changelog, cut string) (bool, error) {
	branchText, err := os.ReadFile(filepath.Join(worktree, changelog))
	if err != nil {
		return false, err
	}
	bs, be, ok := section(string(branchText), func(h string) bool { return strings.HasPrefix(h, "## "+cut+" ") || h == "## "+cut })
	if !ok {
		return false, nil
	}
	released := string(branchText[bs:be])
	mainPath := filepath.Join(root, changelog)
	mainData, err := os.ReadFile(mainPath)
	if err != nil {
		return false, err
	}
	mainText := string(mainData)
	if _, _, has := section(mainText, func(h string) bool { return strings.HasPrefix(h, "## "+cut+" ") }); has {
		return false, nil
	}
	us, ue, ok := section(mainText, func(h string) bool { return unreleasedRe.MatchString(h) })
	if !ok {
		return false, nil
	}
	drop := map[string]bool{}
	for _, l := range strings.Split(released, "\n")[1:] {
		if strings.TrimSpace(l) != "" {
			drop[l] = true
		}
	}
	var kept []string
	for _, l := range strings.Split(strings.TrimRight(mainText[us:ue], "\n"), "\n") {
		if !drop[l] {
			kept = append(kept, l)
		}
	}
	unreleased := strings.TrimRight(strings.Join(kept, "\n"), "\n") + "\n\n"
	newText := mainText[:us] + unreleased + strings.TrimRight(released, "\n") + "\n\n" + strings.TrimLeft(mainText[ue:], "\n")
	if err := os.WriteFile(mainPath, []byte(newText), 0o644); err != nil {
		return false, err
	}
	if _, err := git(root, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "docs(changelog): "+cut+" is published", "--", changelog); err != nil {
		return false, err
	}
	return true, nil
}

// ParseVersion checks a release argument: "v0.54.0" (the v is added when
// missing).
func ParseVersion(v string) (string, error) {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !snapshotSubjectRe.MatchString(v) {
		return "", fmt.Errorf("%q is not a release version like v0.54.0", v)
	}
	return v, nil
}

// unquote reads a decision's quote ("…" with escapes).
func unquote(q string) string {
	if s, err := strconv.Unquote(q); err == nil {
		return s
	}
	return strings.Trim(q, `"`)
}
