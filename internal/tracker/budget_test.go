package tracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestBudgetUnmarshal(t *testing.T) {
	for src, want := range map[string]string{
		"{usd: 50}":                        "",
		"{tokens: 2000000, period: month}": "",
		"50":                               "not a mapping",
		"{usd: 0}":                         "more than 0",
		"{usd: x}":                         "must be numbers",
		"{tokens: 1.5}":                    "whole number",
		"{usd: 5, tokens: 5}":              "both",
		"{period: week}":                   "neither",
		"{usd: 5, period: day}":            "use week or month",
		"{usd: 5, limit: 3}":               "unknown key",
	} {
		var b Budget
		if err := yaml.Unmarshal([]byte(src), &b); err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if want == "" && b.Err != "" || want != "" && !strings.Contains(b.Err, want) {
			t.Errorf("%s: Err = %q, want %q", src, b.Err, want)
		}
	}
	var b Budget
	yaml.Unmarshal([]byte("{usd: 12.5}"), &b)
	if b.USD != 12.5 || b.Period != "week" {
		t.Errorf("default period: %+v", b)
	}
}

// A malformed budget never fails loading projects.yaml.
func TestBadBudgetLoadsProjects(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "projects.yaml")
	os.MkdirAll(filepath.Join(dir, "issues"), 0o755)
	os.WriteFile(cfg, []byte("projects:\n  - name: A\n    issues: ./issues\n    company:\n      budget: {usd: 100, period: month}\n    departments:\n      - name: Marketing\n        budget: lots\n"), 0o644)
	ps, err := LoadProjects(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if b := ps[0].Departments[0].Budget; b == nil || b.Err == "" {
		t.Errorf("marketing budget = %+v", b)
	}
	if b := ps[0].Company.Budget; b == nil || b.USD != 100 || b.Period != "month" {
		t.Errorf("company budget = %+v", b)
	}
}
