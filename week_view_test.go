package main

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
	"github.com/michal-franc/issue-viewer/internal/weekplan"
)

// AC2: the group's room shows its latest week report, linked to the page
// that renders the file; with none yet, the command that writes one.
func TestGroupRoomShowsTheWeekReport(t *testing.T) {
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	stubLeadChat(t, false)
	withGTMGroup(&a)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()

	_, company := getBody(t, ts.URL+"/p/agents/company")
	assertContains(t, company, `data-testid="company.room.week.empty"`, "No week report yet: <code>issue-cli --project agents department week gtm --write</code>")
	if strings.Count(company, `data-testid="company.room.week`) != 1 {
		t.Error("only the group's room has a Week block")
	}

	now := time.Now()
	d := a.DepartmentByKey("gtm")
	w := weekplan.Gather(weekplan.Input{Project: &a, Now: now}, d)
	w.Counts = weekplan.Counts{GoalsMoved: 2, Shipped: 1, Reviewed: 3, Blocked: 1}
	w.Plan, w.PlanIssue = "### Marketing\n\n- Finish the pricing page", "gtm/weekly-plan-x"
	if _, _, err := weekplan.Write(&a, w); err != nil {
		t.Fatal(err)
	}
	_, company = getBody(t, ts.URL+"/p/agents/company")
	url := "/p/agents/reports/gtm/" + w.Key
	assertContains(t, company, `data-testid="company.room.week"`, `href="`+url+`"`, "Week "+w.Key,
		"2 goals moved · 1 output shipped, 3 reviewed · 1 blocked", ">with plan<", ".reports/gtm/"+w.Key+".md")

	code, page := getBody(t, ts.URL+url)
	if code != http.StatusOK {
		t.Fatalf("report page = %d", code)
	}
	assertContains(t, page, `data-testid="week_report.page"`, "GTM · week "+w.Key, "<h3>Marketing</h3>", "Finish the pricing page", ".reports/gtm/"+w.Key+".md")
	if strings.Contains(page, "&lt;!-- week:") || strings.Contains(page, `"counts"`) {
		t.Error("the header line stays out of the page")
	}
	for _, bad := range []string{"/p/agents/reports/gtm/2026-W99", "/p/agents/reports/gtm/..%2F..%2Fx", "/p/agents/reports/hq/" + w.Key, "/p/agents/reports/nope/" + w.Key, "/p/agents/reports/gtm"} {
		if code, _ := getBody(t, ts.URL+bad); code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", bad, code)
		}
	}
}
