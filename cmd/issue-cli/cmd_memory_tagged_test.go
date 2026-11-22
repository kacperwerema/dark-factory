package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// memory add refuses a fact retold in other words (under half the words
// the same, most of the shorter one's), naming the memory; --force adds it.
func TestMemoryAddRefusesARetoldFactUnlessForce(t *testing.T) {
	memoryAs(t, "")
	proj := goalsProject(t)
	ctx, out := goalsCtx(t, proj, false)
	first := "Editing a department template's role.md or jobs/*.md: save the old text as internal/depttemplate/testdata/earlier/<key>/<path>.<n>, add its sha256 to earlierTexts in update.go, and repin testdata/current.sha256; otherwise TestBundledFilesChangeOnlyWithTheirEarlierHash fails."
	retold := "Editing a bundled department template's file fails TestBundledFilesChangeOnlyWithTheirEarlierHash: append the old sha256 to earlierTexts in internal/depttemplate/update.go, save the old text as testdata/earlier/<template>/<file>.<n> (git show <base>:<path>), and set the new hash in testdata/current.sha256."
	if err := runMemory(ctx, []string{"add", first, "--kind", "howto"}); err != nil {
		t.Fatal(err)
	}
	old := onlyMemory(t, proj)
	err := runMemory(ctx, []string{"add", retold, "--kind", "pitfall"})
	var sim *memory.SimilarError
	if !errors.As(err, &sim) {
		t.Fatalf("retold fact: %v", err)
	}
	assertContains(t, err.Error(), old.ID)
	assertContains(t, err.Error(), "--force")
	out.Reset()
	if err := runMemory(ctx, []string{"add", retold, "--kind", "pitfall", "--force"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "✓ Added ")
	if err := runMemory(ctx, []string{"add", "x y z words", "--updates", old.ID, "--force"}); err == nil {
		t.Error("--updates with --force accepted")
	}
}

// start and the transition into in progress print the memories tagged
// template for an issue whose body names a file under
// cmd/issue-cli/templates/; an issue elsewhere gets none.
func TestStartAtInProgressCarriesTheTemplateMemories(t *testing.T) {
	memoryAs(t, "")
	proj := goalsProject(t)
	if err := os.MkdirAll(filepath.Join(proj.WorkDir, "cmd", "issue-cli", "templates", "departments", "sales-b2c"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, _ := goalsCtx(t, proj, false)
	const pitfall = "Editing a bundled department template's file needs its earlier hash pinned in the same commit"
	if err := runMemory(ctx, []string{"add", pitfall, "--kind", "pitfall", "--tag", "template"}); err != nil {
		t.Fatal(err)
	}
	id := onlyMemory(t, proj).ID
	touching := &tracker.Issue{Slug: "engine", Title: "Engine", Status: "in progress", FilePath: filepath.Join(proj.IssueDir, "engine.md"),
		BodyRaw: "Change `cmd/issue-cli/templates/departments/sales-b2c/role.md`.\n"}
	var w bytes.Buffer
	used := printStartMemories(&w, ctx, touching)
	assertContains(t, w.String(), "For the template files this issue touches:\n  "+id+" (pitfall) "+pitfall)
	if len(used) != 1 || used[0] != id {
		t.Errorf("start recorded %v as used, want [%s]", used, id)
	}
	w.Reset()
	printTransitionMemories(&w, ctx, touching)
	assertContains(t, w.String(), "== Memories for in progress ==")
	assertContains(t, w.String(), id+" (pitfall) "+pitfall)

	elsewhere := *touching
	elsewhere.BodyRaw = "Change `cmd/issue-cli/main.go`.\n"
	w.Reset()
	printTransitionMemories(&w, ctx, &elsewhere)
	if strings.Contains(w.String(), pitfall) {
		t.Errorf("an issue outside the templates got the template memory:\n%s", w.String())
	}
}
