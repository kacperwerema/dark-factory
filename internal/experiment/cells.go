package experiment

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// A prompt cell is what an agent of one type of work reads entering one
// status: the status prompt in force for that type (a type's own prompt:
// replaces the base one, prompt_append adds to it; docs/Workflow/types.md)
// plus the inject_prompt of each edge into the status. An experiment that
// edits a prompt reaches only the benchmark tasks whose type walks through
// a cell it changed (issue-cli prompt-cell).

// OwnerBase marks a cell whose text is the base workflow's (the type has
// no prompt: of its own for the status).
const OwnerBase = "base"

// Cell is one prompt cell an experiment edits. System is "" for every
// system, or the one system whose overlay the change is in.
type Cell struct {
	Type   string `json:"type"`
	Status string `json:"status"`
	System string `json:"system,omitempty"`
	// Owner is OwnerBase or the type's name: whose text the cell is.
	Owner string `json:"owner"`
}

// Label is "type · status" (and " · system"); a workflow without types
// prints the status alone.
func (c Cell) Label() string {
	parts := []string{}
	if c.Type != "" {
		parts = append(parts, c.Type)
	}
	parts = append(parts, c.Status)
	if c.System != "" {
		parts = append(parts, c.System)
	}
	return strings.Join(parts, " · ")
}

// ParseWorkflow reads a workflow file's bytes, as tracker.LoadWorkflow
// reads the file (a revision of it from git has no path to load).
func ParseWorkflow(data []byte) (*tracker.WorkflowConfig, error) {
	var cfg tracker.WorkflowConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// types are the workflow's type names, or one unnamed type without types:.
func types(wf *tracker.WorkflowConfig) []string {
	if names := wf.TypeNames(); len(names) > 0 {
		return names
	}
	return []string{""}
}

// TypePath is the statuses typ walks, in order.
func TypePath(wf *tracker.WorkflowConfig, typ string) []string {
	return wf.ForType(typ).GetStatusOrder()
}

// cellText is what an agent of wf (already scoped to a type and system)
// reads entering status: the status prompt and each edge's injected one.
func cellText(wf *tracker.WorkflowConfig, status string) string {
	var b strings.Builder
	b.WriteString(wf.StatusPrompt(status))
	var edges []string
	for _, tr := range wf.Transitions {
		if tr.To != status {
			continue
		}
		for _, a := range tr.Actions {
			if a.Type == "inject_prompt" && strings.TrimSpace(a.Prompt) != "" {
				edges = append(edges, tr.From+"→"+tr.To+": "+strings.TrimSpace(a.Prompt))
			}
		}
	}
	sort.Strings(edges)
	for _, e := range edges {
		b.WriteString("\n" + e)
	}
	return b.String()
}

// scoped is wf as an issue of typ in system sees it.
func scoped(wf *tracker.WorkflowConfig, typ, system string) *tracker.WorkflowConfig {
	return wf.ForIssue(&tracker.Issue{Type: typ, System: system})
}

// Owner is whose text typ's status prompt is: typ when its overlay sets a
// prompt: of its own for status, else OwnerBase.
func Owner(wf *tracker.WorkflowConfig, typ, status string) string {
	if t, ok := wf.Types[typ]; ok {
		for _, s := range t.Statuses {
			if s.Name == status && strings.TrimSpace(s.Prompt) != "" {
				return typ
			}
		}
	}
	return OwnerBase
}

// SharedBy are the types whose status prompt is the base one there: the
// types a base cell's edit reaches.
func SharedBy(wf *tracker.WorkflowConfig, status string) []string {
	var out []string
	for _, typ := range types(wf) {
		if slices.Contains(TypePath(wf, typ), status) && Owner(wf, typ, status) == OwnerBase {
			out = append(out, typ)
		}
	}
	return out
}

func systems(wfs ...*tracker.WorkflowConfig) []string {
	seen := map[string]bool{}
	var out []string
	for _, wf := range wfs {
		for s := range wf.Systems {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	sort.Strings(out)
	return out
}

// EditedCells are the cells whose text differs between base and ref, in
// ref's type order then path order. A cell that differs for every system
// is listed once with System ""; one that differs only under some systems'
// overlays is listed once per such system. A type or status only one side
// has counts as edited.
func EditedCells(base, ref *tracker.WorkflowConfig) []Cell {
	var out []Cell
	sys := systems(base, ref)
	typs := types(ref)
	for _, typ := range types(base) {
		if !slices.Contains(typs, typ) {
			typs = append(typs, typ)
		}
	}
	for _, typ := range typs {
		path := TypePath(ref, typ)
		for _, st := range TypePath(base, typ) {
			if !slices.Contains(path, st) {
				path = append(path, st)
			}
		}
		b, r := scoped(base, typ, ""), scoped(ref, typ, "")
		for _, st := range path {
			owner := Owner(ref, typ, st)
			if cellText(b, st) != cellText(r, st) {
				out = append(out, Cell{Type: typ, Status: st, Owner: owner})
				continue
			}
			for _, s := range sys {
				if cellText(scoped(base, typ, s), st) != cellText(scoped(ref, typ, s), st) {
					out = append(out, Cell{Type: typ, Status: st, System: s, Owner: owner})
				}
			}
		}
	}
	return out
}

// NamedCells are the cells specs name, read from wf: "<status>" is the
// base prompt, so every type that shares it; "<type>:<status>" is that
// type's cell, its own prompt or the base one.
func NamedCells(wf *tracker.WorkflowConfig, specs []string) ([]Cell, error) {
	var out []Cell
	add := func(c Cell) {
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	for _, spec := range specs {
		spec = strings.TrimSpace(spec)
		typ, status, typed := strings.Cut(spec, ":")
		if !typed {
			typ, status = "", spec
		}
		typ, status = strings.TrimSpace(typ), strings.TrimSpace(status)
		if wf.GetStatus(status) == nil {
			return nil, fmt.Errorf("--cell %q: no status %q in the workflow (statuses: %s)", spec, status, strings.Join(wf.GetStatusOrder(), ", "))
		}
		if !typed {
			for _, t := range SharedBy(wf, status) {
				add(Cell{Type: t, Status: status, Owner: OwnerBase})
			}
			continue
		}
		if _, ok := wf.Types[typ]; !ok {
			return nil, fmt.Errorf("--cell %q: no type %q (types: %s)", spec, typ, strings.Join(wf.TypeNames(), ", "))
		}
		if !slices.Contains(TypePath(wf, typ), status) {
			return nil, fmt.Errorf("--cell %q: %s's path skips %s (%s)", spec, typ, status, strings.Join(TypePath(wf, typ), " → "))
		}
		add(Cell{Type: typ, Status: status, Owner: Owner(wf, typ, status)})
	}
	return out, nil
}

// TaskRef is a benchmark task as reach reads it.
type TaskRef struct {
	Slug   string `json:"slug"`
	Type   string `json:"type,omitempty"`
	System string `json:"system,omitempty"`
	// Start is the status the task's worker starts at ("": the type's
	// first status).
	Start string `json:"start,omitempty"`
	// Unread says why the task can't be read against the workflow (it runs
	// on a department's own file, its issue is not on the board): reach
	// lists it unchecked with this reason.
	Unread string `json:"unread,omitempty"`
}

// TaskReach is whether a task walks through an edited cell.
type TaskReach struct {
	Task TaskRef `json:"task"`
	// Type is the type that governs the task (an unknown one falls back
	// to default_type).
	Type    string `json:"type,omitempty"`
	Checked bool   `json:"checked"`
	Cells   []Cell `json:"cells,omitempty"`
	// Why says why an unreached or unchecked task misses each edited cell.
	Why []string `json:"why,omitempty"`
}

// Reached reports a checked task that walks through an edited cell.
func (r TaskReach) Reached() bool { return r.Checked && len(r.Cells) > 0 }

// Reach says, for each task, which edited cells it walks through (on its
// type's path at or after its start, under its system), and for one that
// walks through none, why for each edited status.
func Reach(wf *tracker.WorkflowConfig, cells []Cell, tasks []TaskRef) []TaskReach {
	out := make([]TaskReach, 0, len(tasks))
	for _, t := range tasks {
		r := TaskReach{Task: t}
		if t.Unread != "" {
			r.Why = []string{t.Unread}
			out = append(out, r)
			continue
		}
		r.Checked = true
		r.Type, _ = wf.ResolveType(t.Type)
		path := TypePath(wf, r.Type)
		start := 0
		if t.Start != "" {
			if i := slices.Index(path, t.Start); i >= 0 {
				start = i
			}
		}
		for _, c := range cells {
			i := slices.Index(path, c.Status)
			if c.Type == r.Type && i >= start && (c.System == "" || c.System == t.System) {
				r.Cells = append(r.Cells, c)
			}
		}
		if len(r.Cells) == 0 {
			r.Why = missWhy(wf, r.Type, path, start, t.System, cells)
		}
		out = append(out, r)
	}
	return out
}

// missWhy is why a task of typ (path, starting at path[start]) walks
// through none of cells: one reason per edited status, in cell order.
func missWhy(wf *tracker.WorkflowConfig, typ string, path []string, start int, system string, cells []Cell) []string {
	name := typ
	if name == "" {
		name = "the task"
	}
	var out []string
	seen := map[string]bool{}
	for _, c := range cells {
		if seen[c.Status] {
			continue
		}
		seen[c.Status] = true
		i := slices.Index(path, c.Status)
		var why string
		switch {
		case i < 0:
			why = name + "'s path skips " + c.Status
		case i < start:
			why = "starts at " + path[start] + ", after " + c.Status
		case Owner(wf, typ, c.Status) != OwnerBase && !hasCell(cells, typ, c.Status):
			why = typ + " has its own " + c.Status + " prompt"
		case hasCell(cells, typ, c.Status):
			why = "the change to " + typ + " · " + c.Status + " is for system " + systemsOf(cells, typ, c.Status) + " only, not " + systemName(system)
		default:
			why = name + "'s " + c.Status + " prompt is unchanged"
		}
		out = append(out, why)
	}
	return out
}

func hasCell(cells []Cell, typ, status string) bool {
	return slices.ContainsFunc(cells, func(c Cell) bool { return c.Type == typ && c.Status == status })
}

func systemsOf(cells []Cell, typ, status string) string {
	var out []string
	for _, c := range cells {
		if c.Type == typ && c.Status == status && c.System != "" {
			out = append(out, c.System)
		}
	}
	return strings.Join(out, ", ")
}

func systemName(system string) string {
	if system == "" {
		return "a task with no system"
	}
	return system
}

// ReachCount is the reached tasks and the checked ones.
func ReachCount(rs []TaskReach) (reached, checked int) {
	for _, r := range rs {
		if r.Checked {
			checked++
		}
		if r.Reached() {
			reached++
		}
	}
	return reached, checked
}
