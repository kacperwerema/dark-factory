package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

const codemapWorkflow = memAtWorkflow + `workbench:
  layers:
    - name: kernel
      shared: true
    - name: domain
    - name: surfaces
  contexts:
    - name: Kernel
      layer: kernel
      packages: [kernel/...]
      leaks:
        - kernel/util -> app/feature
    - name: Feature
      layer: domain
      packages: [app/feature]
      paths: [docs/feature/]
    - name: Binaries
      layer: surfaces
      packages: [cmd/...]
`

// codemapProject is newTestProject with a code map and a small module:
// cmd/tool (main) imports app/feature, which imports kernel/util, which
// imports app/feature back (a known leak); app/stray is in no context.
func codemapProject(t *testing.T) *tracker.Project {
	t.Helper()
	p := newTestProject(t)
	root := p.WorkDir
	mustWrite(t, filepath.Join(root, "workflow.yaml"), codemapWorkflow)
	for path, src := range map[string]string{
		"go.mod":                 "module example.com/m\n",
		"kernel/util/util.go":    "package util\n\nimport _ \"example.com/m/app/feature\"\n",
		"app/feature/feature.go": "package feature\n\nimport _ \"example.com/m/kernel/util\"\n",
		"app/stray/stray.go":     "package stray\n",
		"cmd/tool/main.go":       "package main\n\nimport _ \"example.com/m/app/feature\"\n",
	} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, full, src)
	}
	return p
}

func TestCodemapCommandPrintsLayersEdgesAndUnmapped(t *testing.T) {
	p := codemapProject(t)
	ctx, out, _ := testContext(p)
	if err := runCodemap(ctx, nil); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"4 packages, 3 contexts, 3 layers", "surfaces\n  Binaries [binaries]", "kernel (shared)",
		"Feature [feature] · 1 packages", "feature → kernel: 1", "kernel → feature: 1  ↑ up a layer",
		"kernel/util → app/feature (Kernel → Feature) · known leak · kernel/util/util.go:3", "Unmapped packages: 1\n  app/stray"} {
		assertContains(t, got, want)
	}
	if strings.Index(got, "surfaces") > strings.Index(got, "kernel (shared)") {
		t.Errorf("layers print from the top:\n%s", got)
	}

	ctx, out, _ = testContext(p)
	ctx.JSONOutput = true
	if err := runCodemap(ctx, nil); err != nil {
		t.Fatal(err)
	}
	var j struct {
		Contexts []struct{ Key string }
		Upward   []struct {
			From, To string
			Known    bool
		}
		Unmapped []string
		Packages map[string]struct{ Context string }
	}
	if err := json.Unmarshal(out.Bytes(), &j); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(j.Contexts) != 3 || len(j.Upward) != 1 || !j.Upward[0].Known || len(j.Unmapped) != 1 || j.Packages["cmd/tool"].Context != "binaries" {
		t.Errorf("json %+v", j)
	}
}

func TestAffectedFilesNamesContextsAndBinaries(t *testing.T) {
	p := codemapProject(t)
	ctx, out, _ := testContext(p)
	if err := runAffected(ctx, []string{"--files", "kernel/util/util.go,docs/feature/a.md,README.md,app/feature/feature_test.go"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"4 files", "Changed: 1 packages, 2 more reached", "Works in: Kernel, Feature", "Reaches: Binaries",
		"Rebuild: cmd/tool", "Watch: kernel/util import up a layer", "Mapped by paths:\n  docs/feature/a.md → Feature",
		"Tests (reach nothing): 1", "Not mapped: README.md"} {
		assertContains(t, got, want)
	}

	ctx, out, _ = testContext(p)
	ctx.Stdin = strings.NewReader("cmd/tool/main.go\n\n")
	ctx.JSONOutput = true
	if err := runAffected(ctx, []string{"--files", "-"}); err != nil {
		t.Fatal(err)
	}
	var j struct {
		WorksIn  []string `json:"works_in"`
		Binaries []string
		Names    map[string]string
	}
	if err := json.Unmarshal(out.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	if strings.Join(j.WorksIn, ",") != "feature,binaries" || strings.Join(j.Binaries, ",") != "cmd/tool" || j.Names["feature"] != "Feature" {
		t.Errorf("json %+v", j)
	}
}

func TestAffectedNeedsOneSource(t *testing.T) {
	ctx, _, _ := testContext(codemapProject(t))
	t.Setenv(tracker.OwnSlugEnv, "")
	if err := runAffected(ctx, nil); err == nil || !strings.Contains(err.Error(), "<slug> or --files <list>: none given") || strings.Contains(err.Error(), "not both") {
		t.Fatalf("nothing given: %v", err)
	}
	if err := runAffected(ctx, []string{"cli/x", "--files", "a.go"}); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("both given: %v", err)
	}
	if err := runAffected(ctx, []string{"--root", ".", "a/b", "c/d"}); err == nil {
		t.Fatal("two slugs refused")
	}
	ctx, _, _ = testContext(newTestProject(t))
	if err := runAffected(ctx, []string{"--files", "a.go"}); err == nil || !strings.Contains(err.Error(), "no workbench.contexts") {
		t.Fatalf("no contexts: %v", err)
	}
}

// A dispatched worker leaves its slug out of affected, as with show or
// check; --files given alone still reads the files, not the session's slug.
func TestAffectedTakesOwnSlug(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(tracker.OwnSlugEnv, "cli/not-on-this-board")
	ctx, _, stderr := testContext(codemapProject(t))
	err := runAffected(ctx, nil)
	if err == nil || !strings.Contains(err.Error(), "cli/not-on-this-board") {
		t.Fatalf("affected with the slug left out did not look up the session's issue: %v", err)
	}
	assertContains(t, stderr.String(), "no slug given: using your issue cli/not-on-this-board")
	ctx, out, _ := testContext(codemapProject(t))
	if err := runAffected(ctx, []string{"--files", "kernel/util/util.go"}); err != nil {
		t.Fatalf("--files with the env set: %v", err)
	}
	assertContains(t, out.String(), "Changed:")
}
