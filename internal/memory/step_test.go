package memory

import "testing"

func TestNamesStep(t *testing.T) {
	steps := []string{"idea", "in design", "in progress", "testing", "human-testing", "shipping"}
	for text, want := range map[string]string{
		"Shipping on a busy night: merge and ff":   "shipping",
		"** shipping: run the short path":          "shipping",
		"before shipping, run vet":                 "shipping",
		"Ask it while the issue is in design.":     "in design",
		"At In Progress the guard refuses it":      "in progress",
		"the owner runs it at human-testing":       "human-testing",
		"Chrome for Testing plays mp4":             "",
		"testing the old code fails for no reason": "testing",
		"a test that is shipping-critical":         "",
		"idea → in progress adds an edge":          "",
		"an edge idea->in design":                  "",
		"a good idea for later":                    "",
		"the shipping box ticks itself":            "",
		"work in progressive steps":                "",
	} {
		if got := NamesStep(text, steps); got != want {
			t.Errorf("NamesStep(%q) = %q, want %q", text, got, want)
		}
	}
}
