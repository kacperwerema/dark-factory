package tracker

import (
	"fmt"
	pathpkg "path"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Caps on what MainHolds reads and names: the tests a brief names, and the
// commits shown for each.
const (
	mainHoldsNames   = 5
	mainHoldsCommits = 3
)

// testNameRe is a Go test, benchmark or fuzz function's name as a brief
// writes it.
var testNameRe = regexp.MustCompile(`\b(?:Test|Benchmark|Fuzz)[A-Z0-9_][A-Za-z0-9_]*`)

// MainHoldCommit is a commit on main that may already do what the brief asks.
type MainHoldCommit struct {
	Sha     string // short
	Date    string // committer date, 2006-01-02
	Subject string
}

// MainHoldTest is a test the brief names, the file main defines it in and
// the commits since the issue was created that changed it or name it.
type MainHoldTest struct {
	Name    string
	File    string
	Commits []MainHoldCommit
}

// MainHold is what main has done since the issue was created to the tests
// its title and brief name: a fix may have landed before anyone picked the
// issue up (retro 20261116-033105).
type MainHold struct {
	Main  string
	Since string // the issue's created date
	Tests []MainHoldTest
}

// BriefTestNames are the Go test names in the issue's title and brief: the
// body before its first heading and its Why and Bug sections. The design
// and the criteria are left out: they name the tests the work will write.
// First mainHoldsNames, in order, each once.
func BriefTestNames(issue *Issue) []string {
	if issue == nil {
		return nil
	}
	text := issue.Title + "\n" + bodyBeforeHeading(issue.BodyRaw)
	for _, s := range []string{"Why", "Bug"} {
		if t, ok := SectionText(issue.BodyRaw, s); ok {
			text += "\n" + t
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, n := range testNameRe.FindAllString(text, -1) {
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
		if len(out) == mainHoldsNames {
			break
		}
	}
	return out
}

// bodyBeforeHeading is the body up to its first markdown heading.
func bodyBeforeHeading(body string) string {
	lines := strings.Split(body, "\n")
	fence := computeFenceFlags(lines)
	for i, l := range lines {
		if fence[i] {
			continue
		}
		if _, _, ok := parseHeadingLine(strings.TrimSpace(l)); ok {
			return strings.Join(lines[:i], "\n")
		}
	}
	return body
}

// MainHolds asks main about the tests the issue's title and brief name: for
// each one main defines, the commits since the issue's created day (its
// 00:00 local) that change the test function or name it in their message,
// leaving out merges and the issue's own commits (Refs: <slug>). Nil when
// there is nothing to say: no named test main has, nothing changed, no
// created date, no git, or the issue's work branch already has commits of
// its own (the work is underway; what main did is in its merges).
func MainHolds(root string, issue *Issue) *MainHold {
	if root == "" || issue == nil {
		return nil
	}
	created, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(issue.Created), time.Local)
	if err != nil {
		return nil
	}
	names := BriefTestNames(issue)
	if len(names) == 0 {
		return nil
	}
	mainBranch, err := MainBranch(root)
	if err != nil {
		return nil
	}
	mainHead := BranchHead(root, mainBranch)
	if mainHead == "" {
		return nil
	}
	if own := BranchHead(root, "work/"+issue.Slug); own != "" && countCommits(root, mainHead, own) > 0 {
		return nil
	}
	since := "--since=" + created.Format("2006-01-02T15:04:05")
	out := &MainHold{Main: mainBranch, Since: created.Format("2006-01-02")}
	for _, name := range names {
		files, err := runGit(root, "grep", "-l", "-E", "^func "+name+`\(`, mainHead, "--", "*_test.go")
		if err != nil {
			continue
		}
		file := ""
		for _, l := range strings.Split(strings.TrimSpace(files), "\n") {
			if _, f, ok := strings.Cut(l, ":"); ok {
				file = f
				break
			}
		}
		if file == "" {
			continue
		}
		const format = "--format=%x1e%h%x1f%cs%x1f%ct%x1f%s%x1f%B"
		var logs []string
		if text, err := runGit(root, "log", mainHead, "--no-merges", since, "-s", format,
			"-L", "/^func "+name+"(/,/^}/:"+file); err == nil {
			logs = append(logs, text)
		}
		if text, err := runGit(root, "log", mainHead, "--no-merges", since, "-F", "--grep="+name, format); err == nil {
			logs = append(logs, text)
		}
		commits := mainHoldCommits(logs, issue.Slug)
		if len(commits) == 0 {
			continue
		}
		out.Tests = append(out.Tests, MainHoldTest{Name: name, File: file, Commits: commits})
	}
	if len(out.Tests) == 0 {
		return nil
	}
	return out
}

// mainHoldCommits reads the records of one or more git logs, each commit
// once, newest first, without the issue's own (a Refs: line naming slug),
// at most mainHoldsCommits.
func mainHoldCommits(logs []string, slug string) []MainHoldCommit {
	type rec struct {
		c    MainHoldCommit
		time string
	}
	seen := map[string]bool{}
	var recs []rec
	for _, text := range logs {
		for _, r := range strings.Split(text, "\x1e") {
			f := strings.SplitN(r, "\x1f", 5)
			if len(f) < 5 {
				continue
			}
			sha := strings.TrimSpace(f[0])
			if sha == "" || seen[sha] {
				continue
			}
			seen[sha] = true
			if refsIssue(f[4], slug) {
				continue
			}
			recs = append(recs, rec{c: MainHoldCommit{Sha: sha, Date: f[1], Subject: strings.TrimSpace(f[3])}, time: f[2]})
		}
	}
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].time > recs[j].time }) // unix seconds, same width
	var out []MainHoldCommit
	for _, r := range recs {
		if len(out) == mainHoldsCommits {
			break
		}
		out = append(out, r.c)
	}
	return out
}

// refsIssue reports whether a commit message has a Refs: line naming slug.
func refsIssue(msg, slug string) bool {
	for _, line := range strings.Split(msg, "\n") {
		v, ok := strings.CutPrefix(strings.TrimSpace(line), "Refs:")
		if !ok {
			continue
		}
		for _, ref := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' }) {
			if ref == slug {
				return true
			}
		}
	}
	return false
}

// Lines is the block dispatch, start and the briefing share: each commit
// under the test it touches, the go test that checks it, and what to do
// when main holds it.
func (m *MainHold) Lines() []string {
	if m == nil || len(m.Tests) == 0 {
		return nil
	}
	lines := []string{fmt.Sprintf("Since this issue was created (%s), %s changed the tests its brief names:", m.Since, m.Main)}
	for _, t := range m.Tests {
		for _, c := range t.Commits {
			lines = append(lines, fmt.Sprintf("- %s: %s %s %s", t.Name, c.Sha, c.Date, c.Subject))
		}
	}
	lines = append(lines, "Check before building:")
	for _, t := range m.Tests {
		lines = append(lines, fmt.Sprintf("  go test -count=1 -run '^%s$' ./%s/", t.Name, pathpkg.Dir(t.File)))
	}
	lines = append(lines, "If main already holds it, say so in a progress: comment naming the commit and stop: the lead closes the issue.")
	return lines
}

// Summary is the one line dispatch prints for the lead, and the stage
// briefing folds to: the first commit and how many more.
func (m *MainHold) Summary() string {
	if m == nil || len(m.Tests) == 0 {
		return ""
	}
	t := m.Tests[0]
	c := t.Commits[0]
	n := 0
	for _, t := range m.Tests {
		n += len(t.Commits)
	}
	more := ""
	if n > 1 {
		more = fmt.Sprintf(" (+%d more)", n-1)
	}
	return fmt.Sprintf("main may already hold it: %s %s (%s)%s", c.Sha, c.Subject, t.Name, more)
}

// MainHoldsPromptSection is the "## Already on main?" part of a worker's
// briefing; empty when MainHolds has nothing to say.
func MainHoldsPromptSection(m *MainHold) string {
	lines := m.Lines()
	if len(lines) == 0 {
		return ""
	}
	return "\n\n## Already on main?\n\n" + strings.Join(lines, "\n") + "\n"
}
