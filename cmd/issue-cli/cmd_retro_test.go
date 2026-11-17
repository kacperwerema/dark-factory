package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/jobs"
	"github.com/kacperwerema/dark-factory/internal/retro"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

const pmOpenActions = `## Proposed actions

- [ ] A1: Do a thing
  Differently: a fresh agent would do this other thing first
  Place: none docs/workflow.md
`

func retroFile(t *testing.T, proj *tracker.Project, name, kind, date, body string) {
	t.Helper()
	dir := filepath.Join(projectRoot(proj), "retros")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, name), fmt.Sprintf("# Retro\n\nIssue: cli/%s\nDate: %s\nKind: %s\n\n%s\n", strings.TrimSuffix(name, ".md"), date, kind, body))
}

func runRetroAt(t *testing.T, proj *tracker.Project, at string, jsonOut bool, args ...string) (string, error) {
	t.Helper()
	now, err := time.Parse(time.RFC3339, at)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stdout, _ := newTestContext(proj, jsonOut)
	ctx.Now = func() time.Time { return now }
	err = runRetro(ctx, args)
	return stdout.String(), err
}

// The whole sweep: list since the mark, archive up to the time list
// printed, and the next list starts after it. A retro written while the
// sweep read stays; so does a post-mortem still waiting for the owner.
func TestRetroSweepListArchiveList(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "backlog")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "factory/sweep")
	root := projectRoot(proj)
	retros := filepath.Join(root, "retros")
	retroFile(t, proj, "20261101-090000-r1.md", "", "2026-11-01T09:00:00Z", "Base workflow: fine")
	retroFile(t, proj, "20261101-100000-postmortem-p1.md", "postmortem", "2026-11-01T10:00:00Z", pmOpenActions)
	retroFile(t, proj, "20261101-110000-postmortem-p2.md", "postmortem", "2026-11-01T11:00:00Z", "## Proposed actions\n\nNone.\n")

	const t1 = "2026-11-01T12:00:00Z"
	out, err := runRetroAt(t, proj, t1, false, "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"no sweep recorded yet", "20261101-090000-r1.md", "20261101-110000-postmortem-p2.md",
		"20261101-100000-postmortem-p1.md  (1 action(s) wait for the owner: stays at archive)",
		"3 file(s) in " + retros + ": 1 retro(s), 2 post-mortem(s), 1 of them waiting for the owner.",
		"retro archive --until " + t1,
		"the lead files them",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list misses %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(retro.LogPath(retros)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("list wrote something: %v", err)
	}

	// Written while the sweep read: newer than the list's time.
	retroFile(t, proj, "20261101-130000-late.md", "", "2026-11-01T13:00:00Z", "Base workflow: late")

	out, err = runRetroAt(t, proj, "2026-11-01T14:00:00Z", false, "archive", "--until", t1)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"✓ 2 file(s) moved to " + filepath.Join(retros, "archive"), "the next sweep reads after " + t1, "stays, waits for the owner: 20261101-100000-postmortem-p1.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("archive misses %q:\n%s", want, out)
		}
	}
	for name, where := range map[string]string{
		"20261101-090000-r1.md":            "archive",
		"20261101-110000-postmortem-p2.md": "archive",
		"20261101-100000-postmortem-p1.md": "",
		"20261101-130000-late.md":          "",
	} {
		if _, err := os.Stat(filepath.Join(retros, where, name)); err != nil {
			t.Errorf("%s not in retros/%s: %v", name, where, err)
		}
	}
	raw, err := os.ReadFile(retro.LogPath(retros))
	if err != nil {
		t.Fatal(err)
	}
	var line retro.Sweep
	if err := json.Unmarshal(raw, &line); err != nil {
		t.Fatalf("log line %s: %v", raw, err)
	}
	if line.By != "agent-factory-sweep" || len(line.Moved) != 2 || len(line.Waiting) != 1 {
		t.Errorf("log line %+v", line)
	}

	// The next sweep: only what came after the mark, not the post-mortem
	// that stayed behind it.
	out, err = runRetroAt(t, proj, "2026-11-02T00:00:00Z", false, "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "since the last sweep ("+t1+")") || !strings.Contains(out, "late.md") || strings.Contains(out, "postmortem-p1") {
		t.Errorf("second list:\n%s", out)
	}
	// --since reaches behind the mark, and its archive command says so.
	out, err = runRetroAt(t, proj, "2026-11-02T00:00:00Z", true, "list", "--since", "2026-11-01")
	if err != nil {
		t.Fatal(err)
	}
	var js struct {
		Retros []struct {
			File    string `json:"file"`
			Kind    string `json:"kind"`
			Waiting int    `json:"waiting"`
		} `json:"retros"`
		Archive string `json:"archive"`
	}
	if err := json.Unmarshal([]byte(out), &js); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	if len(js.Retros) != 2 || js.Retros[0].Kind != "postmortem" || js.Retros[0].Waiting != 1 || !strings.Contains(js.Archive, "--since 2026-11-01") {
		t.Errorf("--since list: %s", out)
	}
}

func TestRetroListEmptyAndBadInput(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "backlog")
	if out, err := runRetroAt(t, proj, "2026-11-01T12:00:00Z", false, "list"); err != nil || !strings.Contains(out, "none") {
		t.Errorf("no retros folder: %q, %v", out, err)
	}
	if _, err := runRetroAt(t, proj, "2026-11-01T12:00:00Z", false, "list", "--since", "yesterday"); err == nil || !strings.Contains(err.Error(), "want YYYY-MM-DD") {
		t.Errorf("bad --since: %v", err)
	}
	if _, err := runRetroAt(t, proj, "2026-11-01T12:00:00Z", false, "archive"); err == nil || !strings.Contains(err.Error(), "needs --until") {
		t.Errorf("archive with no --until: %v", err)
	}
	if _, err := runRetroAt(t, proj, "2026-11-01T12:00:00Z", false, "archive", "--until", "2026-11-01"); err == nil || !strings.Contains(err.Error(), "RFC 3339") {
		t.Errorf("a day as --until: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectRoot(proj), "retros")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("refused calls wrote something: %v", err)
	}
}

// The bundled brief says who files, and sends the run through retro list
// and retro archive, not by hand.
func TestRetroSweepBriefSaysTheLeadFiles(t *testing.T) {
	brief, err := jobs.BriefText(nil, tracker.Job{Brief: "builtin:retro-sweep"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"issue-cli retro list", "The lead files the proposals", "you never run `issue-cli create`", "an issue never applies one", "issue-cli output add --issue {{slug}} --kind report"} {
		if !strings.Contains(brief, want) {
			t.Errorf("brief misses %q", want)
		}
	}
	for _, bad := range []string{"issue-cli create --", "job auto on", "git commit", "issue-cli transition", "mv "} {
		if strings.Contains(brief, bad) {
			t.Errorf("brief tells the run to %q", bad)
		}
	}
}

// Sweep briefs may ask the worker to file issues: the closing line of
// retro list says the brief decides, not that a sweep never files.
func TestRetroListClosingLineMatchesSweepBriefs(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "backlog")
	retroFile(t, proj, "20261101-090000-r1.md", "", "2026-11-01T09:00:00Z", "Base workflow: fine")
	out, err := runRetroAt(t, proj, "2026-11-01T12:00:00Z", false, "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	last := lines[len(lines)-1]
	if last != retroWhoFiles {
		t.Fatalf("retro list closes with %q, want %q", last, retroWhoFiles)
	}
	for _, want := range []string{"files issues when its brief asks for them", "lists its proposals in its report", "the lead files them"} {
		if !strings.Contains(last, want) {
			t.Errorf("closing line misses %q: %q", want, last)
		}
	}
	if strings.Contains(last, "files nothing") {
		t.Errorf("closing line still says a sweep files nothing: %q", last)
	}
}

// The two docs that state the sweep's filing rule quote the CLI's line
// word for word, and neither keeps the old "files nothing" rule for every
// sweep.
func TestSweepFilingRuleIsWordedOnceInDocs(t *testing.T) {
	for _, doc := range []string{"docs/CLI/overview.md", "docs/patterns/lead-session.md"} {
		b, err := os.ReadFile(filepath.Join("..", "..", doc))
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		if n := strings.Count(text, retroWhoFiles); n != 1 {
			t.Errorf("%s states the sweep filing rule %d time(s), want once: %q", doc, n, retroWhoFiles)
		}
		for _, old := range []string{"A sweep lists its proposals in its report and files nothing", "proposes one issue per cause in its report and files nothing"} {
			if strings.Contains(text, old) {
				t.Errorf("%s keeps the old rule %q", doc, old)
			}
		}
	}
}
