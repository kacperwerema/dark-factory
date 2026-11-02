package tracker

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	// ErrNoBackTarget: the issue is at the start of its path, at its end
	// (done) or on a side status, so there is nothing to send it back to.
	ErrNoBackTarget = errors.New("no status to go back to")
	// ErrBadBackTarget: the asked-for status is not earlier on the path.
	ErrBadBackTarget = errors.New("not an earlier status on the issue's path")
)

// BackTarget is where a send-back moves an issue at status: to when given
// (an earlier status on the path, or status itself for no move), else the
// status before it. The path is the non-optional statuses, so a type's path
// and side statuses like parked are respected.
func (w *WorkflowConfig) BackTarget(status, to string) (string, error) {
	var path []string
	for _, s := range w.Statuses {
		if !s.Optional {
			path = append(path, s.Name)
		}
	}
	at := -1
	for i, s := range path {
		if strings.EqualFold(s, status) {
			at = i
		}
	}
	if at <= 0 || at == len(path)-1 {
		return "", ErrNoBackTarget
	}
	if to == "" {
		return path[at-1], nil
	}
	for i := 0; i <= at; i++ {
		if strings.EqualFold(path[i], to) {
			return path[i], nil
		}
	}
	return "", fmt.Errorf("%q: %w", to, ErrBadBackTarget)
}

// MoveBack moves the issue at filePath back to toStatus, the owner's call.
// An edge the workflow declares (from → toStatus) runs as a transition with
// its actions; otherwise the move is an override: the status changes, the
// approvals are cleared, the assignee stays, no forward check or side-effect
// runs, and the transition stat is recorded so the Timeline counts rework.
// It returns the status the issue left.
func (w *WorkflowConfig) MoveBack(filePath, toStatus string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", filePath, err)
	}
	issue, err := ParseIssue(filepath.Base(filePath), data)
	if err != nil {
		return "", err
	}
	if t := w.ResolveTransition(issue.Status, toStatus); t != nil {
		from, _, err := w.ApplyTransitionToFileWithFields(filePath, toStatus, nil)
		return from, err
	}
	seen := issue.Status
	err = withIssueLock(filePath, func() error {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("reading %s: %w", filePath, err)
		}
		issue, err := ParseIssue(filepath.Base(filePath), data)
		if err != nil {
			return err
		}
		if issue.Status != seen {
			return fmt.Errorf("%s moved to %q meanwhile: %w", filePath, issue.Status, ErrInvalidTransition)
		}
		if fi, ti := w.GetStatusIndex(seen), w.GetStatusIndex(toStatus); fi < 0 || ti < 0 || ti >= fi {
			return fmt.Errorf("%q is not before %q: %w", toStatus, seen, ErrBadBackTarget)
		}
		rawBody, err := ParseFrontmatter(string(data), &Issue{})
		if err != nil {
			return fmt.Errorf("parsing current issue state for %s: %w", filePath, err)
		}
		_, comments := ParseComments(rawBody)
		update := IssueUpdate{Status: stringPtr(toStatus)}
		if issue.HumanApproval != "" {
			update.HumanApproval = stringPtr("")
		}
		if issue.QueuedApproval != "" {
			update.QueuedApproval = stringPtr("")
		}
		if err := updateIssueFrontmatterLocked(filePath, update); err != nil {
			return err
		}
		return w.recordTransitionStat(filePath, folderSlug(w.IssuesRoot, filePath, issue.Slug), seen, toStatus, issue.BodyRaw, comments, "")
	})
	return seen, err
}

const rejectedPrefix = "rejected: "

// RejectedComment is the comment a send-back writes with the owner's words:
//
//	rejected: "<words>" (<how>, <date>) → back to <status>
//	rejected: "<words>" (<how>, <date>) → stays in <status>
//
// IsRejectedComment reports whether text is a comment RejectedComment wrote.
func IsRejectedComment(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), rejectedPrefix)
}

func RejectedComment(words, how, date, from, to string) string {
	where := "back to " + to
	if strings.EqualFold(from, to) {
		where = "stays in " + to
	}
	return fmt.Sprintf("%s%q (%s, %s) → %s", rejectedPrefix, words, how, date, where)
}
