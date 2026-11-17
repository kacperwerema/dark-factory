package tracker

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// docQuote is one place outside the Go source that repeats text with one
// source: file, from the repository root, must hold the text word for word.
type docQuote struct{ file, source, text string }

func docQuotes() []docQuote {
	// The doc quotes the rule in full; it reads the same on every workflow.
	yoloRule := YOLORule(nil, "<slug>")
	messageRule := WorkerMessageRule("lead-<project>", "agent-<slug>")
	capUnset := fmt.Sprintf("%d when unset", DefaultMaxRedRounds)
	capYAML := fmt.Sprintf("max_red_rounds: %d", DefaultMaxRedRounds)
	feature := "cmd/issue-cli/templates/workflow-features/red-round-cap.yaml"
	return []docQuote{
		{"docs/Workflow/yolo.md", "YOLORule", yoloRule},
		{"docs/workflow.md", "RedRoundsWhatCounts", RedRoundsWhatCounts},
		{"docs/Workflow/critique.md", "RedRoundsWhatCounts", RedRoundsWhatCounts},
		{"docs/doc-map.md", "RedRoundsWhatCounts", RedRoundsWhatCounts},
		{"docs/workflow.md", "DefaultMaxRedRounds", capYAML + "   # top level, project-wide like ci:; " + capUnset},
		{"docs/doc-map.md", "DefaultMaxRedRounds", "`max_red_rounds:` (" + capUnset + ")"},
		{"cmd/issue-cli/redrounds.go", "DefaultMaxRedRounds", "(workflow.yaml, " + capUnset + ")"},
		{feature, "DefaultMaxRedRounds", "`max_red_rounds:` (" + capUnset + ")"},
		{feature, "DefaultMaxRedRounds", fmt.Sprintf("(%d by default)", DefaultMaxRedRounds)},
		{feature, "DefaultMaxRedRounds", "  " + capYAML + "\n"},
		{"docs/CLI/overview.md", "CloseRule", CloseRule},
		{"docs/agent-dispatch.md", "WorkerMessageRule", messageRule},
		{"docs/getting-started.md", "SandboxViewerRule", SandboxViewerRule},
		{"docs/patterns/guided-check-scripts.md", "SandboxViewerRule", SandboxViewerRule},
		{"docs/patterns/guided-check-scripts.md", "WorkerGuardRule", WorkerGuardRule},
		{"docs/departments.md", "WorkerGuardRule", WorkerGuardRule},
	}
}

// quoteDrift says which quotes under root no longer hold their source's text.
func quoteDrift(root string, quotes []docQuote) []string {
	var drift []string
	for _, q := range quotes {
		raw, err := os.ReadFile(filepath.Join(root, q.file))
		if err != nil {
			drift = append(drift, err.Error())
			continue
		}
		if !strings.Contains(string(raw), q.text) {
			drift = append(drift, fmt.Sprintf("%s no longer quotes %s: %q", q.file, q.source, q.text))
		}
	}
	return drift
}

// TestDocsQuoteTheCode: a doc that repeats a rule the code prints says it word
// for word, so the two can't drift. When a source changes, paste it here.
func TestDocsQuoteTheCode(t *testing.T) {
	for _, msg := range quoteDrift(filepath.Join("..", ".."), docQuotes()) {
		t.Error(msg)
	}
}

func TestQuoteDriftNamesTheFileAndSource(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("# A\n\nThe rule, as it was.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := quoteDrift(root, []docQuote{{"a.md", "Rule", "The rule, as it was."}}); len(got) != 0 {
		t.Errorf("a good quote drifted: %v", got)
	}
	got := quoteDrift(root, []docQuote{{"a.md", "Rule", "The rule, as it is now."}})
	if len(got) != 1 || !strings.Contains(got[0], "a.md no longer quotes Rule") {
		t.Errorf("drift = %v", got)
	}
}

// TestShellNotesDocMatchesBriefing: the bullets under Shell notes are ShellPromptSection's
// lines, all of them and nothing else.
func TestShellNotesDocMatchesBriefing(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "getting-started.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, rest, found := strings.Cut(string(raw), "#### Shell notes\n")
	if !found {
		t.Fatal("getting-started.md has no #### Shell notes")
	}
	body, _, _ := strings.Cut(rest, "\n### ")
	var bullets []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "- ") {
			bullets = append(bullets, line)
		}
	}
	want := ShellNotes(ShellFacts{Shell: "zsh", BSD: true, RTK: true}, ProjectTests{CI: "make validate", Go: true})
	if !slices.Equal(bullets, want) {
		t.Errorf("Shell notes bullets drifted from ShellNotes:\n got %q\nwant %q", bullets, want)
	}
}

// TestRedRoundPhraseReadsOneSource: the briefing and process schema print the
// phrase, and no struct tag keeps a copy of it or of the default cap.
func TestRedRoundPhraseReadsOneSource(t *testing.T) {
	section := RedRoundsPromptSection("s", &WorkflowConfig{})
	if !strings.Contains(section, "("+RedRoundsWhatCounts+")") {
		t.Errorf("briefing lost the phrase: %s", section)
	}
	var desc string
	for _, section := range WorkflowSchemaSections() {
		for _, f := range section.Fields {
			if f.Name == "max_red_rounds" {
				desc = f.Description
			}
		}
	}
	if !strings.Contains(desc, "("+RedRoundsWhatCounts+")") || !strings.Contains(desc, fmt.Sprintf("; %d when unset", DefaultMaxRedRounds)) {
		t.Errorf("process schema's max_red_rounds = %q", desc)
	}
	if left := regexp.MustCompile(`\{\{[^}]*\}\}`).FindString(desc); left != "" {
		t.Errorf("unfilled name %s in %q", left, desc)
	}
}
