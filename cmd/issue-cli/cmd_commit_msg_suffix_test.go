package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/proof"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// suffixedCriteriaBody is the shape a lead leaves when it adds criteria
// after the fact: an empty ## Acceptance criteria, then the boxes under
// a heading with a parenthetical suffix (checklist lists them as ACL1,
// ACL2).
const suffixedCriteriaBody = `## Acceptance criteria
## Acceptance criteria (lead, 2026-11-21)

Some context.

- [ ] ship never runs npm ci through a linked node_modules.
- [ ] A test with a stand-in npm leaves the main checkout untouched.

## Repro
Steps.
`

// An issue whose criteria sit under "## Acceptance criteria (lead, …)"
// has criteria: the commit-msg hook takes Proves: ACL1 <Test> and says
// what it still waits on, and Collect links ACL1 to the test.
func TestCommitMsgSuffixedCriteriaHeading(t *testing.T) {
	proj, _, issuePath := makeSimpleProject(t, "in progress")
	data, err := os.ReadFile(issuePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(issuePath, []byte(string(data)+"\n\n"+suffixedCriteriaBody), 0o644); err != nil {
		t.Fatal(err)
	}
	onWorkBranch(t, filepath.Dir(proj.IssueDir))

	items := tracker.Criteria(string(data) + "\n\n" + suffixedCriteriaBody)
	if len(items) != 2 || items[0].ID != "ACL1" || items[1].ID != "ACL2" {
		t.Fatalf("criteria under the suffixed heading: %+v, want ACL1, ACL2", items)
	}

	ctx, _, stderr := newTestContext(proj, false)
	if err := runCommitMsg(ctx, []string{commitMsgFile(t, commitSubject+"Proves: ACL1 TestShipLinkedNodeModules\n")}); err != nil {
		t.Fatalf("Proves: ACL1 refused on an issue with criteria: %v\n%s", err, stderr)
	}
	out := stderr.String()
	if strings.Contains(out, "has no ## Acceptance criteria") {
		t.Errorf("the hook says the issue has no criteria:\n%s", out)
	}
	assertContains(t, out, "note: ACL1 stays unproven until `issue-cli prove cli/sample --run` runs TestShipLinkedNodeModules")

	// A box id that isn't a criterion is still refused.
	ctx, _, stderr = newTestContext(proj, false)
	if err := runCommitMsg(ctx, []string{commitMsgFile(t, commitSubject+"Proves: D1 TestX\n")}); err == nil {
		t.Fatalf("Proves: D1 accepted:\n%s", stderr)
	}

	ids, tests := proof.ProvesParts("ACL1, ACL2 TestA", proof.CriteriaPrefixes(items)...)
	if strings.Join(ids, ",") != "ACL1,ACL2" || strings.Join(tests, ",") != "TestA" {
		t.Errorf("ProvesParts: ids %v tests %v, want [ACL1 ACL2] [TestA]", ids, tests)
	}
	// Boxes under both headings: one line may name AC and ACL ids.
	both := tracker.Criteria("## Acceptance Criteria\n- [ ] one\n\n" + suffixedCriteriaBody)
	if got := proof.CriteriaPrefixes(both); strings.Join(got, ",") != "AC,ACL" {
		t.Fatalf("CriteriaPrefixes on both headings: %v, want [AC ACL]", got)
	}
	if ids, tests := proof.ProvesParts("AC1 ACL2 TestA", proof.CriteriaPrefixes(both)...); strings.Join(ids, ",") != "AC1,ACL2" || len(tests) != 1 {
		t.Errorf("ProvesParts on both: ids %v tests %v", ids, tests)
	}
}

// A heading that only starts with the words is not the criteria section:
// the suffix has to be parenthetical.
func TestCriteriaSectionNeedsParentheticalSuffix(t *testing.T) {
	for name, want := range map[string]bool{
		"Acceptance Criteria":                    true,
		"acceptance criteria":                    true,
		"Acceptance criteria (lead, 2026-11-21)": true,
		"Acceptance criteria(lead)":              true,
		"Acceptance criteria for the UI":         false,
		"Acceptance criteria notes":              false,
		"Acceptance":                             false,
	} {
		if got := tracker.IsCriteriaSection(name); got != want {
			t.Errorf("IsCriteriaSection(%q) = %v, want %v", name, got, want)
		}
	}
}
