package outputs

import (
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Shelved is the output that ends an issue's work for its worker: the
// issue's path gates on has_output (the report type), it is at or past that
// gate and not parked, and the shelf holds a good output (not rejected, its
// file still there) of the gate's kind. Such an issue waits on the owner's
// review, and its worker has nothing left to do until the owner answers.
// The newest such output; nil otherwise. wf is the issue's workflow
// (LoadWorkflowForIssue); whether the issue is closed is the caller's check.
func Shelved(list []*Output, wf *tracker.WorkflowConfig, issue *tracker.Issue) *Output {
	if wf == nil || issue == nil || tracker.IsParkedStatus(issue.Status) {
		return nil
	}
	to, kind, ok := wf.HasOutputRule()
	if !ok {
		return nil
	}
	at, gate := wf.GetStatusIndex(issue.Status), wf.GetStatusIndex(to)
	if at < 0 || gate < 0 || at < gate {
		return nil
	}
	var newest *Output
	for _, o := range Select(list, Filter{Issue: issue.Slug}) {
		if o.State == StateRejected || o.Missing || (kind != "" && !strings.EqualFold(strings.TrimSpace(o.Kind), kind)) {
			continue
		}
		if newest == nil || o.Added.After(newest.Added) {
			newest = o
		}
	}
	return newest
}
