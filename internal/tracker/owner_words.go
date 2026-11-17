package tracker

import (
	"fmt"
	"strings"
)

// Limits for OwnerWordsPromptSection: the newest comments, each cut.
const (
	ownerWordsLimit = 5
	ownerWordsRunes = 400
)

// OwnerWordsPromptSection is the "## The owner's words on this issue" part
// of a worker's briefing: the owner's newest comments on the trail (a reply
// to a question:, a clarify answer's note), so a session started after the
// owner answered, with none live to hear it, starts from the answer. The
// dispatched issue-cli start prints only the comments made after the
// briefing, so without this an answer written between two sessions reached
// the next one only through issue-cli show. "" when the owner wrote none.
func OwnerWordsPromptSection(issue *Issue) string {
	var owner []Comment
	for _, c := range issue.Comments() {
		if CommentAuthor(c) == OwnerAuthor && strings.TrimSpace(c.Text) != "" {
			owner = append(owner, c)
		}
	}
	if len(owner) == 0 {
		return ""
	}
	older := 0
	if len(owner) > ownerWordsLimit {
		older = len(owner) - ownerWordsLimit
		owner = owner[older:]
	}
	var b strings.Builder
	b.WriteString("\n\n## The owner's words on this issue\n\n")
	b.WriteString("The owner's newest comments, oldest first: an answer to your question: comment is here even when no session was live to get it.\n")
	for _, c := range owner {
		text := strings.Join(strings.Fields(c.Text), " ")
		if r := []rune(text); len(r) > ownerWordsRunes {
			text = strings.TrimSpace(string(r[:ownerWordsRunes])) + "…"
		}
		fmt.Fprintf(&b, "- #%d %s: %s\n", c.ID, c.Date, text)
	}
	if older > 0 {
		fmt.Fprintf(&b, "- and %d older: issue-cli show %s\n", older, issue.Slug)
	}
	return b.String()
}
