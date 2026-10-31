package checks

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// Removed is a script for an issue that a commit took off the branch: the
// shipping step drops scripts/checks/ before the merge, while the owner's run
// may still be due.
type Removed struct {
	Name    string // file name without .sh
	Path    string // scripts/checks/<name>.sh, as it was in the repository
	Commit  string // the commit that removed it
	Content []byte // the script as it was just before
}

// FromHistory finds slug's scripts that commits reachable from HEAD in the
// repository at root (and from work/<slug>, when that branch exists) removed,
// the newest removal winning for a name. Nil when git has none, or no git.
func FromHistory(root, slug string) []Removed {
	if root == "" || slug == "" {
		return nil
	}
	refs := []string{"HEAD"}
	if exec.Command("git", "-C", root, "rev-parse", "--verify", "--quiet", "refs/heads/work/"+slug).Run() == nil {
		refs = append(refs, "refs/heads/work/"+slug)
	}
	// --full-history: main never had the file, so a merge is the same as its
	// first parent for this path, and plain git log would skip the branch.
	args := append([]string{"-C", root, "log", "--full-history", "--diff-filter=D", "--name-only", "--format=commit %H"}, refs...)
	out, err := exec.Command("git", append(args, "--", Dir+"/")...).Output()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var found []Removed
	commit := ""
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if c, ok := strings.CutPrefix(line, "commit "); ok {
			commit = c
			continue
		}
		if commit == "" || path.Dir(line) != Dir || path.Ext(line) != ".sh" {
			continue
		}
		name := strings.TrimSuffix(path.Base(line), ".sh")
		if seen[name] {
			continue
		}
		content, err := exec.Command("git", "-C", root, "show", commit+"^:"+line).Output()
		if err != nil || issueOfContent(content) != slug {
			continue
		}
		seen[name] = true
		found = append(found, Removed{Name: name, Path: line, Commit: commit, Content: content})
	}
	return found
}

// Short is the removing commit's abbreviated hash.
func (r Removed) Short() string {
	if len(r.Commit) > 7 {
		return r.Commit[:7]
	}
	return r.Commit
}

// WriteRemoved writes the scripts into a new temporary folder, as Scripts
// that run from root, and returns them with a cleanup that removes the folder.
func WriteRemoved(root string, removed []Removed) ([]Script, func(), error) {
	dir, err := os.MkdirTemp("", "check-script-")
	if err != nil {
		return nil, func() {}, fmt.Errorf("writing scripts from history: %w", err)
	}
	cleanup := func() { os.RemoveAll(dir) }
	var out []Script
	for _, r := range removed {
		p := filepath.Join(dir, r.Name+".sh")
		if err := os.WriteFile(p, r.Content, 0o755); err != nil {
			cleanup()
			return nil, func() {}, fmt.Errorf("writing %s from history: %w", r.Path, err)
		}
		out = append(out, Script{Name: r.Name, Path: p, Root: root, From: r.Commit})
	}
	return out, cleanup, nil
}

// issueOfContent is IssueOf on a script's content.
func issueOfContent(data []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(data))
	for i := 0; i < headerLines && sc.Scan(); i++ {
		if m := issueHeader.FindStringSubmatch(strings.TrimSpace(sc.Text())); m != nil {
			return headerSlug(m[1])
		}
	}
	return ""
}
