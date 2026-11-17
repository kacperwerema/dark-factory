package gitmain

import "strings"

// Work is where an issue's work branch stands against the main branch.
type Work int

const (
	// WorkNone: no head to judge (no work branch).
	WorkNone Work = iota
	// WorkEmpty: the head is the dispatch base: the branch has no commits
	// of its own, so main containing it says nothing about the issue.
	WorkEmpty
	// WorkAhead: the main branch doesn't contain the head yet.
	WorkAhead
	// WorkOnMain: the branch has commits past its base and main contains
	// its head.
	WorkOnMain
)

// WorkAt is the one rule for "is this issue's work on main", shared by the
// queue's --merged blockers, its done marking, the worktree prune and the
// lead tick (TestOneBranchOnMainPredicate): head is the branch's head (or
// a head recorded earlier), base its dispatch base ("" when unknown: only
// containment counts) and contains whether the main branch holds a commit
// (Contains, or a set the caller read in one git call). contains is asked
// only once the head is past its base.
func WorkAt(head, base string, contains func(sha string) bool) Work {
	head = strings.TrimSpace(head)
	if head == "" {
		return WorkNone
	}
	if SameCommit(head, base) {
		return WorkEmpty
	}
	if !contains(head) {
		return WorkAhead
	}
	return WorkOnMain
}

// OnMain is WorkAt(...) == WorkOnMain.
func OnMain(head, base string, contains func(sha string) bool) bool {
	return WorkAt(head, base, contains) == WorkOnMain
}

// SameCommit reports whether two SHAs name one commit: a short SHA (stats
// may hold one) matches the full one it starts, case aside. "" matches
// nothing.
func SameCommit(a, b string) bool {
	a, b = strings.ToLower(strings.TrimSpace(a)), strings.ToLower(strings.TrimSpace(b))
	if a == "" || b == "" {
		return false
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	return len(a) >= 7 && strings.HasPrefix(b, a) || a == b
}

// Contains is a contains for WorkAt: whether refs/heads/<branch> in the
// repository at root holds sha (git merge-base --is-ancestor).
func Contains(root, branch string) func(sha string) bool {
	return func(sha string) bool {
		_, err := Git(root, "merge-base", "--is-ancestor", sha, "refs/heads/"+branch)
		return err == nil
	}
}
