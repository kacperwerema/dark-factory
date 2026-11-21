---
title: "Validation and Test Rules"
order: 89
---

What `make validate` runs, and the rules a test in this repo follows: on macOS and Linux, on a loaded machine, with tmux, stand-in agent CLIs and git kept to the test. CLAUDE.md keeps the short form and links here.

## make validate

`make validate` runs its cheap steps one by one beside the full suite (each logs to a file, printed whole with a `[<step>]` lead when it ends; a red one says so at once): `make merge-check` (no MERGE_HEAD in the checkout's own git dir, no tracked line starting with a conflict marker, each named file:line; fixtures that need one go on `MERGE_CHECK_ALLOW`), `gofmt -l` (fmt-check), `go vet ./...` (vet), `make catalog-check`, `make link-check` (every relative markdown link points at a file, spelled in the exact case of its name on disk), `make leak-check` (the tracked tree's leak scan), `make codemap-check` (every package mapped in `workbench.contexts`, no new upward import; names the unmapped package and the fix), `make guard-check` (the repo-wide guard tests listed in `scripts/guard-tests.txt`: docs tables, doc numbers, every-X-is-listed checks; a new `TestEvery*`, `Test*ListedInDocs` or `TestDocNumbers*` goes on that list; the suite skips them then, `GUARD_SKIP`, and cover-cli adds their coverage, `TestValidateRunsTheGuardTestsOnce`), `make playtest-check`, and beside them the full test
suite (test; tools/test-shards runs the slowest packages' tests in several go test processes each, `TEST_SHARD_PKGS`: cmd/issue-cli in `TEST_SHARDS` (default 4), internal/viewer and internal/bench 3, internal/agentrun 2; split by the newest local CI run's times and kept while a package's tests stay the same, beside the rest) and a coverage gate on `cmd/issue-cli` (cover-cli; floor: `CLI_COVERAGE_FLOOR`,
default 70). Packages come from go test's cache when nothing they read changed: `scripts/test-cache.txt` lists every test package `cached` or `fresh` (-count=1, with why: it asks git about the checkout, reads this machine), plus `fresh-test` tests that run on their own beside the cached run; a live-board test (`liveboard.Dir`) skips in the cached run and fails there unless it has a fresh-test line (`TestEveryTestPackageIsInTheCacheList`). `TEST_CACHE=0` runs everything fresh, as local CI's slow tier does. A test writes nothing into the checkout: a folder a test walks is hashed with each entry's size and mtime, so one stray file reruns every walker. The suite writes cmd/issue-cli's profile (`-coverpkg`, `CLI_COVER_PROFILE`, in TMPDIR, merged across the shards) and cover-cli reads it instead of running those tests again; `make cover-cli` alone runs them itself (`TestValidateCoverageFloorReadsTheTestRun`). Use it before proposing a change that touches
`cmd/issue-cli/`. Every step runs even after a red one; a red step prints `validate: red so far: <step>, <step>` at once, so a worker watching the log can stop there. Its last line on stdout is `validate: PASS` or `validate: FAIL (<step>, <step>)`, naming each red step;
each step prints `validate: <step> <seconds>s` when it ends and
`validate: total <seconds>s` comes just before the verdict (local CI keeps
them as the run's `steps` in `.ci/runs/<sha>.json`, and `issue-cli ci status`
names the slowest):
read that line, not a wrapper's exit code (a backgrounded `make validate > log; echo $?`
reports the echo's 0). `VALIDATE_STEPS=` runs a subset.

## macOS and Linux

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
test that seems to pass can still exit 1 naming the CLI. It also points
`TMPDIR` at a private `$TMPDIR/dft-*` folder for the binary's run and removes
it at exit: anything a test left in it (an `os.MkdirTemp("", …)` nobody
removed, a package-level helper's folder) fails the binary with
`tmuxguard: <pkg>.test left <name> in TMPDIR`
(`TestTestBinaryLeavingATempEntryFails`). Make it with `t.TempDir`, or remove
a folder shared by the whole binary in `TestMain` after `m.Run`. What test
binaries share on purpose (internal/standin's warmed scripts) goes under
`DF_SHARED_TMPDIR`.
`CHECK_REAL_CODEX=1` gives codex its real home back
(`TestRealCLIRunsNeedTheirSwitch`). A test that starts a stand-in agent CLI in
a pane sets PATH inside the start command (`PATH=<bin>:$PATH; trap : INT;
codex go`): `tmux -e PATH` doesn't survive the pane's login shell.
`#{pane_current_command}` differs too: a pane started as `sleep 600` reads
`sleep` on macOS, whose `/bin/sh` (bash) execs a lone command, but `sh` on
Ubuntu, whose dash forks it and stays the pane's process. A test that waits for
a pane to run its command starts it with `exec` (`exec sleep 600`, crewSession
in crew_all_test.go), so the pane's own process is the command everywhere.
A stand-in
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

## A loaded machine

It also has to pass on a loaded machine: CI runs while several workers run
`make validate`. A timing test keeps its absolute limit for a quiet machine
and, when the machine is slow, compares against a cheaper request timed in
the same run (`TestStatsPageFastOnABigProject`): it times the two in
turns over several rounds and compares the best of each, since one sample
can land in a burst of load. `timing.Check` (internal/timing) is that
shape: `Check{Limit, Ratio, Max, Rounds}.Run(op, base)`; a timeout check
sets `Max` under the time the uncut command takes, and its baseline is a
command that ends by itself at the budget. When the 1-minute load is above
the core count, a heavier op loses more to it than its baseline, so Ratio
doubles (`SaturatedSlack`) and the result names the load; a test of a
check's verdict pins `Check.Load` to a stand-in reading. One timed sample held to a
fixed limit, or a retry loop by hand against one (a fixed floor such as
`max(300ms, k×base)` counts), fails `TestEveryTimeLimitGoesThroughTheHelper`
(a guard test) with its file:line; a sample that must stay single (a lower bound) says why
in a `// one sample: <why>` comment. A test waits for a file or
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

## Shell

The worker briefing's `## Shell on this machine` section says what
applies on yours (zsh, macOS BSD tools, rtk), detected from `$SHELL`, the OS
and `rtk` on PATH; the same notes are in docs/getting-started.md#shell-notes.

## UI checks

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

See [docs/patterns/playtest/web-app.md](patterns/playtest/web-app.md).
