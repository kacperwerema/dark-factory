package tracker

import (
	"strings"
	"testing"
)

// A deeper heading with the block's title is a different section: a "## X"
// block never lands under a "### X" inside another section
// (cli/append-puts-a-section-under-its-own-heading-not-into-a-subheading-of-the-same-name).
func TestAppendIssueBodyHeadingLevel(t *testing.T) {
	cases := []struct {
		name, body, text, want string
	}{
		{
			name: "only a deeper heading: a new section",
			body: "## Design\nPlan\n\n### Clarify\nSee below\n\n## Notes\nN",
			text: "## Clarify\n- Q1",
			want: "## Design\nPlan\n\n### Clarify\nSee below\n\n## Notes\nN\n## Clarify\n- Q1\n",
		},
		{
			name: "both levels: the block's own level, not a refusal",
			body: "## Design\nPlan\n\n### Clarify\nSee below\n\n## Clarify\nQ0",
			text: "## Clarify\n- Q1",
			want: "## Design\nPlan\n\n### Clarify\nSee below\n\n## Clarify\nQ0\n\n- Q1\n",
		},
		{
			name: "own level comes before the deeper one",
			body: "## Clarify\nQ0\n\n## Design\nPlan\n\n### Clarify\nSee below",
			text: "## Clarify\n- Q1",
			want: "## Clarify\nQ0\n\n- Q1\n## Design\nPlan\n\n### Clarify\nSee below\n",
		},
		{
			name: "a later block in the text is not a collision either",
			body: "## Design\nPlan\n\n### Clarify\nSee below",
			text: "## New\nA\n\n## Clarify\n- Q1",
			want: "## Design\nPlan\n\n### Clarify\nSee below\n## New\nA\n\n## Clarify\n- Q1\n",
		},
		{
			name: "a heading in fenced code is no section",
			body: "## Design\n```\n## Clarify\n```",
			text: "## Clarify\n- Q1",
			want: "## Design\n```\n## Clarify\n```\n## Clarify\n- Q1\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed, err := AppendIssueBody(tc.body, tc.text)
			if err != nil {
				t.Fatalf("AppendIssueBody: %v", err)
			}
			if !changed || got != tc.want {
				t.Fatalf("changed %v, body\n%q, want\n%q", changed, got, tc.want)
			}
		})
	}
}

// What matched before still does: the same level, and a deeper block into a
// shallower section of its name.
func TestAppendIssueBodyMatchingSectionsUnchanged(t *testing.T) {
	cases := []struct {
		name, body, text, want string
	}{
		{
			name: "same level",
			body: "## Design\nPlan\n\n## Notes\nN",
			text: "## Design\n- more",
			want: "## Design\nPlan\n\n- more\n## Notes\nN\n",
		},
		{
			name: "same level with deeper subheadings in the block",
			body: "## Test Plan\nT",
			text: "## Test Plan\n### Automated\n- one",
			want: "## Test Plan\nT\n\n### Automated\n- one\n",
		},
		{
			name: "a subsection at its own level",
			body: "## Design\nPlan\n\n### Risks\nR\n\n## Notes\nN",
			text: "### Risks\n- more",
			want: "## Design\nPlan\n\n### Risks\nR\n\n- more\n## Notes\nN\n",
		},
		{
			name: "a deeper block into a shallower section",
			body: "## Design\nPlan",
			text: "### design\n- more",
			want: "## Design\nPlan\n\n- more\n",
		},
		{
			name: "normalized title",
			body: "## Test  Plan\nT",
			text: "##   test plan\n- more",
			want: "## Test  Plan\nT\n\n- more\n",
		},
		{
			name: "no heading in the text",
			body: "## Design\nPlan",
			text: "plain line",
			want: "## Design\nPlan\nplain line\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed, err := AppendIssueBody(tc.body, tc.text)
			if err != nil {
				t.Fatalf("AppendIssueBody: %v", err)
			}
			if !changed || got != tc.want {
				t.Fatalf("changed %v, body\n%q, want\n%q", changed, got, tc.want)
			}
		})
	}
}

func TestAppendIssueBodyStillRefuses(t *testing.T) {
	cases := []struct {
		name, body, text, want string
	}{
		{
			name: "two sections at the block's level",
			body: "## Design\nA\n\n## Design\nB",
			text: "## Design\n- more",
			want: "multiple matching sections",
		},
		{
			name: "two shallower sections for a deeper block",
			body: "## Design\nA\n\n## Design\nB",
			text: "### Design\n- more",
			want: "multiple matching sections",
		},
		{
			name: "a peer heading at the same level",
			body: "## Design\nA",
			text: "## New\n\n## Design\nB",
			want: "duplicate heading",
		},
		{
			name: "a new block repeating a heading at its level",
			body: "## Design\nPlan\n\n### Clarify\nSee below",
			text: "## Clarify\n### Clarify\n- Q1",
			want: "duplicate heading",
		},
		{
			name: "a peer heading deeper than the section it names",
			body: "## Design\nA",
			text: "## New\n\n### Design\nB",
			want: "duplicate heading",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, changed, err := AppendIssueBody(tc.body, tc.text)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if changed {
				t.Fatal("changed = true, want false")
			}
		})
	}
}
