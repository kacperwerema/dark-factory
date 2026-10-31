package tracker

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/memory"
)

func TestMemoryPromptSectionCarriesThePins(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir := filepath.Join(t.TempDir(), "issues")
	proj := &Project{Name: "acme", Slug: "acme", IssueDir: dir, Departments: []Department{{Name: "Marketing", Systems: []string{"SEO"}}}}
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

	seo := MemoryPromptSection(proj, &Issue{Slug: "seo-pass", System: "SEO"})
	for _, want := range []string{"## Memory", "issue-cli start", "never push from a worker", "marketing posts go out on Tuesdays", "--updates <id>", "Never put a secret"} {
		if !strings.Contains(seo, want) {
			t.Errorf("SEO worker's briefing is missing %q:\n%s", want, seo)
		}
	}
	engine := MemoryPromptSection(proj, &Issue{Slug: "engine"})
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
