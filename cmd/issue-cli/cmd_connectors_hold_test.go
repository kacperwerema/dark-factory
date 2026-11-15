package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/connectors/connectortest"
	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/standin"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// holdProject is the fixture's project as loaded from its projects.yaml,
// for putting outputs on its shelf.
func holdProject(t *testing.T) *tracker.Project {
	t.Helper()
	projects, err := tracker.LoadProjects(os.Getenv("ISSUE_VIEWER_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	return &projects[0]
}

// shelfOutput adds a file output for seo/page, in review.
func shelfOutput(t *testing.T, root string) (*tracker.Project, *outputs.Output) {
	t.Helper()
	p := holdProject(t)
	src := filepath.Join(root, "post.md")
	mustWrite(t, src, "# Post\n")
	o, err := outputs.Add(p, outputs.AddInput{Department: "marketing", Title: "Post", Kind: "post", File: src, Issue: "seo/page", By: "agent-seo-page"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return p, o
}

// TestApprovedOutputConnectorRefusedBeforeApproval: a tool connector with
// needs_approved_output: true exits non-zero and runs nothing (no child,
// no per_run: start) for a worker until the issue's newest output is
// approved; then it runs; a revision holds it again; --as-owner passes.
// The bundled sendportal example, as written in sales-b2c's template, is
// held the same way.
func TestApprovedOutputConnectorRefusedBeforeApproval(t *testing.T) {
	root, _ := perRunFixture(t)
	ran := filepath.Join(root, "ran.txt")
	started := filepath.Join(root, "started")
	cfg := os.Getenv("ISSUE_VIEWER_CONFIG")
	b, _ := os.ReadFile(cfg)
	mustWrite(t, cfg, string(b)+`          - name: post
            kind: tool
            command: /bin/sh
            args: ["-c", "printf %s \"$WP_PASSWORD\" > `+ran+`"]
            secrets:
              WP_PASSWORD: env:ACME_WP
            sends: true
            needs_approved_output: true
          - name: heavypost
            kind: tool
            command: /bin/sh
            args: ["-c", "true"]
            needs_approved_output: true
            per_run:
              start: 'touch `+started+` `+started+`.up'
              ready: 'test -f `+started+`.up'
              stop: 'rm -f `+started+`.up'
`+exampleItem(t, sendPortalExample, "sendportal", "          "))
	// The sendportal example runs as written: its curl is a stand-in on
	// PATH, its keychain: token a fake.
	sent := filepath.Join(root, "sent.txt")
	standin.Write(t, filepath.Join(root, "bin", "curl"), "printf '%s %s' \"$SENDPORTAL_API_TOKEN\" \"$*\" > \"${0%/bin/*}/sent.txt\"\n")
	fakeKeychain(t, "keychain:sendportal-api-token", fakeConnectorValue)
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "seo/page")
	refused := func(name, want string) {
		t.Helper()
		out, err := runCLI(t, "connector", "exec", "marketing/"+name)
		if err == nil || !strings.Contains(out, "connector marketing/"+name+" needs an approved output (needs_approved_output: true)") || !strings.Contains(out, want) {
			t.Fatalf("%s: want a refusal naming %q, got (%v):\n%s", name, want, err, out)
		}
		if strings.Contains(out, fakeConnectorValue) {
			t.Fatalf("the refusal prints the secret:\n%s", out)
		}
		if fileExists(ran) || fileExists(started) || fileExists(sent) {
			t.Fatalf("%s ran something before approval", name)
		}
	}
	refused("post", "no output on the shelf for seo/page")
	refused("heavypost", "no output on the shelf for seo/page")
	refused("sendportal", "no output on the shelf for seo/page")

	p, o := shelfOutput(t, root)
	refused("post", o.ID+" v1 still in review on the shelf for seo/page")
	refused("sendportal", o.ID+" v1 still in review on the shelf for seo/page")

	if _, err := outputs.Review(p, o.ID, outputs.StateApproved, "post it", "test", "owner", time.Now()); err != nil {
		t.Fatal(err)
	}
	if out, err := runCLI(t, "connector", "exec", "marketing/post"); err != nil {
		t.Fatalf("after approval: %s", out)
	}
	if b, _ := os.ReadFile(ran); string(b) != fakeConnectorValue {
		t.Fatalf("the child got %q", b)
	}
	if out, err := runCLI(t, "connector", "exec", "marketing/heavypost"); err != nil || !fileExists(started) {
		t.Fatalf("per_run after approval (%v, started %v): %s", err, fileExists(started), out)
	}
	os.Remove(ran)
	if out, err := runCLI(t, "connector", "exec", "marketing/sendportal", "--", "curl", "-X", "POST", "http://localhost:8000/api/v1/campaigns/1/send"); err != nil {
		t.Fatalf("sendportal after approval: %s", out)
	}
	if b, _ := os.ReadFile(sent); string(b) != fakeConnectorValue+" -X POST http://localhost:8000/api/v1/campaigns/1/send" {
		t.Fatalf("sendportal's curl got %q", b)
	}
	os.Remove(sent)

	// A new version goes back in review: held until the owner looks again.
	src := filepath.Join(root, "post2.md")
	mustWrite(t, src, "# Post v2\n")
	if _, err := outputs.Revise(p, o.ID, src, "", "agent-seo-page", time.Now()); err != nil {
		t.Fatal(err)
	}
	os.Remove(started)
	refused("post", o.ID+" v2 still in review")
	refused("sendportal", o.ID+" v2 still in review")

	// The owner's own shell passes, and a lead has no issue to read.
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	if out, err := runCLI(t, "connector", "exec", "marketing/post", "--as-owner"); err != nil || !fileExists(ran) {
		t.Fatalf("--as-owner: %s", out)
	}
	os.Remove(ran)
	asLeadOf(t, "marketing")
	refused("post", "or for the owner with --as-owner")

	// The listing names the hold.
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	list, _ := runCLI(t, "connectors", "marketing")
	assertContains(t, list, "marketing/post (tool) · sends · needs an approved output")
}

// TestBridgeRefusesAToolCallBeforeApproval: through connector exec's
// bridge, a tools/call before approval is answered with a JSON-RPC error
// and never reaches the stand-in server, while initialize, tools/list and a
// read_tools: tool pass; once the output is approved the call goes through.
func TestBridgeRefusesAToolCallBeforeApproval(t *testing.T) {
	root, _ := httpConnectorFixture(t)
	stub := connectortest.New(t, "")
	cfg := os.Getenv("ISSUE_VIEWER_CONFIG")
	b, _ := os.ReadFile(cfg)
	mustWrite(t, cfg, string(b)+`          - name: postiz
            kind: mcp
            url: `+stub.MCPURL()+`
            sends: true
            needs_approved_output: true
            read_tools: [`+connectortest.ToolName+`]
`)
	_, servers := launchMCP(t, root)
	srv, ok := servers["postiz"]
	if !ok {
		t.Fatalf("mcp.json has no postiz: %v", servers)
	}
	const (
		callSend = `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"create_post","arguments":{}}}`
		callRead = `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"` + connectortest.ToolName + `","arguments":{}}}`
	)
	session := clientInit + "\n" + clientInited + "\n" + clientList + "\n" + callSend + "\n" + callRead + "\n"
	answers := func(out string) map[float64]string {
		got := map[float64]string{}
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			var head struct {
				ID *float64 `json:"id"`
			}
			if json.Unmarshal([]byte(line), &head) == nil && head.ID != nil {
				got[*head.ID] = line
			}
		}
		return got
	}
	called := func(tool string) bool {
		for _, r := range stub.Requests() {
			if r.RPC == "tools/call" && r.Tool == tool {
				return true
			}
		}
		return false
	}

	out, err := runMCPServer(t, srv.Args, srv.Env, session)
	if err != nil {
		t.Fatalf("exec: %s", out)
	}
	got := answers(out)
	if !strings.Contains(got[2], connectortest.ToolName) {
		t.Errorf("tools/list did not pass: %q", got[2])
	}
	if !strings.Contains(got[3], `"error"`) || !strings.Contains(got[3], "connector marketing/postiz needs an approved output") || !strings.Contains(got[3], "no output on the shelf for seo/page") {
		t.Errorf("create_post was not refused: %q", got[3])
	}
	if !strings.Contains(got[4], "called "+connectortest.ToolName) {
		t.Errorf("the read tool did not pass: %q", got[4])
	}
	if called("create_post") {
		t.Fatal("the refused call reached the server")
	}
	if !called(connectortest.ToolName) {
		t.Error("the read tool never reached the server")
	}

	p, o := shelfOutput(t, root)
	if _, err := outputs.Review(p, o.ID, outputs.StateApproved, "post it", "test", "owner", time.Now()); err != nil {
		t.Fatal(err)
	}
	out, err = runMCPServer(t, srv.Args, srv.Env, session)
	if err != nil {
		t.Fatalf("exec after approval: %s", out)
	}
	if got := answers(out); !strings.Contains(got[3], "called create_post") || !called("create_post") {
		t.Errorf("after approval the call was still held: %q", got[3])
	}
}

// TestVideoConnectorKeepsTheOwnersWordRule: the bundled Video template's
// openmontage connector is sends: true but not held: a Video worker runs
// it through connector exec with nothing on the shelf, as role.md says
// (paid tools on the owner's word on the issue, before any output).
func TestVideoConnectorKeepsTheOwnersWordRule(t *testing.T) {
	asLead(t)
	proj, cfg := deptTemplateProject(t)
	studio := filepath.Join(t.TempDir(), "studio")
	ctx, _ := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "video", "--partner-path", studio}); err != nil {
		t.Fatal(err)
	}
	ran := filepath.Join(studio, "ran")
	py := filepath.Join(studio, ".venv", "bin", "python")
	os.MkdirAll(filepath.Dir(py), 0755)
	standin.Write(t, py, "echo \"$@\" > \"${0%/.venv/bin/*}/ran\"\n")
	mustWrite(t, filepath.Join(proj.IssueDir, "clip.md"), "---\ntitle: \"Clip\"\nstatus: \"idea\"\ndepartment: \"Video\"\n---\nBody.\n")

	proj = reloadDeptProject(t, cfg)
	d := proj.DepartmentByKey("video")
	if d == nil || deptConnector(d, "openmontage") == nil {
		t.Fatalf("video connectors: %+v", d)
	}
	if c := deptConnector(d, "openmontage"); !c.Sends || c.NeedsApprovedOutput {
		t.Fatalf("openmontage = sends %v, needs_approved_output %v: the template must stay sends: true and unheld", c.Sends, c.NeedsApprovedOutput)
	}
	if list, _ := outputs.Load(proj); len(list) != 0 {
		t.Fatalf("the shelf is not empty: %d", len(list))
	}

	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "clip")
	ctx, out := goalsCtx(t, proj, false)
	if err := runConnectorExec(ctx, []string{"video/openmontage", "--", py, "tools/tts.py"}); err != nil {
		t.Fatalf("exec: %v\n%s", err, out)
	}
	if b, _ := os.ReadFile(ran); strings.TrimSpace(string(b)) != "tools/tts.py" {
		t.Fatalf("the Video connector did not run: %q", b)
	}
}

// TestConnectorsListNeedsApprovedOutput: the listing, its JSON and the
// worker's briefing line name the hold and the tools open before it.
func TestConnectorsListNeedsApprovedOutput(t *testing.T) {
	httpConnectorFixture(t)
	cfg := os.Getenv("ISSUE_VIEWER_CONFIG")
	b, _ := os.ReadFile(cfg)
	mustWrite(t, cfg, string(b)+`          - name: postiz
            kind: mcp
            url: https://postiz.example.com/mcp
            sends: true
            needs_approved_output: true
            read_tools: [list_integrations]
`)
	out, err := runCLI(t, "connectors", "marketing")
	if err != nil {
		t.Fatal(out)
	}
	assertContains(t, out, "marketing/postiz (mcp) · sends · needs an approved output (open before: list_integrations)")
	if strings.Contains(out, "marketing/wp (tool) · sends · needs") {
		t.Errorf("a sends: connector without the field is listed as held:\n%s", out)
	}
	out, _ = runCLI(t, "connectors", "marketing", "--json")
	assertContains(t, out, `"needs_approved_output": true`)
	assertContains(t, out, `"read_tools": [`)

	brief := tracker.ConnectorsBrief(holdProject(t), "Marketing", false)
	assertContains(t, brief, "postiz (MCP, loaded in this session) — it sends: connector exec refuses it until the owner approves your issue's newest output on the shelf (list_integrations work before); then issue-cli output publish")
	assertContains(t, brief, "wp (tool: issue-cli connector exec marketing/wp -- <cmd>) — it sends: use it only for an output the owner approved")
}

// exampleItem is the connector item name from a template's commented
// connector example file (connectors.example.yaml, items at two spaces),
// up to the next item or comment at that indent, reindented by indent.
func exampleItem(t *testing.T, file, name, indent string) string {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	on := false
	for _, l := range strings.SplitAfter(string(raw), "\n") {
		if strings.HasPrefix(l, "  - name: ") {
			on = strings.TrimSpace(strings.TrimPrefix(l, "  - name: ")) == name
		} else if on && (strings.HasPrefix(l, "  # ") || !strings.HasPrefix(l, "    ")) {
			on = false
		}
		if on {
			b.WriteString(strings.TrimSuffix(indent, "  ") + l)
		}
	}
	if b.Len() == 0 {
		t.Fatalf("no connector %s in %s", name, file)
	}
	out := b.String()
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out
}

// fakeKeychain makes ref read value for this test, every other reference
// as before.
func fakeKeychain(t *testing.T, ref, value string) {
	t.Helper()
	present, read := tracker.SecretPresent, tracker.SecretValue
	tracker.SecretPresent = func(r tracker.SecretRef) bool { return r.String() == ref || present(r) }
	tracker.SecretValue = func(r tracker.SecretRef) (string, bool) {
		if r.String() == ref {
			return value, true
		}
		return read(r)
	}
	t.Cleanup(func() { tracker.SecretPresent, tracker.SecretValue = present, read })
}
