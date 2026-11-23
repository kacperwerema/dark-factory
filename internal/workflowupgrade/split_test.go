package workflowupgrade

import (
	"strings"
	"testing"
)

// splitFixture is the bundled development template behind its stamp, with
// one opt-in proposal left undecided (missing: not in the file) and one
// installed feature recorded at an older version (present, record only).
// Every other proposal is declined, so those two are the whole plan.
func splitFixture(t *testing.T) (raw []byte, missing, recordOnly string) {
	t.Helper()
	data, err := ReadTemplate("development")
	if err != nil {
		t.Fatal(err)
	}
	base, err := BuildPlan("workflow.yaml", data, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(base.Proposals) < 1 {
		t.Fatalf("the bundled template leaves nothing undecided; pick another fixture")
	}
	rec, _ := ReadRecord(data)
	missing = base.Proposals[0].ID
	for _, p := range base.Proposals[1:] {
		rec.Declined = append(rec.Declined, p.ID)
	}
	recordOnly, _ = featureRef(rec.Installed[0])
	rec.Installed[0] = recordOnly + "@v0.0.1"
	out, err := WriteRecord(data, rec)
	if err != nil {
		t.Fatal(err)
	}
	latest := mustLatest(t, "development")
	return []byte(strings.Replace(string(out), "template: development@"+latest, "template: development@v0.36.0", 1)), missing, recordOnly
}

// The Dark Factory report: 17 "features to adopt", 12 of them already in
// the file at an older record version. The plan tells the two apart.
func TestUpgradeSplitsMissingFromRecordOnly(t *testing.T) {
	raw, missing, recordOnly := splitFixture(t)
	plan, err := BuildPlan("workflow.yaml", raw, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(plan.Missing, ","); got != missing {
		t.Errorf("missing = %q, want %q (proposals %v)", got, missing, proposalIDs(plan))
	}
	if got := strings.Join(plan.RecordOnly, ","); got != recordOnly {
		t.Errorf("record only = %q, want %q", got, recordOnly)
	}
	for _, p := range plan.Proposals {
		if p.ID == recordOnly && (!p.Present || p.Kind != ProposalUpdate) {
			t.Errorf("%s: present=%v kind=%s, want a present update", p.ID, p.Present, p.Kind)
		}
	}
	assertContains(t, plan.Message, "1 missing feature(s) to propose (1 more present in the file, record only")

	// Only the record-only one left: nothing to adopt, the steps are scan
	// and stamp.
	rec, _ := ReadRecord(raw)
	rec.Declined = append(rec.Declined, missing)
	only, err := WriteRecord(raw, rec)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = BuildPlan("workflow.yaml", only, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Missing) != 0 || len(plan.RecordOnly) != 1 {
		t.Fatalf("missing %v, record only %v", plan.Missing, plan.RecordOnly)
	}
	assertContains(t, plan.Message, "Nothing missing")
	assertContains(t, strings.Join(plan.Steps, "\n"), "issue-cli workflow scan --installed "+recordOnly)
}

// Every feature present and recorded at the current versions: no proposal.
func TestUpgradeAllPresentAndRecordedProposesNothing(t *testing.T) {
	data, err := ReadTemplate("development")
	if err != nil {
		t.Fatal(err)
	}
	base, err := BuildPlan("workflow.yaml", data, "", false)
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := ReadRecord(data)
	for _, p := range base.Proposals {
		rec.Declined = append(rec.Declined, p.ID)
	}
	out, err := WriteRecord(data, rec)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan("workflow.yaml", out, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != StatusUpToDate || len(plan.Proposals) != 0 || len(plan.Missing) != 0 || len(plan.RecordOnly) != 0 {
		t.Errorf("status %s, proposals %v, missing %v, record only %v; want up to date and empty", plan.Status, proposalIDs(plan), plan.Missing, plan.RecordOnly)
	}
}
