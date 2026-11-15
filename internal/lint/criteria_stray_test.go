package lint

import (
	"strings"
	"testing"
)

// strayWorkflow gives features a criteria gate (in design → backlog) and
// tweaks a path that never runs one.
const strayWorkflow = `template: custom
statuses:
  - name: idea
    description: raw
  - name: in design
    description: design
  - name: backlog
    description: ready
  - name: in progress
    description: building
  - name: done
    description: shipped
transitions:
  - from: idea
    to: in design
  - from: in design
    to: backlog
    actions:
      - type: validate
        rule: "criteria_checkboxes: Acceptance Criteria"
  - from: backlog
    to: in progress
  - from: in progress
    to: done
types:
  feature:
    description: full path
  tweak:
    description: small
    path: [idea, in progress, done]
    transitions:
      - from: idea
        to: in progress
`

const strayBrief = `## Brief

- **Why:** the reason.
- **Acceptance:**
  - [x] ` + "`TestA`" + ` passes
  - [ ] ` + "`TestB`" + ` passes
## Repro

Steps.
`

func strayIssue(title, typ, status, body string) string {
	return "---\ntitle: \"" + title + "\"\ntype: " + typ + "\nstatus: " + status + "\ncreated: \"2026-11-10\"\n---\n\n" + body
}

func strayFindings(t *testing.T, issues map[string]string) []Finding {
	t.Helper()
	var out []Finding
	for _, f := range run(t, lintProject(t, strayWorkflow, issues), "criteria") {
		if strings.Contains(f.Message, "outside ## Acceptance Criteria") {
			out = append(out, f)
		}
	}
	return out
}

// An open feature with **Acceptance:** boxes under ## Brief gets one
// finding naming the boxes, whose fix moves them into the criteria
// section; an Extra criteria line is read the same way, and boxes already
// in ## Acceptance Criteria are not flagged. Done issues are skipped.
func TestCriteriaWarnsOnBriefAcceptance(t *testing.T) {
	got := strayFindings(t, map[string]string{
		"S/brief.md":  strayIssue("brief", "feature", "in design", strayBrief+"\n## Acceptance Criteria\n\n- [ ] `TestC` passes\n"),
		"S/extra.md":  strayIssue("extra", "feature", "idea", "## Idea\n\nText.\n\nExtra criteria:\n\n- [x] a docs page walks it (test: TestDocs)\n\n## Notes\n- [ ] not a criterion\n"),
		"S/inside.md": strayIssue("inside", "feature", "in design", "## Acceptance Criteria\n\n**Acceptance:**\n- [ ] `TestD` passes\n"),
		"S/done.md":   strayIssue("done", "feature", "done", strayBrief),
	})
	if len(got) != 2 {
		t.Fatalf("got %d findings, want 2 (brief, extra): %+v", len(got), got)
	}
	byWhere := map[string]Finding{}
	for _, f := range got {
		byWhere[f.Where] = f
	}
	b, ok := byWhere["s/brief ## Brief"]
	if !ok {
		t.Fatalf("no finding at s/brief ## Brief: %+v", got)
	}
	if !strings.Contains(b.Message, "2 box(es)") || !strings.Contains(b.Message, "(B1, B2)") {
		t.Errorf("brief message %q: want both boxes by id", b.Message)
	}
	wantFix := `issue-cli append s/brief --section 'Acceptance Criteria' --body '- [ ] ` + "`TestA`" + ` passes\n- [ ] ` + "`TestB`" + ` passes', then issue-cli replace s/brief --section Brief`
	if !strings.HasPrefix(b.Fix, wantFix) {
		t.Errorf("brief fix %q, want it to start %q", b.Fix, wantFix)
	}
	e, ok := byWhere["s/extra ## Idea"]
	if !ok {
		t.Fatalf("no finding at s/extra ## Idea: %+v", got)
	}
	if !strings.Contains(e.Message, `"Extra criteria"`) || strings.Contains(e.Message, "not a criterion") || !strings.Contains(e.Message, "1 box(es)") {
		t.Errorf("extra message %q: want the one box under the label", e.Message)
	}
	if !strings.Contains(e.Fix, `issue-cli append s/extra --body "## Acceptance Criteria\n- [ ] a docs page walks it (test: TestDocs)"`) {
		t.Errorf("extra fix %q: with no criteria section yet the fix appends one", e.Fix)
	}
}

// A tweak's path runs no criteria gate, so its brief's boxes are not
// flagged.
func TestCriteriaBriefAcceptanceNoGateQuiet(t *testing.T) {
	if got := strayFindings(t, map[string]string{
		"S/tweak.md": strayIssue("tweak", "tweak", "in progress", strayBrief),
	}); len(got) != 0 {
		t.Errorf("tweak flagged: %+v", got)
	}
}
