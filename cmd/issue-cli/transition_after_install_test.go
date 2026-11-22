package main

import (
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The move into shipping warns on a Test Plan "After install" line no
// after install: criterion carries, with the fix; other moves don't.
func TestTransitionIntoShippingWarnsOnAProseAfterInstallLine(t *testing.T) {
	wf := &tracker.WorkflowConfig{Statuses: []tracker.WorkflowStatus{{Name: "testing", Role: tracker.StatusRoleVerify}, {Name: "shipping"}, {Name: "done"}}}
	issue := &tracker.Issue{Slug: "s/one", BodyRaw: "## Acceptance Criteria\n- [ ] AC1 x (test: TestX)\n\n## Test Plan\n- After install, try the hook\n"}
	if wf.ShipStatus() != "shipping" {
		t.Fatalf("ship status = %q, the test workflow needs one", wf.ShipStatus())
	}
	got := afterInstallWarnings(wf, issue, "shipping")
	if len(got) != 1 || !strings.HasPrefix(got[0], "after-install: s/one Test Plan:") || !strings.Contains(got[0], "fix: issue-cli check-add s/one") {
		t.Fatalf("warnings into shipping = %q", got)
	}
	if got := afterInstallWarnings(wf, issue, "testing"); len(got) != 0 {
		t.Fatalf("warnings into testing = %q, want none", got)
	}
}
