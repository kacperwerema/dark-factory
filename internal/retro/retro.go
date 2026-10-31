// Package retro reads the retrospectives agents leave in retros/ (issue-cli
// retrospective): the header lines, the four labelled sections and the items
// inside them. The viewer's Retro sorting room and `issue-cli experiment
// candidates` both read retros through it.
package retro

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Entry is one retro file.
type Entry struct {
	FileName     string
	FilePath     string
	IssueSlug    string
	IssueTitle   string
	Status       string
	System       string
	Date         string
	Body         string
	ModTime      time.Time
	ReviewStatus string
	// Kind is "postmortem" for a post-mortem (internal/postmortem), else "".
	Kind string
}

// Time is when the retro was written: its Date line, else the file's time.
func (e *Entry) Time() time.Time {
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(e.Date)); err == nil {
		return t.Local()
	}
	return e.ModTime
}

// Load reads every .md under dir (subfolders included), newest first. A
// missing dir is no retros, not an error.
func Load(dir string) ([]*Entry, error) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil, nil
	}
	var out []*Entry
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		meta, body := Parse(string(data))
		if strings.TrimSpace(body) == "" {
			body = strings.TrimSpace(string(data))
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, &Entry{
			FileName: d.Name(), FilePath: path,
			IssueSlug: meta["Issue"], IssueTitle: meta["Title"], Status: meta["Status"],
			System: meta["System"], Date: meta["Date"], ReviewStatus: meta["ReviewStatus"], Kind: meta["Kind"],
			Body: body, ModTime: fi.ModTime(),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking retros directory %s: %w", dir, err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime.After(out[j].ModTime) })
	return out, nil
}

// Parse splits a retro into its header lines (Issue:, Title:, Status:,
// System:, Date:, ReviewStatus:, Kind:, after an optional "# " title) and the body.
// A file without those lines is all body.
func Parse(raw string) (map[string]string, string) {
	content := strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	meta := map[string]string{}

	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	if start < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[start]), "#") {
		start++
	}
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}

	idx := start
	for idx < len(lines) {
		line := strings.TrimSpace(lines[idx])
		if line == "" {
			idx++
			break
		}
		key, val, ok := strings.Cut(line, ":")
		switch key {
		case "Issue", "Title", "Status", "System", "Date", "ReviewStatus", "Kind":
		default:
			ok = false
		}
		if !ok {
			return meta, strings.TrimSpace(content)
		}
		meta[key] = strings.TrimSpace(val)
		idx++
	}

	if len(meta) == 0 {
		return meta, strings.TrimSpace(content)
	}
	return meta, strings.TrimSpace(strings.Join(lines[idx:], "\n"))
}

// Section labels, lower case, as Sections returns them.
const (
	LabelBase      = "base workflow"
	LabelSubsystem = "subsystem workflow" // a prefix: "subsystem workflow for <system>"
	LabelMissing   = "missing system-specific instructions"
)

// labelRe is one of the retrospective's labelled lines, as a "Label: text"
// line or a "## Label" heading.
var labelRe = regexp.MustCompile(`(?i)^(?:#{1,4}\s*)?(?:[-*]\s+)?(?:\*\*)?(base workflow|subsystem workflow(?: for ([^:*]+?))?|missing system-specific instructions|tooling/workflow friction|tooling friction)(?:\*\*)?\s*(?::\s*(.*))?$`)

// itemRe finds "(1) " item markers inside a finding.
var itemRe = regexp.MustCompile(`\(\d{1,2}\)\s`)

// Section is one labelled section of a retro.
type Section struct {
	Label, System, Text string
}

// Workflow reports a Base workflow or Subsystem workflow section: the
// findings the sorting room puts in its Workflow bin.
func (s Section) Workflow() bool {
	return s.Label == LabelBase || strings.HasPrefix(s.Label, LabelSubsystem)
}

// Sections splits a retro's body into its labelled sections.
func Sections(body string) []Section {
	var out []Section
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		if m := labelRe.FindStringSubmatch(t); m != nil {
			out = append(out, Section{Label: strings.ToLower(m[1]), System: strings.TrimSpace(m[2]), Text: strings.TrimSpace(m[3])})
			continue
		}
		if len(out) == 0 || t == "" {
			continue
		}
		cur := &out[len(out)-1]
		t = strings.TrimSpace(strings.TrimLeft(t, "-*"))
		if cur.Text == "" {
			cur.Text = t
		} else {
			cur.Text += " " + t
		}
	}
	return out
}

// None reports a section that says there is nothing ("none", "brak").
func None(text string) bool {
	switch strings.ToLower(strings.Trim(strings.TrimSpace(text), ".!;—- ")) {
	case "", "none", "brak", "nothing", "n/a", "na", "no", "nic", "nothing new", "none so far":
		return true
	}
	return false
}

// Items splits "(1) a (2) b" into its items; text without markers is one
// item.
func Items(text string) []string {
	locs := itemRe.FindAllStringIndex(text, -1)
	if len(locs) < 2 {
		return []string{text}
	}
	var out []string
	if pre := strings.Trim(strings.TrimSpace(text[:locs[0][0]]), ":;,"); pre != "" {
		out = append(out, pre)
	}
	for i, l := range locs {
		end := len(text)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		if s := strings.Trim(strings.TrimSpace(text[l[1]:end]), ";,"); s != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

// TermRe finds code-like terms (check-script, scripts/checks, report-bug,
// work/<slug>) a pattern can be made of.
var TermRe = regexp.MustCompile(`[A-Za-z][A-Za-z0-9_]*(?:[-/.][A-Za-z0-9_<>]+)+`)

// prefixRe finds the workflow's own comment and trailer prefixes (tests:,
// question:, Proves:, Catalog:): a word and a colon before a space.
var prefixRe = regexp.MustCompile(`\b[A-Za-z][a-z]{2,}:(?:\s|$)`)

// capsRe finds all-caps names (CHANGELOG, UNPROVEN).
var capsRe = regexp.MustCompile(`\b[A-Z]{6,}\b`)

// labelWords are prose labels a retro writes before a colon ("Friction: …"),
// never a prefix of the workflow's own.
var labelWords = map[string]bool{"friction:": true, "example:": true, "result:": true, "update:": true, "because:": true}

// Terms are the pattern terms of text, once each (case folded), in text
// order: TermRe matches of 6+ characters, skipping stop words and dotted
// names without a - or / (file.go, v0.1); a prefix such as tests: or
// Proves: inside a sentence (not a label at its start, "Friction: …"); and
// an all-caps name of 6+ letters that isn't a file name (CHANGELOG, not
// CLAUDE.md).
func Terms(text string, stop map[string]bool) []string {
	type match struct {
		at   int
		term string
	}
	var ms []match
	for _, loc := range TermRe.FindAllStringIndex(text, -1) {
		term := strings.TrimRight(text[loc[0]:loc[1]], ".")
		if len(term) < 6 || strings.Contains(term, ".") && !strings.ContainsAny(term, "-/") {
			continue
		}
		ms = append(ms, match{loc[0], term})
	}
	for _, loc := range prefixRe.FindAllStringIndex(text, -1) {
		term := strings.TrimSpace(text[loc[0]:loc[1]])
		if len(term) < 6 || labelWords[strings.ToLower(term)] || labelStart(text[:loc[0]]) {
			continue
		}
		ms = append(ms, match{loc[0], term})
	}
	for _, loc := range capsRe.FindAllStringIndex(text, -1) {
		if rest := text[loc[1]:]; len(rest) > 1 && rest[0] == '.' && isLetter(rest[1]) {
			continue
		}
		ms = append(ms, match{loc[0], text[loc[0]:loc[1]]})
	}
	sort.SliceStable(ms, func(i, j int) bool { return ms[i].at < ms[j].at })
	seen := map[string]bool{}
	var out []string
	for _, m := range ms {
		lt := strings.ToLower(m.term)
		if seen[lt] || stop[lt] {
			continue
		}
		seen[lt] = true
		out = append(out, m.term)
	}
	return out
}

// labelStart is whether a word after before starts a sentence or a list
// item: nothing before it, or a full stop, colon, semicolon or bracket.
func labelStart(before string) bool {
	b := strings.TrimRight(before, " \t\n")
	if b == "" {
		return true
	}
	return strings.ContainsRune(".!?:;()", rune(b[len(b)-1]))
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// SectionsText is the text of a retro's labelled sections (the part a
// pattern is looked for in); the whole body when it has none.
func SectionsText(body string) string {
	secs := Sections(body)
	if len(secs) == 0 {
		return body
	}
	var b strings.Builder
	for _, s := range secs {
		b.WriteString(s.Text)
		b.WriteString("\n")
	}
	return b.String()
}
