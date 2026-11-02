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
	// Decisions are the decisions in force for the issue at the gate's
	// status, hard first: refs and the files holding them, never text.
	Decisions []DecisionRef
	// Touched are the hard decisions whose paths: the branch changes (the
	// decisions_respected audit): refs, files, never text.
	Touched []TouchedRef
}

// TouchedRef is a hard decision the branch's changes touch: its ref, the
// issue file holding it and the changed files its paths: match.
type TouchedRef struct {
	Ref   string
	File  string
	Files []string
}

// DecisionRef is one decision in force, for the brief: its ref, strength,
// scope and the issue file whose ## Decisions holds it.
type DecisionRef struct {
	Ref   string
	Hard  bool
	Scope string
	File  string
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
	for _, d := range in.Decisions {
		add(d.File)
	}
	for _, t := range in.Touched {
		add(t.File)
	}
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
	for _, d := range in.Decisions {
		if label[d.File] == "" {
			label[d.File] = "holds decisions in force"
		}
	}
	for _, t := range in.Touched {
		if label[t.File] == "" {
			label[t.File] = "holds a hard decision the branch touches"
		}
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
	if len(in.Decisions) > 0 {
		b.WriteString("\nDecisions in force for this issue (hard first; read each under ## Decisions in its file; a hard one is a rule the work must follow):\n")
		for _, d := range in.Decisions {
			strength := "soft"
			if d.Hard {
				strength = "hard"
			}
			fmt.Fprintf(&b, "- %s · %s · %s — in %s\n", d.Ref, strength, d.Scope, d.File)
		}
	} else {
		b.WriteString("\nDecisions in force for this issue: none beyond the files above.\n")
	}
	if len(in.Touched) > 0 {
		fmt.Fprintf(&b, "\nHard decisions this branch touches (their paths: match changed files; check the change follows each, read under ## Decisions in its file; issue-cli decisions --touched %s):\n", in.Slug)
		for _, t := range in.Touched {
			fmt.Fprintf(&b, "- %s — in %s; files: %s\n", t.Ref, t.File, strings.Join(t.Files, ", "))
		}
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
