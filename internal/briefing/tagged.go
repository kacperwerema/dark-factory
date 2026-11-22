package briefing

import (
	"time"

	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Limits for the memories a work-status briefing carries for the folders
// the issue touches: one long memory and a short one, or three short ones.
const (
	TaggedMemoryLimit = 3
	TaggedMemoryChars = 900
)

// IssueFiles is what the issue touches: its branch's changed files, then
// the repository paths its body names (affected's planned files), each
// once. At the start of the work the branch has none yet, so the body's
// paths stand for the change.
func IssueFiles(proj *tracker.Project, issue *tracker.Issue) []string {
	if proj == nil || issue == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(fs []string) {
		for _, f := range fs {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	if d, err := tracker.BranchChanges(proj.TelemetryRoot(), issue); err == nil {
		add(d.Files)
	}
	add(tracker.PlannedFiles(proj.Root(), issue.BodyRaw))
	return out
}

// WorkTags is the memory tags (memory.PathTags) of the folders the issue
// touches while it is at its workflow's work status (in progress), none
// at any other status.
func WorkTags(proj *tracker.Project, issue *tracker.Issue) []string {
	if proj == nil || issue == nil {
		return nil
	}
	wf := proj.LoadWorkflowForIssue(issue)
	if wf == nil || !wf.HasRole(issue.Status, tracker.StatusRoleWork) {
		return nil
	}
	return memory.TagsForFiles(IssueFiles(proj, issue))
}

// TaggedMemories is the memories of mems a work-status briefing for issue
// carries for the folders it touches (WorkTags), leaving out the ids in
// skip, which it already shows.
func TaggedMemories(proj *tracker.Project, issue *tracker.Issue, mems []memory.Memory, skip map[string]bool, now time.Time) (memory.Pins, []string) {
	tags := WorkTags(proj, issue)
	return memory.ForTags(mems, tags, skip, now, TaggedMemoryLimit, TaggedMemoryChars), tags
}
