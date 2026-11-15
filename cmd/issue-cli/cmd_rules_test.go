package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/rules"
)

func TestRulesCommandShowsBothLayers(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p := goalsProject(t)
	ctx, out := goalsCtx(t, p, false)
	if err := runRules(ctx, nil); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "none yet: the owner adds them")

	path := rules.MachinePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, path, "- [no-force] Never force-push main.\n")
	wf := filepath.Join(p.WorkDir, "workflow.yaml")
	mustWrite(t, wf, "rules:\n  - text: Force-push main on tag days.\n    narrows: no-force\n")
	p.WorkflowFile = wf
	ctx, out = goalsCtx(t, p, false)
	if err := runRules(ctx, nil); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"== On every project", "[no-force] Never force-push main.", "== On this project", "(narrows [no-force])"} {
		assertContains(t, got, want)
	}
	if strings.Index(got, "On every project") > strings.Index(got, "On this project") {
		t.Errorf("the machine's layer comes first:\n%s", got)
	}
	out.Reset()
	printStartRules(ctx)
	assertContains(t, out.String(), "Rules: 1 on every project, 1 on this project:")
}

func TestRulesCommandTakesNoArguments(t *testing.T) {
	ctx, _ := goalsCtx(t, goalsProject(t), false)
	if err := runRules(ctx, []string{"add"}); err == nil {
		t.Fatal("rules has no write command")
	}
}
