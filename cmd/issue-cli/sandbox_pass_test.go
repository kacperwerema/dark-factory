package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/clilog"
)

// sandboxFixture is a sandbox as lib.sh's sandbox_project makes it: a temp
// folder holding projects.yaml, the project (issues, workflow) and the XDG
// dirs, marked unless mark is false. The caller is a worker on another issue.
type sandboxFixture struct {
	dir, cfg, log string
}

func newSandboxFixture(t *testing.T, mark bool) sandboxFixture {
	t.Helper()
	dir := realTempDir(t)
	f := sandboxFixture{dir: dir, cfg: filepath.Join(dir, "projects.yaml"), log: filepath.Join(dir, "clilog.jsonl")}
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("demo/issues/loner.md", "---\ntitle: \"Loner\"\nstatus: \"backlog\"\n---\n\nBody.\n")
	write("demo/issues/mine.md", "---\ntitle: \"Mine\"\nstatus: \"backlog\"\n---\n\nBody.\n")
	write("demo/workflow.yaml", "statuses:\n  - name: \"backlog\"\n  - name: \"in progress\"\n  - name: \"done\"\n")
	f.writeConfig(t, filepath.Join(dir, "demo"))
	if mark {
		f.mark(t, dir)
	}
	for _, d := range []string{"state", "config"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("ISSUE_VIEWER_CONFIG", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_WORKER", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "mine")
	t.Setenv("ISSUE_CLI_LOG", f.log)
	t.Setenv("ISSUE_CLI_TELEMETRY", "off")
	return f
}

func (f sandboxFixture) writeConfig(t *testing.T, projectDir string) {
	t.Helper()
	cfg := "projects:\n  - name: \"Demo\"\n    slug: \"demo\"\n    issues: \"" + projectDir + "/issues\"\n    workflow: \"" + projectDir + "/workflow.yaml\"\n    workdir: \"" + projectDir + "\"\n    terminal: \"none\"\n"
	if err := os.WriteFile(f.cfg, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f sandboxFixture) mark(t *testing.T, folder string) {
	t.Helper()
	b, _ := json.Marshal(sandboxMarker{Folder: folder, Created: "2026-11-02T10:00:00Z", By: "test"})
	if err := os.WriteFile(filepath.Join(filepath.Dir(f.cfg), sandboxMarkerName), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// run runs issue-cli with --config on the fixture, logging the call.
func (f sandboxFixture) run(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return f.runWith(t, append([]string{"--config", f.cfg, "--project", "demo"}, args...)...)
}

func (f sandboxFixture) runWith(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	l := newCallLog(args, time.Now())
	inv := &invocation{}
	var out, errw strings.Builder
	err = runTraced(args, strings.NewReader(""), &out, &errw, inv)
	l.write(inv, err, false)
	return out.String(), errw.String(), err
}

func (f sandboxFixture) lastRecord(t *testing.T) clilog.Record {
	t.Helper()
	b, err := os.ReadFile(f.log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var r clilog.Record
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func (f sandboxFixture) status(t *testing.T, slug string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.dir, "demo", "issues", slug+".md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "status: "); ok {
			return strings.Trim(v, `"`)
		}
	}
	return ""
}

const refusedOther = "you work on mine only"

// On a marked sandbox a worker's transition of another issue passes, says
// so on stderr and in the clilog; on the same sandbox unmarked it is
// refused as before.
func TestSandboxPassOnAMarkedConfig(t *testing.T) {
	f := newSandboxFixture(t, true)
	_, errOut, err := f.run(t, "transition", "loner", "--to", "in progress")
	if err != nil {
		t.Fatalf("marked sandbox: %v\n%s", err, errOut)
	}
	assertContains(t, errOut, "sandbox: the worker guard passed transition for worker (marked sandbox "+f.dir+")")
	if got := f.status(t, "loner"); got != "in progress" {
		t.Fatalf("loner status = %q", got)
	}
	if r := f.lastRecord(t); !r.Sandbox || r.SandboxRefused {
		t.Fatalf("clilog record = %+v, want sandbox: true", r)
	}

	out, _, err := f.run(t, "--json", "lead", "whoami")
	if err != nil {
		t.Fatal(err)
	}
	var who whoamiJSON
	if err := json.Unmarshal([]byte(out), &who); err != nil || who.Role != "worker" || who.Sandbox == nil || !who.Sandbox.Pass {
		t.Fatalf("whoami: %v %s", err, out)
	}
}

func TestSandboxPassRefusedOnAnUnmarkedConfig(t *testing.T) {
	f := newSandboxFixture(t, false)
	_, errOut, err := f.run(t, "transition", "loner", "--to", "in progress")
	if err == nil || !strings.Contains(err.Error(), refusedOther) {
		t.Fatalf("unmarked sandbox: err = %v", err)
	}
	if strings.Contains(errOut, "sandbox: the worker guard passed") {
		t.Fatalf("unmarked sandbox said it passed: %s", errOut)
	}
	if got := f.status(t, "loner"); got != "backlog" {
		t.Fatalf("a refused transition moved loner to %q", got)
	}
	if r := f.lastRecord(t); r.Sandbox {
		t.Fatalf("clilog says sandbox on a refusal: %+v", r)
	}
}

// AC2: the owner's real config never passes, even with a marker copied
// beside it: the default projects.yaml (found without --config), and a
// config named by --config that is the default one.
func TestSandboxPassNeverForTheRealConfig(t *testing.T) {
	f := newSandboxFixture(t, true)
	def := userConfigPath() // under the fixture's XDG_CONFIG_HOME
	if err := os.MkdirAll(filepath.Dir(def), 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(f.cfg)
	if err := os.WriteFile(def, b, 0o644); err != nil {
		t.Fatal(err)
	}
	marker, _ := json.Marshal(sandboxMarker{Folder: filepath.Dir(def)})
	if err := os.WriteFile(filepath.Join(filepath.Dir(def), sandboxMarkerName), marker, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{
		"found as the user config": {"--project", "demo", "transition", "loner", "--to", "in progress"},
		"named with --config":      {"--config", def, "--project", "demo", "transition", "loner", "--to", "in progress"},
	} {
		t.Run(name, func(t *testing.T) {
			wd, _ := os.Getwd()
			t.Chdir(t.TempDir()) // no ./projects.yaml
			defer func() { _ = os.Chdir(wd) }()
			_, errOut, err := f.runWith(t, args...)
			if err == nil || !strings.Contains(err.Error(), refusedOther) {
				t.Fatalf("err = %v\n%s", err, errOut)
			}
			assertContains(t, errOut, "not a marked sandbox:")
		})
	}
	if got := f.status(t, "loner"); got != "backlog" {
		t.Fatalf("loner moved to %q", got)
	}
}

// Containment: every way out of the marked folder refuses, and says which.
func TestSandboxPassRefusesAPathOutside(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, f sandboxFixture)
		why   string
	}{
		{"issues outside", func(t *testing.T, f sandboxFixture) {
			outside := realTempDir(t)
			if err := os.Rename(filepath.Join(f.dir, "demo"), filepath.Join(outside, "demo")); err != nil {
				t.Fatal(err)
			}
			f.writeConfig(t, filepath.Join(outside, "demo"))
		}, "is outside " /* project demo: issues … */},
		{"workflow through a symlink", func(t *testing.T, f sandboxFixture) {
			outside := realTempDir(t)
			wf := filepath.Join(f.dir, "demo", "workflow.yaml")
			if err := os.Rename(wf, filepath.Join(outside, "workflow.yaml")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(outside, "workflow.yaml"), wf); err != nil {
				t.Fatal(err)
			}
		}, "project demo: workflow"},
		{"workdir a worktree of an outside repo", func(t *testing.T, f sandboxFixture) {
			outside := realTempDir(t)
			if err := os.WriteFile(filepath.Join(f.dir, "demo", ".git"), []byte("gitdir: "+outside+"/.git/worktrees/demo\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "git folder of"},
		{"XDG_STATE_HOME outside", func(t *testing.T, f sandboxFixture) {
			t.Setenv("XDG_STATE_HOME", realTempDir(t))
		}, "$XDG_STATE_HOME"},
		{"XDG_CONFIG_HOME unset", func(t *testing.T, f sandboxFixture) {
			t.Setenv("XDG_CONFIG_HOME", "")
		}, "$XDG_CONFIG_HOME is not set"},
		{"stale marker", func(t *testing.T, f sandboxFixture) {
			f.mark(t, realTempDir(t))
		}, "a stale or copied marker"},
		{"not under a temp folder", func(t *testing.T, f sandboxFixture) {
			orig := sandboxTempRoots
			sandboxTempRoots = func() []string { return []string{"/nonexistent-root"} }
			t.Cleanup(func() { sandboxTempRoots = orig })
		}, "is not under a temp folder"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSandboxFixture(t, true)
			tc.setup(t, f)
			_, errOut, err := f.run(t, "transition", "loner", "--to", "in progress")
			if err == nil || !strings.Contains(err.Error(), refusedOther) {
				t.Fatalf("err = %v\n%s", err, errOut)
			}
			assertContains(t, errOut, "not a marked sandbox:")
			assertContains(t, errOut, tc.why)
		})
	}
}

func TestSandboxPassRefusesUnderWorktrees(t *testing.T) {
	f := newSandboxFixture(t, true)
	v := sandboxVerdict{}
	dir := filepath.Join(f.dir, ".worktrees", "x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(sandboxMarker{Folder: dir})
	if err := os.WriteFile(filepath.Join(dir, sandboxMarkerName), b, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := &Context{ConfigPath: filepath.Join(dir, "projects.yaml"), Config: configSource{Path: filepath.Join(dir, "projects.yaml"), Why: "--config"}}
	v = computeSandbox(ctx)
	if v.Pass || !strings.Contains(v.Why, "under a .worktrees/ folder") {
		t.Fatalf("verdict = %+v", v)
	}
}

// The never-list stays refused on a marked sandbox (owner's rule D-4), and
// the refusal is said and logged so the owner sees when it bites.
func TestSandboxPassNeverList(t *testing.T) {
	f := newSandboxFixture(t, true)
	_, errOut, err := f.run(t, "dispatch", "loner", "--quote", "go")
	if err == nil || !strings.Contains(err.Error(), "workers can't dispatch") {
		t.Fatalf("dispatch on a sandbox: err = %v", err)
	}
	assertContains(t, errOut, "sandbox: dispatch stays refused in a sandbox (owner's rule, D-4)")
	if r := f.lastRecord(t); !r.SandboxRefused || r.Sandbox {
		t.Fatalf("clilog = %+v, want sandbox_refused", r)
	}
	for verb, rule := range guardVerbs {
		if rule == neverInSandbox && sandboxPass(&Context{Project: nil}, verb, "worker") {
			t.Errorf("%s passed", verb)
		}
	}
}
