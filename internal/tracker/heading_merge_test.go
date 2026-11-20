package tracker

import "testing"

// append --section onto a section that already has the body's sub-headings
// puts the lines under them instead of writing each heading a second time
// (cli/append-section-onto-an-existing-sub-heading-appends-under-it-or-warns-instead-of-writing-a-duplicate-heading).
func TestAppendToSectionMergesIntoExistingSubheadings(t *testing.T) {
	testPlan := "## Test Plan\n\n### Automated\n- [ ] unit tests\n\n### Manual\n- [ ] click it\n\n## Notes\nkept"
	cases := []struct {
		name, body, section, text, want string
		unchanged                       bool
	}{
		{
			name: "both sub-headings merge", body: testPlan, section: "Test Plan",
			text: "### Automated\n- go test ./x\n\n### Manual\n- open the page",
			want: "## Test Plan\n\n### Automated\n- [ ] unit tests\n- go test ./x\n\n### Manual\n- [ ] click it\n- open the page\n\n## Notes\nkept\n",
		},
		{
			name: "body opening with the section heading merges too", body: testPlan, section: "Test Plan",
			text: "## Test Plan\n### Manual\n- open the page",
			want: "## Test Plan\n\n### Automated\n- [ ] unit tests\n\n### Manual\n- [ ] click it\n- open the page\n\n## Notes\nkept\n",
		},
		{
			name: "a new sub-heading goes at the section's end", body: testPlan, section: "Test Plan",
			text: "### Automated\n- go test ./x\n\n### Load\n- under-load.sh",
			want: "## Test Plan\n\n### Automated\n- [ ] unit tests\n- go test ./x\n\n### Manual\n- [ ] click it\n\n### Load\n- under-load.sh\n\n## Notes\nkept\n",
		},
		{
			name: "lead text goes before the first sub-heading", body: "## Test Plan\nIntro.\n\n### Automated\n- [ ] a", section: "Test Plan",
			text: "More intro.\n\n### Automated\n- b",
			want: "## Test Plan\nIntro.\n\nMore intro.\n\n### Automated\n- [ ] a\n- b\n",
		},
		{
			name: "prose under a sub-heading is set apart", body: "## Design\n### Risks\nNone known.", section: "Design",
			text: "### Risks\nLoad.",
			want: "## Design\n### Risks\nNone known.\n\nLoad.\n",
		},
		{
			name: "an empty sub-heading gets the lines right under it", body: "## Design\n### Risks\n\n### Plan\nx", section: "Design",
			text: "### Risks\n- load",
			want: "## Design\n### Risks\n- load\n\n### Plan\nx\n",
		},
		{
			name: "deeper headings travel with their group", body: "## Design\n### Plan\n- a", section: "Design",
			text: "### Plan\n- b\n#### Detail\n- c",
			want: "## Design\n### Plan\n- a\n- b\n#### Detail\n- c\n",
		},
		{
			name: "a sub-heading of another level is not merged", body: "## Design\n### Risks\n- a", section: "Design",
			text: "#### Risks\n- b",
			want: "## Design\n### Risks\n- a\n\n#### Risks\n- b\n",
		},
		{
			name: "a heading in a fence is not a sub-heading", body: "## Design\n### Plan\n- a", section: "Design",
			text: "```\n### Plan\n```",
			want: "## Design\n### Plan\n- a\n\n```\n### Plan\n```\n",
		},
		{
			name: "lines already there are not added again", body: testPlan, section: "Test Plan",
			text: "### Manual\n- [ ] click it", unchanged: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, changed, err := AppendIssueBodyToSection(tc.body, tc.section, tc.text, false)
			if err != nil {
				t.Fatalf("AppendIssueBodyToSection returned error: %v", err)
			}
			if tc.unchanged {
				if changed || body != tc.body {
					t.Fatalf("changed = %v, body = %q; want it untouched", changed, body)
				}
				return
			}
			if !changed {
				t.Fatal("changed = false, want true")
			}
			if body != tc.want {
				t.Fatalf("body =\n%s\nwant\n%s", body, tc.want)
			}
		})
	}
}

// Without --section, a body that opens with an existing section's heading
// auto-routes into it, and its sub-headings merge the same way.
func TestAppendIssueBodyAutoRouteMergesSubheadings(t *testing.T) {
	body := "## Test Plan\n### Automated\n- [ ] a\n### Manual\n- [ ] b"
	got, changed, err := AppendIssueBody(body, "## Test Plan\n### Automated\n- c")
	if err != nil || !changed {
		t.Fatalf("changed = %v, err = %v", changed, err)
	}
	want := "## Test Plan\n### Automated\n- [ ] a\n- c\n\n### Manual\n- [ ] b\n"
	if got != want {
		t.Fatalf("body =\n%s\nwant\n%s", got, want)
	}
}
