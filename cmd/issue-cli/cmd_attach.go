package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var attachCommand = &Command{
	Name:      "attach",
	ShortHelp: "Save screenshots as issue attachments and print their markdown links",
	LongHelp: `Save images where the viewer's upload saves them (<issues>/attachments/,
named by the hash of their bytes) and print one markdown image link per file.
The links show on the issue's page in the viewer at once, with no viewer
running and no curl.

Only PNG, JPEG, GIF and WebP (sniffed from the bytes, not the name), at most
10MB each. A file must be inside your worktree, the project or the temp
folder (your scratchpad); copy anything else there first. One bad file
refuses the whole call and saves nothing.

With --section, the links are also appended into that section of the issue
(as append --section does); stdout still carries only the links.

Examples:
  issue-cli attach <slug> shot.png
  issue-cli attach <slug> before.png after.png --section "Test Plan"
  issue-cli attach <slug> board.png --alt "Board at 390px"
  issue-cli attach <slug> shot.png --json`,
	Run: runAttach,
}

func init() {
	registerCommand(attachCommand)
}

// attachRoots lists the folders issue-cli attach reads files from: the
// working directory's checkout (git top level, else the directory itself),
// the project root and the temp folder. Tests replace it.
var attachRoots = func(ctx *Context) []string {
	var roots []string
	if wd, err := os.Getwd(); err == nil {
		roots = append(roots, gitTopOrSelf(wd))
	}
	if ctx.Project != nil {
		roots = append(roots, ctx.Project.Root())
	}
	return append(roots, os.TempDir(), "/tmp")
}

// gitTopOrSelf is the nearest folder at or above dir holding .git (a
// worktree's .git is a file), else dir.
func gitTopOrSelf(dir string) string {
	for d := dir; ; {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		up := filepath.Dir(d)
		if up == d {
			return dir
		}
		d = up
	}
}

// attachment is one saved file, the --json record.
type attachment struct {
	File     string `json:"file"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Markdown string `json:"markdown"`
}

func runAttach(ctx *Context, args []string) error {
	slug, rest, err := requireSlug(args, "attach")
	if err != nil {
		return err
	}
	fs := newFlagSet("attach", ctx)
	sectionFlag := fs.String("section", "", "also append the links into this section of the issue")
	altFlag := fs.String("alt", "", "alt text for every link (default: each file's name without extension)")
	if err := parseFlagsArgs(ctx, fs, rest); err != nil {
		return err
	}
	files := fs.Args()
	if len(files) == 0 {
		return fmt.Errorf("attach requires at least one image file\n\nExample:\n  issue-cli attach %s shot.png", slug)
	}

	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}

	// Check every file before saving any, so one bad file saves nothing.
	roots := resolvedRoots(attachRoots(ctx))
	datas := make([][]byte, len(files))
	for i, f := range files {
		data, err := readAttachable(f, roots)
		if err != nil {
			return &exitCodeError{Code: 1, Msg: fmt.Sprintf("✗ %s: %v (nothing saved)", f, err)}
		}
		datas[i] = data
	}

	var out []attachment
	var lines []string
	for i, f := range files {
		name, err := tracker.SaveAttachment(ctx.Project.IssueDir, datas[i])
		if err != nil {
			return fmt.Errorf("attach %s: %w", f, err)
		}
		alt := *altFlag
		if alt == "" {
			base := filepath.Base(f)
			alt = strings.TrimSuffix(base, filepath.Ext(base))
		}
		url := tracker.AttachmentURL(ctx.Project.Slug, name)
		md := tracker.AttachmentMarkdown(ctx.Project.Slug, name, alt)
		out = append(out, attachment{File: f, Name: name, URL: url, Markdown: md})
		lines = append(lines, md)
	}

	if section := strings.TrimSpace(*sectionFlag); section != "" {
		text := strings.Join(lines, "\n")
		guard := ownerTickGuard(ctx, issue)
		_, _, err = tracker.UpdateIssueBody(issue.FilePath, func(body string) (string, bool, error) {
			next, ok, err := tracker.AppendIssueBodyToSection(body, section, text, false)
			if err == nil && ok {
				err = guard(body, next)
			}
			return next, ok, err
		})
		if err != nil {
			return writeErr("saved, but failed to append the links", err)
		}
		fmt.Fprintf(ctx.Stderr, "✓ Appended %d link(s) to %s under ## %s\n", len(lines), issue.Slug, section)
	}

	if ctx.JSONOutput {
		enc := json.NewEncoder(ctx.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	for _, l := range lines {
		fmt.Fprintln(ctx.Stdout, l)
	}
	return nil
}

// resolvedRoots resolves each root's symlinks (on macOS /tmp and $TMPDIR
// are links into /private), dropping the ones that don't exist.
func resolvedRoots(roots []string) []string {
	var out []string
	for _, r := range roots {
		if r == "" {
			continue
		}
		abs, err := filepath.Abs(r)
		if err != nil {
			continue
		}
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			out = append(out, real)
		}
	}
	return out
}

// readAttachable reads path when it resolves (symlinks followed) to a
// regular file inside one of roots, at most tracker.MaxAttachmentBytes, and
// sniffs as an image.
func readAttachable(path string, roots []string) ([]byte, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("cannot read: %w", err)
	}
	inside := false
	for _, r := range roots {
		if _, ok := relInside(r, real); ok {
			inside = true
			break
		}
	}
	if !inside {
		return nil, fmt.Errorf("outside your worktree, the project and the temp folder (%s); copy it into your scratchpad or worktree first", strings.Join(roots, ", "))
	}
	info, err := os.Stat(real)
	if err != nil {
		return nil, fmt.Errorf("cannot read: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	if info.Size() > tracker.MaxAttachmentBytes {
		return nil, fmt.Errorf("too large (%d bytes, max 10MB)", info.Size())
	}
	data, err := os.ReadFile(real)
	if err != nil {
		return nil, fmt.Errorf("cannot read: %w", err)
	}
	if _, ok := tracker.AttachmentExt(data); !ok {
		return nil, fmt.Errorf("not an image: only PNG, JPEG, GIF or WebP (sniffed from the bytes, not the name)")
	}
	return data, nil
}

// relInside reports whether target lies inside root.
func relInside(root, target string) (string, bool) {
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return rel, true
}
