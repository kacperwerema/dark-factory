package depttemplate

import (
	"strings"
	"testing"
)

func TestParseGoal(t *testing.T) {
	mk, err := Load("marketing")
	if err != nil {
		t.Fatal(err)
	}
	g, err := ParseGoal("leads=Leads / quotes/5.5/Month", nil)
	if err != nil || g.ID != "leads" || g.Name != "Leads / quotes" || *g.Target != 5.5 || g.Period != "month" {
		t.Fatalf("got %+v, %v", g, err)
	}
	if got := GoalFlag(g); got != "leads=Leads / quotes/5.5/month" {
		t.Errorf("GoalFlag = %q", got)
	}
	// A template goal's id keeps its source.
	if g, err := ParseGoal("VISITS=Visits/200/week", mk); err != nil || g.ID != "visits" || !strings.HasPrefix(g.Source, "file:") {
		t.Errorf("got %+v, %v", g, err)
	}
	for _, bad := range []string{"", "x", "x=Name", "x=Name/1", "=Name/1/week", "a b=N/1/week", "x=/1/week", "x=N/one/week", "x=N/1/year", "x=N/1/"} {
		if _, err := ParseGoal(bad, nil); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

// TestSampleGoalNeedsTheBundledValues: a sample goal is the template's
// id with its name (today's or an earlier release's), target and period;
// any of them changed makes it the project's.
func TestSampleGoalNeedsTheBundledValues(t *testing.T) {
	mk, err := Load("marketing")
	if err != nil {
		t.Fatal(err)
	}
	for flag, want := range map[string]bool{
		"pages=Pages refreshed/10/week":        true,
		"pages=Client pages refreshed/10/week": true, // an earlier release's name
		"pages=Pages refreshed/12/week":        false,
		"pages=Pages refreshed/10/month":       false,
		"pages=Our pages/10/week":              false,
		"other=Pages refreshed/10/week":        false,
	} {
		g, err := ParseGoal(flag, mk)
		if err != nil {
			t.Fatal(err)
		}
		if got := mk.sampleGoal(g); got != want {
			t.Errorf("%s: sample = %v, want %v", flag, got, want)
		}
	}
	if !mk.sampleMission("Every client site refreshed every week") || !mk.sampleMission(mk.Mission) || mk.sampleMission("Ours") {
		t.Error("sampleMission")
	}
}
