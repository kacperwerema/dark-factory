package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/tracker"
	"github.com/michal-franc/issue-viewer/internal/workflowupgrade"
)

func writeRepoFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readRepoFile(t *testing.T, dir, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCatalogBumpCommand(t *testing.T) {
	dir := catalogRepo(t)
	tpl := workflowupgrade.TemplatesPath + "/development.yaml"
	writeRepoFile(t, dir, tpl, "# stamp\ntemplate: development@v0.53.0\n\nfeatures:\n  installed:\n    - health-tuning@v0.53.0\n\nstatuses: []\n")
	chdir(t, dir)
	proj := &tracker.Project{Name: "t", IssueDir: dir}

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runWorkflow(ctx, []string{"catalog-bump", "health-tuning", "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "would change (dry run) "+tpl)
	if !strings.Contains(readRepoFile(t, dir, tpl), "development@v0.53.0") {
		t.Fatal("--dry-run wrote")
	}

	ctx, stdout, _ = newTestContext(proj, false)
	if err := runWorkflow(ctx, []string{"catalog-bump", "health-tuning"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{"health-tuning version: v0.53.0 → v0.54.0", "stamp development@v0.53.0 → v0.54.0", "record health-tuning@v0.53.0 → v0.54.0"} {
		assertContains(t, out, want)
	}
	if got := readRepoFile(t, dir, tpl); got != "# stamp\ntemplate: development@v0.54.0\n\nfeatures:\n  installed:\n    - health-tuning@v0.54.0\n\nstatuses: []\n" {
		t.Fatalf("template:\n%s", got)
	}
	assertContains(t, readRepoFile(t, dir, workflowupgrade.CatalogPath), "    version: v0.54.0\n")

	ctx, _, _ = newTestContext(proj, false)
	if err := runWorkflow(ctx, []string{"catalog-bump"}); err == nil || !strings.Contains(err.Error(), "needs an entry id") {
		t.Fatalf("no id: %v", err)
	}
}

// TestSiblingBranchesAddingEntriesAndFlagsMerge is the night-plan case: two
// branches off one main each add a catalog entry, a bool flag and a
// CHANGELOG line, and the second merges after the first shipped. With an
// entry per file under workflow-features/, flags that need no list edit and
// the CHANGELOG's merge=union, the merge is clean and the catalog reads both
// entries. The old way, both entries appended to the list and both flags to
// boolFlags, conflicts; the test shows that too, so it proves what helps.
func TestSiblingBranchesAddingEntriesAndFlagsMerge(t *testing.T) {
	attrs, err := os.ReadFile(filepath.Join("..", "..", ".gitattributes"))
	if err != nil {
		t.Fatal(err)
	}
	newRepo := func() string {
		dir := t.TempDir()
		writeRepoFile(t, dir, ".gitattributes", string(attrs))
		writeRepoFile(t, dir, workflowupgrade.CatalogPath, "entries:\n  - id: base-board\n    version: v0.53.0\n    category: board\n")
		writeRepoFile(t, dir, workflowupgrade.SplitCatalogPath+"/README.md", "one entry per file\n")
		writeRepoFile(t, dir, workflowupgrade.ChangelogPath, "# Changelog\n\n## Unreleased\n\n- base line\n\n## v0.53.0 — 2026-10-23\n")
		writeRepoFile(t, dir, "internal/clilog/redact.go", "package clilog\n\nvar boolFlags = map[string]bool{\n\t\"all\": true,\n\t\"yes\": true,\n}\n")
		for _, c := range []string{"alpha", "beta"} {
			writeRepoFile(t, dir, "cmd/issue-cli/cmd_"+c+".go", "package main\n\nfunc run_"+c+"(fs *flagSet) {\n\tfs.String(\"to\", \"\", \"\")\n}\n")
		}
		catalogRepoGit(t, dir, "init", "-q", "-b", "main")
		catalogRepoGit(t, dir, "add", "-A")
		catalogRepoGit(t, dir, "commit", "-q", "-m", "init")
		return dir
	}
	addFlag := func(dir, cmd, flag string) {
		p := "cmd/issue-cli/cmd_" + cmd + ".go"
		writeRepoFile(t, dir, p, strings.Replace(readRepoFile(t, dir, p), "\tfs.String", "\tfs.Bool(\""+flag+"\", false, \"\")\n\tfs.String", 1))
	}
	changelog := func(dir, line string) {
		p := workflowupgrade.ChangelogPath
		writeRepoFile(t, dir, p, strings.Replace(readRepoFile(t, dir, p), "## Unreleased\n\n", "## Unreleased\n\n- "+line+"\n", 1))
	}
	entry := func(id string) string {
		return "id: " + id + "\nversion: v0.54.0\ncategory: board\n"
	}
	merge := func(dir string) error {
		catalogRepoGit(t, dir, "checkout", "-q", "main")
		catalogRepoGit(t, dir, "merge", "-q", "--no-edit", "work/a")
		cmd := exec.Command("git", "-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false", "merge", "-q", "--no-edit", "work/b")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Logf("merge work/b: %s", out)
		}
		return err
	}
	branch := func(dir, name string, change func()) {
		catalogRepoGit(t, dir, "checkout", "-q", "-b", name, "main")
		change()
		catalogRepoGit(t, dir, "add", "-A")
		catalogRepoGit(t, dir, "commit", "-q", "-m", name)
	}

	// The new way.
	dir := newRepo()
	branch(dir, "work/a", func() {
		writeRepoFile(t, dir, workflowupgrade.SplitCatalogPath+"/alpha-feature.yaml", entry("alpha-feature"))
		addFlag(dir, "alpha", "alpha-switch")
		changelog(dir, "alpha shipped")
	})
	branch(dir, "work/b", func() {
		writeRepoFile(t, dir, workflowupgrade.SplitCatalogPath+"/beta-feature.yaml", entry("beta-feature"))
		addFlag(dir, "beta", "beta-switch")
		changelog(dir, "beta shipped")
	})
	if err := merge(dir); err != nil {
		t.Fatalf("sibling branches conflict: %v", err)
	}
	repo := repoAt(dir)
	var split []workflowupgrade.CatalogFile
	for _, name := range repo.Dir(workflowupgrade.SplitCatalogPath) {
		if strings.HasSuffix(name, ".yaml") {
			data, _ := repo.Read(workflowupgrade.SplitCatalogPath + "/" + name)
			split = append(split, workflowupgrade.CatalogFile{Name: name, Data: data})
		}
	}
	mainRaw, _ := repo.Read(workflowupgrade.CatalogPath)
	entries, err := workflowupgrade.ParseCatalog(mainRaw, split, true)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	if got := strings.Join(ids, ","); got != "base-board,alpha-feature,beta-feature" {
		t.Fatalf("merged catalog %s", got)
	}
	cl := readRepoFile(t, dir, workflowupgrade.ChangelogPath)
	if !strings.Contains(cl, "- alpha shipped") || !strings.Contains(cl, "- beta shipped") {
		t.Fatalf("changelog lost a line:\n%s", cl)
	}

	// The old way: the same additions next to each other in shared lists.
	old := newRepo()
	appendTo := func(rel, anchor, add string) {
		writeRepoFile(t, old, rel, strings.Replace(readRepoFile(t, old, rel), anchor, anchor+add, 1))
	}
	branch(old, "work/a", func() {
		appendTo(workflowupgrade.CatalogPath, "category: board\n", "  - id: alpha-feature\n    version: v0.54.0\n    category: board\n")
		appendTo("internal/clilog/redact.go", "\"yes\": true,\n", "\t\"alpha-switch\": true,\n")
	})
	branch(old, "work/b", func() {
		appendTo(workflowupgrade.CatalogPath, "category: board\n", "  - id: beta-feature\n    version: v0.54.0\n    category: board\n")
		appendTo("internal/clilog/redact.go", "\"yes\": true,\n", "\t\"beta-switch\": true,\n")
	})
	if err := merge(old); err == nil {
		t.Fatal("adjacent additions to shared lists merged cleanly: the test no longer shows the problem")
	}
}
