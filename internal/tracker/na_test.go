package tracker

import (
	"strings"
	"testing"
)

func TestNAClaim(t *testing.T) {
	cases := []struct {
		text   string
		reason string
		ok     bool
	}{
		{"`make ux-test REPEAT=2` passed (N/A: no page changes)", "no page changes", true},
		{"Manual check (n/a: the repro is an automated test (TestFoo))", "the repro is an automated test (TestFoo)", true},
		{"Manual check — N/A: runner-internal, covered by TestRunner", "runner-internal, covered by TestRunner", true},
		{"Manual checks come as scripts, or N/A with the reason", "", false},
		{"Plain box", "", false},
		{"Box (N/A:)", "", true},
	}
	for _, c := range cases {
		reason, ok := NAClaim(c.text)
		if reason != c.reason || ok != c.ok {
			t.Errorf("NAClaim(%q) = %q, %v; want %q, %v", c.text, reason, ok, c.reason, c.ok)
		}
	}
}

// Every named form, filled in, passes; the problems each name what's
// missing.
func TestCheckNA(t *testing.T) {
	good := []string{
		"the repro is an automated test (TestStatsPageFastOnABigProject)",
		"no page changes",
		"runner-internal, covered by TestShotRetry",
		"covered by playtest session stats-cards",
		"a read-only card covered by the template test",
	}
	for _, r := range good {
		if p := CheckNA(r); p != "" {
			t.Errorf("CheckNA(%q) = %q, want accepted", r, p)
		}
	}
	bad := map[string]string{
		"":                                  "no reason",
		"the repro is an automated test":    "test's name",
		"the repro is an automated test ()": "test's name",
		"runner-internal":                   "covers it",
		"runner-internal, covered by":       "covers it",
		"covered by":                        "what covers it",
		"no page changes (<TestName>)":      "placeholder <TestName>",
		"none":                              "too short",
	}
	for r, want := range bad {
		if p := CheckNA(r); !strings.Contains(p, want) {
			t.Errorf("CheckNA(%q) = %q, want it to say %q", r, p, want)
		}
	}
}

// The forms' own text, filled in with a name, is accepted: the hints never
// suggest a form lint warns on.
func TestNAFormsFilledAreAccepted(t *testing.T) {
	for _, f := range NAForms {
		filled := naPlaceholderRe.ReplaceAllString(f.Form, "TestSomething, and why it fits")
		if p := CheckNA(filled); p != "" {
			t.Errorf("form %q filled as %q: %s", f.Form, filled, p)
		}
	}
}

func TestWithNA(t *testing.T) {
	cases := map[string]string{
		"ux-test passed":                         "ux-test passed (N/A: no page changes)",
		"ux-test passed (n/a: old words)":        "ux-test passed (N/A: no page changes)",
		"ux-test passed — N/A: something before": "ux-test passed (N/A: no page changes)",
	}
	for in, want := range cases {
		if got := WithNA(in, "no page changes"); got != want {
			t.Errorf("WithNA(%q) = %q, want %q", in, got, want)
		}
	}
}

// A form longer than the column puts its use on the next line, under the
// others' uses, instead of running into it.
func TestNAFormsHelpLongForm(t *testing.T) {
	help := NAFormsHelp("  ")
	want := "  N/A: page state a scenario can't build (<what>), shown by <evidence>\n" + strings.Repeat(" ", 2+5+46+1) + "a page change"
	if !strings.Contains(help, want) {
		t.Errorf("long form not wrapped:\n%s", help)
	}
}
