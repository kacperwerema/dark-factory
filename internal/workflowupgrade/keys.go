package workflowupgrade

import (
	"fmt"
	"path"
	"reflect"
	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// NonFeatureKeys are workflow.yaml keys with no catalog entry, each with the
// reason. Everything else a project can set must be detected by an entry's
// probe or taught by an entry's snippet, so `workflow upgrade` can offer it.
var NonFeatureKeys = map[string]string{
	"template":                   "the template stamp itself",
	"features":                   "the feature record itself",
	"worktree_sparse_exclude":    "tunes worktree (default issues/); not something to adopt on its own",
	"leads.epic":                 "obsolete and ignored; lint warns when set",
	"leads.green_branch_minutes": "how long the lead tick lets a shipping branch wait unmerged before its merge: line; a tuning knob with a default (30)",
	"leads.idle_minutes":         "how long the lead tick lets a project start nothing while it has room before its idle: line; a tuning knob with a default (60)",
	"ci.branch":                  "local CI's integration branch; the default (main/master) fits every project so far",
	"ci.nice":                    "local CI's process priority; a tuning knob, not a feature",
	"health.off":                 "turns snapshots off; the opposite of adopting a feature",
	"health.timeout":             "per-family time limit; a tuning knob",
	"health.exclude":             "codebase-count exclusions; a tuning knob",
	"health.big_file_lines":      "a tuning knob for big files",
	"health.long_func_lines":     "a tuning knob for long functions",
	"health.score":               "per-component score overrides; a tuning knob",
	"decisions":                  "the decisions-in-force block's cap (brief_cap), a tuning knob with a default; the decision-ledger entry (factory/decisions-cited-in-design-…) may cover it",
}

// KeysFile is where NonFeatureKeys lives, for the fix a problem names.
const KeysFile = "internal/workflowupgrade/keys.go"

// keyBlocks are the project-wide config blocks whose sub-keys are walked.
// Lifecycle structs (statuses, transitions, overlays) are covered by the
// lifecycle and gate entries and are not walked key by key.
var keyBlocks = []string{"ci", "ci.run[]", "health", "leads", "workbench", "workbench.systems.*"}

// WorkflowKeys lists every workflow.yaml key the coverage rule asks about:
// the top-level keys of tracker.WorkflowConfig and the sub-keys of each
// keyBlocks block, as dotted paths (ci.run[].name).
func WorkflowKeys() ([]string, error) {
	keys := yamlKeys(reflect.TypeOf(tracker.WorkflowConfig{}), "")
	for _, b := range keyBlocks {
		typ, err := schemaType(b)
		if err != nil {
			return nil, err
		}
		keys = append(keys, yamlKeys(typ, b)...)
	}
	return keys, nil
}

// yamlKeys lists the yaml keys of a struct type, prefixed with at.
func yamlKeys(typ reflect.Type, at string) []string {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	var keys []string
	for i := 0; i < typ.NumField(); i++ {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			continue
		}
		if at != "" {
			name = at + "." + name
		}
		keys = append(keys, name)
	}
	return keys
}

// schemaType resolves a block path (as in keyBlocks) to its struct type.
func schemaType(p string) (reflect.Type, error) {
	typ := reflect.TypeOf(tracker.WorkflowConfig{})
	for _, seg := range strings.Split(p, ".") {
		for typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		if seg == "*" {
			typ = typ.Elem()
			continue
		}
		key, isList := strings.CutSuffix(seg, "[]")
		var next reflect.Type
		for i := 0; i < typ.NumField(); i++ {
			if name, _, _ := strings.Cut(typ.Field(i).Tag.Get("yaml"), ","); name == key {
				next = typ.Field(i).Type
			}
		}
		if next == nil {
			return nil, fmt.Errorf("block %s: no key %s", p, seg)
		}
		for next.Kind() == reflect.Pointer {
			next = next.Elem()
		}
		if isList {
			next = next.Elem()
		}
		typ = next
	}
	return typ, nil
}

// KeyCovered reports whether an entry's detect: path starts at the key, or
// an entry's snippet sets it.
func KeyCovered(entries []Entry, key string) bool {
	for _, e := range entries {
		for _, p := range e.Detect {
			if p.Path == key || strings.HasPrefix(p.Path, key+".") || strings.HasPrefix(p.Path, key+"[]") {
				return true
			}
		}
		found := false
		walkProbePath(ParseDoc([]byte(e.Snippet)), key, func(string, any) { found = true })
		if found {
			return true
		}
	}
	return false
}

// keyNeighbours are the entries that probe the key's block (the part
// before its last dot): the likely home of its detect: line. Empty for a
// top-level key.
func keyNeighbours(entries []Entry, key string) []string {
	i := strings.LastIndex(key, ".")
	if i < 0 {
		return nil
	}
	block := strings.ReplaceAll(key[:i], ".*", "")
	var ids []string
	for _, e := range entries {
		for _, p := range e.Detect {
			if strings.HasPrefix(p.Path, block+".") || strings.HasPrefix(p.Path, block+"[]") {
				ids = append(ids, e.ID)
				break
			}
		}
	}
	return ids
}

// UncoveredKeyProblem is catalog-check's problem for a workflow.yaml key no
// entry detects or teaches, with its fix: the detect: line to add, the
// entries that already probe its block, or the allowlist.
func UncoveredKeyProblem(entries []Entry, key string) string {
	where := CatalogPath + "/<id>.yaml"
	if ids := keyNeighbours(entries, key); len(ids) > 0 {
		where = fmt.Sprintf("%s/<id>.yaml (entries probing its block: %s)", CatalogPath, strings.Join(ids, ", "))
	}
	return fmt.Sprintf("workflow.yaml key %s has no catalog entry: no detect: path starts at it and no snippet sets it, so issue-cli workflow upgrade can't offer it to a project\n"+
		"Fix: add `- path: %s` under detect: of the entry that teaches it, in %s, then issue-cli workflow catalog-bump <id>\n"+
		"Or: a new entry file for it (docs/Workflow/overview.md, Feature catalog); or, for a tuning knob, a line in NonFeatureKeys in %s with the reason",
		key, key, where, KeysFile)
}

// KeyProblems is catalog-check's coverage check on a branch head: each
// workflow.yaml key with no entry, by UncoveredKeyProblem. The keys come
// from this build's tracker.WorkflowConfig, and only a key whose yaml tag
// is in the head's internal/tracker sources (sources) counts, so a build
// newer than the checkout asks nothing about keys the checkout lacks. nil
// sources checks every key.
func KeyProblems(entries []Entry, sources func() []byte) ([]string, error) {
	keys, err := WorkflowKeys()
	if err != nil {
		return nil, err
	}
	var src string
	if sources != nil {
		src = string(sources())
	}
	var out []string
	for _, k := range keys {
		if _, allowed := NonFeatureKeys[k]; allowed || KeyCovered(entries, k) {
			continue
		}
		if sources != nil && !strings.Contains(src, `yaml:"`+lastKeySegment(k)) {
			continue
		}
		out = append(out, UncoveredKeyProblem(entries, k))
	}
	sort.Strings(out)
	return out, nil
}

func lastKeySegment(key string) string {
	if i := strings.LastIndex(key, "."); i >= 0 {
		key = key[i+1:]
	}
	return strings.TrimSuffix(key, "[]")
}

// trackerSources joins the head's internal/tracker Go files (tests left
// out): where the workflow.yaml keys are declared.
func trackerSources(b Branch) func() []byte {
	if b.HeadDir == nil || b.Head == nil {
		return func() []byte { return nil }
	}
	return func() []byte {
		var all []byte
		for _, f := range b.HeadDir("internal/tracker") {
			if path.Ext(f) != ".go" || strings.HasSuffix(f, "_test.go") {
				continue
			}
			if data, ok := b.Head("internal/tracker/" + f); ok {
				all = append(all, data...)
			}
		}
		return all
	}
}

// PlannedEntry is a catalog entry whose sources: a planned file matches:
// the entry catalog-check will name once that file changes.
type PlannedEntry struct {
	ID      string        `json:"id"`
	Version string        `json:"version"`
	Matches []SourceMatch `json:"matches"`
	// Narrow is true when every match is a file.go#Symbol source: the entry
	// turns red only if that symbol's code changes.
	Narrow bool `json:"narrow,omitempty"`
}

// PlannedEntries lists, in catalog order, the entries whose sources: match
// one of files (paths from the repository root): catalog-check's match
// before anything is edited. Exempt files (tests, fixtures) and @prompt
// sources match nothing, as in catalog-check.
func PlannedEntries(entries []Entry, files []string) []PlannedEntry {
	var out []PlannedEntry
	for _, e := range entries {
		pe := PlannedEntry{ID: e.ID, Version: e.Version, Narrow: true}
		for _, f := range files {
			if Exempt(f) {
				continue
			}
			for _, s := range e.Sources {
				if s == PromptSource {
					continue
				}
				glob, _, hasSym := strings.Cut(s, "#")
				if MatchSource(glob, f) {
					pe.Matches = append(pe.Matches, SourceMatch{File: f, Source: s})
					pe.Narrow = pe.Narrow && hasSym
					break
				}
			}
		}
		if len(pe.Matches) > 0 {
			out = append(out, pe)
		}
	}
	return out
}
