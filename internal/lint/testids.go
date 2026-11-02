package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/internal/testids"
)

// sessionsDir is where a repository keeps its playtest sessions.
const sessionsDir = "playtest/sessions"

// Keys whose value is a target id: a check or action kind whose argument is
// the target itself (`visible: x`, `visible: [x, y]`, `click: x`), and the
// fields of a mapping argument that name one (`{target: x}`, `count:
// {pattern, within}`, `beside: {a, b}`, `drag: {target, to}`).
var (
	targetKinds = map[string]bool{
		"exists": true, "absent": true, "visible": true, "hidden": true,
		"enabled": true, "disabled": true, "selected": true,
		"click": true, "hover": true, "wait_for": true, "beside": true, "below": true,
	}
	targetFields = map[string]bool{"target": true, "pattern": true, "within": true, "a": true, "b": true, "to": true}
	// opaqueKinds take free data (a request body, CLI arguments, a file's
	// text): only their data-testid="…" strings are read, not their fields.
	opaqueKinds = map[string]bool{"post": true, "http": true, "cli": true, "write": true, "issue": true, "tmux": true}
	// testidAttrRe finds a data-testid named in a string: an HTML attribute
	// an http check looks for, or a CSS attribute selector.
	testidAttrRe = regexp.MustCompile(`data-testid\s*[~^$*|]?=\s*["']?([^"'\]\s>]+)`)
)

// checkTestIDs checks the repository's playtest sessions for steps whose
// selector names a data-testid internal/testids does not list: such a step
// only fails when the session runs in Chrome. It runs on a repository that
// carries internal/testids (this engine); another project's sessions point
// at its own pages, not these templates.
func checkTestIDs(env *Env) []Finding {
	root := env.Project.Root()
	if _, err := os.Stat(filepath.Join(root, "internal", "testids")); err != nil {
		return nil
	}
	files, _ := filepath.Glob(filepath.Join(root, sessionsDir, "*.yaml"))
	sort.Strings(files)
	var out []Finding
	for _, f := range files {
		out = append(out, sessionTestIDs(f, filepath.ToSlash(filepath.Join(sessionsDir, filepath.Base(f))))...)
	}
	return out
}

// sessionTestIDs is the findings for one session file, named rel.
func sessionTestIDs(path, rel string) []Finding {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc yaml.Node
	if yaml.Unmarshal(raw, &doc) != nil || len(doc.Content) == 0 {
		return nil // make playtest-check reports a session that doesn't parse
	}
	steps := mapValue(doc.Content[0], "steps")
	if steps == nil || steps.Kind != yaml.SequenceNode {
		return nil
	}
	var out []Finding
	for _, step := range steps.Content {
		id := "?"
		if n := mapValue(step, "id"); n != nil && n.Value != "" {
			id = n.Value
		}
		seen := map[string]bool{}
		for _, t := range stepTargets(step) {
			if seen[t.id] || testids.Known(t.id) {
				continue
			}
			seen[t.id] = true
			fix := "add the id to internal/testids (and the template), or point the step at a listed id"
			if s := testids.Suggest(t.id); s != "" {
				fix = fmt.Sprintf("did you mean %q? ", s) + fix
			}
			out = append(out, Finding{
				Where:   fmt.Sprintf("%s:%d", rel, t.line),
				Message: fmt.Sprintf("step %s names data-testid %q, which no template carries (not in internal/testids)", id, t.id),
				Fix:     fix,
			})
		}
	}
	return out
}

type namedTarget struct {
	id   string
	line int
}

// stepTargets is every data-testid a step's setup, act, checks and targets
// name.
func stepTargets(step *yaml.Node) []namedTarget {
	var out []namedTarget
	add := func(n *yaml.Node) {
		if n != nil && n.Kind == yaml.ScalarNode && strings.TrimSpace(n.Value) != "" {
			out = append(out, namedTarget{strings.TrimSpace(n.Value), n.Line})
		}
	}
	var walk func(n *yaml.Node, opaque bool)
	walk = func(n *yaml.Node, opaque bool) {
		if n == nil {
			return
		}
		switch n.Kind {
		case yaml.ScalarNode:
			for _, m := range testidAttrRe.FindAllStringSubmatch(n.Value, -1) {
				out = append(out, namedTarget{m[1], n.Line})
			}
		case yaml.SequenceNode:
			for _, c := range n.Content {
				walk(c, opaque)
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, v := n.Content[i].Value, n.Content[i+1]
				if !opaque {
					switch {
					case targetFields[k]:
						add(v)
						continue
					case targetKinds[k] && v.Kind == yaml.ScalarNode:
						add(v)
						continue
					case targetKinds[k] && v.Kind == yaml.SequenceNode:
						for _, c := range v.Content {
							add(c)
						}
						continue
					}
				}
				walk(v, opaque || opaqueKinds[k])
			}
		}
	}
	for _, key := range []string{"setup", "act", "checks"} {
		walk(mapValue(step, key), false)
	}
	// targets: the elements a judgement step's overlay points at, each an
	// id or {id, note}.
	if ts := mapValue(step, "targets"); ts != nil && ts.Kind == yaml.SequenceNode {
		for _, t := range ts.Content {
			if t.Kind == yaml.MappingNode {
				t = mapValue(t, "id")
			}
			add(t)
		}
	}
	return out
}
