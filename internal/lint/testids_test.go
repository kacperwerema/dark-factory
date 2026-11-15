package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/testids"
)

// withSessions writes sessions under the fixture repository's
// playtest/sessions, and marks it as carrying internal/testids unless
// engine is false.
func withSessions(t *testing.T, env *Env, engine bool, sessions map[string]string) {
	t.Helper()
	root := env.Project.Root()
	if engine {
		os.MkdirAll(filepath.Join(root, "internal", "testids"), 0755)
	}
	dir := filepath.Join(root, "playtest", "sessions")
	os.MkdirAll(dir, 0755)
	for name, body := range sessions {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

const badSession = `session: bad
issue: x/y
title: Points at ids no template carries
scenario: basic
steps:
  - id: TP1
    title: Known ids pass, unknown ones are named
    setup:
      - open: /p/{project}/board?classic=1
      - click: board.colum.backlog
    checks:
      - visible: [board.column.backlog, nope.not.here]
      - text_equals: { target: board.column.backlog.count, text: "2" }
      - http: { path: "/p/{project}/stats", contains: 'data-testid="gone.http.marker"' }
  - id: TP2
    title: Overlay targets
    targets:
      - { id: ghost.overlay.target, note: "missing" }
    do: look
    expect: it is there
`

const goodSession = `session: good
issue: x/y
title: Only other selectors and listed ids
scenario: basic
steps:
  - id: TP1
    title: Listed ids, urls, CLI and requests
    setup:
      - open: /p/{project}/board?classic=1
      - post: { path: "/p/{project}/x", json: { target: not.an.id, a: b } }
      - cli: [show, x/y, --target, whatever]
    checks:
      - visible: board.column.backlog
      - count: { pattern: board.card.*, within: board.column.backlog.cards, equals: 2 }
      - url: { contains: /board }
      - http: { path: "/p/{project}/board", contains: "class=\"wb-card\"" }
      - issue: { slug: x/y, status: backlog }
`

func TestTestIDsCheck(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	withSessions(t, env, true, map[string]string{"bad.yaml": badSession, "good.yaml": goodSession})
	got := run(t, env, "testids")
	for _, want := range [][]string{
		{"playtest/sessions/bad.yaml:10", "step TP1", `"board.colum.backlog"`, `did you mean "board.column.*"`},
		{"playtest/sessions/bad.yaml:12", "step TP1", `"nope.not.here"`, "internal/testids"},
		{"playtest/sessions/bad.yaml:14", "step TP1", `"gone.http.marker"`},
		{"playtest/sessions/bad.yaml:18", "step TP2", `"ghost.overlay.target"`},
	} {
		if !has(got, want...) {
			t.Errorf("missing finding %v in %+v", want, got)
		}
	}
	if len(got) != 4 {
		t.Errorf("want 4 findings (good.yaml and listed ids left alone), got %+v", got)
	}
}

func TestTestIDsCheckOtherSelectorsLeftAlone(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	withSessions(t, env, true, map[string]string{"good.yaml": goodSession})
	if got := run(t, env, "testids"); len(got) != 0 {
		t.Errorf("a session with listed ids and other selectors: %+v", got)
	}
}

func TestTestIDsCheckOnlyOnTheEngine(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	withSessions(t, env, false, map[string]string{"bad.yaml": badSession})
	if got := run(t, env, "testids"); len(got) != 0 {
		t.Errorf("a repository without internal/testids has its own pages: %+v", got)
	}
	if got := run(t, lintProject(t, minimalWorkflow, nil), "testids"); len(got) != 0 {
		t.Errorf("no sessions, no findings: %+v", got)
	}
}

// TestLintTestIDsSeesAnEditedSession: a session's parse is kept by (path,
// size, mtime), so an edit shows in the next run, even one of the same size.
func TestLintTestIDsSeesAnEditedSession(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	withSessions(t, env, true, map[string]string{"edit.yaml": goodSession})
	path := filepath.Join(env.Project.Root(), sessionsDir, "edit.yaml")
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if got := run(t, env, "testids"); len(got) != 0 {
		t.Fatalf("the good session: %+v", got)
	}

	// Same size, same mtime: the kept parse stands, so the file isn't read.
	edited := strings.Replace(goodSession, "visible: board.column.backlog", "visible: board.column.nothing", 1)
	edited = strings.Replace(edited, "column.nothing\n", "colum.backlog1\n", 1)
	if len(edited) != len(goodSession) {
		t.Fatalf("the edit changed the size: %d, %d", len(edited), len(goodSession))
	}
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if got := run(t, env, "testids"); len(got) != 0 {
		t.Fatalf("the parse was not kept: %+v", got)
	}

	// The same bytes under a new mtime are a new version of the file.
	if err := os.Chtimes(path, old.Add(time.Minute), old.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := run(t, env, "testids"); !has(got, `"board.colum.backlog1"`) || len(got) != 1 {
		t.Errorf("an edit of the same size under a new mtime: %+v", got)
	}

	// A size change alone is an edit too, and a fresh file is never kept.
	if err := os.WriteFile(path, []byte(goodSession), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := run(t, env, "testids"); len(got) != 0 {
		t.Errorf("the session put back: %+v", got)
	}
	if err := os.WriteFile(path, []byte(edited+"# a comment\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := run(t, env, "testids"); !has(got, `"board.colum.backlog1"`) {
		t.Errorf("a second edit straight after: %+v", got)
	}
}

// TestRepoSessionsCarryListedIDs runs the check on this repository's own
// playtest/sessions: each finding is a step that would fail only in Chrome.
func TestRepoSessionsCarryListedIDs(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	files, _ := filepath.Glob(filepath.Join(root, sessionsDir, "*.yaml"))
	if len(files) == 0 {
		t.Skip("no playtest sessions in this checkout")
	}
	ids := repoTestIDs(filepath.Join(root, "internal", "testids"))
	if ids == testids.Builtin() {
		t.Fatal("this checkout's internal/testids gave no ids")
	}
	for _, f := range files {
		for _, fd := range sessionTestIDs(ids, f, filepath.Base(f)) {
			t.Errorf("%s: %s", fd.Where, fd.Message)
		}
	}
}

// TestTestIDsCheckReadsTheRepoList: the ids come from the project's
// internal/testids/*.go on disk, not from the list built into this binary,
// so a build older than the tree gives no false finding (and a build newer
// than it gives no false pass).
func TestTestIDsCheckReadsTheRepoList(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	session := `session: fresh
issue: x/y
title: Ids only the tree lists
scenario: basic
steps:
  - id: TP1
    title: An id the binary doesn't know
    checks:
      - visible: [fresh.page.title, fresh.row.42, fresh.page.titel]
      - exists: fixture.extra.marker
`
	withSessions(t, env, true, map[string]string{"fresh.yaml": session})
	ids := `package testids

func init() {
	IDs = append(IDs,
		"fresh.page.title",
		"fresh.row.#",
	)
}

var FixtureIDs = []string{"fixture.extra.marker"}

var notAnID = "fresh.page.titel"
`
	if err := os.WriteFile(filepath.Join(env.Project.Root(), "internal", "testids", "fresh.go"), []byte(ids), 0o644); err != nil {
		t.Fatal(err)
	}
	got := run(t, env, "testids")
	if len(got) != 1 || !has(got, `"fresh.page.titel"`, `did you mean "fresh.page.title"`) {
		t.Errorf("want one finding, the misspelt id with the tree's suggestion: %+v", got)
	}
	// An id the binary carries but the tree doesn't list is a finding.
	withSessions(t, env, true, map[string]string{"fresh.yaml": strings.Replace(session, "fixture.extra.marker", "board.column.backlog", 1)})
	if got := run(t, env, "testids"); !has(got, `"board.column.backlog"`) {
		t.Errorf("an id only the binary lists: %+v", got)
	}
}
