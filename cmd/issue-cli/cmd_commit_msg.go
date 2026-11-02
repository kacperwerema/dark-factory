package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/privacy"
	"github.com/kacperwerema/dark-factory/internal/proof"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

const commitMsgUsage = `Usage:
  issue-cli workflow commit-msg <message-file>   # git's commit-msg hook runs it
  issue-cli workflow commit-msg --install        # write the hook (make hooks)

Checks every Catalog: and Proves: line of a commit message with the parsers
catalog-check and the proof check use, before the commit exists, and refuses
it with the fix when a line marks or links nothing:
  Catalog: n/a <id>[,<id>...] <why>     an entry id right after n/a, no colon
                                        after it, an id the catalog has
  Proves: AC1[, AC2] [TestName ...]     test names only: no <slug>#AC1, no
                                        package path, no playtest step (steps
                                        prove through proves: in their session)
--install writes the repository's commit-msg hook (core.hooksPath, else the
common .git/hooks every worktree shares); an existing hook is kept as
commit-msg.local and runs first.`

// commitMsgMarker tells our commit-msg hook from someone else's.
const commitMsgMarker = "# issue-cli commit-msg check"

const commitMsgScript = `#!/bin/sh
` + commitMsgMarker + ` (issue-cli workflow commit-msg --install, or make hooks)
here=$(dirname "$0")
if [ -x "$here/commit-msg.local" ]; then
	"$here/commit-msg.local" "$@" || exit $?
fi
if ! command -v issue-cli >/dev/null 2>&1; then
	echo "commit-msg: issue-cli is not on PATH: Catalog: and Proves: lines not checked" >&2
	exit 0
fi
out=$(issue-cli workflow commit-msg "$1" 2>&1)
code=$?
case "$out" in
*"unknown workflow subcommand"*)
	echo "commit-msg: the installed issue-cli is older than this check (make install): Catalog: and Proves: lines not checked" >&2
	exit 0 ;;
esac
[ -n "$out" ] && printf '%s\n' "$out" >&2
exit $code
`

func runCommitMsg(ctx *Context, args []string) error {
	fs := newFlagSet("workflow commit-msg", ctx)
	install := fs.Bool("install", false, "write the repository's commit-msg hook")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	root, rootErr := catalogGit("", "rev-parse", "--show-toplevel")
	if *install {
		if rootErr != nil {
			return fmt.Errorf("commit-msg --install runs inside a git checkout: %w", rootErr)
		}
		path, kept, err := installCommitMsgHook(root)
		if err != nil {
			return err
		}
		fmt.Fprintf(ctx.Stdout, "✓ commit-msg check installed: %s\n", path)
		if kept {
			fmt.Fprintln(ctx.Stdout, "✓ The existing commit-msg hook is kept as commit-msg.local and runs first")
		}
		return nil
	}
	if fs.NArg() != 1 {
		return &usageError{msg: fmt.Sprintf("commit-msg takes the message file git passes\n\n%s", commitMsgUsage)}
	}
	data, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	// Outside a dark-factory checkout the ids aren't checked; the forms are.
	var known map[string]bool
	if rootErr == nil {
		if ids, err := workflowupgrade.EntryIDs(repoAt(root)); err == nil {
			known = ids
		}
	}
	problems := CommitMsgProblems(string(data), known)
	if len(problems) == 0 {
		return nil
	}
	printCommitMsgProblems(ctx.Stderr, problems)
	return &exitCodeError{Code: 1}
}

// CommitMsgProblems lists every Catalog: and Proves: line of msg (a commit
// message as git hands it to the hook) that marks or links nothing, each
// as `<the line>: <what is wrong and the fix>`.
func CommitMsgProblems(msg string, known map[string]bool) []string {
	msg = stripCommitComments(msg)
	var out []string
	var trailers []workflowupgrade.Trailer
	for _, v := range proof.MessageLines(msg, "Catalog") {
		trailers = append(trailers, workflowupgrade.Trailer{Value: v})
	}
	for _, b := range workflowupgrade.CheckTrailers(trailers, known) {
		out = append(out, fmt.Sprintf("Catalog: %s\n    → %s", b.Value, b.Problem))
	}
	for _, v := range proof.MessageLines(msg, "Proves") {
		for _, p := range proof.CheckProves(v, "AC") {
			out = append(out, fmt.Sprintf("Proves: %s\n    → %s", v, p))
		}
	}
	return out
}

// stripCommitComments drops what git strips from the message after the
// hook: lines starting with # and everything below the scissors line.
func stripCommitComments(msg string) string {
	var keep []string
	for _, l := range strings.Split(msg, "\n") {
		if strings.HasPrefix(l, "# ") && strings.Contains(l, ">8") {
			break
		}
		if strings.HasPrefix(l, "#") {
			continue
		}
		keep = append(keep, l)
	}
	return strings.Join(keep, "\n")
}

func printCommitMsgProblems(w io.Writer, problems []string) {
	fmt.Fprintf(w, "✗ commit refused: %d Catalog:/Proves: line(s) would mark or link nothing\n", len(problems))
	for _, p := range problems {
		fmt.Fprintf(w, "✗ %s\n", p)
	}
	fmt.Fprintln(w, "Fix the message and commit again (your message is in .git/COMMIT_EDITMSG). Forms: issue-cli help workflow, commit-msg.")
}

// installCommitMsgHook writes the check as root's commit-msg hook, keeping
// a hook that isn't ours as commit-msg.local.
func installCommitMsgHook(root string) (path string, kept bool, err error) {
	dir, err := privacy.HooksDir(root)
	if err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false, err
	}
	path = filepath.Join(dir, "commit-msg")
	if data, rerr := os.ReadFile(path); rerr == nil && !strings.Contains(string(data), commitMsgMarker) {
		local := filepath.Join(dir, "commit-msg.local")
		if _, serr := os.Stat(local); serr == nil {
			return "", false, fmt.Errorf("%s exists and is not the commit-msg check, and %s is taken too: merge them by hand", path, local)
		}
		if err := os.Rename(path, local); err != nil {
			return "", false, err
		}
		kept = true
	} else if rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
		return "", false, rerr
	}
	return path, kept, os.WriteFile(path, []byte(commitMsgScript), 0o755)
}
