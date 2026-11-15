package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/connectors"
	"github.com/kacperwerema/dark-factory/internal/connectors/connectortest"
	"github.com/kacperwerema/dark-factory/internal/lint"
	"github.com/kacperwerema/dark-factory/internal/standin"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// marketingExample is the bundled Marketing connector example file.
var marketingExample = filepath.Join("templates", "departments", "marketing", "connectors.example.yaml")

// postizException is the decision the example's exception: names: the
// engine owner's licence exception for Postiz.
const postizException = "factory/open-source-partners-integrate-outside-tools-openmontage-openseo-postiz-instead-of-building-them-in-house#D-4"

// postizServices are the containers of the Postiz compose project the
// spike ran (o-20261102-a64d). spotlight stands for a service a plain
// `docker compose … stop` doesn't reach: one under a profile the stop
// isn't given, or an orphan the file no longer names.
var postizServices = []string{"postiz", "postiz-postgres", "postiz-redis", "temporal", "temporal-postgresql", "temporal-elasticsearch", "temporal-admin-tools", "temporal-ui"}

const postizUnreached = "spotlight"

// postizRun is marketing/social as the Marketing template writes it,
// uncommented, with its url: at the stub and its header from an env:
// reference (a subprocess can't see a fake Keychain), and stand-ins on
// PATH for docker (a compose project of the services above, one file per
// running container) and curl (ready:'s probe: 401 while postiz runs).
// Its start:, ready: and stop: are the template's, byte for byte.
type postizRun struct {
	t    *testing.T
	root string
	stub *connectortest.Server
	dir  string // the stand-ins' state
}

func postizFixture(t *testing.T) *postizRun {
	t.Helper()
	root := connectorFixture(t)
	stub := connectortest.New(t, "Bearer "+fakeHeaderValue)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("ACME_SEO", fakeHeaderValue)
	r := &postizRun{t: t, root: root, stub: stub, dir: filepath.Join(root, "docker")}
	os.MkdirAll(filepath.Join(r.dir, "running"), 0755)

	item := exampleItem(t, marketingExample, "social", "          ")
	for old, repl := range map[string]string{
		"http://127.0.0.1:4007/api/mcp": stub.MCPURL(),
		"Bearer keychain:postiz":        "Bearer env:ACME_SEO",
	} {
		if !strings.Contains(item, old) {
			t.Fatalf("the social example has no %q:\n%s", old, item)
		}
		item = strings.Replace(item, old, repl, 1)
	}
	cfg := os.Getenv("ISSUE_VIEWER_CONFIG")
	b, _ := os.ReadFile(cfg)
	mustWrite(t, cfg, string(b)+item)

	bin := filepath.Join(root, "bin")
	os.MkdirAll(bin, 0755)
	// The rig's folder comes from the environment, so every stand-in's
	// body is the same each run (internal/standin).
	t.Setenv("POSTIZ_RIG", r.dir)
	standin.Write(t, filepath.Join(bin, "docker"), `D=$POSTIZ_RIG
echo "$*" >> "$D/docker.log"
case "$1" in
compose)
  for a in "$@"; do verb=$a; done
  case "$verb" in
  start)
    [ -f "$D/fail-start" ] && exit 1
    for s in `+strings.Join(postizServices, " ")+` `+postizUnreached+`; do touch "$D/running/$s"; done ;;
  stop)
    for s in `+strings.Join(postizServices, " ")+`; do rm -f "$D/running/$s"; done ;;
  esac ;;
ps) ls "$D/running" ;;
stop) shift; for id in "$@"; do rm -f "$D/running/$id"; echo "$id"; done ;;
esac
`)
	standin.Write(t, filepath.Join(bin, "curl"), "if [ -f \"$POSTIZ_RIG/running/postiz\" ]; then printf 401; else printf 000; fi\n")
	standin.Write(t, filepath.Join(bin, "mem-guard"), "echo 'ok: memory used 40% (< 75%)'\n")
	// ready: waits out nginx's 10 s marks with sleep 2 between probes: no
	// marks here, so no wait.
	standin.Write(t, filepath.Join(bin, "sleep"), "exit 0\n")
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	return r
}

// up are the stand-in containers running now.
func (r *postizRun) up() []string {
	entries, _ := os.ReadDir(filepath.Join(r.dir, "running"))
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func (r *postizRun) dockerLog() string {
	b, _ := os.ReadFile(filepath.Join(r.dir, "docker.log"))
	return string(b)
}

// events are runs.jsonl's events for marketing/social, oldest first.
func (r *postizRun) events() []string {
	b, _ := os.ReadFile(filepath.Join(r.root, "issues", ".connectors", "runs.jsonl"))
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var rec connectors.RunRecord
		if json.Unmarshal([]byte(line), &rec) == nil && rec.Connector == "marketing/social" {
			out = append(out, rec.Event)
		}
	}
	return out
}

// assertDown: no container of the project runs, stop: ran, and the run log
// ends with want after a start.
func (r *postizRun) assertDown(want ...string) {
	r.t.Helper()
	if up := r.up(); len(up) != 0 {
		r.t.Errorf("containers still running after the run: %v\ndocker calls:\n%s", up, r.dockerLog())
	}
	if !strings.Contains(r.dockerLog(), "compose -p postiz -f") || !strings.Contains(r.dockerLog(), "stop") {
		r.t.Errorf("stop: never ran:\n%s", r.dockerLog())
	}
	if got := r.events(); !slices.Equal(got, want) {
		r.t.Errorf("runs.jsonl events = %v, want %v", got, want)
	}
}

// execArgs is connector exec of marketing/social in the owner's shell.
var postizExecArgs = []string{"--project", "demo", "connector", "exec", "marketing/social", "--as-owner"}

// waitFor polls cond until it holds or 30 s pass.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// startExec runs connector exec as a program (this test binary as the
// CLI), its stdin a pipe held open as an agent's MCP client holds it, and
// waits until the run brought Postiz up.
func (r *postizRun) startExec() (*exec.Cmd, io.WriteCloser) {
	r.t.Helper()
	cmd := exec.Command(os.Args[0], postizExecArgs...)
	cmd.Env = append(os.Environ(), "ISSUE_CLI_TEST_AS_CLI=1")
	cmd.Dir = r.root
	in, err := cmd.StdinPipe()
	if err != nil {
		r.t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	io.WriteString(in, clientInit+"\n"+clientInited+"\n")
	waitFor(r.t, "the run to bring Postiz up", func() bool {
		return len(r.up()) == len(postizServices)+1 && slices.Contains(r.events(), connectors.RunStarted)
	})
	return cmd, in
}

func (r *postizRun) waitExit(cmd *exec.Cmd) {
	r.t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		cmd.Process.Kill()
		r.t.Fatal("connector exec did not end after the signal")
	}
}

const postizCall = `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"` + connectortest.ToolName + `","arguments":{}}}`

// AC3: every way out of a run stops the whole Postiz compose project, using
// the template's own start:, ready: and stop: against a stand-in docker:
// the session's end (EOF on stdin, the first session and a later one that
// starts at its first call), SIGINT, SIGTERM and SIGHUP to a live exec, a
// start that fails, and SIGKILL, which only the sweep can follow.
func TestPostizPerRunStopsOnEveryWayOut(t *testing.T) {
	t.Run("session ends at EOF", func(t *testing.T) {
		r := postizFixture(t)
		out, err := runMCPServer(t, postizExecArgs, nil, strings.Join([]string{clientInit, clientInited, clientList, postizCall}, "\n")+"\n")
		if err != nil {
			t.Fatalf("exec: %s", out)
		}
		if !strings.Contains(out, `"id":3`) || strings.Contains(out, `"error"`) {
			t.Errorf("the call was not answered by the server:\n%s", out)
		}
		r.assertDown(connectors.RunStarted, connectors.RunStopped)

		// The tool list is saved now: the next session stays down until its
		// first call, then stops at its end as well.
		out, err = runMCPServer(t, postizExecArgs, nil, strings.Join([]string{clientInit, clientInited, clientList}, "\n")+"\n")
		if err != nil || len(r.events()) != 2 {
			t.Fatalf("a session that only lists started Postiz: %v %v\n%s", err, r.events(), out)
		}
		out, err = runMCPServer(t, postizExecArgs, nil, strings.Join([]string{clientInit, clientInited, postizCall}, "\n")+"\n")
		if err != nil {
			t.Fatalf("exec: %s", out)
		}
		r.assertDown(connectors.RunStarted, connectors.RunStopped, connectors.RunStarted, connectors.RunStopped)
	})

	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			r := postizFixture(t)
			cmd, _ := r.startExec()
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			r.waitExit(cmd)
			r.assertDown(connectors.RunStarted, connectors.RunStopped)
		})
	}

	t.Run("start fails", func(t *testing.T) {
		r := postizFixture(t)
		mustWrite(t, filepath.Join(r.dir, "fail-start"), "")
		out, err := runMCPServer(t, postizExecArgs, nil, clientInit+"\n")
		if err == nil || !strings.Contains(out, "did not start (start: exit 1); its stop: ran") {
			t.Fatalf("a failed start: %v\n%s", err, out)
		}
		r.assertDown(connectors.RunStartFailed)
	})

	t.Run("SIGKILL, then the sweep", func(t *testing.T) {
		r := postizFixture(t)
		cmd, _ := r.startExec()
		if err := cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		r.waitExit(cmd)
		if len(r.up()) == 0 {
			t.Fatal("a killed run stopped Postiz: SIGKILL can't run stop:, the sweep must be what stops it here")
		}
		if out, err := runCLI(t, "connector", "sweep", "marketing"); err != nil {
			t.Fatalf("sweep: %s", out)
		}
		r.assertDown(connectors.RunStarted, connectors.RunStopped)
	})
}

// TestPostizStopReachesEveryContainerOfTheProject: why the template's stop:
// goes on past `docker compose … stop`: that alone stops only the file's
// services (under the active profiles), and the run leaves the rest up.
func TestPostizStopReachesEveryContainerOfTheProject(t *testing.T) {
	r := postizFixture(t)
	cfg := os.Getenv("ISSUE_VIEWER_CONFIG")
	b, _ := os.ReadFile(cfg)
	compose := "docker compose -p postiz -f ~/postiz/compose.yaml stop"
	full := compose + `; ids=$(docker ps -q --filter label=com.docker.compose.project=postiz); [ -z "$ids" ] || docker stop $ids`
	if !strings.Contains(string(b), "stop: '"+full+"'") {
		t.Fatalf("the template's stop: is not %q:\n%s", full, b)
	}
	mustWrite(t, cfg, strings.Replace(string(b), full, compose, 1))
	if out, err := runMCPServer(t, postizExecArgs, nil, clientInit+"\n"); err != nil {
		t.Fatalf("exec: %s", out)
	}
	if up := r.up(); !slices.Equal(up, []string{postizUnreached}) {
		t.Errorf("compose stop alone left %v, want only %s: the stand-in no longer shows the gap", up, postizUnreached)
	}
}

// AC1: department add marketing writes the Postiz `social` connector
// commented; uncommented it is an mcp url: connector on loopback with its
// key a keychain: reference in a header, sends: true, the AGPL exception,
// Postiz's AI tools off, tools per status (read at brief, none at draft or
// owner review, scheduling only at publish, never type: now) and per_run: on tool postiz
// stopping the whole compose project. Lint is clean once the board holds
// the exception it names, and warns without it.
func TestMarketingSocialExampleIsPostizPerRun(t *testing.T) {
	asLead(t)
	proj, cfg := deptTemplateProject(t)
	ctx, out := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "marketing"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"#   - name: social",
		"#       Authorization: \"Bearer keychain:postiz\"",
		"#       tool: postiz",
		"security add-generic-password -s postiz",
	} {
		assertContains(t, out.String(), want)
		assertContains(t, string(raw), want)
	}
	if d := reloadDeptProject(t, cfg).DepartmentByKey("marketing"); d == nil || len(d.Connectors) != 0 {
		t.Fatalf("marketing installed with connectors %+v, want none (the examples are commented)", d)
	}

	uncommentConnectors(t, cfg)
	proj = reloadDeptProject(t, cfg)
	d := proj.DepartmentByKey("marketing")
	var c *tracker.Connector
	for i := range d.Connectors {
		if d.Connectors[i].Name == "social" {
			c = &d.Connectors[i]
		}
	}
	if c == nil {
		t.Fatalf("uncommented: no social connector in %+v", d.Connectors)
	}
	if c.Kind != tracker.ConnectorMCP || c.URL != "http://127.0.0.1:4007/api/mcp" || c.Command != "" || !c.Sends || c.Exception != postizException {
		t.Errorf("social = %+v", *c)
	}
	if len(c.Headers) != 1 || c.Headers["Authorization"] != "Bearer keychain:postiz" || len(c.Secrets) != 0 {
		t.Errorf("social headers %v secrets %v, want only Authorization: Bearer keychain:postiz", c.Headers, c.Secrets)
	}
	for _, ai := range []string{"ask_postiz", "generateImageTool", "generateVideoTool"} {
		if !slices.Contains(c.DeniedTools(), ai) {
			t.Errorf("Postiz's AI tool %s is not in deny_tools: %v", ai, c.DeniedTools())
		}
	}
	if got := strings.Join(c.AtStatuses(), ","); got != "brief,measure,publish" {
		t.Errorf("at: statuses = %s, want brief, measure, publish (none at draft or owner review)", got)
	}
	const schedule = "integrationSchedulePostTool"
	for status, wantSchedule := range map[string]bool{"brief": false, "publish": true, "measure": false} {
		if got := slices.Contains(c.At[status].Allow, schedule); got != wantSchedule {
			t.Errorf("at: %s allows %s = %v, want %v", status, schedule, got, wantSchedule)
		}
		if !slices.Contains(c.At[status].Allow, "integrationList") {
			t.Errorf("at: %s lacks the read call integrationList", status)
		}
	}
	// AC4 of the nested deny_args issue: at publish a post sent to go out at
	// once is refused at the bridge, a scheduled one passes.
	now := `{"socialPost":[{"integrationId":"a","type":"schedule"},{"integrationId":"b","type":"now"}]}`
	if err := connectors.AtError("marketing/social", c, "publish", schedule, []byte(now)); err == nil || !strings.Contains(err.Error(), "socialPost[1].type: now is refused") {
		t.Errorf("publish: a socialPost item with type now: %v, want refused", err)
	}
	later := `{"socialPost":[{"integrationId":"a","type":"schedule","date":"2026-12-01T09:00:00Z"}]}`
	if err := connectors.AtError("marketing/social", c, "publish", schedule, []byte(later)); err != nil {
		t.Errorf("publish: a scheduled post refused: %v", err)
	}
	for _, status := range []string{"draft", "owner review"} {
		if err := connectors.AtError("marketing/social", c, status, "integrationList", nil); err == nil {
			t.Errorf("at %s a Postiz call passes: draft makes no connector call", status)
		}
	}
	r := c.PerRun
	if r == nil || r.Tool != "postiz" || len(r.MissingSteps()) != 0 || r.Timeout < 60 {
		t.Fatalf("per_run = %+v", r)
	}
	if !strings.HasPrefix(r.Start, "docker compose -p postiz -f ") || !strings.HasSuffix(r.Start, " start") {
		t.Errorf("start: %q, want docker compose -p postiz -f <file> start", r.Start)
	}
	if !strings.Contains(r.Stop, "docker compose -p postiz -f ") || !strings.Contains(r.Stop, "label=com.docker.compose.project=postiz") {
		t.Errorf("stop: %q, want compose stop and every container labelled with the project", r.Stop)
	}
	// One 401 isn't ready: nginx in Postiz keeps a refused start-up probe's
	// 10 s mark per worker, so ready: asks the MCP endpoint itself over 12 s.
	if !strings.Contains(r.Ready, "http://127.0.0.1:4007/api/mcp") || !strings.Contains(r.Ready, "sleep 2") || !strings.Contains(r.Ready, "1 2 3 4 5 6") {
		t.Errorf("ready: %q, want six 401s from /api/mcp, 2 s apart", r.Ready)
	}

	for _, f := range []string{"brand.md", "icp.md"} {
		mustWrite(t, filepath.Join(proj.Root(), "departments", "marketing", f), "# settled\n")
	}
	// Without the exception on this board, lint says so for social.
	if s := socialLint(t, proj); !strings.Contains(s, "connector social: Postiz is AGPL-3.0") {
		t.Errorf("no licence warning for social without the exception on the board:\n%s", s)
	}
	// The owner records it; the example's exception: names it.
	slug, _, _ := strings.Cut(strings.TrimPrefix(postizException, "factory/"), "#")
	os.MkdirAll(filepath.Join(proj.IssueDir, "Factory"), 0755)
	mustWrite(t, filepath.Join(proj.IssueDir, "Factory", "partners.md"), "---\ntitle: \"Partners\"\nstatus: \"done\"\ncreated: \"2026-11-02\"\nslug: \""+slug+"\"\n---\n## Decisions\n"+
		"- **D-4** · 2026-11-02 · CEO chat: Postiz is accepted as the licence exception, \"Postiz can be exception for now.\"\n")
	proj = reloadDeptProject(t, cfg)
	if s := socialLint(t, proj); s != "" {
		t.Errorf("uncommented: lint findings about social:\n%s", s)
	}
}

// socialLint is lint's findings that name the social connector, Postiz or
// a connector at all.
func socialLint(t *testing.T, proj *tracker.Project) string {
	t.Helper()
	env, err := lint.Load(proj, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rep, err := lint.Run(env)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range rep.Findings {
		s := strings.ToLower(strings.ReplaceAll(f.Where+" "+f.Message, proj.Root(), "<root>"))
		if strings.Contains(s, "social") || strings.Contains(s, "postiz") || strings.Contains(s, "connector") {
			got = append(got, f.Check+": "+f.Where+": "+f.Message)
		}
	}
	return strings.Join(got, "\n")
}

// AC5: docs/departments.md shows the Postiz example in Per run and says the
// container is stopped after each run; the catalog paragraph names Postiz
// the social default.
func TestDocsShowThePostizPerRunExample(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "departments.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	for _, want := range []string{
		"- name: social",
		"Authorization: \"Bearer keychain:postiz\"",
		"label=com.docker.compose.project=postiz",
		"Postiz's containers are stopped after each run",
		"Postiz is the social slot's default",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/departments.md lacks %q", want)
		}
	}
	if strings.Contains(doc, "**social has none**") {
		t.Error("docs/departments.md still says the social slot has no default")
	}
}

// issue-cli connectors names the owner's Connect socials page under a
// social connector the page drives.
func TestConnectorsNamesTheConnectSocialsPage(t *testing.T) {
	postizFixture(t)
	out, err := runCLI(t, "connectors", "marketing")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "connect networks (the owner, in the browser): ") || !strings.Contains(out, "/socials/marketing") {
		t.Fatalf("no Connect socials line:\n%s", out)
	}
}
