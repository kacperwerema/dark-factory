package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

func TestCheckDepartments(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	root := env.Project.Root()
	if err := os.MkdirAll(filepath.Join(root, "departments"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "departments", "hq.md"), []byte("CEO"), 0644); err != nil {
		t.Fatal(err)
	}
	env.Project.Company = &tracker.Company{Prompt: "departments/hq.md", Goals: []tracker.Goal{
		{ID: "clients"}, {ID: "top", RollsUp: "clients"},
	}}
	env.Project.Product = &tracker.Department{Name: "Product", Systems: []string{"Engine"}}
	env.Project.Departments = []tracker.Department{{Name: "Marketing", Prompt: "departments/marketing.md", Goals: []tracker.Goal{
		{ID: "pages", RollsUp: "clients", Source: "file:docs/run.json#pages"},
		{ID: "pages"},
		{ID: "posts", Period: "daily", Direction: "sideways", RollsUp: "revenue"},
		{ID: "leads", Source: "quickbooks"},
		{ID: "calls", Source: "file:run.json"},
		{},
	}}}
	var got []string
	for _, f := range checkDepartments(env) {
		got = append(got, f.Where+": "+f.Message)
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		"projects.yaml (company): goal top: rolls_up: on an HQ goal",
		"projects.yaml (Product): systems: on the Product entry is ignored",
		"projects.yaml (Marketing): prompt: departments/marketing.md does not exist",
		"goal pages: source file " + filepath.Join(root, "docs/run.json") + " does not exist",
		`goal id "pages" appears twice`,
		`goal posts: period "daily" is not week, month, quarter or none`,
		`goal posts: direction "sideways" is not up or down`,
		`goal posts: rolls_up: "revenue" names no HQ goal`,
		`goal leads: source "quickbooks" is not manual or file:<path>#<metric id>`,
		"goal calls: source file:run.json: want file:<path>#<metric id>",
		"a goal has no id:",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing finding %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "hq.md") {
		t.Errorf("an existing prompt file was flagged:\n%s", all)
	}
	if len(got) != 11 {
		t.Errorf("got %d findings, want 11:\n%s", len(got), all)
	}
}

// A goal reading a job's metrics file (<issues>/.goals/<dept>/<job>.json):
// a path naming no job of the department is a finding; a missing file names
// the job that writes it; an escape out of the issues folder is refused; a
// written one is clean.
func TestCheckDepartmentsJobFile(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	p := env.Project
	p.Departments = []tracker.Department{{Name: "Sales",
		Jobs: []tracker.Job{{ID: "weekly-funnel", Every: "week"}},
		Goals: []tracker.Goal{
			{ID: "visits", Source: "file:<issues>/.goals/sales/weekly-funnel.json#visits"},
			{ID: "signups", Source: "file:<issues>/.goals/sales/weekly-funnel.json#signups"},
			{ID: "paid", Source: "file:<issues>/.goals/sales/stripe-daily.json#paid"},
			{ID: "evil", Source: "file:<issues>/../secret.json#x"},
		}}}
	var got []string
	for _, f := range checkDepartments(env) {
		got = append(got, f.Message)
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		"goal paid: <issues>/.goals/sales/stripe-daily.json names no job sales/stripe-daily",
		"goals visits, signups: source file " + filepath.Join(p.IssueDir, ".goals", "sales", "weekly-funnel.json") + " does not exist (job sales/weekly-funnel writes it on its first run: issue-cli goal write sales/weekly-funnel <id>=<value>...)",
		"goal evil: <issues>/../secret.json climbs out of the issues folder",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing finding %q in:\n%s", want, all)
		}
	}
	file := filepath.Join(p.IssueDir, ".goals", "sales", "weekly-funnel.json")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"date":"2026-10-26","metrics":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, f := range checkDepartments(env) {
		if strings.Contains(f.Message, "weekly-funnel.json") {
			t.Errorf("a written job file is flagged: %s", f.Message)
		}
	}
}

func TestCheckDepartmentsNothingConfigured(t *testing.T) {
	if f := checkDepartments(lintProject(t, minimalWorkflow, nil)); len(f) != 0 {
		t.Errorf("findings without departments: %v", f)
	}
}

func TestCheckOutputs(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	p := env.Project
	p.Departments = []tracker.Department{{Name: "Marketing"}}
	old := env.Now.Add(-20 * 24 * time.Hour).UTC().Format(time.RFC3339)
	fresh := env.Now.Add(-time.Hour).UTC().Format(time.RFC3339)
	log := strings.Join([]string{
		`{"op":"add","id":"o-1","date":"` + old + `","department":"marketing","title":"Old post","kind":"post","url":"https://a.example"}`,
		`{"op":"add","id":"o-2","date":"` + fresh + `","department":"sales","title":"Lead list","kind":"report","url":"https://a.example/l"}`,
		`{"op":"add","id":"o-3","date":"` + fresh + `","department":"marketing","title":"Gone","kind":"page","path":"docs/gone.md"}`,
		`broken`,
	}, "\n") + "\n"
	path := outputs.LogPath(p)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(log), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputs.CommentsPath(p), []byte(`{"op":"reply","thread":"c-1","output":"o-1","date":"`+fresh+`","text":"x"}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range checkDepartments(env) {
		got = append(got, f.Where+": "+f.Message)
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		"outputs.jsonl:4: line skipped:",
		"comments.jsonl:1: comment line skipped: reply on unknown thread c-1",
		"output o-1 (Old post) has waited for review since",
		"output o-3 (Gone): docs/gone.md is gone",
		`1 output(s) of department "sales", which projects.yaml no longer has`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "o-2 (Lead list) has waited") || len(got) != 5 {
		t.Errorf("findings = %d:\n%s", len(got), all)
	}
}

// TestLintOutputsOfARemovedDepartmentAreHistory: outputs of a department
// projects.yaml no longer has are the department's record: a history line
// in the report, no warning, no score.
func TestLintOutputsOfARemovedDepartmentAreHistory(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	env.Project.Departments = []tracker.Department{{Name: "Sales"}}
	writeOutputs(t, env,
		`{"op":"add","id":"o-1","date":"2026-10-01T00:00:00Z","department":"marketing","title":"Post","kind":"post","url":"https://a.example","state":"approved"}`,
		`{"op":"add","id":"o-2","date":"2026-10-01T00:00:00Z","department":"marketing","title":"Page","kind":"post","url":"https://a.example/p","state":"approved"}`,
	)
	rep, err := Run(env, "departments")
	if err != nil {
		t.Fatal(err)
	}
	if has(rep.Findings, "marketing") {
		t.Errorf("outputs of a removed department warned: %+v", rep.Findings)
	}
	if len(rep.History) != 1 || !has(rep.History, `2 output(s) of department "marketing", which projects.yaml no longer has`) {
		t.Errorf("want one history line for marketing's 2 outputs: %+v", rep.History)
	}
}

func TestCheckDepartmentsJobs(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	root := env.Project.Root()
	if err := os.MkdirAll(filepath.Join(root, "jobs"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "jobs", "weekly.md"), []byte("brief"), 0644); err != nil {
		t.Fatal(err)
	}
	env.Project.Company = &tracker.Company{Jobs: []tracker.Job{{ID: "x", Every: "day", Brief: "jobs/weekly.md"}}}
	env.Project.Product = &tracker.Department{Name: "Product", Jobs: []tracker.Job{{ID: "y", Every: "day", Brief: "jobs/weekly.md"}, {ID: "nightly-improve", Every: "day", Brief: "builtin:nightly-improve"}, {ID: "z", Every: "day", Brief: "jobs/nope.md"}}}
	env.Project.Departments = []tracker.Department{{Name: "Marketing", Jobs: []tracker.Job{
		{ID: "weekly", Every: "week", On: "mon", Brief: "jobs/weekly.md"},
		{ID: "weekly", Every: "week", Brief: "jobs/weekly.md"},
		{ID: "hourly", Every: "hour", Brief: "jobs/weekly.md"},
		{ID: "nobrief", Every: "day"},
		{ID: "gone", Every: "month", On: "last", Brief: "jobs/gone.md"},
		{ID: "typed", Every: "day", Brief: "jobs/weekly.md", Type: "seo-pass"},
		{ID: "memory-consolidate", Every: "week", Brief: "builtin:memory-consolidate"},
		{ID: "nobuiltin", Every: "week", Brief: "builtin:no-such-brief"},
		{Every: "day"},
	}}}
	env.Issues = []*tracker.Issue{
		{Slug: "seo/old-run", Status: "draft", ExtraFields: []tracker.ExtraField{{Key: "job", Value: "marketing/old"}}},
		{Slug: "seo/ok-run", Status: "draft", ExtraFields: []tracker.ExtraField{{Key: "job", Value: "marketing/weekly"}}},
		{Slug: "seo/old-done", Status: "done", ExtraFields: []tracker.ExtraField{{Key: "job", Value: "marketing/old"}}},
	}
	var got []string
	for _, f := range checkDepartments(env) {
		got = append(got, f.Where+": "+f.Message)
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		"projects.yaml (company): jobs: on HQ are not run",
		// Product's jobs run (factory/nightly-self-improvement-loop-…#Q1):
		// checked like a department's, no "not run" warning.
		"projects.yaml (Product): job z: brief jobs/nope.md does not exist",
		`projects.yaml (Marketing): job id "weekly" appears twice`,
		`projects.yaml (Marketing): job hourly: every: "hour": day, week or month`,
		"projects.yaml (Marketing): job nobrief: no brief:",
		"projects.yaml (Marketing): job gone: brief jobs/gone.md does not exist",
		"projects.yaml (Marketing): a job has no id:",
		`projects.yaml (Marketing): job typed: type "seo-pass": the workflow defines no types`,
		"projects.yaml (Marketing): job nobuiltin: brief builtin:no-such-brief is not a bundled brief (bundled: builtin:decision-drift, builtin:growth-scout, builtin:lead-flush, builtin:memory-consolidate, builtin:nightly-improve, builtin:retro-sweep)",
		`seo/old-run: open run of job "marketing/old", which projects.yaml no longer has`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if len(got) != 10 {
		t.Errorf("want 10 findings, got %d:\n%s", len(got), all)
	}
}

func TestCheckDepartmentsConnectors(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	env.Project.Company = &tracker.Company{Connectors: []tracker.Connector{{Name: "bank", Kind: "tool"}}}
	env.Project.Product = &tracker.Department{Name: "Product", Connectors: []tracker.Connector{{Name: "gh", Kind: "tool"}}}
	env.Project.Departments = []tracker.Department{{Name: "Marketing", Connectors: []tracker.Connector{
		{Name: "gsc", Kind: "mcp", Command: "gsc-mcp", Secrets: map[string]string{"GSC_TOKEN": "keychain:acme-gsc"}, Optional: []string{"GSC_TOKEN", "GSC_QUOTA"}},
		{Name: "gsc", Kind: "tool"},
		{Name: "Search Console", Kind: "api"},
		{Name: "wp", Kind: "mcp", Secrets: map[string]string{"WP_PASSWORD": "pasted-value-1234", "WP_USER": "vault:x"}},
		{Kind: "tool"},
	}}}
	var got []string
	for _, f := range checkDepartments(env) {
		got = append(got, f.Where+": "+f.Message)
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		"projects.yaml (company): connectors: on company: are ignored",
		"projects.yaml (Product): connectors: on the Product entry are ignored",
		`projects.yaml (Marketing): connector "gsc" appears twice`,
		`connector name "Search Console" is not lower-case letters, digits and -`,
		`connector Search Console: kind "api" is not mcp, tool or rest`,
		"connector wp: an mcp connector needs command: or url:",
		"connector wp: secret WP_PASSWORD: not env:<VAR> or keychain:<service>: it looks like a secret value",
		"connector wp: secret WP_USER: not env:<VAR> or keychain:<service>",
		"a connector has no name:",
		"connector gsc: optional GSC_QUOTA is not one of its secrets:",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing finding %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "pasted-value-1234") {
		t.Errorf("a finding quotes the raw value:\n%s", all)
	}
	if len(got) != 10 {
		t.Errorf("got %d findings, want 10:\n%s", len(got), all)
	}
}

func TestCheckDepartmentsHTTPConnectors(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	env.Project.Departments = []tracker.Department{{Name: "Marketing", Connectors: []tracker.Connector{
		{Name: "ok", Kind: "mcp", URL: "https://mcp.example.com/mcp", Headers: map[string]string{"Authorization": "Bearer keychain:acme-seo"}},
		{Name: "local", Kind: "mcp", URL: "http://localhost:3011/mcp"},
		{Name: "lan", Kind: "mcp", URL: "http://192.168.1.4/mcp"},
		{Name: "both", Kind: "mcp", Command: "x", URL: "https://a.example/mcp"},
		{Name: "tool", Kind: "tool", URL: "https://a.example/mcp"},
		{Name: "env", Kind: "mcp", URL: "https://a.example/mcp", Secrets: map[string]string{"K": "env:K"}, Headers: map[string]string{"X-Key": "Bearer pasted-value-5678"}},
		{Name: "stdio", Kind: "mcp", Command: "x", Headers: map[string]string{"X-Key": "env:K"}},
	}}}
	var got []string
	for _, f := range checkDepartments(env) {
		got = append(got, f.Message)
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		"connector lan: url: http is allowed only on this machine",
		"connector both: give command: or url:, not both",
		"connector tool: url: is only for kind: mcp or rest",
		"connector env: secrets: are unused with url:",
		"connector env: header X-Key: not env:<VAR> or keychain:<service>: it looks like a secret value",
		"connector stdio: headers: are only for a connector with url:",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing finding %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "pasted-value-5678") || strings.Contains(all, "connector ok") || strings.Contains(all, "connector local") {
		t.Errorf("unexpected finding:\n%s", all)
	}
	if len(got) != 6 {
		t.Errorf("got %d findings, want 6:\n%s", len(got), all)
	}
}

func TestCheckDepartmentsRestConnectors(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	env.Project.Departments = []tracker.Department{{Name: "Marketing", Connectors: []tracker.Connector{
		{Name: "ok", Kind: "rest", URL: "http://127.0.0.1:4007/api", Get: []string{"/public/v1/analytics/post/*", "/public/v1/posts"},
			Headers: map[string]string{"Authorization": "keychain:postiz"}},
		{Name: "nourl", Kind: "rest", Get: []string{"/a"}},
		{Name: "cmd", Kind: "rest", URL: "http://127.0.0.1:1/api", Command: "curl", Get: []string{"/a"}},
		{Name: "noget", Kind: "rest", URL: "http://127.0.0.1:1/api"},
		{Name: "bad", Kind: "rest", URL: "http://127.0.0.1:1/api", Get: []string{"a/b", "/a/../b", "//evil/x", "/a?x=1", "/[", "/ok", "/ok"}},
		{Name: "tool", Kind: "tool", Command: "curl", Get: []string{"/a"}},
		{Name: "extras", Kind: "rest", URL: "http://127.0.0.1:1/api", Get: []string{"/a"}, DenyTools: []string{"x"},
			NeedsApprovedOutput: true, ReadTools: []string{"x"}, At: map[string]tracker.ConnectorAt{"backlog": {Allow: []string{"x"}}}},
	}}}
	var got []string
	for _, f := range checkDepartments(env) {
		got = append(got, f.Message)
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		"connector nourl: a rest connector needs url:",
		"connector cmd: a rest connector runs no command:",
		"connector noget: a rest connector needs get:",
		`connector bad: get: "a/b" must start with /`,
		`connector bad: get: "/a/../b" must be a clean path`,
		`connector bad: get: "//evil/x" must be a path, not //host`,
		`connector bad: get: "/a?x=1" is a path only`,
		`connector bad: get: "/[" is not a valid pattern`,
		"connector bad: get: /ok appears twice",
		"connector tool: get: is only for kind: rest",
		"connector extras: deny_tools: does nothing on a kind: rest connector",
		"connector extras: read_tools: does nothing on a rest connector",
		"connector extras: at: does nothing on a rest connector",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing finding %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "connector ok") {
		t.Errorf("the clean rest connector has findings:\n%s", all)
	}
}

// A get: whose first segment is a wildcard warns, with a fix naming the
// first segment; one deeper down (Postiz's per-channel entry) does not.
func TestCheckDepartmentsRestWidePatterns(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	env.Project.Departments = []tracker.Department{{Name: "Marketing", Connectors: []tracker.Connector{
		{Name: "analytics", Kind: "rest", URL: "http://127.0.0.1:4007/api", Get: []string{"/public/v1/analytics/post/*", "/public/v1/analytics/*", "/public/v1/posts", "/public/v1/integrations"}},
		{Name: "wide", Kind: "rest", URL: "http://127.0.0.1:1/api", Get: []string{"/*", "/*/status", "/ok/*"}},
		{Name: "tool", Kind: "tool", Command: "x"},
	}}}
	byMsg := map[string]string{}
	for _, f := range checkDepartments(env) {
		byMsg[f.Message] = f.Fix
	}
	for g, fix := range map[string]string{"/*": "get: /<resource>/*", "/*/status": "get: /<resource>/status"} {
		msg := "connector wide: get: " + g + " opens every route at the API's first level, including any that changes state on GET"
		got, ok := byMsg[msg]
		if !ok {
			t.Errorf("no warning %q in %v", msg, byMsg)
		} else if !strings.Contains(got, fix) {
			t.Errorf("%s: fix %q doesn't name %q", g, got, fix)
		}
	}
	for msg := range byMsg {
		if strings.Contains(msg, "connector analytics") || strings.Contains(msg, "/ok/*") {
			t.Errorf("narrow pattern warned: %s", msg)
		}
	}
}

// docs/departments.md's get row names the wide-pattern warning.
func TestDocsNameTheWideRestPatternWarning(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "departments.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "whose first segment is a wildcard") {
		t.Error("docs/departments.md#connectors doesn't say lint warns on a get: whose first segment is a wildcard")
	}
}

func TestCheckDepartmentsFileGates(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	root := env.Project.Root()
	wf, err := workflowupgrade.ReadTemplate("marketing")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "workflows"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", "growth.yaml"), wf, 0644); err != nil {
		t.Fatal(err)
	}
	env.Project.Departments = []tracker.Department{{Name: "Growth Team", Workflow: "workflows/growth.yaml"}}
	all := func() string {
		var got []string
		for _, f := range checkDepartments(env) {
			got = append(got, f.Where+": "+f.Message+" | "+f.Fix)
		}
		return strings.Join(got, "\n")
	}
	got := all()
	for _, want := range []string{
		"projects.yaml (Growth Team): departments/growth-team/brand.md is not written yet: brief → draft refuses until it is (file_exists)",
		"departments/growth-team/icp.md is not written yet",
		"issue-cli create --department growth-team --type foundation",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// market.md is the market-size type's own publish → done gate: no warning.
	if strings.Contains(got, "market.md") {
		t.Errorf("market size gates nothing before channel work, got:\n%s", got)
	}
	// So is keywords.md for keyword-research: briefs read it, never wait for it.
	if strings.Contains(got, "keywords.md") {
		t.Errorf("keyword research gates nothing before channel work, got:\n%s", got)
	}

	dir := filepath.Join(root, "departments", "growth-team")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"brand.md", "icp.md"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("# settled\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if got := all(); strings.Contains(got, "not written yet") {
		t.Errorf("with brand.md and icp.md written there is nothing to warn about, got:\n%s", got)
	}
}

// A type's own file_exists (competitors.md, keywords.md) counts only once an
// issue of that type in the department has run to its owner review: a
// department that never ran a scan hears nothing, one waiting on the owner
// hears what its publish will write.
func TestFileGateWarnsForATypeOnlyPastOwnerReview(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	root := env.Project.Root()
	wf, err := workflowupgrade.ReadTemplate("marketing")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "workflows"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", "growth.yaml"), wf, 0644); err != nil {
		t.Fatal(err)
	}
	env.Project.Departments = []tracker.Department{{Name: "Growth Team", Workflow: "workflows/growth.yaml", Systems: []string{"SEO"}}}
	dir := filepath.Join(root, "departments", "growth-team")
	write := func(name string) {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte("# settled\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("brand.md")
	write("icp.md")
	scan := &tracker.Issue{Slug: "seo/competitor-scan-plumbing", Title: "Competitor scan: plumbing", Type: "competitor-scan", System: "SEO"}
	other := &tracker.Issue{Slug: "elsewhere/competitor-scan", Type: "competitor-scan", Status: "owner review", System: "Product"}
	env.Issues = []*tracker.Issue{scan, other}
	all := func() string {
		var got []string
		for _, f := range checkDepartments(env) {
			got = append(got, f.Where+": "+f.Message+" | "+f.Fix)
		}
		return strings.Join(got, "\n")
	}

	for _, status := range []string{"research", "competitors", "parked"} {
		scan.Status = status
		if got := all(); strings.Contains(got, "competitors.md") {
			t.Errorf("a scan at %s hasn't reached the owner: no warning, got:\n%s", status, got)
		}
	}
	for _, status := range []string{"owner review", "publish", "done"} {
		scan.Status = status
		got := all()
		want := "projects.yaml (Growth Team): departments/growth-team/competitors.md is not written yet: seo/competitor-scan-plumbing (competitor-scan) has run to " + status
		if !strings.Contains(got, want) || !strings.Contains(got, "issue-cli outputs --issue seo/competitor-scan-plumbing") {
			t.Errorf("a scan at %s: want %q and the fix in:\n%s", status, want, got)
		}
		if strings.Contains(got, "elsewhere/") {
			t.Errorf("an issue outside the department counts:\n%s", got)
		}
		if strings.Contains(got, "keywords.md") || strings.Contains(got, "market.md") {
			t.Errorf("no keyword-research or market-size ran: no warning for their files, got:\n%s", got)
		}
	}
	write("competitors.md")
	if got := all(); strings.Contains(got, "not written yet") {
		t.Errorf("with competitors.md written there is nothing to warn about, got:\n%s", got)
	}

	// The same rule holds for keyword-research's keywords.md.
	env.Issues = []*tracker.Issue{{Slug: "seo/keywords", Type: "keyword-research", Status: "owner review", System: "SEO"}}
	if got := all(); !strings.Contains(got, "departments/growth-team/keywords.md is not written yet: seo/keywords (keyword-research) has run to owner review") {
		t.Errorf("keyword-research at owner review: want its keywords.md warning, got:\n%s", got)
	}
}

func TestCheckDepartmentsGroups(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	env.Project.Company = &tracker.Company{Goals: []tracker.Goal{{ID: "revenue"}}}
	env.Project.Product = &tracker.Department{Name: "Product", Parent: "gtm"}
	env.Project.Departments = []tracker.Department{
		{Name: "GTM", Goals: []tracker.Goal{{ID: "pipeline", RollsUp: "revenue"}}},
		{Name: "Marketing", Parent: "gtm", Goals: []tracker.Goal{{ID: "pages", RollsUp: "pipeline"}, {ID: "posts", RollsUp: "revenue"}, {ID: "x", RollsUp: "nope"}}},
		{Name: "Sales", Parent: "marketing"},
		{Name: "Ops", Parent: "finance"},
		{Name: "Self", Parent: "self"},
		{Name: "Odd", Parent: "Product"},
		{Name: "LoopA", Parent: "loopb"},
		{Name: "LoopB", Parent: "loopa"},
		{Name: "Z"}, {Name: "Y", Parent: "z"}, {Name: "X", Parent: "y"}, {Name: "W", Parent: "x"}, {Name: "V", Parent: "w"},
	}
	var got []string
	for _, f := range checkDepartments(env) {
		got = append(got, f.Where+": "+f.Message)
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		"projects.yaml (Product): parent: on the Product entry is ignored",
		`projects.yaml (Marketing): goal x: rolls_up: "nope" names no GTM goal and no HQ goal`,
		"projects.yaml (LoopA): parent: chain loops (LoopA → LoopB → LoopA): LoopA is treated as in no group",
		"projects.yaml (LoopB): parent: chain loops (LoopB → LoopA → LoopB)",
		"projects.yaml (V): parent: chain is too deep (V → W → X → Y → Z): groups nest at most 3 deep",
		`projects.yaml (Ops): parent: "finance" names no department`,
		"projects.yaml (Self): parent: names the department itself",
		"projects.yaml (Odd): parent: Product is built in and never a group",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing finding %q in:\n%s", want, all)
		}
	}
	// Sales under Marketing under GTM is a good nested group, and W sits
	// exactly 3 deep.
	if len(got) != 8 {
		t.Errorf("got %d findings, want 8 (pages→pipeline, posts→revenue, Sales and W are fine):\n%s", len(got), all)
	}
}

func TestCheckDepartmentsBudget(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	env.Project.Company = &tracker.Company{Budget: &tracker.Budget{Err: "budget: period \"day\": use week or month"}}
	env.Project.Product = &tracker.Department{Name: "Product", Budget: &tracker.Budget{USD: 5, Period: "week"}}
	env.Project.Departments = []tracker.Department{
		{Name: "Marketing", Budget: &tracker.Budget{Err: "budget: is not a mapping"}},
		{Name: "Sales", Budget: &tracker.Budget{Tokens: 100, Period: "month"}},
	}
	var got []string
	for _, f := range checkDepartments(env) {
		if strings.Contains(f.Message, "budget") {
			got = append(got, f.Where+": "+f.Message+" | "+f.Fix)
		}
	}
	if len(got) != 2 || !strings.Contains(got[0], "(company): budget: period") || !strings.Contains(got[1], "(Marketing): budget: is not a mapping") || !strings.Contains(got[1], "{usd: 50, period: week}") {
		t.Errorf("budget findings:\n%s", strings.Join(got, "\n"))
	}
}

// The OpenSEO connector from the spike report (shelf output 2262) as the
// owner would add it: lint names its sending and spending tools until they
// are denied or the connector is sends: true; deny_tools: mistakes too.
func TestCheckDepartmentsConnectorDenyTools(t *testing.T) {
	openseo := func() tracker.Connector {
		return tracker.Connector{Name: "openseo", Kind: "mcp", Command: "npx",
			Args:     []string{"-y", "mcp-remote@0.1.0", "http://localhost:3011/mcp", "--transport", "http-only", "--allow-http", "--header", "Authorization: Bearer ${OPENSEO_API_KEY}"},
			Secrets:  map[string]string{"OPENSEO_API_KEY": "keychain:openseo-api-key"},
			Optional: []string{"OPENSEO_API_KEY"},
			Check:    "curl -fsS http://localhost:3011/api/health"}
	}
	run := func(cs ...tracker.Connector) string {
		env := lintProject(t, minimalWorkflow, nil)
		env.Project.Departments = []tracker.Department{{Name: "Marketing", Connectors: cs}}
		var got []string
		for _, f := range checkDepartments(env) {
			got = append(got, f.Where+": "+f.Message)
		}
		return strings.Join(got, "\n")
	}

	plain := openseo()
	if got, want := run(plain), "projects.yaml (Marketing): connector openseo: tools that send or spend are on: submit_indexnow_urls, set_report_sharing, set_ai_visibility_schedule; add them to deny_tools:"; !strings.Contains(got, want) {
		t.Errorf("missing %q in:\n%s", want, got)
	}

	part := openseo()
	part.DenyTools = []string{"submit_indexnow_urls"}
	if got := run(part); !strings.Contains(got, "are on: set_report_sharing, set_ai_visibility_schedule;") || strings.Contains(got, "on: submit_indexnow_urls") {
		t.Errorf("partly denied:\n%s", got)
	}

	denied := openseo()
	denied.DenyTools = []string{"submit_indexnow_urls", "set_report_sharing", "set_ai_visibility_schedule"}
	sends := openseo()
	sends.Sends = true
	unknown := openseo()
	unknown.Name, unknown.Args = "gsc", []string{"-y", "@example/gsc-mcp"}
	if got := run(denied, sends, unknown); strings.Contains(got, "send or spend") {
		t.Errorf("denied, sends: true or unknown still warned:\n%s", got)
	}

	// Matched by its args when the owner names it differently.
	renamed := openseo()
	renamed.Name, renamed.Args = "seo", []string{"-y", "openseo-mcp"}
	if got := run(renamed); !strings.Contains(got, "connector seo: tools that send or spend are on") {
		t.Errorf("renamed:\n%s", got)
	}

	// A remote (url:) connector the owner named "seo-data": matched by its url.
	remote := tracker.Connector{Name: "seo-data", Kind: "mcp", URL: "https://mcp.openseo.so/mcp"}
	if got := run(remote); !strings.Contains(got, "connector seo-data: tools that send or spend are on") {
		t.Errorf("remote:\n%s", got)
	}

	bad := openseo()
	bad.DenyTools = []string{"submit_indexnow_urls", "set_report_sharing", "set_ai_visibility_schedule", "rm -rf", "set_report_sharing"}
	tool := tracker.Connector{Name: "wp", Kind: "tool", Command: "wp", DenyTools: []string{"post"}}
	got := run(bad, tool)
	for _, want := range []string{
		`connector openseo: deny_tools: "rm -rf" is not a tool name`,
		"connector openseo: deny_tools: set_report_sharing appears twice",
		"connector wp: deny_tools: does nothing on a kind: tool connector",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// A system named after a department doesn't put the issue in it: the
// competitor scan was filed as system: Marketing and walked Product's
// workflow, with Marketing's systems: listing only SEO.
func TestCheckDepartmentsUnfiledIssue(t *testing.T) {
	env := lintProject(t, minimalWorkflow, map[string]string{
		"Marketing/scan.md":   "---\ntitle: scan\nstatus: idea\nsystem: Marketing\ncreated: 2026-10-01\n---\n",
		"Marketing/filed.md":  "---\ntitle: filed\nstatus: idea\nsystem: Marketing\ndepartment: Marketing\ncreated: 2026-10-01\n---\n",
		"Marketing/closed.md": "---\ntitle: closed\nstatus: done\nsystem: Marketing\ncreated: 2026-10-01\n---\n",
		"SEO/page.md":         "---\ntitle: page\nstatus: idea\nsystem: SEO\ncreated: 2026-10-01\n---\n",
		"Sales/listed.md":     "---\ntitle: listed\nstatus: idea\nsystem: Sales\ncreated: 2026-10-01\n---\n",
		"CLI/tool.md":         "---\ntitle: tool\nstatus: idea\nsystem: CLI\ncreated: 2026-10-01\n---\n",
		"Product/own.md":      "---\ntitle: own\nstatus: idea\nsystem: Product\ncreated: 2026-10-01\n---\n",
	})
	env.Project.Departments = []tracker.Department{
		{Name: "Marketing", Systems: []string{"SEO", "Sales"}},
		{Name: "Sales"},
	}
	env.Project.Product = &tracker.Department{Name: tracker.ProductName}
	var hits []Finding
	for _, f := range checkDepartments(env) {
		if strings.Contains(f.Message, "names a department") {
			hits = append(hits, f)
		}
	}
	if len(hits) != 1 || hits[0].Where != "marketing/scan" {
		t.Fatalf("want only marketing/scan flagged (not filed, closed, listed, Product's own): %+v", hits)
	}
	if want := "issue-cli set-meta marketing/scan --key department --value Marketing"; !strings.Contains(hits[0].Fix, want) {
		t.Errorf("fix %q does not name %q", hits[0].Fix, want)
	}
}

// A growth-scout job needs its runs to be reports: lint warns, with the fix,
// when the department's workflow has no report type, though the job sets no
// type: (factory/growth-scout-…, AC3).
func TestLintGrowthScoutNeedsReportType(t *testing.T) {
	typed := minimalWorkflow + "default_type: piece\ntypes:\n  piece:\n    path: [idea, done]\n"
	for _, tc := range []struct {
		name, workflow string
		want           bool
	}{
		{"no types", minimalWorkflow, true},
		{"no report type", typed, true},
		{"report type", typed + "  report:\n    path: [idea, done]\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := lintProject(t, tc.workflow, nil)
			env.Project.Departments = []tracker.Department{{Name: "Marketing", Jobs: []tracker.Job{
				{ID: "growth-scout", Every: "month", Brief: "builtin:growth-scout"},
			}}}
			var all []string
			for _, f := range checkDepartments(env) {
				all = append(all, f.Where+": "+f.Message+" | "+f.Fix)
			}
			got := strings.Join(all, "\n")
			found := strings.Contains(got, "job growth-scout: builtin:growth-scout runs are type report: the workflow has no report type") &&
				strings.Contains(got, "add the type to the Marketing workflow's types:")
			if found != tc.want || (!tc.want && got != "") {
				t.Errorf("findings:\n%s", got)
			}
		})
	}
}

func TestConnectorPerRunFindings(t *testing.T) {
	got := connectorFindings(tracker.Department{Connectors: []tracker.Connector{
		{Name: "heavy", Kind: tracker.ConnectorTool, PerRun: &tracker.PerRun{Start: "up", Timeout: -1}},
		{Name: "fine", Kind: tracker.ConnectorTool, PerRun: &tracker.PerRun{Start: "up", Ready: "probe", Stop: "down", Tool: "analytics.v2"}},
		{Name: "shared", Kind: tracker.ConnectorTool, PerRun: &tracker.PerRun{Start: "up", Ready: "probe", Stop: "down", Tool: "../Analytics"}},
	}})
	want := []string{
		"connector heavy: per_run: needs ready:, stop:: connector exec refuses it until then",
		"connector heavy: per_run: timeout: is seconds, above 0",
		"connector shared: per_run: tool: must be a short id ([a-z0-9._-], starting with a letter or digit, up to 64): connector exec refuses it until then",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("findings = %q", got)
	}
}

// needs_approved_output: on a command: mcp server holds the whole server,
// and read_tools: does nothing without the field or url:, or with a name
// that isn't a tool's.
func TestLintApprovedOutputConnector(t *testing.T) {
	run := func(cs ...tracker.Connector) string {
		env := lintProject(t, minimalWorkflow, nil)
		env.Project.Departments = []tracker.Department{{Name: "Marketing", Connectors: cs}}
		var got []string
		for _, f := range checkDepartments(env) {
			got = append(got, f.Message)
		}
		return strings.Join(got, "\n")
	}
	good := tracker.Connector{Name: "postiz", Kind: "mcp", URL: "https://postiz.example.com/mcp", Sends: true, NeedsApprovedOutput: true, ReadTools: []string{"list_integrations"}}
	tool := tracker.Connector{Name: "wp", Kind: "tool", Command: "wp", NeedsApprovedOutput: true}
	if got := run(good, tool); strings.Contains(got, "needs_approved_output") || strings.Contains(got, "read_tools") {
		t.Errorf("a url: server with read_tools: and a held tool warned:\n%s", got)
	}
	stdio := tracker.Connector{Name: "poster", Kind: "mcp", Command: "poster-mcp", NeedsApprovedOutput: true, ReadTools: []string{"list"}}
	got := run(stdio)
	for _, want := range []string{
		"connector poster: needs_approved_output: on a command: mcp server holds the whole server",
		"connector poster: read_tools: does nothing without url:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	loose := tracker.Connector{Name: "seo", Kind: "mcp", URL: "https://seo.example.com/mcp", ReadTools: []string{"look", "bad name", "look"}}
	got = run(loose)
	for _, want := range []string{
		"connector seo: read_tools: does nothing without needs_approved_output: true",
		`connector seo: read_tools: "bad name" is not a tool name`,
		"connector seo: read_tools: look appears twice",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// TestLintConnectorAtNamesItsWorkflowStatuses: an at: status is checked
// against the department's own workflow file (not Product's): one it
// doesn't have warns, naming its statuses; at: without url:, an entry that
// limits nothing and names that aren't tool names warn too.
func TestLintConnectorAtNamesItsWorkflowStatuses(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	wf := filepath.Join(env.Project.Root(), "marketing.yaml")
	if err := os.WriteFile(wf, []byte("statuses:\n  - name: draft\n  - name: publish\n  - name: measure\n  - name: done\n"), 0644); err != nil {
		t.Fatal(err)
	}
	lint := func(cs ...tracker.Connector) string {
		env.Project.Departments = []tracker.Department{{Name: "Marketing", Workflow: wf, Connectors: cs}}
		var got []string
		for _, f := range checkDepartments(env) {
			got = append(got, f.Message)
		}
		return strings.Join(got, "\n")
	}
	good := tracker.Connector{Name: "postiz", Kind: "mcp", URL: "https://postiz.example.com/mcp", Sends: true, At: map[string]tracker.ConnectorAt{
		"draft":   {Allow: []string{"list_integrations", "create_post"}, DenyArgs: map[string]map[string][]string{"create_post": {"type": {"now"}}}},
		"publish": {Allow: []string{"create_post"}},
	}}
	if got := lint(good); strings.Contains(got, "at:") {
		t.Errorf("a good at: block warned:\n%s", got)
	}
	bad := tracker.Connector{Name: "postiz", Kind: "mcp", URL: "https://postiz.example.com/mcp", At: map[string]tracker.ConnectorAt{
		"idea":    {Allow: []string{"create_post"}},
		"publish": {},
		"measure": {Allow: []string{"bad name", "stats"}, DenyArgs: map[string]map[string][]string{"create_post": {"type": {"now"}}}},
	}}
	stdio := tracker.Connector{Name: "poster", Kind: "mcp", Command: "poster-mcp", At: map[string]tracker.ConnectorAt{"draft": {Allow: []string{"list"}}}}
	got := lint(bad, stdio)
	for _, want := range []string{
		"connector postiz: at: idea is not a status of Marketing's workflow (draft, publish, measure, done): that entry never applies",
		"connector postiz: at: publish has neither allow: nor deny_args:",
		`connector postiz: at: measure: allow: "bad name" is not a tool name`,
		"connector postiz: at: measure: deny_args: create_post is not in allow:",
		"connector poster: at: does nothing without url:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "at: draft is not a status") {
		t.Errorf("a status of the department's file warned:\n%s", got)
	}
}

// TestLintDenyArgsPath (AC3): a deny_args: argument written as a path is
// checked for its syntax; names, well-formed paths and a plain key with
// other characters pass, a malformed path warns naming the key and that
// the bridge refuses the tool until it is fixed.
func TestLintDenyArgsPath(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	wf := filepath.Join(env.Project.Root(), "marketing.yaml")
	if err := os.WriteFile(wf, []byte("statuses:\n  - name: publish\n  - name: done\n"), 0644); err != nil {
		t.Fatal(err)
	}
	lint := func(args map[string][]string) string {
		c := tracker.Connector{Name: "social", Kind: "mcp", URL: "http://127.0.0.1:4007/api/mcp", Sends: true, At: map[string]tracker.ConnectorAt{
			"publish": {Allow: []string{"schedule"}, DenyArgs: map[string]map[string][]string{"schedule": args}},
		}}
		env.Project.Departments = []tracker.Department{{Name: "Marketing", Workflow: wf, Connectors: []tracker.Connector{c}}}
		var got []string
		for _, f := range checkDepartments(env) {
			got = append(got, f.Message)
		}
		return strings.Join(got, "\n")
	}
	if got := lint(map[string][]string{"type": {"now"}, "socialPost[].type": {"now"}, "a.b[][].c-d": {"x"}, "my key": {"x"}}); strings.Contains(got, "deny_args") {
		t.Errorf("good keys warned:\n%s", got)
	}
	got := lint(map[string][]string{"socialPost[.type": {"now"}, "a..b": {"x"}, "[]": {"x"}, "a[0].b": {"x"}})
	for _, want := range []string{
		`connector social: at: publish: deny_args: schedule: "socialPost[.type" is not an argument path (name, name.sub, name[].sub`,
		`deny_args: schedule: "a..b" is not an argument path`,
		`deny_args: schedule: "[]" is not an argument path`,
		`deny_args: schedule: "a[0].b" is not an argument path`,
		"until it is fixed the bridge refuses every call of schedule",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestCheckDepartmentsSpendConnectors(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	env.Project.Departments = []tracker.Department{{Name: "Video", Connectors: []tracker.Connector{
		{Name: "kling", Kind: "tool", Command: "kling", Spend: &tracker.ConnectorSpend{Meter: "json", Field: "creditsConsumed", Unit: "credits", Cap: 500}},
		{Name: "tts", Kind: "tool", URL: "https://api.elevenlabs.io", Headers: map[string]string{"xi-api-key": "keychain:elevenlabs-api"},
			Spend:  &tracker.ConnectorSpend{Meter: "elevenlabs", Unit: "characters", Cap: 1000},
			Voices: []tracker.Voice{{Placement: "narrator", VoiceID: "V1", RealPerson: "the owner", Consent: "2026-11-21"}, {Placement: "Narrator", VoiceID: "V2"}, {Placement: "actor", RealPerson: "an actor"}, {VoiceID: "V4"}}},
		{Name: "nocap", Kind: "tool", Command: "kling", Spend: &tracker.ConnectorSpend{Meter: "json", Field: "creditsConsumed"}},
		{Name: "nofield", Kind: "tool", Command: "kling", Spend: &tracker.ConnectorSpend{Meter: "json", Cap: 1}},
		{Name: "meter", Kind: "tool", Command: "x", Spend: &tracker.ConnectorSpend{Meter: "usd", Cap: 1}},
		{Name: "mcp", Kind: "mcp", Command: "x", Spend: &tracker.ConnectorSpend{Meter: "json", Field: "c", Cap: 1}},
		{Name: "ttscmd", Kind: "tool", Command: "x", Spend: &tracker.ConnectorSpend{Meter: "elevenlabs", Cap: -1}},
		{Name: "loose", Kind: "tool", Command: "x", Voices: []tracker.Voice{{Placement: "a", VoiceID: "V"}}},
	}}}
	var got []string
	for _, f := range checkDepartments(env) {
		got = append(got, f.Message)
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		"connector nocap: spend: has no cap: every billing call is refused until the owner writes one (spend: cap:, units a month)",
		"connector nofield: spend: meter: json needs field:",
		`connector meter: spend: meter: "usd" is not json or elevenlabs`,
		"connector mcp: spend: is only for kind: tool",
		"connector ttscmd: a meter: elevenlabs connector runs no command:",
		"connector ttscmd: spend: cap: is an amount a month, above 0",
		"connector loose: voices: are only read by a spend: meter: elevenlabs connector",
		"connector tts: voice placement Narrator appears twice",
		"connector tts: voice actor has no voice_id:",
		"connector tts: voice actor is an actor's, a real person's, with no consent: noted",
		"connector tts: a voice has no placement:",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing finding %q in:\n%s", want, all)
		}
	}
	for _, clean := range []string{"connector kling:", "connector tts: url:", "connector tts: headers:", "voice narrator is"} {
		if strings.Contains(all, clean) {
			t.Errorf("a finding on a good config (%q) in:\n%s", clean, all)
		}
	}
}
