package postmortem

import (
	"fmt"
	"github.com/kacperwerema/dark-factory/internal/shell"
	"strings"
)

// Run is where an issue's run can be read: paths and refs only. The brief
// carries these and never the run's text, so the post-mortem agent reads
// the run itself, fresh.
type Run struct {
	Project string
	Slug    string
	Title   string
	// IssueFile holds the body, decisions and comments.
	IssueFile string
	// Sidecars: the data and stats files next to it (may not exist).
	DataFile  string
	StatsFile string
	// AgentLogs is the worker's .agent-logs/<session>/ folder (clilog, prompt).
	AgentLogs string
	// Repo is the checkout; Branch the work branch, "" when it no longer
	// exists (merged and deleted); Base the sha it was cut from ("" when
	// unknown); Main the main branch ("" reads as main).
	Repo   string
	Branch string
	Base   string
	Main   string
	// Retros are the retro files already written for the issue.
	Retros []string
	// Children are an epic's child issues, so its post-mortem reads the
	// whole run at the higher level; empty for any other issue.
	Children []Child
	// CLI is issue-cli with --project when it needs one.
	CLI string
	// Statuses are the workflow's statuses, which a memory action's At:
	// may name.
	Statuses []string
}

// Child is one child of an epic: paths and refs only, as for the epic.
type Child struct {
	Slug      string
	IssueFile string
	// Branch is its work branch, "" when it no longer exists: the brief
	// then points at its commits on main by slug.
	Branch string
	// Retros are its worker's retros; Postmortems its own post-mortems.
	Retros      []string
	Postmortems []string
}

// Brief is the post-mortem agent's prompt: who it is, the paths to read,
// the six sections, the one test, the places and the command that saves.
func Brief(r Run) string {
	cli := r.CLI
	if cli == "" {
		cli = "issue-cli"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "You are the post-mortem agent for %s (%s). You did none of its work: read the run yourself from the paths below, fresh. Nothing of it is pasted here on purpose.\n\n", r.Slug, r.Title)
	b.WriteString("## Read\n\n")
	line := func(label, v string) {
		if v != "" {
			fmt.Fprintf(&b, "- %s: %s\n", label, v)
		}
	}
	line("Issue (body, decisions, comments)", r.IssueFile)
	line("Data sidecar", r.DataFile)
	line("Stats sidecar (dispatches, time in each status)", r.StatsFile)
	line("Worker's session logs (the clilog: one line per issue-cli call; the prompt it got)", r.AgentLogs)
	mainName := r.Main
	if mainName == "" {
		mainName = "main"
	}
	if r.Repo != "" {
		if r.Branch != "" {
			// --first-parent: a chore(merge) of main brings other issues'
			// commits onto the branch; the range shows only its own.
			rng := r.Branch
			if r.Base != "" {
				rng = r.Base + ".." + r.Branch
			}
			fmt.Fprintf(&b, "- Commits: git -C %s log --first-parent --stat %s (and git log --grep %s %s for what reached main)\n", r.Repo, rng, shell.Quote(r.Slug), mainName)
		} else {
			fmt.Fprintf(&b, "- Commits: git -C %s log --stat --grep %s %s (its work branch is gone)\n", r.Repo, shell.Quote(r.Slug), mainName)
		}
	}
	for _, p := range r.Retros {
		line("Worker's retro", p)
	}
	fmt.Fprintf(&b, "- Commands: %s show %s · %s memory candidates %s · %s memory search \"<words>\"\n\n", cli, r.Slug, cli, r.Slug, cli)
	if len(r.Children) > 0 {
		b.WriteString("### Children\n\n")
		b.WriteString("This is an epic: read its run at the higher level, across its children. Look for what repeats from child to child and for what the epic's split or order cost; leave a lesson one child's own post-mortem already proposes to that one.\n\n")
		for _, c := range r.Children {
			fmt.Fprintf(&b, "- %s: issue %s", c.Slug, c.IssueFile)
			if c.Branch != "" {
				fmt.Fprintf(&b, " · branch %s", c.Branch)
			} else if r.Repo != "" {
				fmt.Fprintf(&b, " · commits: git -C %s log --grep %s %s", r.Repo, shell.Quote(c.Slug), mainName)
			}
			for _, p := range c.Retros {
				fmt.Fprintf(&b, " · retro %s", p)
			}
			for _, p := range c.Postmortems {
				fmt.Fprintf(&b, " · post-mortem %s", p)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("Only read. Don't edit code, docs or the issue, don't transition, comment or add memories: the owner agrees to every action first.\n\n")
	b.WriteString("## Write\n\n")
	b.WriteString("A markdown file with these six headings, in order (write \"none\" under one with nothing):\n\n")
	for _, s := range Sections {
		fmt.Fprintf(&b, "## %s\n", s)
	}
	b.WriteString("\nSteps is what happened in order; Worked and Did not work are about the workflow and the tools, not the code; Decisions are the D-n that mattered; Known is what a next agent should already know.\n\n")
	fmt.Fprintf(&b, "Each proposed action passes one test: %q. If it doesn't, leave it out. Each names exactly one place:\n\n", TheTest)
	b.WriteString("- memory <kind>: a shared memory (fact, convention, pitfall, howto, who); At: <status> when it matters at one step, so transition shows it to whoever enters that status")
	if len(r.Statuses) > 0 {
		fmt.Fprintf(&b, " (%s)", strings.Join(r.Statuses, ", "))
	}
	b.WriteString("\n")
	b.WriteString("- file <repo path .md>: CLAUDE.md, AGENTS.md or a page under docs/ (Under: <heading> to place it)\n")
	b.WriteString("- skill <name>: .claude/skills/<name>/SKILL.md\n")
	b.WriteString("- workflow: a workflow change, filed as an experiment proposal (never edited directly)\n")
	b.WriteString("- none <doc path#anchor>: the docs already say it; name where\n\n")
	b.WriteString("The shape:\n\n")
	b.WriteString(Shape)
	b.WriteString("\n\n## Save\n\n")
	fmt.Fprintf(&b, "  %s postmortem write %s --file <your file>\n\n", cli, r.Slug)
	b.WriteString("It refuses an action without the test or a place and says why; fix the file and run it again. Once it saves, stop: the owner agrees or refuses the actions in one round.\n")
	return b.String()
}
