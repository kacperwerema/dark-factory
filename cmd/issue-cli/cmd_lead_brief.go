package main

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/spend"
	"github.com/kacperwerema/dark-factory/internal/tokens"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// leadBriefSectionsJSON is lead brief --sections --json.
type leadBriefSectionsJSON struct {
	Session  string               `json:"session"`
	Kind     string               `json:"kind"`
	Sections []spend.BriefSection `json:"sections"`
	Chars    int                  `json:"chars"`
	Tokens   int64                `json:"tokens"`
}

// runLeadBrief is lead brief --render: the first prompt lead spawn would
// send a fresh lead now, built by the same spec and printed, with nothing
// started or written. It reads the project, never the caller, so a worker's
// shell gets the lead's brief too.
func runLeadBrief(ctx *Context, args []string) error {
	fs := newFlagSet("lead brief", ctx)
	render := fs.Bool("render", false, "print the brief a fresh lead would get now")
	sections := fs.Bool("sections", false, "print each section with its characters and tokens (chars/2.7) instead of the text")
	deptFlag := fs.String("department", "", "a department's lead's brief instead of the CEO's")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	if !*render && !*sections {
		return fmt.Errorf("lead brief needs --render (the text) or --sections (each section's tokens)\n\nUsage:\n  %s\n  %s",
			tracker.InjectProject(ctx.Project, "issue-cli lead brief --render [--department <name>]"),
			tracker.InjectProject(ctx.Project, "issue-cli lead brief --render --sections [--department <name>]"))
	}
	proj := ctx.Project
	spec := agentrun.LeadSpec(proj, proj.LoadWorkflow(), projectRoot(proj), ctx.now())
	if name := strings.TrimSpace(*deptFlag); name != "" {
		dept, err := spawnDepartment(proj, name)
		if err != nil {
			return err
		}
		wf, err := proj.WorkflowFor(dept.Name)
		if err != nil {
			return fmt.Errorf("%w\n\nFix the department's workflow: file first (issue-cli health lint)", err)
		}
		spec = agentrun.DepartmentLeadSpec(proj, dept, wf, projectRoot(proj), ctx.now())
	}
	if !*sections {
		if ctx.JSONOutput {
			return writeJSON(ctx.Stdout, map[string]string{"session": spec.Session, "prompt": spec.Prompt})
		}
		fmt.Fprint(ctx.Stdout, spec.Prompt)
		if !strings.HasSuffix(spec.Prompt, "\n") {
			fmt.Fprintln(ctx.Stdout)
		}
		return nil
	}
	kind, secs := spend.SplitBriefText(spec.Prompt)
	out := leadBriefSectionsJSON{Session: spec.Session, Kind: kind, Sections: secs, Chars: len(spec.Prompt), Tokens: spend.EstimateTokens(len(spec.Prompt))}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, out)
	}
	fmt.Fprintf(ctx.Stdout, "%s brief by section, rendered now (est. chars/%.1f; names as spend --context):\n", spec.Session, spend.CharsPerToken)
	for _, s := range secs {
		fmt.Fprintf(ctx.Stdout, "  %-13s %7d chars  %7s tokens\n", s.Name, s.Chars, tokens.Format(s.Tokens))
	}
	fmt.Fprintf(ctx.Stdout, "  %-13s %7d chars  %7s tokens\n", "total", out.Chars, tokens.Format(out.Tokens))
	return nil
}
