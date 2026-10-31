package checks

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeScript(t *testing.T, root, name, body string) string {
	t.Helper()
	path := filepath.Join(root, Dir, name+".sh")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFindMatchesTheIssueHeader(t *testing.T) {
	wt, main := t.TempDir(), t.TempDir()
	writeScript(t, wt, "lead-tab", "#!/usr/bin/env bash\n# issue: api/lead-tab\nsource x\n")
	writeScript(t, wt, "other", "#!/usr/bin/env bash\n# issue: api/other\n")
	writeScript(t, wt, "no-header", "#!/usr/bin/env bash\necho hi\n")
	// The same name in a later root loses to the first.
	writeScript(t, main, "lead-tab", "#!/usr/bin/env bash\n# issue: api/lead-tab\n")
	writeScript(t, main, "late", "#!/usr/bin/env bash\n# a comment\n#issue:api/lead-tab trailing words\n")

	got := Find([]string{wt, "", filepath.Join(wt, "missing"), main}, "api/lead-tab")
	var names, roots []string
	for _, s := range got {
		names = append(names, s.Name)
		roots = append(roots, s.Root)
	}
	if !reflect.DeepEqual(names, []string{"lead-tab", "late"}) {
		t.Fatalf("names = %v", names)
	}
	if !reflect.DeepEqual(roots, []string{wt, main}) {
		t.Fatalf("roots = %v", roots)
	}
}

func TestFindMatchesASlugWithASpace(t *testing.T) {
	root := t.TempDir()
	writeScript(t, root, "probe", "#!/usr/bin/env bash\n# issue: level design/rock-sentinel\n")
	writeScript(t, root, "words", "#!/usr/bin/env bash\n# issue: level design/rock-sentinel  the boulder check\n")
	writeScript(t, root, "other", "#!/usr/bin/env bash\n# issue: level design/rock-sentinel-two\n")
	writeScript(t, root, "first-word", "#!/usr/bin/env bash\n# issue: level\n")

	var names []string
	for _, s := range Find([]string{root}, "level design/rock-sentinel") {
		names = append(names, s.Name)
	}
	if !reflect.DeepEqual(names, []string{"probe", "words"}) {
		t.Fatalf("names = %v", names)
	}
	if got := Find([]string{root}, "level"); len(got) != 1 || got[0].Name != "first-word" {
		t.Fatalf("Find(level) = %v", got)
	}
	if sk := Skeleton("level design/rock-sentinel", "probe", nil); !strings.Contains(sk, "#   issue-cli check-script 'level design/rock-sentinel'\n") {
		t.Fatalf("the run line doesn't quote the slug:\n%s", sk)
	}
}

func TestIssueOfOnlyReadsTheTopOfTheFile(t *testing.T) {
	root := t.TempDir()
	body := "#!/usr/bin/env bash\n" + strings.Repeat("# filler\n", headerLines) + "# issue: too/late\n"
	path := writeScript(t, root, "deep", body)
	if got := IssueOf(path); got != "" {
		t.Fatalf("IssueOf = %q, want none past line %d", got, headerLines)
	}
}

func TestDueUntilAnOwnerRunIsRecorded(t *testing.T) {
	root := t.TempDir()
	writeScript(t, root, "a", "# issue: x/y\n")
	writeScript(t, root, "b", "# issue: x/y\n")
	if got := Due([]string{root}, "x/y"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("Due = %v", got)
	}
	// A machine run doesn't count; an owner run does.
	rec := filepath.Join(root, RecordDir, "a")
	os.MkdirAll(rec, 0o755)
	os.WriteFile(filepath.Join(rec, "20261023T100000Z-machine.txt"), nil, 0o644)
	if got := Due([]string{root}, "x/y"); len(got) != 2 {
		t.Fatalf("a machine run cleared it: %v", got)
	}
	os.WriteFile(filepath.Join(rec, "20261023T110000Z-owner.txt"), nil, 0o644)
	if got := Due([]string{root}, "x/y"); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("Due after the owner run = %v", got)
	}
}

func TestWindowName(t *testing.T) {
	if got := WindowName("api/leads-only.terminal"); got != "check-script-api-leads-only-terminal" {
		t.Fatalf("WindowName = %q", got)
	}
}

func TestValidName(t *testing.T) {
	for name, want := range map[string]bool{"lead-tab": true, "prove2": true, "": false, "-x": false, "Lead": false, "a/b": false, "a.sh": false} {
		if ValidName(name) != want {
			t.Errorf("ValidName(%q) = %v", name, !want)
		}
	}
}

func TestLibPathIsStableAndHoldsTheLibrary(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	a, err := LibPath()
	if err != nil {
		t.Fatal(err)
	}
	b, err := LibPath()
	if err != nil || a != b {
		t.Fatalf("second LibPath = %q, %v; want %q", b, err, a)
	}
	data, _ := os.ReadFile(a)
	if string(data) != string(Lib) {
		t.Fatal("the cached library differs from the embedded one")
	}
}

func TestSkeletonIsValidBashWithOneStepPerBox(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	sk := Skeleton("cli/x", "x", []Box{{ID: "TP2", Text: "The owner's sandbox"}, {ID: "TP3", Text: "steps written"}})
	for _, want := range []string{"# issue: cli/x\n", `source "${CHECKS_LIB:?`, "step TP2 'The owner'\\''s sandbox' --box TP2", "step TP3 'steps written' --box TP3", "\nsummary\n"} {
		if !strings.Contains(sk, want) {
			t.Errorf("skeleton lacks %q:\n%s", want, sk)
		}
	}
	path := writeScript(t, t.TempDir(), "x", sk)
	if out, err := exec.Command("bash", "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("bash -n: %v\n%s", err, out)
	}
	if IssueOf(path) != "cli/x" {
		t.Fatal("the skeleton's header isn't found")
	}
	if !strings.Contains(Skeleton("cli/x", "x", nil), "step MV1 ") {
		t.Fatal("no boxes: want one example step")
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestIssueWorktreeFindsTheCheckoutOnTheIssueBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	main := t.TempDir()
	gitRun(t, main, "init", "-q", "-b", "main")
	gitRun(t, main, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init")
	if got := IssueWorktree(main, "plan/fix"); got != "" {
		t.Fatalf("no worktree yet: %q", got)
	}
	// A worktree named freely, on work/<slug>.
	wt := filepath.Join(main, ".worktrees", "short-name")
	gitRun(t, main, "worktree", "add", "-q", "-b", "work/plan/fix", wt)
	want, _ := filepath.EvalSymlinks(wt)
	got := IssueWorktree(main, "plan/fix")
	if resolved, _ := filepath.EvalSymlinks(got); resolved != want {
		t.Fatalf("IssueWorktree = %q, want %q", got, want)
	}
	if got := IssueWorktree(main, "plan/other"); got != "" {
		t.Fatalf("another slug: %q", got)
	}
	if got := IssueWorktree(t.TempDir(), "plan/fix"); got != "" {
		t.Fatalf("not a repo: %q", got)
	}
}

func TestMergedIntoOnlyForACleanWorktreeOnMain(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	main := t.TempDir()
	commit := func(dir, msg string) {
		gitRun(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", msg)
	}
	gitRun(t, main, "init", "-q", "-b", "main")
	commit(main, "init")
	wt := filepath.Join(main, ".worktrees", "fix")
	gitRun(t, main, "worktree", "add", "-q", "-b", "work/plan/fix", wt)
	if got := MergedInto(main, main); got != "" {
		t.Fatalf("the main checkout itself: %q", got)
	}
	// A fresh worktree with a new script not committed yet is in use.
	os.WriteFile(filepath.Join(wt, "new.sh"), []byte("x"), 0o644)
	if got := MergedInto(main, wt); got != "" {
		t.Fatalf("uncommitted work: %q", got)
	}
	gitRun(t, wt, "add", "new.sh")
	commit(wt, "script")
	if got := MergedInto(main, wt); got != "" {
		t.Fatalf("a commit main lacks: %q", got)
	}
	gitRun(t, main, "merge", "-q", "--ff-only", "work/plan/fix")
	commit(main, "main moves on")
	if got := MergedInto(main, wt); got != "main" {
		t.Fatalf("merged and clean: %q, want main", got)
	}
	if got := MergedInto(t.TempDir(), wt); got != "" {
		t.Fatalf("not a repo: %q", got)
	}
}

func TestWorktreeHoldingFindsTheCheckoutADirIsIn(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	main := t.TempDir()
	gitRun(t, main, "init", "-q", "-b", "main")
	gitRun(t, main, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init")
	wt := filepath.Join(main, ".worktrees", "rock-sentinel")
	gitRun(t, main, "worktree", "add", "-q", "-b", "art/rock-sentinel", wt)
	sub := filepath.Join(wt, "scripts")
	os.MkdirAll(sub, 0o755)
	want, _ := filepath.EvalSymlinks(wt)
	for _, dir := range []string{wt, sub} {
		if got := WorktreeHolding(main, dir); got != want {
			t.Fatalf("WorktreeHolding(%s) = %q, want %q", dir, got, want)
		}
	}
	if got := WorktreeHolding(main, main); got != "" {
		t.Fatalf("the main checkout: %q", got)
	}
	if got := WorktreeHolding(main, t.TempDir()); got != "" {
		t.Fatalf("outside the repository: %q", got)
	}
}

func TestMachineRunsPassedReadsTheNewestRecord(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, RecordDir, "a")
	os.MkdirAll(dir, 0o755)
	if ok, ran := MachineRunPassed(root, "a"); ok || ran {
		t.Fatalf("no record: ok=%v ran=%v", ok, ran)
	}
	os.WriteFile(filepath.Join(dir, "20261030T100000Z-machine.txt"), []byte("script: a\nstep S1 pass boxes=\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "20261030T110000Z-machine.txt"), []byte("script: a\nstep S1 fail boxes= note=nope\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "20261030T120000Z-owner.txt"), []byte("script: a\nstep S1 pass boxes=\n"), 0o644)
	if ok, ran := MachineRunPassed(root, "a"); ok || !ran {
		t.Fatalf("newest machine run failed: ok=%v ran=%v", ok, ran)
	}
	os.WriteFile(filepath.Join(dir, "20261030T130000Z-machine.txt"), []byte("script: a\nstep S1 eyes boxes=\nstep S2 skip boxes=\n"), 0o644)
	if ok, ran := MachineRunPassed(root, "a"); !ok || !ran {
		t.Fatalf("newest machine run passed: ok=%v ran=%v", ok, ran)
	}
}

func TestFindBashPrefersAVersionFourOrNewer(t *testing.T) {
	dir := t.TempDir()
	fake := func(name, major string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte("#!/bin/sh\necho "+major+"\n"), 0o755)
		return p
	}
	old, newer := fake("old", "3"), fake("new", "5")
	got, err := findBash([]string{filepath.Join(dir, "missing"), old, newer})
	if err != nil || got != newer {
		t.Fatalf("findBash = %q, %v; want %q", got, err, newer)
	}
	_, err = findBash([]string{old})
	if err == nil || !strings.Contains(err.Error(), "brew install bash") || !strings.Contains(err.Error(), old+" is bash 3") {
		t.Fatalf("only bash 3: %v", err)
	}
}
