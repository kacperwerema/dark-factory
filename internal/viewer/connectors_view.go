package viewer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kacperwerema/dark-factory/internal/connectors"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// connectorsTTL is how long a project's connector listing is reused:
// every page's Needs you count reads it, and a Keychain probe runs
// security once per secret.
const connectorsTTL = 30 * time.Second

var connectorsCache = struct {
	sync.Mutex
	m map[string]connectorsEntry
}{m: map[string]connectorsEntry{}}

type connectorsEntry struct {
	at   time.Time
	list []connectors.Status
}

// projectConnectors is connectors.List(p, "") through connectorsCache.
func projectConnectors(p *tracker.Project) []connectors.Status {
	if len(p.Departments) == 0 {
		return nil
	}
	key := p.IssueDir
	connectorsCache.Lock()
	e, ok := connectorsCache.m[key]
	connectorsCache.Unlock()
	if ok && time.Since(e.at) < connectorsTTL {
		return e.list
	}
	list := connectors.List(p, "")
	connectorsCache.Lock()
	connectorsCache.m[key] = connectorsEntry{at: time.Now(), list: list}
	connectorsCache.Unlock()
	return list
}

// forgetConnectors drops p's cached listing (after a check).
func forgetConnectors(p *tracker.Project) {
	connectorsCache.Lock()
	delete(connectorsCache.m, p.IssueDir)
	connectorsCache.Unlock()
}

// WbConnectors is a department room's connectors: the summary line and,
// unfolded, a row per connector with Check now.
type WbConnectors struct {
	Count, Failing int
	Rows           []WbConnectorRow
	Dept           string
	CheckURL       string
	StateURL       string
	// Since is set (unix seconds) while a Check now started then runs.
	Since string
}

// WbConnectorRow is one connector in the room. Secrets only say set or
// missing; no value ever reaches the page.
type WbConnectorRow struct {
	Name, Kind, Note string
	Secrets          []WbSecret
	Sends            bool
	// Check is "✓ ok 3h ago", "✗ failing (exit 1) 2m ago", "never checked"
	// or "no check:"; Tone green, red or idle.
	Check, Tone string
}

// WbSecret is one secret's env var, reference and state.
type WbSecret struct {
	Var, Ref string
	Set      bool
}

// roomConnectors is department dept's block, nil when it has none.
func roomConnectors(p *tracker.Project, prefix, dept string, now time.Time) *WbConnectors {
	key := tracker.DepartmentKey(dept)
	var out WbConnectors
	for _, s := range projectConnectors(p) {
		if s.Key != key {
			continue
		}
		row := WbConnectorRow{Name: s.Name, Kind: s.Kind, Note: s.Note, Sends: s.Sends, Tone: "idle"}
		for _, sec := range s.Secrets {
			row.Secrets = append(row.Secrets, WbSecret{Var: sec.Var, Ref: sec.Ref, Set: sec.Set})
		}
		switch {
		case !s.HasCheck:
			row.Check = "no check:"
		case s.Last == nil:
			row.Check = "never checked"
		default:
			row.Check = s.Last.Result + " " + shortDuration(now.Sub(s.Last.Date)) + " ago"
			switch s.Last.Result {
			case connectors.ResultOK:
				row.Check, row.Tone = "✓ "+row.Check, "green"
			case connectors.ResultFailing:
				row.Check, row.Tone = fmt.Sprintf("✗ failing (exit %d) %s ago", s.Last.Exit, shortDuration(now.Sub(s.Last.Date))), "red"
			case connectors.ResultMissing:
				row.Check, row.Tone = "✗ "+row.Check, "red"
			}
		}
		if s.Failing() {
			out.Failing++
		}
		out.Rows = append(out.Rows, row)
	}
	if len(out.Rows) == 0 {
		return nil
	}
	out.Count = len(out.Rows)
	out.Dept = key
	out.CheckURL = prefix + "/connectors/check"
	out.StateURL = prefix + "/connectors.json?dept=" + url.QueryEscape(key)
	return &out
}

// connectorWaits are the Needs you rows: one per department with a
// connector whose secret is missing or whose last check failed.
func connectorWaits(p *tracker.Project, color, prefix string, now time.Time) []WbQueueCard {
	var rows []WbQueueCard
	byDept := map[string][]string{}
	var order []string
	for _, s := range projectConnectors(p) {
		if !s.Failing() {
			continue
		}
		if _, ok := byDept[s.Department]; !ok {
			order = append(order, s.Department)
		}
		what := s.Name + " check failing"
		if s.Missing {
			what = s.Name + " secret missing"
		}
		byDept[s.Department] = append(byDept[s.Department], what)
	}
	for _, dept := range order {
		rows = append(rows, WbQueueCard{
			Kind: "connectors", Label: "Connectors", Tone: "red",
			Project: p.Name, ProjectSlug: p.Slug, Color: color,
			Slug: dept, Note: dept + ": " + strings.Join(byDept[dept], ", ") + " (issue-cli connectors " + tracker.DepartmentKey(dept) + ").",
			Since: now, CTA: "Open room", URL: prefix + "/company#dept-" + tracker.DepartmentKey(dept),
		})
	}
	return rows
}

// handleConnectorsCheck is Check now: the department's checks run in the
// background, and the page comes back polling connectors.json.
func (s *Server) handleConnectorsCheck(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	d := proj.DepartmentByKey(r.FormValue("dept"))
	if d == nil || len(d.Connectors) == 0 {
		http.Error(w, "no such department with connectors", http.StatusBadRequest)
		return
	}
	dept := *d
	started := time.Now()
	s.bg.Go("connector checks "+proj.Slug+"/"+tracker.DepartmentKey(dept.Name), func() {
		ctx, cancel := context.WithCancel(s.bg.hard)
		defer cancel()
		var recs []connectors.Record
		for i := range dept.Connectors {
			recs = append(recs, connectors.RunCheck(ctx, dept.Name, &dept.Connectors[i], time.Now))
		}
		_ = connectors.Append(proj, recs...)
		forgetConnectors(proj)
	})
	key := tracker.DepartmentKey(dept.Name)
	http.Redirect(w, r, fmt.Sprintf("%s/company?checking=%d&dept=%s#dept-%s", prefix, started.Unix(), url.QueryEscape(key), key), http.StatusSeeOther)
}

// handleConnectorsJSON lists the connectors (?dept= for one department):
// names, references and set / missing, never a value.
func (s *Server) handleConnectorsJSON(w http.ResponseWriter, r *http.Request, proj *tracker.Project) {
	dept := ""
	if key := r.URL.Query().Get("dept"); key != "" {
		d := proj.DepartmentByKey(key)
		if d == nil {
			http.NotFound(w, r)
			return
		}
		dept = d.Name
	}
	list := connectors.List(proj, dept)
	if list == nil {
		list = []connectors.Status{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}
