package viewer

import (
	"fmt"

	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kacperwerema/dark-factory/internal/gitdir"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// waitLabel names what a wait was for: the approval that ended it, or the
// status it sat in.
func waitLabel(s seg, rows []statRow) string {
	for _, r := range rows {
		if r.ApprovedAt != nil && r.ApprovedAt.Equal(s.To) {
			return "approval to leave " + r.From
		}
	}
	for i, r := range rows {
		if !r.TS.After(s.From) && (i+1 == len(rows) || rows[i+1].TS.After(s.From)) {
			return "in " + r.To + ", no agent call for 15 min or more"
		}
	}
	return ""
}

// fillBands puts each event in the status band it happened in, after the
// filter, and sums each band's waiting, idle and rework time.
func fillBands(bands []lifeBand, events []tlEvent, segs []seg, show string, end time.Time, known bool) []timelineBand {
	out := make([]timelineBand, 0, len(bands)+1)
	for _, b := range bands {
		if !b.To.After(b.From) && !b.Open && len(bands) > 1 {
			continue // e.g. the first status when nothing marks the start
		}
		tb := timelineBand{Status: b.Status, Start: b.From, End: b.To, Open: b.Open}
		if known {
			tb.Working, tb.Waiting, tb.Idle, tb.Rework = b.Working, b.Waiting, b.Idle, b.Rework
		}
		out = append(out, tb)
	}
	after := timelineBand{Status: "after done", Start: end}
	before := timelineBand{Status: "before dispatch"}
	for _, e := range events {
		if !e.show[show] {
			continue
		}
		if len(bands) > 0 && e.At.Before(bands[0].From) {
			before.Events = append(before.Events, e)
			continue
		}
		if e.At.After(end) {
			after.Events = append(after.Events, e)
			continue
		}
		i := len(out) - 1
		for j, b := range out {
			if !e.At.Before(b.Start) && e.At.Before(b.End) {
				i = j
				break
			}
		}
		if i < 0 {
			continue
		}
		out[i].Events = append(out[i].Events, e)
	}
	for i := range out {
		for _, e := range out[i].Events {
			if e.Row == "event" || e.Row == "fold" {
				out[i].CallRows++
			}
		}
	}
	if len(before.Events) > 0 {
		before.Start = before.Events[0].At // no span: it isn't the agent's time
		out = append([]timelineBand{before}, out...)
	}
	if len(after.Events) > 0 {
		out = append(out, after)
	}
	return out
}

// waitItems is "Your part": each wait with what it was for.
func waitItems(segs []seg, rows []statRow, bands []lifeBand) []waitItem {
	var items []waitItem
	var max time.Duration
	for _, s := range segs {
		if s.Kind != segWaiting {
			continue
		}
		d := s.To.Sub(s.From)
		label := waitLabel(s, rows)
		if strings.HasPrefix(label, "approval to leave ") {
			label = "Approval to leave " + strings.TrimPrefix(label, "approval to leave ")
		} else if b := bandAt(bands, s.From); b != nil {
			label = "Your part in " + b.Status
		}
		items = append(items, waitItem{Label: label, Dur: d})
		if d > max {
			max = d
		}
	}
	for i := range items {
		items[i].Pct = durPct(items[i].Dur, max)
	}
	return items
}

// wordItems is "Your words": decisions with your quote, then each approval
// with its words, or "no words recorded".
func wordItems(src timelineSources) (items []wordItem, words, approvalsN int) {
	for _, d := range src.Decisions {
		if d.Quote == "" {
			continue
		}
		items = append(items, wordItem{Ref: d.ID, How: d.How, Quote: d.Quote})
		words++
	}
	said := approvalWords(src)
	for i, r := range src.Transitions {
		if r.ApprovedAt == nil {
			continue
		}
		approvalsN++
		ref := r.ApprovedAt.Local().Format("15:04")
		if a, ok := said[i]; ok {
			how := "approve → " + r.To
			if a.How != "" {
				how += " · " + a.How
			}
			items = append(items, wordItem{Ref: ref, How: how, Quote: a.Words})
			words++
			continue
		}
		items = append(items, wordItem{Ref: ref, How: "approve → " + r.To, Quote: "no words recorded", Muted: true})
	}
	return items, words, approvalsN
}

// approvalWords pairs each approved transition (by index in
// src.Transitions) with your words: the latest approval: comment made after
// the previous transition and up to this one, whose statuses name its
// target. A comment without statuses (written before the → suffix) matches
// any target, a day-only date counts when that day overlaps the window, and
// each comment pairs once.
func approvalWords(src timelineSources) map[int]tracker.ApprovalWords {
	type said struct {
		words    tracker.ApprovalWords
		at       time.Time
		dayOnly  bool
		consumed bool
	}
	var all []*said
	for _, c := range src.Comments {
		a, ok := tracker.ParseApprovalComment(c.Text)
		if !ok {
			continue
		}
		at, ok := tracker.CommentTime(c.Date)
		if !ok {
			continue
		}
		all = append(all, &said{words: a, at: at, dayOnly: tracker.CommentDay(c.Date) == strings.TrimSpace(c.Date)})
	}
	out := map[int]tracker.ApprovalWords{}
	var prev time.Time
	for i, r := range src.Transitions {
		from := prev
		prev = r.TS
		if r.ApprovedAt == nil {
			continue
		}
		var best *said
		for _, s := range all {
			if s.consumed || !s.words.Approves(r.To) {
				continue
			}
			start, end := s.at, s.at
			if s.dayOnly {
				end = s.at.AddDate(0, 0, 1)
			}
			if start.After(r.TS) || (!from.IsZero() && !end.After(from)) {
				continue
			}
			if best == nil || s.at.After(best.at) {
				best = s
			}
		}
		if best != nil {
			best.consumed = true
			out[i] = best.words
		}
	}
	return out
}

// openItems is "Left open at done": unticked boxes, a section with many
// shown as a count, and failed playtest steps marked.
func openItems(src timelineSources) ([]openItem, int) {
	failed := map[string]bool{}
	latest := map[string]string{}
	for _, r := range src.Runs {
		for _, s := range r.Steps {
			latest[s.ID] = s.Status
		}
	}
	for id, st := range latest {
		if st == "fail" {
			failed[id] = true
		}
	}
	type sec struct {
		name          string
		total, ticked int
		open          []tracker.CheckboxItem
	}
	var secs []*sec
	idx := map[string]*sec{}
	total := 0
	for _, b := range src.Boxes {
		s := idx[b.Section]
		if s == nil {
			s = &sec{name: b.Section}
			idx[b.Section] = s
			secs = append(secs, s)
		}
		s.total++
		if b.Checked {
			s.ticked++
		} else {
			s.open = append(s.open, b)
			total++
		}
	}
	var items []openItem
	for _, s := range secs {
		if len(s.open) == 0 {
			continue
		}
		if len(s.open) > 3 {
			items = append(items, openItem{Text: s.name, Chip: fmt.Sprintf("%d / %d ticked", s.ticked, s.total), Tone: "amber"})
			continue
		}
		for _, b := range s.open {
			it := openItem{Text: strings.TrimSpace(b.ID + " " + firstLine(b.Label())), Chip: "unticked", Tone: "gray"}
			if failed[b.ID] {
				it.Chip, it.Tone = "failed", "red"
			}
			items = append(items, it)
		}
	}
	return items, total
}

func ciSummaryFrom(src timelineSources) *ciSummary {
	if len(src.CI) == 0 {
		return nil
	}
	r := src.CI[0]
	s := &ciSummary{SHA: gitdir.ShortSHA(r.SHA), State: r.State, Command: strings.Join(r.Commands, ", "), Duration: r.Duration, At: r.Started, Since: len(src.CI) - 1}
	for _, later := range src.CI[1:] {
		if later.State != "green" {
			s.SinceRed++
		}
	}
	return s
}

// clock is a time as HH:MM, with the weekday when it's on another day than ref.
func clock(t, ref time.Time) string {
	t, ref = t.Local(), ref.Local()
	if t.YearDay() != ref.YearDay() || t.Year() != ref.Year() {
		return t.Format("15:04 Mon")
	}
	return t.Format("15:04")
}

// fmtDur: "13s", "45m 48s", "3h 48m", "2d 4h".
func fmtDur(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d/time.Second)) + "s"
	case d < time.Hour:
		m, s := int(d/time.Minute), int(d%time.Minute/time.Second)
		if s == 0 {
			return strconv.Itoa(m) + "m"
		}
		return fmt.Sprintf("%dm %ds", m, s)
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
	default:
		return fmt.Sprintf("%dd %dh", int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour))
	}
}

// fmtSpan is a band's length, rounded: "13s", "50m", "3h 14m".
func fmtSpan(d time.Duration) string {
	if d >= time.Minute && d < time.Hour {
		return strconv.Itoa(int(d.Round(time.Minute)/time.Minute)) + "m"
	}
	return fmtDur(d)
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// truncRunes cuts s to at most n bytes without splitting a UTF-8 rune.
func truncRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
