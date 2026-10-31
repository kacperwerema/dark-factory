package depttemplate

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// projects.yaml is the owner's hand-written file. AddEntry, RemoveEntry and
// SetParent change it as text: the YAML tree only finds the lines, so every byte
// outside the one departments: item stays as it was (comments, quoting,
// order). The result must load and say what was asked before it replaces
// the file (factory/department-templates-add-a-department-to-any-project#D-2).

// AddEntry adds item (a "- name: …" block from renderEntry) to the
// departments: of the project slug in the config at configPath.
func AddEntry(configPath, slug, name, item string) error {
	path, raw, proj, err := readProject(configPath, slug)
	if err != nil {
		return err
	}
	lines := splitLines(raw)
	var at, indent int
	var block string
	if _, depts := mapValue(proj, "departments"); depts != nil && depts.Kind == yaml.SequenceNode && len(depts.Content) > 0 && depts.Style&yaml.FlowStyle == 0 {
		first := depts.Content[0]
		indent = dashColumn(lines, first)
		at = lastLine(depts)
		block = indentBlock(item, indent)
	} else if depts != nil && !isNull(depts) {
		return fmt.Errorf("%s: departments: of project %s is not a block list; add the department by hand:\n%s", path, slug, indentBlock(item, 2))
	} else {
		key := proj.Content[0]
		indent = key.Column - 1
		at = lastLine(proj)
		block = strings.Repeat(" ", indent) + "departments:\n" + indentBlock(item, indent+2)
		if k, _ := mapValue(proj, "departments"); k != nil {
			// departments: with no value: put the items under that key.
			at, block = k.Line, indentBlock(item, indent+2)
		}
	}
	out := insertAfter(lines, at, block)
	return writeChecked(path, out, func(p *tracker.Project) error {
		if p.DepartmentByKey(name) == nil {
			return fmt.Errorf("department %s is not in the result", name)
		}
		return nil
	}, slug)
}

// RemoveEntry takes department name out of the departments: of project
// slug. It refuses Product's entry.
func RemoveEntry(configPath, slug, name string) error {
	path, raw, proj, err := readProject(configPath, slug)
	if err != nil {
		return err
	}
	_, depts := mapValue(proj, "departments")
	if depts == nil || depts.Kind != yaml.SequenceNode || depts.Style&yaml.FlowStyle != 0 {
		return fmt.Errorf("%s: project %s has no departments: list to remove %s from", path, slug, name)
	}
	key := tracker.DepartmentKey(name)
	idx := -1
	for i, item := range depts.Content {
		if _, n := mapValue(item, "name"); n != nil && tracker.DepartmentKey(n.Value) == key {
			idx = i
		}
	}
	if idx < 0 {
		return fmt.Errorf("%s: project %s has no department %s", path, slug, name)
	}
	lines := splitLines(raw)
	item := depts.Content[idx]
	from, to := item.Line, lastLine(item)
	if len(depts.Content) == 1 {
		// The only item: the departments: key goes with it.
		dk, _ := mapValue(proj, "departments")
		from = dk.Line
	}
	out := deleteLines(lines, from, to)
	return writeChecked(path, out, func(p *tracker.Project) error {
		if p.DepartmentByKey(name) != nil {
			return fmt.Errorf("department %s is still in the result", name)
		}
		return nil
	}, slug)
}

// SetParent adds parent: <parent> to department name's item in the
// departments: of project slug, right under its name: line. It refuses an
// item that has a parent: already.
func SetParent(configPath, slug, name, parent string) error {
	path, raw, proj, err := readProject(configPath, slug)
	if err != nil {
		return err
	}
	_, depts := mapValue(proj, "departments")
	if depts == nil || depts.Kind != yaml.SequenceNode || depts.Style&yaml.FlowStyle != 0 {
		return fmt.Errorf("%s: project %s has no departments: list", path, slug)
	}
	key := tracker.DepartmentKey(name)
	for _, item := range depts.Content {
		nk, nv := mapValue(item, "name")
		if nv == nil || tracker.DepartmentKey(nv.Value) != key {
			continue
		}
		if k, _ := mapValue(item, "parent"); k != nil {
			return fmt.Errorf("%s: department %s has a parent: already", path, name)
		}
		if item.Style&yaml.FlowStyle != 0 {
			return fmt.Errorf("%s: department %s is written in flow style; add parent: %s by hand", path, name, parent)
		}
		line := strings.Repeat(" ", nk.Column-1) + "parent: " + parent + "\n"
		out := insertAfter(splitLines(raw), lastLine(nv), line)
		return writeChecked(path, out, func(p *tracker.Project) error {
			if g := p.GroupOf(name); g == nil || tracker.DepartmentKey(g.Name) != tracker.DepartmentKey(parent) {
				return fmt.Errorf("department %s is not under %s in the result", name, parent)
			}
			return nil
		}, slug)
	}
	return fmt.Errorf("%s: project %s has no department %s", path, slug, name)
}

// readProject reads the config (through a symlink) and finds project
// slug's mapping in projects:.
func readProject(configPath, slug string) (string, []byte, *yaml.Node, error) {
	path := configPath
	if real, err := filepath.EvalSymlinks(configPath); err == nil {
		path = real
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil, nil, fmt.Errorf("reading %s: %w", configPath, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return "", nil, nil, fmt.Errorf("parsing %s: %w", configPath, err)
	}
	if len(doc.Content) == 0 {
		return "", nil, nil, fmt.Errorf("%s is empty", configPath)
	}
	_, projects := mapValue(doc.Content[0], "projects")
	if projects == nil || projects.Kind != yaml.SequenceNode {
		return "", nil, nil, fmt.Errorf("%s has no projects: list", configPath)
	}
	for _, p := range projects.Content {
		if p.Kind != yaml.MappingNode || len(p.Content) == 0 {
			continue
		}
		s := ""
		if _, n := mapValue(p, "slug"); n != nil {
			s = n.Value
		}
		if s == "" {
			if _, n := mapValue(p, "name"); n != nil {
				s = tracker.Slugify(n.Value)
			}
		}
		if s == slug {
			if p.Style&yaml.FlowStyle != 0 {
				return "", nil, nil, fmt.Errorf("%s: project %s is written in flow style; edit it by hand", configPath, slug)
			}
			return path, raw, p, nil
		}
	}
	return "", nil, nil, fmt.Errorf("%s has no project %s", configPath, slug)
}

// writeChecked writes out next to path, loads it as a projects config and
// checks project slug with check before it replaces path.
func writeChecked(path string, out []byte, check func(*tracker.Project) error, slug string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Chmod(tmpPath, info.Mode().Perm()); err != nil {
		return err
	}
	projects, err := tracker.LoadProjects(tmpPath)
	if err != nil {
		return fmt.Errorf("%s left unchanged: the edited file would not load: %w", path, err)
	}
	for i := range projects {
		if projects[i].Slug == slug {
			if err := check(&projects[i]); err != nil {
				return fmt.Errorf("%s left unchanged: %w", path, err)
			}
			return os.Rename(tmpPath, path)
		}
	}
	return fmt.Errorf("%s left unchanged: project %s is not in the result", path, slug)
}

// mapValue is the key and value nodes of key in mapping m, or nils.
func mapValue(m *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i], m.Content[i+1]
		}
	}
	return nil, nil
}

func isNull(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Tag == "!!null"
}

// lastLine is the last line (1-based) node n's text takes.
func lastLine(n *yaml.Node) int {
	last := n.Line
	if n.Kind == yaml.ScalarNode && (n.Style&(yaml.LiteralStyle|yaml.FoldedStyle)) != 0 {
		last = n.Line + strings.Count(strings.TrimRight(n.Value, "\n"), "\n") + 1
	}
	for _, c := range n.Content {
		if l := lastLine(c); l > last {
			last = l
		}
	}
	return last
}

// dashColumn is the 0-based column of the "-" that starts sequence item n.
func dashColumn(lines []string, n *yaml.Node) int {
	if n.Line-1 < len(lines) {
		line := lines[n.Line-1]
		for i := n.Column - 2; i >= 0 && i < len(line); i-- {
			if line[i] == '-' {
				return i
			}
		}
	}
	return n.Column - 3
}

// splitLines splits raw into lines, each with its newline.
func splitLines(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	lines := strings.SplitAfter(string(raw), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func indentBlock(block string, n int) string {
	pad := strings.Repeat(" ", n)
	var b strings.Builder
	for _, l := range strings.SplitAfter(strings.TrimRight(block, "\n")+"\n", "\n") {
		if l == "" {
			continue
		}
		b.WriteString(pad + l)
	}
	return b.String()
}

// insertAfter puts block after line at (1-based).
func insertAfter(lines []string, at int, block string) []byte {
	var b bytes.Buffer
	for i, l := range lines {
		b.WriteString(l)
		if i+1 == at {
			if !strings.HasSuffix(l, "\n") {
				b.WriteString("\n")
			}
			b.WriteString(block)
		}
	}
	if at > len(lines) {
		b.WriteString(block)
	}
	return b.Bytes()
}

// deleteLines drops lines from..to (1-based, inclusive).
func deleteLines(lines []string, from, to int) []byte {
	var b bytes.Buffer
	for i, l := range lines {
		if n := i + 1; n >= from && n <= to {
			continue
		}
		b.WriteString(l)
	}
	return b.Bytes()
}
