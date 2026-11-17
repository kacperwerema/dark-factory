package lint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// TestLintShasFix: --fix swaps a stale ref only when, of the commits on
// main that name the issue's slug, exactly one has the stale commit's
// subject; it keeps a ref with two such commits and one whose landed
// commit doesn't name the slug (with the subject-only hint), and it
// changes only whole words in ## Implementation.
func TestLintShasFix(t *testing.T) {
	for k, v := range map[string]string{"GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_DATE": "2026-11-01T10:00:00Z", "GIT_COMMITTER_DATE": "2026-11-01T10:00:00Z"} {
		t.Setenv(k, v)
	}
	env := lintProject(t, minimalWorkflow, nil)
	root := env.Project.Root()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "maintenance.auto=false", "-c", "gc.auto=0", "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	n := 0
	// commit writes a change and commits msg; amend then rewrites it with
	// the same message and other content, so the SHA from before is off
	// main. It returns the SHA a short ref of it would be (7 characters).
	commit := func(msg string, amend bool) string {
		t.Helper()
		n++
		os.WriteFile(filepath.Join(root, "a.txt"), []byte(strings.Repeat("x", n)), 0o644)
		git("add", "a.txt")
		if amend {
			git("commit", "-q", "--amend", "--no-edit")
		} else {
			git("commit", "-q", "-m", msg)
		}
		return git("rev-parse", "--short=7", "HEAD")
	}
	git("init", "-q", "-b", "main")
	commit("first", false)

	staleBefore := commit("feat: the work\n\nIssue: sys/stale", false)
	staleLanded := commit("", true)
	twiceFirst := commit("feat: twice\n\nIssue: sys/twice", false)
	twiceBefore := commit("feat: twice\n\nIssue: sys/twice", false)
	commit("", true)
	unnamedBefore := commit("feat: unnamed", false)
	unnamedLanded := commit("", true)
	if staleBefore == staleLanded || twiceFirst == twiceBefore {
		t.Fatalf("amend kept the SHA: %s %s", staleBefore, staleLanded)
	}

	issue := func(title, impl string) string {
		return "---\ntitle: \"" + title + "\"\nstatus: \"done\"\ncreated: \"2026-11-01\"\n---\n\n## Implementation\n- [x] Code changes complete\n\n" + impl + "\n\n## Test Plan\n- [x] " + staleBefore + " here is not Implementation\n"
	}
	files := map[string]string{
		"Sys/stale.md":   issue("Stale", "Commit `"+staleBefore+"` (was "+staleBefore+"), not #"+staleBefore+" nor "+staleBefore+"0."),
		"Sys/twice.md":   issue("Twice", "Commit "+twiceBefore+"."),
		"Sys/unnamed.md": issue("Unnamed", "Commit "+unnamedBefore+"."),
		// Quotes sys/stale's stale SHA as an example: the slug grep
		// finds nothing for this issue, so it is kept.
		"Sys/example.md": issue("Example", "Like "+staleBefore+" in sys/stale."),
	}
	for name, content := range files {
		p := filepath.Join(env.Project.IssueDir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
	}
	env, err := Load(env.Project, lintNow)
	if err != nil {
		t.Fatal(err)
	}
	if got := run(t, env, "shas"); len(got) != 4 || !has(got, "sys/stale", "health lint --check shas --fix") {
		t.Fatalf("before: %+v", got)
	}

	fix, err := FixSHAs(env)
	if err != nil {
		t.Fatal(err)
	}
	if len(fix.Swaps) != 1 || fix.Swaps[0] != (SHASwap{Slug: "sys/stale", Old: staleBefore, New: staleLanded, Subject: "feat: the work"}) || fix.Issues != 1 {
		t.Errorf("swaps = %+v (issues %d)", fix.Swaps, fix.Issues)
	}
	kept := map[string]SHAKept{}
	for _, k := range fix.Kept {
		kept[k.Slug] = k
	}
	if k := kept["sys/twice"]; k.Ref != twiceBefore || !strings.Contains(k.Reason, "2 commits on main name sys/twice with this subject") {
		t.Errorf("twice: %+v", k)
	}
	if k := kept["sys/unnamed"]; !strings.Contains(k.Reason, "no commit on main names sys/unnamed") || !strings.Contains(k.Hint, unnamedLanded) || k.Subject != "feat: unnamed" {
		t.Errorf("unnamed: %+v", k)
	}
	if k := kept["sys/example"]; !strings.Contains(k.Reason, "no commit on main names sys/example") {
		t.Errorf("example: %+v", k)
	}
	if len(fix.Kept) != 3 {
		t.Errorf("kept = %+v", fix.Kept)
	}

	raw, _ := os.ReadFile(filepath.Join(env.Project.IssueDir, "Sys/stale.md"))
	body := string(raw)
	impl, _ := tracker.SectionText(body, "Implementation")
	want := "Commit `" + staleLanded + "` (was " + staleLanded + "), not #" + staleBefore + " nor " + staleBefore + "0."
	if !strings.Contains(impl, want) {
		t.Errorf("## Implementation = %q, want %q", impl, want)
	}
	if plan, _ := tracker.SectionText(body, "Test Plan"); !strings.Contains(plan, staleBefore+" here is not Implementation") {
		t.Errorf("## Test Plan changed: %q", plan)
	}
	for _, f := range []string{"Sys/twice.md", "Sys/unnamed.md", "Sys/example.md"} {
		if raw, _ := os.ReadFile(filepath.Join(env.Project.IssueDir, f)); string(raw) != files[f] {
			t.Errorf("%s changed:\n%s", f, raw)
		}
	}

	env, _ = Load(env.Project, lintNow)
	if got := run(t, env, "shas"); len(got) != 3 || has(got, "sys/stale") {
		t.Errorf("after: %+v", got)
	}
}
