package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// evidenceFiles lists the names in cli/sample's evidence folder.
func evidenceFiles(t *testing.T, proj *tracker.Project) []string {
	t.Helper()
	entries, _ := os.ReadDir(tracker.EvidenceDir(proj.IssueDir, "cli/sample"))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// Run from a worktree that has no issues/ tree, with the slug left out (a
// dispatched worker's call), evidence add lands the still in the primary
// checkout's issues/_evidence/<slug>/ and prints its path and link.
func TestEvidenceAddFromAWorktree(t *testing.T) {
	proj, _ := makeGateFixture(t, "in progress", "", true)
	root := filepath.Dir(proj.IssueDir)
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte("issues/\n.worktrees/\n"), 0o644)
	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "add", "-A")
	git(t, root, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init")
	tree := filepath.Join(root, ".worktrees", "sample")
	git(t, root, "worktree", "add", "-q", "-b", "work/cli/sample", tree)
	if _, err := os.Stat(filepath.Join(tree, "issues")); !os.IsNotExist(err) {
		t.Fatalf("the worktree has an issues/ tree (%v); the test needs one without", err)
	}
	data := pngBytes(t)
	os.WriteFile(filepath.Join(tree, "shot.png"), data, 0o644)
	withCwd(t, tree)
	t.Setenv(tracker.OwnSlugEnv, "cli/sample")

	ctx, stdout, stderr := newTestContext(proj, false)
	if err := runEvidence(ctx, []string{"add", "shot.png"}); err != nil {
		t.Fatalf("evidence add: %v\n%s", err, stderr)
	}
	want := filepath.Join(root, "issues", "_evidence", "cli", "sample", "shot.png")
	saved, err := os.ReadFile(want)
	if err != nil || !bytes.Equal(saved, data) {
		t.Fatalf("%s: %v (same bytes: %v)", want, err, bytes.Equal(saved, data))
	}
	assertContains(t, stdout.String(), "✓ "+want+"\n")
	assertContains(t, stdout.String(), "![shot](/p/test/files/issues/_evidence/cli/sample/shot.png)")
	if _, err := os.Stat(filepath.Join(tree, "issues")); !os.IsNotExist(err) {
		t.Errorf("evidence add made an issues/ tree in the worktree (%v)", err)
	}

	// A name through .. keeps only its base: nothing lands outside the folder.
	os.MkdirAll(filepath.Join(tree, "sub"), 0o755)
	os.WriteFile(filepath.Join(tree, "sub", "up.png"), data, 0o644)
	ctx, _, _ = newTestContext(proj, false)
	if err := runEvidence(ctx, []string{"add", "sub/../sub/up.png", "--step", "tp3"}); err != nil {
		t.Fatalf("evidence add sub/../sub/up.png: %v", err)
	}
	if got := evidenceFiles(t, proj); strings.Join(got, ",") != "TP3-up.png,shot.png" {
		t.Errorf("evidence folder = %v, want TP3-up.png and shot.png", got)
	}
	if _, err := os.Stat(filepath.Join(root, "issues", "_evidence", "cli", "up.png")); !os.IsNotExist(err) {
		t.Errorf("a still landed outside the issue's folder")
	}
}

// A name already in the folder is refused, and the refusal saves none of the
// call's files; --force replaces it.
func TestEvidenceAddRefusesOverwrite(t *testing.T) {
	proj, _, scratch := attachFixture(t)
	first, second := filepath.Join(scratch, "after.png"), filepath.Join(scratch, "other.png")
	data := pngBytes(t)
	os.WriteFile(first, data, 0o644)
	os.WriteFile(second, data, 0o644)

	ctx, _, _ := newTestContext(proj, false)
	if err := runEvidence(ctx, []string{"add", "cli/sample", first}); err != nil {
		t.Fatalf("first add: %v", err)
	}
	newer := append(pngBytes(t), 0)
	os.WriteFile(first, newer, 0o644)

	ctx, _, _ = newTestContext(proj, false)
	err := runEvidence(ctx, []string{"add", "cli/sample", second, first})
	var ec *exitCodeError
	if !errors.As(err, &ec) || ec.Code != 1 {
		t.Fatalf("add over an existing name = %v, want exit 1", err)
	}
	assertContains(t, ec.Msg, "after.png is already in")
	assertContains(t, ec.Msg, "nothing saved")
	assertContains(t, ec.Msg, "--force")
	if got := evidenceFiles(t, proj); strings.Join(got, ",") != "after.png" {
		t.Errorf("after a refusal the folder = %v, want after.png only", got)
	}
	path := filepath.Join(tracker.EvidenceDir(proj.IssueDir, "cli/sample"), "after.png")
	if b, _ := os.ReadFile(path); !bytes.Equal(b, data) {
		t.Error("the refused call changed after.png")
	}

	ctx, _, _ = newTestContext(proj, false)
	if err := runEvidence(ctx, []string{"add", "cli/sample", first, "--force"}); err != nil {
		t.Fatalf("add --force: %v", err)
	}
	if b, _ := os.ReadFile(path); !bytes.Equal(b, newer) {
		t.Error("--force did not replace after.png")
	}

	// Named like an image but text: refused, nothing saved.
	txt := filepath.Join(scratch, "notes.png")
	os.WriteFile(txt, []byte("plain text"), 0o644)
	ctx, _, _ = newTestContext(proj, false)
	if err := runEvidence(ctx, []string{"add", "cli/sample", txt}); err == nil || !strings.Contains(err.Error(), "named like an image") {
		t.Errorf("add of a text file named .png = %v, want named like an image", err)
	}
	// A bad step id is a usage error.
	ctx, _, _ = newTestContext(proj, false)
	if err := runEvidence(ctx, []string{"add", "cli/sample", second, "--step", "../x"}); !errors.As(err, &ec) || ec.Code != 2 {
		t.Errorf("--step ../x = %v, want exit 2", err)
	}
}

// Any file that is no image (a pprof -top capture, a profile) is saved
// under its own name with the step in front, and printed with a plain link;
// one named like an image is refused and saves nothing.
func TestEvidenceAddAnyFile(t *testing.T) {
	proj, _, scratch := attachFixture(t)
	root := filepath.Dir(proj.IssueDir)
	top := filepath.Join(scratch, "top.txt")
	os.WriteFile(top, []byte("Showing: 12.40s\n 4.10s os.Stat\n"), 0o644)
	prof := filepath.Join(scratch, "cpu.pprof")
	os.WriteFile(prof, []byte{0x1f, 0x8b, 0x08, 0, 1, 2}, 0o644)
	fake := filepath.Join(scratch, "fake.png")
	os.WriteFile(fake, []byte("plain text"), 0o644)

	ctx, _, _ := newTestContext(proj, false)
	if err := runEvidence(ctx, []string{"add", "cli/sample", top, fake, "--step", "AC1"}); err == nil || !strings.Contains(err.Error(), "nothing saved") {
		t.Fatalf("add with a fake image = %v, want refused", err)
	}
	if got := evidenceFiles(t, proj); len(got) != 0 {
		t.Fatalf("a refused call saved %v", got)
	}

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runEvidence(ctx, []string{"add", "cli/sample", top, prof, "--step", "AC1"}); err != nil {
		t.Fatalf("evidence add: %v", err)
	}
	if got := evidenceFiles(t, proj); strings.Join(got, ",") != "AC1-cpu.pprof,AC1-top.txt" {
		t.Errorf("evidence folder = %v", got)
	}
	rel, _ := filepath.Rel(root, tracker.EvidenceDir(proj.IssueDir, "cli/sample"))
	url := "/p/test/files/" + filepath.ToSlash(rel) + "/"
	assertContains(t, stdout.String(), "  [AC1-top.txt]("+url+"AC1-top.txt)\n")
	assertContains(t, stdout.String(), "  [AC1-cpu.pprof]("+url+"AC1-cpu.pprof)\n")
	if strings.Contains(stdout.String(), "![") {
		t.Errorf("a file got an image link:\n%s", stdout)
	}
	saved, _ := os.ReadFile(filepath.Join(tracker.EvidenceDir(proj.IssueDir, "cli/sample"), "AC1-cpu.pprof"))
	if !bytes.Equal(saved, []byte{0x1f, 0x8b, 0x08, 0, 1, 2}) {
		t.Error("the profile's bytes changed")
	}
}

// evidence list prints the folder and each still; with none it names the add
// command; --json is the records.
func TestEvidenceList(t *testing.T) {
	proj, _, scratch := attachFixture(t)

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runEvidence(ctx, []string{"list", "cli/sample"}); err != nil {
		t.Fatalf("list on none: %v", err)
	}
	assertContains(t, stdout.String(), "No evidence for cli/sample yet: issue-cli evidence add cli/sample <file>")

	for _, n := range []string{"before.png", "after.png"} {
		os.WriteFile(filepath.Join(scratch, n), pngBytes(t), 0o644)
	}
	ctx, _, _ = newTestContext(proj, false)
	if err := runEvidence(ctx, []string{"add", "cli/sample", filepath.Join(scratch, "before.png"), filepath.Join(scratch, "after.png"), "--step", "TP2"}); err != nil {
		t.Fatalf("add: %v", err)
	}

	ctx, stdout, _ = newTestContext(proj, false)
	if err := runEvidence(ctx, []string{"ls", "cli/sample"}); err != nil {
		t.Fatalf("list: %v", err)
	}
	out := stdout.String()
	assertContains(t, out, tracker.EvidenceDir(proj.IssueDir, "cli/sample")+" (2)\n")
	if a, b := strings.Index(out, "  TP2-after.png  "), strings.Index(out, "  TP2-before.png  "); a < 0 || b < a {
		t.Errorf("list = %q, want TP2-after.png then TP2-before.png", out)
	}

	ctx, stdout, _ = newTestContext(proj, true)
	if err := runEvidence(ctx, []string{"list", "cli/sample"}); err != nil {
		t.Fatalf("list --json: %v", err)
	}
	var got []tracker.EvidenceFile
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil || len(got) != 2 || got[0].Step != "TP2" || got[0].Name != "TP2-after.png" {
		t.Errorf("list --json = %s (%v)", stdout, err)
	}
}
