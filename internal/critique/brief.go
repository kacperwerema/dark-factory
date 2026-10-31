package critique

import (
	"fmt"
	"strings"
)

// Input is everything the critic is given: paths, nothing else. It has no
// field for a summary, the worker's reasoning or the issue's comments, so
// the brief can't carry them (factory/a-fresh-critic-…#D-1).
type Input struct {
	// Slug names the issue; From and To the gate being judged.
	Slug, From, To string
	// Issue is the issue file (its body holds the decisions).
	Issue string
	// Epic is the epic's issue file when the issue has a parent.
	Epic string
	// Diff is a file holding the branch's diff against its base ("" when
	// there is none yet, e.g. at design).
	Diff string
	// Changed are the files the branch changed; Docs the docs among them.
	Changed []string
	// Rules are the settled-rules files (docs/departments.md).
	Rules []string
	// Root is the folder the paths are relative to (the worktree).
	Root string
}

// Paths is every path the brief names, in order, without repeats.
func (in Input) Paths() []string {
	var out []string
	seen := map[string]bool{}
	add := func(ps ...string) {
		for _, p := range ps {
			if p = strings.TrimSpace(p); p != "" && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	add(in.Issue, in.Epic, in.Diff)
	add(in.Rules...)
	add(in.Changed...)
	return out
}

// Brief is the critic's prompt: who it is, what to look for, the paths to
// read and the output format. It quotes nothing from the files.
func Brief(in Input) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are a critic reviewing work you did not write, before the gate %s → %s of issue %s.\n", in.From, in.To, in.Slug)
	b.WriteString(`Assume they are wrong. Your job is to find the three things most likely to make this work wrong, not to praise it.

Look for:
- claims with no source: a statement of fact, a "always"/"never", or a rule with no decision (D-n), doc or code cited
- conflicts with the issue's decisions (## Decisions), its epic's, or the settled rules
- cases nobody covered: empty or malformed input, a partial failure, two callers at once, rollback or a retry
- Done lines or acceptance criteria no command could fail
- missing "Must not" lines: what the work must never do that nothing says

Read-only: change nothing, run nothing that writes. Read only the files below (and files they point to when you need them). You are not given the author's summary or reasoning on purpose; judge the files.

Files:
`)
	if in.Root != "" {
		fmt.Fprintf(&b, "(relative to %s)\n", in.Root)
	}
	label := map[string]string{in.Issue: "the issue (body, checklists, decisions)", in.Epic: "its epic", in.Diff: "the branch's diff"}
	for _, r := range in.Rules {
		label[r] = "settled rules"
	}
	for _, d := range in.Docs() {
		if label[d] == "" {
			label[d] = "doc changed by the branch"
		}
	}
	for _, p := range in.Paths() {
		if l := label[p]; l != "" {
			fmt.Fprintf(&b, "- %s  (%s)\n", p, l)
		} else {
			fmt.Fprintf(&b, "- %s\n", p)
		}
	}
	if in.Diff == "" {
		b.WriteString("(no diff yet: judge the design in the issue)\n")
	}
	b.WriteString("\nAnswer in this format and nothing else that starts with blocks, should or note:\n\n")
	b.WriteString(Format)
	b.WriteString(`

blocks: the work is wrong or unsafe as it stands; the gate holds until it is fixed.
should: a real weakness the owner should see; it doesn't hold the gate.
note: worth keeping, no action needed.
Every finding names a path:line from the files above. At most three blocks.
`)
	return b.String()
}

// Docs is the changed files that are docs (*.md outside the issues tree).
func (in Input) Docs() []string {
	var out []string
	for _, c := range in.Changed {
		if strings.HasSuffix(c, ".md") {
			out = append(out, c)
		}
	}
	return out
}
