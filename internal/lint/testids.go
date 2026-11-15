package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/internal/testids"
	"github.com/kacperwerema/dark-factory/internal/tracker"
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
	dir := filepath.Join(root, "internal", "testids")
	if _, err := os.Stat(dir); err != nil {
		return nil
	}
	ids := repoTestIDs(dir)
	files, _ := filepath.Glob(filepath.Join(root, sessionsDir, "*.yaml"))
	sort.Strings(files)
	var out []Finding
	for _, f := range files {
		out = append(out, sessionTestIDs(ids, f, filepath.ToSlash(filepath.Join(sessionsDir, filepath.Base(f))))...)
	}
	return out
}

// sourceIDs keeps each internal/testids source file's parse, by (path,
// size, mtime).
var sourceIDs tracker.FileCache[[]string]

// lastSet is the set built from the last list read, reused while the list
// stays the same: /health runs the check on every load.
var lastSet struct {
	sync.Mutex
	key string
	set *testids.Set
}

// repoTestIDs is the ids the project's own internal/testids/*.go list on
// disk, so a binary built before an id was added doesn't call it unknown.
// A folder with no id in it (nothing parses) falls back to the list built
// into this binary.
func repoTestIDs(dir string) *testids.Set {
	files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	sort.Strings(files)
	var ids []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		got, err := sourceIDs.Get(f, info, func() ([]string, error) {
			src, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			return testids.ParseSource(f, src)
		})
		if err == nil {
			ids = append(ids, got...)
		}
	}
	if len(ids) == 0 {
		return testids.Builtin()
	}
	key := strings.Join(ids, "\n")
	lastSet.Lock()
	defer lastSet.Unlock()
	if lastSet.set == nil || lastSet.key != key {
		lastSet.key, lastSet.set = key, testids.NewSet(ids)
	}
	return lastSet.set
}

// sessionTargets keeps each session file's parse: /health runs this check
// on every load, and the yaml is most of its cost.
var sessionTargets tracker.FileCache[[]namedTarget]

// sessionTestIDs is the findings for one session file, named rel, against
// the listed ids.
func sessionTestIDs(ids *testids.Set, path, rel string) []Finding {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	targets, err := sessionTargets.Get(path, info, func() ([]namedTarget, error) { return readTargets(path) })
	if err != nil {
		return nil
	}
	var out []Finding
	seen := map[[2]string]bool{}
	for _, t := range targets {
		if seen[[2]string{t.step, t.id}] || ids.Known(t.id) {
			continue
		}
		seen[[2]string{t.step, t.id}] = true
		fix := "add the id to internal/testids (and the template), or point the step at a listed id"
		if s := ids.Suggest(t.id); s != "" {
			fix = fmt.Sprintf("did you mean %q? ", s) + fix
		}
		out = append(out, Finding{
			Where:   fmt.Sprintf("%s:%d", rel, t.line),
			Message: fmt.Sprintf("step %s names data-testid %q, which no template carries (not in internal/testids)", t.step, t.id),
			Fix:     fix,
		})
	}
	return out
}

// readTargets parses a session file into every data-testid its steps name.
func readTargets(path string) ([]namedTarget, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if yaml.Unmarshal(raw, &doc) != nil || len(doc.Content) == 0 {
		return nil, nil // make playtest-check reports a session that doesn't parse
	}
	steps := mapValue(doc.Content[0], "steps")
	if steps == nil || steps.Kind != yaml.SequenceNode {
		return nil, nil
	}
	var out []namedTarget
	for _, step := range steps.Content {
		id := "?"
		if n := mapValue(step, "id"); n != nil && n.Value != "" {
			id = n.Value
		}
		for _, t := range stepTargets(step) {
			t.step = id
			out = append(out, t)
		}
	}
	return out, nil
}

type namedTarget struct {
	step string
	id   string
	line int
}

// stepTargets is every data-testid a step's setup, act, checks and targets
// name.
func stepTargets(step *yaml.Node) []namedTarget {
	var out []namedTarget
	add := func(n *yaml.Node) {
		if n != nil && n.Kind == yaml.ScalarNode && strings.TrimSpace(n.Value) != "" {
			out = append(out, namedTarget{id: strings.TrimSpace(n.Value), line: n.Line})
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
				out = append(out, namedTarget{id: m[1], line: n.Line})
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
