package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runProjectCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out, errw strings.Builder
	err := run(args, strings.NewReader(""), &out, &errw)
	return out.String() + errw.String(), err
}

func TestProjectInitDryRunListsEverythingAndWritesNothing(t *testing.T) {
	root := realTempDir(t)
	withCwd(t, root)
	dir := filepath.Join(root, "shop")
	config := filepath.Join(root, "sandbox.yaml")
	out, err := runProjectCLI(t, "--config", filepath.Join(root, "missing.yaml"), "project", "init", dir, "--name", "Acme Shop", "--config", config, "--dry-run")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"Dry run: project init " + dir + " (nothing changed)",
		dir + "/: create",
		filepath.Join(dir, "issues") + "/: create",
		filepath.Join(dir, "docs") + "/: create",
		filepath.Join(dir, "workflow.yaml") + ": write",
		config + ": would write a new file with project acme-shop:",
		"    slug: acme-shop",
		"Apply: issue-cli project init " + dir + " --name 'Acme Shop' --config " + config,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	for _, f := range []string{dir, config} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("dry run made %s", f)
		}
	}
}

func TestProjectInitMakesTheProjectAndOnlyTheGivenConfig(t *testing.T) {
	root := realTempDir(t)
	withCwd(t, root)
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	// The config issue-cli reads (global --config) is never written.
	readCfg := filepath.Join(root, "projects.yaml")
	readBody := "projects:\n  - name: Other\n    issues: " + filepath.Join(root, "other") + "\n"
	mustWrite(t, readCfg, readBody)
	if err := os.MkdirAll(filepath.Join(root, "other"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "shop")
	sandbox := filepath.Join(root, "sandbox", "projects.yaml")

	out, err := runProjectCLI(t, "--config", readCfg, "project", "init", dir)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "projects.yaml: not touched (no --config)") || !strings.Contains(out, "issue-cli --config <your projects.yaml> --project shop lead spawn") {
		t.Errorf("no-config output:\n%s", out)
	}
	if got, _ := os.ReadFile(readCfg); string(got) != readBody {
		t.Errorf("read config changed:\n%s", got)
	}

	out, err = runProjectCLI(t, "--config", readCfg, "project", "init", dir, "--config", sandbox)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		filepath.Join(dir, "workflow.yaml") + ": already there, same as the template",
		sandbox + ": wrote a new file with project shop:",
		"git -C " + dir + " init",
		"issue-viewer -config " + sandbox + "   # then open /p/shop/company: the New department tile adds a department",
		"issue-cli --config " + sandbox + " --project shop lead spawn   # start the CEO",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if got, _ := os.ReadFile(readCfg); string(got) != readBody {
		t.Errorf("read config changed:\n%s", got)
	}

	// The new project works with the rest of issue-cli.
	out, err = runProjectCLI(t, "--config", sandbox, "--project", "shop", "create", "--title", "First")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}

	// A second run reports what is there and changes nothing.
	before, _ := os.ReadFile(sandbox)
	out, err = runProjectCLI(t, "--json", "project", "init", dir, "--config", sandbox)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var plan struct {
		Slug  string `json:"slug"`
		Files []struct{ Path, State string }
		Config struct {
			State string `json:"state"`
		} `json:"config"`
		DryRun bool     `json:"dry_run"`
		Next   []string `json:"next"`
	}
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if plan.Slug != "shop" || plan.Config.State != "there" || len(plan.Files) != 4 || len(plan.Next) == 0 {
		t.Errorf("plan = %+v", plan)
	}
	for _, f := range plan.Files {
		if f.State != "there" && f.State != "same" {
			t.Errorf("second run: %s %s", f.Path, f.State)
		}
	}
	if after, _ := os.ReadFile(sandbox); string(after) != string(before) {
		t.Error("second run changed the config")
	}
}

func TestProjectInitRefusesAWorkerOnItsOwnConfig(t *testing.T) {
	root := realTempDir(t)
	withCwd(t, root)
	cfg := filepath.Join(root, "projects.yaml")
	if err := os.MkdirAll(filepath.Join(root, "issues"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, cfg, "projects:\n  - name: Home\n    issues: "+filepath.Join(root, "issues")+"\n")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "engine")
	out, err := runProjectCLI(t, "--config", cfg, "project", "init", filepath.Join(root, "new"), "--config", cfg)
	if err == nil || !strings.Contains(err.Error(), "workers never add projects") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if got, _ := os.ReadFile(cfg); strings.Contains(string(got), "new") {
		t.Errorf("config changed:\n%s", got)
	}
	// A dry run and a sandbox config are fine.
	if out, err := runProjectCLI(t, "--config", cfg, "project", "init", filepath.Join(root, "new"), "--config", cfg, "--dry-run"); err != nil {
		t.Errorf("dry run: %v\n%s", err, out)
	}
	if out, err := runProjectCLI(t, "--config", cfg, "project", "init", filepath.Join(root, "new2"), "--config", filepath.Join(root, "sandbox.yaml")); err != nil {
		t.Errorf("sandbox: %v\n%s", err, out)
	}
}

func TestProjectUsage(t *testing.T) {
	withCwd(t, realTempDir(t))
	for _, args := range [][]string{{"project"}, {"project", "frob"}, {"project", "init"}} {
		if _, err := runProjectCLI(t, args...); err == nil || !strings.Contains(err.Error(), "usage: issue-cli project init") {
			t.Errorf("%v: err = %v", args, err)
		}
	}
}
