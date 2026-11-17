package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var evidenceCommand = &Command{
	Name:      "evidence",
	ShortHelp: "Save stills or any proof file (a profile, a text capture) into the issue's evidence folder, which its Evidence tab shows (sub: add|list)",
	LongHelp: `Keep an issue's stills (screenshots taken outside a playtest run) and
proof files (a CPU profile, pprof -top output, a benchmark log) in
<issues>/_evidence/<slug>/ of the project's issues folder, the primary
checkout's, whatever folder you run it from: a worktree has no issues/ tree.
The issue's Evidence tab shows that folder's stills, and lists its other
files with a link: a text file (UTF-8, no NUL byte) opens as text in the
browser, anything else downloads.

Subcommands:
  add [<slug>] <file>... [--step <id>] [--force]
      Copy each file in, keeping its name (an image's extension follows the
      sniffed type, so shot.jpeg is saved as shot.jpg; another file's name is
      cleaned to letters, digits, ".", "_" and "-"). Prints each saved path
      and a markdown link to paste into the body. Any file, at most 10MB
      each, from inside your worktree, the project or the temp folder (your
      scratchpad); a non-image named like an image (fake.png) is refused. --step TP3 files the still under that step:
      it is saved as TP3-<name>. A name already there is refused unless
      --force replaces it; one refused file saves nothing.
  list [<slug>]
      Print the stills there: name, size, date.

Images or files for the body or a comment, with no step or name to keep, go through
issue-cli attach instead.

Examples:
  issue-cli evidence add <slug> shot.png
  issue-cli evidence add <slug> before.png after.png --step TP3
  issue-cli evidence add <slug> cpu.pprof top.txt --step AC1
  issue-cli evidence add <slug> after.png --force
  issue-cli evidence list <slug>
  issue-cli evidence list <slug> --json`,
	Run:         runEvidence,
	Subcommands: []string{"add", "list"},
	SubAliases:  map[string]string{"ls": "list"},
}

func init() {
	registerCommand(evidenceCommand)
}

func runEvidence(ctx *Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("evidence requires a subcommand\n\nUsage:\n  issue-cli evidence add <slug> <file>... [--step <id>] [--force]\n  issue-cli evidence list <slug>")
	}
	switch args[0] {
	case "add":
		return runEvidenceAdd(ctx, args[1:])
	case "list", "ls":
		return runEvidenceList(ctx, args[1:])
	default:
		return fmt.Errorf("unknown evidence subcommand: %s\n\nValid: add, list", args[0])
	}
}

// evidenceSlug takes the slug off args: a first word that names an issue on
// the board, else the worker's own slug (ISSUE_VIEWER_ISSUE_SLUG) when it
// has one. A file path with a "/" is never read as a slug.
func evidenceSlug(ctx *Context, args []string, cmd string) (string, []string, error) {
	own := ownSlugEnv()
	if own == "" {
		return requireSlug(args, cmd)
	}
	if len(args) > 0 && !isFlagToken(args[0]) && namesIssue(ctx, args[0]) {
		return args[0], args[1:], nil
	}
	noteOwnSlug(ctx, own)
	return own, args, nil
}

// savedStill is one file evidence add saved, the --json record.
type savedStill struct {
	File     string `json:"file"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	URL      string `json:"url,omitempty"`
	Markdown string `json:"markdown,omitempty"`
}

func runEvidenceAdd(ctx *Context, args []string) error {
	slug, rest, err := evidenceSlug(ctx, args, "evidence add")
	if err != nil {
		return err
	}
	fs := newFlagSet("evidence add", ctx)
	stepFlag := fs.String("step", "", "file the stills or files under this step id (saved as <id>-<name>)")
	forceFlag := fs.Bool("force", false, "replace a still of the same name")
	if err := parseFlagsArgs(ctx, fs, rest); err != nil {
		return err
	}
	files := fs.Args()
	if len(files) == 0 {
		return fmt.Errorf("evidence add requires at least one file\n\nExample:\n  issue-cli evidence add %s shot.png", slug)
	}
	step := strings.ToUpper(strings.TrimSpace(*stepFlag))
	if step != "" && !tracker.EvidenceStepPattern.MatchString(step) {
		return &exitCodeError{Code: 2, Msg: fmt.Sprintf("✗ --step %q: a step id is letters then digits (TP3, AC2, S1)", *stepFlag)}
	}

	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	issueDir := ctx.Project.IssueDir
	dir := tracker.EvidenceDir(issueDir, issue.Slug)

	// Check every file and name before saving any, so one bad file saves
	// nothing.
	roots := resolvedRoots(attachRoots(ctx))
	datas := make([][]byte, len(files))
	names := make([]string, len(files))
	seen := map[string]string{}
	for i, f := range files {
		data, err := readAttachable(f, roots)
		if err != nil {
			return &exitCodeError{Code: 1, Msg: fmt.Sprintf("✗ %s: %v (nothing saved)", f, err)}
		}
		name, err := tracker.EvidenceName(f, step, data)
		if err != nil {
			return &exitCodeError{Code: 1, Msg: fmt.Sprintf("✗ %s: %v (nothing saved)", f, err)}
		}
		if other, dup := seen[name]; dup {
			return &exitCodeError{Code: 1, Msg: fmt.Sprintf("✗ %s and %s would both be saved as %s (nothing saved); rename one", other, f, name)}
		}
		seen[name] = f
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil && !*forceFlag {
			return &exitCodeError{Code: 1, Msg: fmt.Sprintf("✗ %s is already in %s (nothing saved); pass --force to replace it, or rename the file", name, dir)}
		}
		datas[i], names[i] = data, name
	}

	var out []savedStill
	for i, f := range files {
		path, err := tracker.SaveEvidence(issueDir, issue.Slug, names[i], datas[i], *forceFlag)
		if err != nil {
			var exists *tracker.EvidenceExistsError
			if errors.As(err, &exists) {
				return &exitCodeError{Code: 1, Msg: fmt.Sprintf("✗ %v; pass --force to replace it", err)}
			}
			return fmt.Errorf("evidence add %s: %w", f, err)
		}
		s := savedStill{File: f, Name: names[i], Path: path}
		if u := evidenceURL(ctx, path); u != "" {
			s.URL = u
			if tracker.AttachmentKind(datas[i]) == "image" {
				s.Markdown = fmt.Sprintf("![%s](%s)", strings.TrimSuffix(names[i], filepath.Ext(names[i])), u)
			} else {
				s.Markdown = fmt.Sprintf("[%s](%s)", names[i], u)
			}
		}
		out = append(out, s)
	}

	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, out)
	}
	for _, s := range out {
		fmt.Fprintf(ctx.Stdout, "✓ %s\n", s.Path)
		if s.Markdown != "" {
			fmt.Fprintf(ctx.Stdout, "  %s\n", s.Markdown)
		}
	}
	fmt.Fprintf(ctx.Stderr, "Shown on the issue's Evidence tab (stills as images, other files as links); issue-cli evidence list %s lists them.\n", issue.Slug)
	return nil
}

// evidenceURL is the viewer's /files/ URL for a saved still, "" when the
// issues folder is outside the project root (the route serves only inside).
func evidenceURL(ctx *Context, path string) string {
	root := ctx.Project.Root()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	if p, err := filepath.EvalSymlinks(path); err == nil {
		path = p
	}
	rel, ok := relInside(root, path)
	if !ok {
		return ""
	}
	return "/p/" + ctx.Project.Slug + "/files/" + (&url.URL{Path: filepath.ToSlash(rel)}).EscapedPath()
}

func runEvidenceList(ctx *Context, args []string) error {
	slug, rest, err := evidenceSlug(ctx, args, "evidence list")
	if err != nil {
		return err
	}
	fs := newFlagSet("evidence list", ctx)
	if err := parseFlags(ctx, fs, rest); err != nil {
		return err
	}
	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	stills, err := tracker.ListEvidence(ctx.Project.IssueDir, issue.Slug)
	if err != nil {
		return fmt.Errorf("evidence list: %w", err)
	}
	if ctx.JSONOutput {
		if stills == nil {
			stills = []tracker.EvidenceFile{}
		}
		return writeJSON(ctx.Stdout, stills)
	}
	dir := tracker.EvidenceDir(ctx.Project.IssueDir, issue.Slug)
	if len(stills) == 0 {
		fmt.Fprintf(ctx.Stdout, "No evidence for %s yet: issue-cli evidence add %s <file>\n", issue.Slug, issue.Slug)
		return nil
	}
	fmt.Fprintf(ctx.Stdout, "%s (%d)\n", dir, len(stills))
	for _, s := range stills {
		fmt.Fprintf(ctx.Stdout, "  %s  %s  %s\n", s.Name, humanBytes(s.Size), s.ModTime.Format("2006-01-02 15:04"))
	}
	return nil
}

// humanBytes is a size in B, KB or MB.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
