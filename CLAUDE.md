# Dark Factory (issue-viewer + issue-cli)

Dark Factory runs Claude Code and Codex agents through a process written down in `workflow.yaml`, one issue at a time. It ships two binaries: `issue-viewer`, the board (a Go web app over markdown issue files: the Workshop Board pages, list, board and docs views), and `issue-cli`, the CLI agents use to walk issues through the workflow. The repo and the Go module are `github.com/kacperwerema/dark-factory`. Config and state folders and env vars keep earlier names (`ai-workflow-engine`, `ISSUE_VIEWER_*`): see docs/getting-started.md#legacy-names, and don't rename them.

## Running

```bash
go build -o issue-viewer ./cmd/issue-viewer && ./issue-viewer -config ~/.config/ai-workflow-engine/projects.yaml
```

Flags:

- `-config` — the projects.yaml to serve (every project in it); without it the viewer serves one project from `-dir` and `-docs`
- `-dir` — issue markdown directory (default `./issues`), supports subdirectories
- `-docs` — documentation markdown directory (default `./docs`)
- `-version` — print the version and exit
- `-host` — interface to listen on (default `127.0.0.1`; anything else is an opt-in and warns at startup). Requests whose `Host` isn't loopback, `*.localhost`, the bind address or in `allowed_hosts:` (projects.yaml) get 421. Any public request (non-loopback Host, or proxy headers) gets 403 unless `identity:` is configured and its owner token verifies (docs/API/security.md)
- `-port` — HTTP port (default `8080`)
- `-owner-code` — where the owner sign-in code shows: `dialog` (macOS), `terminal`, `auto` (default: the dialog, else the terminal). Owner decisions (the O routes) need a browser signed in at `/owner`
- `-record-viewer` — write viewer.json even for a sandbox viewer (a temp-folder config, or another live viewer holds it); `=false` never writes it

## Validation

`make validate` runs its cheap steps one by one beside the full suite (each logs to a file, printed whole with a `[<step>]` lead when it ends; a red one says so at once): `make merge-check` (no MERGE_HEAD in the checkout's own git dir, no tracked line starting with a conflict marker, each named file:line; fixtures that need one go on `MERGE_CHECK_ALLOW`), `gofmt -l` (fmt-check), `go vet ./...` (vet), `make catalog-check`, `make link-check` (every relative markdown link points at a file, spelled in the exact case of its name on disk), `make leak-check` (the tracked tree's leak scan), `make codemap-check` (every package mapped in `workbench.contexts`, no new upward import; names the unmapped package and the fix), `make guard-check` (the repo-wide guard tests listed in `scripts/guard-tests.txt`: docs tables, doc numbers, every-X-is-listed checks; a new `TestEvery*`, `Test*ListedInDocs` or `TestDocNumbers*` goes on that list; the suite skips them then, `GUARD_SKIP`, and cover-cli adds their coverage, `TestValidateRunsTheGuardTestsOnce`), `make playtest-check`, and beside them the full test
suite (test; tools/test-shards runs the slowest packages' tests in several go test processes each, `TEST_SHARD_PKGS`: cmd/issue-cli in `TEST_SHARDS` (default 3), internal/viewer 3, internal/bench and internal/agentrun 2; split by the newest local CI run's times and kept while a package's tests stay the same, beside the rest) and a coverage gate on `cmd/issue-cli` (cover-cli; floor: `CLI_COVERAGE_FLOOR`,
default 70). Packages come from go test's cache when nothing they read changed: `scripts/test-cache.txt` lists every test package `cached` or `fresh` (-count=1, with why: it asks git about the checkout, reads this machine), plus `fresh-test` tests that run on their own beside the cached run; a live-board test (`liveboard.Dir`) skips in the cached run and fails there unless it has a fresh-test line (`TestEveryTestPackageIsInTheCacheList`). `TEST_CACHE=0` runs everything fresh, as local CI's slow tier does. A test writes nothing into the checkout: a folder a test walks is hashed with each entry's size and mtime, so one stray file reruns every walker. The suite writes cmd/issue-cli's profile (`-coverpkg`, `CLI_COVER_PROFILE`, in TMPDIR, merged across the shards) and cover-cli reads it instead of running those tests again; `make cover-cli` alone runs them itself (`TestValidateCoverageFloorReadsTheTestRun`). Use it before proposing a change that touches
`cmd/issue-cli/`. Every step runs even after a red one; a red step prints `validate: red so far: <step>, <step>` at once, so a worker watching the log can stop there. Its last line on stdout is `validate: PASS` or `validate: FAIL (<step>, <step>)`, naming each red step;
each step prints `validate: <step> <seconds>s` when it ends and
`validate: total <seconds>s` comes just before the verdict (local CI keeps
them as the run's `steps` in `.ci/runs/<sha>.json`, and `issue-cli ci status`
names the slowest):
read that line, not a wrapper's exit code (a backgrounded `make validate > log; echo $?`
reports the echo's 0). `VALIDATE_STEPS=` runs a subset.

It has to pass on macOS as well as Linux. `make linux-check` runs the public
CI's steps (.github/workflows/ci.yml) in an `ubuntu:24.04` container on the
working tree: apt tmux, shellcheck and curl, Go through GOTOOLCHAIN, a non-root user,
`PLAYTEST_CHROME=none`; it needs docker and refuses when `mem-guard check` is
blocked, and its last line is `linux-check: PASS` or `linux-check: FAIL (<step>)`.
`make linux-check PKG=./internal/x RUN=TestY` runs only that `go test` there
(either variable alone works) and says the run was a subset.
Run it for a change to ci.yml, lib.sh, file ordering, tmux parsing or anything
that leans on the Mac's case-insensitive disk: public CI runs only after a
release publish. `make test` says when shellcheck is missing, since go test
hides the skip of `TestLibPassesShellcheck`. On a Mac, `t.TempDir()` is under
`/var`, a link to `/private/var`: a test that compares printed paths uses the
resolved dir (`realTempDir` in cmd/issue-cli). That dir is also too deep for
a tmux socket (~104 bytes), so `isolateTmux` keeps `TMUX_TMPDIR` under `/tmp`.
A test talks to its tmux server only with `-S <own socket>` (`testTmux`), and
kills it only that way (`TestTmuxCallsInTestsNameTheirSocket`). A tmux `-F` format is built with `agentrun.Format` and read with
`agentrun.SplitFields`, never a tab: tmux prints a tab as `_` without a UTF-8
locale (`TestNoTmuxFormatUsesATab`). Every test
package's `TestMain` calls `internal/tmuxguard`, which drops `$TMUX` for the
whole binary (`TestEveryTestBinaryDropsTMUX`) and points `CODEX_HOME` and
`CLAUDE_CONFIG_DIR` at empty folders: a real agent CLI finds no login, and
what it writes there fails the run (`TestNoTestReachesARealAgentCLI`), so a
test that seems to pass can still exit 1 naming the CLI.
`CHECK_REAL_CODEX=1` gives codex its real home back
(`TestRealCLIRunsNeedTheirSwitch`). A test that starts a stand-in agent CLI in
a pane sets PATH inside the start command (`PATH=<bin>:$PATH; trap : INT;
codex go`): `tmux -e PATH` doesn't survive the pane's login shell. A stand-in
executable a test writes goes through `standin.Write` (internal/standin):
macOS checks a fresh executable on its first exec, now and then for over
10 s, and caches the result per file, so the helper keeps each distinct body
once in a cache folder named by its sha256, runs it once there and hard-links
it in. What differs between tests (a fixture, a log path) comes from the
environment, so a stand-in keeps one body.
`TestAdoptedTestsWriteStandInsThroughTheHelper` holds every `_test.go` in
the module to it; a mode that is no stand-in says `not a stand-in: <why>`.
BSD `script` has no `-c` (internal/checks `ptyCommand`), and tests that
dispatch use `terminal: none`; an unset terminal is i3-msg where it is on
PATH and headless elsewhere (`agentrun.TerminalNone`).

It also has to pass on a loaded machine: CI runs while several workers run
`make validate`. A timing test keeps its absolute limit for a quiet machine
and, when the machine is slow, compares against a cheaper request timed in
the same run (`TestStatsPageFastOnABigProject`): it times the two in
turns over several rounds and compares the best of each, since one sample
can land in a burst of load (`statusTooSlow`). A test waits for a file or
pane with a retry loop, never one fixed sleep, and gives a binary it built
in the same run (go build, then start a viewer) at least 60 s to answer:
its first exec can wait on macOS's check for over 10 s. Code that dates two records
that must agree reads the clock once. A date rule counts calendar days, not
24-hour spans (a DST day is 23 or 25 hours; a "last N days" window starts at `period.DaysBack`, `TestOneDayWindow`), and its fixtures pin `now` instead of reading the clock. A stand-in that writes a record a test
polls for writes it aside and renames it into place: sh line-buffers printf,
so a record written in place can be read half done. Every git a test
binary starts runs with `maintenance.auto` and `gc.auto` off (git detaches
a maintenance run after commit, merge and fetch that can still be writing
`.git/objects` at cleanup): tmuxguard adds them as `GIT_CONFIG_*` entries,
so a helper that sets `cmd.Env` starts from `os.Environ()`
(`TestChildGitRunsQuiet`). A test that reads the live board (the real issues,
not a fixture) gets its folder from `liveboard.Dir(t)` (internal/liveboard):
the primary checkout's `issues/` beside the git common dir, since a worktree
has none, and a skip on a fresh clone with no board; `LIVE_BOARD=<dir>`
names another. A stand-in clock that ticks on every `now()`
must not be read by a poll loop whose tick count depends on speed. To check a test under
load, `scripts/under-load.sh [-c cpu] [-m mb] -- <command>` runs it beside
a CPU and memory hog it starts and stops itself (refuses when `mem-guard
check` is blocked, stops at 70%).

Shell: the worker briefing's `## Shell on this machine` section says what
applies on yours (zsh, macOS BSD tools, rtk), detected from `$SHELL`, the OS
and `rtk` on PATH; the same notes are in docs/getting-started.md#shell-notes.

UI checks run as playtest sessions:

- `make ux-test [ISSUE=<slug>] [S=<session>] [REPEAT=2]` runs them in headless
  Chrome against a throwaway scenario board. `S=` runs that session even when
  it belongs to another issue than the branch's; that run keeps its shots and
  goes to the project root's `.playtest/runs`, where its issue's Evidence tab
  reads it.
- `make ux-test ALL=1` runs every session, on any branch. Local CI runs it
  every night (`ci.nightly`: the slow tier after 03:00 when mem-guard allows;
  `issue-cli ci status --tier slow` names each red `<session>/<step>`).
  Console `no_errors` (the default) also catches CSP violations.
- `make shot URL=<page> [W=390] [FULL=1] [SCROLL=<sel>] [OUT=…]` takes one
  screenshot of one page (headless, waits for load, not network idle);
  FULL=1 on a page that only scrolls inside (the Workshop floor) unrolls its
  biggest scroller, SCROLL= names it. `URL=/p/…` shoots the board
  `make serve` runs; `BASE=main` shoots its before board too (OUT and
  OUT.base), `HTML=1` saves both pages' HTML for a diff.
- `make serve SCENARIO=<name> [BASE=main]` leaves an isolated scenario
  board running (and one built from BASE beside it), prints the URLs;
  `make serve-stop [SCENARIO=<name>]` stops them. Use it instead of a
  hand-started sandbox viewer.
- The playtest guides' one source is docs/patterns/playtest; issue-cli embeds a generated copy: after editing them run `go generate ./cmd/issue-cli/...` (TestPlaytestGuidesInSync).
- `make playtest ISSUE=<slug>` shows the owner the judgement steps in an
  overlay.
- `make playtest-report ISSUE=<slug> [APPLY=1]` ticks the boxes that passed.

See [docs/patterns/playtest/web-app.md](docs/patterns/playtest/web-app.md).

## Project Structure

- `cmd/issue-viewer/` — the board's thin main; the viewer itself is package viewer in `internal/viewer/` (templates/ and static/ embedded beside it)
- `cmd/issue-cli/` — the CLI agents walk issues with; bundled templates under `cmd/issue-cli/templates/`
- `internal/<pkg>/` — one package per concern (tracker, agentrun, briefing, memory, ci, …); `cmd/playtest/` the playtest runner, `playtest/` its sessions and scenarios, `scripts/` shell helpers

What each file and package holds is in [docs/code-structure.md](docs/code-structure.md). `issue-cli codemap` and `issue-cli affected <slug>` read packages, contexts and a change's footprint from the code.

## Issue File Format

Issues are markdown files with YAML frontmatter stored in `./issues/` or `./issues/<System>/` subdirectories.

```markdown
---
title: "Issue title"
status: "in progress"
system: "Combat"
version: "0.1"
labels:
  - bug
  - enhancement
priority: "high"
created: "2025-01-15"
number: 42
repo: "owner/repo"
---

Markdown body here. Supports `[x]` checkboxes.
```

### Required fields

- `title` — issue title

### Optional fields

- `status` — one of the statuses in the project's `workflow.yaml` (the bundled `development` template: `idea`, `in design`, `backlog`, `in progress`, `testing`, `human-testing`, `documentation`, `shipping`, `done`)
- `system` — categorization tag (also used as subdirectory name by sync script)
- `version` — version string, filterable on the board view
- `labels` — list of label strings
- `priority` — one of: `low`, `medium`, `high`, `critical`
- `created` — date string for sorting (newest first)
- `number` — GitHub issue number (used in board card display as `#number`)
- `repo` — GitHub repo in `owner/repo` format
- `parent` — slug of the epic this issue belongs to (`umbrella` is a legacy alias). Old `base`/`lead` fields on epics are plain custom fields; nothing reads or writes them
- `type` — type of work when `workflow.yaml` defines `types:` (picks the issue's path; see docs/Workflow/types.md)

### Custom fields

Any other frontmatter key is preserved as a custom field and displayed in the detail view sidebar. String values that start with `http://` or `https://` render as clickable links. List values render as bullet lists. Example:

```yaml
pr: "https://github.com/org/repo/pull/456"   # renders as a link
pr_author: "jsmith"                            # renders as text
participants:                                  # renders as a list
  - alice
  - bob
```

### Filename convention

- For GitHub issues: `<number>.md` (e.g., `42.md`)
- For draft issues: slugified title (e.g., `my-feature-idea.md`)
- The slug used in URLs (`/issue/<slug>`) comes from the title (with the system folder in front), not the filename; a `slug:` field pins it. `issue-cli update --title` keeps the slug by writing that pin

## Doc Page Format

Documentation pages are markdown files in the docs directory.

```markdown
---
title: "Page Title"
order: 1
---

Page content in markdown.
```

- `title` — defaults to titlecased filename if omitted
- `order` — numeric sort order (lower first), pages with same order sort alphabetically
- Frontmatter is optional; a plain markdown file works too

## Documentation

Detailed documentation lives in `docs/` and is viewable at `/docs` in the web UI:

- [Why a Workflow Harness](docs/why.md) — the argument behind the tool
- [Roadmap](docs/roadmap.md) — goals, epics, phase order; cutting a release
- [Getting Started](docs/getting-started.md) — install, flags, first issue, phone notifications
- [Issue File Format](docs/issue-format.md) — frontmatter, custom fields, files
- [Workflow](docs/workflow.md) — lifecycle, clarify, local CI, fresh sessions, red rounds, rules, decisions, privacy, post-mortem
- [Agent Workflow Flow](docs/agent-workflow-flow.md) — dispatch to done
- [Board Configuration](docs/board-configuration.md) — columns, card fields
- [Agent Dispatch](docs/agent-dispatch.md) — starting work, leads, auto-start, the queue, routing between Claude and Codex
- [GitHub Integration](docs/github-integration.md) — sync, references, auto-close
- [Per-issue Data Store](docs/data-store.md) — sidecar JSON, `issue-cli data`
- [Workbench Files](docs/workbench-files.md) — metrics, code map, project pages, health snapshots
- [Departments](docs/departments.md) — company, departments, goals, outputs, leads, connections, jobs, partners; Settled rules
- [SEO Baseline](docs/seo-baseline.md) — the owner's SEO walk-through
- [Shared Memory](docs/memory.md) — `issue-cli memory`, scopes, the update step
- [Factory Benchmark](docs/benchmark.md) — golden tasks, runs, the nightly loop, evals
- [Workflow Stats](docs/workflow-stats.md) — experiments, the Stats page, Timeline, connectors
- [The Lead Session](docs/patterns/lead-session.md) — the project lead
- [Guided Check Scripts](docs/patterns/guided-check-scripts.md) — shell checks on the issue branch
- [Playtest Sessions](docs/patterns/playtest/README.md) — an issue's manual checks as YAML sessions
- [API](docs/API/overview.md) — endpoints, handlers
- [Security](docs/API/security.md) — threat model, route inventory, `make security-check`
- [CLI](docs/CLI/overview.md) — commands, output contracts
- [CLI Usage Telemetry](docs/CLI/telemetry.md) — usage log, sharing
- [UI](docs/UI/overview.md) — templates, views, issue page tabs
- [Workshop Board](docs/UI/workshop-board.md) — the new look's rules, `wb/*` catalog
- [Workflow engine](docs/Workflow/overview.md) — transitions, overlays, roles, feature catalog
- [Proof Links](docs/Workflow/proof.md) — criteria linked to proof
- [Types of Work](docs/Workflow/types.md) — per-type paths, N/A forms, the report type
- [The Fresh Critic](docs/Workflow/critique.md) — `issue-cli critique`
- [YOLO Mode](docs/Workflow/yolo.md) — gates reviewed after the fact
- [Code Structure](docs/code-structure.md) — what each file and package holds
- [Documentation Map](docs/doc-map.md) — every page above with what it covers in full

## Sessions

`sessions/` holds lead-session handoff notes, one file per handoff (`YYYY-MM-DD-lead-<project>.md`, with a `.summary.md` of the day's chat when the nightly flush wrote it; docs/patterns/lead-session.md#the-nightly-flush). Each note records running agents, issue and branch state, owner decisions and rules, and how to resume after a restart. When resuming lead work, read the newest file first. Before a shutdown or a long break, write a new one.

## Adding New Statuses

Status colors are defined in the `statusColor` template function in `template_funcs.go` `funcMap`. Board column order and descriptions come from `workflow.yaml` statuses.
