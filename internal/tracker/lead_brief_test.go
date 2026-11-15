package tracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewestHandoffNote(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"2026-10-01-lead-session.md", "2026-10-13-lead-session.md",
		"2026-10-02-lead-p.md", "2026-10-09-lead-p.md",
		"2026-10-20-lead-other.md", "notes.md",
	} {
		os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644)
	}
	if got := NewestHandoffNote(dir, "lead-p"); got != filepath.Join(dir, "2026-10-09-lead-p.md") {
		t.Errorf("own note should win over legacy: %q", got)
	}
	os.Remove(filepath.Join(dir, "2026-10-02-lead-p.md"))
	os.Remove(filepath.Join(dir, "2026-10-09-lead-p.md"))
	if got := NewestHandoffNote(dir, "lead-p"); got != filepath.Join(dir, "2026-10-13-lead-session.md") {
		t.Errorf("falls back to the newest legacy note: %q", got)
	}
	if got := NewestHandoffNote(filepath.Join(dir, "missing"), "lead-p"); got != "" {
		t.Errorf("missing dir: %q", got)
	}
}

func TestLeadsSurviveCloneAndSchema(t *testing.T) {
	wf := &WorkflowConfig{Leads: LeadsConfig{Project: "P", ProjectAddendum: "A", Epic: "E"}}
	if got := wf.Clone().Leads; got != wf.Leads {
		t.Fatalf("Clone dropped leads: %+v", got)
	}
	var found bool
	for _, sec := range WorkflowSchemaSections() {
		if sec.Path == "leads" && len(sec.Fields) == 3 {
			found = true
		}
	}
	if !found {
		t.Fatal("help schema has no leads section with project_addendum/project/epic")
	}
}

// TestLeadSessionDocNamesLeadRulesAndScopeBoxes keeps the lead-session doc in
// step with the brief's rules-in-the-body lines.
func TestLeadSessionDocNamesLeadRulesAndScopeBoxes(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "patterns", "lead-session.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		`append <slug> --section "Lead rules"`,
		"A scope change mid-issue is a box, not a message",
		"owner boxes and other issues' boxes are untouched; your own Implementation/Testing/Shipping boxes are yours",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/patterns/lead-session.md lacks %q", want)
		}
	}
}
