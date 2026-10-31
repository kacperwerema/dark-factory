---
title: "Playtest: a web app (chromedp)"
order: 21
---

# Playtest example: a web app driven by headless Chrome

This is how issue-viewer playtests its own board. Copy the parts your web app
needs. The shared shape is in `issue-cli process playtest`.

## Layout

```
internal/testids/<page>.go      every data-testid id and pattern, one file per page
templates/*.html                data-testid="board.card.{{tid .Slug}}" on what steps point at
testids_test.go                 fails when templates and the list drift apart
playtest/sessions/<name>.yaml   sessions (step id = checkbox id)
playtest/scenarios/<name>/      scenario.yaml, workflow.yaml, tracker/<System>/*.md fixtures, optional root/
cmd/playtest/                   the runner: check | ux-test | playtest | report
cmd/playtest/assets/checks.js   DOM checks, layout rules, action helpers (both modes)
cmd/playtest/assets/overlay.js  the owner's overlay (injected; the board has none)
.playtest/runs/                 gitignored: results.yaml, logs/, shots/
```

The make targets:

- `make playtest-check`, which is part of `make validate`
- `make ux-test [ISSUE=…] [S=…] [STEP=TP2,TP3] [REPEAT=2]`
- `make playtest [ISSUE=…]`
- `make playtest-report ISSUE=… [APPLY=1]`

In a dispatched worktree (branch `work/<slug>`) `ISSUE` defaults to `<slug>`, so a plain `make playtest` or `make playtest-report APPLY=1` works on that issue. On any other branch it stays empty, and passing `ISSUE=` runs every session. `S=` narrows within that issue, so to run another issue's session from a worktree pass both: `make ux-test ISSUE= S=local-ci`.

## Test ids

- An id is `page.area.element`, lower snake case with dots between levels.
  In the list, `*` stands for any one segment (a status, a slug) and `#` for
  an index.
- Templates build dynamic segments with `tid`, which lower-cases and turns
  spaces and dots into `_`:
  `data-testid="board.column.{{tid .Status}}"` gives
  `board.column.in_progress`.
- Ids set from JS take two forms that the test can read:
  - `el.dataset.testid = 'detail.body.checkbox.' + i`
  - a literal `data-testid="detail.comment.eye.' + idx + '"` inside an HTML
    string.
- Every id goes in its page's file under `internal/testids/` (`sidebar.go`,
  `workboard.go`, ...), which appends to `testids.IDs`. `TestTemplateTestIDs`
  fails on an unlisted template id or an unused entry. `playtest check`
  rejects a session target that matches no entry, and suggests the closest.
- Label containers too (`nav.sidebar`, `board.filters`). A `layout_ignore`
  on a container waives everything inside it.

## Scenarios: isolation lives in the runner

Each step at each viewport starts a fresh scenario:

- **Its own project:** a temp dir under `/tmp/pt-…`, with a marker file in
  it. The fixtures are copied into `proj/issues/` and committed to a new git
  repo. Fixtures live under `tracker/`, never under `issues/`, so they never
  read as a real issues tree.
- **Files beside the issues:** with `root: <folder>` in `scenario.yaml`,
  that folder is copied into the project root as is: `sessions/` handoff
  notes, a `CHANGELOG.md`, `playtest/sessions/` and `.playtest/runs/`
  (the `status` scenario uses it).
- **Its own config:** a `projects.yaml` that points only at that project.
  `project: { extra: "…" }` in `scenario.yaml` adds raw YAML to that
  project's entry (`company:`, `departments:`; the `departments` scenario
  uses it).
- **Fresh binaries:** `issue-viewer` and `issue-cli` built from the
  checkout, once per run, and put first on `PATH`.
- **Its own board:** on a free port.
- **Its own environment:**
  - `HOME` and the XDG dirs point into the temp dir, so the scenario
    viewer's `viewer.json` never replaces the real one.
  - `ISSUE_VIEWER_CONFIG` is the scenario's config.
  - `TMUX` is unset, and `TMUX_TMPDIR` is a private dir that also holds a
    marker. tmux calls refuse to run without the marker, and teardown's
    `kill-server` only runs when it is there.
  - Agent variables (`CLAUDECODE`, `AGENT_NAME`, …) are dropped.
- **A pinned order:** the board sorts issues by file mtime, so each fixture's
  mtime is set from its `created:` date.

Slugs come from the issue title (`card-titles-overflow-on-a-phone`), with
the system folder lower-cased (`ui/…`), not from the file name.

### Generated issues

A scenario that needs volume (hundreds of issues) lists them under
`generate:` in `scenario.yaml` instead of committing one fixture per issue.
The runner writes them into `proj/issues/` next to the fixtures, before it
pins the times:

```yaml
generate:
  - { count: 300, dir: Sim, title: "Sim idea %03d", status: idea, created: "2026-08-01" }
  - { count: 6, dir: Sim, title: "Revise %02d", status: backlog, extra: "labels:\n  - needs-revision" }
  - { count: 25, dir: Sim, title: "Build %02d", status: in progress, body: "## Plan\n- [ ] Build it" }
```

- `title` is a fmt pattern with one `%d`, numbered from 1. The slug comes
  from it as usual (`sim/sim-idea-001`).
- `dir` is both the folder under `issues/` and the `system`. Leave it out
  for issues at the root.
- `created` pins the mtime like a fixture's `created:`.
- `extra` is raw frontmatter added to each issue, and `body` is the body.
- A bad entry (no count, no status, or a title without one `%`) fails
  `make playtest-check`.
- **Example:** `playtest/scenarios/workboard-scale/` (~520 issues for the
  workboard).

### Claude Code transcripts (spend)

A view that reads spend (`internal/spend`) needs Claude Code sessions. List
them under `transcripts:`; the runner writes each as a session file under
the scenario's home (`~/.claude/projects/<cwd slug>/`), numbers only.
`CLAUDE_CONFIG_DIR` is dropped from the scenario's environment, so the real
transcripts never show.

```yaml
transcripts:
  - cwd: .worktrees/seo/landing-page          # an issue's worktree; "" is the project root
    messages:
      - {model: claude-test, input: 20000, output: 9000, cache_read: 1200000}
      - {ago: -7d, model: claude-test, input: 10000}   # last week
  - agent: lead-acme-marketing                 # a named session in the project root
    messages:
      - {model: claude-test, input: 2000}
```

- **Example:** `playtest/scenarios/spend/`.

### Git history and branches

A scenario that needs git history or open branches (a view over branches, knob history) adds these keys to `scenario.yaml`, next to `root:`. Each `files:` names a folder in the scenario:

```yaml
root: files                   # copied into the project root, in the first commit
commits:                      # applied on main afterwards, in order
  - files: commits/pass1
    message: "Play calls balance pass 1"
branches:
  - name: work/sim/keep-redesign
    worktree: true            # .worktrees/work-sim-keep-redesign
    commits:
      - files: branches/keep
        message: "Keep: outer ring"
    uncommitted: branches/keep-run   # overlaid in the worktree, not committed
  - name: work/economy/shop-prices   # no worktree: committed on the branch only
    commits:
      - files: branches/shop
```

- **Fixed commit dates:** scenario commits get fixed dates, an hour apart from 2026-10-01 12:00 UTC, so shas and dates match between runs and `REPEAT=2` passes.
- **Worktrees** are excluded from the project's own commits.
- **Upstream:** `upstream: <branch>` makes main track that branch (`git branch -u`), so views that compare main with its upstream have one. Create the branch with `from: main~N` to leave main N commits ahead. Example: `playtest/scenarios/release/`.
- **Tags:** `tags: [{name: v1.5.0-rc.1, ref: main~1}]` makes annotated tags after the commits (`ref` defaults to main), dated like the commits. **Repo:** `project.repo:` goes into the scenario's projects.yaml; the engine's repo (`michal-franc/ai-workflow-engine`) makes the board treat the project as the engine. Example: `playtest/scenarios/release-rc/` (a release candidate soaking).
- **Example:** `playtest/scenarios/metrics/` (the Metrics view).

## The runner (cmd/playtest)

- **Browser:** chromedp. It drives the installed Chrome or Chromium, else
  the newest Chrome for Testing under `~/Library/Caches/ms-playwright`
  (a Mac without Chrome in /Applications); set `PLAYTEST_CHROME` for another. ux-test runs headless, with a new browser
  context per step and viewport, so storage and cookies start empty.
- **Injected scripts:** `checks.js` is added to every document with
  `Page.addScriptToEvaluateOnNewDocument`. A second script turns off CSS
  animations and transitions.
- **Waiting:**
  - Actions wait up to 5s for their target to exist and be visible,
    enabled and uncovered (the runner hit-tests the target's centre).
  - Checks settle: they are evaluated again until they pass or
    `settle_ms` runs out (default 3000).
  - So a navigation or a fetch never needs a sleep.
- **Viewports:** `session_checks.viewports`, by default `1440x900` and
  `390x844`.
- **Reduced motion:** `session_checks.reduced_motion: true` makes the
  browser report `prefers-reduced-motion: reduce` (Emulation.setEmulatedMedia)
  for every step of the session, to check a page's still version. SVG
  animations (`animateMotion`) keep running otherwise: the injected script
  only stops CSS ones.
- **What fails a step:**
  - **Console:** an error or exception in the console, unless
    `console_ignore` matches it.
  - **Layout rules:**
    - `inside_viewport`: no sideways page scroll. When the page does scroll
      sideways, the error names the element that causes it.
    - `no_page_scroll`: the page scrolls neither way: the document is no
      taller or wider than the viewport, and no shown id outside a panel
      that scrolls (overflow auto or scroll) ends past the viewport, so a
      page that hides its overflow can't cut a control off. For a screen
      that must fit its view, like the company floor.
    - `no_clipping`: text fits its box.
    - `no_overlap`: sibling ids don't overlap.
    - `no_zero_size_text`: no text in a 0-wide or 0-high box.
    - `styles_applied`: every class on a visible test id element is
      matched by a style rule that is active at this viewport: a top-level
      rule, or one inside an `@media` that matches or an `@supports` that
      holds. A class counts when the element matches the rule's selector
      up to the class's compound, so `.ctx .title` styles a `.ctx` that
      wraps a title, while `.row.dim .x` doesn't style a plain `.row`.
      Pseudo-classes are ignored (`.btn:hover` styles `.btn`). A class only
      JS reads gets a `js-` prefix; otherwise add the rule or waive the id
      with `layout_ignore`. The error reads `class "status-epic" has no CSS
      rule active at 1440x900`. The first 25 are listed, then a count.
      It caught v0.48.0: a lost `}` put the rest of `style.css` inside a
      `max-width: 900px` block, the desktop pages rendered unstyled, and
      every content check and the four rules above still passed.
- **On failure:** the log holds the actions, the processes' output, and
  every test id on the page with its rect, visible and enabled state and
  text.
- **Determinism:** `REPEAT=2` compares statuses, check results, layout and
  console output across runs (not pixels) and prints the first difference.

### Actions

| Action | Example |
|:--|:--|
| `open` | `open: /p/{project}/board` (`{project}`, `{base}`, `{dir}` expand) |
| `reload` | `reload: true` |
| `click` / `hover` | `click: board.transition_modal.confirm`. A real mouse event at the centre; refused when the target is hidden, disabled or covered. |
| `type` | `type: { target: list.search, text: csv, enter: true }` (clears first unless `clear: false`) |
| `select` | `select: { target: list.filter.status, value: backlog }` |
| `key` | `key: Escape` (also `"1"`–`"4"`, `f`, `+`, `-`: keys a page binds itself, and `Control+1`, `Control+f`, `Meta+f` to check it leaves the browser's own alone) |
| `drag` | `drag: { target: board.card.ui/x, to: board.column.backlog.cards }`. A real mouse drag (native HTML5 drag and drop) when both ends are on screen, else synthetic drag events. |
| `wait_for` | `wait_for: board.toast`, `wait_for: { hidden: board.transition_modal, ms: 8000 }`, `wait_for: { url: { contains: status=backlog } }` |
| `wait` | `wait: 500` (ms; prefer `wait_for`) |
| `post` | `post: { path: /p/{project}/issue/x/approve, json: {status: done}, status: 200 }`. `headers: {Origin: "null"}` sends extra headers. |
| `write` | `write: { path: docs/status/index.html, text: "<p>v2</p>" }`. Replaces a file under the scenario's project root (a page an agent rewrites). |
| `upload` | `upload: { target: lead_chat.image_input, path: files/shot.png }`. Picks a file under the scenario's project root in a file input, as the file dialog does (its change event fires; the input may be hidden). |
| `cli` | `cli: [transition, cli/x, --to, backlog]` or `{ args: […], exit: 1 }`. Runs the scenario's issue-cli. |
| `tmux` | `tmux: [new-session, -d, -s, lead]`. Runs on the private server. |
| `snapshot` | `snapshot: { name: before, target: board.column.backlog.count }` |
| `check` | `check: { count: { pattern: list.row.*, equals: 2 } }`. An inline check that must pass now. |

### Checks

| Check | Passes when |
|:--|:--|
| `exists` / `absent` | in the DOM / missing or hidden |
| `visible` / `hidden` | shown with a size / present but not shown |
| `enabled` / `disabled` | shown and not disabled / disabled |
| `selected` | checked, selected, aria-selected/pressed, or class `active`/`selected`/`approved` |
| `text_equals` / `text_contains` | `{ target, text }` on whitespace-normalised text (an input's value) |
| `value_equals` | `{ target, value }` on a form control |
| `count` | `{ pattern, within?, equals\|min\|max }` visible matches |
| `url` | `{ equals\|contains\|matches }` on path and query |
| `changed` / `unchanged` | `{ target, since: <snapshot> }` |
| `issue` | `{ slug, field, equals }`, `{ slug, checkbox: P1, checked: true }`, `{ slug, comment_contains }`, `{ slug, body_contains }`. Reads the scenario's issue file. |
| `cli` | `{ args, exit?, contains?, not_contains? }` |
| `tmux` | `{ session, present }` or `{ pane, contains }` on the private server |
| `http` | `{ path, status?, contains?, json: { "a.0.b": value } }` |
| `beside` | `[a, b]` or `{ a, b, gap_max? }`: b starts right of a's right edge and their vertical extents overlap (one row); `gap_max` caps the gap in px |
| `below` | `[a, b]` or `{ a, b }`: b starts below a's bottom edge |
| `inside` | `{ target, within }`: target's box lies wholly inside within's (±1px), as it is now: nothing is scrolled into view first |
| `css` | `{ target, property, equals \| in: [..] \| not }` on the computed value, e.g. `{ target: board.column.backlog, property: display, equals: flex }` |
| `size` | `{ target, min_width?, max_width?, min_height?, max_height? }` in CSS px, from the element's box |

Content checks pass on an unstyled page that stacks top to bottom, and so do
the layout rules. Give each view a few shape checks for its main
structure: the sidebar `beside` the page, columns `beside` each other,
sections `below` the header, a progress bar with a `size`. A failure shows
both boxes (`x 260..976 y 130..388`) or the measured value.

A list such as `visible: [a, b]` becomes one check per item.

## The overlay (make playtest)

- **Window:** the runner opens a visible Chrome window that it drives.
- **Bridge:** `chromedp.ExposeFunc` adds `window.__ptSend`, the overlay's
  line to the runner (`{type:'hello'}` returns the state). The overlay
  script is injected like `checks.js`, so the board has no playtest code.
- **Each judgement step:**
  1. The runner starts a fresh scenario and replays `from` and `setup` in
     the window. A badge shows meanwhile; it doesn't take clicks.
  2. The dock shows DO / EXPECT / WHY, the step list (YOU or AUTO, each with
     its last ux-test result), and the DOM checks evaluated live. Runner
     checks (`issue`, `cli`, …) run when the owner passes the step.
  3. Targets pulse, with numbered chips. Arrows point at targets off screen.
     Hovering a target shows its note, EXPECT and WHY.
- **Keys:**
  - Alt+P passes the step.
  - Alt+F fails it. It asks for a note and saves a screenshot without the
    overlay.
  - Alt+S skips, and Alt+R replays from a fresh scenario.
  - Alt+N records an opinion on the element under the mouse.
  - F10 hides the dock, and Alt+Q quits.
- **Results:** the playtest reuses the last ux-test of the same build, or
  runs one first. It writes a `-judge` run.
- **Several sessions** (an issue with two sessions):
  - When every judgement step of a session has a verdict, the next session
    starts in its own tab. The previous tab is blanked.
  - After the last session, Alt+Q (or closing the window) finishes.
  - Closing the window part-way stops the run, and names the sessions
    still to judge.

## Report

- `make playtest-report ISSUE=…` names the project it reports to
  (`PLAYTEST_PROJECT`, else the worker's `ISSUE_VIEWER_PROJECT`, else the
  configured project whose issues dir is this repo's) and lists the runs it
  read and their builds. It warns about runs from an older build, then
  prints the boxes to tick, the comments to post, and the steps with no
  result.
- `APPLY=1` calls `issue-cli --project <project> check …` and
  `comment … --text "playtest: …"`, then reads the checklist back: a box it
  meant to tick that is still open is named, and the run fails. It records
  posted comments in the run's `reported.yaml`, so a second APPLY doesn't
  repeat them.

## One screenshot (make shot)

`make shot URL=<page> [W=390] [H=844] [FULL=1] [SCROLL=<selector>] [OUT=file.png]`
runs `playtest shot`: the same browser, one page, one PNG (default 1280x800
into `.playtest/shot.png`; `FULL=1` takes the whole page). It waits for the
load event and the body, then 300 ms, never for the network to go idle, so
Workshop pages that poll don't hang it. Workshop pages are one screen tall
and scroll inside (the company floor `.wb-map`, `.wb-scroll`), so on a page
that doesn't scroll itself `FULL=1` unrolls the inner scroller hiding the
most: it and its ancestors drop their height cap and clip, the page gets a
resize (the roads redraw), then the full shot. `SCROLL=<selector>` unrolls
that element instead (and implies `FULL=1`); a selector that matches nothing
fails the shot. Point it at a throwaway board (the
demo staged by `scripts/readme-shots.sh --serve`, or a scenario), not the
owner's viewer.

## Picking sessions (ISSUE= and S=)

On a work branch `ISSUE` defaults to the branch's slug. `S=<session>` drops
that default and runs the named session whoever's issue it is. With both
`ISSUE=` and `S=` given and no match, the error names the issue filter and,
when the session belongs to another issue, that issue. A named session keeps its shots, and when it is another issue's its run goes
to the project root's `.playtest/runs/`, where that issue's Evidence tab
reads it (see the shared contract's "A session asked for by name").

## README screenshots (playtest shots)

`make readme-shots` reuses the runner's browser for the README's pictures.
`scripts/readme-shots.sh` copies `demo/` to a temp folder, points `HOME`,
`XDG_STATE_HOME` and `CLAUDE_CONFIG_DIR` into it, starts the CEO
(`lead-demo`, a fixture transcript and pane from `demo/shots/`) and three
workers on a private tmux socket, and starts the viewer on a free port.
`playtest shots -list docs/images/shots.yaml -base URL -out docs/images`
then shoots each page at 1280 px, 2x, in the light theme. `-only a.png,b.png`
retakes some. Teardown kills only that socket's server and that viewer.
Look at every picture before committing: the repo is public.

## Pitfalls

Each of these has cost a worker a full ux-test run to find. One line and one
example each.

- **REPEAT=2 compares each check's text, so check stable values only.** Ids,
  shas, times and ports differ in run 2 (scenario commits have fixed dates,
  so their shas hold). Check the words and counts around them:
  `text_contains: { target: company.room.dept, text: "📦 2 to review" }`,
  not the random output id the shelf also shows.
- **`cli:` runs issue-cli in the scenario folder, which names the project
  `proj`.** Per-project commands need the board's slug:
  `cli: [--project, "{project}", autostart, status]`.
- **playtest-report finds the board and defaults to the branch's slug.** The
  project is `PLAYTEST_PROJECT`, else a dispatched worker's
  `ISSUE_VIEWER_PROJECT`, else the configured project whose issues dir is this
  repo's (the report prints it on its `project:` line). After APPLY it reads
  the checklist back and fails naming any box still unticked. On another
  board pass the project, and the issue when the branch isn't `work/<slug>`
  (a session that lives on a sibling issue's branch):
  `PLAYTEST_PROJECT=dark-factory make playtest-report ISSUE=factory/yolo-mode`.
- **The first health snapshot lands seconds after the board starts.** A step
  that reads snapshot data (Stats charts like Disk use, the Health score
  components, the Status Health card's lint line) waits for it first:
  `wait_for: { http: { path: "/p/{project}/stats?range=all", contains: 'data-testid="stats.chart.storage"' }, ms: 30000 }`.
- **A tmux window started from a button ends with "Press Enter to close this
  window."** Wait for that line (the window exists before its command is
  done), press Enter, then check the page:
  `wait_for: { tmux: { pane: playtest-ui-play-due, contains: "Press Enter to close" }, ms: 15000 }`
  then `tmux: [send-keys, -t, playtest-ui-play-due, Enter]`. The whole step
  is `playtest/sessions/example-tmux-window.yaml`.
- **go test doesn't run the sessions' `contains:` checks, so a rename misses
  them.** After changing words the owner sees, search the sessions and run
  the ones that match:
  `/usr/bin/grep -rln "Lead" playtest/sessions` then
  `make ux-test ISSUE= S=<session>`.
- **A step's shot is its last page.** To show two pages, split the step and
  chain the second with `from: <first step's id>`.
- **playtest-report reads runs by build.** It marks a run from before your last
  merge of main "not the current build", so run the final `REPEAT=2` after
  that merge.

## Gotchas found while building it

- HTML5 drag and drop doesn't start from plain CDP mouse events. Use
  `chromedp.DragAndDropXY` (it intercepts the drag) or synthetic
  `DragEvent`s.
- Elements that only show on hover (the comment eye) need a `hover` on their
  block first.
- A popover that closes after submit can't be checked by its text. Check the
  state it leaves behind (a count, the file).
- A class that only marks state for JS (`just-dropped`) fails
  `styles_applied`; name it `js-just-dropped`. A marker class nothing reads
  is dead: drop it.
- Shape checks run at every viewport of the session. A check that holds
  only on desktop (two columns) goes in a desktop session, not one that
  also runs at phone width.
- Waive a known layout bug with `layout_ignore` on its container and a
  comment naming the bug; remove the waiver when it is fixed. A pan surface
  (`canvas.world`) is waived for good: it is meant to run past the screen.
- A page the Workshop Board replaced keeps its classic version at
  `?classic=1` (issue page, Status, Decisions, Docs, Systems, Health). A
  step that checks the classic page or its sidebar opens that URL, and an
  `http:` check on it carries the same query. When a sidebar tab now leads
  to a Workshop page, the step checks the URL after the click, then opens the
  classic page for its checks.
- On macOS the scenario lives under `/var`, a link to `/private/var`, and
  tmux can report a session's folder resolved. Code that matches paths
  (agentrun.ForProject) has to accept both forms.
