package experiment

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Trailer is the commit trailer an adopted experiment's commits carry.
const Trailer = "Experiment"

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// SHA is the commit ref names in repo.
func SHA(repo, ref string) (string, error) {
	return git(repo, "rev-parse", "--verify", ref+"^{commit}")
}

// Contains reports whether ref has commit sha in its history.
func Contains(repo, ref, sha string) bool {
	_, err := git(repo, "merge-base", "--is-ancestor", sha, ref)
	return err == nil
}

// Changed lists the files ref changed since it left base (base...ref).
func Changed(repo, base, ref string) ([]string, error) {
	out, err := git(repo, "diff", "--name-only", base+"..."+ref)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// TopLevel is the root of the repository dir is in.
func TopLevel(dir string) (string, error) { return git(dir, "rev-parse", "--show-toplevel") }

// AdoptedCommits lists the commits on ref whose message has the trailer
// "Experiment: <slug>", newest first.
func AdoptedCommits(repo, ref, slug string) ([]string, error) {
	out, err := git(repo, "log", ref, "--format=%H%x00%B%x01")
	if err != nil {
		return nil, err
	}
	re := regexp.MustCompile(`(?mi)^` + Trailer + `:\s*` + regexp.QuoteMeta(slug) + `\s*$`)
	var shas []string
	for _, rec := range strings.Split(out, "\x01") {
		sha, msg, ok := strings.Cut(strings.TrimSpace(rec), "\x00")
		if ok && re.MatchString(msg) {
			shas = append(shas, sha)
		}
	}
	return shas, nil
}

// RevertBranch is the branch rollback prepares for slug.
func RevertBranch(slug string) string { return "revert/" + slug }

// PrepareRevert creates branch revert/<slug> from ref with one commit that
// reverts shas (newest first), made in a throwaway worktree so neither ref
// nor the caller's checkout changes. A merge commit is reverted against its
// first parent. The commit is conventional (the repo's commit hook) and
// names the experiment.
func PrepareRevert(repo, ref, slug string, shas []string) (string, error) {
	if len(shas) == 0 {
		return "", fmt.Errorf("no commit on %s carries %s: %s", ref, Trailer, slug)
	}
	branch := RevertBranch(slug)
	if _, err := git(repo, "rev-parse", "--verify", "refs/heads/"+branch); err == nil {
		return "", fmt.Errorf("branch %s already exists: review or delete it first", branch)
	}
	tmp, err := os.MkdirTemp("", "experiment-revert-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	wt := filepath.Join(tmp, "wt")
	if _, err := git(repo, "worktree", "add", "-q", "-b", branch, wt, ref); err != nil {
		return "", err
	}
	fail := func(err error) (string, error) {
		git(repo, "worktree", "remove", "--force", wt) //nolint:errcheck
		git(repo, "branch", "-D", branch)              //nolint:errcheck
		return "", err
	}
	var listed []string
	for _, sha := range shas {
		args := []string{"revert", "--no-commit"}
		if parents, _ := git(wt, "rev-list", "--parents", "-n", "1", sha); len(strings.Fields(parents)) > 2 {
			args = append(args, "-m", "1")
		}
		if _, err := git(wt, append(args, sha)...); err != nil {
			git(wt, "revert", "--abort") //nolint:errcheck
			return fail(fmt.Errorf("revert of %s does not apply cleanly on %s: %w", short(sha), ref, err))
		}
		listed = append(listed, short(sha))
	}
	msg := fmt.Sprintf("revert(experiment): roll back %s\n\nReverts %s.\n\nCatalog: n/a experiment rollback", slug, strings.Join(listed, ", "))
	if _, err := git(wt, "commit", "-q", "-m", msg); err != nil {
		return fail(err)
	}
	git(repo, "worktree", "remove", "--force", wt) //nolint:errcheck // the branch is made either way
	return branch, nil
}

func short(sha string) string {
	if len(sha) > 9 {
		return sha[:9]
	}
	return sha
}
