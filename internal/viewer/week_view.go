package viewer

import (
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/weekplan"
)

// The week report in the viewer: the group room's Week block and
// /p/<project>/reports/<box>/<week>, the report rendered. Both only read
// the files `issue-cli department week <group> --write` writes under the
// work files (internal/weekplan); nothing here computes a past week.

// WbWeekReport is a group's latest week report in its room; with no URL it
// is the empty state naming the command that writes one.
type WbWeekReport struct {
	Week string
	// Line is the counts in words: goals moved, outputs shipped and
	// reviewed, blocked.
	Line string
	Plan bool
	URL  string
	// Path is the file, relative to the project root when it is under it.
	Path    string
	Command string
}

// reportURL is the viewer's page for box's report of week.
func reportURL(prefix, box, week string) string {
	return prefix + "/reports/" + box + "/" + week
}

// roomWeek is a group's Week block: its latest report, else the command.
func roomWeek(p *tracker.Project, prefix string, dept tracker.Department) *WbWeekReport {
	box := tracker.DepartmentKey(dept.Name)
	h := weekplan.Latest(p, box)
	if h == nil {
		return &WbWeekReport{Command: tracker.InjectProject(p, "issue-cli department week "+box+" --write")}
	}
	path := h.Path
	if rel, err := filepath.Rel(p.Root(), path); err == nil && !strings.HasPrefix(rel, "..") {
		path = rel
	}
	return &WbWeekReport{Week: h.Week, Line: h.Counts.Line(), Plan: h.PlanIssue != "", URL: reportURL(prefix, box, h.Week), Path: path}
}

// ReportPageData is the report page: the file rendered, and the other weeks.
type ReportPageData struct {
	Header WbHeader
	Name   string
	Dept   string
	Week   string
	Path   string
	HTML   template.HTML
	Weeks  []WbShelfTab
}

// handleWeekReport serves /p/<project>/reports/<box>/<week>: only a report file
// under the work files, by a department key and an ISO week key (no other
// path reaches the disk).
func (s *Server) handleWeekReport(w http.ResponseWriter, r *http.Request, p *tracker.Project, prefix, rest string) {
	box, week, ok := strings.Cut(rest, "/")
	d := p.DepartmentByKey(box)
	if !ok || d == nil || tracker.DepartmentKey(d.Name) != box || !weekplan.ValidKey(week) {
		http.NotFound(w, r)
		return
	}
	path := weekplan.Path(p, box, week)
	raw, err := os.ReadFile(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	company := s.buildCompany(p, prefix, time.Now())
	data := ReportPageData{Header: company.Header, Name: p.Name, Dept: d.Name, Week: week, Path: path}
	data.Header.Crumbs[1].Current, data.Header.Crumbs[1].URL = false, prefix+"/company"
	data.Header.Crumbs[2] = WbCrumb{Label: d.Name + " · week " + week, Current: true}
	if rel, err := filepath.Rel(p.Root(), path); err == nil && !strings.HasPrefix(rel, "..") {
		data.Path = rel
	}
	for _, k := range weekplan.Weeks(p, box) {
		data.Weeks = append(data.Weeks, WbShelfTab{Label: k, URL: reportURL(prefix, box, k), Current: k == week})
	}
	data.HTML = template.HTML(renderMarkdown(string(raw)))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "week-report.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
