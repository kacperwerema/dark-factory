package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/projectinit"
	"github.com/michal-franc/issue-viewer/internal/workflowupgrade"
)

var projectCommand = &Command{
	Name:      "project",
	ShortHelp: "Make a new project: folder, workflow.yaml, its projects.yaml entry and the next steps",
	LongHelp: `Make a new project in one command (project init).

  project init <dir>   create <dir> with issues/, docs/ and workflow.yaml
                       (a bundled template, with its stamp), and the
                       project's projects.yaml entry. Without --config the
                       entry is printed; with --config <file> it goes into
                       that file as text (every other line kept), or a new
                       file when there is none. No other config is touched:
                       not the one issue-cli reads, not the viewer's.
                       Nothing is ever overwritten: what is already there is
                       reported and left alone, so a second run says what
                       it found. Then it prints how to open the board (the
                       company page has the New department tile), start the
                       CEO and add a department.

Flags:
  --name <Name>        the project's name (default: the folder's name)
  --slug <slug>        its slug for --project (default: from the name)
  --template <name>    workflow template (default development; the
                       others as in issue-cli workflow init)
  --config <file>      the projects.yaml to add the entry to (after the
                       command; the global --config only picks what to read)
  --dry-run            list every folder, file and config change; change
                       nothing
  --json               the plan as JSON

Examples:
  issue-cli project init ~/src/shop --dry-run
  issue-cli project init ~/src/shop --name "Acme Shop" --config ~/.config/ai-workflow-engine/projects.yaml
  issue-cli project init ./sandbox --config ./sandbox/projects.yaml`,
	Run:         runProject,
	Subcommands: []string{"init"},
}

func init() {
	registerCommand(projectCommand)
}

func runProject(ctx *Context, args []string) error {
	if len(args) == 0 || args[0] != "init" {
		return &usageError{msg: "usage: issue-cli project init <dir> [--name <Name>] [--slug <slug>] [--template <name>] [--config <file>] [--dry-run]"}
	}
	return runProjectInit(ctx, args[1:])
}

func runProjectInit(ctx *Context, args []string) error {
	fs := newFlagSet("project init", ctx)
	name := fs.String("name", "", "the project's name (default: the folder's name)")
	slug := fs.String("slug", "", "the project's slug (default: from the name)")
	tmpl := fs.String("template", projectinit.DefaultTemplate, "workflow template: "+strings.Join(workflowupgrade.Templates(), ", "))
	config := fs.String("config", "", "the projects.yaml to add the entry to (default: print it only)")
	dry := fs.Bool("dry-run", false, "list what would change, change nothing")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return &usageError{msg: "usage: issue-cli project init <dir> [--name <Name>] [--slug <slug>] [--template <name>] [--config <file>] [--dry-run]"}
	}
	plan, err := projectinit.NewPlan(projectinit.Options{Dir: fs.Arg(0), Name: *name, Slug: *slug, Template: *tmpl, Config: *config})
	if err != nil {
		return err
	}
	if !*dry && plan.Config.State == projectinit.ConfigAdd {
		if err := refuseWorkerConfig(ctx, plan.Config.Path); err != nil {
			return err
		}
	}
	if !*dry {
		if err := plan.Apply(); err != nil {
			return err
		}
	}
	next := projectInitNext(plan)
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, struct {
			*projectinit.Plan
			DryRun bool     `json:"dry_run"`
			Next   []string `json:"next"`
		}{plan, *dry, next})
	}
	printProjectInit(ctx, plan, *dry, next, projectInitApply(plan, *name, *slug, *tmpl))
	return nil
}

// refuseWorkerConfig stops a worker from adding a project to the config
// issue-cli reads (the one its project is in): that is the owner's
// projects.yaml. A sandbox config is fine.
func refuseWorkerConfig(ctx *Context, config string) error {
	if ctx.Project == nil {
		return nil
	}
	for _, own := range []string{ctx.ConfigPath, ctx.Project.ConfigPath} {
		if own != "" && sameFile(config, own) {
			return refuseWorker(ctx, "add projects to the config issue-cli reads")
		}
	}
	return nil
}

func sameFile(a, b string) bool {
	clean := func(s string) string {
		if abs, err := filepath.Abs(s); err == nil {
			s = abs
		}
		if real, err := filepath.EvalSymlinks(s); err == nil {
			s = real
		}
		return s
	}
	return clean(a) == clean(b)
}

// projectInitNext is what to run once the project is made.
func projectInitNext(p *projectinit.Plan) []string {
	cfg := p.Config.Path
	if cfg == "" {
		cfg = "<your projects.yaml>"
	}
	cli := "issue-cli --config " + quoteArg(cfg) + " --project " + p.Slug
	var next []string
	if !p.GitRepo {
		next = append(next, "git -C "+shellQuote(p.Dir)+" init   # workers work in git worktrees")
	}
	next = append(next,
		"issue-viewer -config "+quoteArg(cfg)+"   # then open /p/"+p.Slug+"/company: the New department tile adds a department",
		cli+" lead spawn   # start the CEO",
		cli+" department list   # the department templates you can add",
		cli+" create --title \"<first issue>\"",
		cli+" workflow upgrade   # decide privacy (entry privacy) and adopt newer features",
	)
	return next
}

// projectInitApply is the command that makes what a dry run listed.
func projectInitApply(p *projectinit.Plan, name, slug, tmpl string) string {
	cmd := "issue-cli project init " + shellQuote(p.Dir)
	if name != "" {
		cmd += " --name " + shellQuote(name)
	}
	if slug != "" {
		cmd += " --slug " + shellQuote(slug)
	}
	if tmpl != projectinit.DefaultTemplate {
		cmd += " --template " + shellQuote(tmpl)
	}
	if p.Config.Path != "" {
		cmd += " --config " + shellQuote(p.Config.Path)
	}
	return cmd
}

// quoteArg is shellQuote, keeping a <placeholder> as it is.
func quoteArg(s string) string {
	if strings.HasPrefix(s, "<") && strings.HasSuffix(s, ">") {
		return s
	}
	return shellQuote(s)
}

func printProjectInit(ctx *Context, p *projectinit.Plan, dry bool, next []string, apply string) {
	w := ctx.Stdout
	if dry {
		fmt.Fprintf(w, "Dry run: project init %s (nothing changed)\n", p.Dir)
	} else {
		fmt.Fprintf(w, "✓ Project %s (slug %s) in %s\n", p.Name, p.Slug, p.Dir)
	}
	fmt.Fprintf(w, "Files (workflow template %s):\n", p.Template)
	for _, a := range p.Files {
		word := map[string]string{
			projectinit.StateCreate: "create",
			projectinit.StateWrite:  "write",
			projectinit.StateThere:  "already there",
			projectinit.StateSame:   "already there, same as the template",
			projectinit.StateKeep:   "kept your file (differs from the template, not overwritten)",
		}[a.State]
		if !dry {
			switch a.State {
			case projectinit.StateCreate:
				word = "created"
			case projectinit.StateWrite:
				word = "wrote"
			}
		}
		path := a.Path
		if a.Kind == "dir" {
			path += "/"
		}
		fmt.Fprintf(w, "  %s: %s\n", path, word)
	}
	entry := indentLines(p.Config.Entry, "  ")
	switch p.Config.State {
	case projectinit.ConfigPrint:
		fmt.Fprintln(w, "projects.yaml: not touched (no --config). Add this under projects: in yours:")
		fmt.Fprint(w, entry)
		fmt.Fprintf(w, "Or let init add it: issue-cli project init %s --config <file>\n", shellQuote(p.Dir))
	case projectinit.ConfigCreate:
		verb := "would write a new file"
		if !dry {
			verb = "wrote a new file"
		}
		fmt.Fprintf(w, "%s: %s with project %s:\n%s", p.Config.Path, verb, p.Slug, entry)
	case projectinit.ConfigAdd:
		verb := "would add"
		if !dry {
			verb = "added"
		}
		fmt.Fprintf(w, "%s: %s project %s under projects: (the rest of the file as it was):\n%s", p.Config.Path, verb, p.Slug, entry)
	case projectinit.ConfigThere:
		fmt.Fprintf(w, "%s: already has project %s for this folder (not changed)\n", p.Config.Path, p.Slug)
	}
	if dry {
		fmt.Fprintf(w, "Apply: %s\n", apply)
		return
	}
	fmt.Fprintln(w, "Next:")
	for _, n := range next {
		fmt.Fprintf(w, "  %s\n", n)
	}
}
