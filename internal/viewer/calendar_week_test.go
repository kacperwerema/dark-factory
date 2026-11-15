package viewer

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
	_ "time/tzdata" // Europe/Warsaw on a machine without a zoneinfo database

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// TestWeekWindowsCountCalendarDays pins now on the day after each DST
// change: the 7-day windows hold the whole seventh day and nothing of the
// eighth, on every page that counts one. A cut at now minus 7×24h misses
// the first in autumn (a 25-hour day) and lets the second in in spring (a
// 23-hour day).
func TestWeekWindowsCountCalendarDays(t *testing.T) {
	warsaw, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name                 string
		now, seventh, eighth time.Time
	}{
		{
			name:    "autumn",
			now:     time.Date(2026, 10, 26, 0, 30, 0, 0, warsaw),
			seventh: time.Date(2026, 10, 19, 0, 15, 0, 0, warsaw),
			eighth:  time.Date(2026, 10, 18, 23, 50, 0, 0, warsaw),
		},
		{
			name:    "spring",
			now:     time.Date(2026, 3, 30, 0, 30, 0, 0, warsaw),
			seventh: time.Date(2026, 3, 23, 0, 15, 0, 0, warsaw),
			eighth:  time.Date(2026, 3, 22, 23, 50, 0, 0, warsaw),
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := setupAgentsProject(t)
			p.Departments = []tracker.Department{{Name: "Ops"}}
			done := func(slug string, at time.Time) {
				t.Helper()
				body := "---\ntitle: \"" + slug + "\"\nstatus: \"done\"\ndepartment: \"Ops\"\ncreated: \"2026-01-01\"\ndone_at: \"" + at.Format(time.RFC3339) + "\"\n---\n\nBody.\n"
				if err := os.WriteFile(filepath.Join(p.IssueDir, slug+".md"), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			done("seventh", c.seventh)
			done("eighth", c.eighth)
			writeComments(t, p, "loner.md",
				[4]string{c.seventh.Format(time.RFC3339), "progress: on the seventh day", "cli", "a"},
				[4]string{c.eighth.Format(time.RFC3339), "progress: on the eighth day", "cli", "a"},
			)
			for _, f := range []string{"seventh.md", "eighth.md", "loner.md"} {
				if err := os.Chtimes(filepath.Join(p.IssueDir, f), c.seventh, c.seventh); err != nil {
					t.Fatal(err)
				}
			}
			issues, err := tracker.LoadIssues(p.IssueDir)
			if err != nil {
				t.Fatal(err)
			}

			// Only this fixture's issue: the shared project's comments are
			// dated after a March now.
			var loner []*tracker.Issue
			for _, issue := range issues {
				if issue.Slug == "loner" {
					loner = append(loner, issue)
				}
			}
			radio := radioComments(&p, "/p/agents", loner, c.now, 8)
			if len(radio) != 1 || radio[0].Text != "on the seventh day" {
				t.Errorf("radio = %+v, want the seventh day's line only", radio)
			}
			if os.Chtimes(filepath.Join(p.IssueDir, "loner.md"), c.eighth, c.eighth) != nil {
				t.Fatal("chtimes")
			}
			if got := radioComments(&p, "/p/agents", loner, c.now, 8); len(got) != 0 {
				t.Errorf("radio read a file last written on the eighth day: %+v", got)
			}

			m := buildStatus(&p, issues, nil, c.now)
			if len(m.DoneRecent) != 1 || m.DoneRecent[0].Issue.Slug != "seventh" {
				t.Errorf("status done recent = %+v, want seventh only", m.DoneRecent)
			}

			srv, err := NewServer([]tracker.Project{p})
			if err != nil {
				t.Fatal(err)
			}
			if lot, _ := srv.lotCore(&p, "", c.now); lot.DoneWeek != 1 {
				t.Errorf("map done this week = %d, want 1", lot.DoneWeek)
			}
			room := companyDeptRoom(&p, "/p/agents", p.Departments[0], 0, issues, nil, nil, nil, c.now)
			if len(room.Foot) == 0 || room.Foot[0].Text != "✓ 1 this week" {
				t.Errorf("room foot = %+v, want ✓ 1 this week", room.Foot)
			}
			created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
			want := fmt.Sprintf("%.1f days", c.seventh.Sub(created).Hours()/24)
			var avg string
			for _, r := range hallNumbers(&p, issues, nil, 1, c.now).Rows {
				if r.Label == "Avg. created → done" {
					avg = r.Value
				}
			}
			if avg != want {
				t.Errorf("hall avg = %q, want %q (the seventh day's issue alone)", avg, want)
			}
		})
	}
}
