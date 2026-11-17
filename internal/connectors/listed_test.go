package connectors

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The listing is read from each transcript's head only: a listing past
// listedHead (a 100 MB lead transcript's later turns) is never reached.
func TestListedSkillsReadsOnlyTheHead(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := "/work/acme"
	folder := filepath.Join(cfg, "projects", "-work-acme")
	os.MkdirAll(folder, 0o755)
	rec := func(names ...string) string {
		return `{"type":"attachment","attachment":{"type":"skill_listing","content":"The following skills:\n\n- ` + strings.Join(names, `: x\n- `) + `: x\n"}}` + "\n"
	}
	pad := `{"type":"user","message":"` + strings.Repeat("a", listedHead) + `"}` + "\n"
	os.WriteFile(filepath.Join(folder, "s.jsonl"), []byte(rec("dataviz", "anthropic-skills:pdf")+pad+rec("late")), 0o644)
	got := ListedSkills(root)
	if !slices.Equal(got, []string{"anthropic-skills:pdf", "dataviz"}) {
		t.Errorf("ListedSkills = %v", got)
	}
	if ListedSkills("") != nil || ListedSkills("/no/such") != nil {
		t.Error("no root or no transcripts: want nil")
	}
}

func TestParseSkillListing(t *testing.T) {
	got := ParseSkillListing("The following skills are available:\n\n- dataviz: Use it.\n- anthropic-skills:docx: Word files - with a dash: inside.\n- name-only\n  - an indented line\n- two words: not a name\n")
	if !slices.Equal(got, []string{"dataviz", "anthropic-skills:docx", "name-only"}) {
		t.Errorf("ParseSkillListing = %v", got)
	}
}
