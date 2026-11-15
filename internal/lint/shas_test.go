package lint

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSHARefs(t *testing.T) {
	got := shaRefs("Commit `a05f230` (was b55d24e0), not #1e1e2eff, defaced, 20261115, abc12, a05f230 again, main..6ae4742.")
	want := []string{"a05f230", "b55d24e0", "6ae4742"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("shaRefs = %v, want %v", got, want)
	}
}

// TestSHAsFlagsTheCommitFromBeforeAnAmend: a done issue naming the SHA a
// commit had before git commit --amend is flagged with the landed-SHA fix;
// one naming the landed commit, an open issue naming the old one and a
// parked one are not, nor a ref git doesn't know (another repository's
// commit, an attachment's hash).
func TestSHAsFlagsTheCommitFromBeforeAnAmend(t *testing.T) {
	for k, v := range map[string]string{"GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_NOSYSTEM": "1",
		// Fixed dates, fixed SHAs: each has a letter and a digit, as a ref
		// must to be read as one (shaRefs).
		"GIT_AUTHOR_DATE": "2026-11-01T10:00:00Z", "GIT_COMMITTER_DATE": "2026-11-01T10:00:00Z"} {
		t.Setenv(k, v)
	}
	issue := func(title, status, impl string) string {
		return "---\ntitle: \"" + title + "\"\nstatus: \"" + status + "\"\ncreated: \"2026-11-01\"\n---\n\n## Implementation\n- [x] Code changes complete\n\n" + impl + "\n\n## Test Plan\n- [x] a05f230 here is not Implementation\n"
	}
	env := lintProject(t, minimalWorkflow, nil)
	root := env.Project.Root()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root, "-c", "maintenance.auto=false", "-c", "gc.auto=0", "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("a\n"), 0o644)
	git("add", "a.txt")
	git("commit", "-q", "-m", "first")
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("b\n"), 0o644)
	git("commit", "-q", "-am", "feat: the work")
	before := git("rev-parse", "--short=7", "HEAD")
	git("commit", "-q", "--amend", "-m", "feat: the work, amended")
	landed := git("rev-parse", "--short=7", "HEAD")
	if before == landed || len(shaRefs(before+" "+landed)) != 2 {
		t.Fatalf("before %s, landed %s: want two different refs shaRefs reads", before, landed)
	}

	issues := map[string]string{
		"Sys/stale.md":  issue("Stale", "done", "Commit `"+before+"`, on top of "+landed+"."),
		"Sys/landed.md": issue("Landed", "done", "Commit "+landed+"."),
		"Sys/open.md":   issue("Open", "idea", "Commit "+before+"."),
		"Sys/gone.md":   issue("Gone", "done", "Commit dead123 then."),
		"Sys/park.md":   issue("Park", "parked", "Commit "+before+"."),
	}
	for name, content := range issues {
		p := filepath.Join(env.Project.IssueDir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
	}
	env, err := Load(env.Project, lintNow)
	if err != nil {
		t.Fatal(err)
	}
	got := run(t, env, "shas")
	if len(got) != 1 {
		t.Fatalf("findings = %+v", got)
	}
	if !has(got, "sys/stale", "## Implementation names "+before+", not on main", "replace it with the landed SHA (git log --oneline main --grep sys/stale)") {
		t.Errorf("the pre-amend SHA: %+v", got)
	}
	if has(got, landed) {
		t.Errorf("the landed SHA flagged: %+v", got)
	}

	// Outside git there is nothing to check.
	if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
		t.Fatal(err)
	}
	if got := run(t, env, "shas"); len(got) != 0 {
		t.Errorf("outside git: %+v", got)
	}
}
