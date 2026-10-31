package workflowupgrade

import (
	"strings"
	"testing"
)

const splitMain = `entries:
  - id: a-life
    version: v0.50.0
    category: lifecycle
  - id: b-board
    version: v0.50.0
    category: board
  - id: c-conv
    version: v0.50.0
    category: conventions
`

func entryIDs(entries []Entry) string {
	var ids []string
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	return strings.Join(ids, ",")
}

// A one-entry file lands after the last entry of its category, or of the
// category before it when the list has none of its own; files in name order.
func TestParseCatalogPlacesSplitEntriesInTheirCategory(t *testing.T) {
	split := []CatalogFile{
		{Name: "z-life.yaml", Data: []byte("id: z-life\nversion: v0.58.0\ncategory: lifecycle\n")},
		{Name: "m-gates.yaml", Data: []byte("id: m-gates\nversion: v0.58.0\ncategory: gates\n")},
		{Name: "y-life.yaml", Data: []byte("id: y-life\nversion: v0.58.0\ncategory: lifecycle\n")},
		{Name: "n-conv.yaml", Data: []byte("id: n-conv\nversion: v0.58.0\ncategory: conventions\n")},
	}
	got, err := ParseCatalog([]byte(splitMain), split, true)
	if err != nil {
		t.Fatal(err)
	}
	if ids := entryIDs(got); ids != "a-life,y-life,z-life,m-gates,b-board,c-conv,n-conv" {
		t.Fatalf("order %s", ids)
	}
}

func TestParseCatalogRefusesABadSplitFile(t *testing.T) {
	cases := map[string]CatalogFile{
		"must be the file name":  {Name: "x.yaml", Data: []byte("id: y\nversion: v0.58.0\ncategory: board\n")},
		"already in the catalog": {Name: "a-life.yaml", Data: []byte("id: a-life\nversion: v0.58.0\ncategory: board\n")},
		"not found":              {Name: "k.yaml", Data: []byte("id: k\nversoin: v0.58.0\n")},
	}
	for want, f := range cases {
		_, err := ParseCatalog([]byte(splitMain), []CatalogFile{f}, true)
		if err == nil || (want != "not found" && !strings.Contains(err.Error(), want)) {
			t.Errorf("%s: err = %v, want %q", f.Name, err, want)
		}
	}
}

// catalog-check sees an entry that lives in its own file.
func TestCheckBranchReadsTheSplitCatalog(t *testing.T) {
	file := SplitCatalogPath + "/split-thing.yaml"
	head := map[string]string{
		CatalogPath:           "entries: []\n",
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
			if dir == SplitCatalogPath {
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
