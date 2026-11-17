package agentrun

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

// shelfProject is a project on the bundled development template with the
// given issues (slug → status, type) and a report on the shelf for each slug
// in shelved.
func shelfProject(t *testing.T, issues map[string][2]string, shelved ...string) (*tracker.Project, []*tracker.Issue) {
	t.Helper()
	dev, err := workflowupgrade.ReadTemplate("development")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	wfPath := filepath.Join(dir, "workflow.yaml")
	issueDir := filepath.Join(dir, "issues")
	if err := os.MkdirAll(issueDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wfPath, dev, 0o644); err != nil {
		t.Fatal(err)
	}
	for slug, st := range issues {
		body := "---\ntitle: \"" + slug + "\"\nstatus: \"" + st[0] + "\"\ntype: \"" + st[1] + "\"\ncreated: \"2026-11-17\"\n---\n\nBody.\n"
		if err := os.WriteFile(filepath.Join(issueDir, slug+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	proj := &tracker.Project{Slug: "p", Name: "P", WorkflowFile: wfPath, IssueDir: issueDir, WorkDir: dir}
	file := filepath.Join(t.TempDir(), "r.md")
	if err := os.WriteFile(file, []byte("# r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	for _, slug := range shelved {
		if _, err := outputs.Add(proj, outputs.AddInput{Department: "product", Title: "Report " + slug, Kind: "report", File: file, Issue: slug}, now); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := proj.LoadIssues()
	if err != nil {
		t.Fatal(err)
	}
	return proj, loaded
}

// A report at human-testing with its report on the shelf waits on the
// owner: its session doesn't count against the worker cap. A report still
// researching, one at human-testing with nothing on the shelf, a done one
// and a bugfix at human-testing still count (or are not open).
func TestLiveWorkersSkipsShelvedReport(t *testing.T) {
	proj, _ := shelfProject(t, map[string][2]string{
		"waiting":     {"human-testing", "report"},
		"researching": {"in progress", "report"},
		"empty":       {"human-testing", "report"},
		"bug":         {"human-testing", "bugfix"},
	}, "waiting", "researching", "bug")
	var sessions []string
	for _, slug := range []string{"waiting", "researching", "empty", "bug"} {
		sessions = append(sessions, tracker.AgentSessionName(slug))
	}
	sweepStubs(t, append(sessions, "lead-p"), nil, nil, nil, nil)

	if got := LiveWorkers(proj, ""); got != 3 {
		t.Errorf("LiveWorkers = %d, want 3 (waiting's report is on the shelf: it holds no slot)", got)
	}
	shelved := ShelvedReports(proj, mustIssues(t, proj))
	if r, ok := shelved[tracker.AgentSessionName("waiting")]; !ok || r.Output == nil || r.Output.Title != "Report waiting" || r.Status != "human-testing" {
		t.Errorf("ShelvedReports = %+v, want waiting with its report", shelved)
	}
	if len(shelved) != 2 { // agent- and limited- of waiting
		t.Errorf("ShelvedReports has %d names, want 2: %+v", len(shelved), shelved)
	}
}

func mustIssues(t *testing.T, proj *tracker.Project) []*tracker.Issue {
	t.Helper()
	issues, err := proj.LoadIssues()
	if err != nil {
		t.Fatal(err)
	}
	return issues
}

// The sweep closes an idle session whose report waits on the owner, naming
// the output; one in a turn and an open issue without a shelved report stay.
func TestFinishedSessionsClosesShelvedReport(t *testing.T) {
	proj, issues := shelfProject(t, map[string][2]string{
		"waiting": {"human-testing", "report"},
		"busy":    {"human-testing", "report"},
		"bug":     {"human-testing", "bugfix"},
	}, "waiting", "busy", "bug")
	name := tracker.AgentSessionName
	snap := sweepStubs(t, []string{name("waiting"), name("busy"), name("bug")}, nil, map[string]bool{name("busy"): true}, nil, nil)
	in := SweepInput{Issues: issues, Closed: tracker.ClosedIssueFunc(proj), Snap: snap}

	if got := FinishedSessions(proj, "", in); len(got) != 0 {
		t.Errorf("without Shelved the sweep closed %+v; the viewer's sweep keeps open issues", got)
	}
	in.Shelved = ShelvedReports(proj, issues)
	got := FinishedSessions(proj, "", in)
	if len(got) != 1 {
		t.Fatalf("finished = %+v, want only %s", got, name("waiting"))
	}
	f := got[0]
	if f.Name != name("waiting") || f.Slug != "waiting" || f.Dead || f.Shelved == "" {
		t.Errorf("finished = %+v", f)
	}
	want := "waiting waits on the owner's review at human-testing; its output " + f.Shelved + " is on the shelf"
	if f.Why != want {
		t.Errorf("why = %q, want %q", f.Why, want)
	}
}
