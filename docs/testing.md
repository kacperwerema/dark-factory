---
title: "Validation and Test Rules"
order: 89
---

What `make validate` runs, and the rules a test in this repo follows: on macOS and Linux, on a loaded machine, with tmux, stand-in agent CLIs and git kept to the test. CLAUDE.md keeps the short form and links here.

## make validate

`make validate` runs its cheap steps one by one beside the full suite (each logs to a file, printed whole with a `[<step>]` lead when it ends; a red one says so at once): `make merge-check` (no MERGE_HEAD in the checkout's own git dir, no tracked line starting with a conflict marker, each named file:line; fixtures that need one go on `MERGE_CHECK_ALLOW`), `gofmt -l` (fmt-check), `go vet ./...` (vet), `make catalog-check`, `make link-check` (every relative markdown link points at a file, spelled in the exact case of its name on disk), `make leak-check` (the tracked tree's leak scan), `make codemap-check` (every package mapped in `workbench.contexts`, no new upward import; names the unmapped package and the fix), `make guard-check` (the repo-wide guard tests listed in `scripts/guard-tests.txt`, each failure with its rule from [Guard tests](#guard-tests): docs tables, doc numbers, every-X-is-listed checks; a new `TestEvery*`, `Test*ListedInDocs` or `TestDocNumbers*` goes on that list; the suite skips them then, `GUARD_SKIP`, and cover-cli adds their coverage, `TestValidateRunsTheGuardTestsOnce`), `make playtest-check`, and beside them the full test
suite (test; tools/test-shards runs the slowest packages' tests in several go test processes each, `TEST_SHARD_PKGS`: cmd/issue-cli in `TEST_SHARDS` (default 4), internal/viewer and internal/bench 3, internal/agentrun 2; split by the newest local CI run's times and kept while a package's tests stay the same, beside the rest) and a coverage gate on `cmd/issue-cli` (cover-cli; floor: `CLI_COVERAGE_FLOOR`,
default 70). Packages come from go test's cache when nothing they read changed: `scripts/test-cache.txt` lists every test package `cached` or `fresh` (-count=1, with why: it asks git about the checkout, reads this machine), plus `fresh-test` tests that run on their own beside the cached run; a live-board test (`liveboard.Dir`) skips in the cached run and fails there unless it has a fresh-test line (`TestEveryTestPackageIsInTheCacheList`). `TEST_CACHE=0` runs everything fresh, as local CI's slow tier does. A test writes nothing into the checkout: a folder a test walks is hashed with each entry's size and mtime, so one stray file reruns every walker. The suite writes cmd/issue-cli's profile (`-coverpkg`, `CLI_COVER_PROFILE`, in TMPDIR, merged across the shards) and cover-cli reads it instead of running those tests again; `make cover-cli` alone runs them itself (`TestValidateCoverageFloorReadsTheTestRun`). Use it before proposing a change that touches
`cmd/issue-cli/`. Every step runs even after a red one; a red step prints `validate: red so far: <step>, <step>` at once, so a worker watching the log can stop there. A red test step on a work branch is followed by `validate: main has <sha> <subject> touching <path>: merge main and re-run` for each commit main has since the merge base that touches a failing test's file or package (tools/main-fix-hint, `MAIN_FIX_HINT`): merge main first, it may have fixed the test already; the briefing's Local CI line says how many commits main is ahead of the branch's base. Its last line on stdout is `validate: PASS` or `validate: FAIL (<step>, <step>)`, naming each red step;
each step prints `validate: <step> <seconds>s` when it ends and
`validate: total <seconds>s` comes just before the verdict (local CI keeps
them as the run's `steps` in `.ci/runs/<sha>.json`, and `issue-cli ci status`
names the slowest):
read that line, not a wrapper's exit code (a backgrounded `make validate > log; echo $?`
reports the echo's 0). `VALIDATE_STEPS=` runs a subset. In a linked worktree it also writes its whole output, stderr too, to `VALIDATE_LOG` as it runs, by default the worktree's own git dir (`git rev-parse --git-path validate.log`), and its first line names the file, `validate: log <path>`: one file per worktree, never a shared /tmp name another session's run can overwrite, and outside the work tree, where a growing file would make go test's cache miss for every test that walks the checkout. `VALIDATE_LOG=<file>` names another, `VALIDATE_LOG=` turns it off, the main checkout has none, and a run already redirected into that file (`make validate > "$(git rev-parse --git-path validate.log)" 2>&1`, the briefing's shell note) is not written twice (`TestValidateWritesItsLogIntoTheWorktreesGitDir`).

### Test tiers

`make validate` is the fast tier. Local CI's slow tier (`issue-cli ci run --tier slow`, and the nightly after 03:00) runs `make test TEST_CACHE=0 SLOW_TESTS=1` and `make ux-test ALL=1`. `SLOW_TESTS=1` sets `DF_SLOW_TESTS=1` for the suite; a test that calls `slowtier.Only(t, why)` skips without it and says how to run it (`DF_SLOW_TESTS=1 go test -run <name> ./<pkg>`). A test goes there only when a cheaper fast-tier test beside it already proves the path works, and it costs a whole run.

The slow-tier-only tests, each with the fast-tier tests beside it that prove the same path:

- `TestBenchSmokeFailsWhenTheHarnessBreaks` (cmd/issue-cli): a second whole smoke run of the fixture (8 s idle, ~40 s in a loaded validate) for the broken-scorer and `--keep` paths. In validate: `TestBenchSmokePassesOnTheFixture` (one whole smoke run) and internal/bench's `TestSmoke*`.
- `TestBenchCandidatesCheckReplaysWithoutModelOrBenchmarkWrites` (cmd/issue-cli): `bench candidates --replay` end to end, worktrees and go test on a fixture repo (2.7 s idle, ~23 s loaded). In validate: internal/bench's `TestReplay*` and `TestBenchCandidatesNeedReplayForSlugs` (the usage error and footer hint).

A path-gated test runs in `make validate` only when the change since main (its merge-base, committed or not, untracked files too; `scripts/touches.sh`) touches the paths it drives, and always in the slow tier; a plain `go test` runs it too. cmd/playtest's Chrome tests (every test that starts with `needChrome`, ~50 s of the package's ~60 s) are gated on the Makefile's `PLAYTEST_E2E_PATHS`: `cmd/playtest/`, `playtest/`, `internal/viewer/templates/` and `internal/viewer/static/`. `make test` sets `DF_PLAYTEST_E2E=1` or `0` from it (`1` under `SLOW_TESTS=1`), and a gated-out test skips with the paths in its message (`DF_PLAYTEST_E2E=1 go test -run <name> ./cmd/playtest` runs it). On main the change is empty, so local CI's default tier leaves them to the slow tier and the nightly. `TestChromeGatePathsMatchTheMakefile` keeps the path list and the Makefile in step, and `TestTouchesSaysWhetherTheChangeSinceMainHitsAPath` checks the script.

### Guard tests

`make guard-check` runs the guard tests alone, in about 10 seconds: run it after your first commit that touches `cmd/` or `internal/` (the commit-msg hook says so), not first at `make validate`. Each guard test that fails gets a `guard-check: <test>` line under the go test output with its rule from the table below, the table's file:line and the page that says more. Every test in `scripts/guard-tests.txt` has one row here (`TestGuardTestsTableNamesEveryGuardTest`); a row's cells hold no `|`.

| Test | Rule | More |
|---|---|---|
| `TestEveryTestPackageIsInTheCacheList` | Every test package has one line in scripts/test-cache.txt: `cached ./<pkg>`, or `fresh ./<pkg> <why>` when a test reads what go test's cache can't see (git about this checkout, this machine, the live board); a live-board test also gets a `fresh-test` line. | [make validate](#make-validate) |
| `TestEveryCommandJoinsEveryRegistry` | A new command or subcommand joins commandAccess, readSamples (a command with a read mode; the sample call must write a clilog record), guardVerbs for a guarded verb and the Commands table: walk "Adding a command". | [Adding a command](CLI/overview.md#adding-a-command) |
| `TestCommandWideTestsListsEveryTestOverAllCommands` | A test that loops over every registered command is listed in commandWideTests (command_checklist_test.go), so the checklist test's `-run` line names it. | [Adding a command](CLI/overview.md#adding-a-command) |
| `TestCommandsTableNamesOnlyCommands` | Each row of the Commands table names a registered command or alias. | [Adding a command](CLI/overview.md#adding-a-command) |
| `TestEveryCommandHelpNeedsNoProject` | `<cmd> --help` runs without a config: help loads no project, and a command that runs with no project at all (one that reads only files) is named in bypassProjErr (main.go). | [Adding a command](CLI/overview.md#adding-a-command) |
| `TestEveryCommandRejectsUnknownFlags` | Every command and subcommand parses its flags through parseFlags, parseFlagsArgs or parsePositionals before acting, so `--no-such-flag` exits 2 naming it; flags parsed by hand (a vendor's after ` -- `) go in ExtraFlags. | [Adding a command](CLI/overview.md#adding-a-command) |
| `TestEveryHelpHintNamesRealFlags` | A flag named in a ShortHelp, LongHelp or usage line is one that command defines. | [Adding a command](CLI/overview.md#adding-a-command) |
| `TestEveryWorkerGuardConsultsTheSandboxPass` | Each worker guard asks the sandbox pass under a verb in guardVerbs, and every verb there has a guard; a new guard's rule is the owner's call in that command's design. | [Owner-only commands in a sandbox](patterns/guided-check-scripts.md#owner-only-commands-in-a-sandbox) |
| `TestEveryCleanAreaIsInTheGrowthTable` | Every `issue-cli clean` area is a row of the table of what grows and what bounds it. | [What grows over time](fs-churn.md#what-grows-over-time-and-what-bounds-it) |
| `TestPlaytestGuidesInSync` | issue-cli's copy of docs/patterns/playtest is generated: run `go generate ./cmd/issue-cli/...` after editing a guide. | [UI checks](#ui-checks) |
| `TestEveryPaneSenderIsListed` | Code outside internal/agentrun that types into an agent's pane is listed in paneSenders (suggestion_test.go) with its delivery check. | [Code Structure](code-structure.md) |
| `TestDocNumbersInSync` | docs/numbers.md is generated from the constants: run `go generate ./internal/docnumbers/...`, never edit it by hand. | [Numbers](numbers.md) |
| `TestDocNumbersCopiesAgree` | A sentence that quotes a number reads the constant's value: change the sentence, the constant is the source. | [Numbers](numbers.md) |
| `TestLintChecksListedInDocs` | Every registered lint check has a bullet under "The checks" in the lint family, and every bullet names one. | [Adding a lint check](CLI/overview.md#adding-a-lint-check) |
| `TestEveryCallerGoesThroughThePackage` | Go code runs mem-guard and compares memory only through internal/memguard: no copy of its lines or load comparisons. | [Code Structure](code-structure.md) |
| `TestEveryGoBuildInATestGoesThroughTestbin` | A test that needs a binary of this module asks testbin.Path for it, never its own `go build`. | [Code Structure](code-structure.md) |
| `TestEveryTimeLimitGoesThroughTheHelper` | A test with a time limit runs through timing.Check, never its own clock comparison. | [A loaded machine](#a-loaded-machine) |
| `TestEveryTestBinaryDropsTMUX` | Every test binary's TestMain is tmuxguard.Main, so no test reaches the shared tmux server. | [macOS and Linux](#macos-and-linux) |
| `TestCloneCopiesEveryField` | A field added to WorkflowConfig gets its line in Clone (workflow_merge.go). | [Workflow engine](Workflow/overview.md#key-files) |
| `TestEveryRuleHasASummary` | A validator rule has a summary (structuredSummary or validationSummary) for process transitions and the previews. | [Validation Rules](Workflow/overview.md#validation-rules) |
| `TestEveryRuleIsInTheDocs` | docs/workflow.md names every validator rule. | [Validation Rules](Workflow/overview.md#validation-rules) |
| `TestSettledRulesTableMatchesDoc` | Every rule and D-n ref "Settled rules" cites has a row in the settled-rules table, and every row is one it cites. | [Settled rules](departments.md#settled-rules) |
| `TestEveryRouteIsInTheSecurityInventory` | A route you add or change gets a row in the route inventory, classified R/W/O/X. | [Route inventory](API/security.md#route-inventory) |
| `TestOwnerRoutesListedInSecurityDoc` | Every O row of the route inventory has a request in ownerRoutes (owner_test.go), and every request there is an O row. | [Route inventory](API/security.md#route-inventory) |
| `TestEveryWorkflowFeatureHasACatalogEntry` | A workflow.yaml key, validator rule or action type has a catalog entry that detects it (a probe or `rule_in:`). | [Adding or changing a catalog entry](Workflow/overview.md#adding-or-changing-a-catalog-entry) |
| `TestEveryEntryDetectsItsOwnSnippet` | A catalog entry's probes find its own snippet. | [Adding or changing a catalog entry](Workflow/overview.md#adding-or-changing-a-catalog-entry) |
| `TestEveryEntryHasSourcesThatExist` | A catalog entry names the code behind it in `sources:`, and every source matches a tracked file. | [Adding or changing a catalog entry](Workflow/overview.md#adding-or-changing-a-catalog-entry) |
| `TestGuardCheckListsEveryGuardTest` | A new `TestEvery*`, `Test*ListedInDocs` or `TestDocNumbers*` test goes on scripts/guard-tests.txt, and every line there names a test that exists. | [Guard tests](#guard-tests) |
| `TestGuardTestsTableNamesEveryGuardTest` | Every test in scripts/guard-tests.txt has one row in this table, and every row names a listed test. | [Guard tests](#guard-tests) |

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
kills it only that way (`TestTmuxCallsInTestsNameTheirSocket`). `tmuxguard.KillServer` does that kill and returns once nothing the server ran is left: a bare `kill-server` returns while the panes' processes still run, and under load one wrote into its test's `t.TempDir` after the cleanup removed it. A test that starts a process which starts its own (the viewer starts the nightly clean, `git`, `lsof`) stops it with `procs.StopTree`, not `Process.Kill`, for the same reason (TestNewcomerWalkthrough left its TempDir in TMPDIR that way). A tmux `-F` format is built with `agentrun.Format` and read with
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
(`TestRealCLIRunsNeedTheirSwitch`). A test never reads the owner's
config: a package whose code reads `~/.config/ai-workflow-engine` (the
machine's rules.md, projects.yaml) points `XDG_CONFIG_HOME` at a temp folder
in its `TestMain`, as cmd/issue-cli and internal/lint do
(`TestLintTestsNeverReadTheOwnersRules`), so a stray line in the owner's
file can't turn every worktree red; a test that wants one sets its own with
`t.Setenv`. A test that starts a stand-in agent CLI in
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

### Timing self-checks

A timing test that proves its check bites (the page made N× slower must
fail) times a stand-in through the same check: `op, base :=
check.Slowed(n, op, base)`, then `check.Run(op, base)`. A bare N× of the
real page is not enough: where the baseline is slow too, N× can sit inside
Ratio× it (v0.65.0's public CI: a /status of 36 ms against a /board of
60 ms is 180 ms at 5×, within 3×). `Slowed` keeps the stand-in at least
twice both `Limit` and `Ratio`× the slowest baseline it has seen, so it fails
on any machine. On a saturated run the ratio doubles and the stand-in may
pass: when the `Result` passed with `Saturated` set, the self-check
`t.Skip`s with `r.String()` (it names the load) instead of failing
(`statusSelfCheck`, `TestStatusSelfCheckCatchesASlowedPageOnASlowRunner`).

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
