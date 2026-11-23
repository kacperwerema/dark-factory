package main

import (
	"fmt"
	"github.com/kacperwerema/dark-factory/internal/goalwork"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/connections"
	"github.com/kacperwerema/dark-factory/internal/privacy"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

var createCommand = &Command{
	Name:      "create",
	ShortHelp: "Create a new issue",
	LongHelp: `Create a new issue file in the issues directory (or in issues/<system>/ when
--system is supplied). Only allows statuses earlier than backlog.

A --system named after a department does not put the issue in it: unless the
department lists that system, create warns and the issue is Product's; pass
--department to file it there.

--body gives the issue its first text in the same call (written above the
status template's sections); --body-file <path> (or - for stdin) reads it
raw, as append does, so backticks and $ survive the shell.

--parent <epic> writes parent: (the epic it belongs to); a slug that isn't
an epic on the board (type epic, or already another issue's parent) is
refused.

--goal <key> writes goal: (the projects.yaml goal it serves: hq/clients,
marketing/pages, or a bare id only one owner has); an unknown key is
refused with the keys the project has. The goal-without-work lint check
and the queue's out-of-work line name goals no open issue serves.

In a dark-factory checkout (the workflow catalog is there), a name from the
private-names list in the title or slug leaves the slug, pinned with slug:,
and create warns: a slug goes into the branch and every Refs: line, which
make leak-check scans. The title keeps the name.

create warns when a done issue may already hold the new one: its brief's
first lines name the same sweep source and item (an output id such as
o-20261117-f9bc, "report f9bc", or the report issue's slug, and "item N"
or "follow-up N"), or its title shares at least 5 words and half of the
shorter title's. It names the done issues and the close --fixed-by line;
it never refuses.

When workflow.yaml defines types of work, --type picks the issue's path
(default: default_type); the issue starts at that type's first status.

An issue one department files for another records who asked:
requested_by: is filled from the department lead or the worker (on another
department's issue) that runs create, or set with --requested-by <dept|hq>.
The company page draws it as a request between the two rooms. When
projects.yaml declares connections: requests:, a department lead or worker
asking off those routes is refused with the routes it has.

Example:
  issue-cli create --title "Fix heat overflow" --system Combat --status idea
  issue-cli create --title "Bigger play cards" --system UI --type tweak
  issue-cli create --title "Ten client case studies" --goal hq/clients
  issue-cli create --title "Board: card colours" --system UI --parent ui/board-redesign
  issue-cli create --title "Engine: brand fields" --system Factory --requested-by marketing
  issue-cli create --department marketing --title "Spring menu post" --body "## Brief\n\nWhat and why."`,
	Run: runCreate,
}

func init() {
	registerCommand(createCommand)
}

func runCreate(ctx *Context, args []string) error {
	fs := newFlagSet("create", ctx)
	titleFlag := fs.String("title", "", "issue title (required)")
	systemFlag := fs.String("system", "", "system / category")
	statusFlag := fs.String("status", "", "initial status")
	priorityFlag := fs.String("priority", "", "priority")
	typeFlag := fs.String("type", "", "type of work (when workflow.yaml defines types:)")
	deptFlag := fs.String("department", "", "department (projects.yaml departments:): the issue walks its workflow file")
	requestedFlag := fs.String("requested-by", "", "the department (or hq) that asked for it; filled from a department lead or worker when unset")
	bodyFlag := fs.String("body", "", "the issue's first text, above the status template")
	bodyFileFlag := fs.String("body-file", "", "read the body from a file (or - for stdin); avoids shell mangling")
	parentFlag := fs.String("parent", "", parentFlagHelp)
	goalFlag := fs.String("goal", "", "the goal it serves (a projects.yaml goal key: hq/clients); writes goal:")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	body, err := resolveBodyInput(ctx, normalizeEscapedText(*bodyFlag), flagWasSet(fs, "body"), *bodyFileFlag, flagWasSet(fs, "body-file"))
	if err != nil {
		return err
	}
	if *titleFlag == "" {
		return fmt.Errorf("--title is required\n\nExample:\n  issue-cli create --title \"Fix heat overflow\" --system Combat --status idea")
	}
	var extra [][2]string
	if flagWasSet(fs, "parent") {
		issues, err := ctx.Project.LoadIssues()
		if err != nil {
			return err
		}
		parent, err := checkParent(issues, *parentFlag, "")
		if err != nil {
			return fmt.Errorf("create: %w", err)
		}
		extra = append(extra, [2]string{"parent", parent})
	}
	if flagWasSet(fs, "goal") {
		key, err := goalwork.Check(ctx.Project, *goalFlag, ctx.now())
		if err != nil {
			return fmt.Errorf("create: %w", err)
		}
		extra = append(extra, [2]string{goalwork.Field, key})
	}
	_, err = writeNewIssue(ctx, newIssueSpec{
		Title: *titleFlag, System: *systemFlag, Status: *statusFlag, Priority: *priorityFlag,
		Type: *typeFlag, Department: *deptFlag, RequestedBy: *requestedFlag, Body: body,
		Extra: extra,
	})
	return err
}

// newIssueSpec is what create writes; handoff fills Extra and Body too.
type newIssueSpec struct {
	Title, System, Status, Priority, Type, Department, RequestedBy string
	// Extra are frontmatter fields written after requested_by:, in order.
	Extra [][2]string
	// Body goes before the status template.
	Body string
}

// newIssue is the file writeNewIssue made.
type newIssue struct {
	File, Slug, Department, RequestedBy string
}

// writeNewIssue checks spec, writes the issue file and prints create's
// lines.
func writeNewIssue(ctx *Context, spec newIssueSpec) (*newIssue, error) {
	title, system, status, priority := spec.Title, spec.System, spec.Status, spec.Priority
	deptFlag, requestedFlag, typeFlag := &spec.Department, &spec.RequestedBy, &spec.Type
	proj := ctx.Project
	dept, err := departmentFlag(proj, *deptFlag)
	if err != nil {
		return nil, err
	}
	if *deptFlag == "" {
		dept = proj.DepartmentOf(&tracker.Issue{System: system})
	}
	base, err := proj.WorkflowFor(dept)
	if err != nil {
		return nil, err
	}
	me := whoAmI(proj, nil)
	requestedBy, err := requestedByFor(proj, me, *requestedFlag, dept)
	if err != nil {
		return nil, err
	}
	route, err := checkRoute(proj, me, requestedBy, deptBox(dept))
	if err != nil {
		return nil, fmt.Errorf("create: %w", err)
	}
	typ := strings.TrimSpace(*typeFlag)
	if typ != "" {
		if !base.HasTypes() {
			return nil, fmt.Errorf("--type %q: this project defines no types (add a types: block to workflow.yaml; see docs/Workflow/types.md)", typ)
		}
		if _, ok := base.Types[typ]; !ok {
			return nil, fmt.Errorf("unknown type %q (types: %s)", typ, strings.Join(base.TypeNames(), ", "))
		}
	}
	typ, _ = base.ResolveType(typ)
	// The type's path decides which statuses exist; the backlog cut-off is
	// the base position so a type without backlog still can't start late.
	wf := base.ForType(typ)
	statusOrder := wf.GetStatusOrder()

	if status == "" {
		status = "idea"
		for _, s := range statusOrder {
			if s != "none" {
				status = s
				break
			}
		}
	}

	idx := base.GetStatusIndex(status)
	backlogIdx := base.GetStatusIndex(base.RoleStatus(tracker.StatusRoleQueue))
	if backlogIdx == -1 {
		backlogIdx = 3
	}
	// A type's first status is where its issues start, however far down the
	// base list it sits (the marketing template's channels start at brief,
	// after the brand foundation's statuses).
	first := len(statusOrder) > 0 && status == statusOrder[0] && typ != ""
	if !first && (idx == -1 || idx >= backlogIdx || wf.GetStatusIndex(status) == -1) {
		var allowed []string
		for _, s := range statusOrder {
			if s == "none" {
				continue
			}
			if base.GetStatusIndex(s) < backlogIdx {
				allowed = append(allowed, "\""+s+"\"")
			}
		}
		if len(allowed) == 0 {
			// Nothing sits before backlog: an empty "allowed:" list would
			// leave the reader guessing (retro 20261031-051750).
			return nil, fmt.Errorf("cannot create issue with status %q — the workflow needs a status before backlog to create issues in (add one, such as \"idea\", above \"backlog\" in statuses:)", status)
		}
		if typ != "" {
			return nil, fmt.Errorf("cannot create a %s issue with status %q — allowed: %s", typ, status, strings.Join(allowed, ", "))
		}
		return nil, fmt.Errorf("cannot create issue with status %q — allowed: %s", status, strings.Join(allowed, ", "))
	}

	dir := proj.IssueDir
	if system != "" {
		dir = filepath.Join(dir, system)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create system directory: %w", err)
		}
	}

	slug, private, filename := newIssueFile(proj, system, title)
	if _, err := os.Stat(filename); err == nil {
		return nil, fmt.Errorf("issue already exists: %s\nUse 'update' to modify existing issues.", filename)
	}

	var content strings.Builder
	content.WriteString("---\n")
	content.WriteString(fmt.Sprintf("title: \"%s\"\n", strings.ReplaceAll(title, "\"", "\\\"")))
	if len(private) > 0 {
		content.WriteString(fmt.Sprintf("slug: \"%s\"\n", slug))
	}
	content.WriteString(fmt.Sprintf("status: \"%s\"\n", status))
	content.WriteString(tracker.CreatedLine(ctx.now()))
	if system != "" {
		content.WriteString(fmt.Sprintf("system: \"%s\"\n", system))
	}
	if priority != "" {
		content.WriteString(fmt.Sprintf("priority: \"%s\"\n", priority))
	}
	if typ != "" {
		content.WriteString(fmt.Sprintf("type: \"%s\"\n", typ))
	}
	if *deptFlag != "" && dept != "" {
		content.WriteString(fmt.Sprintf("department: \"%s\"\n", dept))
	}
	if requestedBy != "" {
		content.WriteString(fmt.Sprintf("%s: \"%s\"\n", connections.RequestedByField, connections.BoxName(proj, requestedBy)))
	}
	for _, kv := range spec.Extra {
		content.WriteString(fmt.Sprintf("%s: \"%s\"\n", kv[0], strings.ReplaceAll(kv[1], "\"", "\\\"")))
	}
	content.WriteString("---\n")
	if spec.Body != "" {
		content.WriteString("\n" + strings.TrimRight(spec.Body, "\n") + "\n")
	}

	tmpl := wf.TemplateForStatus(status)
	if tmpl != "" {
		content.WriteString("\n")
		content.WriteString(tmpl)
		content.WriteString("\n")
	} else {
		content.WriteString("\n")
	}

	if err := tracker.CheckIssueFrontmatter(content.String()); err != nil {
		return nil, fmt.Errorf("failed to create issue: %w", err)
	}
	if err := os.WriteFile(filename, []byte(content.String()), 0644); err != nil {
		return nil, fmt.Errorf("failed to create issue: %w", err)
	}

	display := slug
	if system != "" {
		display = strings.ToLower(system) + "/" + slug
	}
	fmt.Fprintf(ctx.Stdout, "✓ Created: %s\n", filename)
	fmt.Fprintf(ctx.Stdout, "file: %s\n", filename)
	fmt.Fprintf(ctx.Stdout, "  Slug: %s\n", display)
	if len(private) > 0 {
		fmt.Fprintf(ctx.Stdout, "  ⚠ The title names a private project (line %s of the private-names list): the slug leaves it out, so the branch and every Refs: line stay clean for make leak-check. The title keeps it; the slug is pinned with slug:.\n", joinInts(private))
	}
	if base.File != "" {
		fmt.Fprintf(ctx.Stdout, "  Department: %s (workflow %s: %s)\n", base.Department, base.File, base.StatusNames())
	}
	if typ != "" {
		fmt.Fprintf(ctx.Stdout, "  Type: %s%s\n", typ, otherTypesNote(base, typ, *typeFlag == "", display, status))
		fmt.Fprintf(ctx.Stdout, "  Path: %s\n", wf.PathLine())
	}
	if name := proj.UnfiledDepartment(&tracker.Issue{System: system}); name != "" && *deptFlag == "" {
		fmt.Fprintf(ctx.Stdout, "  ⚠ System %s names a department, but this issue is Product's: %s's systems: don't list it.\n", system, name)
		fmt.Fprintf(ctx.Stdout, "  For %s's work: issue-cli set-meta %s --key department --value %s (or create with --department %s)\n", name, display, name, name)
	}
	if requestedBy != "" {
		fmt.Fprintf(ctx.Stdout, "  Requested by %s: drawn as a request %s → %s on the company page (issue-cli connections)\n",
			connections.BoxName(proj, requestedBy), connections.BoxName(proj, requestedBy), connections.BoxName(proj, deptBox(dept)))
		if len(spec.Extra) == 0 {
			fmt.Fprintf(ctx.Stdout, "  Tip: issue-cli handoff %s --title … --why … also records the route, and tells you when it's done (issue-cli handoffs).\n", deptBox(dept))
		}
		if note := routeNote(proj, route); note != "" {
			fmt.Fprintln(ctx.Stdout, note)
		}
	}
	if tmpl != "" {
		fmt.Fprintln(ctx.Stdout, "✓ Template checkboxes added to issue body")
	}
	if issues, err := proj.LoadIssues(); err == nil {
		warnDoneTwins(ctx.Stdout, display, findDoneTwins(proj, issues, display, title, spec.Body))
	}
	return &newIssue{File: filename, Slug: display, Department: dept, RequestedBy: requestedBy}, nil
}

// newIssueFile is where create writes an issue titled title in system: its
// slug (private names left out, see privateFreeSlug), the private-names
// lines that hit, and the file path.
func newIssueFile(proj *tracker.Project, system, title string) (string, []int, string) {
	dir := proj.IssueDir
	if system != "" {
		dir = filepath.Join(dir, system)
	}
	slug, private := privateFreeSlug(proj, tracker.Slugify(title), title)
	return slug, private, filepath.Join(dir, slug+".md")
}

// privateFreeSlug drops the owner's private names (privacy.PrivateNames)
// from a new issue's slug where the leak scan runs: a checkout with the
// workflow catalog, as the commit-msg hook's warning decides. A sweep or
// report named after another project would otherwise carry its name into
// the branch and every Refs: line (report 7ab1 item 8). Elsewhere a
// project names itself all the time, and the slug stays. private are the
// list's lines that hit; none means slug is unchanged.
func privateFreeSlug(proj *tracker.Project, slug, title string) (string, []int) {
	entries, err := os.ReadDir(filepath.Join(proj.Root(), filepath.FromSlash(workflowupgrade.CatalogPath)))
	if err != nil || len(entries) == 0 {
		return slug, nil
	}
	names, _ := privacy.PrivateNames()
	return privacy.CleanSlug(slug, title, names)
}

func joinInts(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ", ")
}

// otherTypesNote tells the agent which other types exist and how to switch,
// so types are discoverable from create output alone.
func otherTypesNote(wf *tracker.WorkflowConfig, typ string, defaulted bool, slug, status string) string {
	var others []string
	for _, n := range wf.TypeNames() {
		if n != typ {
			others = append(others, n)
		}
	}
	if len(others) == 0 {
		return ""
	}
	prefix := ""
	if defaulted {
		prefix = "default; "
	}
	if status != wf.FirstStatus(typ) {
		return fmt.Sprintf(" (%salso: %s; only a human can change it past %q)", prefix, strings.Join(others, ", "), wf.FirstStatus(typ))
	}
	return fmt.Sprintf(" (%salso: %s — issue-cli set-type %s <type> while at %s)", prefix, strings.Join(others, ", "), slug, status)
}

// deptBox is the connections box of a department name ("" is Product).
func deptBox(dept string) string {
	if dept == "" {
		return connections.Product
	}
	return tracker.DepartmentKey(dept)
}

// requestedByFor is the box that asks for a new issue of department dept:
// --requested-by when given (checked), else the caller's department when it
// is a department lead or a worker on another department's issue; "" when
// that is the issue's own box, or nobody (the CEO, a shell).
func requestedByFor(proj *tracker.Project, me Identity, flag, dept string) (string, error) {
	to := deptBox(dept)
	if flag = strings.TrimSpace(flag); flag != "" {
		from := connections.ResolveBox(proj, flag)
		if from == "" {
			return "", fmt.Errorf("--requested-by %q: no such department (one of: %s)", flag, strings.Join(connections.Boxes(proj), ", "))
		}
		if from == to {
			return "", nil
		}
		return from, nil
	}
	if len(proj.Departments) == 0 {
		return "", nil
	}
	from := ""
	switch {
	case me.Role == tracker.RoleDepartmentLead && me.Department != "":
		from = connections.ResolveBox(proj, me.Department)
	case me.Role == tracker.RoleWorker && me.Slug != "":
		issues, _ := tracker.LoadIssues(proj.IssueDir)
		if own := tracker.FindIssue(issues, me.Slug); own != nil {
			from = connections.BoxOf(proj, own)
		}
	}
	if from == to {
		return "", nil
	}
	return from, nil
}
