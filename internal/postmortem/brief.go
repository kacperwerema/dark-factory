package postmortem

import (
	"fmt"
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
	// Repo is the checkout; Branch the work branch; Base the sha it was cut
	// from ("" when unknown).
	Repo   string
	Branch string
	Base   string
	// Retros are the retro files already written for the issue.
	Retros []string
	// CLI is issue-cli with --project when it needs one.
	CLI string
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
	if r.Repo != "" {
		rng := r.Branch
		if r.Base != "" {
			rng = r.Base + ".." + r.Branch
		}
		fmt.Fprintf(&b, "- Commits: git -C %s log --stat %s (and git log --grep %q main for what reached main)\n", r.Repo, rng, r.Slug)
	}
	for _, p := range r.Retros {
		line("Worker's retro", p)
	}
	fmt.Fprintf(&b, "- Commands: %s show %s · %s memory candidates %s · %s memory search \"<words>\"\n\n", cli, r.Slug, cli, r.Slug, cli)
	b.WriteString("Only read. Don't edit code, docs or the issue, don't transition, comment or add memories: the owner agrees to every action first.\n\n")
	b.WriteString("## Write\n\n")
	b.WriteString("A markdown file with these six headings, in order (write \"none\" under one with nothing):\n\n")
	for _, s := range Sections {
		fmt.Fprintf(&b, "## %s\n", s)
	}
	b.WriteString("\nSteps is what happened in order; Worked and Did not work are about the workflow and the tools, not the code; Decisions are the D-n that mattered; Known is what a next agent should already know.\n\n")
	fmt.Fprintf(&b, "Each proposed action passes one test: %q. If it doesn't, leave it out. Each names exactly one place:\n\n", TheTest)
	b.WriteString("- memory <kind>: a shared memory (fact, convention, pitfall, howto, who)\n")
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
