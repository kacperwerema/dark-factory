package lint

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/internal/codemap"
)

// checkContexts checks the code map (workbench.layers and contexts,
// docs/workbench-files.md#code-map) against the repository's packages: a
// package no context lists, an import pointing up a layer that leaks:
// doesn't list, a leak that no longer happens, a context name or key used
// twice, a package two contexts claim, a layer: naming no layer and a
// packages: entry matching no package. Warnings only: nothing refuses an
// upward import.
func checkContexts(env *Env) []Finding {
	if env.Workflow == nil || !codemap.Configured(env.Workflow.Workbench) {
		return nil
	}
	wb := env.Workflow.Workbench
	lines := contextLines(env.Raw)
	at := func(i int) string { return env.where(lines[i]) }
	var out []Finding
	layers := map[string]bool{}
	for _, l := range wb.Layers {
		if layers[l.Name] {
			out = append(out, Finding{Where: env.where(0), Message: fmt.Sprintf("workbench.layers names %q twice", l.Name), Fix: "give each layer one entry"})
		}
		layers[l.Name] = true
	}
	names, keys := map[string]bool{}, map[string]bool{}
	for i, c := range wb.Contexts {
		key := c.ContextKey()
		if names[c.Name] {
			out = append(out, Finding{Where: at(i), Message: fmt.Sprintf("workbench.contexts name %q is used twice", c.Name), Fix: "merge the two entries or rename one"})
		} else if keys[key] {
			out = append(out, Finding{Where: at(i), Message: fmt.Sprintf("workbench.contexts key %q is used twice (context %q)", key, c.Name), Fix: "set a key: of its own"})
		}
		names[c.Name], keys[key] = true, true
		if !layers[c.Layer] {
			out = append(out, Finding{Where: at(i), Message: fmt.Sprintf("context %q has layer %q, which workbench.layers doesn't name", c.Name, c.Layer), Fix: "set layer: to one of workbench.layers"})
		}
		for _, l := range c.Leaks {
			if _, _, ok := codemap.ParseLeak(l); !ok {
				out = append(out, Finding{Where: at(i), Message: fmt.Sprintf("context %q leaks: entry %q isn't \"<from dir> -> <to dir>\"", c.Name, l), Fix: "write it as internal/a -> internal/b"})
			}
		}
	}
	m, err := codemap.Build(env.Project.Root(), wb)
	if err != nil {
		return append(out, Finding{Where: env.where(0), Message: "code map: can't read the repository: " + err.Error()})
	}
	if !m.HasGo {
		return out
	}
	for i, c := range wb.Contexts {
		for _, pat := range c.Packages {
			hit := false
			for _, d := range m.PackageDirs() {
				if codemap.Match(pat, d) > 0 {
					hit = true
					break
				}
			}
			if !hit {
				out = append(out, Finding{Where: at(i), Message: fmt.Sprintf("context %q packages: entry %q matches no Go package", c.Name, pat), Fix: "fix the directory or remove it (dir/... takes the packages under it)"})
			}
		}
	}
	for _, d := range m.PackageDirs() {
		if cl := m.Claims(d); len(cl) > 1 {
			out = append(out, Finding{Where: env.where(0), Message: fmt.Sprintf("package %s is in %d contexts (%s); the first owns it", d, len(cl), strings.Join(cl, ", ")), Fix: "list it in one context only"})
		}
	}
	for _, d := range m.Unmapped() {
		out = append(out, Finding{Where: env.where(0), Message: fmt.Sprintf("package %s is in no context, so the code map shows it as unmapped", d),
			Fix: "add it to a context's packages: in workbench.contexts (or its folder to workbench.codemap_exclude when it is a fixture)"})
	}
	for _, e := range m.Upward() {
		if e.Known {
			continue
		}
		fl, tl := "", ""
		if c := m.Context(e.FromCtx); c != nil {
			fl = c.Layer
		}
		if c := m.Context(e.ToCtx); c != nil {
			tl = c.Layer
		}
		out = append(out, Finding{Where: fmt.Sprintf("%s:%d", e.File, e.Line),
			Message: fmt.Sprintf("%s (%s, layer %s) imports %s (%s, layer %s): an import up a layer", e.From, e.FromName, fl, e.To, e.ToName, tl),
			Fix:     fmt.Sprintf("move the code so the import points down, or list it under context %q leaks: as \"%s -> %s\"", e.FromName, e.From, e.To)})
	}
	for _, l := range m.StaleLeaks() {
		out = append(out, Finding{Where: env.where(0), Message: fmt.Sprintf("leaks: entry %q -> %q no longer happens", l.From, l.To), Fix: "remove it from leaks:"})
	}
	return out
}

// contextLines maps each workbench.contexts item's index to its line.
func contextLines(raw []byte) map[int]int {
	out := map[int]int{}
	var doc yaml.Node
	if yaml.Unmarshal(raw, &doc) != nil || len(doc.Content) == 0 {
		return out
	}
	list := mapValue(mapValue(doc.Content[0], "workbench"), "contexts")
	if list == nil || list.Kind != yaml.SequenceNode {
		return out
	}
	for i, item := range list.Content {
		out[i] = item.Line
	}
	return out
}
