package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/telemetry"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// issueSlug is requireSlug for the commands in tracker.OwnSlugCommands: a
// dispatched worker may leave its own slug out, and the slug comes from
// ISSUE_VIEWER_ISSUE_SLUG. A slug on the command line always wins.
//
//   - no args, or a flag first: the env slug, else "<cmd> requires <slug>".
//   - a command that takes positionals after the slug (check D1, comment
//     "text"): the first word is the slug when it names an issue exactly
//     (or as a unique /suffix); else, with the env set, it is the first
//     positional, unless it is shaped like <system>/<name> (slugPath): that
//     word stays the slug, so a mistyped slug fails as not found instead of
//     landing on the worker's own issue.
//   - any other command: a first word is the slug, as before.
func issueSlug(ctx *Context, args []string, cmd string) (string, []string, error) {
	own := ownSlugEnv()
	if own == "" {
		return requireSlug(args, cmd)
	}
	if len(args) == 0 || isFlagToken(args[0]) {
		noteOwnSlug(ctx, own)
		return own, args, nil
	}
	if !slices.Contains(tracker.OwnSlugPositionalCommands, cmd) || slugPath(args[0]) || namesIssue(ctx, args[0]) {
		return args[0], args[1:], nil
	}
	noteOwnSlug(ctx, own)
	return own, args, nil
}

// slugPathRe is a word shaped like a slug with its system folder: slug
// characters only, with a "/". Text with a space or other punctuation
// ("tests: 5/5 passed") is never one.
var slugPathRe = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)+$`)

// slugPath reports whether word looks like a <system>/<name> slug.
func slugPath(word string) bool {
	return slugPathRe.MatchString(word)
}

// ownSlugEnv is the worker's own slug, "" outside a dispatched session.
func ownSlugEnv() string {
	return strings.TrimSpace(os.Getenv(tracker.OwnSlugEnv))
}

// namesIssue reports whether word is an issue's slug on this board, exactly
// (case-insensitive, .md dropped) or as the one issue ending in /word: the
// matches findIssueOrErr takes without calling them partial.
func namesIssue(ctx *Context, word string) bool {
	if ctx.Project == nil {
		return false
	}
	issues, err := tracker.LoadIssues(ctx.Project.IssueDir)
	if err != nil {
		return false
	}
	w := strings.ToLower(strings.TrimSuffix(word, ".md"))
	suffix := 0
	for _, issue := range issues {
		s := strings.ToLower(issue.Slug)
		if s == w {
			return true
		}
		if strings.HasSuffix(s, "/"+w) {
			suffix++
		}
	}
	return suffix == 1
}

// noteOwnSlug says once per issue, on stderr, where the left-out slug came
// from. A marker under issue-cli's state dir keeps it to once; when the
// marker can't be written the line prints each time.
func noteOwnSlug(ctx *Context, slug string) {
	marker := ownSlugMarker(slug)
	if marker != "" {
		if _, err := os.Stat(marker); err == nil {
			return
		}
	}
	fmt.Fprintf(ctx.Stderr, "issue-cli: no slug given: using your issue %s (%s)\n", slug, tracker.OwnSlugEnv)
	if marker == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err == nil {
		_ = os.WriteFile(marker, []byte(slug+"\n"), 0o644)
	}
}

// ownSlugMarker is the file that records the note was shown for slug, ""
// when issue-cli has no state dir.
func ownSlugMarker(slug string) string {
	p := telemetry.GlobalPath()
	if p == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(slug))
	return filepath.Join(filepath.Dir(p), "own-slug", hex.EncodeToString(sum[:8]))
}
