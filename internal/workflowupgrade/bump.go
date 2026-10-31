package workflowupgrade

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Repo is the issue-viewer checkout catalog-bump reads: a file's content
// (ok false when it doesn't exist) and the file names in a folder.
type Repo struct {
	Read func(path string) (data []byte, ok bool)
	Dir  func(dir string) []string
}

// BumpChange is one file catalog-bump changes, and what it changes there.
type BumpChange struct {
	Path  string   `json:"path"`
	What  []string `json:"what"`
	Data  []byte   `json:"-"`
	Entry string   `json:"entry,omitempty"`
}

// BumpPlan is what catalog-bump writes: the version and every changed file.
type BumpPlan struct {
	Version string       `json:"version"`
	Changes []BumpChange `json:"changes"`
}

var (
	stampLine  = regexp.MustCompile(`(?m)^template:[ \t]*["']?([^"'\s@]+)@(v\d+\.\d+\.\d+)["']?[ \t]*$`)
	entryStart = regexp.MustCompile(`^\s*-\s+id:\s*["']?([^"'\s]+)["']?\s*$`)
	versionKey = regexp.MustCompile(`^(\s*version:\s*)["']?(v\d+\.\d+\.\d+)["']?(\s*(?:#.*)?)$`)
)

// PlanBump sets each entry's version: to version and carries every bundled
// template the entry applies to along: its `template: <name>@` stamp rises
// to version when lower, and its features: record line `- <id>@<old>`
// becomes `- <id>@<version>`. Files are edited as text, so comments and
// layout stay. A version older than an entry's current one is refused; the
// same version only brings the templates in line.
func PlanBump(repo Repo, ids []string, version string) (*BumpPlan, error) {
	if ParseVersion(version) == nil {
		return nil, fmt.Errorf("version %q is not vX.Y.Z", version)
	}
	mainRaw, ok := repo.Read(CatalogPath)
	if !ok {
		return nil, fmt.Errorf("%s not found: catalog-bump runs in the issue-viewer repository", CatalogPath)
	}
	split := map[string][]byte{}
	var splitFiles []CatalogFile
	for _, name := range repo.Dir(SplitCatalogPath) {
		if path.Ext(name) != ".yaml" {
			continue
		}
		if data, ok := repo.Read(SplitCatalogPath + "/" + name); ok {
			split[name] = data
			splitFiles = append(splitFiles, CatalogFile{Name: name, Data: data})
		}
	}
	entries, err := ParseCatalog(mainRaw, splitFiles, false)
	if err != nil {
		return nil, err
	}
	index := catalogIndex(entries)

	plan := &BumpPlan{Version: version}
	changes := map[string]*BumpChange{}
	edit := func(p string, data []byte) *BumpChange {
		if c, ok := changes[p]; ok {
			return c
		}
		c := &BumpChange{Path: p, Data: data}
		changes[p] = c
		return c
	}

	templates := map[string][]byte{}
	var names []string
	for _, f := range repo.Dir(TemplatesPath) {
		if path.Ext(f) != ".yaml" {
			continue
		}
		if data, ok := repo.Read(TemplatesPath + "/" + f); ok {
			name := strings.TrimSuffix(f, ".yaml")
			templates[name] = data
			names = append(names, name)
		}
	}
	sort.Strings(names)

	for _, id := range ids {
		e, ok := index[id]
		if !ok {
			return nil, fmt.Errorf("no catalog entry %q (ids are in %s and %s/)", id, CatalogPath, SplitCatalogPath)
		}
		if CompareVersions(version, e.Version) < 0 {
			return nil, fmt.Errorf("%s is at %s already; %s is older", id, e.Version, version)
		}
		if e.Version != version {
			file := SplitCatalogPath + "/" + id + ".yaml"
			data, inSplit := split[id+".yaml"]
			if !inSplit {
				file = CatalogPath
				data = mainRaw
			}
			c := edit(file, data)
			out, err := setEntryVersion(c.Data, id, version, inSplit)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", file, err)
			}
			c.Data = out
			c.What = append(c.What, fmt.Sprintf("%s version: %s → %s", id, e.Version, version))
		}
		for _, name := range names {
			if !e.appliesTo(name) {
				continue
			}
			p := TemplatesPath + "/" + name + ".yaml"
			c, seen := changes[p]
			if !seen {
				c = edit(p, templates[name])
			}
			if m := stampLine.FindSubmatchIndex(c.Data); m != nil && string(c.Data[m[2]:m[3]]) == name {
				old := string(c.Data[m[4]:m[5]])
				if CompareVersions(old, version) < 0 {
					c.Data = append(append(append([]byte{}, c.Data[:m[4]]...), version...), c.Data[m[5]:]...)
					c.What = append(c.What, fmt.Sprintf("stamp %s@%s → %s", name, old, version))
				}
			}
			rec := regexp.MustCompile(`(?m)^(\s*-\s+)` + regexp.QuoteMeta(id) + `@(v\d+\.\d+\.\d+)(\s*)$`)
			if m := rec.FindSubmatchIndex(c.Data); m != nil && string(c.Data[m[4]:m[5]]) != version {
				old := string(c.Data[m[4]:m[5]])
				c.Data = append(append(append([]byte{}, c.Data[:m[4]]...), version...), c.Data[m[5]:]...)
				c.What = append(c.What, fmt.Sprintf("record %s@%s → %s", id, old, version))
			}
		}
	}
	var paths []string
	for p, c := range changes {
		if len(c.What) > 0 {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	plan.Changes = []BumpChange{}
	for _, p := range paths {
		plan.Changes = append(plan.Changes, *changes[p])
	}
	return plan, nil
}

// setEntryVersion rewrites the version: line of entry id: the top-level one
// of a one-entry file, or the entry's own key after `- id: <id>` in the list
// (snippets may hold `- id:` lines of their own, deeper in).
func setEntryVersion(data []byte, id, version string, single bool) ([]byte, error) {
	lines := strings.SplitAfter(string(data), "\n")
	keyIndent, listIndent := 0, -1
	in := single
	for i, l := range lines {
		body := strings.TrimRight(l, "\r\n")
		indent := len(body) - len(strings.TrimLeft(body, " "))
		if !single {
			if m := entryStart.FindStringSubmatch(body); m != nil && (listIndent < 0 || indent == listIndent) {
				if in {
					break // the next entry: this one had no version line
				}
				listIndent = indent
				in = m[1] == id
				keyIndent = indent + 2
				continue
			}
		}
		if !in || indent != keyIndent {
			continue
		}
		if m := versionKey.FindStringSubmatch(body); m != nil {
			lines[i] = m[1] + version + m[3] + l[len(body):]
			return []byte(strings.Join(lines, "")), nil
		}
	}
	return nil, fmt.Errorf("no version: line for entry %s", id)
}
