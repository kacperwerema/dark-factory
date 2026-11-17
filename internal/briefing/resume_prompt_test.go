package briefing

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Re-dispatching an issue whose session the lead tick closed while its
// report waited on the owner resumes it: both briefing forms quote the
// session: closed comment and point at the progress: comments before it,
// the owner's words after it and the output on the shelf. A briefing with
// no such comment has no resume section.
func TestRedispatchAfterAClosedSessionResumes(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		t.Run(fmt.Sprintf("fresh=%v", fresh), func(t *testing.T) {
			proj, issue := stageIssue(t, "human-testing")
			wf := tracker.DefaultWorkflow()
			if fresh {
				wf = freshWorkflow(t, "human-testing", 8000)
			}
			if dp := BuildDispatchPrompt(proj, issue, wf, t.TempDir(), nil); strings.Contains(dp.Prompt, "## Resuming after a closed session") {
				t.Fatalf("a first dispatch has a resume section:\n%s", dp.Prompt)
			}
			for _, c := range []struct{ text, by string }{
				{"progress: report written, on the shelf as o-1; next: the owner's review", "agent-api-fix"},
				{tracker.SessionClosedComment("agent-api-fix", "human-testing", `o-1 "Report"`), "lead-proj"},
				{"Section 3 needs the costs per month", tracker.OwnerAuthor},
			} {
				if err := tracker.AddCommentBy(issue.FilePath, 0, c.text, "cli", c.by); err != nil {
					t.Fatal(err)
				}
			}
			dp := BuildDispatchPrompt(proj, issue, wf, t.TempDir(), nil)
			if fresh != (dp.Stage != nil) {
				t.Fatalf("fixture: stage = %+v", dp.Stage)
			}
			i := strings.Index(dp.Prompt, "## Resuming after a closed session")
			if i < 0 {
				t.Fatalf("no resume section:\n%s", dp.Prompt)
			}
			section := dp.Prompt[i:]
			for _, want := range []string{
				"(comment #2,", "> session: closed agent-api-fix while the issue waits on the owner's review at human-testing",
				"progress: comments before #2", "issue-cli --project proj outputs --issue", "the comments after #2 first",
			} {
				if !strings.Contains(section, want) {
					t.Errorf("resume section lacks %q:\n%s", want, section)
				}
			}
			// The owner's review after the close reaches the session too.
			if !strings.Contains(dp.Prompt, "Section 3 needs the costs per month") {
				t.Errorf("the owner's words after the close are missing:\n%s", dp.Prompt)
			}
		})
	}
}
