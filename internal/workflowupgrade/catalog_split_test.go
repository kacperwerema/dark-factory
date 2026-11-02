package workflowupgrade

import (
	"strings"
	"testing"
)

func entryIDs(entries []Entry) string {
	var ids []string
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	return strings.Join(ids, ",")
}

func entryFile(id, category string) CatalogFile {
	return CatalogFile{Name: id + ".yaml", Data: []byte("id: " + id + "\nversion: v0.58.0\ncategory: " + category + "\n")}
}

// Entries come in catalog order: by category in Categories order, an unknown
// category last, then by file name.
func TestParseCatalogOrdersByCategoryThenName(t *testing.T) {
	files := []CatalogFile{
		entryFile("z-life", "lifecycle"),
		entryFile("n-conv", "conventions"),
		entryFile("m-gates", "gates"),
		entryFile("q-odd", "no-such-category"),
		entryFile("y-life", "lifecycle"),
		entryFile("b-board", "board"),
	}
	got, err := ParseCatalog(files, true)
	if err != nil {
		t.Fatal(err)
	}
	if ids := entryIDs(got); ids != "y-life,z-life,m-gates,b-board,n-conv,q-odd" {
		t.Fatalf("order %s", ids)
	}
}

func TestParseCatalogRefusesABadFile(t *testing.T) {
	cases := map[string][]CatalogFile{
		"must be the file name":  {{Name: "x.yaml", Data: []byte("id: y\nversion: v0.58.0\ncategory: board\n")}},
		"already in the catalog": {entryFile("a", "board"), {Name: "a.yaml", Data: []byte("id: a\n")}},
		"not found":              {{Name: "k.yaml", Data: []byte("id: k\nversoin: v0.58.0\n")}},
	}
	for want, files := range cases {
		_, err := ParseCatalog(files, true)
		if err == nil || (want != "not found" && !strings.Contains(err.Error(), want)) {
			t.Errorf("%s: err = %v, want %q", want, err, want)
		}
	}
}

// catalog-check reads the entry files at the branch head.
func TestCheckBranchReadsTheEntryFiles(t *testing.T) {
	file := CatalogPath + "/split-thing.yaml"
	head := map[string]string{
		file:                  "id: split-thing\nversion: v0.53.0\ncategory: board\nsources: [\"internal/thing/**\"]\n",
		"internal/thing/x.go": "package thing\n",
	}
	b := Branch{
		Changed: []string{"internal/thing/x.go"},
		Base:    func(string) ([]byte, bool) { return nil, false },
		Head: func(p string) ([]byte, bool) {
			s, ok := head[p]
			return []byte(s), ok
		},
		HeadDir: func(dir string) []string {
			if dir == CatalogPath {
				return []string{"split-thing.yaml", "README.md"}
			}
			return nil
		},
	}
	rep, err := CheckBranch(b, "v0.53.0")
	if err != nil {
		t.Fatal(err)
	}
	if got := entryStates(rep); got != "split-thing=stale" {
		t.Fatalf("states %s", got)
	}
}
