package depttemplate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/michal-franc/issue-viewer/internal/tracker"
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
