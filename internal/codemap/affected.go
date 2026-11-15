package codemap

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Footprint is what a set of changed files reaches through the map.
type Footprint struct {
	Files    []string      `json:"files"`              // the changed files as given, cleaned
	Changed  []string      `json:"changed"`            // packages with a changed non-test file
	Tests    []string      `json:"tests,omitempty"`    // changed test files and testdata: they reach nothing
	Reached  []string      `json:"reached"`            // packages importing a changed one, directly or not
	WorksIn  []string      `json:"works_in"`           // context keys the change works in
	Reaches  []string      `json:"reaches"`            // other context keys it reaches
	Binaries []string      `json:"binaries"`           // main packages changed or reached
	Watch    []string      `json:"watch,omitempty"`    // changed packages that import up a layer
	Surface  []FileContext `json:"surface,omitempty"`  // changed surface files and the contexts they import
	ByPaths  []FileContext `json:"by_paths,omitempty"` // changed non-Go files mapped by paths:
	New      []FileContext `json:"new,omitempty"`      // .go files in a folder that is no package on the map's tree
	Other    []string      `json:"other,omitempty"`    // files no context maps
	NoGo     bool          `json:"no_imports,omitempty"`
}

// FileContext is one file and the contexts it maps to.
type FileContext struct {
	File     string   `json:"file"`
	Contexts []string `json:"contexts"`
}

// Affected is the footprint of files (repo-relative): the packages they
// change, what imports those, the contexts the change works in (the changed
// packages' own, each changed surface file's imports, each mapped non-Go
// file's) and the others it reaches, the binaries to rebuild and the
// changed packages that import up a layer.
func (m *Map) Affected(files []string) Footprint {
	fp := Footprint{NoGo: !m.HasGo}
	changed := map[string]bool{}
	works := map[string]bool{}
	seen := map[string]bool{}
	for _, raw := range files {
		f := strings.TrimPrefix(strings.TrimSpace(raw), "./")
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		fp.Files = append(fp.Files, f)
		if strings.HasSuffix(f, ".go") {
			dir := path.Dir(f)
			if strings.HasSuffix(f, "_test.go") || inTestdata(f) {
				fp.Tests = append(fp.Tests, f)
				continue
			}
			if p := m.Packages[dir]; p != nil {
				changed[dir] = true
				if p.Context != "" {
					works[p.Context] = true
				}
				if m.IsSurface(dir) {
					if cs := m.FileContexts(f); len(cs) > 0 {
						fp.Surface = append(fp.Surface, FileContext{File: f, Contexts: cs})
						for _, c := range cs {
							works[c] = true
						}
					}
				}
				continue
			}
			if m.HasGo {
				cs := m.dirContexts(dir)
				fp.New = append(fp.New, FileContext{File: f, Contexts: cs})
				for _, c := range cs {
					works[c] = true
				}
				continue
			}
		}
		if cs := m.PathContexts(f); len(cs) > 0 {
			fp.ByPaths = append(fp.ByPaths, FileContext{File: f, Contexts: cs})
			for _, c := range cs {
				works[c] = true
			}
			continue
		}
		fp.Other = append(fp.Other, f)
	}
	for d := range changed {
		fp.Changed = append(fp.Changed, d)
	}
	sort.Strings(fp.Changed)
	for _, d := range m.Reaches(fp.Changed...) {
		if !changed[d] {
			fp.Reached = append(fp.Reached, d)
		}
	}
	reach := map[string]bool{}
	for _, d := range append(append([]string(nil), fp.Changed...), fp.Reached...) {
		if c := m.Packages[d].Context; c != "" && !works[c] {
			reach[c] = true
		}
		if m.Packages[d].Main {
			fp.Binaries = append(fp.Binaries, d)
		}
		if changed[d] && len(m.Packages[d].Up) > 0 {
			fp.Watch = append(fp.Watch, d)
		}
	}
	sort.Strings(fp.Binaries)
	fp.WorksIn = m.ordered(works)
	fp.Reaches = m.ordered(reach)
	return fp
}

// dirContexts maps a folder that holds no known package (a package new on
// the branch, or one deleted) by packages: patterns, then paths: globs.
func (m *Map) dirContexts(dir string) []string {
	if c := m.owner(dir); c != nil {
		return []string{c.Key}
	}
	return m.PathContexts(dir + "/x.go")
}

func inTestdata(f string) bool {
	for _, s := range strings.Split(f, "/") {
		if s == "testdata" {
			return true
		}
	}
	return false
}

// IssueFootprint is an issue's branch footprint on its project's map.
type IssueFootprint struct {
	Map *Map
	Footprint
	Main       string // main's head the map was built from
	Base, Head string // the branch diff's ends
	Warn       string // why there is no diff (no recorded base, no branch)
}

// ForIssue is the footprint of the issue's work branch (tracker.BranchChanges:
// the merge-base diff decisions_respected reads) on the project's cached
// map; nil when the workflow has no contexts.
func ForIssue(p *tracker.Project, wf *tracker.WorkflowConfig, issue *tracker.Issue) (*IssueFootprint, error) {
	m, sha, err := ForProject(p, wf)
	if err != nil || m == nil {
		return nil, err
	}
	d, err := tracker.BranchChanges(p.TelemetryRoot(), issue)
	if err != nil {
		return nil, err
	}
	warn := strings.TrimPrefix(d.Warn, "decisions_respected: ")
	warn = strings.Replace(warn, "the diff audit didn't run", "no footprint yet", 1)
	out := &IssueFootprint{Map: m, Main: sha, Base: d.Base, Head: d.Head, Warn: warn}
	if len(d.Files) > 0 {
		out.Footprint = m.Affected(d.Files)
	}
	return out, nil
}

// Lines are the footprint in three plain lines (works in, reaches, watch)
// for a brief or a transition hint: names from the map, no author text.
func (f *IssueFootprint) Lines() []string {
	if f == nil || len(f.Files) == 0 {
		return nil
	}
	names := func(keys []string) string {
		if len(keys) == 0 {
			return "none"
		}
		out := make([]string, len(keys))
		for i, k := range keys {
			out[i] = f.Map.ContextName(k)
		}
		return strings.Join(out, ", ")
	}
	lines := []string{
		fmt.Sprintf("works in: %s (%d packages changed)", names(f.WorksIn), len(f.Changed)),
		fmt.Sprintf("reaches: %s (%d more packages)", names(f.Reaches), len(f.Reached)),
	}
	if len(f.Binaries) > 0 {
		lines = append(lines, "rebuild: "+strings.Join(f.Binaries, ", "))
	}
	if len(f.Watch) > 0 {
		lines = append(lines, "watch: "+strings.Join(f.Watch, ", ")+" import up a layer")
	}
	return lines
}
