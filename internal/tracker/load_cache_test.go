package tracker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// idleBoard writes n issues with mtimes older than racyWindow, as a board
// nobody touched for a while.
func idleBoard(t *testing.T, n int) string {
	t.Helper()
	dir := t.TempDir()
	for i := 0; i < n; i++ {
		writeSettled(t, filepath.Join(dir, "CLI", fmt.Sprintf("i%d.md", i)), issueText(fmt.Sprintf("Issue %d", i), "idea"))
	}
	return dir
}

func issueText(title, status string) string {
	return fmt.Sprintf("---\ntitle: %q\nstatus: %q\nlabels:\n  - bug\nowner_team: core\n---\n\n## Body\n\n- [ ] box\n\n<!-- issue-viewer-comments\n{\"id\":1,\"date\":\"2026-11-04\",\"text\":\"progress: one\"}\n-->\n", title, status)
}

func writeSettled(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

func mustLoad(t *testing.T, dir string) []*Issue {
	t.Helper()
	issues, err := LoadIssues(dir)
	if err != nil {
		t.Fatal(err)
	}
	return issues
}

func parsesDuring(fn func()) int64 {
	before := ReadLoadCounts().IssueParses
	fn()
	return ReadLoadCounts().IssueParses - before
}

// Audit cause 4: one load cost 65 ms on a board of 330 files and a page did
// 4 to 14. Loading again parses only what changed.
func TestLoadIssuesParsesOnlyWhatChanged(t *testing.T) {
	dir := idleBoard(t, 5)
	if got := parsesDuring(func() { mustLoad(t, dir) }); got != 5 {
		t.Fatalf("first load parsed %d files, want 5", got)
	}
	if got := parsesDuring(func() { mustLoad(t, dir) }); got != 0 {
		t.Fatalf("second load parsed %d files, want 0", got)
	}

	writeSettled(t, filepath.Join(dir, "CLI", "i0.md"), issueText("Issue 0", "in progress"))
	got := parsesDuring(func() {
		issues := mustLoad(t, dir)
		if i := FindIssue(issues, "cli/issue-0"); i == nil || i.Status != "in progress" {
			t.Fatalf("changed issue not read: %+v", i)
		}
	})
	if got != 1 {
		t.Fatalf("after one change a load parsed %d files, want 1", got)
	}

	os.Remove(filepath.Join(dir, "CLI", "i1.md"))
	writeSettled(t, filepath.Join(dir, "CLI", "new.md"), issueText("New", "idea"))
	issues := mustLoad(t, dir)
	if len(issues) != 5 || FindIssue(issues, "cli/issue-1") != nil || FindIssue(issues, "cli/new") == nil {
		t.Fatalf("delete and add not seen: %d issues", len(issues))
	}
}

// A file changed twice within the clock's tick keeps its size and mtime: a
// file that young is not cached at all, so the second write is seen.
func TestLoadIssuesRereadsAYoungFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.md")
	os.WriteFile(path, []byte(issueText("Same", "idea")), 0o644)
	info, _ := os.Stat(path)
	mustLoad(t, dir)
	os.WriteFile(path, []byte(issueText("Same", "icea")), 0o644) // same size
	os.Chtimes(path, info.ModTime(), info.ModTime())
	if got := mustLoad(t, dir)[0].Status; got != "icea" {
		t.Fatalf("status = %q: a young file was served from the cache", got)
	}
}

func TestLoadIssuesFollowsAWriteThroughTheTracker(t *testing.T) {
	dir := idleBoard(t, 1)
	issue := mustLoad(t, dir)[0]
	if err := AddCommentBy(issue.FilePath, 0, "progress: two", "cli", "t"); err != nil {
		t.Fatal(err)
	}
	if got := len(mustLoad(t, dir)[0].Comments()); got != 2 {
		t.Fatalf("comments after a write = %d, want 2", got)
	}
}

// Callers set fields such as Slug, and sort what they get.
func TestLoadIssuesCallersGetTheirOwnCopies(t *testing.T) {
	dir := idleBoard(t, 1)
	first := mustLoad(t, dir)[0]
	first.Slug, first.Status = "changed", "changed"
	first.Labels[0] = "changed"
	first.ExtraFields[0].Value = "changed"
	first.Comments()[0].Text = "changed"

	second := mustLoad(t, dir)[0]
	if second.Slug != "cli/issue-0" || second.Status != "idea" || second.Labels[0] != "bug" ||
		second.ExtraFields[0].Value != "core" || second.Comments()[0].Text != "progress: one" {
		t.Fatalf("a caller's change reached the cache: %+v", second)
	}
}

func TestLoadIssuesCollidingSlugsStayNumbered(t *testing.T) {
	dir := t.TempDir()
	writeSettled(t, filepath.Join(dir, "a.md"), issueText("Same", "idea"))
	writeSettled(t, filepath.Join(dir, "b.md"), issueText("Same", "idea"))
	for pass := 0; pass < 2; pass++ {
		issues := mustLoad(t, dir)
		if FindIssue(issues, "same") == nil || FindIssue(issues, "same-2") == nil {
			t.Fatalf("pass %d: slugs lost: %s %s", pass, issues[0].Slug, issues[1].Slug)
		}
	}
}

func TestLoadIssuesWhereUsesTheCache(t *testing.T) {
	dir := idleBoard(t, 3)
	mustLoad(t, dir)
	var kept []*Issue
	reads := ReadLoadCounts().FileReads
	got := parsesDuring(func() {
		kept, _ = LoadIssuesWhere(dir, func(data []byte) bool { return strings.Contains(string(data), "Issue 1") })
	})
	if got != 0 || len(kept) != 1 {
		t.Fatalf("parsed %d, kept %d; want 0 and 1", got, len(kept))
	}
	if n := ReadLoadCounts().FileReads - reads; n != 0 {
		t.Fatalf("a filtered load read %d files, want none: the cache holds them", n)
	}
	// A filtered load holds only some files: it must not replace the cache.
	if got := parsesDuring(func() { mustLoad(t, dir) }); got != 0 {
		t.Fatalf("after a filtered load a full one parsed %d files", got)
	}
}

// Audit step 1: every part of a request shares one load.
func TestShareBoardLoad(t *testing.T) {
	dir := idleBoard(t, 2)
	p := Project{IssueDir: dir}
	release := p.ShareBoardLoad()

	before := ReadLoadCounts().BoardWalks
	first, err := p.LoadIssues()
	if err != nil {
		t.Fatal(err)
	}
	first[0].Slug = "changed"
	first[0].Labels[0] = "changed"
	second, _ := p.LoadIssues()
	if got := ReadLoadCounts().BoardWalks - before; got != 1 {
		t.Fatalf("two loads walked the board %d times, want 1", got)
	}
	if second[0].Slug == "changed" || second[0].Labels[0] == "changed" {
		t.Fatalf("callers share issues: %+v", second[0])
	}

	release()
	p.LoadIssues()
	if got := ReadLoadCounts().BoardWalks - before; got != 2 {
		t.Fatalf("after release walks = %d, want 2", got)
	}

	plain := Project{IssueDir: dir}
	plain.LoadIssues()
	plain.LoadIssues()
	if got := ReadLoadCounts().BoardWalks - before; got != 4 {
		t.Fatalf("a project without a shared load walks each time: %d, want 4", got)
	}
}

// Audit cause 5: goldmark was 60% of a load, for pages that never show a body.
func TestBodyRenderedOnlyWhenAsked(t *testing.T) {
	dir := idleBoard(t, 3)
	before := ReadLoadCounts().BodyRenders
	issues := mustLoad(t, dir)
	if got := ReadLoadCounts().BodyRenders - before; got != 0 {
		t.Fatalf("a load rendered %d bodies", got)
	}
	if html := issues[0].BodyHTML(); !strings.Contains(html, "<h2") || !strings.Contains(html, `type="checkbox"`) {
		t.Fatalf("BodyHTML = %q", html)
	}
	if got := ReadLoadCounts().BodyRenders - before; got != 1 {
		t.Fatalf("renders = %d, want 1", got)
	}
}

// Audit cause 6: the comments were parsed, thrown away and read again.
func TestCommentsComeWithTheIssue(t *testing.T) {
	dir := idleBoard(t, 1)
	issue := mustLoad(t, dir)[0]
	before := ReadLoadCounts().CommentReads
	got := issue.Comments()
	if n := ReadLoadCounts().CommentReads - before; n != 0 {
		t.Fatalf("Comments read the file %d times", n)
	}
	want, err := LoadComments(issue.FilePath)
	if err != nil || len(got) != 1 || len(want) != 1 || got[0].Text != want[0].Text {
		t.Fatalf("Comments = %+v, LoadComments = %+v (%v)", got, want, err)
	}

	hand := &Issue{FilePath: issue.FilePath}
	if c := hand.Comments(); len(c) != 1 {
		t.Fatalf("an Issue built by hand has %d comments, want its file's 1", len(c))
	}
	if parsed := len((&Issue{FilePath: filepath.Join(dir, "gone.md")}).Comments()); parsed != 0 {
		t.Fatalf("a missing file has %d comments", parsed)
	}
}

const cacheWorkflow = "statuses:\n  - name: idea\n  - name: done\ntransitions:\n  - from: idea\n    to: done\n    actions:\n      - type: validate\n        rule: body_not_empty\nsystems:\n  UI:\n    statuses:\n      - name: idea\n        prompt_append: ui\nboard:\n  columns: [idea]\n"

func writeSettledWorkflow(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workflow.yaml")
	writeSettled(t, path, text)
	return path
}

func workflowParsesDuring(fn func()) int64 {
	before := ReadLoadCounts().WorkflowParses
	fn()
	return ReadLoadCounts().WorkflowParses - before
}

// Audit cause 3: 714 parses of workflow.yaml for one page.
func TestLoadWorkflowParsesAFileOnce(t *testing.T) {
	path := writeSettledWorkflow(t, cacheWorkflow)
	got := workflowParsesDuring(func() {
		for i := 0; i < 10; i++ {
			if _, err := LoadWorkflow(path); err != nil {
				t.Fatal(err)
			}
		}
	})
	if got != 1 {
		t.Fatalf("10 loads parsed %d times, want 1", got)
	}

	p := &Project{WorkflowFile: path, IssueDir: t.TempDir()}
	issue := &Issue{System: "UI", Status: "idea"}
	got = workflowParsesDuring(func() {
		for i := 0; i < 10; i++ {
			p.LoadWorkflowForIssue(issue)
		}
	})
	if got != 0 {
		t.Fatalf("10 LoadWorkflowForIssue parsed %d times, want 0", got)
	}

	writeSettled(t, path, strings.Replace(cacheWorkflow, "name: done", "name: shipped", 1))
	wf, err := LoadWorkflow(path)
	if err != nil || wf.Statuses[1].Name != "shipped" {
		t.Fatalf("a changed file was not read: %+v, %v", wf, err)
	}
}

func TestLoadWorkflowCallersGetTheirOwnConfig(t *testing.T) {
	path := writeSettledWorkflow(t, cacheWorkflow)
	LoadWorkflow(path)
	first, _ := LoadWorkflow(path)
	first.Statuses[0].Name = "changed"
	first.Transitions[0].Actions[0].Rule = "changed"
	first.Board.Columns[0] = "changed"
	first.IssuesRoot = "changed"
	first.LookupIssue = func(string) *Issue { return nil }

	second, _ := LoadWorkflow(path)
	if second.Statuses[0].Name != "idea" || second.Transitions[0].Actions[0].Rule != "body_not_empty" ||
		second.Board.Columns[0] != "idea" || second.IssuesRoot != "" || second.LookupIssue != nil {
		t.Fatalf("a caller's change reached the cache: %+v", second)
	}
}

func TestLoadWorkflowRereadsAYoungFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workflow.yaml")
	os.WriteFile(path, []byte("statuses:\n  - name: aaa\n"), 0o644)
	info, _ := os.Stat(path)
	LoadWorkflow(path)
	os.WriteFile(path, []byte("statuses:\n  - name: bbb\n"), 0o644)
	os.Chtimes(path, info.ModTime(), info.ModTime())
	if wf, err := LoadWorkflow(path); err != nil || wf.Statuses[0].Name != "bbb" {
		t.Fatalf("a young file was served from the cache: %+v, %v", wf, err)
	}
}

func TestLoadWorkflowForgetsABrokenOrMissingFile(t *testing.T) {
	path := writeSettledWorkflow(t, cacheWorkflow)
	LoadWorkflow(path)
	writeSettled(t, path, "statuses: [")
	if _, err := LoadWorkflow(path); err == nil {
		t.Fatal("a broken file loaded")
	}
	os.Remove(path)
	if _, err := LoadWorkflow(path); err == nil {
		t.Fatal("a missing file loaded")
	}
}

func TestFileCacheBuildsOncePerVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.txt")
	writeSettled(t, path, "one")
	var c FileCache[string]
	builds := 0
	get := func() string {
		t.Helper()
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		v, err := c.Get(path, info, func() (string, error) {
			builds++
			data, err := os.ReadFile(path)
			return string(data), err
		})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	get()
	if got := get(); got != "one" || builds != 1 {
		t.Fatalf("got %q after %d builds, want one build", got, builds)
	}
	writeSettled(t, path, "other")
	if got := get(); got != "other" {
		t.Fatalf("got %q after a change", got)
	}
}

func TestFileCacheKeepsNothingFromAYoungFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.txt")
	os.WriteFile(path, []byte("one"), 0o644)
	info, _ := os.Stat(path)
	var c FileCache[string]
	builds := 0
	for range 2 {
		c.Get(path, info, func() (string, error) { builds++; return "x", nil })
	}
	if builds != 2 {
		t.Fatalf("builds = %d, want 2: a young file can change with the same stamp", builds)
	}
}

func TestStatsOfFollowsTheSidecar(t *testing.T) {
	issue := filepath.Join(t.TempDir(), "a.md")
	if got, err := StatsOf(issue); err != nil || len(got.Transitions) != 0 {
		t.Fatalf("no sidecar: %+v, %v", got, err)
	}
	if err := SaveStats(issue, StatsStore{Transitions: []TransitionStat{{To: "testing"}}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := StatsOf(issue); len(got.Transitions) != 1 {
		t.Fatalf("after a save: %+v", got)
	}
	if err := SaveStats(issue, StatsStore{Transitions: []TransitionStat{{To: "shipping"}}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := StatsOf(issue); got.Transitions[0].To != "shipping" {
		t.Fatalf("after a second save: %+v", got)
	}
}
