package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/connections"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var connectionsCommand = &Command{
	Name:      "connections",
	ShortHelp: "What passes between departments: requests, outputs and dependencies",
	LongHelp: `List the lines the company page draws between HQ, Product and the
departments, derived from what is on disk (nothing is declared):

  request     an issue with requested_by: <department> (issue-cli create
              fills it for a department lead or worker; --requested-by),
              or one citing a decision on another department's issue;
              "via HQ" when the CEO recorded its approval or dispatch
  output      a shelf output whose issue another department's issue
              depends on or cites, or one recorded with issue-cli output use
  dependency  an open issue whose depends_on or blocked_on names an open
              issue in another department

Each line counts its open items; closed ones from the last 30 days are
listed under them. A project without departments has none.

projects.yaml can also declare connections (connections: requests: for who
may ask whom, feeds: for whose outputs another uses). Then each request or
output line is marked against them: "not declared" for traffic off the
declared ones, and a declared one nothing flows on is listed as "declared,
no traffic". A department lead or worker asking off the declared routes is
refused by create and set-meta; --json carries route: on each edge.

Examples:
  issue-cli connections
  issue-cli connections --to product --kind request
  issue-cli connections --from marketing --json`,
	Run: runConnections,
}

func init() {
	registerCommand(connectionsCommand)
}

func runConnections(ctx *Context, args []string) error {
	fs := newFlagSet("connections", ctx)
	from := fs.String("from", "", "only lines from this box (hq, product or a department)")
	to := fs.String("to", "", "only lines to this box")
	kind := fs.String("kind", "", "request, output or dependency")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	p := ctx.Project
	f := connections.Filter{Kind: strings.ToLower(strings.TrimSpace(*kind))}
	if f.Kind != "" && !slices.Contains(connections.Kinds, f.Kind) {
		return fmt.Errorf("--kind %q: one of %s", *kind, strings.Join(connections.Kinds, ", "))
	}
	for _, b := range []struct {
		flag string
		dst  *string
		name string
	}{{*from, &f.From, "--from"}, {*to, &f.To, "--to"}} {
		if b.flag == "" {
			continue
		}
		if *b.dst = connections.ResolveBox(p, b.flag); *b.dst == "" {
			return fmt.Errorf("%s %q: no such department (one of: %s)", b.name, b.flag, strings.Join(connections.Boxes(p), ", "))
		}
	}
	issues, _ := tracker.LoadIssues(p.IssueDir)
	edges := connections.Select(connections.Load(p, issues, ctx.now()), f)
	if ctx.JSONOutput {
		if edges == nil {
			edges = []connections.Edge{}
		}
		return writeJSON(ctx.Stdout, edges)
	}
	w := ctx.Stdout
	if len(p.Departments) == 0 {
		fmt.Fprintln(w, "No departments: connections are drawn between departments (issue-cli department list).")
		return nil
	}
	if len(edges) == 0 {
		fmt.Fprintln(w, "No connections yet: no issue in one department depends on, cites or was requested by another.")
		fmt.Fprintln(w, "Lines appear with depends_on across departments (issue-cli set-meta <slug> --key depends_on --value <slug>),")
		fmt.Fprintln(w, "requested_by: (issue-cli create --requested-by <department>), or a used output (issue-cli output use <id> --by <department>).")
		return nil
	}
	fmt.Fprintf(w, "== Connections · %d open ==\n", connections.OpenTotal(edges))
	for _, e := range edges {
		fmt.Fprintf(w, "%s\n", connections.Label(p, e))
		for _, it := range e.Open {
			printConnectionItem(ctx, it, "  ")
		}
		if len(e.Closed) > 0 {
			fmt.Fprintf(w, "  closed in the last 30 days:\n")
			for _, it := range e.Closed {
				printConnectionItem(ctx, it, "    ")
			}
		}
	}
	if decls := connections.Declared(p); len(decls) > 0 {
		fmt.Fprintf(w, "Declared in projects.yaml connections: %d (requests: who may ask whom, feeds: whose outputs another uses).\n", len(decls))
	}
	fmt.Fprintf(w, "Page: %s\n", fmt.Sprintf("%s/p/%s/connections", viewerBaseURL(p), p.Slug))
	return nil
}

func printConnectionItem(ctx *Context, it connections.Item, indent string) {
	head := it.Slug
	if head == "" {
		head = it.Title
	}
	if it.Status != "" {
		head += " [" + it.Status + "]"
	}
	why := it.Why
	if it.State != "" {
		why += " · handoff " + it.Route + " · " + it.State
	}
	fmt.Fprintf(ctx.Stdout, "%s%s · %s\n", indent, head, why)
}
