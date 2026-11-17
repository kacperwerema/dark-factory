package tracker

import (
	"strings"
	"testing"
)

func TestIsRemoteStepBox(t *testing.T) {
	for text, want := range map[string]bool{
		"Commit pushed to the remote":                            true,
		"PR opened if applicable":                                true,
		"Pull request opened and linked":                         true,
		"Push the branch to origin":                              true,
		"Changes committed with a message referencing the issue": false,
		"Check scripts (scripts/checks/<name>.sh) removed in a last commit before the merge, or none":                           false,
		"Local main fast-forwarded to the branch (`git -C <main checkout> merge --ff-only <branch>`; no push, no pull request)": false,
		"Never open a PR: ship by fast-forward": false,
		"Proof recorded":                        false,
	} {
		if got := IsRemoteStepBox(text); got != want {
			t.Errorf("IsRemoteStepBox(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestTickNoRemoteBoxes(t *testing.T) {
	body := "## Testing\n- [ ] Branch pushed somewhere\n\n## Shipping\n- [ ] Changes committed\n- [ ] Commit pushed to the remote\n- [x] PR opened if applicable\n- [ ] PR reviewed (owner)\n"
	got, ticked := TickNoRemoteBoxes(body)
	if TickedIDs(ticked) != "S2" {
		t.Fatalf("ticked %q, want S2:\n%s", TickedIDs(ticked), got)
	}
	for _, want := range []string{
		"- [ ] Branch pushed somewhere\n",
		"- [ ] Changes committed\n",
		"- [x] Commit pushed to the remote (N/A: no git remote)\n",
		"- [x] PR opened if applicable\n",
		"- [ ] PR reviewed (owner)\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if p := CheckNA(NANoGitRemote); p != "" {
		t.Errorf("the no-git-remote form fails CheckNA: %s", p)
	}
}
