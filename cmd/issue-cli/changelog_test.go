package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// misplacedChangelogEntries returns the entry lines head has and base lacks
// that sit below head's first version heading. CHANGELOG.md merges with
// merge=union, so after a release renamed ## Unreleased, git can put a
// branch's new entry under the released version without a conflict.
func misplacedChangelogEntries(base, head string) []string {
	have := map[string]int{}
	for _, l := range strings.Split(base, "\n") {
		have[l]++
	}
	var out []string
	released := false
	for _, l := range strings.Split(head, "\n") {
		if strings.HasPrefix(l, "## v") {
			released = true
		}
		if have[l] > 0 {
			have[l]--
			continue
		}
		if released && strings.HasPrefix(l, "- ") {
			out = append(out, l)
		}
	}
	return out
}

func TestMisplacedChangelogEntries(t *testing.T) {
	t.Parallel()

	base := "# Changelog\n\n## v0.58.0 — 2026-10-31\n\n- A\n- B\n\n## v0.57.0 — 2026-10-30\n\n- old\n"
	cases := []struct {
		name, head string
		want       []string
	}{
		{"entry under Unreleased", "# Changelog\n\n## Unreleased\n\n- C\n\n## v0.58.0 — 2026-10-31\n\n- A\n- B\n\n## v0.57.0 — 2026-10-30\n\n- old\n", nil},
		{"entry merged under the release", "# Changelog\n\n## v0.58.0 — 2026-10-31\n\n- C\n- A\n- B\n\n## v0.57.0 — 2026-10-30\n\n- old\n", []string{"- C"}},
		{"entry in an older release", "# Changelog\n\n## v0.58.0 — 2026-10-31\n\n- A\n- B\n\n## v0.57.0 — 2026-10-30\n\n- C\n- old\n", []string{"- C"}},
		{"unchanged", base, nil},
	}
	for _, c := range cases {
		got := misplacedChangelogEntries(base, c.head)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// TestChangelogBranchEntriesUnreleased checks this branch's CHANGELOG.md
// against its merge base with main: every entry the branch added sits above
// the first version heading, under ## Unreleased. On main, a release
// branch or outside a git checkout there is nothing to compare.
func TestChangelogBranchEntriesUnreleased(t *testing.T) {
	t.Parallel()

	gitOut := func(args ...string) (string, bool) {
		out, err := exec.Command("git", args...).Output()
		return strings.TrimSpace(string(out)), err == nil
	}
	branch, ok := gitOut("rev-parse", "--abbrev-ref", "HEAD")
	if !ok || branch == "main" || strings.HasPrefix(branch, "release/") {
		t.Skipf("not a work branch (%q)", branch)
	}
	baseRev, ok := gitOut("merge-base", "main", "HEAD")
	if !ok {
		t.Skip("no merge base with main")
	}
	prefix, _ := gitOut("rev-parse", "--show-prefix")
	base, ok := gitOut("show", baseRev+":"+prefix+"CHANGELOG.md")
	if !ok {
		t.Skip("CHANGELOG.md not at the merge base")
	}
	head, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	if bad := misplacedChangelogEntries(base, string(head)); len(bad) > 0 {
		t.Errorf("cmd/issue-cli/CHANGELOG.md: %d entr(ies) this branch added sit under a released version heading; move them under ## Unreleased at the top (create the heading if a release took it):\n%s",
			len(bad), strings.Join(bad, "\n"))
	}
}

// TestChangelogUnionMerge merges two branches that each add an entry at the
// top of ## Unreleased, in a scratch repo with this repo's .gitattributes:
// no conflict, both entries kept. Then a release renames ## Unreleased on
// main, and merging it into a third branch is checked the same way.
func TestChangelogUnionMerge(t *testing.T) {
	t.Parallel()

	attrs, err := os.ReadFile("../../.gitattributes")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	run := func(args ...string) (string, error) {
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	must := func(args ...string) {
		t.Helper()
		if out, err := run(args...); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	const path = "cmd/issue-cli/CHANGELOG.md"
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(dir+"/"+path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func() string {
		t.Helper()
		b, err := os.ReadFile(dir + "/" + path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	addEntry := func(entry string) {
		t.Helper()
		write(strings.Replace(read(), "## Unreleased\n\n", "## Unreleased\n\n"+entry+"\n", 1))
		must("commit", "-qam", entry)
	}

	must("init", "-q", "-b", "main")
	if err := os.MkdirAll(dir+"/cmd/issue-cli", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/.gitattributes", attrs, 0o644); err != nil {
		t.Fatal(err)
	}
	write("# Changelog\n\n## Unreleased\n\n- A\n\n## v0.57.0 — 2026-10-30\n\n- old\n")
	must("add", ".")
	must("commit", "-qm", "base")

	must("checkout", "-qb", "one")
	addEntry("- one")
	must("checkout", "-q", "main")
	must("checkout", "-qb", "two")
	addEntry("- two")
	must("checkout", "-q", "main")
	must("merge", "-q", "--ff-only", "one")
	must("checkout", "-q", "two")
	if out, err := run("merge", "-q", "-m", "merge main", "main"); err != nil {
		t.Fatalf("merging two branches' Unreleased entries conflicted: %v\n%s\n%s", err, out, read())
	}
	got := read()
	for _, e := range []string{"- one", "- two", "- A"} {
		if strings.Count(got, e+"\n") != 1 {
			t.Errorf("after the merge %q appears %d times, want once:\n%s", e, strings.Count(got, e+"\n"), got)
		}
	}
	if bad := misplacedChangelogEntries("", got); len(bad) != 1 || bad[0] != "- old" {
		t.Errorf("entries below the first version heading = %q, want only the released \"- old\"", bad)
	}

	// The release: main's Unreleased becomes v0.58.0 while branch three adds an entry.
	must("checkout", "-q", "main")
	must("merge", "-q", "--ff-only", "two")
	must("checkout", "-qb", "three")
	addEntry("- three")
	must("checkout", "-q", "main")
	write(strings.Replace(read(), "## Unreleased", "## v0.58.0 — 2026-10-31", 1))
	must("commit", "-qam", "release")
	base := read()
	must("checkout", "-q", "three")
	if out, err := run("merge", "-q", "-m", "merge main", "main"); err != nil {
		t.Fatalf("merging the release into a branch conflicted: %v\n%s\n%s", err, out, read())
	}
	got = read()
	if strings.Count(got, "- three\n") != 1 {
		t.Errorf("after the release merge \"- three\" appears %d times, want once:\n%s", strings.Count(got, "- three\n"), got)
	}
	// Wherever union put it, the check names it unless it is above the release.
	bad := misplacedChangelogEntries(base, got)
	above := strings.Index(got, "- three\n") < strings.Index(got, "## v0.58.0")
	if above != (len(bad) == 0) {
		t.Errorf("entry above the release heading = %v but misplaced = %q:\n%s", above, bad, got)
	}
}
