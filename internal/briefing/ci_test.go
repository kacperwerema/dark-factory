package briefing

import (
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestLeadBriefMentionsCIOnlyWhenConfigured(t *testing.T) {
	proj := &tracker.Project{Name: "P", Slug: "p"}
	in := LeadBriefInput{Project: proj, Root: t.TempDir()}
	off := LeadBrief(&tracker.WorkflowConfig{}, in)
	if strings.Contains(off, "Local CI") {
		t.Error("no ci: block, but the brief mentions local CI")
	}
	on := LeadBrief(&tracker.WorkflowConfig{CI: &tracker.CIConfig{Run: []tracker.CICommand{{Cmd: "make check"}}}}, in)
	for _, want := range []string{"- Local CI:", "CI never messages you: read issue-cli --project p ci status", "before any push", "Never push a release while it is red"} {
		if !strings.Contains(on, want) {
			t.Errorf("brief missing %q", want)
		}
	}
	if strings.Contains(on, "[from ci]") {
		t.Error("the brief still promises a [from ci] message")
	}
}

func TestLeadBriefMentionsReleaseCandidatesOnlyWhenConfigured(t *testing.T) {
	proj := &tracker.Project{Name: "P", Slug: "p"}
	in := LeadBriefInput{Project: proj, Root: t.TempDir()}
	if off := LeadBrief(&tracker.WorkflowConfig{}, in); strings.Contains(off, "release candidate") {
		t.Error("no release_candidates, but the brief mentions release candidates")
	}
	on := LeadBrief(&tracker.WorkflowConfig{Workbench: tracker.WorkbenchConfig{ReleaseCandidates: true}}, in)
	for _, want := range []string{"- Releases go through a release candidate on a release branch.", "release cut <version>", "never push a candidate tag", "release pick <slug>", "each pick needs the next candidate", "Never publish the release", "explicit ok on a soaked candidate"} {
		if !strings.Contains(on, want) {
			t.Errorf("brief missing %q", want)
		}
	}
	if strings.Contains(on, "release publish") || strings.Contains(on, "Private tracker") {
		t.Error("no privacy: block, but the brief talks about snapshots or the tracker")
	}
	priv := LeadBrief(&tracker.WorkflowConfig{Workbench: tracker.WorkbenchConfig{ReleaseCandidates: true}, Privacy: &tracker.PrivacyConfig{WorkFiles: "tracker", Code: "snapshots"}}, in)
	for _, want := range []string{"release publish <version> from the release worktree", "never push anything else to public", "tracker sync; never add work files"} {
		if !strings.Contains(priv, want) {
			t.Errorf("privacy brief missing %q", want)
		}
	}
}
