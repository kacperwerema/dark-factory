package tracker

import (
	"testing"
	"time"
)

func TestShipStatus(t *testing.T) {
	wf := func(names ...string) *WorkflowConfig {
		w := &WorkflowConfig{}
		for _, n := range names {
			w.Statuses = append(w.Statuses, WorkflowStatus{Name: n})
		}
		return w
	}
	optional := wf("in progress", "shipping", "done", "parked")
	optional.Statuses[1].Optional = true
	for _, tc := range []struct {
		name string
		wf   *WorkflowConfig
		want string
	}{
		{"development", wf("idea", "in progress", "documentation", "shipping", "done", "parked"), "shipping"},
		{"no shipping status", wf("idea", "in progress", "documentation", "done"), "documentation"},
		{"no done: the last status closes", wf("draft", "review", "published"), "review"},
		{"optional statuses are off the path", optional, "in progress"},
		{"nothing before done", wf("done"), ""},
		{"nil", nil, ""},
	} {
		if got := tc.wf.ShipStatus(); got != tc.want {
			t.Errorf("%s: ShipStatus() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestDispatchBaseIsTheFirstRecordedBase(t *testing.T) {
	at := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	store := StatsStore{Dispatches: []Dispatch{
		{At: at, Session: "s"},
		{At: at.Add(time.Hour), Session: "s", BaseBranch: "main", BaseSHA: "abc"},
		{At: at.Add(2 * time.Hour), Session: "s", BaseBranch: "other", BaseSHA: "def"},
	}}
	d, ok := DispatchBase(store)
	if !ok || d.BaseBranch != "main" || d.BaseSHA != "abc" {
		t.Errorf("DispatchBase = %+v, %v", d, ok)
	}
	if _, ok := DispatchBase(StatsStore{Dispatches: store.Dispatches[:1]}); ok {
		t.Error("DispatchBase found a base in rows without one")
	}
}
