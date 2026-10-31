package workflowupgrade

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/michal-franc/issue-viewer/cmd/issue-cli/templates"
)

// The catalog has two forms, read together: the entries: list in
// workflow-features.yaml, and one file per entry under workflow-features/
// (<id>.yaml, the entry as a mapping). A new entry goes in a file of its
// own, so two branches that each add one touch different files and merge
// without a conflict; the entries already in the list stay there.
const (
	// SplitCatalogPath is the folder of one-entry files, from the repo root.
	SplitCatalogPath = "cmd/issue-cli/templates/workflow-features"
	splitCatalogDir  = "workflow-features"
)

// CatalogFile is one file of the split catalog: its name (<id>.yaml) and
// content.
type CatalogFile struct {
	Name string
	Data []byte
}

// bundledSplitCatalog returns the split catalog files bundled in the binary,
// in name order.
func bundledSplitCatalog() []CatalogFile {
	entries, err := fs.ReadDir(templates.FS, splitCatalogDir)
	if err != nil {
		return nil
	}
	var files []CatalogFile
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".yaml" {
			continue
		}
		data, err := fs.ReadFile(templates.FS, path.Join(splitCatalogDir, e.Name()))
		if err != nil {
			continue
		}
		files = append(files, CatalogFile{Name: e.Name(), Data: data})
	}
	return files
}

// ParseCatalog reads both forms of the catalog: the entries: list of
// workflow-features.yaml and the one-entry files. Each file's entry is placed
// after the last entry of its category (or of an earlier one in
// Categories), files in name order, so each category's entries stay
// together. A file whose id isn't its name, or an id twice, is an error.
// strict refuses keys Entry doesn't know.
func ParseCatalog(main []byte, split []CatalogFile, strict bool) ([]Entry, error) {
	var doc struct {
		Entries []Entry `yaml:"entries"`
	}
	if err := decodeYAML(main, &doc, strict); err != nil {
		return nil, fmt.Errorf("parsing workflow-features.yaml: %w", err)
	}
	entries := doc.Entries
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.ID] = true
	}
	files := append([]CatalogFile(nil), split...)
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	rank := map[string]int{}
	for i, c := range Categories {
		rank[c] = i
	}
	for _, f := range files {
		var e Entry
		if err := decodeYAML(f.Data, &e, strict); err != nil {
			return nil, fmt.Errorf("parsing %s/%s: %w", splitCatalogDir, f.Name, err)
		}
		if want := strings.TrimSuffix(f.Name, ".yaml"); e.ID != want {
			return nil, fmt.Errorf("%s/%s: id %q must be the file name (%s)", splitCatalogDir, f.Name, e.ID, want)
		}
		if seen[e.ID] {
			return nil, fmt.Errorf("%s/%s: entry %s is already in the catalog", splitCatalogDir, f.Name, e.ID)
		}
		seen[e.ID] = true
		at := 0
		for i, have := range entries {
			if r, ok := rank[have.Category]; !ok || r <= rank[e.Category] {
				at = i + 1
			}
		}
		entries = append(entries[:at], append([]Entry{e}, entries[at:]...)...)
	}
	return entries, nil
}

func decodeYAML(data []byte, v any, strict bool) error {
	if !strict {
		return yaml.Unmarshal(data, v)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	return dec.Decode(v)
}
