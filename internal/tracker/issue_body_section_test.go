package tracker

import (
	"strings"
	"testing"
)

func TestAppendIssueBodyAutoRoutesWhenLeadingHeadingExists(t *testing.T) {
	body := "## Design\nExisting design notes"
	got, changed, err := AppendIssueBody(body, "## Design\n\nNew design notes")
	if err != nil {
		t.Fatalf("AppendIssueBody returned error: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	want := "## Design\nExisting design notes\n\nNew design notes\n"
	if got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestAppendIssueBodyAutoRoutesWithSubheadings(t *testing.T) {
	body := "## Implementation\n- [ ] step one"
	got, changed, err := AppendIssueBody(body, "## Implementation\n\n### Notes\n- detail")
	if err != nil {
		t.Fatalf("AppendIssueBody returned error: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	if !strings.Contains(got, "### Notes") || !strings.Contains(got, "- detail") {
		t.Fatalf("body = %q, want subheading appended into Implementation", got)
	}
	if strings.Count(got, "## Implementation") != 1 {
		t.Fatalf("body = %q, want only one ## Implementation heading", got)
	}
}

func TestAppendIssueBodyAutoRoutesNormalizedHeading(t *testing.T) {
	body := "## Design\nExisting design notes"
	got, changed, err := AppendIssueBody(body, "###   design\nNew design notes")
	if err != nil {
		t.Fatalf("AppendIssueBody returned error: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	if !strings.Contains(got, "New design notes") {
		t.Fatalf("body = %q, want appended detail", got)
	}
}

func TestAppendIssueBodyRejectsPeerHeadingCollision(t *testing.T) {
	body := "## Design\nA\n\n## Test Plan\nB"
	_, changed, err := AppendIssueBody(body, "## New\n\n## Design\nshould fail")
	if err == nil {
		t.Fatal("expected duplicate heading error")
	}
	if changed {
		t.Fatal("changed = true, want false")
	}
	if !strings.Contains(err.Error(), "duplicate heading") {
		t.Fatalf("error = %q, want duplicate heading guidance", err)
	}
	if !strings.Contains(err.Error(), "Use --section") {
		t.Fatalf("error = %q, want section guidance", err)
	}
}

func TestAppendIssueBodyToSectionCreatesMissingSection(t *testing.T) {
	body, changed, err := AppendIssueBodyToSection("## Idea\nProblem", "Design", "Plan", false)
	if err != nil {
		t.Fatalf("AppendIssueBodyToSection returned error: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	want := "## Idea\nProblem\n\n## Design\nPlan\n"
	if body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
}

func TestAppendIssueBodyToSectionAppendsToNormalizedMatch(t *testing.T) {
	body, changed, err := AppendIssueBodyToSection("###   Design  \nExisting detail", "design", "New detail", false)
	if err != nil {
		t.Fatalf("AppendIssueBodyToSection returned error: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	want := "###   Design  \nExisting detail\n\nNew detail\n"
	if body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
}

func TestAppendIssueBodyToSectionRejectsAmbiguousMatchWithoutForce(t *testing.T) {
	body := "## Design\nOne\n\n### Design\nTwo"
	_, changed, err := AppendIssueBodyToSection(body, "design", "Three", false)
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
	if changed {
		t.Fatal("changed = true, want false")
	}
	if !strings.Contains(err.Error(), "multiple matching sections") {
		t.Fatalf("error = %q, want ambiguity guidance", err)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Fatalf("error = %q, want force guidance", err)
	}
}

func TestAppendIssueBodyToSectionForceUsesFirstMatch(t *testing.T) {
	body, changed, err := AppendIssueBodyToSection("## Design\nOne\n\n### Design\nTwo", "design", "Three", true)
	if err != nil {
		t.Fatalf("AppendIssueBodyToSection returned error: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	want := "## Design\nOne\n\n### Design\nTwo\n\nThree\n"
	if body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
}

// A body that starts with the target section's own heading must not write
// that heading a second time (cli/append-section-duplicates-the-heading-when-the-body-starts-with-it).
func TestAppendIssueBodyToSectionStripsLeadingSameHeading(t *testing.T) {
	cases := []struct {
		name, body, section, text, want string
	}{
		{
			name: "existing section", body: "## Design\n- [ ] one", section: "Design",
			text: "\n## Design\n- [ ] two",
			want: "## Design\n- [ ] one\n\n- [ ] two\n",
		},
		{
			name: "normalized title", body: "## Design\n- [ ] one", section: "design",
			text: "##  DESIGN \n- [ ] two",
			want: "## Design\n- [ ] one\n\n- [ ] two\n",
		},
		{
			name: "missing section is created once", body: "## Idea\nx", section: "Design",
			text: "## Design\n- [ ] two",
			want: "## Idea\nx\n\n## Design\n- [ ] two\n",
		},
		{
			name: "a different leading heading is kept", body: "## Design\n- [ ] one", section: "Design",
			text: "### Risks\n- [ ] two",
			want: "## Design\n- [ ] one\n\n### Risks\n- [ ] two\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, changed, err := AppendIssueBodyToSection(tc.body, tc.section, tc.text, false)
			if err != nil {
				t.Fatalf("AppendIssueBodyToSection returned error: %v", err)
			}
			if !changed {
				t.Fatal("changed = false, want true")
			}
			if body != tc.want {
				t.Fatalf("body = %q, want %q", body, tc.want)
			}
		})
	}
}

func TestReplaceIssueBodySectionReplacesUntilNextHeading(t *testing.T) {
	body := "## Design\nOld plan\nMore old detail\n\n## Notes\nKept"
	got, changed, err := ReplaceIssueBodySection(body, "Design", "New plan", false)
	if err != nil {
		t.Fatalf("ReplaceIssueBodySection returned error: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	want := "## Design\nNew plan\n\n## Notes\nKept\n"
	if got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestReplaceIssueBodySectionStopsAtSameDepthHeading(t *testing.T) {
	body := "### Sub\nOld\n\n### Other\nKept"
	got, changed, err := ReplaceIssueBodySection(body, "Sub", "New", false)
	if err != nil {
		t.Fatalf("ReplaceIssueBodySection returned error: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	want := "### Sub\nNew\n\n### Other\nKept\n"
	if got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestReplaceIssueBodySectionConsumesNestedSubheadings(t *testing.T) {
	body := "## Test Plan\n### Automated\n- old test\n\n### Manual\n- old step\n\n## Notes\nKept"
	got, changed, err := ReplaceIssueBodySection(body, "Test Plan", "### Automated\n- new test\n\n### Manual\n- new step", false)
	if err != nil {
		t.Fatalf("ReplaceIssueBodySection returned error: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	want := "## Test Plan\n### Automated\n- new test\n\n### Manual\n- new step\n\n## Notes\nKept\n"
	if got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestReplaceIssueBodySectionPreservesHeadingFormatting(t *testing.T) {
	body := "###   Design  \nOld"
	got, changed, err := ReplaceIssueBodySection(body, "design", "New", false)
	if err != nil {
		t.Fatalf("ReplaceIssueBodySection returned error: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	want := "###   Design  \nNew\n"
	if got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestReplaceIssueBodySectionAtEndOfBody(t *testing.T) {
	body := "## Intro\nKept\n\n## Last\nOld content\nMore old"
	got, changed, err := ReplaceIssueBodySection(body, "Last", "New", false)
	if err != nil {
		t.Fatalf("ReplaceIssueBodySection returned error: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	want := "## Intro\nKept\n\n## Last\nNew\n"
	if got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestReplaceIssueBodySectionMissingSectionErrors(t *testing.T) {
	_, changed, err := ReplaceIssueBodySection("## Intro\nBody", "Design", "New", false)
	if err == nil {
		t.Fatal("expected missing-section error")
	}
	if changed {
		t.Fatal("changed = true, want false")
	}
	if !strings.Contains(err.Error(), "no section matching") {
		t.Fatalf("error = %q, want missing-section guidance", err)
	}
	if !strings.Contains(err.Error(), "issue-cli append") {
		t.Fatalf("error = %q, want append guidance", err)
	}
}

func TestReplaceIssueBodySectionRejectsAmbiguousMatchWithoutForce(t *testing.T) {
	body := "## Design\nOne\n\n### Design\nTwo"
	_, changed, err := ReplaceIssueBodySection(body, "design", "New", false)
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
	if changed {
		t.Fatal("changed = true, want false")
	}
	if !strings.Contains(err.Error(), "multiple matching sections") {
		t.Fatalf("error = %q, want ambiguity guidance", err)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Fatalf("error = %q, want force guidance", err)
	}
}

func TestReplaceIssueBodySectionForceReplacesFirstMatch(t *testing.T) {
	body := "## Design\nOne\n\n### Design\nTwo"
	got, changed, err := ReplaceIssueBodySection(body, "design", "New", true)
	if err != nil {
		t.Fatalf("ReplaceIssueBodySection returned error: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	// First match is "## Design" (level 2); nested "### Design" is inside its
	// scope and gets replaced with the rest of the section content.
	want := "## Design\nNew\n"
	if got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestReplaceIssueBodySectionEmptyBodyClearsSection(t *testing.T) {
	body := "## Design\nOld\n\n## Notes\nKept"
	got, changed, err := ReplaceIssueBodySection(body, "Design", "", false)
	if err != nil {
		t.Fatalf("ReplaceIssueBodySection returned error: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	want := "## Design\n\n## Notes\nKept\n"
	if got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestReplaceIssueBodySectionNoOpWhenContentUnchanged(t *testing.T) {
	body := "## Design\nSame content"
	got, changed, err := ReplaceIssueBodySection(body, "Design", "Same content", false)
	if err != nil {
		t.Fatalf("ReplaceIssueBodySection returned error: %v", err)
	}
	if changed {
		t.Fatal("changed = true, want false (idempotent)")
	}
	if got != body {
		t.Fatalf("body changed unexpectedly: %q", got)
	}
}
