package privacy

import (
	"path/filepath"
	"strings"
	"testing"
)

// After the branch point, main gets a .go fix with no Refs: line, a
// changelog-only commit, a guided check script and its drop, and an
// issue's commit with Refs:. NoRefs lists only the .go fix, with the
// command that picks it by sha; once picked (-x) it is gone.
func TestNoRefsListsMainCommitsReleasePickBySlugMisses(t *testing.T) {
	t.Parallel()
	r := newReleaseRepo(t)
	run(t, r.root, "git", "branch", "release/v0.53")
	commit := func(file, text, msg string) string {
		t.Helper()
		write(t, filepath.Join(r.root, file), text)
		run(t, r.root, "git", "add", "-A")
		run(t, r.root, "git", "commit", "-q", "-m", msg)
		return strings.TrimSpace(run(t, r.root, "git", "rev-parse", "HEAD"))
	}
	fix := commit("fix.go", "package main\n\nvar fixed = true\n", "fix(cli): a follow-up commit after the issue's first\n\nCo-Authored-By: A <a@example.com>")
	commit("CHANGELOG.md", "# Changelog\n\n## Unreleased\n\n- fixed\n- feature A\n\n## v0.53.0 — 2026-10-23\n\n- old\n", "docs(changelog): fixed")
	commit("scripts/checks/fix.sh", "#!/bin/sh\n", "test(checks): guided check")
	run(t, r.root, "git", "rm", "-q", "scripts/checks/fix.sh")
	run(t, r.root, "git", "commit", "-q", "-m", "chore(checks): drop the check before the merge")
	commit("other.go", "package main\n\nvar other = 1\n", "feat: other\n\nRefs: ui/other")

	got, err := NoRefs(r.root, "release/v0.53", "main", "CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SHA != fix || got[0].Subject != "fix(cli): a follow-up commit after the issue's first" {
		t.Fatalf("NoRefs = %+v; want only %s, the .go fix", got, fix)
	}
	if want := "issue-cli release pick " + fix[:7]; got[0].PickCommand() != want {
		t.Errorf("pick command = %q, want %q", got[0].PickCommand(), want)
	}

	run(t, r.root, "git", "checkout", "-q", "release/v0.53")
	run(t, r.root, "git", "cherry-pick", "-x", fix)
	run(t, r.root, "git", "checkout", "-q", "main")
	if got, err := NoRefs(r.root, "release/v0.53", "main", "CHANGELOG.md"); err != nil || len(got) != 0 {
		t.Fatalf("after the pick NoRefs = %+v, %v; want none", got, err)
	}
}
