package tracker

import (
	"regexp"
	"strings"
)

// OwnSlugEnv names the issue a dispatched worker works on; agentrun exports
// it into every worker session.
const OwnSlugEnv = "ISSUE_VIEWER_ISSUE_SLUG"

// OwnSlugCommands are the issue-cli commands whose <slug> a dispatched
// worker may leave out: issue-cli takes it from OwnSlugEnv, and the worker
// briefing writes them without it (DropOwnSlug). The one list both read.
var OwnSlugCommands = []string{
	"show", "context", "checklist", "check", "uncheck", "check-add", "check-edit", "check-remove",
	"transition", "append", "replace", "comment", "comments", "decide", "prove", "rounds",
	"clarify", "retrospective", "start", "trace", "critique", "check-script", "update", "ship",
}

// OwnSlugPositionalCommands are the OwnSlugCommands that take positionals
// after the slug (ids, text): for them a first word that names no issue is
// the first positional, not a slug.
var OwnSlugPositionalCommands = []string{"check", "uncheck", "check-edit", "check-remove", "comment", "prove", "retrospective", "transition"}

// OwnSlugNote is the line under a worker briefing's first one, which names
// the slug: the commands below leave it out.
const OwnSlugNote = "The issue-cli commands below that act on this issue leave its slug out: issue-cli takes it from " + OwnSlugEnv + ", which your session has. A slug you pass wins."

// ShortenOwnSlug is the worker briefing's last pass: the issue-cli commands
// on the worker's own issue drop its slug (and the <slug> placeholder), and
// OwnSlugNote goes under the first line, the one place the slug stays.
func ShortenOwnSlug(prompt, slug string) string {
	prompt = DropOwnSlug(DropOwnSlug(prompt, slug), "<slug>")
	if slug != "" {
		// decisions --in-force takes the slug as a flag value: the shell
		// fills it in. The issue's own decisions go by their D-n.
		prompt = strings.ReplaceAll(prompt, "--in-force "+slug, `--in-force "$`+OwnSlugEnv+`"`)
		prompt = strings.ReplaceAll(prompt, "\n- "+slug+"#D-", "\n- (this issue) D-")
	}
	first, rest, ok := strings.Cut(prompt, "\n")
	if !ok {
		return prompt
	}
	return first + "\n" + OwnSlugNote + "\n" + rest
}

// DropOwnSlug removes slug from every `issue-cli [--project P] <cmd> <slug>`
// in text, for each cmd in OwnSlugCommands: the worker's issue-cli fills it
// in from OwnSlugEnv. A slug that is only the start of a longer word is
// left alone, as is the slug anywhere else.
func DropOwnSlug(text, slug string) string {
	if slug == "" || !strings.Contains(text, slug) {
		return text
	}
	cmds := make([]string, 0, len(OwnSlugCommands))
	for _, c := range OwnSlugCommands {
		cmds = append(cmds, regexp.QuoteMeta(c))
	}
	re := regexp.MustCompile(`(issue-cli(?: --project [^\s` + "`" + `]+)? (?:` + strings.Join(cmds, "|") + `)) ` + regexp.QuoteMeta(slug) + `([^A-Za-z0-9_./-]|$)`)
	// Run to a fixed point: two matches can share the separator between them.
	for {
		next := re.ReplaceAllString(text, "$1$2")
		if next == text {
			return text
		}
		text = next
	}
}
