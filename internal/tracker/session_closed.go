package tracker

import (
	"fmt"
	"strings"
)

// SessionClosedPrefix starts the comment the lead tick posts when it closes
// the idle session of an issue whose work is on the shelf, waiting on the
// owner's review (agentrun.ShelvedReports): the Agents tab shows it on the
// stopped worker, and a re-dispatch's prompt quotes it
// (ResumePromptSection).
const SessionClosedPrefix = "session: closed"

// SessionClosedComment is that comment: the session, the issue's status and
// the output on the shelf.
func SessionClosedComment(session, status, output string) string {
	return fmt.Sprintf("%s %s while the issue waits on the owner's review at %s, with its output %s on the shelf: an idle session there held a worker slot. Re-dispatch resumes it from this issue, its comments and the shelf.",
		SessionClosedPrefix, session, status, output)
}

// LastSessionClosed is the newest session: closed comment, nil when there
// is none.
func LastSessionClosed(comments []Comment) *Comment {
	for i := len(comments) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(comments[i].Text), SessionClosedPrefix) {
			return &comments[i]
		}
	}
	return nil
}

// SessionClosedSinceDispatch is the newest session: closed comment when no
// dispatch: line came after it (the worker is not back yet); nil otherwise.
func SessionClosedSinceDispatch(comments []Comment) *Comment {
	for i := len(comments) - 1; i >= 0; i-- {
		text := strings.TrimSpace(comments[i].Text)
		if _, _, ok := parseDispatchComment(text); ok {
			return nil
		}
		if strings.HasPrefix(text, SessionClosedPrefix) {
			return &comments[i]
		}
	}
	return nil
}

// ResumePromptSection tells a re-dispatched worker that an earlier session
// of the issue was closed while it waited on the owner, and where its
// context is: the comment, the progress: comments before it, the owner's
// words after it and the output on the shelf. "" when no session was closed
// that way.
func ResumePromptSection(issue *Issue) string {
	if issue == nil {
		return ""
	}
	c := LastSessionClosed(issue.Comments())
	if c == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Resuming after a closed session\n\n")
	fmt.Fprintf(&b, "An earlier session of this issue was closed while it waited on the owner (comment #%d, %s):\n\n> %s\n\n", c.ID, c.Date, strings.Join(strings.Fields(c.Text), " "))
	fmt.Fprintf(&b, "Its work is where it left it: the issue's progress: comments before #%d say what it did and what was next, and `issue-cli outputs --issue %s` lists its output on the shelf with the owner's review. Read the comments after #%d first (the owner's review, the lead's ask): they are why you were dispatched again. Then carry on from there; don't redo what the shelf already holds.\n", c.ID, issue.Slug, c.ID)
	return b.String()
}
