package postmortem

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/retro"
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

// WaitingIn lists the newest post-mortem of each issue under dir that has
// open actions. It reads only the files Save names (*-postmortem-*.md), so
// a page that counts what waits doesn't read every retro.
func WaitingIn(dir string) []Waiting {
	files, _ := filepath.Glob(filepath.Join(dir, "*-postmortem-*.md"))
	newest := map[string]Waiting{}
	var order []string
	for i := len(files) - 1; i >= 0; i-- { // names start with the UTC time: newest last
		raw, err := os.ReadFile(files[i])
		if err != nil {
			continue
		}
		meta, body := retro.Parse(string(raw))
		if meta["Kind"] != Kind || meta["Issue"] == "" {
			continue
		}
		if _, seen := newest[meta["Issue"]]; seen {
			continue
		}
		w := Waiting{Issue: meta["Issue"], Title: meta["Title"], File: files[i], Pending: len(Pending(Actions(body)))}
		if t, err := time.Parse(time.RFC3339, meta["Date"]); err == nil {
			w.Since = t
		}
		newest[w.Issue] = w
		order = append(order, w.Issue)
	}
	var out []Waiting
	for _, k := range order {
		if w := newest[k]; w.Pending > 0 {
			out = append(out, w)
		}
	}
	return out
}
