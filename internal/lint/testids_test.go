package lint

import (
	"os"
	"path/filepath"
	"testing"
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
      - post: { path: /p/{project}/x, json: { target: not.an.id, a: b } }
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

// TestRepoSessionsCarryListedIDs runs the check on this repository's own
// playtest/sessions: each finding is a step that would fail only in Chrome.
func TestRepoSessionsCarryListedIDs(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	files, _ := filepath.Glob(filepath.Join(root, sessionsDir, "*.yaml"))
	if len(files) == 0 {
		t.Skip("no playtest sessions in this checkout")
	}
	for _, f := range files {
		for _, fd := range sessionTestIDs(f, filepath.Base(f)) {
			t.Errorf("%s: %s", fd.Where, fd.Message)
		}
	}
}
