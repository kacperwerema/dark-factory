package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/proof"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

const catalogCheckUsage = `Usage:
  issue-cli workflow catalog-check [--base main]   # in the dark-factory repo; make validate runs it

For issue-cli developers: when a work branch changes the code behind a
catalog feature (its sources: in cmd/issue-cli/templates/workflow-features/<id>.yaml)
or a bundled template (other than its features: record), the entry's version: (or the template's stamp) must
be newer than the last release in cmd/issue-cli/CHANGELOG.md, so projects
that have the feature get an update proposal. When nothing a project adopts
changed (a refactor, a fix), mark it on a branch commit instead:
  Catalog: n/a <id>[,<id>...] <why>
The line counts in any paragraph of the message, merge commits included;
one without an entry id, or naming an entry the catalog lacks, fails the
check. Files the branch has only because it merged main aren't asked about.
On a release/vX.Y branch only the commits after its newest vX.Y.Z or
vX.Y.Z-rc.N tag are read: the tagged ones are published and were checked.
It also checks the catalog itself at the head, each failure with its fix:
every entry's category and sources (a bare folder wants <folder>/**,
@prompt stands alone), and every bundled template's features: record in
catalog order (by category, then id; issue-cli workflow scan --template
<name> rewrites it).
Run it while the work is in progress: it checks uncommitted changes too.`

// runCatalogCheck compares the branch with its merge-base and fails when a
// touched catalog entry or template wasn't bumped (or marked n/a).
func runCatalogCheck(ctx *Context, args []string) error {
	fs := newFlagSet("workflow catalog-check", ctx)
	base := fs.String("base", "main", "branch the work branch merges into")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	root, err := catalogGit("", "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("catalog-check runs inside a git checkout of issue-viewer: %w", err)
	}
	if fi, err := os.Stat(filepath.Join(root, workflowupgrade.CatalogPath)); err != nil || !fi.IsDir() {
		return fmt.Errorf("%s not found: catalog-check runs in the dark-factory repository\n\n%s", workflowupgrade.CatalogPath, catalogCheckUsage)
	}
	mergeBase, err := catalogGit(root, "merge-base", *base, "HEAD")
	if err != nil {
		fmt.Fprintf(ctx.Stdout, "catalog-check skipped: no merge-base with %s\n", *base)
		return nil
	}
	// The checked-out branch decides, not HEAD == merge-base: a work branch
	// with no commits yet still has its uncommitted changes checked.
	branch, _ := catalogGit(root, "symbolic-ref", "--short", "-q", "HEAD")
	head, _ := catalogGit(root, "rev-parse", "HEAD")
	if branch == *base || (branch == "" && head == mergeBase) {
		fmt.Fprintf(ctx.Stdout, "catalog-check skipped: not a work branch (HEAD is %s)\n", *base)
		return nil
	}
	from := mergeBase
	if tag, sha := catalogReleaseTag(root, branch); tag != "" {
		fmt.Fprintf(ctx.Stdout, "catalog-check on %s: reading the commits since %s\n", branch, tag)
		from = sha
	}

	changed, err := catalogChangedFiles(root, from)
	if err != nil {
		return err
	}
	changed = catalogOwnChanges(root, *base, changed)
	trailers, err := catalogTrailers(root, from)
	if err != nil {
		return err
	}
	changelog, err := os.ReadFile(filepath.Join(root, workflowupgrade.ChangelogPath))
	if err != nil {
		return err
	}
	last, ok := workflowupgrade.LastRelease(changelog)
	if !ok {
		return fmt.Errorf("no `## vX.Y.Z` release heading in %s", workflowupgrade.ChangelogPath)
	}
	rep, err := workflowupgrade.CheckBranch(workflowupgrade.Branch{
		Changed: changed,
		Base: func(p string) ([]byte, bool) {
			out, err := exec.Command("git", "-C", root, "show", from+":"+p).Output()
			return out, err == nil
		},
		Head:    repoAt(root).Read,
		HeadDir: repoAt(root).Dir,
		HeadFiles: func() []string {
			out, err := catalogGit(root, "ls-files", "--cached", "--others", "--exclude-standard")
			if err != nil {
				return nil
			}
			return strings.Split(out, "\n")
		},
		Trailers: trailers,
	}, last)
	if err != nil {
		return err
	}
	if ctx.JSONOutput {
		if err := writeJSON(ctx.Stdout, rep); err != nil {
			return err
		}
	} else {
		printCatalogCheck(ctx.Stdout, rep)
	}
	if !rep.OK() {
		return fmt.Errorf("catalog-check: bump the entries above or mark them n/a (%s), and fix each catalog problem as it says", workflowupgrade.TrailerForm)
	}
	return nil
}

var releaseBranch = regexp.MustCompile(`^release/(v\d+\.\d+)$`)

// catalogReleaseTag is the newest vX.Y.Z or vX.Y.Z-rc.N tag reachable from
// HEAD on a release/vX.Y branch, and its commit: the commits up to it were
// checked when they were tagged and are published, so they can't be
// reworded. Empty off a release branch or before its line has a tag.
func catalogReleaseTag(root, branch string) (tag, sha string) {
	m := releaseBranch.FindStringSubmatch(branch)
	if m == nil {
		return "", ""
	}
	line := regexp.MustCompile(`^` + regexp.QuoteMeta(m[1]) + `\.\d+(-rc\.\d+)?$`)
	out, err := catalogGit(root, "tag", "--merged", "HEAD", "--list", m[1]+".*")
	if err != nil {
		return "", ""
	}
	best := -1
	for _, t := range strings.Split(out, "\n") {
		if t = strings.TrimSpace(t); !line.MatchString(t) {
			continue
		}
		n, err := catalogGit(root, "rev-list", "--count", t+"..HEAD")
		if err != nil {
			continue
		}
		c, err := strconv.Atoi(n)
		if err != nil || (best >= 0 && c >= best) {
			continue
		}
		if s, err := catalogGit(root, "rev-list", "-n", "1", t); err == nil {
			tag, sha, best = t, s, c
		}
	}
	return tag, sha
}

// catalogTrailers reads the Catalog: lines of the branch's commits since
// mergeBase from their whole message, merge commits included.
func catalogTrailers(root, mergeBase string) ([]workflowupgrade.Trailer, error) {
	out, err := catalogGit(root, "log", "--format=%x1e%h%x1f%B", mergeBase+"..HEAD")
	if err != nil {
		return nil, err
	}
	var trailers []workflowupgrade.Trailer
	for _, rec := range strings.Split(out, "\x1e") {
		sha, msg, ok := strings.Cut(rec, "\x1f")
		if !ok {
			continue
		}
		for _, v := range proof.MessageLines(msg, "Catalog") {
			trailers = append(trailers, workflowupgrade.Trailer{Commit: strings.TrimSpace(sha), Value: v})
		}
	}
	return trailers, nil
}

// catalogOwnChanges drops the changed files whose content in the working
// tree is the base branch's: the branch has them from a merge of it (one
// in progress too), not from its own work.
func catalogOwnChanges(root, base string, changed []string) []string {
	var own []string
	for _, f := range changed {
		onBase, err := exec.Command("git", "-C", root, "show", base+":"+f).Output()
		here, herr := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		switch {
		case err != nil && herr != nil:
			continue // gone here and not on base
		case err == nil && herr == nil && bytes.Equal(onBase, here):
			continue
		}
		own = append(own, f)
	}
	return own
}

// printCatalogCheck prints one line per touched entry, template and bad
// trailer first, then what to do about each stale one, and ends with a
// count, so the head and the tail of the output both say what failed.
func printCatalogCheck(w io.Writer, rep *workflowupgrade.CheckReport) {
	if len(rep.Entries) == 0 && len(rep.Templates) == 0 && len(rep.BadTrailers) == 0 && len(rep.Problems) == 0 {
		fmt.Fprintln(w, "catalog-check ok: this branch changes no catalog feature's sources and no bundled template")
		return
	}
	staleEntries, staleTemplates := 0, 0
	for _, e := range rep.Entries {
		switch e.State {
		case "bumped":
			fmt.Fprintf(w, "✓ catalog %s: bumped to %s (newer than %s)\n", e.ID, e.Version, rep.LastRelease)
		case "n/a":
			fmt.Fprintf(w, "✓ catalog %s: marked n/a: %s\n", e.ID, e.Why)
		default:
			staleEntries++
			fmt.Fprintf(w, "✗ catalog %s: needs a bump or an n/a (changes %s)\n", e.ID, strings.Join(e.Files, ", "))
		}
	}
	for _, t := range rep.Templates {
		if t.State == "bumped" {
			fmt.Fprintf(w, "✓ template %s: stamped %s\n", t.Name, t.Stamp)
			continue
		}
		staleTemplates++
		fmt.Fprintf(w, "✗ template %s: needs a stamp bump (stamp %s)\n", t.Name, t.Stamp)
	}
	for _, b := range rep.BadTrailers {
		fmt.Fprintf(w, "✗ trailer on %s: \"Catalog: %s\": %s\n", b.Commit, b.Value, b.Problem)
	}
	for _, p := range rep.Problems {
		first, rest, _ := strings.Cut(p, "\n")
		fmt.Fprintf(w, "✗ catalog: %s\n", first)
		for _, l := range strings.Split(rest, "\n") {
			if l != "" {
				fmt.Fprintf(w, "    %s\n", l)
			}
		}
	}
	if staleEntries+staleTemplates+len(rep.BadTrailers)+len(rep.Problems) == 0 {
		fmt.Fprintf(w, "catalog-check ok: %d entries and %d templates touched, all bumped or marked n/a\n", len(rep.Entries), len(rep.Templates))
		return
	}

	fmt.Fprintln(w)
	for _, e := range rep.Entries {
		if e.State != "stale" {
			continue
		}
		fmt.Fprintf(w, "✗ catalog: this branch changes %s\n", strings.Join(e.Files, ", "))
		fmt.Fprintf(w, "    entry %s is %s; the next release is %s (last: %s in %s)\n", e.ID, e.Version, rep.NextRelease, rep.LastRelease, workflowupgrade.ChangelogPath)
		fmt.Fprintf(w, "    → if what projects adopt changed: issue-cli workflow catalog-bump %s (sets version: %s and the bundled template stamps with it), then update its what/snippet\n", e.ID, rep.NextRelease)
		fmt.Fprintf(w, "    → if not (refactor, fix): add a commit trailer  Catalog: n/a %s <why>\n", e.ID)
	}
	for _, t := range rep.Templates {
		if t.State == "bumped" {
			continue
		}
		fmt.Fprintf(w, "✗ template: this branch changes %s/%s.yaml (stamp %s)\n", workflowupgrade.TemplatesPath, t.Name, t.Stamp)
		fmt.Fprintf(w, "    → bump it to template: %s@%s, then run issue-cli workflow scan --template %s so its features: record matches (catalog-bump <id> does both for an entry it applies to)\n", t.Name, rep.NextRelease, t.Name)
	}
	if len(rep.BadTrailers) > 0 {
		var ids []string
		for _, e := range rep.Entries {
			ids = append(ids, e.ID)
		}
		fmt.Fprintf(w, "✗ %d Catalog: line(s) mark nothing\n", len(rep.BadTrailers))
		fmt.Fprintf(w, "    → the form is  %s  (any paragraph of the message; several ids comma-separated)\n", workflowupgrade.TrailerForm)
		if len(ids) > 0 {
			fmt.Fprintf(w, "    → the entries this branch touches: %s\n", strings.Join(ids, ", "))
		}
		fmt.Fprintln(w, "    → reword the commit (git commit --amend when it is the last one), or drop the line")
	}
	fmt.Fprintf(w, "\ncatalog-check failed: %d of %d entries and %d of %d templates need a bump or an n/a; %d Catalog: lines mark nothing; %d catalog problems\n", staleEntries, len(rep.Entries), staleTemplates, len(rep.Templates), len(rep.BadTrailers), len(rep.Problems))
}

// catalogChangedFiles lists the files changed since mergeBase, in commits and
// in the working tree, untracked files included.
func catalogChangedFiles(root, mergeBase string) ([]string, error) {
	diff, err := catalogGit(root, "diff", "--name-only", mergeBase)
	if err != nil {
		return nil, err
	}
	untracked, err := catalogGit(root, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(diff+"\n"+untracked, "\n") {
		if f = strings.TrimSpace(f); f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

func catalogGit(dir string, args ...string) (string, error) {
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	var stderr bytes.Buffer
	cmd := exec.Command("git", args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}
