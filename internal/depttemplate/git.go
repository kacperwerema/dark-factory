package depttemplate

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/gitfile"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// DepartmentFiles are the files department d keeps in the project, relative
// to its root: the role prompt, the workflow file, each job's brief (not a
// builtin: one) and the departments/<key>/ folder (brand.md, icp.md, …).
// A path outside the root is left out.
func DepartmentFiles(proj *tracker.Project, d *tracker.Department) []string {
	root := proj.Root()
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p == "" || strings.HasPrefix(p, "builtin:") {
			return
		}
		if filepath.IsAbs(p) {
			rel, err := filepath.Rel(root, p)
			if err != nil || strings.HasPrefix(rel, "..") {
				return
			}
			p = rel
		}
		p = filepath.ToSlash(filepath.Clean(p))
		if strings.HasPrefix(p, "../") || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	add(d.Prompt)
	add(d.Workflow)
	for _, j := range d.Jobs {
		add(j.Brief)
	}
	dir := "departments/" + tracker.DepartmentKey(d.Name)
	if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir))); err == nil && info.IsDir() {
		add(dir)
	}
	return out
}

// InRepo reports whether root is inside a git work tree.
func InRepo(root string) bool {
	_, err := gitfile.Git(root, "rev-parse", "--is-inside-work-tree")
	return err == nil
}

// Untracked are the files under paths (files or folders, relative to root)
// that git does not track and does not ignore, sorted. Outside a git
// repository it is nil with no error: there is nothing to commit to.
func Untracked(root string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	if !InRepo(root) {
		return nil, nil
	}
	var exist []string
	for _, p := range paths {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err == nil {
			exist = append(exist, p)
		}
	}
	if len(exist) == 0 {
		return nil, nil
	}
	out, err := gitfile.Git(root, append([]string{"ls-files", "--others", "--exclude-standard", "--"}, exist...)...)
	if err != nil {
		return nil, err
	}
	var files []string
	seen := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" && !seen[l] {
			seen[l] = true
			files = append(files, l)
		}
	}
	sort.Strings(files)
	return files, nil
}

// CommitFiles adds files (relative to root) and commits only them, whatever
// else is staged, with subject. It returns the short sha.
func CommitFiles(root string, files []string, subject string) (string, error) {
	if len(files) == 0 {
		return "", fmt.Errorf("nothing to commit")
	}
	if _, err := gitfile.Git(root, append([]string{"add", "--"}, files...)...); err != nil {
		return "", err
	}
	if _, err := gitfile.Git(root, append([]string{"commit", "-q", "--only", "-m", subject, "--"}, files...)...); err != nil {
		// Unstage them again: left staged, the next unrelated commit would
		// take them, the leak this commit was to prevent.
		if _, rerr := gitfile.Git(root, append([]string{"reset", "-q", "--"}, files...)...); rerr != nil {
			return "", fmt.Errorf("%w (and unstaging them failed: %v; run git reset -- %s)", err, rerr, strings.Join(files, " "))
		}
		return "", err
	}
	return gitfile.Git(root, "rev-parse", "--short", "HEAD")
}

// CommitCommand is the git command that commits files, for an owner who
// didn't pass --commit.
func CommitCommand(root string, files []string, subject string) string {
	q := make([]string, len(files))
	for i, f := range files {
		q[i] = shellQuote(f)
	}
	return fmt.Sprintf("git -C %s add %s && git -C %s commit -m %s -- %s", shellQuote(root), strings.Join(q, " "), shellQuote(root), shellQuote(subject), strings.Join(q, " "))
}

// RemoveCommand is the rm that deletes files (relative to root), runnable
// from any folder.
func RemoveCommand(root string, files []string) string {
	q := make([]string, len(files))
	for i, f := range files {
		q[i] = shellQuote(filepath.ToSlash(filepath.Join(root, filepath.FromSlash(f))))
	}
	return "rm -- " + strings.Join(q, " ")
}

// ConfigUncommitted reports whether configPath sits in root's repository
// with changes git hasn't committed (department add's new entry).
func ConfigUncommitted(root, configPath string) bool {
	if configPath == "" || !InRepo(root) {
		return false
	}
	out, err := gitfile.Git(filepath.Dir(configPath), "status", "--porcelain", "--", filepath.Base(configPath))
	if err != nil || out == "" {
		return false
	}
	top, err1 := gitfile.Git(root, "rev-parse", "--show-toplevel")
	ctop, err2 := gitfile.Git(filepath.Dir(configPath), "rev-parse", "--show-toplevel")
	return err1 == nil && err2 == nil && gitfile.RealPath(top) == gitfile.RealPath(ctop)
}

func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r == '/' || r == '.' || r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
