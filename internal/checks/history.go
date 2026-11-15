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

	"github.com/kacperwerema/dark-factory/internal/gitdir"
)

// Removed is a script for an issue that a commit took off the branch: the
// shipping step drops scripts/checks/ before the merge, while the owner's run
// may still be due.
type Removed struct {
	Name    string // file name without .sh
	Path    string // scripts/checks/<name>.sh, as it was in the repository
	Commit  string // the commit that removed it
	Content []byte // the script as it was just before
	Files   []File // its files beside it the same commit removed (Beside)
}

// File is one file a script keeps beside it, as it was before its removal.
type File struct {
	Rel     string // path under scripts/checks/ ("<name>-probe.sh", "<name>/fixture.txt")
	Content []byte
	Exec    bool // git had it executable
}

// Beside reports whether rel, a path under scripts/checks/, belongs to the
// script name: <name>-*, <name>.* (not the script itself) or anything under
// a <name>/ folder.
func Beside(name, rel string) bool {
	first, _, _ := strings.Cut(rel, "/")
	if first == name+".sh" {
		return false
	}
	return first == name || strings.HasPrefix(first, name+"-") || strings.HasPrefix(first, name+".")
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
	type removal struct {
		commit string
		paths  []string
	}
	var removals []removal
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if c, ok := strings.CutPrefix(line, "commit "); ok {
			removals = append(removals, removal{commit: c})
			continue
		}
		if line != "" && len(removals) > 0 {
			removals[len(removals)-1].paths = append(removals[len(removals)-1].paths, line)
		}
	}
	seen := map[string]bool{}
	var found []Removed
	for _, rm := range removals {
		for _, line := range rm.paths {
			if path.Dir(line) != Dir || path.Ext(line) != ".sh" {
				continue
			}
			name := strings.TrimSuffix(path.Base(line), ".sh")
			if seen[name] {
				continue
			}
			content, err := exec.Command("git", "-C", root, "show", rm.commit+"^:"+line).Output()
			if err != nil || issueOfContent(content) != slug {
				continue
			}
			seen[name] = true
			found = append(found, Removed{Name: name, Path: line, Commit: rm.commit, Content: content, Files: filesBeside(root, rm.commit, name, rm.paths)})
		}
	}
	return found
}

// filesBeside reads the files of script name among paths, which commit
// removed, as they were in its parent.
func filesBeside(root, commit, name string, paths []string) []File {
	var files []File
	for _, p := range paths {
		rel, ok := strings.CutPrefix(p, Dir+"/")
		if !ok || !Beside(name, rel) {
			continue
		}
		content, err := exec.Command("git", "-C", root, "show", commit+"^:"+p).Output()
		if err != nil {
			continue
		}
		// "100755 blob <sha>\t<path>"; --format needs git 2.36.
		tree, _ := exec.Command("git", "-C", root, "ls-tree", commit+"^", "--", p).Output()
		files = append(files, File{Rel: rel, Content: content, Exec: strings.HasPrefix(string(tree), "100755 ")})
	}
	return files
}

// Short is the removing commit's abbreviated hash.
func (r Removed) Short() string {
	return gitdir.ShortSHA(r.Commit)
}

// WriteRemoved writes the scripts, and the files each keeps beside it, into a
// new temporary folder, as Scripts that run from root ($CHECKS_DIR is that
// folder), and returns them with a cleanup that removes the folder.
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
		for _, f := range r.Files {
			fp := filepath.Join(dir, filepath.FromSlash(f.Rel))
			mode := os.FileMode(0o644)
			if f.Exec {
				mode = 0o755
			}
			if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
				cleanup()
				return nil, func() {}, fmt.Errorf("writing %s/%s from history: %w", Dir, f.Rel, err)
			}
			if err := os.WriteFile(fp, f.Content, mode); err != nil {
				cleanup()
				return nil, func() {}, fmt.Errorf("writing %s/%s from history: %w", Dir, f.Rel, err)
			}
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
