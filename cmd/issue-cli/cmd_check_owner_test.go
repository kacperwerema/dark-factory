package main

import (
	"os"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// ownerBoxesProject is a simple project whose issue has a Human Testing box
// (HT1) and a box marked "(owner)" in ## Notes (N1).
func ownerBoxesProject(t *testing.T) (*tracker.Project, string, string) {
	t.Helper()
	proj, slug, issuePath := makeSimpleProject(t, "human-testing")
	data := readFile(t, issuePath) + "\n## Human Testing\n- [ ] Owner looked at it\n\n## Notes\n- [ ] Glows on hover (owner)\n- [ ] Logged\n"
	if err := os.WriteFile(issuePath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return proj, slug, issuePath
}

func TestAgentsCantTickTheOwnersBoxes(t *testing.T) {
	proj, slug, issuePath := ownerBoxesProject(t)
	data := readFile(t, issuePath)

	asWorker(t)
	ctx, _, _ := newTestContext(proj, false)
	for _, c := range []struct {
		name string
		run  func() error
		want string
	}{
		{"check ids", func() error { return runCheck(ctx, []string{slug, "D1", "HT1"}) }, "HT1 Owner looked at it (a Human Testing box)"},
		{"check (owner) id", func() error { return runCheck(ctx, []string{slug, "N1"}) }, `N1 Glows on hover (owner) (marked "(owner)")`},
		{"check --all", func() error { return runCheck(ctx, []string{slug, "--section", "Human Testing", "--all"}) }, "HT1 Owner looked at it"},
		{"check --index", func() error { return runCheck(ctx, []string{slug, "--section", "Notes", "--index", "1"}) }, "N1 Glows on hover (owner)"},
		{"check text", func() error { return runCheck(ctx, []string{slug, "Owner looked"}) }, "HT1 Owner looked at it"},
		{"replace", func() error {
			return runReplace(ctx, []string{slug, "--section", "Human Testing", "--body", "- [x] Owner looked at it"})
		}, "HT1 Owner looked at it"},
		{"append", func() error {
			return runAppend(ctx, []string{slug, "--section", "Notes", "--body", "- [x] Signed off (owner)"})
		}, "Signed off (owner)"},
		{"update --body", func() error {
			return runUpdate(ctx, []string{slug, "--body", strings.Replace(issueBody(t, issuePath), "- [ ] Owner looked", "- [x] Owner looked", 1)})
		}, "HT1 Owner looked at it"},
	} {
		err := c.run()
		if err == nil {
			t.Errorf("%s: a worker ticking an owner box: want a refusal", c.name)
			continue
		}
		assertContains(t, err.Error(), "these boxes are the owner's; agent-calc-other can't tick them, and nothing changed:")
		assertContains(t, err.Error(), c.want)
		assertContains(t, err.Error(), "/owner-checks?epic=")
		if got := readFile(t, issuePath); got != data {
			t.Fatalf("%s: a refused call changed the file:\n%s", c.name, got)
		}
	}

	// The worker's own boxes still tick, and so does a rewrite that keeps
	// the owner's boxes as they are.
	if err := runCheck(ctx, []string{slug, "D1", "N2"}); err != nil {
		t.Fatalf("a worker ticking its own boxes: %v", err)
	}
	if err := runAppend(ctx, []string{slug, "--section", "Notes", "--body", "- [x] Also logged"}); err != nil {
		t.Fatalf("a worker appending its own ticked box: %v", err)
	}

	// The owner, in their own shell, ticks them.
	asOwner(t)
	ctx, _, _ = newTestContext(proj, false)
	if err := runCheck(ctx, []string{slug, "HT1", "N1"}); err != nil {
		t.Fatalf("the owner ticking HT1 and N1: %v", err)
	}
	assertContains(t, readFile(t, issuePath), "- [x] Owner looked at it\n")
	assertContains(t, readFile(t, issuePath), "- [x] Glows on hover (owner)\n")
}

// A Claude Code session that isn't a dispatched worker (the lead, or an
// agent in a plain terminal) is an agent too.
func TestAClaudeCodeSessionCantTickTheOwnersBoxes(t *testing.T) {
	proj, slug, _ := ownerBoxesProject(t)
	asOwner(t)
	t.Setenv("CLAUDECODE", "1")
	ctx, _, _ := newTestContext(proj, false)
	err := runCheck(ctx, []string{slug, "HT1"})
	if err == nil {
		t.Fatal("a Claude Code session ticking HT1: want a refusal")
	}
	assertContains(t, err.Error(), "can't tick them")
}

// issueBody is the issue file's body, after the frontmatter.
func issueBody(t *testing.T, path string) string {
	t.Helper()
	parts := strings.SplitN(readFile(t, path), "---\n", 3)
	return strings.TrimLeft(parts[2], "\n")
}
