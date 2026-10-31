package tracker

import "strings"

// RefExists reports whether name resolves to a commit in the repository at
// root.
func RefExists(root, name string) bool {
	return refExists(root, name)
}

// CheckoutState is the main checkout of a repository and what it has
// checked out.
type CheckoutState struct {
	Path   string // the first `git worktree list` entry
	Branch string // "" when HEAD is detached
	SHA    string // HEAD's commit
}

// MainCheckoutState reads the main checkout of the repository dir is in.
// ok is false when dir is not in a git repository.
func MainCheckoutState(dir string) (st CheckoutState, ok bool) {
	out, err := runGit(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return st, false
	}
	for _, l := range strings.Split(out, "\n") {
		if p, found := strings.CutPrefix(l, "worktree "); found {
			st.Path = p
			break
		}
	}
	if st.Path == "" {
		return st, false
	}
	if sha, err := runGit(st.Path, "rev-parse", "--verify", "--quiet", "HEAD"); err == nil {
		st.SHA = strings.TrimSpace(sha)
	}
	if b, err := runGit(st.Path, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		st.Branch = strings.TrimSpace(b)
	}
	return st, true
}

// Label names what the checkout has checked out: the branch, or
// "detached at <sha>".
func (st CheckoutState) Label() string {
	if st.Branch != "" {
		return st.Branch
	}
	return "detached at " + ShortSHA(st.SHA)
}

// ShortSHA is sha cut to 7 characters.
func ShortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
