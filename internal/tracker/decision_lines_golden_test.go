package tracker

import (
	"encoding/json"
	"flag"
	"os"
	"strings"
	"testing"
)

var updateDecisionGolden = flag.Bool("update-decision-golden", false, "rewrite testdata/decision-lines.golden.json from the current parser")

// goldenDecision is what a D-n line parsed to before the attribute group
// existed: every field a reader of the line relied on.
type goldenDecision struct {
	Line           string   `json:"line"`
	Ref            string   `json:"ref"`
	Date           string   `json:"date"`
	How            string   `json:"how"`
	Text           string   `json:"text"`
	Quote          string   `json:"quote,omitempty"`
	AlsoConsidered string   `json:"also_considered,omitempty"`
	Deferred       bool     `json:"deferred,omitempty"`
	Replaces       []string `json:"replaces,omitempty"`
}

// TestDecisionLinesParseAsBefore: every D-n line on the dark-factory board
// at the base commit (testdata/decision-lines.txt, anonymised: words and
// slugs swapped, all markup kept) parses to what the parser before the
// attribute group gave (testdata/decision-lines.golden.json).
func TestDecisionLinesParseAsBefore(t *testing.T) {
	data, err := os.ReadFile("testdata/decision-lines.txt")
	if err != nil {
		t.Fatal(err)
	}
	var got []goldenDecision
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		d, ok := parseDecisionLine("x/fixture", line)
		if !ok {
			t.Fatalf("line does not parse: %s", line)
		}
		got = append(got, goldenDecision{Line: line, Ref: d.Ref, Date: d.Date.Format(decisionDateLayout), How: d.How, Text: d.Text,
			Quote: d.Quote, AlsoConsidered: d.AlsoConsidered, Deferred: d.Deferred, Replaces: d.Replaces})
	}
	if len(got) < 500 {
		t.Fatalf("fixture has %d lines, want the whole board (500+)", len(got))
	}
	const golden = "testdata/decision-lines.golden.json"
	if *updateDecisionGolden {
		out, _ := json.MarshalIndent(got, "", " ")
		if err := os.WriteFile(golden, append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	raw, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	var want []goldenDecision
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(got) {
		t.Fatalf("golden has %d lines, fixture %d", len(want), len(got))
	}
	for i := range want {
		a, _ := json.Marshal(want[i])
		b, _ := json.Marshal(got[i])
		if string(a) != string(b) {
			t.Errorf("line %d parses differently\nwant %s\ngot  %s", i+1, a, b)
		}
	}
}
