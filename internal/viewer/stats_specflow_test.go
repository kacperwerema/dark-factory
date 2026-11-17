package viewer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/critique"
	"github.com/kacperwerema/dark-factory/internal/postmortem"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// specFlowCard is the table card key's HTML on page.
func specFlowCard(t *testing.T, page, key string) string {
	t.Helper()
	i := strings.Index(page, `data-testid="stats.table_card.`+key+`"`)
	if i < 0 {
		t.Fatalf("no %s card on the Stats page", key)
	}
	card := page[i:]
	return card[:strings.Index(card, "</section>")]
}

// The Workshop Stats page has a Spec flow section with four table cards:
// rows link to their issues, totals sit in each card's sub line, and an
// empty card names the command that fills it.
func TestStatsSpecFlowSection(t *testing.T) {
	withMockTmuxSessions(t, nil)
	withStubTelemetryReport(t, func(*tracker.Project) ([]byte, error) { return []byte(`{"v":1,"enabled":true}`), nil })

	// Empty: each card says what fills it, and no table is drawn.
	proj, tmp := setupTestProject(t)
	proj.WorkDir = tmp
	ts := newTestServer(t, []tracker.Project{proj})
	_, page := getBody(t, ts.URL+"/p/test-project/stats")
	ts.Close()
	if !strings.Contains(page, `data-testid="stats.specflow"`) {
		t.Fatal("no Spec flow section")
	}
	for key, cmd := range map[string]string{
		"specflow-critique": "critique &lt;slug&gt;", "specflow-clarify": "clarify &lt;slug&gt;",
		"specflow-redrounds": "rounds &lt;slug&gt;", "specflow-postmortem": "postmortem list",
	} {
		card := specFlowCard(t, page, key)
		if !strings.Contains(card, `data-copy="issue-cli --project test-project `+cmd+`"`) || strings.Contains(card, "<table") || strings.Contains(card, "wb-trend-need") {
			t.Errorf("empty %s card:\n%s", key, card)
		}
	}

	// With records.
	proj, tmp = setupTestProject(t)
	proj.WorkDir = tmp
	now := time.Now()
	q, _ := json.Marshal(tracker.Comment{ID: 1, Date: now.Add(-2 * time.Hour).Format(time.RFC3339), Text: "question: red rounds: make validate — red 4 of 4", Status: "open"})
	body := "---\ntitle: \"Widget\"\nstatus: \"in progress\"\ncreated: \"" + now.Format("2006-01-02") + "\"\n---\n\n## Clarify\n\n### Q1. Which?\n1. **A** (recommended) why. Cost: none.\n**Answer:** assumed — took the recommendation\n\n<!-- issue-viewer-comments\n" + string(q) + "\n-->\n"
	if err := os.WriteFile(filepath.Join(proj.IssueDir, "widget.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	held := &critique.Run{Slug: "widget", From: "in design", To: "backlog", At: now.Add(-3 * time.Hour), Agent: "codex", TookMS: 65_000,
		Report: critique.Report{Findings: []critique.Finding{{Severity: critique.Blocks, Path: "a.go", Text: "no test"}}}}
	again := &critique.Run{Slug: "widget", From: "in design", To: "backlog", At: now.Add(-time.Hour), Agent: "codex",
		Report: critique.Report{Findings: []critique.Finding{{Severity: critique.Blocks, Path: "a.go", Text: "still no test"}}}}
	for _, r := range []*critique.Run{held, again} {
		if err := critique.Record(proj.IssueDir, r); err != nil {
			t.Fatal(err)
		}
	}
	pmBody := "## Proposed actions\n\n- [ ] A1: Write it\n  Differently: would write it\n  Place: none docs/x.md#y\n  Refused: not now\n"
	if _, err := postmortem.Save(filepath.Join(tmp, "retros"), postmortem.Meta{Issue: "widget", Title: "Widget"}, pmBody, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	ts = newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	_, page = getBody(t, ts.URL+"/p/test-project/stats")

	crit := specFlowCard(t, page, "specflow-critique")
	for _, want := range []string{"2 runs (2 gated, 0 failed) · 2 blocks, 0 should, 0 note · held a gate 2: fixed 0, overridden 0, held again 1, still holds 1 · took: median 1m5s (1 of 2 timed; tokens not recorded)",
		`<a href="/p/test-project/issue/widget">widget</a>`, "in design → backlog", "held again", "still holds", "1m5s", "—", "1 · 0 · 0"} {
		if !strings.Contains(crit, want) {
			t.Errorf("critique card lacks %q:\n%s", want, crit)
		}
	}
	if !strings.Contains(page, `class="wb-tcard wb-tcard--wide" data-testid="stats.table_card.specflow-critique"`) {
		t.Error("the critique card doesn't span the row")
	}
	for key, want := range map[string]string{
		"specflow-clarify":    "1 design (0 with nothing to ask) · 1 question, 1.0 per design: 0 owner-answered, 1 assumed, 0 open",
		"specflow-redrounds":  "1 question on 1 issue · 1 open, 0 resolved",
		"specflow-postmortem": "1 action proposed in 1 post-mortem · 0 applied, 1 refused, 0 overtaken, 0 waiting",
	} {
		card := specFlowCard(t, page, key)
		if !strings.Contains(card, want) || !strings.Contains(card, `href="/p/test-project/issue/widget"`) {
			t.Errorf("%s card lacks %q or the issue link:\n%s", key, want, card)
		}
	}
}
