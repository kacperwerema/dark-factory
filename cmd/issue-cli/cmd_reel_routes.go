package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/directpost"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

const reelRoutesUsage = "usage: issue-cli reel routes [<slug>] [--department <name>]"

// runReelRoutes is reel routes: how each channel of a department goes out,
// direct (a publish: connector) or through a social tool (Postiz), with the
// command that uses it. The department is --department, else the issue's
// (the slug, else ISSUE_VIEWER_ISSUE_SLUG), else every department with
// publish:.
func runReelRoutes(ctx *Context, args []string) error {
	fs := newFlagSet("reel routes", ctx)
	deptFlag := fs.String("department", "", "the department whose routes to show")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return &usageError{msg: reelRoutesUsage}
	}
	p := ctx.Project
	var depts []*tracker.Department
	switch slug := strings.TrimSpace(fs.Arg(0)); {
	case *deptFlag != "":
		d := p.DepartmentByKey(*deptFlag)
		if d == nil {
			return fmt.Errorf("no department %q (issue-cli departments)", *deptFlag)
		}
		depts = append(depts, d)
	default:
		if slug == "" {
			slug = strings.TrimSpace(os.Getenv("ISSUE_VIEWER_ISSUE_SLUG"))
		}
		if slug != "" {
			issues, err := tracker.LoadIssues(p.IssueDir)
			if err != nil {
				return err
			}
			is := tracker.FindIssue(issues, slug)
			if is == nil {
				return fmt.Errorf("no issue %s on the board", slug)
			}
			d := p.DepartmentByKey(p.DepartmentOf(is))
			if d == nil {
				return fmt.Errorf("issue %s belongs to no department: name one with --department", slug)
			}
			depts = append(depts, d)
			break
		}
		for i := range p.Departments {
			if len(p.Departments[i].Publish) > 0 {
				depts = append(depts, &p.Departments[i])
			}
		}
	}
	shown := 0
	for _, d := range depts {
		routes := directpost.Routes(p, d)
		if len(routes) == 0 {
			fmt.Fprintf(ctx.Stdout, "%s: no publish: routes; nothing goes out from it by itself. Add publish: <channel>: <connector> to it in projects.yaml (docs/reels.md#direct-publishing)\n", d.Name)
			continue
		}
		fmt.Fprintf(ctx.Stdout, "%s publishes:\n", d.Name)
		for _, r := range routes {
			fmt.Fprintln(ctx.Stdout, "  "+r.Says())
		}
		shown++
	}
	if len(depts) == 0 {
		fmt.Fprintln(ctx.Stdout, "no department has publish: routes: the owner posts by hand. Add publish: <channel>: <connector> to a department in projects.yaml (docs/reels.md#direct-publishing)")
	}
	return nil
}
