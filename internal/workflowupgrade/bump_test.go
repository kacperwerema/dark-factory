package workflowupgrade

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// realRepo reads the checkout this test runs in, with overrides on top.
func realRepo(t *testing.T, over map[string]string) Repo {
	t.Helper()
	root := filepath.Join("..", "..")
	return Repo{
		Read: func(p string) ([]byte, bool) {
			if s, ok := over[p]; ok {
				return []byte(s), true
			}
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
			return data, err == nil
		},
		Dir: func(dir string) []string {
			seen := map[string]bool{}
			var names []string
			entries, _ := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
			for _, e := range entries {
				seen[e.Name()] = true
				names = append(names, e.Name())
			}
			for p := range over {
				if path.Dir(p) == dir && !seen[path.Base(p)] {
					names = append(names, path.Base(p))
				}
			}
			return names
		},
	}
}

func applied(repo Repo, plan *BumpPlan) Repo {
	over := map[string][]byte{}
	for _, c := range plan.Changes {
		over[c.Path] = c.Data
	}
	return Repo{Read: func(p string) ([]byte, bool) {
		if d, ok := over[p]; ok {
			return d, true
		}
		return repo.Read(p)
	}, Dir: repo.Dir}
}

// One call moves the entry's version, every applying template's stamp and
// its record line; the result passes the checks the template tests make.
func TestCatalogBumpCarriesTheTemplateStamps(t *testing.T) {
	repo := realRepo(t, nil)
	const version = "v9.1.0"
	plan, err := PlanBump(repo, []string{"status-prompts"}, version)
	if err != nil {
		t.Fatal(err)
	}
	after := applied(repo, plan)
	mainRaw, _ := after.Read(CatalogPath)
	entries, err := ParseCatalog(mainRaw, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	e := catalogIndex(entries)["status-prompts"]
	if e.Version != version {
		t.Fatalf("entry version %s", e.Version)
	}
	before, _ := repo.Read(CatalogPath)
	if strings.Count(string(before), "\n") != strings.Count(string(mainRaw), "\n") {
		t.Fatal("the catalog edit changed more than one line")
	}
	for _, name := range Templates() {
		data, _ := after.Read(TemplatesPath + "/" + name + ".yaml")
		_, stampVersion, ok := ParseStamp(ReadStamp(data))
		if !ok {
			t.Fatalf("%s: stamp %q", name, ReadStamp(data))
		}
		if e.appliesTo(name) && stampVersion != version {
			t.Errorf("%s: stamp %s, want %s", name, stampVersion, version)
		}
		rec, _ := ReadRecord(data)
		installed, _ := RecordIndex(rec)
		if v, ok := installed["status-prompts"]; ok && v != version {
			t.Errorf("%s: record status-prompts@%s, want %s", name, v, version)
		}
	}
	var what []string
	for _, c := range plan.Changes {
		what = append(what, c.Path+": "+strings.Join(c.What, "; "))
	}
	if !strings.Contains(strings.Join(what, "\n"), "record status-prompts@") {
		t.Errorf("plan names no record line:\n%s", strings.Join(what, "\n"))
	}

	again, err := PlanBump(after, []string{"status-prompts"}, version)
	if err != nil || len(again.Changes) != 0 {
		t.Fatalf("a second bump to the same version changes %v (err %v)", again.Changes, err)
	}
	if _, err := PlanBump(after, []string{"status-prompts"}, "v0.1.0"); err == nil {
		t.Fatal("an older version must be refused")
	}
	if _, err := PlanBump(after, []string{"no-such-entry"}, version); err == nil {
		t.Fatal("an unknown id must be refused")
	}
}

// An entry in its own file is bumped there, and only its top-level version:
// moves (a snippet's nested version: stays).
func TestCatalogBumpEditsASplitEntry(t *testing.T) {
	file := SplitCatalogPath + "/zz-split-bump.yaml"
	repo := realRepo(t, map[string]string{
		file: "id: zz-split-bump\nversion: v0.1.0 # first\ncategory: conventions\ntemplates: [review]\nsnippet: |\n  version: v0.1.0\n",
	})
	plan, err := PlanBump(repo, []string{"zz-split-bump"}, "v9.2.0")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := applied(repo, plan).Read(file)
	if !strings.Contains(string(got), "version: v9.2.0 # first\n") || !strings.Contains(string(got), "  version: v0.1.0\n") {
		t.Fatalf("split file:\n%s", got)
	}
	for _, c := range plan.Changes {
		if strings.HasPrefix(c.Path, TemplatesPath+"/") && c.Path != TemplatesPath+"/review.yaml" {
			t.Errorf("%s changed for an entry that applies to review only", c.Path)
		}
	}
}
