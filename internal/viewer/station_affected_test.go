package viewer

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

const affectedWorkflow = `statuses:
  - name: "idea"
  - name: "testing"
    role: "verify"
  - name: "human-testing"
    role: "owner-test"
  - name: "done"
transitions:
  - from: "idea"
    to: "testing"
  - from: "testing"
    to: "human-testing"
  - from: "human-testing"
    to: "done"
workbench:
  layers:
    - name: kernel
      shared: true
    - name: domain
    - name: surfaces
  contexts:
    - name: Kernel
      layer: kernel
      packages: [kernel/...]
    - name: Feature
      layer: domain
      packages: [app/feature]
    - name: Binaries
      layer: surfaces
      packages: [cmd/...]
`

func affectedGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false",
		"-c", "maintenance.auto=false", "-c", "gc.auto=0", "-c", "core.hooksPath=/dev/null"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// affectedProject is a git repo with a small module on main and a work
// branch work/ui/station that changes kernel/util (reached by app/feature
// and cmd/tool), with the issue at status and its dispatch base recorded.
func affectedProject(t *testing.T, status string) tracker.Project {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := affectedDir(t)
	write := func(rel, s string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	affectedGit(t, root, "init", "-q", "-b", "main")
	write(".gitignore", "issues/\n")
	write("workflow.yaml", affectedWorkflow)
	write("go.mod", "module example.com/m\n")
	write("kernel/util/util.go", "package util\n")
	write("app/feature/feature.go", "package feature\n\nimport _ \"example.com/m/kernel/util\"\n")
	write("cmd/tool/main.go", "package main\n\nimport _ \"example.com/m/app/feature\"\n")
	affectedGit(t, root, "add", "-A")
	affectedGit(t, root, "commit", "-q", "-m", "init")
	base := affectedGit(t, root, "rev-parse", "main")
	affectedGit(t, root, "checkout", "-q", "-b", "work/ui/station")
	write("kernel/util/util.go", "package util\n\nvar X = 1\n")
	affectedGit(t, root, "commit", "-q", "-am", "change util")
	affectedGit(t, root, "checkout", "-q", "main")

	issues := filepath.Join(root, "issues")
	file := filepath.Join(issues, "UI", "station.md")
	write("issues/UI/station.md", "---\ntitle: \"Station fixture\"\nslug: \"station\"\nstatus: \""+status+"\"\nsystem: \"UI\"\ncreated: \"2026-10-20\"\n---\n\nBody.\n")
	st := tracker.StatsStore{Dispatches: []tracker.Dispatch{{At: time.Now(), Agent: "claude", BaseBranch: "main", BaseSHA: base}}}
	data, _ := json.Marshal(st)
	if err := os.WriteFile(tracker.StatsSidecarPath(file), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return tracker.Project{Name: "agents", Slug: "agents", IssueDir: issues, WorkDir: root, WorkflowFile: filepath.Join(root, "workflow.yaml")}
}

func affectedDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestStationShowsAffectedAtHumanTesting (A2): at human-testing the
// Station shows the branch's footprint; at idea it shows none.
func TestStationShowsAffectedAtHumanTesting(t *testing.T) {
	p := affectedProject(t, "human-testing")
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{p})
	defer ts.Close()
	code, html := getBody(t, ts.URL+"/p/agents/issue/ui/station")
	if code != 200 {
		t.Fatalf("status %d: %s", code, html)
	}
	assertContains(t, html, `data-testid="station.affected"`, `1 files · 1 packages changed · 2 more reached`,
		`data-kind="works-in">Kernel</span>`, `data-kind="reaches">Feature</span>`, `data-kind="reaches">Binaries</span>`,
		`Rebuild: <code>cmd/tool</code>`, `/p/agents/codemap?files=kernel%2Futil%2Futil.go`)
	if strings.Contains(html, `data-testid="station.affected.watch"`) {
		t.Error("no upward import, no Watch")
	}

	idle := affectedProject(t, "idea")
	ts2 := newTestServer(t, []tracker.Project{idle})
	defer ts2.Close()
	if _, html := getBody(t, ts2.URL+"/p/agents/issue/ui/station"); strings.Contains(html, `data-testid="station.affected"`) {
		t.Error("idea is no verify or owner-test status: no Affected block")
	}
}

// TestCodemapPageDrawsLanesAndFootprint: the page draws a lane per layer,
// the chips, the matrix and ?files='s footprint, and embeds the graph.
func TestCodemapPageDrawsLanesAndFootprint(t *testing.T) {
	p := affectedProject(t, "idea")
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{p})
	defer ts.Close()
	code, html := getBody(t, ts.URL+"/p/agents/codemap?files=kernel/util/util.go")
	if code != 200 {
		t.Fatalf("status %d: %s", code, html)
	}
	assertContains(t, html, `data-testid="codemap.page"`, `data-layer="surfaces"`, `data-layer="kernel"`,
		`data-ctx="kernel" data-mark="works-in"`, `data-ctx="feature" data-mark="reaches"`,
		`data-p="kernel/util" data-mark="changed"`, `data-p="app/feature" data-mark="hit"`,
		`data-testid="codemap.footprint"`, `Works in <b>Kernel</b>; reaches Feature, Binaries.`,
		`id="codemap-graph"`, `"kernel/util"`, `/static/codemap.js`)
	if i, j := strings.Index(html, `data-layer="surfaces"`), strings.Index(html, `data-layer="kernel"`); i > j {
		t.Error("lanes run top layer first")
	}

	none := setupStationProject(t)
	ts2 := newTestServer(t, []tracker.Project{none})
	defer ts2.Close()
	_, html = getBody(t, ts2.URL+"/p/agents/codemap")
	if !strings.Contains(html, `data-testid="codemap.empty"`) && !strings.Contains(html, `data-testid="codemap.plan"`) {
		t.Errorf("page renders either way:\n%s", html)
	}
}
