package timeline

import (
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The stats sidecar is agent-written: a recorded head that isn't a hex sha
// never reaches git, where "--output=<file>" would be an option
// (docs/API/security.md).
func TestGitRef_IgnoresRecordedRefsThatAreNotShas(t *testing.T) {
	root := t.TempDir()
	rows := []tracker.TransitionStat{{BranchHead: "--output=/tmp/x", MergeSHA: "-p"}}
	if ref, _ := GitRef(root, "nope", rows); ref != "" {
		t.Errorf("GitRef = %q, want none", ref)
	}
	rows = []tracker.TransitionStat{{BranchHead: "0123abcd"}}
	if ref, _ := GitRef(root, "nope", rows); ref != "0123abcd" {
		t.Errorf("GitRef = %q, want the recorded sha", ref)
	}
}
