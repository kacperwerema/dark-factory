package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The call that lost a round in the post-mortem: a "## Clarify" block on an
// issue with a "### Clarify" pointer under Design is its own section, the one
// issue-cli clarify reads.
func TestAppendBodyFileReachesTheSectionAtItsLevel(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	data := readFile(t, issuePath) + "\n## Design\nPlan\n\n### Clarify\nSee below\n\n## Notes\nN\n"
	if err := os.WriteFile(issuePath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	block := filepath.Join(t.TempDir(), "block.md")
	if err := os.WriteFile(block, []byte("## Clarify\n- first question\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, _, _ := newTestContext(proj, false)
	if err := runAppend(ctx, []string{slug, "--body-file", block}); err != nil {
		t.Fatalf("append: %v", err)
	}
	body := issueBody(t, issuePath)
	found := false
	for _, s := range tracker.SplitSections(body) {
		if s.Title == "Clarify" {
			found = true
			if !strings.Contains(s.Text, "first question") {
				t.Errorf("## Clarify = %q, want the block", s.Text)
			}
		}
		if s.Title == "Design" && strings.Contains(s.Text, "first question") {
			t.Errorf("the block landed under Design:\n%s", s.Text)
		}
	}
	if !found {
		t.Fatalf("no ## Clarify section in\n%s", body)
	}

	// The same block again now finds its own section.
	if err := runAppend(ctx, []string{slug, "--body", "## Clarify\n- second question"}); err != nil {
		t.Fatalf("second append: %v", err)
	}
	body = issueBody(t, issuePath)
	if n := strings.Count(body, "## Clarify\n") - strings.Count(body, "### Clarify\n"); n != 1 {
		t.Fatalf("%d ## Clarify headings in\n%s", n, body)
	}
	if !strings.Contains(body, "- second question") {
		t.Fatalf("second block missing in\n%s", body)
	}
}

// The retros' call: append --section "Test Plan" with ### Automated and
// ### Manual blocks on a Test Plan that has both puts the lines under them
// and says so, with no second copy of either heading.
func TestAppendSectionMergesIntoItsSubheadingsAndSaysSo(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	data := readFile(t, issuePath) + "\n## Checks\n\n### Automated\n- [ ] unit tests\n\n### Manual\n- [ ] click it\n"
	if err := os.WriteFile(issuePath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, stdout, stderr := newTestContext(proj, false)
	if err := runAppend(ctx, []string{slug, "--section", "Checks", "--body", "### Automated\n- go test ./x\n\n### Manual\n- open the page"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	body, _ := tracker.SectionText(issueBody(t, issuePath), "Checks")
	for _, h := range []string{"### Automated\n", "### Manual\n"} {
		if n := strings.Count(body, h); n != 1 {
			t.Errorf("%d copies of %q in\n%s", n, h, body)
		}
	}
	if !strings.Contains(body, "- [ ] unit tests\n- go test ./x") || !strings.Contains(body, "- [ ] click it\n- open the page") {
		t.Errorf("lines not under their sub-headings:\n%s", body)
	}
	if want := "Merged under the existing ### Automated, ### Manual"; !strings.Contains(stdout.String(), want) {
		t.Errorf("stdout lacks %q:\n%s", want, stdout.String())
	}
	if strings.Contains(stderr.String(), "warning:") {
		t.Errorf("unexpected warning:\n%s", stderr.String())
	}
}

// A body whose block is a sibling section's heading still lands inside the
// target section, as before; the append now warns that the heading is there
// twice.
func TestAppendSectionWarnsWhenItWritesADuplicateHeading(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	data := readFile(t, issuePath) + "\n## Plan\nP\n\n## Notes\nN\n"
	if err := os.WriteFile(issuePath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, _, stderr := newTestContext(proj, false)
	if err := runAppend(ctx, []string{slug, "--section", "Plan", "--body", "More plan\n\n## Notes\nM"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if want := "warning: the issue now has ## Notes twice in one place"; !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr lacks %q:\n%s", want, stderr.String())
	}
}

// The retros' call: append --section X --replace with no old line meant to
// replace the whole section. The refusal names replace --section with the
// call's slug and section, and the one-line form, and changes nothing.
func TestAppendBareReplaceNamesReplaceSection(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	data := readFile(t, issuePath) + "\n## Test Plan\nold plan\n"
	if err := os.WriteFile(issuePath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "plan.md")
	if err := os.WriteFile(file, []byte("new plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{slug, "--section", "Test Plan", "--replace", "--body-file", file},
		{slug, "--section", "Test Plan", "--body-file", file, "--replace"},
		{slug, "--section=Test Plan", "--replace", "", "--body", "new plan"},
	} {
		ctx, _, _ := newTestContext(proj, false)
		err := runAppend(ctx, args)
		if err == nil {
			t.Fatalf("%q: no refusal", args[1:])
		}
		var ue *usageError
		if !errors.As(err, &ue) {
			t.Errorf("%q: %T, want a usage error (exit 2)", args[1:], err)
		}
		for _, want := range []string{
			"issue-cli replace " + slug + " --section 'Test Plan' --body-file <file>",
			"issue-cli append " + slug + " --section 'Test Plan' --replace '<old line>' --body '<new line>'",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%q: refusal lacks %q:\n%s", args[1:], want, err)
			}
		}
	}
	if got := readFile(t, issuePath); got != data {
		t.Fatalf("the refused call changed the issue:\n%s", got)
	}
}

func TestAppendReplaceEmptyBodyNamesReplaceSectionOfTheLine(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	data := readFile(t, issuePath) + "\n## Test Plan\n- placeholder: tbd\n- keep me\n"
	if err := os.WriteFile(issuePath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{slug, "--replace", "- placeholder: tbd", "--body", ""},
		{slug, "--section", "Test Plan", "--replace", "- placeholder: tbd", "--body", ""},
		{slug, "--replace", "  - placeholder: tbd ", "--text", ""},
	} {
		ctx, _, _ := newTestContext(proj, false)
		err := runAppend(ctx, args)
		if err == nil {
			t.Fatalf("%q: no refusal", args[1:])
		}
		var ue *usageError
		if !errors.As(err, &ue) {
			t.Errorf("%q: %T, want a usage error (exit 2)", args[1:], err)
		}
		for _, want := range []string{
			"can't delete a line",
			"issue-cli replace " + slug + " --section 'Test Plan' --body-file <file>",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%q: refusal lacks %q:\n%s", args[1:], want, err)
			}
		}
	}
	if got := readFile(t, issuePath); got != data {
		t.Fatalf("the refused call changed the issue:\n%s", got)
	}
}
