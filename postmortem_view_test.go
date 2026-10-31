package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/memory"
	"github.com/michal-franc/issue-viewer/internal/postmortem"
	"github.com/michal-franc/issue-viewer/internal/retro"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

const pmRetroBody = `## Steps
Built it.

## Worked
The stand-in.

## Did not work
none

## Decisions
none

## Known
none

## Proposed actions

- [ ] A1: Remember the flaky lock test
  Differently: a fresh agent would retry the lock test before blaming the code
  Place: memory pitfall
  Text: The lock test is flaky under load; retry it once before debugging.
- [ ] A2: Say it in CLAUDE.md
  Differently: a fresh agent would keep tmux sockets under /tmp in tests
  Place: file CLAUDE.md
  Under: Validation
  Text: Tests keep TMUX_TMPDIR under /tmp.
- [ ] A3: Try a clarify round
  Differently: a fresh agent would ask every design question at once instead
  Place: workflow
  Text: One clarify round in design.
- [ ] A4: Not this one
  Differently: a fresh agent would write the docs page twice over
  Place: file docs/x.md
  Text: x
`

// postmortemSandbox is project Acme in a git repo on main, with issue
// "cart" done and its post-mortem saved.
func postmortemSandbox(t *testing.T) (tracker.Project, string, string) {
	t.Helper()
	_, proj := deptSandbox(t)
	root := proj.Root()
	os.WriteFile(filepath.Join(proj.IssueDir, "cart.md"), []byte("---\ntitle: \"Cart\"\nslug: \"cart\"\nstatus: \"done\"\nsystem: \"Shop\"\n---\n\nBody.\n"), 0o644)
	os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("# Acme\n\n## Validation\n\nRun make validate.\n"), 0o644)
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte("issues/\nretros/\n"), 0o644)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "T")
	git("config", "commit.gpgsign", "false")
	git("config", "core.hooksPath", "/dev/null")
	git("add", ".")
	git("commit", "-q", "-m", "init")
	path, err := postmortem.Save(filepath.Join(root, "retros"), postmortem.Meta{Issue: "cart", Title: "Cart", Status: "done", System: "Shop"}, pmRetroBody, time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, []tracker.Project{proj})
	t.Cleanup(ts.Close)
	return proj, ts.URL, path
}

func postDecide(t *testing.T, url, body string, headers map[string]string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url+"/p/acme/postmortem/cart/decide", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out
}

func TestPostmortemPageShowsTheActionsAndNeedsYou(t *testing.T) {
	proj, url, _ := postmortemSandbox(t)
	resp, body := getResp(t, url+"/p/acme/postmortem/cart")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d:\n%s", resp.StatusCode, body)
	}
	for _, want := range []string{
		`data-testid="postmortem.page"`, `data-testid="postmortem.count">4</span>`,
		`data-testid="postmortem.action.a1"`, `data-testid="postmortem.action.a1.place">memory pitfall</span>`,
		`data-testid="postmortem.action.a2.place">file CLAUDE.md</span>`,
		`<b>Differently:</b> a fresh agent would retry the lock test`,
		`data-decide-url="/p/acme/postmortem/cart/decide"`, `data-testid="postmortem.submit"`,
		`data-testid="postmortem.section.did_not_work"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("post-mortem page missing %q", want)
		}
	}
	rows := needsYouRows(&proj, "", "/p/acme", nil, time.Now())
	found := false
	for _, r := range rows {
		if r.Kind == "postmortem" && r.Slug == "cart" && r.URL == "/p/acme/postmortem/cart" && strings.Contains(r.Note, "4 actions wait") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no Needs you row for the post-mortem: %+v", rows)
	}
	if resp, _ := getResp(t, url+"/p/acme/postmortem/nope"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown issue: %d", resp.StatusCode)
	}
}

// AC3: the owner's one round applies agreed actions (a memory, a one-file
// commit, an experiment proposal) and records refusals; only the owner's
// browser passes. AC4: each outcome is linked from the retro.
func TestPostmortemRoundIsTheOwnersAndAppliesIt(t *testing.T) {
	proj, url, retroPath := postmortemSandbox(t)
	root := proj.Root()
	before, _ := os.ReadFile(retroPath)
	all := `{"answers":[{"id":"A1","agree":true},{"id":"A2","agree":true},{"id":"A3","agree":true},{"id":"A4","agree":false,"note":"not needed"}]}`
	for name, headers := range map[string]map[string]string{
		"curl or an agent": {strangerHeader: "1"},
		"forged origin":    {strangerHeader: "1", "Sec-Fetch-Site": "same-origin"},
		"other site":       {"Origin": "http://evil.example"},
	} {
		if code, out := postDecide(t, url, all, headers); code != http.StatusForbidden {
			t.Errorf("%s: %d %v, want 403", name, code, out)
		}
	}
	if after, _ := os.ReadFile(retroPath); string(after) != string(before) {
		t.Fatal("a refused round changed the retro")
	}
	if code, out := postDecide(t, url, `{"answers":[{"id":"A1","agree":true}]}`, nil); code != http.StatusBadRequest || !strings.Contains(errText(out), "A2") {
		t.Fatalf("partial round: %d %v", code, out)
	}

	code, out := postDecide(t, url, all, nil)
	if code != 200 || out["ok"] != true {
		t.Fatalf("round: %d %v", code, out)
	}

	set, _ := memory.ProjectStore(proj.IssueDir).Load()
	mems := set.Active(time.Now())
	if len(mems) != 1 || mems[0].Source.Issue != "cart" || mems[0].Author != tracker.OwnerAuthor {
		t.Fatalf("memory: %+v", mems)
	}
	claude, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if !strings.Contains(string(claude), "Tests keep TMUX_TMPDIR under /tmp.") {
		t.Fatalf("CLAUDE.md not edited: %s", claude)
	}
	sha, _ := exec.Command("git", "-C", root, "rev-parse", "--short", "HEAD").Output()
	subj, _ := exec.Command("git", "-C", root, "log", "-1", "--format=%s%n%b").Output()
	if !strings.Contains(string(subj), "docs(postmortem): Say it in CLAUDE.md") || !strings.Contains(string(subj), "Refs: cart") {
		t.Fatalf("commit: %s", subj)
	}
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	var exp *tracker.Issue
	for _, is := range issues {
		for _, l := range is.Labels {
			if l == "experiment" {
				exp = is
			}
		}
	}
	if exp == nil || exp.Status != "idea" || !strings.Contains(exp.BodyRaw, "retros/"+filepath.Base(retroPath)) {
		t.Fatalf("experiment proposal: %+v", exp)
	}

	// AC4: the retro links each outcome.
	raw, _ := os.ReadFile(retroPath)
	acts := postmortem.Actions(string(raw))
	for i, want := range []string{"memory " + mems[0].ID, "commit " + strings.TrimSpace(string(sha)) + " (CLAUDE.md)", "experiment " + exp.Slug} {
		if !acts[i].Done || !strings.HasPrefix(acts[i].Applied, want) {
			t.Errorf("%s: applied %q, want %q", acts[i].ID, acts[i].Applied, want)
		}
	}
	if acts[3].Done || !strings.Contains(acts[3].Refused, "not needed") {
		t.Errorf("A4: %+v", acts[3])
	}
	_, body := getResp(t, url+"/p/acme/postmortem/cart")
	for _, want := range []string{"Applied: memory " + mems[0].ID, "Applied: experiment " + exp.Slug, "Refused: ", "not needed"} {
		if !strings.Contains(body, want) {
			t.Errorf("page after the round missing %q", want)
		}
	}
	if strings.Contains(body, `data-testid="postmortem.submit"`) {
		t.Error("a decided post-mortem still has Submit")
	}
	cart := tracker.FindIssue(issues, "cart")
	comments, _ := tracker.LoadComments(cart.FilePath)
	if last := comments[len(comments)-1].Text; !strings.HasPrefix(last, "postmortem: decided on the post-mortem page — 3 applied, 1 refused") {
		t.Errorf("comment: %q", last)
	}
	if rows := postmortemNeedsYou(&proj, "", "/p/acme", time.Now()); len(rows) != 0 {
		t.Errorf("a decided post-mortem still waits: %+v", rows)
	}
	entries, _ := retro.Load(filepath.Join(root, "retros"))
	if len(postmortem.Of(entries, "cart")) != 1 {
		t.Error("the retro moved")
	}
	if code, _ := postDecide(t, url, all, nil); code != http.StatusConflict {
		t.Errorf("second round: %d, want 409", code)
	}
}
