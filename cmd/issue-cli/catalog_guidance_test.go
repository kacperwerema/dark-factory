package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

func engineWorkflow(t *testing.T) *tracker.WorkflowConfig {
	t.Helper()
	wf, err := tracker.LoadWorkflow(filepath.Join("..", "..", "workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

// designCatalogBox is the Catalog box the transition into in design appends.
func designCatalogBox(t *testing.T, wf *tracker.WorkflowConfig) string {
	t.Helper()
	for _, tr := range wf.Transitions {
		for _, a := range tr.Actions {
			if a.Type != "append_section" || a.Title != "Design" {
				continue
			}
			for _, line := range strings.Split(a.Body, "\n") {
				if strings.Contains(line, "] Catalog:") {
					return line
				}
			}
		}
	}
	t.Fatal("no Catalog box in the Design section of workflow.yaml")
	return ""
}

// The box is what an agent copies into a commit message, so it offers only
// what catalog-check reads: the form the parser takes, and leaving the line out.
func TestDesignCatalogBoxSaysAnIDOrNoLine(t *testing.T) {
	box := designCatalogBox(t, engineWorkflow(t))
	if !strings.Contains(box, workflowupgrade.TrailerForm) {
		t.Errorf("box doesn't name the form catalog-check reads, %q:\n%s", workflowupgrade.TrailerForm, box)
	}
	if !strings.Contains(box, "no `Catalog:` line when catalog-check names no entry") {
		t.Errorf("box doesn't say to leave the line out:\n%s", box)
	}
	if strings.HasSuffix(strings.TrimSpace(box), "or n/a") {
		t.Errorf("box ends in a bare n/a, which an agent copies as an id-less line:\n%s", box)
	}
}

// Every path that commits code hears the same thing in progress: catalog-check
// before the first commit, and the ids it names in one line of the form it reads.
func TestInProgressRunsCatalogCheckBeforeTheFirstCommit(t *testing.T) {
	wf := engineWorkflow(t)
	for _, typ := range []string{"feature", "tweak", "bugfix"} {
		t.Run(typ, func(t *testing.T) {
			prompt := wf.ForType(typ).StatusPrompt("in progress")
			for _, want := range []string{
				"issue-cli workflow catalog-check",
				"on the staged change before the first commit",
				"in one `" + workflowupgrade.TrailerForm + "` line",
			} {
				if !strings.Contains(prompt, want) {
					t.Errorf("%s in progress prompt lacks %q:\n%s", typ, want, prompt)
				}
			}
		})
	}
}

// The form the guidance names, filled in, passes the commit-msg hook; the bare
// id of an entry the change doesn't bump does not, so the guidance must not
// offer the bare form as the n/a's alternative.
func TestCatalogGuidanceFormPassesTheCommitHook(t *testing.T) {
	known := knownCatalogIDs(t)
	filled := strings.NewReplacer("<id>[,<id>...]", "parked-status,parked-status", "<why>", "a refactor").Replace(workflowupgrade.TrailerForm)
	if got := CommitMsgProblems(commitSubject+filled+"\n", known); len(got) != 0 {
		t.Fatalf("%q refused: %v", filled, got)
	}
	if got := CommitMsgProblems(commitSubject+"Catalog: parked-status\n", known); len(got) == 0 {
		t.Fatal("a bare Catalog: <id> of an entry not bumped passes the hook: the box and the guidance can offer it now")
	}
	wf := engineWorkflow(t)
	text := designCatalogBox(t, wf) + "\n" + wf.ForType("tweak").StatusPrompt("in progress")
	if strings.Contains(text, "`Catalog: <id>`") {
		t.Errorf("guidance offers the bare form the hook refuses:\n%s", text)
	}
}
