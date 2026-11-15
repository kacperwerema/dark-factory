package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// postmortemTypedFixture is postmortemFixture's issue on a workflow with
// types:, a post-mortem block of block, and the issue typed issueType.
func postmortemTypedFixture(t *testing.T, block, issueType string) *tracker.Project {
	t.Helper()
	proj := postmortemFixture(t, "auto")
	raw, err := os.ReadFile(proj.WorkflowFile)
	if err != nil {
		t.Fatal(err)
	}
	wf := strings.Replace(string(raw), "postmortem:\n  start: auto\n", "", 1)
	wf += "types:\n  feature: {}\n  tweak: {}\n  bugfix: {}\n  epic: {}\n  chore: {}\ndefault_type: feature\n" + block
	if err := os.WriteFile(proj.WorkflowFile, []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	if issueType != "" {
		file := filepath.Join(proj.IssueDir, "CLI", "sample.md")
		body, _ := os.ReadFile(file)
		typed := strings.Replace(string(body), "system: \"CLI\"\n", "system: \"CLI\"\ntype: \""+issueType+"\"\n", 1)
		if err := os.WriteFile(file, []byte(typed), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return proj
}

const autoTypesBlock = "postmortem:\n  start: auto\n  auto_types: [feature, epic]\n"

// AC1: with auto_types set, a tweak's or bugfix's close prints the ask
// command and starts nothing; a feature's starts the agent.
func TestAutoTypesStartOnlyListedTypes(t *testing.T) {
	for _, tc := range []struct {
		issueType string
		starts    bool
	}{
		{"tweak", false},
		{"bugfix", false},
		{"feature", true},
		{"", true}, // no type: walks default_type feature
	} {
		t.Run("type="+tc.issueType, func(t *testing.T) {
			proj := postmortemTypedFixture(t, autoTypesBlock, tc.issueType)
			specs := standInAgent(t)
			ctx, stdout, _ := newTestContext(proj, false)
			if err := runTransition(ctx, []string{"cli/sample", "--to", "done"}); err != nil {
				t.Fatalf("runTransition: %v", err)
			}
			out := stdout.String()
			if !tc.starts {
				if len(*specs) != 0 {
					t.Fatalf("a %s started an agent: %+v", tc.issueType, *specs)
				}
				assertContains(t, out, "== Post-mortem ==")
				assertContains(t, out, "Not started by itself: postmortem: auto_types: feature, epic starts it, and this is a "+tc.issueType+".")
				assertContains(t, out, "issue-cli --project test postmortem start cli/sample --quote")
				return
			}
			if len(*specs) != 1 {
				t.Fatalf("a %q started %d agents, want 1\n%s", tc.issueType, len(*specs), out)
			}
			assertContains(t, out, "✓ Post-mortem started: postmortem-cli-sample")
		})
	}
}

// AC2: with auto_types unset every non-chore type starts as today, and a
// chore stays silent.
func TestAutoTypesUnsetStartsEveryTypeButChore(t *testing.T) {
	for _, issueType := range []string{"tweak", "bugfix", "feature", "epic", "chore"} {
		t.Run(issueType, func(t *testing.T) {
			proj := postmortemTypedFixture(t, "postmortem:\n  start: auto\n", issueType)
			specs := standInAgent(t)
			ctx, stdout, _ := newTestContext(proj, false)
			if err := runTransition(ctx, []string{"cli/sample", "--to", "done"}); err != nil {
				t.Fatalf("runTransition: %v", err)
			}
			if issueType == "chore" {
				if len(*specs) != 0 {
					t.Fatalf("a chore started an agent")
				}
				assertNotContains(t, stdout.String(), "== Post-mortem ==")
				return
			}
			if len(*specs) != 1 {
				t.Fatalf("a %s started %d agents, want 1\n%s", issueType, len(*specs), stdout.String())
			}
		})
	}
}

// A chore the owner lists starts like any listed type.
func TestAutoTypesListingChoreStartsIt(t *testing.T) {
	proj := postmortemTypedFixture(t, "postmortem:\n  start: auto\n  auto_types: [chore]\n", "chore")
	specs := standInAgent(t)
	ctx, _, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"cli/sample", "--to", "done"}); err != nil {
		t.Fatal(err)
	}
	if len(*specs) != 1 {
		t.Fatalf("a listed chore started %d agents, want 1", len(*specs))
	}
}

// AC4 through the CLI: an epic's brief names each child's file, commits and
// retro, and never their text.
func TestEpicPostmortemBriefListsChildren(t *testing.T) {
	proj := postmortemTypedFixture(t, autoTypesBlock, "epic")
	root := filepath.Dir(proj.IssueDir)
	child := "---\ntitle: \"child one\"\nstatus: \"done\"\nsystem: \"CLI\"\ntype: \"tweak\"\nparent: \"cli/sample\"\n---\n\nthe child's own words\n"
	childFile := filepath.Join(proj.IssueDir, "CLI", "child-one.md")
	if err := os.WriteFile(childFile, []byte(child), 0o644); err != nil {
		t.Fatal(err)
	}
	childRetro := filepath.Join(root, "retros", "20261101-000000-cli-child-one.md")
	if err := os.WriteFile(childRetro, []byte("# Workflow Retrospective\n\nIssue: cli/child-one\n\nchild retro text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runPostmortemBrief(ctx, []string{"cli/sample"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	assertContains(t, out, "### Children")
	// No git repo here, so no work branch: the child's commits are read
	// on main by slug (cmd_postmortem_brief_test.go covers a real branch).
	assertContains(t, out, "- cli/child-one: issue "+childFile+" · commits: git -C "+root+" log --grep cli/child-one main · retro "+childRetro)
	for _, secret := range []string{"the child's own words", "child retro text"} {
		assertNotContains(t, out, secret)
	}
}
