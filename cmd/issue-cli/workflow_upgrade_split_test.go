package main

import (
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

// upgradeSplitFixture is the bundled development template behind its stamp,
// with one opt-in proposal undecided (missing) and one installed feature
// recorded at an older version (present, record only); every other proposal
// is declined.
func upgradeSplitFixture(t *testing.T) (raw, missing, recordOnly string) {
	t.Helper()
	data, err := workflowupgrade.ReadTemplate("development")
	if err != nil {
		t.Fatal(err)
	}
	base, err := workflowupgrade.BuildPlan("workflow.yaml", data, "", false)
	if err != nil || len(base.Proposals) == 0 {
		t.Fatalf("base plan: %v (%d proposals)", err, len(base.Proposals))
	}
	rec, _ := workflowupgrade.ReadRecord(data)
	missing = base.Proposals[0].ID
	for _, p := range base.Proposals[1:] {
		rec.Declined = append(rec.Declined, p.ID)
	}
	recordOnly, _, _ = strings.Cut(rec.Installed[0], "@")
	rec.Installed[0] = recordOnly + "@v0.0.1"
	out, err := workflowupgrade.WriteRecord(data, rec)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := workflowupgrade.BundledVersion("development")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Replace(string(out), "template: development@"+latest, "template: development@v0.36.0", 1), missing, recordOnly
}

func TestWorkflowUpgradePrintsMissingAndRecordOnlyApart(t *testing.T) {
	raw, missing, recordOnly := upgradeSplitFixture(t)
	ctx, stdout, _ := newTestContext(writeUpgradeProject(t, raw), false)
	if err := runWorkflow(ctx, []string{"upgrade"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	assertContains(t, out, "== Missing (1): not in the file, to adopt ==\n[1] "+missing+" — ")
	assertContains(t, out, "== Present, record only (1): in the file, record older or absent ==\n[2] "+recordOnly+" — ")
	assertContains(t, out, "(recorded v0.0.1, catalog ")
	assertContains(t, out, "Record at the catalog version: `issue-cli workflow scan --installed "+recordOnly+"`")
	if strings.Count(out, "  Snippet:") != 1 {
		t.Errorf("want the snippet of the missing feature only:\n%s", out)
	}
}
