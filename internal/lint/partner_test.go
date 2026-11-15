package lint

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestCheckDepartmentsPartner(t *testing.T) {
	// A git checkout standing in for the partner, at one commit.
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo, "-c", "maintenance.auto=false", "-c", "gc.auto=0", "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "first")
	head := git("rev-parse", "HEAD")

	// The owner's AGPL exception, so Video's licence is in order too.
	env := lintProject(t, minimalWorkflow, map[string]string{"video.md": "---\ntitle: \"Video\"\nstatus: \"done\"\n---\n## Decisions\n" +
		"- **D-1** · 2026-11-02 · CEO chat: OpenMontage is accepted as an AGPL exception, \"ok\"\n"})
	env.Project.Departments = []tracker.Department{
		{Name: "Video", Partner: &tracker.Partner{Name: "OpenMontage", Adapter: "openmontage", Path: repo, Commit: head[:7], Exception: "video#D-1"}},
		{Name: "Pinned wrong", Partner: &tracker.Partner{Adapter: "openmontage", Path: repo, Commit: "0000000"}},
		{Name: "Gone", Partner: &tracker.Partner{Adapter: "openmontage", Path: filepath.Join(repo, "nope")}},
		{Name: "Odd", Partner: &tracker.Partner{Adapter: "odd-studio", Path: repo}},
		{Name: "Bare", Partner: &tracker.Partner{Name: "X"}},
	}
	var got []string
	for _, f := range checkDepartments(env) {
		got = append(got, f.Where+": "+f.Message)
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		"projects.yaml (Pinned wrong): partner openmontage: the checkout is at " + gitdir.ShortSHA(head) + ", pinned to 0000000",
		"projects.yaml (Gone): partner openmontage: path " + filepath.Join(repo, "nope") + " is not a folder",
		`projects.yaml (Odd): partner adapter "odd-studio" is unknown (one of openmontage)`,
		"projects.yaml (Bare): partner X names no adapter:",
		"projects.yaml (Bare): partner X has no path:",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing finding %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "(Video)") {
		t.Errorf("a partner at its pin is flagged:\n%s", all)
	}
}

// AC2: a gated stage approved at the partner (human_approved) with no
// owner decision on its issue is flagged; one with the owner's
// partner-approved D-n is not, nor an issue whose partner has no approvals.
func TestCheckDepartmentsPartnerApprovalWithoutDecision(t *testing.T) {
	fixture, err := filepath.Abs(filepath.Join("..", "partner", "testdata", "openmontage"))
	if err != nil {
		t.Fatal(err)
	}
	issue := func(title, project, decisions string) string {
		return "---\ntitle: \"" + title + "\"\nstatus: \"backlog\"\nsystem: \"Video\"\npartner_project: \"" + project + "\"\n---\n\nBody.\n" + decisions
	}
	env := lintProject(t, minimalWorkflow, map[string]string{
		"Video/unpaired.md": issue("Unpaired", "all-done", ""),
		"Video/paired.md": issue("Paired", "all-done", "\n## Decisions\n"+
			"- **D-1** · 2026-11-02 · owner on Needs you: OpenMontage pitch approved, \"concept B\" (ref partner-approved:pitch@20261101T110000Z)\n"+
			"- **D-2** · 2026-11-02 · owner on the Station: OpenMontage storyboard approved (ref partner-approved:storyboard)\n"),
		"Video/waiting.md": issue("Waiting", "waiting", ""),
	})
	env.Project.Departments = []tracker.Department{{Name: "Video", Systems: []string{"Video"}, Partner: &tracker.Partner{Name: "OpenMontage", Adapter: "openmontage", Path: fixture}}}
	var got []string
	for _, f := range checkDepartments(env) {
		got = append(got, f.Where+": "+f.Message)
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		"video/unpaired: OpenMontage: pitch approved at the partner with no owner decision (partner-approved:pitch)",
		"video/unpaired: OpenMontage: storyboard approved at the partner with no owner decision (partner-approved:storyboard)",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing finding %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "video/paired") || strings.Contains(all, "video/waiting") {
		t.Errorf("a paired or unapproved issue is flagged:\n%s", all)
	}
	if strings.Count(all, "approved at the partner") != 2 {
		t.Errorf("want exactly 2 findings (ungated stages aren't checked):\n%s", all)
	}
}
