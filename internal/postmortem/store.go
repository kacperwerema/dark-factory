package postmortem

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/retro"
)

// Meta is a post-mortem's header.
type Meta struct {
	Issue, Title, Status, System string
}

// Of are the post-mortems among entries for slug ("" for all), newest
// first (retro.Load's order).
func Of(entries []*retro.Entry, slug string) []*retro.Entry {
	var out []*retro.Entry
	for _, e := range entries {
		if e.Kind == Kind && (slug == "" || e.IssueSlug == slug) {
			out = append(out, e)
		}
	}
	return out
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Save writes body as a post-mortem retro under dir and returns its path.
// The caller has already passed body through Check.
func Save(dir string, m Meta, body string, now time.Time) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	report := fmt.Sprintf("# Post-mortem\n\nIssue: %s\nTitle: %s\nStatus: %s\nSystem: %s\nDate: %s\nKind: %s\n\n%s\n",
		m.Issue, m.Title, m.Status, m.System, now.Format(time.RFC3339), Kind, strings.TrimSpace(body))
	name := fmt.Sprintf("%s-postmortem-%s.md", now.UTC().Format("20060102-150405"), strings.Trim(unsafeName.ReplaceAllString(m.Issue, "-"), "-"))
	path := filepath.Join(dir, name)
	if _, err := os.Lstat(path); err == nil {
		return "", fmt.Errorf("%s exists already", path)
	}
	if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// Waiting is a post-mortem with actions the owner hasn't answered.
type Waiting struct {
	Issue, Title, File string
	Pending            int
	Since              time.Time
}

// Waits are the post-mortems among entries the owner still has to answer,
// each path with its open actions: WaitingIn's rule, the newest of each
// issue and only with actions open. An older one an issue has moved past
// doesn't wait.
func Waits(entries []*retro.Entry) map[string]int {
	newest := map[string]*retro.Entry{}
	for _, e := range entries {
		if e.Kind != Kind || e.IssueSlug == "" {
			continue
		}
		if have := newest[e.IssueSlug]; have == nil || e.FileName > have.FileName {
			newest[e.IssueSlug] = e // names start with the UTC time
		}
	}
	out := map[string]int{}
	for _, e := range newest {
		if n := len(Pending(Actions(e.Body))); n > 0 {
			out[e.FilePath] = n
		}
	}
	return out
}

// WaitingIn lists the newest post-mortem of each issue under dir, archive/
// included, that has open actions. It reads only the files Save names
// (*-postmortem-*.md), so a page that counts what waits doesn't read every
// retro.
func WaitingIn(dir string) []Waiting {
	var out []Waiting
	for _, f := range newestFiles(dir) {
		if n := len(Pending(Actions(f.body))); n > 0 {
			out = append(out, f.waiting(n))
		}
	}
	return out
}

// Queue is every open action WaitingIn counts, newest post-mortem first.
func Queue(dir string) []Item {
	var out []Item
	for _, f := range newestFiles(dir) {
		w := f.waiting(0)
		for _, a := range Pending(Actions(f.body)) {
			out = append(out, Item{Action: a, Issue: w.Issue, IssueTitle: w.Title, File: filepath.Base(f.path), Path: f.path, Since: w.Since})
		}
	}
	return out
}

// retroFile is one post-mortem file read from disk.
type retroFile struct {
	path, body string
	meta       map[string]string
}

func (f retroFile) waiting(pending int) Waiting {
	w := Waiting{Issue: f.meta["Issue"], Title: f.meta["Title"], File: f.path, Pending: pending}
	if t, err := time.Parse(time.RFC3339, f.meta["Date"]); err == nil {
		w.Since = t
	}
	return w
}

// newestFiles is each issue's newest post-mortem in dir and its archive
// folder, newest first. Names decide, wherever the file sits: they start
// with the UTC time, and a move doesn't change them.
func newestFiles(dir string) []retroFile {
	files, _ := filepath.Glob(filepath.Join(dir, "*-postmortem-*.md"))
	archived, _ := filepath.Glob(filepath.Join(dir, retro.ArchiveDir, "*-postmortem-*.md"))
	files = append(files, archived...)
	sort.SliceStable(files, func(i, j int) bool { return filepath.Base(files[i]) > filepath.Base(files[j]) })
	seen := map[string]bool{}
	var out []retroFile
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		meta, body := retro.Parse(string(raw))
		if meta["Kind"] != Kind || meta["Issue"] == "" || seen[meta["Issue"]] {
			continue
		}
		seen[meta["Issue"]] = true
		out = append(out, retroFile{path: path, body: body, meta: meta})
	}
	return out
}
