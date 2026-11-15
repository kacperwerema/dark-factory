package codemap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// firstArtifact is the lead's hand-drawn map (main at 175774e1): packages
// with their context, lines and imports, surface files with the contexts
// they import, the contexts and the review example.
type firstArtifact struct {
	PK map[string]struct {
		C string   `json:"c"`
		L int      `json:"l"`
		D []string `json:"d"`
	} `json:"pk"`
	Files [][]json.RawMessage `json:"files"`
	Ctx   []struct {
		Key, Name, Summary string
		Layer              int
	} `json:"ctx"`
	Example []string `json:"example"`
}

// artifactDir is the repo dir of an artifact package id.
func artifactDir(id string) string {
	if strings.HasPrefix(id, "cmd/") || strings.HasPrefix(id, "tools/") {
		return id
	}
	return "internal/" + id
}

// loadArtifact turns the artifact's data into a Map: contexts listing
// their packages exactly, five layers (kernel and core shared), and each
// surface file importing one package of every context the artifact gave it.
func loadArtifact(t *testing.T) (*Map, firstArtifact) {
	t.Helper()
	raw, err := os.ReadFile("testdata/first-artifact.json")
	if err != nil {
		t.Fatal(err)
	}
	var a firstArtifact
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatal(err)
	}
	layers := []string{"kernel", "core", "domain", "checks", "surfaces"}
	wb := tracker.WorkbenchConfig{}
	for i, l := range layers {
		wb.Layers = append(wb.Layers, tracker.WorkbenchLayer{Name: l, Shared: i < 2})
	}
	byCtx := map[string][]string{}
	for id, p := range a.PK {
		byCtx[p.C] = append(byCtx[p.C], artifactDir(id))
	}
	first := map[string]string{}
	for _, c := range a.Ctx {
		wb.Contexts = append(wb.Contexts, tracker.WorkbenchContext{Name: c.Name, Key: c.Key, Layer: layers[c.Layer], Packages: byCtx[c.Key]})
		for _, d := range byCtx[c.Key] {
			if first[c.Key] == "" || d < first[c.Key] {
				first[c.Key] = d
			}
		}
	}
	var pkgs []*Package
	for id, p := range a.PK {
		pk := &Package{Dir: artifactDir(id), Lines: p.L, Main: strings.HasPrefix(id, "cmd/") && id != "cmd/issue-cli/templates" || strings.HasPrefix(id, "tools/")}
		for _, d := range p.D {
			pk.Imports = append(pk.Imports, artifactDir(d))
		}
		pkgs = append(pkgs, pk)
	}
	var files []*File
	for _, row := range a.Files {
		var name string
		var lines int
		var ctxs []string
		_ = json.Unmarshal(row[0], &name)
		_ = json.Unmarshal(row[1], &lines)
		_ = json.Unmarshal(row[2], &ctxs)
		name = strings.Replace(strings.Replace(name, "viewer/", "internal/viewer/", 1), "cli/", "cmd/issue-cli/", 1)
		f := &File{Path: name, Package: filepath.Dir(name), Lines: lines}
		for _, c := range ctxs {
			f.Imports = append(f.Imports, Import{Package: first[c], Line: 1})
		}
		files = append(files, f)
	}
	m := New(wb, pkgs, files)
	m.HasGo = true
	return m, a
}

// TestAffectedMatchesTheFirstArtifact: the artifact's review example (the
// post-mortem auto_types change) names the same contexts worked in and
// reached as the artifact computed (its review() run over the same data:
// touched core, surface, knowledge, checker, runtime; reaches measure,
// governance, company, release, quality; 36 more packages).
func TestAffectedMatchesTheFirstArtifact(t *testing.T) {
	m, a := loadArtifact(t)
	fp := m.Affected(a.Example)
	if want := []string{"surface", "checker", "runtime", "knowledge", "core"}; !reflect.DeepEqual(fp.WorksIn, want) {
		t.Errorf("works in %v, want %v", fp.WorksIn, want)
	}
	if want := []string{"quality", "governance", "company", "measure", "release"}; !reflect.DeepEqual(fp.Reaches, want) {
		t.Errorf("reaches %v, want %v", fp.Reaches, want)
	}
	if len(fp.Reached) != 36 {
		t.Errorf("reached %d packages, want 36: %v", len(fp.Reached), fp.Reached)
	}
	if want := []FileContext{{File: "cmd/issue-cli/cmd_postmortem.go", Contexts: []string{"runtime", "knowledge"}}}; !reflect.DeepEqual(fp.Surface, want) {
		t.Errorf("surface %v, want %v", fp.Surface, want)
	}
	if !reflect.DeepEqual(fp.Other, []string{"docs/workflow.md"}) {
		t.Errorf("other %v", fp.Other)
	}
	for _, b := range []string{"cmd/issue-cli", "cmd/issue-viewer"} {
		if !contains(fp.Binaries, b) {
			t.Errorf("binaries %v miss %s", fp.Binaries, b)
		}
	}
	if !reflect.DeepEqual(fp.Watch, []string{"internal/tracker"}) {
		t.Errorf("watch %v, want tracker (the core imports memory, rules and autostart)", fp.Watch)
	}
}

// TestUpwardEdgesOfTheFirstArtifact: the artifact's findings, the core
// importing memory, rules and autostart, and health importing lint.
func TestUpwardEdgesOfTheFirstArtifact(t *testing.T) {
	m, _ := loadArtifact(t)
	got := map[string]bool{}
	for _, e := range m.Upward() {
		got[e.From+" -> "+e.To] = true
	}
	for _, want := range []string{"internal/tracker -> internal/memory", "internal/tracker -> internal/rules", "internal/tracker -> internal/autostart", "internal/health -> internal/lint", "internal/wfedit -> internal/lint"} {
		if !got[want] {
			t.Errorf("upward edges miss %s: %v", want, got)
		}
	}
	if n := len(m.Reaches("internal/memory")); n != 41 {
		t.Errorf("memory reaches %d packages, the artifact says 41", n)
	}
	if n := len(m.Reaches("internal/rules")); n != 39 {
		t.Errorf("rules reaches %d packages, the artifact says 39", n)
	}
}

func TestMatchPrefersExactThenLongest(t *testing.T) {
	wb := tracker.WorkbenchConfig{
		Layers: []tracker.WorkbenchLayer{{Name: "kernel"}, {Name: "domain"}},
		Contexts: []tracker.WorkbenchContext{
			{Name: "Kernel", Layer: "kernel", Packages: []string{"internal/telemetry", "internal/..."}},
			{Name: "Measure", Layer: "domain", Packages: []string{"internal/telemetry/share", "internal/telemetry/..."}},
		},
	}
	m := New(wb, []*Package{{Dir: "internal/telemetry"}, {Dir: "internal/telemetry/share"}, {Dir: "internal/telemetry/x"}, {Dir: "internal/tracker"}, {Dir: "cmd/x"}}, nil)
	want := map[string]string{"internal/telemetry": "kernel", "internal/telemetry/share": "measure", "internal/telemetry/x": "measure", "internal/tracker": "kernel", "cmd/x": ""}
	for d, k := range want {
		if got := m.Packages[d].Context; got != k {
			t.Errorf("%s in %q, want %q", d, got, k)
		}
	}
	if !reflect.DeepEqual(m.Unmapped(), []string{"cmd/x"}) {
		t.Errorf("unmapped %v", m.Unmapped())
	}
}

func TestParseLeak(t *testing.T) {
	from, to, ok := ParseLeak(" internal/tracker ->internal/memory/ ")
	if !ok || from != "internal/tracker" || to != "internal/memory" {
		t.Fatalf("%q %q %v", from, to, ok)
	}
	if _, _, ok := ParseLeak("internal/tracker"); ok {
		t.Fatal("no arrow parsed")
	}
}

// writeRepo writes a small module: kernel/util, core/board (imports util),
// app/cli (main, imports board), and board importing app/feature, a layer up.
func writeRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                   "module example.com/m\n\ngo 1.22\n",
		"kernel/util/util.go":      "package util\n",
		"core/board/board.go":      "package board\n\nimport (\n\t\"fmt\"\n\t\"example.com/m/kernel/util\"\n\t\"example.com/m/app/feature\"\n)\n\nvar _ = fmt.Sprint\nvar _ = util.X\nvar _ = feature.Y\n",
		"core/board/board_test.go": "package board\n\nimport \"example.com/m/app/cli\"\n",
		"app/feature/feature.go":   "package feature\n\nimport \"example.com/m/kernel/util\"\n",
		"app/cli/main.go":          "package main\n\nimport (\n\t\"example.com/m/core/board\"\n\t\"example.com/m/app/feature\"\n)\n",
		"app/cli/gen.go":           "//go:build ignore\n\npackage main\n\nimport \"example.com/m/core/board\"\n",
		"app/cli/testdata/x/x.go":  "package x\n",
		"nested/go.mod":            "module other\n",
		"nested/n.go":              "package n\n",
		"docs/readme.md":           "# docs\n",
	}
	for p, s := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func repoConfig() tracker.WorkbenchConfig {
	return tracker.WorkbenchConfig{
		Layers: []tracker.WorkbenchLayer{{Name: "kernel", Shared: true}, {Name: "core", Shared: true}, {Name: "domain"}, {Name: "surfaces"}},
		Contexts: []tracker.WorkbenchContext{
			{Name: "Kernel", Layer: "kernel", Packages: []string{"kernel/..."}},
			{Name: "Core", Layer: "core", Packages: []string{"core/board"}, Paths: []string{"docs/"}},
			{Name: "Feature", Layer: "domain", Packages: []string{"app/feature"}},
			{Name: "Surfaces", Layer: "surfaces", Packages: []string{"app/cli"}},
		},
	}
}

func TestBuildReadsImportsFromTheTree(t *testing.T) {
	m, err := Build(writeRepo(t), repoConfig())
	if err != nil {
		t.Fatal(err)
	}
	if got := m.PackageDirs(); !reflect.DeepEqual(got, []string{"app/cli", "app/feature", "core/board", "kernel/util"}) {
		t.Fatalf("packages %v (testdata, nested modules and test files stay out)", got)
	}
	cli := m.Packages["app/cli"]
	if !cli.Main || !reflect.DeepEqual(cli.Files, []string{"app/cli/main.go"}) {
		t.Errorf("cli %+v: a //go:build ignore file is left out", cli)
	}
	if !reflect.DeepEqual(m.Packages["core/board"].Imports, []string{"app/feature", "kernel/util"}) {
		t.Errorf("board imports %v", m.Packages["core/board"].Imports)
	}
	up := m.Upward()
	if len(up) != 1 || up[0].From != "core/board" || up[0].To != "app/feature" || up[0].File != "core/board/board.go" || up[0].Line != 6 || up[0].Known {
		t.Fatalf("upward %+v", up)
	}
	fp := m.Affected([]string{"kernel/util/util.go", "core/board/board_test.go", "docs/readme.md", "app/newpkg/a.go", "README.md"})
	if !reflect.DeepEqual(fp.Changed, []string{"kernel/util"}) || !reflect.DeepEqual(fp.Reached, []string{"app/cli", "app/feature", "core/board"}) {
		t.Errorf("changed %v reached %v", fp.Changed, fp.Reached)
	}
	if !reflect.DeepEqual(fp.WorksIn, []string{"kernel", "core"}) || !reflect.DeepEqual(fp.Reaches, []string{"feature", "surfaces"}) {
		t.Errorf("works in %v reaches %v", fp.WorksIn, fp.Reaches)
	}
	if !reflect.DeepEqual(fp.Binaries, []string{"app/cli"}) || !reflect.DeepEqual(fp.Tests, []string{"core/board/board_test.go"}) {
		t.Errorf("binaries %v tests %v", fp.Binaries, fp.Tests)
	}
	if len(fp.New) != 1 || fp.New[0].File != "app/newpkg/a.go" || !reflect.DeepEqual(fp.Other, []string{"README.md"}) {
		t.Errorf("new %v other %v", fp.New, fp.Other)
	}
	if got := m.FileContexts("app/cli/main.go"); !reflect.DeepEqual(got, []string{"feature"}) {
		t.Errorf("main.go works in %v (shared core left out)", got)
	}
}

func TestLeaksQuietAnEdgeAndGoStale(t *testing.T) {
	wb := repoConfig()
	wb.Contexts[1].Leaks = []string{"core/board -> app/feature", "core/board -> app/cli"}
	m, err := Build(writeRepo(t), wb)
	if err != nil {
		t.Fatal(err)
	}
	if up := m.Upward(); len(up) != 1 || !up[0].Known {
		t.Fatalf("upward %+v", up)
	}
	if st := m.StaleLeaks(); len(st) != 1 || st[0].To != "app/cli" {
		t.Fatalf("stale %+v", st)
	}
}

func TestNoGoModMapsByPathsOnly(t *testing.T) {
	root := t.TempDir()
	wb := tracker.WorkbenchConfig{Layers: []tracker.WorkbenchLayer{{Name: "all"}}, Contexts: []tracker.WorkbenchContext{{Name: "Game", Layer: "all", Paths: []string{"src/**"}}}}
	m, err := Build(root, wb)
	if err != nil {
		t.Fatal(err)
	}
	fp := m.Affected([]string{"src/a/b.cs", "x.txt"})
	if !fp.NoGo || len(fp.ByPaths) != 1 || !reflect.DeepEqual(fp.WorksIn, []string{"game"}) || !reflect.DeepEqual(fp.Other, []string{"x.txt"}) {
		t.Fatalf("%+v", fp)
	}
}

// TestCodemapParsesOncePerHead: Load with an unchanged key reads no tree.
func TestCodemapParsesOncePerHead(t *testing.T) {
	root := writeRepo(t)
	before := Parses()
	for i := 0; i < 3; i++ {
		if _, err := Load(root, "head-a", repoConfig()); err != nil {
			t.Fatal(err)
		}
	}
	if n := Parses() - before; n != 1 {
		t.Fatalf("%d parses for one key, want 1", n)
	}
	if _, err := Load(root, "head-b", repoConfig()); err != nil {
		t.Fatal(err)
	}
	if n := Parses() - before; n != 2 {
		t.Fatalf("%d parses after a new key, want 2", n)
	}
}

// TestEngineMapHasNoNewUpwardImport: the engine's own workflow.yaml maps
// every package and lists every upward import as a known leak. make
// codemap-check runs it as a cheap step of make validate, before the suite.
func TestEngineMapHasNoNewUpwardImport(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	wf, err := tracker.LoadWorkflow(filepath.Join(root, "workflow.yaml"))
	if err != nil || wf == nil {
		t.Fatalf("workflow.yaml: %v", err)
	}
	if !Configured(wf.Workbench) {
		t.Skip("no workbench.contexts")
	}
	m, err := Build(root, wf.Workbench)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range m.Unmapped() {
		t.Errorf("unmapped package %s: add it to the packages: list of one context under workbench.contexts in workflow.yaml", d)
	}
	for _, e := range m.Upward() {
		if !e.Known {
			t.Errorf("new upward import %s -> %s (%s:%d)", e.From, e.To, e.File, e.Line)
		}
	}
	for _, l := range m.StaleLeaks() {
		t.Errorf("stale leak %s -> %s", l.From, l.To)
	}
}
