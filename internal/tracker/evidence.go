package tracker

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// An issue's stills (screenshots a worker takes outside a playtest run) live
// in <issueDir>/_evidence/<slug>/, in the primary checkout's issues folder:
// issue-cli evidence add writes there from any worktree, and the viewer's
// Evidence tab lists the folder. Loading skips _ folders (NotIssueFolder).

// EvidenceDir is the folder that holds an issue's stills; a slug's system
// folder nests (cli/foo is _evidence/cli/foo).
func EvidenceDir(issueDir, slug string) string {
	return filepath.Join(issueDir, "_evidence", filepath.FromSlash(slug))
}

// EvidenceStepPattern is a step id a still can be filed under (TP3, AC2,
// S1): letters, then digits. A saved name starts with it and a "-".
var EvidenceStepPattern = regexp.MustCompile(`^[A-Z]+[0-9]+$`)

var evidenceStepPrefix = regexp.MustCompile(`^([A-Z]+[0-9]+)-`)

// EvidenceFile is one still in an issue's evidence folder.
type EvidenceFile struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Step    string    `json:"step,omitempty"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modified"`
}

// ListEvidence returns the files in an issue's evidence folder, by name.
// Dot and underscore files (a save's temp file) and subfolders are left out;
// a missing folder is no files and no error.
func ListEvidence(issueDir, slug string) ([]EvidenceFile, error) {
	dir := EvidenceDir(issueDir, slug)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []EvidenceFile
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || NotIssueFolder(name) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		f := EvidenceFile{Name: name, Path: filepath.Join(dir, name), Size: info.Size(), ModTime: info.ModTime()}
		if m := evidenceStepPrefix.FindStringSubmatch(name); m != nil {
			f.Step = m[1]
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// EvidenceName is the name a file is saved under. An image keeps its base
// name with the extension of its sniffed type (never the one it came with,
// as for attachments); any other file keeps its own clean name
// (CleanFileName) and extension, and is refused when that extension says
// image (LooksLikeImageName), since the viewer would serve it as one. The
// step id goes in front when step is set and the name doesn't start with it
// already. A name that would be hidden (starting with "." or "_") is
// refused.
func EvidenceName(file, step string, data []byte) (string, error) {
	base := filepath.Base(filepath.Clean(file))
	ext, image := AttachmentExt(data)
	if !image {
		if LooksLikeImageName(base) {
			return "", fmt.Errorf("named like an image but the bytes are no PNG, JPEG, GIF or WebP; rename it (a .txt, .pprof) or save the real image")
		}
		ext = filepath.Ext(base)
	}
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem == "" || stem == "." || stem == ".." || NotIssueFolder(stem) {
		return "", fmt.Errorf("a still's name can't be empty or start with \".\" or \"_\"")
	}
	if !image {
		clean := CleanFileName(stem + ext)
		if clean == "" {
			return "", fmt.Errorf("a file name needs a letter or digit")
		}
		ext = filepath.Ext(clean)
		stem = strings.TrimSuffix(clean, ext)
	}
	if step != "" && !strings.HasPrefix(stem, step+"-") {
		stem = step + "-" + stem
	}
	return stem + ext, nil
}

// SaveEvidence writes data as name in the issue's evidence folder through a
// temp file and a rename, so a reader never sees half a file. An existing
// name is refused unless force.
func SaveEvidence(issueDir, slug, name string, data []byte, force bool) (string, error) {
	if name != filepath.Base(name) || NotIssueFolder(name) {
		return "", fmt.Errorf("bad still name %q", name)
	}
	dir := EvidenceDir(issueDir, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, name)
	if st, err := os.Lstat(dest); err == nil {
		if !force {
			return "", &EvidenceExistsError{Name: name, Dir: dir}
		}
		if !st.Mode().IsRegular() {
			return "", fmt.Errorf("%s is not a regular file", dest)
		}
	}
	tmp, err := os.CreateTemp(dir, ".save-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return "", err
	}
	return dest, nil
}

// EvidenceExistsError is SaveEvidence refusing a name already there.
type EvidenceExistsError struct{ Name, Dir string }

func (e *EvidenceExistsError) Error() string {
	return fmt.Sprintf("%s is already in %s", e.Name, e.Dir)
}
