package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/codemap"
	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

var codemapCommand = &Command{
	Name:      "codemap",
	ShortHelp: "The code map: contexts by layer, imports between them, upward imports",
	LongHelp: `Print the project's code map (workbench.layers and workbench.contexts in
workflow.yaml, docs/workbench-files.md#code-map): each layer from the top,
its contexts with their packages and lines, the imports between contexts,
the imports that point up a layer (new ones first, with the file and line
that imports, and the known ones listed under leaks:), and the packages no
context lists. Imports are read from the source with go/parser; a project
without go.mod has a map of paths: only.

--root <dir> reads another checkout (a worktree: --root .) instead of the
project's.

Examples:
  issue-cli codemap
  issue-cli codemap --root .
  issue-cli codemap --json`,
	Run: runCodemap,
}

var affectedCommand = &Command{
	Name:      "affected",
	ShortHelp: "A branch's footprint on the code map: contexts it works in and reaches",
	LongHelp: `Show what a change reaches through the code map: the packages it changes,
the packages that import them directly or not, the contexts it works in
(the changed packages' own, the contexts each changed viewer or CLI file
imports, the contexts a changed non-Go file's paths: match), the other
contexts it reaches, the binaries to rebuild and the changed packages that
import up a layer (Watch). In the dark-factory repo it ends with the
catalog impact: the catalog entries whose sources: the changed files and
the files the issue's body names match (the ones catalog-check will name),
with the catalog-bump command and the Catalog: n/a line to write, so a
design names them before the build.

With <slug> the files are the issue's work branch's changes since it left
main (the diff decisions_respected reads); a dispatched worker may leave
its own slug out (ISSUE_VIEWER_ISSUE_SLUG) when it gives no --files.
Before the branch has changes (in design), the paths the body names are
the files. --files takes a comma or space
separated list, or - for one file per line on stdin (git diff --name-only).
The map is built from the project's checkout (main); --root <dir> reads
another one (--root . in a worktree, to count imports the branch added).

Examples:
  issue-cli affected <slug>
  issue-cli affected --files internal/tracker/issue.go,docs/workflow.md
  git diff --name-only main... | issue-cli affected --files -
  issue-cli affected <slug> --json`,
	Run: runAffected,
}

func init() {
	registerCommand(codemapCommand)
	registerCommand(affectedCommand)
}

// codemapFor builds the map of root (the project's checkout when empty):
// cached per main head for the project's own, fresh for another root.
func codemapFor(ctx *Context, root string) (*codemap.Map, string, error) {
	if root == "" {
		wf := ctx.Project.LoadWorkflow()
		if wf == nil || !codemap.Configured(wf.Workbench) {
			return nil, "", errNoContexts(ctx.Project.Root())
		}
		return codemap.ForProject(ctx.Project, wf)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, "", err
	}
	wf, err := tracker.LoadWorkflow(filepath.Join(abs, "workflow.yaml"))
	if err != nil {
		return nil, "", err
	}
	if wf == nil || !codemap.Configured(wf.Workbench) {
		return nil, "", errNoContexts(abs)
	}
	m, err := codemap.Build(abs, wf.Workbench)
	return m, "", err
}

func errNoContexts(root string) error {
	return fmt.Errorf("no code map: %s has no workbench.contexts in workflow.yaml. Add layers and contexts (docs/workbench-files.md#code-map), or see issue-cli workflow upgrade for the code-map entry", filepath.Join(root, "workflow.yaml"))
}

func runCodemap(ctx *Context, args []string) error {
	fs := newFlagSet("codemap", ctx)
	root := fs.String("root", "", "read this checkout instead of the project's (a worktree: --root .)")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	m, sha, err := codemapFor(ctx, *root)
	if err != nil {
		return err
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, codemapJSON(m, sha))
	}
	printCodemap(ctx.Stdout, m, sha)
	return nil
}

type codemapOut struct {
	*codemap.Map
	Main     string          `json:"main,omitempty"`
	Between  []codemap.Count `json:"between"`
	Upward   []codemapEdge   `json:"upward"`
	Unmapped []string        `json:"unmapped"`
}

type codemapEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	File  string `json:"file"`
	Line  int    `json:"line"`
	Known bool   `json:"known"`
}

func codemapJSON(m *codemap.Map, sha string) codemapOut {
	out := codemapOut{Map: m, Main: sha, Between: m.Between(), Upward: []codemapEdge{}, Unmapped: m.Unmapped()}
	if out.Between == nil {
		out.Between = []codemap.Count{}
	}
	if out.Unmapped == nil {
		out.Unmapped = []string{}
	}
	for _, e := range m.Upward() {
		out.Upward = append(out.Upward, codemapEdge{From: e.From, To: e.To, File: e.File, Line: e.Line, Known: e.Known})
	}
	return out
}

func printCodemap(w io.Writer, m *codemap.Map, sha string) {
	where := ""
	if sha != "" {
		where = " (main at " + gitdir.ShortSHA(sha) + ")"
	}
	fmt.Fprintf(w, "== Code map%s: %d packages, %d contexts, %d layers ==\n", where, len(m.Packages), len(m.Contexts), len(m.Layers))
	if !m.HasGo {
		fmt.Fprintln(w, "No go.mod: the map has no import graph; affected maps files by paths: only.")
	}
	for i := len(m.Layers) - 1; i >= 0; i-- {
		l := m.Layers[i]
		shared := ""
		if l.Shared {
			shared = " (shared)"
		}
		fmt.Fprintf(w, "\n%s%s\n", l.Name, shared)
		for _, c := range m.Contexts {
			if c.Level != l.Level {
				continue
			}
			fmt.Fprintf(w, "  %s [%s] · %d packages · %d lines\n", c.Name, c.Key, len(c.Packages), c.Lines)
			if c.Summary != "" {
				fmt.Fprintf(w, "    %s\n", c.Summary)
			}
			if len(c.Packages) > 0 {
				fmt.Fprintf(w, "    %s\n", strings.Join(c.Packages, ", "))
			}
		}
	}
	var stray []string
	for _, c := range m.Contexts {
		if c.Level < 0 {
			stray = append(stray, c.Name+" (layer "+c.Layer+")")
		}
	}
	if len(stray) > 0 {
		fmt.Fprintf(w, "\nOn no layer: %s\n", strings.Join(stray, ", "))
	}
	if b := m.Between(); len(b) > 0 {
		fmt.Fprintln(w, "\nImports between contexts (package imports, from → to):")
		for _, c := range b {
			up := ""
			if c.Up {
				up = "  ↑ up a layer"
			}
			fmt.Fprintf(w, "  %s → %s: %d%s\n", c.From, c.To, c.N, up)
		}
	}
	up := m.Upward()
	fmt.Fprintf(w, "\nUpward imports: %d\n", len(up))
	for _, e := range up {
		state := "NEW"
		if e.Known {
			state = "known leak"
		}
		fmt.Fprintf(w, "  %s → %s (%s → %s) · %s · %s:%d\n", e.From, e.To, e.FromName, e.ToName, state, e.File, e.Line)
	}
	un := m.Unmapped()
	fmt.Fprintf(w, "\nUnmapped packages: %d\n", len(un))
	for _, d := range un {
		fmt.Fprintf(w, "  %s\n", d)
	}
	for _, l := range m.StaleLeaks() {
		fmt.Fprintf(w, "Stale leak: %s -> %s no longer happens\n", l.From, l.To)
	}
}

type affectedOut struct {
	Slug string `json:"slug,omitempty"`
	Main string `json:"main,omitempty"`
	Base string `json:"base,omitempty"`
	Head string `json:"head,omitempty"`
	codemap.Footprint
	Names map[string]string `json:"names"`
	// Planned are the paths the issue's body names (plannedFiles); Catalog
	// the entries the changed and planned files turn red in catalog-check.
	// Both only in the dark-factory repo, where the catalog is.
	Planned []string                       `json:"planned,omitempty"`
	Catalog []workflowupgrade.PlannedEntry `json:"catalog,omitempty"`
	catalog bool
}

const affectedExamples = "\n\nExamples:\n  issue-cli affected <slug>\n  issue-cli affected --files internal/tracker/issue.go"

func runAffected(ctx *Context, args []string) error {
	slug := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		slug, args = args[0], args[1:]
	}
	fs := newFlagSet("affected", ctx)
	filesFlag := fs.String("files", "", "changed files, comma or space separated, or - for stdin (one per line)")
	root := fs.String("root", "", "read this checkout's map instead of the project's (a worktree: --root .)")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if rest := fs.Args(); len(rest) > 0 {
		if slug != "" || len(rest) > 1 {
			return unexpectedArgError(fs, rest[len(rest)-1])
		}
		slug = rest[0]
	}
	if slug == "" && *filesFlag == "" {
		// A dispatched worker leaves its own slug out, as with show or check.
		if own := ownSlugEnv(); own != "" {
			noteOwnSlug(ctx, own)
			slug = own
		}
	}
	if slug != "" && *filesFlag != "" {
		return errors.New("affected needs <slug> or --files <list>, not both" + affectedExamples)
	}
	if slug == "" && *filesFlag == "" {
		return errors.New("affected needs <slug> or --files <list>: none given" + affectedExamples)
	}
	out := affectedOut{Slug: slug}
	catRoot := ctx.Project.Root()
	if *root != "" {
		catRoot = *root
	}
	var files []string
	if slug != "" {
		issue, _, err := findIssueOrErr(ctx, slug)
		if err != nil {
			return err
		}
		out.Slug = issue.Slug
		out.Planned = plannedFiles(catRoot, issue.BodyRaw)
		d, err := tracker.BranchChanges(ctx.Project.TelemetryRoot(), issue)
		if err != nil {
			return err
		}
		if d.Warn != "" && len(d.Files) == 0 && len(out.Planned) == 0 {
			return errors.New(strings.Replace(d.Warn, "decisions_respected: ", "affected: ", 1) + "; pass --files instead")
		}
		files, out.Base, out.Head = d.Files, d.Base, d.Head
		if len(files) == 0 {
			// In design nothing is changed yet: the planned files are the footprint.
			files = out.Planned
		}
	} else {
		files = splitFiles(*filesFlag, ctx.Stdin)
	}
	m, sha, err := codemapFor(ctx, *root)
	if err != nil {
		return err
	}
	out.Main = sha
	out.Footprint = m.Affected(files)
	out.Catalog, out.catalog = affectedCatalog(catRoot, unionFiles(files, out.Planned))
	out.Names = map[string]string{}
	for _, c := range m.Contexts {
		out.Names[c.Key] = c.Name
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, out)
	}
	printAffected(ctx.Stdout, m, out)
	if out.catalog {
		printAffectedCatalog(ctx.Stdout, out.Catalog, out.Planned)
	}
	return nil
}

// unionFiles is a, then the paths of b that a lacks.
func unionFiles(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range append(append([]string(nil), a...), b...) {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

func splitFiles(s string, stdin io.Reader) []string {
	if s == "-" {
		if stdin == nil {
			stdin = os.Stdin
		}
		var out []string
		sc := bufio.NewScanner(stdin)
		for sc.Scan() {
			if f := strings.TrimSpace(sc.Text()); f != "" {
				out = append(out, f)
			}
		}
		return out
	}
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' })
}

func ctxNames(m *codemap.Map, keys []string) string {
	if len(keys) == 0 {
		return "none"
	}
	names := make([]string, len(keys))
	for i, k := range keys {
		names[i] = m.ContextName(k)
	}
	return strings.Join(names, ", ")
}

func printAffected(w io.Writer, m *codemap.Map, out affectedOut) {
	fp := out.Footprint
	head := "== Footprint"
	if out.Slug != "" {
		head += " of " + out.Slug
		if out.Base != "" {
			head += fmt.Sprintf(" (%s...%s)", gitdir.ShortSHA(out.Base), gitdir.ShortSHA(out.Head))
		}
	}
	fmt.Fprintf(w, "%s: %d files ==\n", head, len(fp.Files))
	fmt.Fprintf(w, "Changed: %d packages, %d more reached\n", len(fp.Changed), len(fp.Reached))
	fmt.Fprintf(w, "Works in: %s\n", ctxNames(m, fp.WorksIn))
	fmt.Fprintf(w, "Reaches: %s\n", ctxNames(m, fp.Reaches))
	bins := fp.Binaries
	if len(bins) == 0 {
		bins = []string{"none"}
	}
	fmt.Fprintf(w, "Rebuild: %s\n", strings.Join(bins, ", "))
	if len(fp.Watch) > 0 {
		fmt.Fprintf(w, "Watch: %s import up a layer (issue-cli codemap lists the edges)\n", strings.Join(fp.Watch, ", "))
	}
	if len(fp.Changed) > 0 {
		fmt.Fprintf(w, "\nChanged packages: %s\n", strings.Join(fp.Changed, ", "))
	}
	if len(fp.Reached) > 0 {
		fmt.Fprintln(w, "Reached, by context:")
		by := map[string][]string{}
		for _, d := range fp.Reached {
			by[m.Packages[d].Context] = append(by[m.Packages[d].Context], d)
		}
		for _, c := range m.Contexts {
			if ds := by[c.Key]; len(ds) > 0 {
				fmt.Fprintf(w, "  %s: %s\n", c.Name, strings.Join(ds, ", "))
				delete(by, c.Key)
			}
		}
		if ds := by[""]; len(ds) > 0 {
			fmt.Fprintf(w, "  unmapped: %s\n", strings.Join(ds, ", "))
		}
	}
	printFileContexts(w, m, "Surface files and the contexts they import:", fp.Surface)
	printFileContexts(w, m, "Mapped by paths:", fp.ByPaths)
	printFileContexts(w, m, "New packages (not on the map's tree):", fp.New)
	if len(fp.Tests) > 0 {
		fmt.Fprintf(w, "Tests (reach nothing): %d\n", len(fp.Tests))
	}
	if len(fp.Other) > 0 {
		other := append([]string(nil), fp.Other...)
		sort.Strings(other)
		fmt.Fprintf(w, "Not mapped: %s\n", strings.Join(other, ", "))
	}
	switch {
	case fp.NoGo:
		fmt.Fprintln(w, "No go.mod: no import graph, files mapped by paths: only.")
	case out.Main != "":
		fmt.Fprintf(w, "Map from main at %s: imports the branch adds count once it merges (--root . reads a worktree).\n", gitdir.ShortSHA(out.Main))
	}
}

func printFileContexts(w io.Writer, m *codemap.Map, title string, rows []codemap.FileContext) {
	if len(rows) == 0 {
		return
	}
	fmt.Fprintln(w, title)
	for _, r := range rows {
		fmt.Fprintf(w, "  %s → %s\n", r.File, ctxNames(m, r.Contexts))
	}
}

// printTransitionFootprint is one line after a transition into a verify or
// owner-test status when the project has a code map: what the branch works
// in and reaches, and the command that shows the rest. Nothing otherwise.
func printTransitionFootprint(w io.Writer, ctx *Context, wf *tracker.WorkflowConfig, issue *tracker.Issue) {
	if wf == nil || !codemap.Configured(wf.Workbench) {
		return
	}
	if !wf.HasRole(issue.Status, tracker.StatusRoleVerify) && !wf.HasRole(issue.Status, tracker.StatusRoleOwnerTest) {
		return
	}
	f, err := codemap.ForIssue(ctx.Project, wf, issue)
	if err != nil || f == nil || len(f.Files) == 0 {
		return
	}
	fmt.Fprintf(w, "Footprint: works in %s; reaches %d more packages in %s%s — %s\n", ctxNames(f.Map, f.WorksIn), len(f.Reached), ctxNames(f.Map, f.Reaches), watchNote(f.Watch),
		tracker.InjectProject(ctx.Project, "issue-cli affected "+issue.Slug))
}

func watchNote(watch []string) string {
	if len(watch) == 0 {
		return ""
	}
	return "; watch " + strings.Join(watch, ", ") + " (imports up a layer)"
}
