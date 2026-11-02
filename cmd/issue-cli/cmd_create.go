package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/connections"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var createCommand = &Command{
	Name:      "create",
	ShortHelp: "Create a new issue",
	LongHelp: `Create a new issue file in the issues directory (or in issues/<system>/ when
--system is supplied). Only allows statuses earlier than backlog.

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
  issue-cli create --title "Engine: brand fields" --system Factory --requested-by marketing`,
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
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	if *titleFlag == "" {
		return fmt.Errorf("--title is required\n\nExample:\n  issue-cli create --title \"Fix heat overflow\" --system Combat --status idea")
	}
	_, err := writeNewIssue(ctx, newIssueSpec{
		Title: *titleFlag, System: *systemFlag, Status: *statusFlag, Priority: *priorityFlag,
		Type: *typeFlag, Department: *deptFlag, RequestedBy: *requestedFlag,
	})
	if err == nil {
		fmt.Fprintln(ctx.Stdout, "\nThank you!")
	}
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
// lines (no closing "Thank you!").
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
	backlogIdx := base.GetStatusIndex("backlog")
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

	slug := tracker.Slugify(title)
	filename := filepath.Join(dir, slug+".md")
	if _, err := os.Stat(filename); err == nil {
		return nil, fmt.Errorf("issue already exists: %s\nUse 'update' to modify existing issues.", filename)
	}

	var content strings.Builder
	content.WriteString("---\n")
	content.WriteString(fmt.Sprintf("title: \"%s\"\n", strings.ReplaceAll(title, "\"", "\\\"")))
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
	if base.File != "" {
		fmt.Fprintf(ctx.Stdout, "  Department: %s (workflow %s: %s)\n", base.Department, base.File, base.StatusNames())
	}
	if typ != "" {
		fmt.Fprintf(ctx.Stdout, "  Type: %s%s\n", typ, otherTypesNote(base, typ, *typeFlag == "", display, status))
		fmt.Fprintf(ctx.Stdout, "  Path: %s\n", wf.PathLine())
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
	return &newIssue{File: filename, Slug: display, Department: dept, RequestedBy: requestedBy}, nil
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
