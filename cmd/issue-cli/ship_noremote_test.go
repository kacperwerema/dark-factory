package main

import (
	"os"
	"strings"
	"testing"
)

const noRemoteWorkflow = `
statuses:
  - name: idea
  - name: in progress
  - name: shipping
  - name: done
transitions:
  - from: "in progress"
    to: "shipping"
    actions:
      - type: "append_section"
        title: "Shipping"
        body: |
          - [ ] Changes committed with a message referencing the issue
          - [ ] Commit pushed to the remote
          - [ ] PR opened if applicable
          - [ ] Local main fast-forwarded to the branch (no push, no pull request)
          - [ ] Branch pushed for review (owner)
  - from: "shipping"
    to: "done"
    actions:
      - type: "validate"
        rule: "section_checkboxes_checked: Shipping"
`

// noRemoteProject is a git repo with one issue in progress whose move into
// shipping appends the bundled push and PR boxes.
func noRemoteProject(t *testing.T) (*Context, string, func(args ...string) string, interface{ String() string }) {
	t.Helper()
	proj, g := shipProject(t, map[string]string{"solo": depIssue("solo", "in progress", "")})
	if err := os.WriteFile(proj.WorkflowFile, []byte(noRemoteWorkflow), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, out, _ := newTestContext(proj, false)
	return ctx, proj.IssueDir + "/solo.md", g, out
}

// With no git remote the move into shipping ticks the push and PR boxes
// itself with "N/A: no git remote"; the other boxes, the one that forbids
// a push and the owner's box stay open. With a remote nothing changes.
func TestShippingNoRemoteTicksPushAndPRBoxes(t *testing.T) {
	asWorkerOn(t, "solo")
	ctx, path, _, out := noRemoteProject(t)
	if err := runTransition(ctx, []string{"solo", "--to", "shipping"}); err != nil {
		t.Fatal(err)
	}
	body := readFile(t, path)
	for _, want := range []string{
		"- [ ] Changes committed with a message referencing the issue\n",
		"- [x] Commit pushed to the remote (N/A: no git remote)\n",
		"- [x] PR opened if applicable (N/A: no git remote)\n",
		"- [ ] Local main fast-forwarded to the branch (no push, no pull request)\n",
		"- [ ] Branch pushed for review (owner)\n",
	} {
		assertContains(t, body, want)
	}
	assertContains(t, out.String(), "No git remote: ticked S2, S3 with N/A: no git remote")

	// A repo with a remote: the same move ticks nothing.
	ctx, path, g, out := noRemoteProject(t)
	g("remote", "add", "origin", "https://example.invalid/solo.git")
	if err := runTransition(ctx, []string{"solo", "--to", "shipping"}); err != nil {
		t.Fatal(err)
	}
	body = readFile(t, path)
	if strings.Contains(body, "- [x]") || strings.Contains(body, "no git remote") {
		t.Errorf("a repo with a remote got boxes ticked:\n%s", body)
	}
	if strings.Contains(out.String(), "No git remote") {
		t.Errorf("a repo with a remote printed the no-remote line:\n%s", out)
	}
}

// check --na "no git remote" takes a push or PR box by hand only when the
// repo has no remote; any other Shipping box, or a repo with a remote,
// keeps the "no N/A form" refusal.
func TestCheckNANoGitRemoteOnShippingBox(t *testing.T) {
	asWorkerOn(t, "solo")
	ctx, path, g, _ := noRemoteProject(t)
	data := strings.Replace(readFile(t, path), "status: \"in progress\"", "status: \"shipping\"", 1) +
		"\n## Shipping\n- [ ] Commit pushed to the remote\n- [ ] Changes committed\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	naRefused(t, runCheck(ctx, []string{"solo", "S2", "--na", "no git remote"}), path, data, "shipping steps have no N/A form")
	if err := runCheck(ctx, []string{"solo", "S1", "--na", "no git remote"}); err != nil {
		t.Fatalf("check --na no git remote on a push box: %v", err)
	}
	assertContains(t, readFile(t, path), "- [x] Commit pushed to the remote (N/A: no git remote)\n")

	ctx, path, g, _ = noRemoteProject(t)
	g("remote", "add", "origin", "https://example.invalid/solo.git")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	naRefused(t, runCheck(ctx, []string{"solo", "S1", "--na", "no git remote"}), path, data, "shipping steps have no N/A form")
}
