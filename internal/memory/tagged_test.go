package memory

import (
	"errors"
	"strings"
	"testing"
)

// The two "earlier hash" memories as they were written (m-7bffcb, then
// m-60b23e beside it): one fact retold, 43% the same words.
const (
	earlierHashA = "Editing a department template's role.md or jobs/*.md: save the old text as internal/depttemplate/testdata/earlier/<key>/<path>.<n>, add its sha256 to earlierTexts in update.go, and repin testdata/current.sha256; otherwise TestBundledFilesChangeOnlyWithTheirEarlierHash fails and department update treats old installs as edited."
	earlierHashB = "Editing a bundled department template's file (e.g. sales-b2c/role.md) fails TestBundledFilesChangeOnlyWithTheirEarlierHash: append the old sha256 to earlierTexts[<template>].Files[<file>] in internal/depttemplate/update.go, save the old text as testdata/earlier/<template>/<file>.<n> (git show <base>:<path>), and set the new hash in testdata/current.sha256. The failure message prints all three steps."
)

// A fact retold in other words is refused by containment though its
// Jaccard is under SimilarThreshold, naming the one it repeats; --new
// (the CLI's --force) adds it anyway.
func TestAddRefusesAFactRetoldInOtherWords(t *testing.T) {
	if j := Similarity(earlierHashA, earlierHashB); j >= SimilarThreshold {
		t.Fatalf("the pair's Jaccard is %.2f: the test no longer proves the containment line", j)
	}
	s := newStore(t)
	first := add(t, s, Draft{Text: earlierHashA, Kind: KindHowto}, AddOptions{}).Memory
	_, err := s.Add(Draft{Text: earlierHashB, Kind: KindPitfall}, AddOptions{}, t0)
	var sim *SimilarError
	if !errors.As(err, &sim) || len(sim.Similar) != 1 || sim.Similar[0].ID != first.ID {
		t.Fatalf("retold fact: err = %v", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "of the shorter one's") || !strings.Contains(msg, "--force") {
		t.Errorf("the refusal doesn't say why or name --force:\n%s", msg)
	}
	if r := add(t, s, Draft{Text: earlierHashB, Kind: KindPitfall}, AddOptions{Mode: ModeNew}); r.Op != OpAdd {
		t.Fatalf("--new: %+v", r)
	}
	// Another scope is another store of facts: not a near-duplicate.
	if r := add(t, s, Draft{Text: earlierHashB + " Seen twice.", Scope: AgentScope("agent-x")}, AddOptions{}); r.Op != OpAdd {
		t.Fatalf("another scope: %+v", r)
	}
}

// Containment needs ContainedMinWords on the shorter side, so a short fact
// whose words a long one holds is not refused.
func TestAShortFactInsideALongOneIsNotRefused(t *testing.T) {
	s := newStore(t)
	add(t, s, Draft{Text: earlierHashA}, AddOptions{})
	if r := add(t, s, Draft{Text: "department template update"}, AddOptions{}); r.Op != OpAdd {
		t.Fatalf("short fact: %+v", r)
	}
	if c := Contained("department template update", earlierHashA); c != 0 {
		t.Errorf("Contained of a 3-word text = %.2f, want 0", c)
	}
	if c := Contained(earlierHashA, earlierHashB); c < ContainedThreshold {
		t.Errorf("Contained of the retold pair = %.2f, want >= %.2f", c, ContainedThreshold)
	}
}

// A file under cmd/issue-cli/templates/ maps to the template tag, and
// ForTags keeps the valid, unpinned, not yet shown memories carrying it
// within its caps.
func TestForTagsPicksTheTaggedMemories(t *testing.T) {
	if got := TagsForFiles([]string{"internal/x/y.go", "./cmd/issue-cli/templates/departments/sales-b2c/role.md"}); len(got) != 1 || got[0] != "template" {
		t.Fatalf("TagsForFiles = %v", got)
	}
	if got := TagsForFiles([]string{"cmd/issue-cli/main.go"}); len(got) != 0 {
		t.Fatalf("TagsForFiles outside the folder = %v", got)
	}
	mems := []Memory{
		{ID: "m-1", Text: "tagged one", Tags: []string{"Template"}, Created: stamp(t0)},
		{ID: "m-2", Text: "tagged but pinned", Tags: []string{"template"}, Pinned: true, Created: stamp(t0)},
		{ID: "m-3", Text: "tagged but shown", Tags: []string{"template"}, Created: stamp(t0)},
		{ID: "m-4", Text: "tagged but gone", Tags: []string{"template"}, InvalidAt: stamp(t0), Created: stamp(t0)},
		{ID: "m-5", Text: "another tag", Tags: []string{"seo"}, Created: stamp(t0)},
		{ID: "m-6", Text: "tagged two", Tags: []string{"seo", "template"}, Created: stamp(t0)},
		{ID: "m-7", Text: "tagged three, too many", Tags: []string{"template"}, Created: stamp(t0)},
	}
	p := ForTags(mems, []string{"template"}, map[string]bool{"m-3": true}, t0, 2, 900)
	if len(p.Shown) != 2 || p.More != 1 {
		t.Fatalf("ForTags = %+v", p)
	}
	for _, m := range p.Shown {
		if m.ID != "m-1" && m.ID != "m-6" && m.ID != "m-7" {
			t.Errorf("ForTags showed %s", m.ID)
		}
	}
	if p := ForTags(mems, nil, nil, t0, 3, 900); len(p.Shown) != 0 {
		t.Errorf("no tags showed %+v", p.Shown)
	}
}
