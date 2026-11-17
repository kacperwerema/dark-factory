package privacy

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The public repo's Release Marker action writes a release's notes with
// scripts/release-notes.sh. GitHub refuses a release body over 125,000
// characters (v0.62.0's section was 136,306), so a section over the
// script's limit goes out as the entries' bold titles and a link to the
// changelog at the tag.

const githubBodyLimit = 125000

func releaseNotesScript(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "scripts", "release-notes.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("scripts/release-notes.sh: %v", err)
	}
	return p
}

// runReleaseNotes runs the script on changelog for version and returns the
// notes it wrote.
func runReleaseNotes(t *testing.T, changelog, version string, env ...string) string {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "CHANGELOG.md")
	if err := os.WriteFile(in, []byte(changelog), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "notes.md")
	cmd := exec.Command("sh", releaseNotesScript(t), in, version, "owner/repo", out)
	cmd.Env = append(os.Environ(), env...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("release-notes.sh: %v\n%s", err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// bigSection is a changelog whose v9.9.0 section holds n entries of about
// 700 characters each, every third one without a bold title, with an older
// section after it.
func bigSection(n int) string {
	var b strings.Builder
	b.WriteString("# Changelog\n\n## Unreleased\n\n## v9.9.0 — 2026-11-15\n")
	for i := 0; i < n; i++ {
		body := strings.Repeat(fmt.Sprintf("Detail %d of the change, with `code` and a link. ", i), 12)
		if i%3 == 2 {
			fmt.Fprintf(&b, "- Test only: entry %d has no bold title. %s\n", i, body)
		} else {
			fmt.Fprintf(&b, "- **Entry %d has a `bold` title** (factory/slug-%d). %s\n", i, i, body)
		}
	}
	b.WriteString("\n## v9.8.0 — 2026-11-01\n- **Older entry**. Not in these notes.\n")
	return b.String()
}

func TestReleaseNotesOverTheLimitKeepTitlesAndLink(t *testing.T) {
	cl := bigSection(240)
	if len(cl) <= githubBodyLimit {
		t.Fatalf("fixture is %d bytes, want it over GitHub's limit", len(cl))
	}
	notes := runReleaseNotes(t, cl, "v9.9.0")
	if n := len([]rune(notes)); n > 120000 {
		t.Fatalf("notes are %d characters, want at most 120000", n)
	}
	link := "https://github.com/owner/repo/blob/v9.9.0/cmd/issue-cli/CHANGELOG.md"
	if !strings.Contains(notes, link) {
		t.Errorf("notes do not link to the changelog at the tag (%s):\n%.400s", link, notes)
	}
	if !strings.Contains(notes, "- **Entry 0 has a `bold` title**\n") {
		t.Errorf("entry 0 is not its bold title alone:\n%.600s", notes)
	}
	if !strings.Contains(notes, "- **Entry 239 has a `bold` title**\n") && !strings.Contains(notes, "- Test only: entry 239") {
		t.Errorf("the last entry is missing:\n...%s", notes[max(0, len(notes)-600):])
	}
	if strings.Contains(notes, "Detail 0 of the change") {
		t.Error("a bold entry kept its body")
	}
	if !strings.Contains(notes, "- Test only: entry 2 has no bold title.") {
		t.Errorf("an entry without a bold title lost its first sentence:\n%.900s", notes)
	}
	if strings.Contains(notes, "Older entry") {
		t.Error("the notes run into the next section")
	}
	if got := strings.Count(notes, "\n- "); got != 240 {
		t.Errorf("notes list %d entries, want 240", got)
	}
}

func TestReleaseNotesUnderTheLimitAreTheSectionInFull(t *testing.T) {
	cl := bigSection(20)
	notes := runReleaseNotes(t, cl, "v9.9.0")
	start := strings.Index(cl, "- **Entry 0")
	end := strings.Index(cl, "\n## v9.8.0")
	want := cl[start:end]
	if strings.TrimRight(notes, "\n") != strings.TrimRight(want, "\n") {
		t.Errorf("notes are not the section in full:\n got %d bytes\nwant %d bytes", len(notes), len(want))
	}
}

func TestReleaseNotesTitlesAloneStillOverAreCut(t *testing.T) {
	// A limit so small that even the titles don't fit: the notes stop at
	// the limit and say how many entries the changelog has left.
	notes := runReleaseNotes(t, bigSection(240), "v9.9.0", "RELEASE_NOTES_LIMIT=4000")
	if n := len([]rune(notes)); n > 4000 {
		t.Fatalf("notes are %d characters, want at most 4000", n)
	}
	if !strings.Contains(notes, "more in the changelog") {
		t.Errorf("cut notes don't say entries are left out:\n%s", notes)
	}
}

func TestReleaseNotesMissingSection(t *testing.T) {
	notes := runReleaseNotes(t, bigSection(3), "v1.0.0")
	if !strings.Contains(notes, "_No v1.0.0 section in") {
		t.Errorf("notes for a missing section = %q", notes)
	}
}

// TestReleaseNotesForTheRealV062 runs the script on the section GitHub
// refused (v0.62.0 in this repo's changelog).
func TestReleaseNotesForTheRealV062(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "cmd", "issue-cli", "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	notes := runReleaseNotes(t, string(b), "v0.62.0")
	if n := len([]rune(notes)); n > 120000 || n < 1000 {
		t.Fatalf("v0.62.0 notes are %d characters, want 1000..120000", n)
	}
	if !strings.Contains(notes, "- **No test file is over 1000 lines**\n") {
		t.Errorf("v0.62.0's first entry is not its title:\n%.600s", notes)
	}
}

// TestReleaseMarkerWritesNotesWithTheScript: the action must call the
// script, not an inline awk that has no limit.
func TestReleaseMarkerWritesNotesWithTheScript(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release-marker.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "scripts/release-notes.sh") {
		t.Error(".github/workflows/release-marker.yml does not write its notes with scripts/release-notes.sh")
	}
}

func TestReleaseNotesScriptPassesShellcheck(t *testing.T) {
	if _, err := exec.LookPath("shellcheck"); err != nil {
		t.Skip("no shellcheck on PATH: scripts/release-notes.sh goes unchecked (make linux-check runs it)")
	}
	if out, err := exec.Command("shellcheck", "-s", "sh", "-S", "warning", releaseNotesScript(t)).CombinedOutput(); err != nil {
		t.Errorf("shellcheck: %v\n%s", err, out)
	}
}
