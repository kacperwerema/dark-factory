package viewer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/ci"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// ciViewProject is a git-repo project with a ci: block (unless ciBlock is
// ""), issues ui/a (done) and ui/b (in progress), and the given CI state.
func ciViewProject(t *testing.T, ciBlock string, st *ci.State) (tracker.Project, string) {
	t.Helper()
	proj, root := ciRepo(t, "civ", ciBlock)
	dir := filepath.Join(root, "issues", "UI")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, status := range map[string]string{"a": "done", "b": "in progress"} {
		body := "---\ntitle: \"" + name + "\"\nstatus: \"" + status + "\"\nsystem: \"UI\"\n---\n\nBody.\n"
		if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if st != nil {
		if err := ci.WriteState(root, st); err != nil {
			t.Fatal(err)
		}
	}
	return proj, root
}

const ciBlockOn = "ci:\n  run:\n    - make validate\n"

func redState() *ci.State {
	return &ci.State{
		Branch: "main", LastSha: "def5678000", State: ci.Red, GreenSha: "abc1234000", RedSince: "def5678000",
		Failed: "make validate", Failures: []string{"TestBroken"},
		Merged:  []ci.Merged{{Branch: "work/ui/a", Slug: "ui/a", Sha: "1111111"}, {Sha: "2222222", Subject: "hotfix"}},
		Blamed:  []string{"ui/a"},
		Log:     ".ci/runs/def5678000.log",
		LogTail: []string{"--- FAIL: TestBroken", "FAIL"},
		Updated: time.Now(),
	}
}

func TestStatusPageShowsRedCI(t *testing.T) {
	proj, _ := ciViewProject(t, ciBlockOn, redState())
	withMockTmuxSessions(t, nil)
	stubAgentsGit(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp, body := getResp(t, ts.URL+"/p/civ/status?classic=1")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d:\n%s", resp.StatusCode, body)
	}
	for _, want := range []string{
		`data-testid="status.ci"`,
		`class="chip chip-red" data-testid="status.ci.state">red</span>`,
		`main red since def5678: make validate: TestBroken`,
		`data-testid="status.ci.blamed"`,
		`href="/p/civ/issue/ui/a" class="status-title">a</a>`,
		`and 1 commit without an issue`,
		`data-testid="status.ci.log"`,
		`--- FAIL: TestBroken`,
		`data-testid="status.waiting.ci"`,
		`data-testid="status.waiting.count">1</span>`,
		`data-testid="nav.tab.status.count">1</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("status page missing %q", want)
		}
	}

	_, md := getResp(t, ts.URL+"/p/civ/status.md")
	for _, want := range []string{
		"- Local CI: main red since def5678: make validate: TestBroken\n  - Landed since green: ui/a\n",
		"```\n--- FAIL: TestBroken\nFAIL\n```",
		"## Waiting on you (1)\n- Local CI red on main since def5678: make validate (issue-cli ci status)",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("status.md missing %q:\n%s", want, md)
		}
	}
}

func TestStatusPageShowsGreenCI(t *testing.T) {
	st := &ci.State{Branch: "main", LastSha: "abc1234000", State: ci.Green, GreenSha: "abc1234000", GreenAt: time.Now().Add(-12 * time.Minute)}
	proj, _ := ciViewProject(t, ciBlockOn, st)
	withMockTmuxSessions(t, nil)
	stubAgentsGit(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, body := getResp(t, ts.URL+"/p/civ/status?classic=1")
	for _, want := range []string{`class="chip chip-green" data-testid="status.ci.state">green</span>`, "main green at abc1234 (12m ago)"} {
		if !strings.Contains(body, want) {
			t.Errorf("status page missing %q", want)
		}
	}
	if strings.Contains(body, "status.waiting.ci") || strings.Contains(body, "status.ci.log") {
		t.Error("green CI must not wait on the owner or show a log")
	}
	_, md := getResp(t, ts.URL+"/p/civ/status.md")
	if !strings.Contains(md, "- Local CI: main green at abc1234") {
		t.Errorf("status.md:\n%s", md)
	}
}

func TestNoCIBlockShowsNothing(t *testing.T) {
	// A stale state file from an earlier opt-in must not show either.
	proj, _ := ciViewProject(t, "", redState())
	withMockTmuxSessions(t, nil)
	stubAgentsGit(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, body := getResp(t, ts.URL+"/p/civ/status?classic=1")
	if strings.Contains(body, `data-testid="status.ci`) || strings.Contains(body, "status.waiting.ci") {
		t.Error("no ci: block, but the Status page shows CI")
	}
	_, md := getResp(t, ts.URL+"/p/civ/status.md")
	if strings.Contains(md, "Local CI") {
		t.Errorf("status.md shows CI:\n%s", md)
	}
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	ctx := newMarkerCtx(&proj, "/p/civ", issues, time.Now())
	for _, issue := range issues {
		if m := ciMarkers(ctx, issue); m != nil {
			t.Errorf("%s got a CI marker without a ci: block", issue.Slug)
		}
	}
}

func TestCIRedMarker(t *testing.T) {
	st := redState()
	st.Blamed = []string{"ui/a", "ui/b"}
	proj, _ := ciViewProject(t, ciBlockOn, st)
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	ctx := newMarkerCtx(&proj, "/p/civ", issues, time.Now())
	for _, issue := range issues {
		var got []Marker
		if issue.Status == "done" {
			got = ciMarkers(ctx, issue)
		} else {
			got = cardMarkers(ctx, issue)
		}
		if len(got) != 1 || got[0].Kind != "ci-red" || got[0].Tone != "red" || got[0].Label != "CI red · make validate" {
			t.Errorf("%s markers = %+v", issue.Slug, got)
		}
	}

	// Green again: the chips go.
	if err := ci.WriteState(ciRootFor(&proj), &ci.State{Branch: "main", State: ci.Green}); err != nil {
		t.Fatal(err)
	}
	ctx = newMarkerCtx(&proj, "/p/civ", issues, time.Now())
	for _, issue := range issues {
		if m := ciMarkers(ctx, issue); m != nil {
			t.Errorf("%s still marked after green", issue.Slug)
		}
	}
}

func TestStatusPageShowsLastRuns(t *testing.T) {
	proj, root := ciViewProject(t, ciBlockOn, redState())
	t0 := time.Now().Add(-time.Hour)
	runs := []ci.Run{
		{Sha: "aaaaaaa111", Tier: "default", State: ci.Green, Started: t0, Finished: t0.Add(time.Minute)},
		{Sha: "bbbbbbb222", Tier: "default", State: ci.Red, Failed: "make validate", Failures: []string{"TestBroken"}, Started: t0.Add(10 * time.Minute), Finished: t0.Add(11 * time.Minute)},
		{Sha: "ccccccc333", Tier: "slow", State: ci.Red, Started: t0.Add(15 * time.Minute), Finished: t0.Add(16 * time.Minute)},
		{Sha: "ddddddd444", Tier: "default", State: ci.Green, Subject: "fix the build", Started: t0.Add(20 * time.Minute), Finished: t0.Add(21 * time.Minute)},
	}
	for _, r := range runs {
		data, _ := json.Marshal(r)
		name := r.Sha
		if r.Tier != "default" {
			name += "." + r.Tier
		}
		if err := os.WriteFile(filepath.Join(ci.RunsDir(root), name+".json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A live run (this process holds the lock).
	lock := fmt.Sprintf(`{"pid": %d, "sha": "eeeeeee555", "tier": "default", "started": %q}`, os.Getpid(), time.Now().Add(-2*time.Minute).Format(time.RFC3339))
	if err := os.WriteFile(ci.LockPath(root), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	withMockTmuxSessions(t, nil)
	stubAgentsGit(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, body := getResp(t, ts.URL+"/p/civ/status?classic=1")
	if !strings.Contains(body, `data-testid="status.ci.runs"`) {
		t.Fatal("no last-runs row")
	}
	// Oldest first, slow tier left out, the live run last.
	var order []string
	for _, sha := range []string{"aaaaaaa", "bbbbbbb", "ccccccc", "ddddddd", "eeeeeee"} {
		if i := strings.Index(body, `data-testid="status.ci.run.`+sha+`"`); i >= 0 {
			order = append(order, sha)
		}
	}
	if strings.Join(order, ",") != "aaaaaaa,bbbbbbb,ddddddd,eeeeeee" {
		t.Errorf("boxes = %v", order)
	}
	if !(strings.Index(body, "status.ci.run.aaaaaaa") < strings.Index(body, "status.ci.run.bbbbbbb") &&
		strings.Index(body, "status.ci.run.bbbbbbb") < strings.Index(body, "status.ci.run.ddddddd") &&
		strings.Index(body, "status.ci.run.ddddddd") < strings.Index(body, "status.ci.run.eeeeeee")) {
		t.Error("boxes are not oldest first")
	}
	for _, want := range []string{
		`class="ci-run ci-run-red" role="listitem" data-testid="status.ci.run.bbbbbbb" data-state="red" title="bbbbbbb · red · 49m ago · took 1m0s · make validate: TestBroken"`,
		`class="ci-run ci-run-running" role="listitem" data-testid="status.ci.run.eeeeeee" data-state="running" title="eeeeeee · running for 2m"`,
		"fix the build",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
}

// A green branch still points at the last red run's kept log: the card's
// Last red log line and the red box link to it, served as plain text.
func TestStatusCardLinksTheLastRedLog(t *testing.T) {
	name := "20261031T110000Z-bbbbbbb222.first.log"
	st := &ci.State{Branch: "main", LastSha: "ddddddd444", State: ci.Green, GreenSha: "ddddddd444", GreenAt: time.Now().Add(-5 * time.Minute),
		LastRedLog: filepath.Join(".ci", "red", name), LastRedSha: "bbbbbbb222", LastRedTier: "default", LastRedAt: time.Now().Add(-40 * time.Minute)}
	proj, root := ciViewProject(t, ciBlockOn, st)
	if err := os.MkdirAll(ci.RedDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ci.RedDir(root), name), []byte("--- FAIL: TestBackgroundWait <b>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().Add(-time.Hour)
	for _, r := range []ci.Run{
		{Sha: "bbbbbbb222", Tier: "default", State: ci.Red, Failed: "make validate", KeptLog: st.LastRedLog, Started: t0, Finished: t0.Add(time.Minute)},
		{Sha: "ddddddd444", Tier: "default", State: ci.Green, Started: t0.Add(10 * time.Minute), Finished: t0.Add(11 * time.Minute)},
	} {
		data, _ := json.Marshal(r)
		if err := os.WriteFile(filepath.Join(ci.RunsDir(root), r.Sha+".json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	withMockTmuxSessions(t, nil)
	stubAgentsGit(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	url := "/p/civ/ci/log/" + name
	for _, page := range []string{"/p/civ/status?classic=1", "/p/civ/status"} {
		_, body := getResp(t, ts.URL+page)
		for _, want := range []string{
			`data-testid="status.ci.lastred"`,
			`href="` + url + `"`,
			`data-testid="status.ci.run.bbbbbbb" data-state="red"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s missing %q", page, want)
			}
		}
		if i := strings.Index(body, `data-testid="status.ci.run.bbbbbbb"`); i < 0 || !strings.Contains(body[max(0, i-200):i], `<a href="`+url+`"`) {
			t.Errorf("%s: the red box does not link to its kept log", page)
		}
	}
	_, md := getResp(t, ts.URL+"/p/civ/status.md")
	if !strings.Contains(md, "Last red log: "+st.LastRedLog) {
		t.Errorf("status.md:\n%s", md)
	}

	resp, body := getResp(t, ts.URL+url)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") || !strings.Contains(body, "TestBackgroundWait <b>") {
		t.Errorf("log: %d %q %q", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
	for _, bad := range []string{"../state.json", "state.json", "20261031T110000Z-zzz.log", "20261031T120000Z-bbbbbbb222.log"} {
		if resp, _ := getResp(t, ts.URL+"/p/civ/ci/log/"+bad); resp.StatusCode != 404 {
			t.Errorf("%s: %d, want 404", bad, resp.StatusCode)
		}
	}
}
