package depttemplate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// AddProject adds item (a "- name: …" block for one project) to the
// projects: list of the config at configPath, as text like AddEntry: every
// other byte stays as it was. A file with no projects: key gets one at the
// end. The result must load and pass check for project slug before it
// replaces the file. The caller makes sure slug isn't in the file yet.
func AddProject(configPath, slug, item string, check func(*tracker.Project) error) error {
	path := configPath
	if real, err := filepath.EvalSymlinks(configPath); err == nil {
		path = real
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", configPath, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parsing %s: %w", configPath, err)
	}
	lines := splitLines(raw)
	if n := len(lines); n > 0 && !strings.HasSuffix(lines[n-1], "\n") {
		lines[n-1] += "\n"
	}
	var out []byte
	switch {
	case len(doc.Content) == 0:
		// Empty, or only comments: the list goes at the end.
		out = insertAfter(lines, len(lines)+1, "projects:\n"+indentBlock(item, 2))
	case doc.Content[0].Kind != yaml.MappingNode:
		return fmt.Errorf("%s is not a mapping with a projects: list; add the project by hand:\nprojects:\n%s", configPath, indentBlock(item, 2))
	default:
		root := doc.Content[0]
		key, projects := mapValue(root, "projects")
		switch {
		case projects == nil:
			out = insertAfter(lines, len(lines)+1, "projects:\n"+indentBlock(item, 2))
		case isNull(projects):
			out = insertAfter(lines, key.Line, indentBlock(item, key.Column+1))
		case projects.Kind == yaml.SequenceNode && projects.Style&yaml.FlowStyle == 0 && len(projects.Content) > 0:
			indent := dashColumn(lines, projects.Content[0])
			out = insertAfter(lines, lastLine(projects), indentBlock(item, indent))
		default:
			return fmt.Errorf("%s: projects: is not a block list; add the project by hand:\n%s", configPath, indentBlock(item, 2))
		}
	}
	return writeChecked(path, out, check, slug)
}

// SetProjectScalar sets the one-line scalar key of project slug's entry to
// value (YAML text, such as "3"), as text: an existing key's value is
// replaced on its line (a comment after it kept), a missing key goes on a
// line of its own after the entry's last line. check reads the result back
// before it replaces the file; every other byte stays.
func SetProjectScalar(configPath, slug, key, value string, check func(*tracker.Project) error) error {
	path, raw, proj, err := readProject(configPath, slug)
	if err != nil {
		return err
	}
	lines := splitLines(raw)
	if n := len(lines); n > 0 && !strings.HasSuffix(lines[n-1], "\n") {
		lines[n-1] += "\n"
	}
	k, v := mapValue(proj, key)
	var out []byte
	switch {
	case k == nil:
		first := proj.Content[0]
		out = insertAfter(lines, lastLine(proj), strings.Repeat(" ", first.Column-1)+key+": "+value+"\n")
	case v.Kind != yaml.ScalarNode || v.Line != k.Line || v.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0:
		return fmt.Errorf("%s: project %s's %s is written in a way this can't change: edit it by hand to %s", path, slug, key, value)
	default:
		line := lines[v.Line-1]
		rest := strings.TrimRight(line[v.Column-1:], "\r\n")
		tail := ""
		if i := strings.Index(rest, " #"); i >= 0 {
			tail = strings.TrimRight(rest[i:], " ")
		}
		lines[v.Line-1] = line[:v.Column-1] + value + tail + "\n"
		out = []byte(strings.Join(lines, ""))
	}
	return writeChecked(path, out, check, slug)
}
