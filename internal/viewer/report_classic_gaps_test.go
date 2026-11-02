package viewer

import (
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/privacy"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// TestReportCarriesTheClassicStatusRows: what only the classic Status page
// showed is on the Report now: Working on, the CEO's whole plan, backlog
// and parked links, Recently released, and links to the hall and Health.
func TestReportCarriesTheClassicStatusRows(t *testing.T) {
	page := getReport(t, setupStatusProject(t, time.Now()))
	assertContains(t, page,
		// Working on: an in-flight issue with its status, age and agent.
		`data-testid="report.active"`, `data-testid="report.active.row.ui/asker"`, `>agent stopped<`, `href="/p/status/agents">Agents</a>`,
		// The CEO's ## Next in full, slugs linked.
		`data-testid="report.next.full"`, `<summary>Full plan</summary>`, `href="/p/status/issue/ui/ready"`,
		// Backlog and parked as links.
		`href="/p/status/?status=backlog" data-testid="report.waiting.link">in backlog</a>`,
		`href="/p/status/hall/station/parked" data-testid="report.waiting.link">parked</a>`,
		// Recently released from the changelog.
		`data-testid="report.released"`, `data-testid="report.released.row.v0_4_0"`, `<strong>Breaking:</strong>`,
	)
	if strings.Contains(page, "/board?classic=1") {
		t.Error("the Report links the classic board")
	}
}

// TestReportBuildersFromTheClassicPage covers the rows the fixture above
// doesn't reach: stale ages, agents on issues not started, the milestone
// by status and area, every version, privacy at every level, health links.
func TestReportBuildersFromTheClassicPage(t *testing.T) {
	now := time.Now()
	a := &tracker.Issue{Slug: "ui/a", Title: "A", Status: "in progress"}
	d := &tracker.Issue{Slug: "ui/d", Title: "D", Status: "in design"}
	m := &StatusModel{Prefix: "/p/x", AsOf: now,
		Active:   []ActiveRow{{Issue: a, Agent: "working", Gate: "testing", Age: Age{Text: "9d", Level: "stale", Since: now.Add(-9 * 24 * time.Hour)}}},
		InDesign: []AgentChip{{Issue: d, State: "at gate"}},
		NotReady: 1, Waiting: []WaitingItem{{Kind: "gate", Issue: a}},
		Versions:         []Progress{{Label: "0.1", Done: 1, Total: 2}, {Label: "0.2"}, {Label: "0.3"}, {Label: "0.4"}, {Label: "0.5", Total: 1}},
		VersionsProgress: Progress{Done: 1, Total: 3},
		Milestone: &MilestoneView{Version: "0.1", Cut: "v0.1.0",
			Segments:  []StatusSegment{{Status: "done", Count: 1, Done: true}, {Status: "in progress", Count: 1}},
			Legend:    []StatusSegment{{Status: "done", Count: 1, Done: true}, {Status: "in progress", Count: 1}},
			OpenAreas: []AreaCount{{Area: "UI", Count: 1}}, OpenEpics: 1},
		Health:  &HealthCard{Proposed: 2, Lint: "3 lint warnings"},
		Privacy: &PrivacyView{Badges: []PrivacyBadge{{Badge: privacy.Badge{Kind: "work", Level: privacy.Green, Label: "Work private"}}, {Badge: privacy.Badge{Kind: "code", Level: privacy.Grey, Label: "Code public", Note: "To make it public: …"}}}},
	}
	act := reportActive(m, "/p/x")
	if len(act.Rows) != 1 || act.Rows[0].Tone != "red" || act.Rows[0].Agent != "agent working → testing" || len(act.Design) != 1 || act.Design[0].Tone != "you" {
		t.Errorf("active = %+v", act)
	}
	w := reportWhere(m, []*tracker.Issue{{Version: "0.1"}}, "/p/x")
	if len(w.Milestones) != reportVersions || len(w.MoreMilestones) != 1 || w.VersionsTotal != "1/3 done over 5 versions" {
		t.Errorf("versions = %d + %d, %q", len(w.Milestones), len(w.MoreMilestones), w.VersionsTotal)
	}
	if len(w.Segments) != 2 || w.Segments[0].Color != reportWorkingColor || len(w.Areas) != 2 || w.Areas[1].Text != "UI 1" {
		t.Errorf("milestone = %+v %+v", w.Segments, w.Areas)
	}
	if c := reportWaiting(m); c.NoteLink == nil || c.NoteLink.URL != "/p/x/hall" {
		t.Errorf("not-ready link = %+v", c.NoteLink)
	}
	if h := reportHealth(m, &tracker.Project{Slug: "x"}, "/p/x"); h.Recs != 2 {
		t.Errorf("recs = %d", h.Recs)
	}
	r := buildReport(&tracker.Project{Slug: "x", Name: "X"}, "#fff", m, nil, nil)
	if len(r.Title.Badges) != 2 || r.Title.Badges[0].Tone != "green" || r.Title.Badges[1].Tone != "idle" || r.Title.Badges[1].URL != "#unlock-code" {
		t.Errorf("badges = %+v", r.Title.Badges)
	}
	for _, it := range r.Unlock.Items {
		if it.Key == "code" && it.Note == "" {
			t.Error("the code item lost the privacy note")
		}
	}
	rel := reportReleased(&StatusModel{Milestone: &MilestoneView{Cut: "v0.1.0"}})
	if rel.First != "No releases yet: v0.1.0 will be the first." || !rel.Hint {
		t.Errorf("released = %+v", rel)
	}

	srv, err := NewServer(nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]any{"wb/active-list": act, "wb/released": rel, "wb/where": w, "wb/health": reportHealth(m, &tracker.Project{Slug: "x"}, "/p/x")} {
		var b strings.Builder
		if err := srv.tmpl.ExecuteTemplate(&b, name, data); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out := b.String()
		switch name {
		case "wb/active-list":
			assertContains(t, out, `data-testid="report.active.design"`, `wb-chip wb-chip--red`)
		case "wb/released":
			assertContains(t, out, `No releases yet: v0.1.0 will be the first.`, `workbench.changelog`)
		case "wb/where":
			assertContains(t, out, `data-testid="report.where.milestone"`, `data-testid="report.where.areas"`, `+1 more versions`, `1/3 done over 5 versions`)
		case "wb/health":
			assertContains(t, out, `href="/p/x/health#recs" data-testid="report.health.recs">2 recommendations wait for you`, `href="/p/x/health#lint"`)
		}
	}
}

// TestStatsOverviewMarkerTones: a project's privacy markers on the stats
// overview are coloured by level, and its test ratio has a meter.
func TestStatsOverviewMarkerTones(t *testing.T) {
	srv, err := NewServer(nil)
	if err != nil {
		t.Fatal(err)
	}
	row := OverviewRow{Name: "P", Slug: "p", HasRatio: true, TestRatio: 0.4, Privacy: []PrivacyBadge{
		{Badge: privacy.Badge{Kind: "work", Level: privacy.Red, Marker: "No backup"}},
		{Badge: privacy.Badge{Kind: "code", Level: privacy.Green, Marker: "Private"}},
	}}
	var b strings.Builder
	if err := srv.tmpl.ExecuteTemplate(&b, "wb/project-stat", row); err != nil {
		t.Fatal(err)
	}
	assertContains(t, b.String(), `wb-chip wb-chip--red" data-testid="stats.overview.privacy.work"`, `wb-chip wb-chip--green" data-testid="stats.overview.privacy.code"`,
		`data-testid="stats.overview.ratio"><i style="width: 40%">`)
}
