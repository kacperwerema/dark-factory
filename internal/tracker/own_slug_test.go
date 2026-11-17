package tracker

import "testing"

// DropOwnSlug drops the slug only after an issue-cli command on the list:
// a longer slug that starts with it, another command and prose keep it.
func TestDropOwnSlug(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"issue-cli check api/fix D1", "issue-cli check D1"},
		{"issue-cli --project p transition api/fix --to testing", "issue-cli --project p transition --to testing"},
		{"`issue-cli --project p comment api/fix \"question: ...\"`", "`issue-cli --project p comment \"question: ...\"`"},
		{"(issue-cli clarify api/fix)", "(issue-cli clarify)"},
		{"issue-cli show api/fix\nissue-cli checklist api/fix", "issue-cli show\nissue-cli checklist"},
		{"issue-cli show api/fix-two", "issue-cli show api/fix-two"},
		{"issue-cli dispatch api/fix", "issue-cli dispatch api/fix"},
		{"issue-cli ask api/fix \"...\"", "issue-cli ask api/fix \"...\""},
		{"your issue is api/fix.", "your issue is api/fix."},
	} {
		if got := DropOwnSlug(c.in, "api/fix"); got != c.want {
			t.Errorf("DropOwnSlug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := ShortenOwnSlug("You have been assigned this issue: api/fix\n\nissue-cli check <slug> <id>\n--in-force api/fix\n- api/fix#D-1 · soft", "api/fix"); got != "You have been assigned this issue: api/fix\n"+OwnSlugNote+"\n\nissue-cli check <id>\n--in-force \"$ISSUE_VIEWER_ISSUE_SLUG\"\n- (this issue) D-1 · soft" {
		t.Errorf("ShortenOwnSlug = %q", got)
	}
}
