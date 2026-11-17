package briefing

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// An owner answer written while no session was live (three SEO
// workers' sessions closed on 2026-11-13) is in the next session's
// briefing, in both briefing forms; a lead's comment is not the owner's.
func TestNextSessionBriefingCarriesTheOwnersAnswer(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		t.Run(fmt.Sprintf("fresh=%v", fresh), func(t *testing.T) {
			proj, issue := stageIssue(t, "in progress")
			wf := tracker.DefaultWorkflow()
			if fresh {
				wf = freshWorkflow(t, "in progress", 6000)
			}
			for _, c := range []struct{ text, by string }{
				{"question: which image source? 1 local ComfyUI, 2 OpenAI gpt-image", "agent-api-fix"},
				{"Go with option 1, the local batch", tracker.OwnerAuthor},
				{"progress: waiting on the batch", "lead-proj"},
			} {
				if err := tracker.AddCommentBy(issue.FilePath, 0, c.text, "app", c.by); err != nil {
					t.Fatal(err)
				}
			}
			dp := BuildDispatchPrompt(proj, issue, wf, t.TempDir(), nil)
			if fresh != (dp.Stage != nil) {
				t.Fatalf("fixture: stage = %+v", dp.Stage)
			}
			section := dp.Prompt[max(0, strings.Index(dp.Prompt, "## The owner's words on this issue")):]
			if !strings.HasPrefix(section, "## The owner's words") || !strings.Contains(section, ": Go with option 1, the local batch\n") {
				t.Fatalf("the briefing lacks the owner's answer:\n%s", dp.Prompt)
			}
			if strings.Contains(section, "waiting on the batch") || strings.Contains(section, "which image source") {
				t.Errorf("a worker's or lead's comment is listed as the owner's:\n%s", section)
			}
		})
	}
}

func TestOwnerWordsKeepsTheNewestFive(t *testing.T) {
	_, issue := stageIssue(t, "in progress")
	if got := tracker.OwnerWordsPromptSection(issue); got != "" {
		t.Fatalf("no owner comments, got %q", got)
	}
	for i := 1; i <= 7; i++ {
		if err := tracker.AddCommentBy(issue.FilePath, 0, fmt.Sprintf("owner note %d", i), "app", tracker.OwnerAuthor); err != nil {
			t.Fatal(err)
		}
	}
	got := tracker.OwnerWordsPromptSection(issue)
	if strings.Contains(got, "owner note 2\n") || !strings.Contains(got, "owner note 3\n") || !strings.Contains(got, "owner note 7\n") {
		t.Errorf("not the newest five:\n%s", got)
	}
	if !strings.Contains(got, "- and 2 older: issue-cli show api/fix") {
		t.Errorf("no pointer to the older ones:\n%s", got)
	}
}
