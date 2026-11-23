package tracker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	_ "time/tzdata" // Europe/Warsaw on a machine without a zoneinfo database

	"github.com/kacperwerema/dark-factory/internal/period"
)

// driftBoard is a fixture board in a git repo with exactly one finding of
// each kind and near misses that must not show:
//
//	citation    board/open cites board/rules#D-2 (replaced by D-3);
//	            board/old (done) cites it too, board/fine cites D-3
//	paths       a direct commit on main touching internal/store/** (D-1);
//	            a Refs: commit and a work-branch commit whose issues cite
//	            D-1, a merged-in main commit, web/** changes made before
//	            D-9 (hard on web/**) was decided
//	docs        docs/departments.md's Settled rules line naming
//	            board/rules#D-1, #D-2; the same ref outside the heading
//	provisional D-5 (project, 30 days old); D-6 (issue scope, old: the
//	            rest count), D-7 (project, 13 days old)
type driftBoard struct {
	repo   *respectedRepo
	issues []*Issue
	dir    string // issues dir
	today  time.Time
	direct string // the direct commit's sha
}

func newDriftBoard(t *testing.T) *driftBoard {
	t.Helper()
	return newDriftBoardAt(t, time.Time{})
}

// newDriftBoardAt is the fixture board with today fixed at now (zero: the
// real clock, read when its fixture is first built). With a fixed now every
// commit is dated a minute before it, so the decisions' dates and the
// commits agree whatever the real day. The repo is a fresh copy of the
// clock's fixture, so a test may commit on it.
func newDriftBoardAt(t *testing.T, now time.Time) *driftBoard {
	t.Helper()
	f := driftFixtureAt(t, now)
	repo := &respectedRepo{t: t, dir: t.TempDir()}
	if err := os.CopyFS(repo.dir, os.DirFS(f.dir)); err != nil {
		t.Fatal(err)
	}
	today := f.today
	day := func(n int) string { return today.AddDate(0, 0, -n).Format("2006-01-02") }
	rules := &Issue{Slug: "board/rules", Status: "done", BodyRaw: "## Decisions\n\n" +
		fmt.Sprintf(`- **D-1** · %s · chat: The store is append-only, "append only" {scope: project; hard; paths: internal/store/**}`, day(40)) + "\n" +
		fmt.Sprintf(`- **D-2** · %s · chat: Pages are plain, "plain"`, day(40)) + "\n" +
		fmt.Sprintf(`- **D-3** · %s · chat: Pages may use one script, "one script" (replaces board/rules#D-2)`, day(20)) + "\n" +
		fmt.Sprintf(`- **D-4** · %s · chat: Freeze till the fair, "freeze" {expires: %s}`, day(40), day(5)) + "\n" +
		fmt.Sprintf(`- **D-5** · %s · YOLO, provisional: Reports go weekly {scope: project}`, day(30)) + "\n" +
		fmt.Sprintf(`- **D-6** · %s · YOLO, provisional: This issue keeps a short title`, day(30)) + "\n" +
		fmt.Sprintf(`- **D-7** · %s · YOLO, provisional: Logs rotate daily {scope: project}`, day(13)) + "\n" +
		fmt.Sprintf(`- **D-8** · %s · recorded by agent: No quote, not provisional {scope: project}`, day(30)) + "\n" +
		fmt.Sprintf(`- **D-9** · %s · chat: Pages get a review, "review" {scope: project; hard; paths: web/**}`, today.AddDate(0, 0, 1).Format("2006-01-02")) + "\n"}
	b := &driftBoard{repo: repo, dir: t.TempDir(), today: today, direct: f.direct, issues: []*Issue{
		rules,
		{Slug: "board/open", Status: "in progress", BodyRaw: "Follows board/rules#D-2.\n"},
		{Slug: "board/old", Status: "done", BodyRaw: "Followed board/rules#D-2.\n"},
		{Slug: "board/fine", Status: "in progress", BodyRaw: "Follows board/rules#D-3 and board/rules#D-1.\n"},
		{Slug: "board/citer", Status: "done", BodyRaw: "Store change, follows board/rules#D-1.\n"},
		{Slug: "board/feature", Status: "done", BodyRaw: "Follows board/rules#D-1.\n"},
		{Slug: "board/other", Status: "done", BodyRaw: "Nothing to cite.\n"},
	}}
	if err := SaveDriftMark(b.dir, DriftMark{At: today.Add(-time.Hour), Main: f.init}); err != nil {
		t.Fatal(err)
	}
	b.based(t, "board/other", f.otherBase)
	b.based(t, "board/feature", f.featureBase)
	return b
}

// driftFixture is the fixture board's git repo for one clock. Its commits
// are dated by the clock, so each clock has its own. Every clock's is built
// once per test binary, all at once and in parallel (about 0.5 s of serial
// git each), and each test gets a copy of its clock's.
type driftFixture struct {
	dir                                  string
	today                                time.Time
	init, otherBase, featureBase, direct string
}

// driftFixtures are the fixtures by clock (driftFixtureKey), built on the
// first test's call.
var (
	driftFixturesMu sync.Mutex
	driftFixtures   map[string]*driftFixture
)

// driftFixturesDir holds the fixtures' repos; TestMain removes it.
func driftFixturesDir() string { return filepath.Join(os.TempDir(), "drift-fixtures") }

func driftFixtureKey(now time.Time) string {
	if now.IsZero() {
		return "real clock"
	}
	return now.Format(time.RFC3339) + " " + now.Location().String()
}

// driftFixtureAt is the fixture for now's clock: one of driftClocks, or
// built on its own for another.
func driftFixtureAt(t *testing.T, now time.Time) *driftFixture {
	t.Helper()
	driftFixturesMu.Lock()
	defer driftFixturesMu.Unlock()
	if driftFixtures == nil {
		var nows []time.Time
		for _, c := range driftClocks(t) {
			nows = append(nows, c.now)
		}
		built, err := buildDriftFixtures(nows)
		if err != nil {
			t.Fatal(err)
		}
		driftFixtures = built
	}
	if f := driftFixtures[driftFixtureKey(now)]; f != nil {
		return f
	}
	built, err := buildDriftFixtures([]time.Time{now})
	if err != nil {
		t.Fatal(err)
	}
	for k, f := range built {
		driftFixtures[k] = f
	}
	return driftFixtures[driftFixtureKey(now)]
}

// buildDriftFixtures builds a fixture for each of nows in parallel, under
// driftFixturesDir. Its git takes the commit dates in its own environment,
// so nothing here touches the process's.
func buildDriftFixtures(nows []time.Time) (map[string]*driftFixture, error) {
	out := make([]*driftFixture, len(nows))
	errs := make([]error, len(nows))
	var wg sync.WaitGroup
	for i, now := range nows {
		dir, err := os.MkdirTemp(mkdirAll(driftFixturesDir()), "clock-")
		if err != nil {
			return nil, err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i], errs[i] = buildDriftFixture(dir, now)
		}()
	}
	wg.Wait()
	built := map[string]*driftFixture{}
	for i, now := range nows {
		if errs[i] != nil {
			return nil, fmt.Errorf("drift fixture at %s: %w", driftFixtureKey(now), errs[i])
		}
		built[driftFixtureKey(now)] = out[i]
	}
	return built, nil
}

func mkdirAll(dir string) string {
	_ = os.MkdirAll(dir, 0o755) // MkdirTemp's error names it
	return dir
}

func buildDriftFixture(dir string, now time.Time) (*driftFixture, error) {
	f := &driftFixture{dir: dir, today: now}
	var env []string
	if f.today.IsZero() {
		f.today = time.Now()
	} else {
		at := f.today.Add(-time.Minute).Format(time.RFC3339)
		env = []string{"GIT_AUTHOR_DATE=" + at, "GIT_COMMITTER_DATE=" + at}
	}
	var failed error
	git := func(args ...string) string {
		if failed != nil {
			return ""
		}
		out, err := respectedGit(dir, env, args...).CombinedOutput()
		if err != nil {
			failed = fmt.Errorf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil && failed == nil {
			failed = err
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil && failed == nil {
			failed = err
		}
	}
	commit := func(msg string) string {
		git("add", "-A")
		git("commit", "-q", "-m", msg)
		return git("rev-parse", "HEAD")
	}
	// What newRespectedRepo starts with.
	git("init", "-q", "-b", "main")
	git("config", "maintenance.auto", "false")
	git("config", "gc.auto", "0")
	write("README.md", "hi\n")
	write("internal/store/db.go", "package store\n")
	write("web/page.html", "<p>\n")
	f.init = commit("init")

	// work/board/other is cut before the direct commit and later merges main.
	f.otherBase = f.init
	git("checkout", "-q", "-b", "work/board/other", "main")
	write("web/other.html", "<p>other\n")
	commit("feat: other page")
	git("checkout", "-q", "main")

	write("internal/store/direct.go", "package store\n")
	f.direct = commit("fix: store tweak straight on main")
	write("internal/store/refs.go", "package store\n")
	commit("feat: store via refs\n\nRefs: board/citer")
	write("web/plain.html", "<p>plain\n")
	f.featureBase = commit("feat: a page")

	git("checkout", "-q", "-b", "work/board/feature", "main")
	write("internal/store/feature.go", "package store\n")
	commit("feat: store feature")
	git("checkout", "-q", "main")
	git("merge", "-q", "--ff-only", "work/board/feature")

	git("checkout", "-q", "work/board/other")
	git("merge", "-q", "--no-edit", "main", "-m", "chore(merge): main into board/other")
	git("checkout", "-q", "main")
	git("merge", "-q", "--ff-only", "work/board/other")

	write("docs/departments.md", "# Departments\n\nSee board/rules#D-2 for history.\n\n## Settled rules\n\n"+
		"- **Store.** Append only. Sources: board/rules#D-1, #D-2.\n"+
		"- **Pages.** One script. Source: board/rules#D-3.\n\n## Config\n\nboard/rules#D-2 again.\n")
	return f, failed
}

// based gives issue slug a file in the issues dir and a stats sidecar
// whose dispatch recorded base as the branch's base.
func (b *driftBoard) based(t *testing.T, slug, base string) {
	t.Helper()
	for _, is := range b.issues {
		if is.Slug != slug {
			continue
		}
		is.FilePath = filepath.Join(b.dir, filepath.FromSlash(slug)+".md")
		if err := os.MkdirAll(filepath.Dir(is.FilePath), 0o755); err != nil {
			t.Fatal(err)
		}
		st := fmt.Sprintf(`{"transitions":[],"dispatches":[{"at":%q,"session":"s","agent":"a","base_branch":"main","base_sha":%q}]}`, b.today.Format(time.RFC3339), base)
		if err := os.WriteFile(StatsSidecarPath(is.FilePath), []byte(st), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("no issue %s", slug)
}

func (b *driftBoard) run(t *testing.T) DriftReport {
	t.Helper()
	rep, err := Drift(DriftInput{Issues: b.issues, IsClosed: func(i *Issue) bool { return i.Status == "done" },
		IssueDir: b.dir, Root: b.repo.dir, DocsDir: filepath.Join(b.repo.dir, "docs"), Today: b.today})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

// driftClocks are the clocks the fixture board's tests run at: the real
// one, and fixed late evenings in a zone with DST (time.Local set to it),
// where a day across the change is 25 or 23 hours long. D-7 is 13 calendar
// days old at each and never listed.
func driftClocks(t *testing.T) []struct {
	name string
	now  time.Time // zero: the real clock
} {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		t.Fatal(err)
	}
	at := func(y int, m time.Month, d, hh, mm int) time.Time { return time.Date(y, m, d, hh, mm, 0, 0, loc) }
	return []struct {
		name string
		now  time.Time
	}{
		{"real clock", time.Time{}},
		{"23:30 after the autumn DST change", at(2026, time.November, 2, 23, 30)},
		{"23:59 after the autumn DST change", at(2026, time.October, 26, 23, 59)},
		{"00:01 after the spring DST change", at(2026, time.April, 6, 0, 1)},
		{"23:59 on a plain day", at(2026, time.July, 15, 23, 59)},
	}
}

// clockNow sets time.Local to now's zone for the rest of the test (inZone)
// unless now is the real clock, and returns now.
func clockNow(t *testing.T, now time.Time) time.Time {
	t.Helper()
	if !now.IsZero() {
		inZone(t, now.Location().String())
	}
	return now
}

// inZone sets time.Local to the named zone for the rest of the test (the
// decision dates parse in it) and returns it. Tracker tests don't run in
// parallel, so nothing else reads time.Local meanwhile.
func inZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = old })
	return loc
}

func TestDriftFixtureBoardEachKindOnce(t *testing.T) {
	for _, c := range driftClocks(t) {
		t.Run(c.name, func(t *testing.T) { driftFixtureBoardEachKindOnce(t, clockNow(t, c.now)) })
	}
}

func driftFixtureBoardEachKindOnce(t *testing.T, now time.Time) {
	b := newDriftBoardAt(t, now)
	rep := b.run(t)
	if len(rep.Findings) != 4 {
		t.Fatalf("want 4 findings, one per kind, got %d:\n%+v", len(rep.Findings), rep.Findings)
	}
	for _, k := range DriftKinds {
		if n := rep.Count(k); n != 1 {
			t.Errorf("kind %s: %d findings, want 1", k, n)
		}
	}
	for _, f := range rep.Findings {
		switch f.Kind {
		case DriftCitation:
			if f.Issue != "board/open" || f.Ref != "board/rules#D-2" || f.ReplacedBy != "board/rules#D-3" {
				t.Errorf("citation: %+v", f)
			}
		case DriftPaths:
			if f.Ref != "board/rules#D-1" || f.SHA != b.direct || f.Issue != "" || len(f.Files) != 1 || f.Files[0] != "internal/store/direct.go" {
				t.Errorf("paths: %+v (direct %s)", f, b.direct)
			}
		case DriftDocs:
			if f.Ref != "board/rules#D-2" || f.File != "docs/departments.md" || f.Line != 7 {
				t.Errorf("docs: %+v", f)
			}
		case DriftProvisional:
			if f.Ref != "board/rules#D-5" || f.Scope != ScopeProject {
				t.Errorf("provisional: %+v", f)
			}
		}
	}
	if rep.ProvisionalRest != 1 {
		t.Errorf("provisional rest = %d, want 1 (D-6)", rep.ProvisionalRest)
	}
	if rep.Window.First || rep.Window.From == "" || rep.Window.To == "" {
		t.Errorf("window should read from the mark: %+v", rep.Window)
	}
}

func TestDriftSecondRecordedRunNoPathDrift(t *testing.T) {
	b := newDriftBoard(t)
	first := b.run(t)
	if first.Count(DriftPaths) != 1 {
		t.Fatalf("first run: %d path findings, want 1", first.Count(DriftPaths))
	}
	if err := SaveDriftMark(b.dir, DriftMark{At: b.today, Main: first.Window.To}); err != nil {
		t.Fatal(err)
	}
	second := b.run(t)
	if n := second.Count(DriftPaths); n != 0 {
		t.Fatalf("second run with nothing new on main: %d path findings, want 0: %+v", n, second.Findings)
	}
	if second.Count(DriftCitation) != 1 || second.Count(DriftDocs) != 1 {
		t.Errorf("state kinds repeat until fixed: %+v", second.Findings)
	}
	// Something new on main shows again.
	b.repo.write("internal/store/later.go", "package store\n")
	sha := b.repo.commit("fix: later store change")
	third := b.run(t)
	if third.Count(DriftPaths) != 1 || third.Findings[1].SHA != sha {
		t.Errorf("a new commit after the mark: %+v", third.Findings)
	}
}

func TestDriftDocsSettledRuleNamesFileLine(t *testing.T) {
	x := BuildDecisionIndex([]*Issue{{Slug: "board/rules", BodyRaw: "## Decisions\n\n" +
		`- **D-1** · 2026-10-01 · chat: A, "a"` + "\n" +
		`- **D-2** · 2026-10-01 · chat: B, "b"` + "\n" +
		`- **D-3** · 2026-10-02 · chat: C, "c" (replaces board/rules#D-2)` + "\n"}}, nil)
	text := "## Settled rules\n\n- One. Source: board/rules#D-1.\n- Two. Sources: board/rules#D-1, #D-2; other#D-2.\n\n```\nboard/rules#D-2\n```\n### Sub\n- board/rules#D-2 again\n## Next\n- board/rules#D-2\n"
	got := settledRuleDrift("docs/x.md", text, x, time.Now())
	if len(got) != 2 {
		t.Fatalf("want the bare #D-2 on line 4 and the subsection line 10, got %+v", got)
	}
	if got[0].File != "docs/x.md" || got[0].Line != 4 || got[0].Ref != "board/rules#D-2" || got[0].ReplacedBy != "board/rules#D-3" {
		t.Errorf("first: %+v", got[0])
	}
	if !strings.Contains(got[0].Fix, "docs/x.md:4") {
		t.Errorf("fix should name file:line: %q", got[0].Fix)
	}
	if got[1].Line != 10 {
		t.Errorf("second: %+v", got[1])
	}
}

func TestDriftFirstSweepReadsTheLastDays(t *testing.T) {
	b := newDriftBoard(t)
	if err := os.Remove(DriftMarkPath(b.dir)); err != nil {
		t.Fatal(err)
	}
	rep := b.run(t)
	if !rep.Window.First || rep.Window.From != "" {
		t.Fatalf("no mark: want a first-sweep window, got %+v", rep.Window)
	}
	// The repo's init commit (internal/store/db.go) is in the window too.
	if n := rep.Count(DriftPaths); n != 2 {
		t.Errorf("first sweep: %d path findings, want 2 (init and the direct commit): %+v", n, rep.Findings)
	}
}

func TestDriftMarkNotInHistoryFallsBack(t *testing.T) {
	b := newDriftBoard(t)
	if err := SaveDriftMark(b.dir, DriftMark{At: b.today, Main: strings.Repeat("ab", 20)}); err != nil {
		t.Fatal(err)
	}
	rep := b.run(t)
	if !rep.Window.First || rep.Window.Note == "" {
		t.Errorf("a mark not in main's history: want the fallback window with a note, got %+v", rep.Window)
	}
}

func TestDriftNoRepoSkipsPathsOnly(t *testing.T) {
	for _, c := range driftClocks(t) {
		t.Run(c.name, func(t *testing.T) {
			b := newDriftBoardAt(t, clockNow(t, c.now))
			rep, err := Drift(DriftInput{Issues: b.issues, IsClosed: func(i *Issue) bool { return i.Status == "done" }, IssueDir: b.dir, Today: b.today})
			if err != nil {
				t.Fatal(err)
			}
			if rep.Count(DriftPaths) != 0 || rep.Window.Note == "" || rep.Count(DriftCitation) != 1 || rep.Count(DriftProvisional) != 1 {
				t.Errorf("no repo: %+v", rep)
			}
		})
	}
}

// A provisional decision's age is calendar days, date against date: 14
// days old is not listed, 15 is, at any hour and across a DST change.
func TestDriftProvisionalAgeCountsCalendarDays(t *testing.T) {
	loc := inZone(t, "Europe/Warsaw")
	for _, c := range []struct {
		name  string
		today time.Time
		date  string
		want  bool
	}{
		{"13 days, 23:30 after the autumn change", time.Date(2026, 11, 2, 23, 30, 0, 0, loc), "2026-10-20", false},
		{"14 days, 23:30 after the autumn change", time.Date(2026, 11, 2, 23, 30, 0, 0, loc), "2026-10-19", false},
		{"15 days, 00:00 after the autumn change", time.Date(2026, 11, 2, 0, 0, 0, 0, loc), "2026-10-18", true},
		{"14 days, 23:59:59 on a plain day", time.Date(2026, 7, 15, 23, 59, 59, 0, loc), "2026-07-01", false},
		{"15 days, 00:00:01 on a plain day", time.Date(2026, 7, 15, 0, 0, 1, 0, loc), "2026-06-30", true},
		{"15 days, 00:30 after the spring change", time.Date(2026, 4, 6, 0, 30, 0, 0, loc), "2026-03-22", true},
		{"14 days, 23:59 after the spring change", time.Date(2026, 4, 6, 23, 59, 0, 0, loc), "2026-03-23", false},
		{"15 days, today given in UTC", time.Date(2026, 11, 2, 23, 30, 0, 0, time.UTC), "2026-10-18", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			issue := &Issue{Slug: "board/rules", Status: "done", BodyRaw: "## Decisions\n\n" +
				"- **D-1** · " + c.date + " · YOLO, provisional: Logs rotate daily {scope: project}\n"}
			rep, err := Drift(DriftInput{Issues: []*Issue{issue}, IssueDir: t.TempDir(), Today: c.today})
			if err != nil {
				t.Fatal(err)
			}
			if got := rep.Count(DriftProvisional) == 1; got != c.want {
				t.Errorf("decision of %s on %s: listed %v, want %v: %+v", c.date, c.today, got, c.want, rep.Findings)
			}
		})
	}
}

func TestDriftFirstSweepWindowStartsAtLocalMidnight(t *testing.T) {
	loc := inZone(t, "Europe/Warsaw")
	today := time.Date(2026, 11, 2, 23, 30, 0, 0, loc)
	if got, want := period.DaysBack(today, DriftProvisionalAge), time.Date(2026, 10, 19, 0, 0, 0, 0, loc); !got.Equal(want) {
		t.Errorf("window start = %s, want %s", got, want)
	}
}

func TestIsProvisional(t *testing.T) {
	for _, c := range []struct {
		how, text string
		want      bool
	}{
		{"YOLO", "x", true},
		{"lead chat, provisional", "x", true},
		{"recorded by agent", "x (provisional, owner reviews)", true},
		{"recorded by agent", "x", false},
		{"YOLO, confirmed by owner 2026-10-01", "x", false},
	} {
		if got := IsProvisional(Decision{How: c.how, Text: c.text}); got != c.want {
			t.Errorf("IsProvisional(%q, %q) = %v, want %v", c.how, c.text, got, c.want)
		}
	}
}
