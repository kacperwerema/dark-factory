package main

import (
	"fmt"

	"github.com/kacperwerema/dark-factory/internal/briefing"
	"github.com/kacperwerema/dark-factory/internal/rules"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var rulesCommand = &Command{
	Name:      "rules",
	ShortHelp: "List the standing rules: the machine's and this project's",
	LongHelp: `List the rules every lead and worker is briefed with, in two layers.

  On every project: the owner's rules file, ~/.config/ai-workflow-engine/rules.md
  ($XDG_CONFIG_HOME honoured). The owner edits it by hand; no command writes
  it and no agent changes it. Each "- " bullet is a rule, an indented line
  continues it, a leading [key] names it.

  On this project: rules: in Product's workflow.yaml. Each entry has text:,
  and may have id: and narrows: <a machine rule's key>.

A project rule adds to the machine's or narrows one. It never removes one:
both print in the briefing, and issue-cli health lint --check rules names a
project rule that contradicts a machine one without declaring narrows:.

  --json   both layers as JSON

Examples:
  issue-cli rules
  issue-cli rules --json`,
	Run: runRules,
}

func init() { registerCommand(rulesCommand) }

type rulesOutput struct {
	Machine rules.File   `json:"machine"`
	Project []rules.Rule `json:"project"`
}

func runRules(ctx *Context, args []string) error {
	fs := newFlagSet("rules", ctx)
	asJSON := fs.Bool("json", false, "both layers as JSON")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return &usageError{msg: "usage: issue-cli rules [--json]"}
	}
	machine, err := rules.LoadMachine()
	if err != nil {
		return fmt.Errorf("reading the machine's rules: %w", err)
	}
	project := briefing.ProjectRules(ctx.Project.LoadWorkflow())
	if *asJSON {
		return writeJSON(ctx.Stdout, rulesOutput{Machine: machine, Project: project})
	}
	fmt.Fprintf(ctx.Stdout, "== On every project (%s, the owner's) ==\n", machine.Path)
	printRules(ctx, machine.Rules, "none yet: the owner adds them to that file")
	fmt.Fprintln(ctx.Stdout, "\n== On this project (workflow.yaml rules:) ==")
	printRules(ctx, project, "none yet: add them under rules: in workflow.yaml")
	return nil
}

func printRules(ctx *Context, all []rules.Rule, none string) {
	if len(all) == 0 {
		fmt.Fprintln(ctx.Stdout, none)
	}
	for _, r := range all {
		line := "- "
		if r.Key != "" {
			line += "[" + r.Key + "] "
		}
		line += r.Text
		if r.Narrows != "" {
			line += " (narrows [" + r.Narrows + "])"
		}
		fmt.Fprintln(ctx.Stdout, line)
	}
}

// printStartRules is start's one line on the rules: the count and where to
// read them, for a session no briefing reached. Nothing when there are none.
func printStartRules(ctx *Context) {
	machine, _ := rules.LoadMachine()
	project := briefing.ProjectRules(ctx.Project.LoadWorkflow())
	if len(machine.Rules)+len(project) == 0 {
		return
	}
	fmt.Fprintf(ctx.Stdout, "Rules: %d on every project, %d on this project: %s\n", len(machine.Rules), len(project), tracker.InjectProject(ctx.Project, "issue-cli rules"))
}
