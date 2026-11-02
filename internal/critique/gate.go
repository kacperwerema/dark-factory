package critique

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func init() {
	tracker.SetCritiqueChecker(func(w *tracker.WorkflowConfig, issue *tracker.Issue) error {
		runs, err := Load(w.IssuesRoot, issue.Slug)
		if err != nil {
			return fmt.Errorf("cannot read the critique log: %w", err)
		}
		return GateError(Latest(runs, issue.Slug, issue.Status), issue.Slug, issue.Status)
	})
}

// GateError is the critique gate: nil when run (the newest from status)
// is clear or overridden. Otherwise it names what holds and the two ways
// on: fix and run the critic again, or the owner's override.
func GateError(run *Run, slug, status string) error {
	again := fmt.Sprintf("issue-cli critique %s", slug)
	if run == nil {
		return fmt.Errorf("no critique run from %s yet — a fresh critic reads the work before this gate:\n\n  %s", status, again)
	}
	if !run.Holds() {
		return nil
	}
	var b strings.Builder
	switch {
	case run.Failed:
		fmt.Fprintf(&b, "the critique run %s failed (%s) — run it again:\n\n  %s", run.ID, run.Error, again)
		return fmt.Errorf("%s", b.String())
	default:
		n := run.Report.Count(Blocks) + len(run.Report.Problems)
		fmt.Fprintf(&b, "the critic found %d blocks finding%s (run %s):\n", n, plural(n), run.ID)
		for _, f := range run.Report.Of(Blocks) {
			// continuation lines one level deeper than the findings
			b.WriteString("  " + strings.ReplaceAll(f.String(), "\n", "\n  ") + "\n")
		}
		for _, p := range run.Report.Problems {
			b.WriteString("  blocks " + p + "\n")
		}
	}
	fmt.Fprintf(&b, "\nFix each, then run the critic again (the new run is what counts):\n\n  %s\n\nOr the owner overrides: issue-cli critique %s --override --quote \"<the owner's words>\"", again, slug)
	return fmt.Errorf("%s", b.String())
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// CommentText is the critique: comment a run leaves on the issue: the
// verdict, counts, blocks and should findings with path:line, malformed
// ones, Coverage and Not read. Notes stay in the log.
func CommentText(r *Run) string {
	var b strings.Builder
	rep := r.Report
	fmt.Fprintf(&b, "critique: %s → %s, %s (%s) — %d blocks, %d should, %d note", r.From, r.To, r.Verdict(), valueOr(r.Agent, "critic"),
		rep.Count(Blocks), rep.Count(Should), rep.Count(Note))
	if r.Fallback != "" {
		fmt.Fprintf(&b, "; %s", r.Fallback)
	}
	if r.Failed {
		fmt.Fprintf(&b, "; failed: %s", r.Error)
	}
	for _, f := range rep.Findings {
		if f.Severity == Note && !f.Malformed {
			continue
		}
		b.WriteString("\n- " + f.String())
	}
	for _, p := range rep.Problems {
		b.WriteString("\n- blocks " + p)
	}
	if rep.Coverage != "" {
		b.WriteString("\nCoverage: " + rep.Coverage)
	}
	if len(rep.Unread) > 0 {
		b.WriteString("\nNot read: " + strings.Join(rep.Unread, ", "))
	}
	return b.String()
}

func valueOr(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}
