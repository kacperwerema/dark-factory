package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

// catalogProject is codemapProject with a two-entry catalog: kernel-tools
// watches kernel/util/**, feature-docs one symbol in app/feature.
func catalogProject(t *testing.T) (root string, ctx *Context, out interface{ String() string }) {
	t.Helper()
	p := codemapProject(t)
	root = p.WorkDir
	dir := filepath.Join(root, filepath.FromSlash(workflowupgrade.CatalogPath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "kernel-tools.yaml"), "id: kernel-tools\nversion: v0.10.0\ncategory: lifecycle\nsources: [\"kernel/util/**\"]\n")
	mustWrite(t, filepath.Join(dir, "feature-docs.yaml"), "id: feature-docs\nversion: v0.11.0\ncategory: lifecycle\nsources: [\"app/feature/feature.go#Feature\"]\n")
	mustWrite(t, filepath.Join(p.IssueDir, "plan.md"), "---\ntitle: \"Plan\"\nstatus: \"in design\"\n---\n"+
		"The change edits `kernel/util/util.go:3` and app/feature/feature.go#Feature, adds kernel/util/new.go,\n"+
		"reads https://example.com/kernel/util/x.go and nowhere/else.go, and tests app/feature/feature_test.go.\n")
	c, o, _ := testContext(p)
	return root, c, o
}

// In design the branch changes nothing yet: affected <slug> reads the
// paths the issue names, lists the entries catalog-check will name for
// them and the lines to write.
func TestAffectedNamesTheCatalogEntriesAPlannedEditTurnsRed(t *testing.T) {
	_, ctx, out := catalogProject(t)
	if err := runAffected(ctx, []string{"plan"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"Planned files named in the issue: kernel/util/util.go, app/feature/feature.go, kernel/util/new.go, app/feature/feature_test.go",
		"Catalog impact: 2 entries turn red in catalog-check once these files change",
		"kernel-tools v0.10.0: kernel/util/util.go (matches kernel/util/**), kernel/util/new.go (matches kernel/util/**)",
		"feature-docs v0.11.0: app/feature/feature.go (matches app/feature/feature.go#Feature) — only if that symbol's code changes",
		"issue-cli workflow catalog-bump feature-docs,kernel-tools",
		"Catalog: n/a feature-docs,kernel-tools <why>",
		"issue-cli workflow catalog-na feature-docs,kernel-tools <why>",
	} {
		assertContains(t, got, want)
	}
	for _, not := range []string{"example.com", "nowhere/else.go"} {
		if strings.Contains(got, not) {
			t.Errorf("%s is no planned file:\n%s", not, got)
		}
	}
}

// --files gets the catalog impact too, and --json carries it; a file no
// source matches says no Catalog: line is needed.
func TestAffectedFilesCatalogImpact(t *testing.T) {
	_, ctx, out := catalogProject(t)
	if err := runAffected(ctx, []string{"--files", "cmd/tool/main.go"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Catalog impact: none — no entry's sources: match these files; the commits need no Catalog: line")

	_, ctx, out = catalogProject(t)
	ctx.JSONOutput = true
	if err := runAffected(ctx, []string{"--files", "kernel/util/util.go"}); err != nil {
		t.Fatal(err)
	}
	var j struct {
		Catalog []workflowupgrade.PlannedEntry `json:"catalog"`
	}
	if err := json.Unmarshal([]byte(out.String()), &j); err != nil {
		t.Fatal(err)
	}
	if len(j.Catalog) != 1 || j.Catalog[0].ID != "kernel-tools" {
		t.Errorf("json catalog = %+v", j.Catalog)
	}
}

// A project without the catalog (any but dark-factory) prints no catalog
// section at all.
func TestAffectedWithoutACatalogSaysNothingOfIt(t *testing.T) {
	ctx, out, _ := testContext(codemapProject(t))
	if err := runAffected(ctx, []string{"--files", "kernel/util/util.go"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Catalog") {
		t.Errorf("catalog section without a catalog:\n%s", out.String())
	}
}
