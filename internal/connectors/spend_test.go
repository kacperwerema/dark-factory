package connectors

import (
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestCostFromJSON(t *testing.T) {
	for _, c := range []struct {
		out  string
		want float64
		ok   bool
	}{
		{`{"creditsConsumed":51}`, 51, true},
		{`{"body":{"submission":{"creditsConsumed":51}},"creditsConsumed":51}`, 51, true},
		{`{"works":[{"creditsConsumed":2},{"creditsConsumed":"7.5"}]}`, 7.5, true},
		{"progress\n{\"creditsConsumed\":3}\n", 3, true},
		{`{"works":[]}`, 0, false},
		{`{"creditsConsumed":{"value":4}}`, 0, false},
		{`not json`, 0, false},
	} {
		got, ok := CostFromJSON([]byte(c.out), "creditsConsumed")
		if got != c.want || ok != c.ok {
			t.Errorf("%s: %v %v, want %v %v", c.out, got, ok, c.want, c.ok)
		}
	}
}

func TestSpendCommandClassifiesSubcommands(t *testing.T) {
	c := &tracker.Connector{Name: "kling", Command: "kling", Spend: &tracker.ConnectorSpend{Meter: "json", Field: "creditsConsumed",
		Free: []string{"query_tasks"}, Refuse: []string{"login"}}}
	for _, x := range []struct {
		args    string
		argv    string
		billing bool
		err     string
	}{
		{"text_to_video --model m p", "kling text_to_video --model m p", true, ""},
		{"kling query_tasks g1", "kling query_tasks g1", false, ""},
		{"/usr/local/bin/kling query_tasks g1", "kling query_tasks g1", false, ""},
		{"text_to_video --help", "kling text_to_video --help", false, ""},
		{"--version", "kling --version", false, ""},
		{"brand_new_tool x", "kling brand_new_tool x", true, ""},
		{"sh -c x", "kling sh -c x", true, ""},
		{"login", "", false, "does not run login"},
		{"--quiet", "", false, "pass kling's subcommand"},
	} {
		call, err := SpendCommand("video/kling", c, strings.Fields(x.args))
		if x.err != "" {
			if err == nil || !strings.Contains(err.Error(), x.err) {
				t.Errorf("%s: err %v, want %q", x.args, err, x.err)
			}
			continue
		}
		if err != nil || strings.Join(call.Argv, " ") != x.argv || call.Billing != x.billing {
			t.Errorf("%s: %+v %v, want %s billing=%v", x.args, call, err, x.argv, x.billing)
		}
	}
}

func TestSpendForCountsThisMonthAndTheUnknownHold(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.Local)
	c := &tracker.Connector{Spend: &tracker.ConnectorSpend{Meter: "json", Unit: "credits", Cap: 100}}
	recs := []SpendRecord{
		{Connector: "video/kling", Month: "2026-10", Amount: 90},
		{Connector: "video/kling", Month: "2026-11", Amount: 51},
		{Connector: "video/other", Month: "2026-11", Amount: 70},
		{Connector: "video/kling", Month: "2026-11", Unknown: true},
	}
	st := SpendFor(recs, "video/kling", c, now)
	if st.Spent != 51 || st.Calls != 2 || !st.Unknown || !st.AtCap() || st.Problem() != "last call's cost unknown" {
		t.Fatalf("state = %+v", st)
	}
	recs = append(recs, SpendRecord{Connector: "video/kling", Month: "2026-11", Amount: 10, Manual: true})
	st = SpendFor(recs, "video/kling", c, now)
	if st.Spent != 61 || st.Unknown || st.AtCap() || st.Words() != "61 / 100 credits this month (2026-11)" {
		t.Fatalf("after the record: %+v %q", st, st.Words())
	}
	if err := SpendError("video/kling", "Video", *st, false); err != nil {
		t.Errorf("room left, refused: %v", err)
	}
	c.Spend.Cap = 0
	st = SpendFor(recs, "video/kling", c, now)
	if !st.AtCap() || st.Words() != "61 credits this month (2026-11), no cap set" {
		t.Errorf("no cap: %+v %q", st, st.Words())
	}
	if SpendFor(recs, "video/kling", &tracker.Connector{}, now) != nil {
		t.Error("a connector without spend: has a state")
	}
}
