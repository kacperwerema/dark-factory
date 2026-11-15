package tracker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// briefIssues writes issues into dir (rel path → file) and returns them.
func briefIssues(t *testing.T, dir string, files map[string]string) []*Issue {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	issues, err := LoadIssues(dir)
	if err != nil {
		t.Fatal(err)
	}
	return issues
}

func issueFile(title, status, system, decisions string) string {
	return fmt.Sprintf("---\ntitle: %q\nstatus: %q\nsystem: %q\n---\n\n## Decisions\n\n%s", title, status, system, decisions)
}

// nDecisions is n D-n lines with the given attribute group.
func nDecisions(n int, from int, group string) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "- **D-%d** · 2026-10-%02d · chat: Rule %d, \"yes\" %s\n", from+i, 1+i%28, from+i, group)
	}
	return b.String()
}

func blockFor(t *testing.T, files map[string]string, slug string) (DecisionBlock, []*Issue) {
	t.Helper()
	dir := t.TempDir()
	issues := briefIssues(t, dir, files)
	proj := &Project{Slug: "acme", IssueDir: dir}
	for _, i := range issues {
		if i.Slug == slug {
			return IssueDecisionBlock(proj, issues, i, i.Status, nil, time.Date(2026, 10, 31, 9, 0, 0, 0, time.Local)), issues
		}
	}
	t.Fatalf("no issue %s", slug)
	return DecisionBlock{}, nil
}

// AC2: 20 soft in force: 12 lines, then "and 8 more".
func TestDecisionBlockFoldsSoftPastTheCap(t *testing.T) {
	b, _ := blockFor(t, map[string]string{
		"Ops/rules.md": issueFile("rules", "done", "Ops", nDecisions(20, 1, "{scope: project}")),
		"UI/work.md":   issueFile("work", "in progress", "UI", ""),
	}, "ui/work")
	lines := b.Lines(InForceCommand("ui/work"), "this issue")
	if len(b.Shown) != 12 || len(b.Folded) != 8 {
		t.Fatalf("shown %d folded %d, want 12 and 8", len(b.Shown), len(b.Folded))
	}
	if len(lines) != 14 { // 12 decisions, the count line, the challenge line
		t.Fatalf("%d lines:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if lines[12] != "- and 8 more: issue-cli decisions --in-force ui/work" {
		t.Errorf("count line %q", lines[12])
	}
	if lines[13] != DecisionChallengeLine {
		t.Errorf("challenge line %q", lines[13])
	}
	if len(b.Refs()) != 20 {
		t.Errorf("refs %d, want all 20", len(b.Refs()))
	}
}

// AC3: 15 hard and 5 soft: every hard one listed, the soft ones folded.
func TestDecisionBlockNeverFoldsHard(t *testing.T) {
	b, _ := blockFor(t, map[string]string{
		"Ops/rules.md": issueFile("rules", "done", "Ops", nDecisions(15, 1, "{scope: project; hard}")+nDecisions(5, 16, "{scope: project}")),
		"UI/work.md":   issueFile("work", "in progress", "UI", ""),
	}, "ui/work")
	if len(b.Shown) != 15 || len(b.Folded) != 5 {
		t.Fatalf("shown %d folded %d, want 15 and 5", len(b.Shown), len(b.Folded))
	}
	for _, s := range b.Shown {
		if !s.Entry.Hard {
			t.Errorf("a soft one shown past the cap: %s", s.Entry.Ref)
		}
	}
	lines := b.Lines(InForceCommand("ui/work"), "this issue")
	if got := lines[15]; got != "- and 5 more: issue-cli decisions --in-force ui/work" {
		t.Errorf("count line %q", got)
	}
	if !strings.Contains(lines[0], " · hard · project · Rule ") {
		t.Errorf("line shape %q", lines[0])
	}
}

// The cap comes from decisions: brief_cap:.
func TestDecisionBriefCap(t *testing.T) {
	if DecisionBriefCap(nil) != 12 {
		t.Error("default cap")
	}
	if DecisionBriefCap(&WorkflowConfig{Decisions: &DecisionsConfig{BriefCap: 3}}) != 3 {
		t.Error("brief_cap not read")
	}
}

// AC6: none in force: the block says so and names the command.
func TestDecisionBlockEmpty(t *testing.T) {
	b, _ := blockFor(t, map[string]string{"UI/work.md": issueFile("work", "in progress", "UI", "")}, "ui/work")
	lines := b.Lines(InForceCommand("ui/work"), "this issue")
	if len(lines) != 1 || lines[0] != "No decisions in force for this issue: issue-cli decisions --in-force ui/work" {
		t.Errorf("empty block %q", lines)
	}
}

// A line: full ref, strength, scope, the text on one line and cut, the
// quote only when short.
func TestDecisionBriefLine(t *testing.T) {
	long := strings.Repeat("word ", 40)
	e := &DecisionEntry{Decision: Decision{Ref: "a/b#D-2", Text: "Two\nlines " + long, Quote: strings.Repeat("q", 61)}}
	got := DecisionBriefLine(e)
	if !strings.HasPrefix(got, "- a/b#D-2 · soft · issue · Two lines word") || !strings.HasSuffix(got, "…") || strings.Contains(got, "qqq") {
		t.Errorf("line %q", got)
	}
	e = &DecisionEntry{Decision: Decision{Ref: "a/b#D-3", Text: "Short", Quote: "ok then", DecisionAttrs: DecisionAttrs{Scope: "system:UI", Hard: true}}}
	if got := DecisionBriefLine(e); got != `- a/b#D-3 · hard · system:UI · Short — "ok then"` {
		t.Errorf("line %q", got)
	}
}

// RecordInjected writes one done event with the refs, and nothing when no
// decision was in force.
func TestRecordInjected(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 31, 9, 0, 0, 0, time.Local)
	if err := RecordInjected(dir, Injected{Briefing: BriefingStart, Issue: "ui/work"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(DecisionLogDir(dir)); !os.IsNotExist(err) {
		t.Fatalf("an empty briefing made the log folder: %v", err)
	}
	if err := RecordInjected(dir, Injected{Briefing: BriefingDispatch, Issue: "ui/work", Refs: []string{"a/b#D-1"}, Actor: DecisionActor{Name: "agent-ui-work", Role: "worker"}}, now); err != nil {
		t.Fatal(err)
	}
	evs, err := ReadDecisionLog(dir)
	if err != nil || len(evs) != 1 {
		t.Fatalf("events %v %v", evs, err)
	}
	ev := evs[0]
	if ev.Event != DecisionInjected || ev.Briefing != BriefingDispatch || ev.Issue != "ui/work" || len(ev.Refs) != 1 || ev.By != "agent-ui-work" {
		t.Errorf("event %+v", ev)
	}
}
