package connections

import (
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestPlanRoute(t *testing.T) {
	p := project(t)
	for _, c := range []struct{ from, to, want, err string }{
		{"marketing", "product", "marketing → hq → product", ""}, // D-8, undeclared
		{"marketing", "sales", "marketing → sales", ""},
		{"hq", "product", "hq → product", ""},
		{"", "marketing", "hq → marketing", ""},
		{"marketing", "marketing", "", "can't hand off to itself"},
		{"marketing", "other/sales", "", "Handoffs stay inside one project."},
		{"marketing", "nobody", "", "is no box of project"},
	} {
		got, err := PlanRoute(p, c.from, c.to)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%s → %s: err %v, want %q", c.from, c.to, err, c.err)
			}
			continue
		}
		if err != nil || strings.Join(got, RouteSep) != c.want {
			t.Errorf("%s → %s = %v, %v; want %s", c.from, c.to, got, err, c.want)
		}
	}

	// Declared routes decide, via a group included.
	p.Connections = &tracker.Connections{Requests: []tracker.Route{{From: "sales", To: "marketing", Via: "gtm"}}}
	if got, err := PlanRoute(p, "sales", "marketing"); err != nil || strings.Join(got, RouteSep) != "sales → gtm → marketing" {
		t.Errorf("declared via gtm: %v %v", got, err)
	}
	if _, err := PlanRoute(p, "marketing", "product"); err == nil || !strings.Contains(err.Error(), "not a declared request route") {
		t.Errorf("off the declared routes: %v", err)
	}
}

func TestHandoffStatesAndTheLine(t *testing.T) {
	p := project(t)
	front := "status: \"idea\"\nrequested_by: \"Marketing\"\nhandoff_route: \"marketing → hq → product\"\nhandoff_for: \"seo/pass\""
	waiting := issue(t, "factory/a", front, "")
	fwd := issue(t, "factory/b", front, "")
	doneX := issue(t, "factory/c", strings.Replace(front, `"idea"`, `"done"`, 1)+"\ndone_at: \"2026-10-29\"", "")
	declined := issue(t, "factory/d", strings.Replace(front, `"idea"`, `"parked"`, 1)+"\ndone_at: \"2026-10-29\"", "")
	plain := issue(t, "factory/e", "status: \"idea\"\nrequested_by: \"Marketing\"", "")
	comments := map[string][]tracker.Comment{
		"factory/b": {{Text: "handoff: forwarded by lead-acme to Product", Author: "lead-acme"}},
		"factory/d": {{Text: "handoff: declined by lead-acme: not now", Author: "lead-acme"}},
	}
	// A forward signed by someone other than the relay lead doesn't count.
	comments["factory/a"] = []tracker.Comment{{Text: "handoff: forwarded by lead-acme-marketing", Author: "lead-acme-marketing"}}

	cases := map[*tracker.Issue]struct{ state, word string }{
		waiting:  {StateWaiting, "waiting on HQ to forward"},
		fwd:      {StateOpen, "at idea"},
		doneX:    {StateDone, "done"},
		declined: {StateDeclined, "declined: not now"},
	}
	for x, want := range cases {
		h, ok := HandoffOf(p, x, comments[x.Slug])
		if !ok || h.State != want.state || h.Word != want.word || h.From != "marketing" || h.To != Product || h.Via != HQ || h.For != "seo/pass" {
			t.Errorf("%s: %+v, want %v", x.Slug, h, want)
		}
	}
	if _, ok := HandoffOf(p, plain, nil); ok {
		t.Error("a plain request is no handoff")
	}

	all := []*tracker.Issue{waiting, fwd, doneX, declined, plain}
	edges := derive(p, all, nil, comments)
	var via, straight *Edge
	for i := range edges {
		e := &edges[i]
		if e.From == "marketing" && e.To == Product && e.Kind == Request {
			if e.Via == HQ {
				via = e
			} else {
				straight = e
			}
		}
	}
	if via == nil || len(via.Open) != 2 || len(via.Closed) != 2 {
		t.Fatalf("handoffs draw via HQ, forwarded or not: %+v", via)
	}
	if straight == nil || len(straight.Open) != 1 || straight.Open[0].State != "" {
		t.Errorf("the plain request stays straight, without a state: %+v", straight)
	}
	if via.Open[0].Route != "Marketing → HQ → Product" || via.Open[0].State == "" {
		t.Errorf("item carries route and state: %+v", via.Open[0])
	}
	if got := Label(p, *via); got != "Marketing → HQ → Product · 2 requests · 1 waiting to be forwarded" {
		t.Errorf("label %q", got)
	}

	f := func(i *tracker.Issue) []tracker.Comment { return comments[i.Slug] }
	if got := ListHandoffs(p, all, f, HandoffFilter{WaitingOn: HQ}, now); len(got) != 1 || got[0].Slug != "factory/a" {
		t.Errorf("waiting on HQ: %+v", got)
	}
	got := ListHandoffs(p, all, f, HandoffFilter{From: "marketing"}, now)
	if len(got) != 4 || !got[0].Open() || !got[1].Open() || got[3].Open() {
		t.Errorf("from marketing, open first: %+v", got)
	}
	if RelayPending(p, waiting, comments["factory/a"]) != HQ || RelayPending(p, fwd, comments["factory/b"]) != "" {
		t.Error("RelayPending follows the handoff's route")
	}
}
