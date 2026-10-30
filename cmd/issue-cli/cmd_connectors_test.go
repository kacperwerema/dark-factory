package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/agentrun"
	"github.com/michal-franc/issue-viewer/internal/connectors"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// fakeConnectorValue stands in for a credential: it may only reach a
// connector's child process.
const fakeConnectorValue = "fake-secret-value-7f3a"

// connectorFixture is a project on disk whose Marketing department (system
// SEO) has a tool connector that writes its secret to <root>/out.txt, an
// mcp connector, and one whose secret is missing. ACME_WP holds the fake
// value; issue-cli finds the project through ISSUE_VIEWER_CONFIG.
func connectorFixture(t *testing.T) (root string) {
	t.Helper()
	f := newCallLogFixture(t, "backlog", "", "")
	root = f.root
	os.MkdirAll(filepath.Join(root, "issues", "SEO"), 0755)
	mustWrite(t, filepath.Join(root, "issues", "SEO", "page.md"), "---\ntitle: \"Page\"\nstatus: \"backlog\"\nsystem: \"SEO\"\n---\n\nBody.\n")
	cfg := os.Getenv("ISSUE_VIEWER_CONFIG")
	b, _ := os.ReadFile(cfg)
	mustWrite(t, cfg, string(b)+`    departments:
      - name: Marketing
        systems: [SEO]
        connectors:
          - name: wp
            kind: tool
            command: /bin/sh
            args: ["-c", "printf %s \"$WP_PASSWORD\" > `+filepath.Join(root, "out.txt")+`"]
            secrets:
              WP_PASSWORD: env:ACME_WP
            check: 'test -n "$WP_PASSWORD" && echo "$WP_PASSWORD"'
            sends: true
          - name: gsc
            kind: mcp
            command: gsc-mcp
            secrets:
              GSC_TOKEN: env:ACME_WP
          - name: qb
            kind: mcp
            command: qb-mcp
            secrets:
              QB_TOKEN: env:ACME_QB_MISSING
            check: "true"
`)
	t.Setenv("ACME_WP", fakeConnectorValue)
	t.Setenv("ACME_QB_MISSING", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	return root
}

func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	l := newCallLog(append([]string{"--project", "demo"}, args...), time.Now())
	inv := &invocation{}
	var out, errw strings.Builder
	err := runTraced(append([]string{"--project", "demo"}, args...), strings.NewReader(""), &out, &errw, inv)
	l.write(inv, err, false)
	if err != nil {
		return out.String() + errw.String() + err.Error(), err
	}
	return out.String() + errw.String(), nil
}

func TestConnectorsCommand(t *testing.T) {
	connectorFixture(t)
	out, err := runCLI(t, "connectors")
	if err != nil {
		t.Fatal(out)
	}
	for _, want := range []string{
		"== Marketing ==",
		"marketing/wp (tool) · sends · never checked (issue-cli connector check marketing/wp)",
		"  WP_PASSWORD env:ACME_WP set",
		"marketing/gsc (mcp) · no check:",
		"  QB_TOKEN env:ACME_QB_MISSING missing",
	} {
		assertContains(t, out, want)
	}
	out, _ = runCLI(t, "connectors", "marketing", "--json")
	var list []connectors.Status
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list) != 3 || !list[2].Missing {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if out, err := runCLI(t, "connectors", "ops"); err == nil || !strings.Contains(out, `no department "ops" (departments: marketing)`) {
		t.Errorf("unknown department: %s", out)
	}

	out, _ = runCLI(t, "department", "show", "marketing")
	assertContains(t, out, "Connectors:\n  == Marketing ==\n  marketing/wp (tool)")
}

func TestConnectorsCommandNothingConfigured(t *testing.T) {
	newCallLogFixture(t, "backlog", "", "")
	out, err := runCLI(t, "connectors")
	if err != nil {
		t.Fatal(out)
	}
	assertContains(t, out, "No connectors configured.")
	assertContains(t, out, "GSC_TOKEN: keychain:acme-gsc")
}

func TestConnectorExecScope(t *testing.T) {
	root := connectorFixture(t)
	outFile := filepath.Join(root, "out.txt")

	// Outside any agent session: refused, unless the owner says so.
	if out, err := runCLI(t, "connector", "exec", "marketing/wp"); err == nil || !strings.Contains(out, "add --as-owner in your own shell") {
		t.Fatalf("shell: %s", out)
	}
	if out, err := runCLI(t, "connector", "exec", "marketing/wp", "--as-owner"); err != nil {
		t.Fatalf("--as-owner: %s", out)
	}
	if b, _ := os.ReadFile(outFile); string(b) != fakeConnectorValue {
		t.Fatalf("the child got %q", b)
	}
	os.Remove(outFile)

	// A Product worker and the CEO are refused; Marketing's worker runs it.
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "cli/sample")
	if out, err := runCLI(t, "connector", "exec", "marketing/wp"); err == nil || !strings.Contains(out, "this connector belongs to Marketing; a Product agent may not use it. Ask Marketing's lead lead-demo-marketing") {
		t.Fatalf("Product worker: %s", out)
	}
	if out, err := runCLI(t, "connector", "exec", "marketing/wp", "--as-owner"); err == nil || !strings.Contains(out, "--as-owner is for the owner's own shell") {
		t.Fatalf("worker --as-owner: %s", out)
	}
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", tracker.LeadScopeProject)
	if out, err := runCLI(t, "connector", "exec", "marketing/wp"); err == nil || !strings.Contains(out, "the CEO does not use Marketing's connectors") {
		t.Fatalf("CEO: %s", out)
	}
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "seo/page")
	if out, err := runCLI(t, "connector", "exec", "marketing/wp"); err != nil {
		t.Fatalf("Marketing worker: %s", out)
	}
	if b, _ := os.ReadFile(outFile); string(b) != fakeConnectorValue {
		t.Fatalf("the child got %q", b)
	}

	// A tool runs the command after --, with its exit code.
	if _, err := runCLI(t, "connector", "exec", "marketing/wp", "--", "/bin/sh", "-c", "exit 4"); processExitCode(err) != 4 {
		t.Errorf("exit code = %d (%v)", processExitCode(err), err)
	}
	// A missing secret stops it before the run, naming the reference.
	if out, err := runCLI(t, "connector", "exec", "marketing/qb"); err == nil || !strings.Contains(out, "secret QB_TOKEN (env:ACME_QB_MISSING) is missing on this machine: the owner exports ACME_QB_MISSING") {
		t.Errorf("missing: %s", out)
	}
	if out, err := runCLI(t, "connector", "exec", "marketing/nope"); err == nil || !strings.Contains(out, "it has: marketing/wp, marketing/gsc, marketing/qb") {
		t.Errorf("unknown: %s", out)
	}
}

func TestConnectorCheck(t *testing.T) {
	root := connectorFixture(t)
	out, err := runCLI(t, "connector", "check", "marketing")
	if processExitCode(err) != 1 {
		t.Fatalf("want exit 1 with a missing secret: %v\n%s", err, out)
	}
	for _, want := range []string{"✓ marketing/wp ok", "· marketing/gsc no-check", "✗ marketing/qb missing (a secret is missing: issue-cli connectors marketing)", "file: " + filepath.Join(root, "issues", ".connectors", "checks.jsonl")} {
		assertContains(t, out, want)
	}
	out, _ = runCLI(t, "connectors")
	assertContains(t, out, "marketing/wp (tool) · sends · check ok just now")
	assertContains(t, out, "marketing/qb (mcp) · check missing just now")

	// Only the owner and the department's own lead check.
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "seo/page")
	if out, err := runCLI(t, "connector", "check", "marketing/wp"); err == nil || !strings.Contains(out, "connector checks are for the owner or Marketing's lead (lead-demo-marketing)") {
		t.Errorf("worker: %s", out)
	}
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "department:marketing")
	if out, err := runCLI(t, "connector", "check", "marketing/wp"); err != nil {
		t.Errorf("Marketing lead: %s", out)
	}
}

func TestConnectorsInShowAndBriefing(t *testing.T) {
	connectorFixture(t)
	out, err := runCLI(t, "show", "seo/page")
	if err != nil {
		t.Fatal(out)
	}
	assertContains(t, out, "Connectors: 3 (1 missing) — issue-cli connectors marketing")
	if out, _ := runCLI(t, "show", "cli/sample"); strings.Contains(out, "Connectors:") {
		t.Errorf("Product issue shows connectors:\n%s", out)
	}
}

// AC8: the fake value reaches the connector's child and nothing the engine
// writes or prints: issue files and sidecars, the checks log, the clilogs,
// the briefing, mcp.json, and every command's output.
func TestConnectorSecretNeverWritten(t *testing.T) {
	root := connectorFixture(t)
	var printed strings.Builder
	run := func(args ...string) {
		out, _ := runCLI(t, args...)
		printed.WriteString(out)
	}
	t.Setenv("ISSUE_CLI_LOG", filepath.Join(root, ".agent-logs", "agent-seo-page", "agent-seo-page.clilog"))
	run("connectors")
	run("connectors", "--json")
	run("department", "show", "marketing", "--json")
	run("connector", "check")
	run("connector", "exec", "marketing/wp", "--as-owner")
	run("health", "lint")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "seo/page")
	run("show", "seo/page")
	run("connector", "exec", "marketing/wp")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")

	// The briefing and the launch's mcp.json.
	proj := &tracker.Project{}
	projects, err := tracker.LoadProjects(os.Getenv("ISSUE_VIEWER_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	*proj = projects[0]
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	page := tracker.FindIssue(issues, "seo/page")
	printed.WriteString(tracker.DepartmentPromptSection(proj, page))
	printed.WriteString(tracker.DepartmentLeadBrief(nil, &proj.Departments[0], tracker.LeadBriefInput{Project: proj}))
	sessionDir := filepath.Join(root, ".agent-logs", "agent-seo-page")
	os.MkdirAll(sessionDir, 0755)
	flags := agentrun.ConnectorFlags(proj, agentrun.Spec{IssueSlug: "seo/page", AgentType: "claude"}, sessionDir, []string{"ISSUE_VIEWER_ISSUE_SLUG=seo/page"}, new([]agentrun.Step))
	if len(flags) != 2 {
		t.Fatalf("flags = %v", flags)
	}

	if b, _ := os.ReadFile(filepath.Join(root, "out.txt")); string(b) != fakeConnectorValue {
		t.Fatalf("the child did not get the secret: %q", b)
	}
	if strings.Contains(printed.String(), fakeConnectorValue) {
		t.Errorf("printed output holds the secret:\n%s", printed.String())
	}
	seen := 0
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path == filepath.Join(root, "out.txt") {
			return nil
		}
		seen++
		if b, _ := os.ReadFile(path); strings.Contains(string(b), fakeConnectorValue) {
			t.Errorf("%s holds the secret", path)
		}
		return nil
	})
	for _, want := range []string{filepath.Join(sessionDir, "mcp.json"), filepath.Join(root, "issues", ".connectors", "checks.jsonl"), filepath.Join(sessionDir, "agent-seo-page.clilog")} {
		if _, err := os.Stat(want); err != nil {
			t.Errorf("expected %s to be written and scanned: %v", want, err)
		}
	}
	if seen < 5 {
		t.Errorf("scanned only %d files", seen)
	}
}
