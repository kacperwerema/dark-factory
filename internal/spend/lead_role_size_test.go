package spend

import (
	"os"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/briefing"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"gopkg.in/yaml.v3"
)

// oldDFLeadRoleChars is the role section of this repo's lead brief at
// 3e19223, rendered as below, before its bullets said each rule once.
const oldDFLeadRoleChars = 12183

// This repo's lead brief's role section, as spend --context splits it,
// stays at least 1k tokens (chars/2.7) under what it was before the cut.
func TestDFLeadBriefRoleIsAThousandTokensShorter(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir()) // no state file of this machine's lead
	raw, err := os.ReadFile("../../workflow.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var wf tracker.WorkflowConfig
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}
	brief := briefing.LeadBrief(&wf, briefing.LeadBriefInput{
		Project:   &tracker.Project{Slug: "dark-factory", Name: "Dark Factory"},
		Root:      "/r",
		Now:       time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC),
		AutoStart: &briefing.AutoStartBrief{Words: "start under 60%", Below: 60, MaxWorkers: 4},
	})
	kind, secs := splitBrief(brief)
	if kind != BriefLead {
		t.Fatalf("kind %q, want a lead brief", kind)
	}
	role := 0
	for _, s := range secs {
		if s.name == SectionRole {
			role = s.chars
		}
	}
	saved := float64(oldDFLeadRoleChars-role) / 2.7
	t.Logf("role %d chars, %.0f tokens under the old", role, saved)
	if saved < 1000 {
		t.Errorf("role section is %d chars, %.0f tokens under the old %d; want at least 1000", role, saved, oldDFLeadRoleChars)
	}
}
