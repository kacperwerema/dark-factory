package briefing

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestMemoryPromptSectionCarriesThePins(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir := filepath.Join(t.TempDir(), "issues")
	proj := &tracker.Project{Name: "acme", Slug: "acme", IssueDir: dir, Departments: []tracker.Department{{Name: "Marketing", Systems: []string{"SEO"}}}}
	s := memory.ProjectStore(dir)
	now := time.Now()
	add := func(text, scope string, pin bool) {
		t.Helper()
		r, err := s.Add(memory.Draft{Text: text, Scope: scope}, memory.AddOptions{Mode: memory.ModeNew}, now)
		if err != nil {
			t.Fatal(err)
		}
		if pin {
			if err := s.Pin(r.Memory.ID, "lead-acme", true, now); err != nil {
				t.Fatal(err)
			}
		}
	}
	add("never push from a worker", memory.ScopeProject, true)
	add("marketing posts go out on Tuesdays", "department:marketing", true)
	add("an unpinned fact stays out", memory.ScopeProject, false)

	seo := MemoryPromptSection(proj, &tracker.Issue{Slug: "seo-pass", System: "SEO"})
	for _, want := range []string{"## Memory", "issue-cli start", "never push from a worker", "marketing posts go out on Tuesdays", "--updates <id>", "Never put a secret"} {
		if !strings.Contains(seo, want) {
			t.Errorf("SEO worker's briefing is missing %q:\n%s", want, seo)
		}
	}
	engine := MemoryPromptSection(proj, &tracker.Issue{Slug: "engine"})
	if strings.Contains(engine, "Tuesdays") || strings.Contains(engine, "unpinned") {
		t.Errorf("a Product worker's briefing has Marketing's pin or an unpinned memory:\n%s", engine)
	}
	if !strings.Contains(MemoryLeadLine(proj, "lead-acme", ""), "Tuesdays") {
		t.Error("the CEO's line misses a department pin")
	}
	if strings.Contains(MemoryLeadLine(proj, "lead-acme-sales", "Sales"), "Tuesdays") {
		t.Error("another department's lead sees Marketing's pin")
	}
}

// Lead-scope pins sit in every lead's brief in full, on their own budget;
// the unpinned ones get a clipped line; a worker's briefing has neither.
func TestLeadScopeInTheBriefs(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir := filepath.Join(t.TempDir(), "issues")
	proj := &tracker.Project{Name: "acme", Slug: "acme", IssueDir: dir, Departments: []tracker.Department{{Name: "Marketing", Systems: []string{"SEO"}}}}
	s := memory.ProjectStore(dir)
	now := time.Now()
	add := func(text, scope string, pin bool) string {
		t.Helper()
		r, err := s.Add(memory.Draft{Text: text, Scope: scope}, memory.AddOptions{Mode: memory.ModeNew}, now)
		if err != nil {
			t.Fatal(err)
		}
		if pin {
			if err := s.Pin(r.Memory.ID, "lead-acme", true, now); err != nil {
				t.Fatal(err)
			}
		}
		return r.Memory.ID
	}
	if line := LeadMemoriesLine(proj); !strings.Contains(line, "none yet") || !strings.Contains(line, "--scope lead") {
		t.Errorf("empty line = %q", line)
	}
	// Four rules at the cap are more than the briefing budget for pins.
	var rules []string
	for i := range 4 {
		rule := fmt.Sprintf("standing rule %d ", i) + strings.Repeat("word ", 96)
		rules = append(rules, strings.TrimSpace(rule))
		add(rules[i], memory.ScopeLead, true)
	}
	add("the project pin stays too", memory.ScopeProject, true)
	long := "an unpinned lead fact " + strings.Repeat("tail ", 40) + "END"
	id := add(long, memory.ScopeLead, false)

	for _, brief := range []string{MemoryLeadLine(proj, "lead-acme", ""), MemoryLeadLine(proj, "lead-acme-marketing", "Marketing")} {
		for _, want := range append([]string{"Standing rules (lead scope):", "the project pin stays too", id + " (fact) an unpinned lead fact", "…"}, rules...) {
			if !strings.Contains(brief, want) {
				t.Errorf("a lead's brief is missing %q:\n%s", want, brief)
			}
		}
		if strings.Contains(brief, "END") {
			t.Errorf("an unpinned lead memory is shown whole:\n%s", brief)
		}
	}
	for _, issue := range []*tracker.Issue{{Slug: "engine"}, {Slug: "seo-pass", System: "SEO"}} {
		section := MemoryPromptSection(proj, issue)
		if strings.Contains(section, "standing rule") || strings.Contains(section, "unpinned lead fact") {
			t.Errorf("%s's briefing has a lead memory:\n%s", issue.Slug, section)
		}
		if !strings.Contains(section, "the project pin stays too") {
			t.Errorf("%s's briefing lost the project pin:\n%s", issue.Slug, section)
		}
	}
	if line := LeadMemoriesLine(proj); !strings.Contains(line, "5 (4 pinned") {
		t.Errorf("line = %q", line)
	}

	// More unpinned ones than the list holds end in the command that has them.
	for i := range leadListLimit {
		add(fmt.Sprintf("an extra lead memory number %d about topic %d", i, i*7), memory.ScopeLead, false)
	}
	if brief := MemoryLeadLine(proj, "lead-acme", ""); !strings.Contains(brief, "- +1 more: issue-cli memory list --scope lead") {
		t.Errorf("no more line:\n%s", brief)
	}
}
