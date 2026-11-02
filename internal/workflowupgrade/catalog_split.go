package workflowupgrade

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/cmd/issue-cli/templates"
)

// The catalog is one file per entry under workflow-features/ (<id>.yaml,
// the entry as a mapping), so two branches that each add or change an entry
// touch different files and merge without a conflict.
const (
	// CatalogPath is the folder of entry files, from the repo root.
	CatalogPath = "cmd/issue-cli/templates/workflow-features"
	catalogDir  = "workflow-features"
)

// CatalogFile is one entry file of the catalog: its name (<id>.yaml) and
// content.
type CatalogFile struct {
	Name string
	Data []byte
}

// bundledCatalog returns the entry files bundled in the binary.
func bundledCatalog() []CatalogFile {
	entries, err := fs.ReadDir(templates.FS, catalogDir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return readCatalogFiles(func(p string) ([]byte, bool) {
		data, err := fs.ReadFile(templates.FS, p)
		return data, err == nil
	}, func(string) []string { return names }, catalogDir)
}

// readCatalogFiles reads the .yaml files of a catalog folder through read
// and dir (a checkout, a branch head, the bundled files).
func readCatalogFiles(read func(string) ([]byte, bool), dir func(string) []string, folder string) []CatalogFile {
	if dir == nil {
		return nil
	}
	var files []CatalogFile
	for _, name := range dir(folder) {
		if path.Ext(name) != ".yaml" {
			continue
		}
		if data, ok := read(folder + "/" + name); ok {
			files = append(files, CatalogFile{Name: name, Data: data})
		}
	}
	return files
}

// RepoCatalog parses the catalog of a checkout (or branch head).
func RepoCatalog(repo Repo) ([]Entry, error) {
	files := readCatalogFiles(repo.Read, repo.Dir, CatalogPath)
	if len(files) == 0 {
		return nil, fmt.Errorf("%s/ has no entry files: this runs in the dark-factory repository", CatalogPath)
	}
	return ParseCatalog(files, false)
}

// ParseCatalog reads the entry files in catalog order: by category, in
// Categories order (an unknown category last), then by file name. That is
// the order workflow scan records features: in. A file whose id isn't its
// name, or an id twice, is an error. strict refuses keys Entry doesn't know.
func ParseCatalog(files []CatalogFile, strict bool) ([]Entry, error) {
	files = append([]CatalogFile(nil), files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	seen := map[string]bool{}
	var entries []Entry
	for _, f := range files {
		var e Entry
		if err := decodeYAML(f.Data, &e, strict); err != nil {
			return nil, fmt.Errorf("parsing %s/%s: %w", catalogDir, f.Name, err)
		}
		if want := strings.TrimSuffix(f.Name, ".yaml"); e.ID != want {
			return nil, fmt.Errorf("%s/%s: id %q must be the file name (%s)", catalogDir, f.Name, e.ID, want)
		}
		if seen[e.ID] {
			return nil, fmt.Errorf("%s/%s: entry %s is already in the catalog", catalogDir, f.Name, e.ID)
		}
		seen[e.ID] = true
		entries = append(entries, e)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return categoryRank(entries[i].Category) < categoryRank(entries[j].Category)
	})
	return entries, nil
}

// categoryRank is a category's place in Categories; an unknown one sorts
// after all of them.
func categoryRank(c string) int {
	for i, have := range Categories {
		if have == c {
			return i
		}
	}
	return len(Categories)
}

func decodeYAML(data []byte, v any, strict bool) error {
	if !strict {
		return yaml.Unmarshal(data, v)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	return dec.Decode(v)
}
