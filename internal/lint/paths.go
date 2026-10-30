package lint

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// checkPaths checks workbench.systems paths: (the Systems page code map
// reads them): each glob must stay inside the repository and match at
// least one of its files, or the system's code silently drops off the map.
func checkPaths(env *Env) []Finding {
	if env.Workflow == nil {
		return nil
	}
	systems := env.Workflow.Workbench.Systems
	names := make([]string, 0, len(systems))
	for name, sys := range systems {
		if len(sys.Paths) > 0 {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	lines := pathLines(env.Raw)
	files := tracker.RepoFiles(env.Project.Root())
	var out []Finding
	for _, name := range names {
		for i, g := range systems[name].Paths {
			where := env.where(lines[name+"\x00"+fmt.Sprint(i)])
			key := fmt.Sprintf("workbench.systems.%s.paths %q", name, g)
			clean := strings.TrimSpace(g)
			if path.IsAbs(clean) || strings.HasPrefix(clean, "~") || containsDotDot(clean) {
				out = append(out, Finding{Where: where, Message: key + " leaves the repository; paths are relative to its root",
					Fix: "write the glob relative to the repository root, e.g. internal/lint/**"})
				continue
			}
			if !matchesAny(g, files) {
				out = append(out, Finding{Where: where, Message: key + " matches no file in the repository, so the code map leaves it out",
					Fix: "fix the glob or remove it (git ls-files lists the repository's files)"})
			}
		}
	}
	return out
}

func containsDotDot(g string) bool {
	for _, s := range strings.Split(g, "/") {
		if s == ".." {
			return true
		}
	}
	return false
}

func matchesAny(glob string, files []string) bool {
	for _, f := range files {
		if tracker.MatchGlob(glob, f) {
			return true
		}
	}
	return false
}

// pathLines maps "<system>\x00<index>" to the workflow.yaml line of each
// workbench.systems.<system>.paths entry; a file that doesn't parse gives
// none (the schema check reports it).
func pathLines(raw []byte) map[string]int {
	out := map[string]int{}
	var doc yaml.Node
	if yaml.Unmarshal(raw, &doc) != nil || len(doc.Content) == 0 {
		return out
	}
	systems := mapValue(mapValue(doc.Content[0], "workbench"), "systems")
	if systems == nil || systems.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(systems.Content); i += 2 {
		list := mapValue(systems.Content[i+1], "paths")
		if list == nil || list.Kind != yaml.SequenceNode {
			continue
		}
		for j, item := range list.Content {
			out[systems.Content[i].Value+"\x00"+fmt.Sprint(j)] = item.Line
		}
	}
	return out
}

// mapValue is the value node of key in a mapping node, or nil.
func mapValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}
