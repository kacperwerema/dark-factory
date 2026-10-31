package main

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/michal-franc/issue-viewer/internal/chart"
	"github.com/michal-franc/issue-viewer/internal/telemetry/share"
)

// /telemetry: the owner's view of other installs' anonymous usage, read from
// a telemetry collector's store (telemetry_data: in projects.yaml). The
// viewer only reads that file; nothing is sent from here.

// TelemetryPage is the page.
type TelemetryPage struct {
	Header WbHeader
	Ranges []statsOption
	Days   int
	// Source is the collector data the page reads; "" when not configured.
	Source string
	// Empty replaces the charts when there is nothing to show.
	Empty   *WbEmpty
	Sub     string
	Skipped int
	Error   string
	Charts  []WbChartCard
	Tables  []WbTable
}

const telemetryMaxBars = 15

func (s *Server) handleTelemetry(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/telemetry" {
		http.NotFound(w, r)
		return
	}
	now := time.Now()
	data := buildTelemetryPage(s.telemetryData, r.URL.Query(), now)
	h := s.buildNeedsYouAll(now).Header
	h.Crumbs = []WbCrumb{{Label: "Map", URL: "/"}, {Label: "Telemetry, other installs", Current: true}}
	h.HideDone = true
	data.Header = h
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "telemetry_wb.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func buildTelemetryPage(source string, q url.Values, now time.Time) TelemetryPage {
	d := TelemetryPage{Source: source, Days: 30}
	key := "30d"
	for _, r := range statsRanges {
		if q.Get("range") == r.Key {
			key, d.Days = r.Key, r.Days
		}
	}
	for _, r := range statsRanges {
		d.Ranges = append(d.Ranges, statsOption{Key: r.Key, Label: r.Label, Href: "/telemetry?" + url.Values{"range": {r.Key}}.Encode(), On: r.Key == key})
	}
	if source == "" {
		d.Empty = &WbEmpty{Text: "No collector data configured. Run a collector, then point telemetry_data: in projects.yaml at its -data folder and restart the viewer:", Cmd: "go run ./cmd/telemetry-collector -data <dir>"}
		return d
	}
	batches, skipped, err := share.ReadStore(source)
	d.Skipped = skipped
	if err != nil {
		d.Error = "Can't read " + source + ": " + err.Error()
	}
	sum := share.Summarize(batches, now, d.Days)
	if sum.Batches == 0 {
		d.Empty = &WbEmpty{Text: "No batches in " + source + " for this range. Installs send one a day while sharing is on (the default) and they have the collector's URL, from a release or:", Cmd: "ISSUE_CLI_TELEMETRY_URL=<collector url>"}
		return d
	}
	d.Sub = fmt.Sprintf("%d installs · %d daily batches · %s calls, %.1f%% failed · %s to %s", sum.Installs, sum.Batches, chart.Format(chart.Count, float64(sum.Calls)), failPct(sum.Fails, sum.Calls), sum.First, sum.Last)

	// Installs per day.
	line := chart.Line{Label: "Installs per day", Unit: chart.Count, Top: 0}
	ser := chart.Series{Name: "installs", Class: "s-count"}
	for _, p := range sum.PerDay {
		t, _ := time.Parse("2006-01-02", p.Day)
		if line.From.IsZero() {
			line.From = t
		}
		line.To = t
		ser.Points = append(ser.Points, chart.Point{At: t, Value: float64(p.Installs)})
	}
	line.Series = []chart.Series{ser}
	installs := ChartCard{ID: "installs", Title: "Installs per day", Sub: "distinct installs that sent a batch for the day", SVG: line.SVG(), Table: line.Table(), Points: line.Points()}
	d.Charts = append(d.Charts, WbChartCard{Card: installs, Table: chartTable(installs), Wide: true})

	bars := func(id, title, sub string, items []share.NameCount, max int) {
		h := chart.HBar{Label: title, Unit: chart.Count}
		for i, it := range items {
			if max > 0 && i >= max {
				break
			}
			h.Bars = append(h.Bars, chart.Bar{Label: it.Name, Segments: []chart.Segment{{Name: title, Class: "s-count", Value: float64(it.Count)}}})
		}
		c := ChartCard{ID: id, Title: title, Sub: sub}
		card := WbChartCard{}
		if len(h.Bars) == 0 {
			c.Empty = "None in this range"
			card = WbChartCard{Card: c, Empty: &WbEmpty{Text: c.Empty}}
		} else {
			c.SVG, c.Table = h.SVG(), h.Table()
			card = WbChartCard{Card: c, Table: chartTable(c)}
		}
		d.Charts = append(d.Charts, card)
	}
	bars("versions", "Versions in use", "installs by the build of their newest batch", sum.Versions, telemetryMaxBars)
	bars("commands", "Most used commands", "calls", sum.Commands, telemetryMaxBars)
	least := make([]share.NameCount, 0, telemetryMaxBars)
	for i := len(sum.Commands) - 1; i >= 0 && len(least) < telemetryMaxBars; i-- {
		least = append(least, sum.Commands[i])
	}
	if len(sum.Commands) > telemetryMaxBars {
		bars("least", "Least used commands", "calls; a command no install called is not listed", least, 0)
	}
	bars("features", "Features on", "installs with the workflow feature (catalog id) in their newest batch", sum.Features, telemetryMaxBars)
	bars("templates", "Workflows in use", "installs per bundled template; custom is a workflow not stamped from one", sum.Templates, 0)
	bars("platforms", "Platforms", "installs per OS and architecture", sum.Platforms, 0)

	errs := WbTable{Key: "telemetry-errors", Title: "Error rate by command", Sub: "commands with at least one failure, highest rate first",
		Head:    []WbTH{{Text: "Command"}, {Text: "Calls", Num: true}, {Text: "Failed", Num: true}, {Text: "Rate", Num: true}, {Text: "Most common"}},
		RowHead: true, Empty: &WbEmpty{Text: "No failures in this range."}}
	for i, e := range sum.Errors {
		if i >= 30 {
			break
		}
		errs.Rows = append(errs.Rows, []WbCell{{Text: e.Command, Code: true}, {Text: fmt.Sprint(e.Calls), Num: true}, {Text: fmt.Sprint(e.Fails), Num: true}, {Text: fmt.Sprintf("%.1f%%", e.Pct), Num: true}, {Text: e.TopClass, Code: true}})
	}
	d.Tables = append(d.Tables, errs)
	return d
}

func failPct(n, of int) float64 {
	if of == 0 {
		return 0
	}
	return 100 * float64(n) / float64(of)
}
