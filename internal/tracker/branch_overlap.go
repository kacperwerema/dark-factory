package tracker

import (
	"fmt"
	pathpkg "path"
	"sort"
	"strings"
)

// overlapsShown caps the branches start and the briefing name; overlapFiles
// caps the files named on each line.
const (
	overlapsShown = 8
	overlapFiles  = 3
)

// BranchOverlap is another open work branch that changes files this issue is
// likely to change.
type BranchOverlap struct {
	Branch    string
	IssueSlug string // "" when no issue matches the branch
	Status    string // the issue's status
	Files     []string
	// Own counts the Files this issue's branch already changes; they come
	// first in Files.
	Own int
}

// BranchOverlaps is what an issue picking up work should know about the
// branches around it: how far its own branch is behind main, and which
// sibling branches touch the same files.
type BranchOverlaps struct {
	Main   string
	Behind int // commits on main the issue's branch doesn't have
	Others []BranchOverlap
}

// IssueBranchOverlaps compares the issue's work branch with the other open
// work/ branches. A branch overlaps when its diff touches a file the issue's
// system maps to (workbench.systems paths) or one the issue's own branch
// already changes; changelogs don't count. Nil when there is nothing to say,
// or no git to ask.
func IssueBranchOverlaps(proj *Project, root string, issue *Issue, issues []*Issue) *BranchOverlaps {
	if proj == nil || root == "" || issue == nil {
		return nil
	}
	mainBranch, err := MainBranch(root)
	if err != nil {
		return nil
	}
	name := "work/" + issue.Slug
	out := &BranchOverlaps{Main: mainBranch, Behind: countCommits(root, BranchHead(root, name), BranchHead(root, mainBranch))}

	_, system, _ := proj.LoadWorkflow().Workbench.SystemConfig(issue.System)
	branches := OpenBranches(root, mainBranch, issues)
	var own map[string][2]int
	for _, b := range branches {
		if b.Name == name {
			own, _ = branchNumstat(root, b)
		}
	}
	closed := ClosedIssueFunc(proj)
	for _, b := range branches {
		if b.Name == name || !strings.HasPrefix(b.Name, "work/") {
			continue
		}
		o := BranchOverlap{Branch: b.Name, IssueSlug: b.IssueSlug}
		if b.IssueSlug != "" {
			if sibling := FindIssue(issues, b.IssueSlug); sibling != nil {
				if closed(sibling) {
					continue
				}
				o.Status = sibling.Status
			}
		}
		stats, err := branchNumstat(root, b)
		if err != nil {
			continue
		}
		for path := range stats {
			if pathpkg.Base(path) == "CHANGELOG.md" {
				continue // every branch adds a line; the merge keeps both
			}
			if _, mine := own[path]; mine {
				o.Own++
				o.Files = append(o.Files, path)
			} else if system.Owns(path) {
				o.Files = append(o.Files, path)
			}
		}
		if len(o.Files) == 0 {
			continue
		}
		sort.Slice(o.Files, func(i, j int) bool {
			_, a := own[o.Files[i]]
			_, b := own[o.Files[j]]
			if a != b {
				return a
			}
			return o.Files[i] < o.Files[j]
		})
		out.Others = append(out.Others, o)
	}
	sort.SliceStable(out.Others, func(i, j int) bool {
		a, b := out.Others[i], out.Others[j]
		if a.Own != b.Own {
			return a.Own > b.Own
		}
		if len(a.Files) != len(b.Files) {
			return len(a.Files) > len(b.Files)
		}
		return a.Branch < b.Branch
	})
	if out.Behind == 0 && len(out.Others) == 0 {
		return nil
	}
	return out
}

// Lines is the block start and the briefing share: one line for a branch
// behind main, one per overlapping branch, and the ask that goes with them.
func (o *BranchOverlaps) Lines(slug string) []string {
	if o == nil {
		return nil
	}
	var lines []string
	if o.Behind > 0 {
		lines = append(lines, fmt.Sprintf("Your branch is %d commit%s behind %s. Merge it before editing anything: git merge %s -m \"chore(merge): %s into %s\"", o.Behind, plural(o.Behind), o.Main, o.Main, o.Main, slug))
	}
	if len(o.Others) == 0 {
		return lines
	}
	if len(o.Others) == 1 {
		lines = append(lines, "1 open branch changes the same files:")
	} else {
		lines = append(lines, fmt.Sprintf("%d open branches change the same files:", len(o.Others)))
	}
	ask := ""
	for i, b := range o.Others {
		if ask == "" {
			ask = b.IssueSlug
		}
		if i >= overlapsShown {
			continue
		}
		lines = append(lines, "- "+b.line())
	}
	if n := len(o.Others) - overlapsShown; n > 0 {
		lines = append(lines, fmt.Sprintf("- … %d more: see the Branches tab of the Metrics view", n))
	}
	if ask != "" {
		lines = append(lines, fmt.Sprintf("Agree who changes what before the merge: `issue-cli ask %s \"...\"`.", ask))
	}
	return lines
}

func (b BranchOverlap) line() string {
	who := strings.TrimPrefix(b.Branch, "work/")
	if b.Status != "" {
		who += " (" + b.Status + ")"
	} else if b.IssueSlug == "" {
		who += " (no issue)"
	}
	count := fmt.Sprintf("%d file%s", len(b.Files), plural(len(b.Files)))
	if b.Own > 0 {
		count += fmt.Sprintf(", %d also on your branch", b.Own)
	}
	files := b.Files
	more := ""
	if len(files) > overlapFiles {
		more = fmt.Sprintf(", +%d more", len(files)-overlapFiles)
		files = files[:overlapFiles]
	}
	return fmt.Sprintf("%s: %s: %s%s", who, count, strings.Join(files, ", "), more)
}

// BranchOverlapPromptSection is the "## Open branches" part of a worker's
// briefing; empty when the branch is level with main and alone on its files.
func BranchOverlapPromptSection(o *BranchOverlaps, slug string) string {
	lines := o.Lines(slug)
	if len(lines) == 0 {
		return ""
	}
	return "\n\n## Open branches\n\n" + strings.Join(lines, "\n") + "\n"
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
