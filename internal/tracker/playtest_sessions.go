package tracker

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// PlaytestSessionStep is one step of a playtest session file.
type PlaytestSessionStep struct {
	ID    string `yaml:"id"`
	Title string `yaml:"title"`
}

// PlaytestSessionFile is the part of a playtest/sessions/<name>.yaml file
// the tracker reads: its name, issue and step ids.
type PlaytestSessionFile struct {
	Session string                `yaml:"session"`
	Issue   string                `yaml:"issue"`
	Steps   []PlaytestSessionStep `yaml:"steps"`
	// Path is the file the session was read from.
	Path string `yaml:"-"`
}

// IssuePlaytestSessions reads the playtest sessions whose issue: is one of
// slugs, from the issue's worktree (root/.worktrees/<slug>, where a session
// written in design lives on the issue branch) and then the checkout. A
// session found in the worktree hides the checkout's copy. Files that don't
// parse are skipped. Sorted by session name.
//
// A file whose text doesn't hold any of the slugs can't name one in its
// issue:, so it is skipped unparsed: each worktree holds a copy of every
// session, and parsing them all to keep a few made each prove call slow.
func IssuePlaytestSessions(root string, slugs ...string) []PlaytestSessionFile {
	names := func(data []byte) bool {
		for _, s := range slugs {
			if s = strings.TrimSpace(s); s != "" && bytes.Contains(data, []byte(s)) {
				return true
			}
		}
		return false
	}
	return IssuePlaytestSessionsIn(func(dir string) []PlaytestSessionFile { return playtestSessionFilesIf(dir, names) }, root, slugs...)
}

// IssuePlaytestSessionsIn is IssuePlaytestSessions reading each folder
// through read, so a pass over many issues can read the folders once.
func IssuePlaytestSessionsIn(read func(dir string) []PlaytestSessionFile, root string, slugs ...string) []PlaytestSessionFile {
	want := map[string]bool{}
	var dirs []string
	for _, s := range slugs {
		if s = strings.TrimSpace(s); s != "" && !want[s] {
			want[s] = true
			dirs = append(dirs, filepath.Join(root, ".worktrees", s))
		}
	}
	if root == "" || len(want) == 0 {
		return nil
	}
	dirs = append(dirs, root)
	seen := map[string]bool{}
	var out []PlaytestSessionFile
	for _, d := range dirs {
		for _, s := range read(d) {
			if !want[strings.TrimSpace(s.Issue)] || seen[s.Session] {
				continue
			}
			seen[s.Session] = true
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Session < out[j].Session })
	return out
}

// PlaytestSessionFiles reads every session under dir's playtest/sessions, in
// file order. Files that don't parse are skipped. A file unchanged since it
// was last read isn't read again: every worktree holds a copy of them all,
// and a page over several issues goes through several worktrees.
func PlaytestSessionFiles(dir string) []PlaytestSessionFile {
	return playtestSessionFilesIf(dir, nil)
}

// playtestSessionFilesIf is PlaytestSessionFiles leaving out, unparsed, each
// file not yet parsed whose text keep refuses (nil keeps all).
func playtestSessionFilesIf(dir string, keep func(data []byte) bool) []PlaytestSessionFile {
	files, _ := filepath.Glob(filepath.Join(dir, "playtest", "sessions", "*.yaml"))
	sort.Strings(files)
	var out []PlaytestSessionFile
	for _, f := range files {
		if s, ok := readPlaytestSession(f, keep); ok {
			out = append(out, s)
		}
	}
	return out
}

type parsedSession struct {
	size  int64
	mtime time.Time
	file  PlaytestSessionFile
	ok    bool
}

const sessionCacheMax = 20000

var sessionCache = struct {
	sync.Mutex
	m map[string]parsedSession
}{m: map[string]parsedSession{}}

// readPlaytestSession parses the session at path, once per size and mtime.
// A file it has to read whose text keep refuses comes back not ok, and is
// not remembered: another caller may want it.
func readPlaytestSession(path string, keep func(data []byte) bool) (PlaytestSessionFile, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return PlaytestSessionFile{}, false
	}
	sessionCache.Lock()
	c, hit := sessionCache.m[path]
	sessionCache.Unlock()
	if hit && c.size == fi.Size() && c.mtime.Equal(fi.ModTime()) {
		return c.file, c.ok
	}
	data, err := os.ReadFile(path)
	if err != nil || keep != nil && !keep(data) {
		return PlaytestSessionFile{}, false
	}
	var s PlaytestSessionFile
	ok := yaml.Unmarshal(data, &s) == nil
	if ok {
		if s.Session == "" {
			s.Session = strings.TrimSuffix(filepath.Base(path), ".yaml")
		}
		s.Path = path
	}
	sessionCache.Lock()
	if len(sessionCache.m) >= sessionCacheMax {
		sessionCache.m = map[string]parsedSession{}
	}
	sessionCache.m[path] = parsedSession{size: fi.Size(), mtime: fi.ModTime(), file: s, ok: ok}
	sessionCache.Unlock()
	return s, ok
}

// sessionStepBoxes is the lead a new "## <heading>" section gets when a
// playtest session for the issue already uses step ids in that section's
// prefix (a session written in design, before in progress appends the Test
// Plan: step id = box id). It has one box per such step, numbered as the
// session numbers them, with a removed-box slot for each gap, so the
// section's own boxes number after the session's. "" when the section
// already exists (ids are never renumbered) or no session uses the prefix.
func (w *WorkflowConfig) sessionStepBoxes(issue *Issue, heading string) string {
	if w == nil || w.RepoRoot == "" || issue == nil || issue.Slug == "" {
		return ""
	}
	level, title, ok := parseHeadingLine(normalizeHeading(heading))
	if !ok || level != 2 || len(findSectionHeadings(issue.BodyRaw, title, level)) > 0 {
		return ""
	}
	// The id prefix the new section would get, given the sections before it.
	probe := ListCheckboxes(strings.TrimRight(issue.BodyRaw, "\n") + "\n\n## " + title + "\n- [ ] probe\n")
	if len(probe) == 0 {
		return ""
	}
	prefix := strings.TrimRight(probe[len(probe)-1].ID, "0123456789")
	if prefix == "" {
		return ""
	}
	idRe := regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(prefix) + `([1-9][0-9]*)$`)

	type stepBox struct {
		title    string
		sessions []string
	}
	steps := map[int]*stepBox{}
	maxN := 0
	slug := folderSlug(w.IssuesRoot, issue.FilePath, issue.Slug)
	for _, s := range IssuePlaytestSessions(w.RepoRoot, slug, issue.Slug) {
		for _, st := range s.Steps {
			m := idRe.FindStringSubmatch(strings.TrimSpace(st.ID))
			if m == nil {
				continue
			}
			n, _ := strconv.Atoi(m[1])
			if n > 200 { // a typo, not a numbering to fill
				continue
			}
			b := steps[n]
			if b == nil {
				b = &stepBox{title: strings.TrimSpace(st.Title)}
				steps[n] = b
			}
			if !containsStr(b.sessions, s.Session) {
				b.sessions = append(b.sessions, s.Session)
			}
			if n > maxN {
				maxN = n
			}
		}
	}
	if maxN == 0 {
		return ""
	}
	var lines []string
	for n := 1; n <= maxN; n++ {
		id := fmt.Sprintf("%s%d", prefix, n)
		b := steps[n]
		if b == nil {
			lines = append(lines, removedBoxMarker+id+": no playtest step with this id -->")
			continue
		}
		text := b.title
		if text == "" {
			text = "playtest step"
		}
		lines = append(lines, fmt.Sprintf("- [ ] %s: %s (playtest %s)", id, text, strings.Join(b.sessions, ", ")))
	}
	return "Steps of the playtest session written before this section (their ids stay):\n" + strings.Join(lines, "\n")
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
