---
title: "CLI Overview"
order: 1
---

## Scope

The CLI system covers `issue-cli`, the command-line tool agents use to interact with issues during automated workflows.

## Key Files

- `cmd/issue-cli/main.go` — entry point, global-flag parsing, top-level error printing
- `cmd/issue-cli/commands.go` — registry (`registerCommand`, `lookupCommand`, `printHelp`); top-level help is auto-generated from this list
- `cmd/issue-cli/context.go` — `Command` and `Context` types, `findIssueOrErr`, `requireSlug`, `newFlagSet`, `flagWasSet`, and flag parsing: `parseFlags` / `parseFlagsArgs` / `parsePositionals`, `flagsFirst`, `usageError`
- `cmd/issue-cli/helpers.go` — shared helpers (`loadProjectOrErr` → `resolveBootstrap` / `resolveFromConfig`, `formatProjectList`, `parseFieldFlags`, `normalizeEscapedText`, `writeJSON`, `printCheckboxes`, etc.)
- `cmd/issue-cli/cmd_<name>.go` — one file per subcommand. Each file declares a `*Command`, registers it in `init()`, and owns its own `flag.FlagSet`. Group commands (`data`, `workflow`, `process`) dispatch internally to per-subcommand handlers in the same file.
- `cmd/issue-cli/workflow_init.go` — testable `doWorkflowInit` core (the `workflow init` subcommand wraps it from `cmd_workflow.go`)
- `cmd/issue-cli/cmd_upgrade.go` / `cmd_scan.go` / `cmd_catalog_check.go` — the `workflow upgrade` / `upgrade`, `workflow scan` and `workflow catalog-check` commands. The catalog, structural probes, `features:` record writer, `BuildPlan` and `CheckBranch` are `internal/workflowupgrade/`, shared with the viewer's Updates tab; the embedded templates and catalog are `cmd/issue-cli/templates` (package `templates`)
- `cmd/issue-cli/workflow_upgrade.go` — `projectWorkflowFile` / `workflowFileLabel`: which workflow file the project runs on
- `cmd/issue-cli/cmd_health.go` — the `health` command (print the latest snapshot and the scores, take one, backfill past days, the snapshot after `ci run`); `cmd_health_review.go` is `health review` (brief, record, file, dismiss, show, `--spawn`); snapshots, families, the score and the opinion are `internal/health/` (plus `internal/timeline` and `internal/milestone`, imported for their families)
- `cmd/issue-cli/cmd_ci_gotest.go` — `ci gotest`, the go test -json filter for `ci.run` results
- `cmd/issue-cli/cmd_ci.go` / `ci_report.go` — the `ci` command (detached runs in tmux) and what a run reports (`ci:` comments, `on_red: file_issue`; it never messages a session); the runner itself is `internal/ci/`

### Adding a command

A new command or subcommand joins several registries. Walk this list before the first run, then run the checklist test, which names every list a command is still missing from in one error per command:

```bash
go test ./cmd/issue-cli -run TestEveryCommandJoinsEveryRegistry
```

It checks `commandAccess`, `readSamples` (when the command has a read mode) and the [Commands](#commands) table row, mentions `guardVerbs` for a guarded verb, and ends with the `-run` line of the command-wide tests (`commandWideTests` in `command_checklist_test.go`), which run every command and fail only when it misbehaves: help without a project, unknown flags, introspection, help hints, read-only calls on another project. `TestCommandWideTestsListsEveryTestOverAllCommands` fails for a new test that loops over `commandNames()`, `registryCases()`, `readSamples` or `commandAccess` and isn't on that list. Both run in `make guard-check`.

1. Create `cmd/issue-cli/cmd_<name>.go`.
2. Declare a `var <name>Command = &Command{Name, ShortHelp, LongHelp, Run}` and call `registerCommand(<name>Command)` in `init()`.
3. Build the `Run` function as `func(ctx *Context, args []string) error`. Construct a `flag.FlagSet` via `newFlagSet("<name>", ctx)` and parse `args` (everything after the subcommand name) with one of these, never `fs.Parse` directly. Call it before doing any work. Return errors instead of calling `os.Exit`.
   - `parseFlags(ctx, fs, args)`: the command takes no positionals beyond what it already pulled off (e.g. the slug from `requireSlug`). A leftover positional is a usage error.
   - `parseFlagsArgs(ctx, fs, args)`: the command reads positionals from `fs.Args()` (text, ids, a status).
   - `parsePositionals(ctx, name, args, max)`: the command has no flags at all; it returns the positionals and refuses more than `max`.

   All three accept flags anywhere in `args` (see [Flags, `--help` and exit codes](#flags---help-and-exit-codes)), record the flag names for [usage telemetry](telemetry.md), and let help and the never-used report introspect the command's flags. Bool flags are read from the FlagSets, so the telemetry needs no list of them.
   - **Flags before the positional.** An item-first subcommand (`<cmd> <sub> <item> [--flag]`) still hands the whole rest of `args`, item included, to the parse call, and it parses before it looks the item up or acts on it. `TestEveryCommandRejectsUnknownFlags` runs every registered command and subcommand with `--no-such-flag` both first and after positionals and wants exit 2 naming the flag. A subcommand that pulls the item off first and parses only what follows, or that does work before parsing, fails it. Introspection needs the same order: it fills the positional slots with placeholders (`introspectArgs`) and stops at the parse call, so a command that touches the item first shows no flags in help.
   - `TestEveryCommandHelpNeedsNoProject` runs `<cmd> --help` without a config, so help must not load a project.
   - Group commands set `Subcommands` (and `SubAliases`) on the `Command`. Flags parsed by hand go in `ExtraFlags`. A word dispatched by hand without a `Subcommands` entry (`decisions log`) is invisible to help's flag list, telemetry and the unknown-flag test; `Subcommands` replaces the bare form there, so a command that takes flags both bare and after a word can't declare both yet.
4. Reach the project via `ctx.Project`, write to `ctx.Stdout` / `ctx.Stderr`, and consult `ctx.JSONOutput` for output mode. The package-global `jsonOutput` no longer exists.
5. Add the command to `commandAccess` in `cmd/issue-cli/readonly.go`: `reads`, `writes`, or read-only for some subcommands or flags. A command with a read mode also gets a sample call in `readSamples` (readonly_test.go). A read-only call on a project the caller isn't working in leaves nothing in that repo, not even its log line (see [Read-only calls on another project](telemetry.md#read-only-calls-on-another-project)). `TestEveryCommandJoinsEveryRegistry` fails without an entry, and without a sample for a command with a read mode; `TestCommandAccessNamesOnlyCommands` fails for an entry naming no command. Mark `writes` when unsure.
6. Help hints name real flags. Every `issue-cli <cmd> [<sub>] … --flag` written in a `ShortHelp` or `LongHelp`, and every usage line starting with the command's own name (`  bench task add <benchmark> <slug> [--start <status>]`), may name only flags that command (and subcommand) defines. `TestEveryHelpHintNamesRealFlags` (help_hints_test.go) introspects each one; `TestBundledTemplatesNameOnlyFlagsTheCommandAccepts` does the same for the bundled workflow templates. Prose about another tool (`git rm --cached`) isn't checked, so write hints with the `issue-cli` prefix.
7. Telemetry needs nothing more when the flags go through the parse calls above: the command, subcommand and flag names are recorded, never values. Don't log free text. Details: [CLI Usage Telemetry](telemetry.md#adding-a-command).
8. Where code hooks in around a call: `runTraced` (main.go) builds the `Context`, runs `cmd.Run`, then runs what rides on every call (`printSiblingAsks`, `decorateApprovalError`). A per-call hint or notice goes there, after `cmd.Run`. `ctx.issue` holds the issue `findIssueOrErr` resolved, if any, so code after `cmd.Run` can tell which issue the call was about.
9. Project resolution: a call without `--project` whose working directory has `./issues` runs in bootstrap mode (`resolveBootstrap` in helpers.go), a one-project config built from that directory, whatever `--config` says. A test that runs `run(...)` must therefore pass `--project` or move into a temp dir (`withCwd(t, t.TempDir())`, as `registryProject` does), or it picks up the directory it runs in.
10. Tests: a `tracker.Project` built without `WorkflowFile` (as `makeSimpleProject` builds it) loads the working directory's `workflow.yaml` (`Project.LoadWorkflow` falls back to it), which in cmd/issue-cli is none but in the repo root is the engine's own workflow. Set `WorkflowFile` or `withCwd` into a temp dir so the test runs on the workflow it means.
11. The new command appears in `issue-cli help` automatically. A new top-level command also needs a row in the [Commands](#commands) table below, worded from its `ShortHelp`: `TestEveryCommandJoinsEveryRegistry` fails for a registered command the table has no row for, and `TestCommandsTableNamesOnlyCommands` (commands_table_doc_test.go) for a row naming no command or alias.

### Adding a lint check

A new `issue-cli health lint` check is one entry in a registry, but five more places name every check, and each has a guard test that fails only in `make validate`. Walk this list with the first commit.

1. **The registry.** Write `check<Name>(env *Env) []Finding` in its own file under `internal/lint/` and add `{"<name>", "<help>", check<Name>}` to `checks` in `internal/lint/lint.go`. The help line is what `health lint --list` prints. A check that reads the board or the repository rather than a workflow file also goes in `boardChecks` there, so it runs once and not again on each department's file.
2. **The run order.** Add the name, in the same place, to the comma-joined list in `TestMinimalWorkflowIsClean` (`internal/lint/lint_test.go`). The minimal workflow must stay clean: a check that warns on it is too eager.
3. **The lint family's list.** Add a bullet `` - `<name>`: … `` under "The checks" in [The lint family](../workbench-files.md#the-lint-family), saying what it warns about. `TestLintChecksListedInDocs` (`internal/lint/docs_test.go`) fails while a registered check has no bullet there, and on a bullet naming no check.
4. **The CLI reference.** Describe each rule and its fix in the `health lint` row of [`health`](#health) below.
5. **Settled rules, for a check that reads issues.** A check over issue bodies or boxes joins the bold list "Lint checks that read issues" in [Settled rules](../departments.md) (`docs/departments.md`), and the same words go in `internal/tracker/testdata/settled-rules.tsv`, or `TestSettledRulesTableMatchesDoc` goes red.
6. **The lint scenario.** `TestLintScenarioCarriesTheSessionsThreeWarnings` (`internal/lint/scenario_fixture_test.go`) runs every check on `playtest/scenarios/lint`, whose session counts three warnings. A new check that fires there: fix the fixture, or, when the warning is the point, add it to the session and the test.

A check that reads a new workflow.yaml key also needs that key in the feature catalog ([Adding or changing a catalog entry](../Workflow/overview.md#adding-or-changing-a-catalog-entry)); `make catalog-check` names a catalog entry whose `sources:` list the file you changed.

## Commands

| Command                          | Description                              |
|:---------------------------------|:-----------------------------------------|
| `issue-cli doctor leaks [--stop] [--under <dir>] [--quiet]` | What the factory left running on this machine: a playtest Chrome whose runner is gone, a process whose CODEX_HOME or CLAUDE_CONFIG_DIR is a temp or scratchpad folder (codex app-server-daemon), a killed test's tmux server, a per_run: tool no run holds, a registered worktree nothing uses in a temp or scratchpad folder (an hour idle) or a subagent's under `.claude/worktrees/` whose branch is on main (a day idle), with its `git worktree remove`; with a resource trace (`DF_TRACE=resources` or `--debug`) also what was started, never stopped and is still alive. Each with its pid, age and stop command; nothing is stopped without `--stop`, which leaves the traced ones alone. `--json`. See [Troubleshooting](../getting-started.md#troubleshooting) |
| `issue-cli show <slug>`          | Print full issue context                 |
| `issue-cli next`                 | Find work for a version: `--version`, else the project's `version:` in projects.yaml, else every version; `--design` lists the ideas that need design |
| `issue-cli search <regex>`       | Search issues by regex or text: title, body and status, case-insensitive |
| `issue-cli create`               | Create an issue (`--title`, `--system`, `--status` before backlog, `--priority`, `--type`, `--body` or `--body-file <path\|->` for its first text, read as `append` reads it; `--parent <epic>` writes `parent:`, refused unless the slug is an epic on the board; `--goal <key>` writes `goal:`, the projects.yaml goal it serves, refused unless the project has that goal: [Work behind a goal](../departments.md#work-behind-a-goal)). It writes `created: "<today>"`, the date the `criteria_proven` gate reads; so do `report-bug`, CI's red issue and the viewer's New issue form. In a dark-factory checkout a name from the private-names list leaves the slug, pinned with `slug:`, with a warning: [Leak scan](../workflow.md#the-leak-scan). It warns, naming the done issue and the `close <new> --fixed-by <done>` line, when a done issue may already hold the new one: the brief's first lines name the same sweep source and item (an output id such as `o-20261117-f9bc`, `report f9bc` or the report issue's slug, with `item N` or `follow-up N`), or the title shares at least 5 words and half of the shorter title's |
| `issue-cli list`                 | List issues with filters; `--all` drops the default project-version filter; `--parent <epic>` lists that epic's children. Supports `--sort score` and emits `Score`/`ScoreBreakdown` in `--json` when scoring is enabled |
| `issue-cli start <slug>`         | Pick up issue from any status — claim + auto-advance handoff states (announced with a banner) |
| `issue-cli claim <slug>`         | Only set the assignee; it does not start work (use `start`). `--force` reassigns an issue someone else claimed |
| `issue-cli unclaim <slug>`       | Remove the assignee from an issue; the status stays |
| `issue-cli transition <slug>`    | Attempt the next workflow transition. `--dry-run` lists every unmet requirement; `--check <ids>` ticks those boxes first (check's rules) and moves in the same call, see [One call: tick and move](#one-call-tick-and-move) |
| `issue-cli done <slug>`          | Mark the issue done: runs the shipping → done validations, then unclaims it; refuses to run twice |
| `issue-cli rounds <slug>`        | Red rounds per check (`red 3 of 4`): gate commands, the proof check, local CI, the critic; each round with why it counted (only a failure that comes back counts; an owner comment starts over). At `max_red_rounds` the check is a question for the owner and transition refuses until it is resolved (parking still passes); `--cannot-fix <check> --why "…"` asks now; `--json`. See [Red-round cap](../workflow.md#red-round-cap) |
| `issue-cli ship [<slug>]`        | Land the issue's branch on local main, in its worktree: merge main (`chore(merge): main into <slug>`), run the full check (`--check`, else `ci.run`'s default tier), `merge --ff-only` in the main checkout; when main moved, merge again and on a clean merge run the re-check (`go build`, `go vet`, the touched packages' tests; `--recheck "<cmd>"\|full`), up to `--rounds` (3), then the full check on the landed tree. A PASS is remembered per HEAD, so a re-run skips it. Last stdout line and exit code: `ship: LANDED <sha>` (0), `ship: NOT LANDED (<reason>)` (1: conflict, red check, ff refused, main moved N times), `ship: LANDED <sha>, check after landing FAIL (<steps>)` (3). A red test step names its failing packages (`FAIL (vet, test: internal/ci, internal/ship)`), and the first tmuxguard leftover line comes just before the verdict (`ship: tmuxguard: <bin> left … in TMPDIR`). Read that line, not `$?` of a pipe. Posts a `progress:` comment naming each check's sha; never pushes, forces or ticks a box; `--dry-run` prints the plan |
| `issue-cli trace <slug>`         | The issue's trace: one span per status visit with agent and model, briefing size and ids, tokens in / out / cached, wall time, CLI calls, the gate's first try and back moves; "—" for what nothing recorded; `--json`, `--write` (the file `<issues>/.traces/<slug>.jsonl`, also rewritten after every transition). See [Traces](../workflow-stats.md#traces) |
| `issue-cli comment <slug>`       | Add a comment to an issue. `comment edit <slug> <id\|last> --text "..."` (or `comment <slug> --amend --text "..."`, which is `edit … last`) corrects your own latest comment, keeping its number and date; the old text stays in its `edits` history and `show` marks it `(edited)`. The owner's, other agents' and approval:/dispatch:/stage:/yolo-review:/yolo-owner-check: comments can't be edited; `last` skips those and the ones the engine writes under your name (stage:, yolo-owner-check:, yolo-hold:, session: closed), and says so when none of your own words is left |
| `issue-cli comments <slug>`      | The issue's comment trail alone, each as `#<id>` (the number the page, `comment edit` and replies use) with day, author, kind and done; filters `--since <day>`, `--author <name>`, `--kind question,progress`, `--open`; `--json` for a parser. See [`comments`](#comments) |
| `issue-cli ask <sibling> "…"` / `issue-cli answer <asker> "…"` | Agree a format with a sibling issue's worker: an `ask:` comment on its issue, the `answer:` back on yours; each shows on the other worker's next issue-cli call. Never an approval. See [Sibling asks](#ask-and-answer-sibling-asks) |
| `issue-cli clarify <slug>`       | The design's clarify round: questions, options, answers; `--assume`, `--answer Qn=k --quote` |
| `issue-cli decide <slug>`        | Record a decision as a `D-n` line under `## Decisions`; `--replaces <slug>#D-n` lists the open issues still citing the old one; `--ref N-26` (repeatable) records what it answers outside the D-n scheme, such as a lead's provisional N-number, as `(ref N-26)` on the line, which `show`, `show --json` (`decisions[].refs`) and `decisions` print; `--scope/--hard/--at/--paths/--expires` and `--amend D-n` set its attribute group |
| `issue-cli prove <slug> [<AC-id>]` | Link an acceptance criterion to its proof (`--test`, `--step`, `--commit`, `--value`, `--shot`, `--lead`, `--clear`); without an id, list every criterion with its links and checks (no `## Acceptance Criteria` on a path that never asks for one: `no acceptance criteria: nothing to prove`, exit 0); `--run` runs the test links' tests and posts the `tests:` comment; `--lead-run` (the lead) runs the lead links' commands and records each run ([Proof that waits on the lead](../Workflow/proof.md#proof-that-waits-on-the-lead)). See [`prove`](#prove) |
| `issue-cli critique <slug>`     | Run a fresh read-only critic before the issue's `critique` gate, or by hand from a status with no gate (it holds nothing and says so), on the other agent than the worker's: paths only, findings `blocks`/`should`/`note` with path:line, recorded and commented; `--brief` prints the brief, `--show` the newest run, `--reparse <run id>` parses a run's kept raw output (`.critique/raw/<run id>.txt`) again with the current parser, `--override --quote` is the owner's. See [The Fresh Critic](../Workflow/critique.md) |
| `issue-cli check-script <slug>`  | Run the issue's guided check scripts (`scripts/checks/<name>.sh`) from its worktree: the owner's run from a terminal, or `--yes` (machine checks only, never ticks); `--list`, `--new <name>`, `--lib`. See [`check-script`](#check-script) |
| `issue-cli playtest prune <slug>` | Tidy a worktree's `.playtest/runs` to what the Evidence tab needs; `--all` for every issue's worktree, `--dry-run` to report the MB first. See [`playtest prune`](#playtest-prune) |
| `issue-cli balance <slug>`       | Add a typed balance note (`--metric`, `--state`, `--hypothesis`, `--change`, `--evidence`, `--verdict`, `--pass`); `balance --list` prints the current notes |
| `issue-cli decision`             | `challenge <ref> --why "…" [--propose] [--for]`: say a decision is wrong (your issue's `decision_unchallenged` gates hold until the owner answers); `resolve <ref> --keep [--quote]`: the owner keeps it (workers refused, a lead relays `--quote`); `challenges [--json]`: the open ones |
| `issue-cli decisions`            | List the project's decisions (active, deferred, replaced, stale) with `--system`, `--state`, `--issue` (or `decisions <slug>`), `--json`; `decisions log [--ref] [--issue] [--since] [--json]` reads the audit log in time order; `decisions drift [--record] [--since <commit>] [--json]` is the drift sweep (stale citations, hard decisions' paths changed on main uncited, Settled rules naming replaced refs, old provisional decisions); `--state provisional` |
| `issue-cli lead <sub>` (alias `ceo`) | The CEO (the project lead): `spawn`, `whoami`, `list`, `message <target> "text"`, `flush`, `tick`, `watch`, `state`, `rotate`, `history`. See [`lead`](#lead) |
| `issue-cli dispatch <slug> --quote "…"` | Start (or re-attach to) the issue's worker exactly like ▶ on the board; needs the human's words (`--quote`) and leaves a `dispatch:` comment; refuses, exit 3, while the issue only waits on your approval. `--how`, `--agent`, `--force`, `--reprompt`. See [`dispatch` and `status`](#dispatch-and-status) `--takeover` (no `--quote`) starts the other agent on the branch of a worker stopped at its usage limit, on the issue's own words, once per issue. |
| `issue-cli dispatch-prompt <slug>` | Print the prompt an agent dispatch would send right now, read-only: no worktree, tmux session or file, no running viewer needed; `--json` adds slug, status, worktree, branch and session. See [`dispatch` and `status`](#dispatch-and-status) |
| `issue-cli status`               | The project's stand-up from the running viewer (its `status.md`; `--json` for `status.json`; `--hq` for the HQ report). See [`dispatch` and `status`](#dispatch-and-status) |
| `issue-cli agents [status\|rest\|clear]` | The agent CLIs (claude, codex) and their subscriptions: which is working or resting after a usage limit, and the `routing:` rules. See [`agents`](#agents) |
| `issue-cli postmortem <sub>`     | The post-mortem at close: `start <slug> --quote "…"` (lead or owner; `--again`), `brief <slug>` (the paths-only brief), `write <slug> --file <path>` (refuses an action without the "act differently" test or one place), `show <slug>`, `list` (ends with the owner's page across post-mortems while anything waits; a refused or overtaken action isn't waiting), `list --grouped [--json]` (that page's rows: each group of copies with every post-mortem it came from, the alone ones, the fixed ones, and "N open action(s) … need M answer(s)"), `fixed <retro file>#A<n> --by <slug\|sha> [--copies]` (links an action to the issue or commit on main that fixes it; once the issue is done it needs no answer; `--copies` links its whole group). See [Post-mortem](../workflow.md#post-mortem) |
| `issue-cli ci <sub>`             | Local CI on the integration branch: `status`, `run`, `reset`, `watch`, `gotest`. See [`ci`](#ci) |
| `issue-cli clean`               | The nightly clean, once for the machine: the Go build cache to its cap, agent transcripts over 14 days, health snapshots thinned, merged worktrees, playtest runs; `--dry-run`, `--if-due`, `--json`. See [The nightly clean](../workbench-files.md#the-nightly-clean) |
| `issue-cli deploy [--dry-run] [--step <name> \| --from <name>] [--quote "…"]` | Walk the project's own `deploy:` steps from workflow.yaml in order: each step's needs, its run and probe, stopping at the first red or refused step; records under the issues dir's `.deploys/`. `--last` prints the newest record. See [`deploy`](#deploy) |
| `issue-cli tracker <sub>`        | The private tracker repo for the work files: `init [--remote <url>]`, `sync [--quiet]`. See [`tracker` and `privacy`](#tracker-and-privacy) |
| `issue-cli release <sub>`        | A release on its branch: `setup --dev <url>`, `cut <version>`, `pick <slug\|sha>...`, `publish <version> [--first]`. See [`release`](#release) |
| `issue-cli privacy <sub>`        | The privacy choice's checks: `check` (exit 1 on red), `hook` (install the pre-push guard), `pre-push` (what the hook runs). See [`tracker` and `privacy`](#tracker-and-privacy) |
| `issue-cli health [snapshot\|backfill\|lint\|review]` | Project health: print the latest snapshot and both scores, take one, compute past days, or the agent review. See [`health`](#health) |
| `issue-cli stats`                | Project overview: issue counts by status, system and assignee; `--json`. `--by-model [--days N]` compares the issues done in the window by type and the model their worker ran on: issues, reopened, red rounds, tokens p50 ([By model](../workflow-stats.md#by-model)). `--spec-flow [--days N]` (or `--all`) counts critique runs (findings, held → fixed / overridden / held again / still holds, time), clarify rounds (owner / assumed / open), red-round questions and post-mortem actions ([Spec flow](../workflow-stats.md#spec-flow)) |
| `issue-cli bench [candidates\|task\|holdout\|budget\|show\|run\|report\|compare\|smoke]` | The factory benchmark: golden tasks solved again in a sandbox, runs, reports, compare (exit 0/1/2). Changes to the benchmark are the owner's. See [`bench`](#bench) |
| `issue-cli approve <slug>`       | Record a human approval with the human's words (leads only). See [`approve`](#approve) |
| `issue-cli autostart <sub>`      | Auto-start: your standing start for one project, one worker at a time while memory is low and the load is under the core count (`--max-load`): `status`, `on --quote …`, `pause`, `resume`, `off`, `next`, `weight <n> --quote …` (`start_weight:`, the project's share of the machine's start gap: [The turn between projects](../agent-dispatch.md#the-turn-between-projects)); the lead starts with `dispatch <slug> --autostart`. See [Auto-start](../agent-dispatch.md#auto-start) |
| `issue-cli queue <sub>`          | The lead's queue: an ordered plan of items (`add`, `list`, `move`, `block`, `link`, `quote`, `drop`, `done`, `limits`); `next --start` starts the first startable one on the owner's words with auto-start's checks and CI green, and items are marked done with their SHA when the issue ships. See [The lead's queue](../agent-dispatch.md#the-leads-queue) |
| `issue-cli experiment <sub>`     | The experiment loop: `candidates` (what retros keep asking for), `init <slug>` (hypothesis, target), `run <slug> [--dry-run] [--baseline <run>]` (guard, baseline and candidate benchmark runs, verdict; `--baseline` pins a finished run when main has moved), `record`, `show`, `list`, `rollback <slug>` (a revert branch). See [Experiments](../workflow-stats.md#experiments) |
| `issue-cli prompt-cell <slug>`   | Which workflow.yaml prompt cells (type · status) a proposal edits, from its branch or `--cell`, and which benchmark tasks walk through them: `Reach: N of M tasks`, each missed task with why. Read-only. See [Experiments](../workflow-stats.md#experiments) |
| `issue-cli jobs [<dept>]`        | The departments' recurring jobs: schedule, next due, last run (status, token estimate, outputs), standing start on/off; `--json`. See [Recurring jobs](../departments.md#recurring-jobs) |
| `issue-cli job <sub>`            | Recurring jobs: `due` (what the lead's check-in starts, waits for you or holds), `run <dept>/<job> --quote …` (a run on your words; `--again`, `--dry-run`), `run <dept>/<job> --scheduled` (on the job's standing start, inside the auto-start budget), `auto on <job>\|--all --quote …` / `auto off`. See [Recurring jobs](../departments.md#recurring-jobs) |
| `issue-cli yolo <sub>`           | YOLO mode: gates pass without the owner for a window, reviewed after the fact: `status`, `on --until … --quote …`, `extend`, `off`, `queue`, `review <slug> --agree`. See [YOLO Mode](../Workflow/yolo.md) |
| `issue-cli check <slug> <id>...` | Tick checkboxes by id (several at once), whole section (`--section X --all`), text, or `--section` + `--index`; `--na "<reason>"` ticks one box that doesn't fit the change, see [N/A boxes](#na-boxes-check---na) |
| `issue-cli check-add <slug> --section "<name>" --text "…"` | Add one open box after the section's last box and print its id; every other box keeps its id. See [`check-add`](#check-add) |
| `issue-cli check-edit <slug> <id> --text "…"` | Change one box's text; it keeps its tick and id. See [`check-edit`](#check-edit) |
| `issue-cli uncheck <slug> <id>...` | Untick boxes by id (several at once). See [`uncheck` and `check-remove`](#uncheck-and-check-remove) |
| `issue-cli check-remove <slug> <id>...` | Remove boxes by id; every other box keeps its id. See [`uncheck` and `check-remove`](#uncheck-and-check-remove) |
| `issue-cli checklist <slug>`     | List checkboxes grouped by section, each with its id (`D3`, `AC2`); an owner box reads `HT1 [ ] (owner's) …` |
| `issue-cli append <slug>`        | Append content to issue body; `--section S --replace "<old line>" --body "<new line>"` replaces one exact line instead (see [`append`](#append)) |
| `issue-cli attach <slug> <file>...` | Save screenshots or any file (a CPU profile, a text capture) as issue attachments and print a markdown link each (`--section` also appends them). See [`attach`](#attach) |
| `issue-cli replace <slug>`       | Replace content of an existing section (`--body`, or `--body-file <path\|->` as for append), or one line of it (`--line N`, `--match RE`). See [`replace`](#replace) |
| `issue-cli update <slug>`        | Change the title (the slug is kept; `--rename` moves it), replace the body (`--body`, or `--body-file <path\|->` as for append) and/or set `parent:` (`--parent <epic>`). See [`update`](#update) |
| `issue-cli set-meta <slug>`      | Set or clear a frontmatter field, replacing its whole old value (list items and block lines included); refuses a write whose YAML would not parse (refuses `type` in projects with types — use `set-type`; `created` only on an issue that has none, `YYYY-MM-DD`, never changed or cleared; checks every slug in `depends_on`, and writes two or more as a YAML list; `--key department|system` is refused when the issue's status isn't in the target department's workflow file, and `--status <s>` moves it with a status from that file) |
| `issue-cli close <slug> --duplicate-of <other>` | Park an issue another one covers (`--fixed-by <other>` for one its change fixes) from any status, with a link both ways. See [`close`](#close) |
| `issue-cli set-type <slug> <t>`  | Change an issue's type of work; agents only while it is at its type's first status. See [Types of Work](../Workflow/types.md) |
| `issue-cli process workflow`     | Print the workflow file in use (`Workflow file:`), the active workflow, the `== Types ==` block, an `Evals:` line (each eval's id, score kind and types, or `none`) and `workflow.yaml` lint warnings; `--type <t>` prints one type's lifecycle; `--department <name>` a department's file; `--file <path>` a [candidate file](#process-transitions), read only |
| `issue-cli process transitions`  | Print transition rules (default workflow, or scoped via `--system <name>`, `--type <t>` or `<issue-slug>`; `--department <name>`, or `--file <path>` for a candidate file; `--workflow` is an alias for `--system`, and a path there is refused) |
| `issue-cli process schema`       | Print the `workflow.yaml` schema (fields, action types, validation rules) |
| `issue-cli process changes`      | Print the release history (last 20 versions) |
| `issue-cli process playtest [web\|game]` | Print the shared playtest-session contract, or one of the two worked examples (web app with chromedp, game engine). The guides are embedded from `cmd/issue-cli/guides/playtest/`, generated from `docs/patterns/playtest/` (the one source): edit docs/, then run `go generate ./cmd/issue-cli/...` (TestPlaytestGuidesInSync fails until you do) |
| `issue-cli report-bug "..."`     | Report a bug in issue-cli or the engine: it becomes a `cli-bug` issue on the engine board (see [`report-bug`](#report-bug)) |
| `issue-cli retrospective <slug>` | Save a workflow retrospective            |
| `issue-cli retro <sub>`          | Retro sweeps: `list [--since <date>]` (the retros and post-mortems since the last sweep), `archive --until <time> [--since <date>]` (move them to `retros/archive/`, log line, moves the mark). See [`retro`](#retro) |
| `issue-cli data <sub> <slug>`    | Per-issue structured data store — see [Per-issue Data Store](../data-store.md) |
| `issue-cli evidence <sub> [<slug>]` | Save stills (`add <file>… [--step <id>] [--force]`) into the issue's `<issues>/_evidence/<slug>/` in the primary checkout, from any folder, and list them (`list`); the Evidence tab shows them under Stills. See [`evidence`](#evidence) |
| `issue-cli workflow init` / `issue-cli init` | Pick a workflow template and bootstrap a new project: writes `workflow.yaml` from a bundled template and scaffolds `issues/`, `docs/` |
| `issue-cli project init <dir>`   | A whole new project in one command: `<dir>` with `issues/`, `docs/` and a stamped `workflow.yaml`, plus its projects.yaml entry (printed, or added to the `--config <file>` you name), then the viewer, CEO and department commands. Never overwrites a file; `--dry-run`, `--json`. See [`project init`](#project-init) |
| `issue-cli workflow upgrade` / `issue-cli upgrade` | Read-only: propose catalog features `workflow.yaml` lacks, item by item, plus the upgrade steps. `--all` lists the whole catalog |
| `issue-cli workflow scan`        | Detect which catalog features `workflow.yaml` has and write its `features:` record. `--dry-run`, `--installed`, `--declined`, `--forget`; `--template <name>` a bundled template's record |
| `issue-cli workflow catalog-bump <id>` | issue-cli developers: set a catalog entry's `version:` (default the next release) and carry every bundled template's stamp and `features:` record line with it. `--version`, `--dry-run`, `--json` |
| `issue-cli workflow catalog-na <id>[,<id>] <why>` | issue-cli developers: record the `Catalog: n/a` line of the commit not made yet: `catalog-check` counts it while the change is uncommitted, and the commit-msg hook writes it into the next commit. `--clear` drops it |
| `issue-cli workflow commit-msg <file>` | issue-cli developers: git's commit-msg hook. Refuses a `Catalog:` or `Proves:` line that would mark or link nothing, with the fix; adds the line `catalog-na` recorded; on a `work/<slug>` branch, notes the `Refs: <slug>` line until one commit has it, or in a session dispatched for that issue adds the line itself. `--install` writes the hook (`make hooks`) |
| `issue-cli workflow catalog-check` | issue-cli developers: fail when a work branch changes a catalog feature's code or a bundled template without a version bump (`make validate` runs it). `--base main` |
| `issue-cli goals [<dept>]`       | HQ's and the departments' goals, each HQ goal with the department goals rolling up into it (value, target, progress, period, stale, source); `--json`. See [Departments](../departments.md) |
| `issue-cli goal set <dept>/<id> <value>` | Record a manual goal's value (`--note`, `--date`) in `<issues>/.goals/values.jsonl`; an unknown key lists the valid ones |
| `issue-cli goal write <dept>/<job> <id>=<value>…` | A recurring job's run: replace its metrics file `<issues>/.goals/<dept>/<job>.json` atomically (metric run format, `--date`; `--merge` sets only the named ids and keeps the others with their own dates; `written` stamps the day it ran), naming the goal that reads each id; goals read it with `file:<issues>/…#<id>` and show "no value since <date>" once the job misses a whole run ([departments](../departments.md#goals-a-job-fills-from-a-connector)) |
| `issue-cli department show <name>` | A department's (or `hq`'s, `product`'s) mission, role, role prompt file (path and content), goals and a `Spend:` line (this week, 4 weeks, budget); `--json` |
| `issue-cli rules [--json]` | The standing rules every lead and worker is briefed with: the machine's `rules.md` (the owner's) and this project's `rules:`, with what narrows what. Reads only. See [Rules](../workflow.md#rules) |
| `issue-cli codemap`              | The code map: contexts by layer, the imports between them and the ones that point up a layer, packages no context lists; `--root <dir>` reads another checkout. See [`codemap` and `affected`](#codemap-and-affected) |
| `issue-cli affected [<slug>]`    | A branch's footprint on the code map: the packages it changes and what imports them, the contexts it works in and reaches, the binaries to rebuild; `--files <list\|->`, `--root <dir>`, `--json`. See [`codemap` and `affected`](#codemap-and-affected) |
| `issue-cli ref [<page> [<heading words>]]` | A docs page in short: with no page, the pages workers read most and the rest; with a page (a short name such as `cli`, a path under docs/, or title words), its outline: each heading with its first sentence and line; with heading words, that one section in full. `--measure [--days 7]` counts which pages this project's workers read and their median first-hour tokens, from Claude Code transcripts on this machine. Computed from the docs each call, nothing cached. See [`ref`](#ref) |
| `issue-cli memory add\|search\|list\|entities\|show\|edit\|forget\|pin\|unpin\|candidates\|consolidate` | The agents' shared memory: a fact with its kind, scope (project, lead, department, agent, machine) and source; the update step refuses a near-duplicate and lists it (`--updates`, `--supersedes id[,id]`, `--new`); `candidates <slug>` lists what an issue's trail and retro could teach and `consolidate` the near-duplicates, stale, expired and promotable memories (both read-only); ranking decays with a 90-day half-life; history per memory; pins for leads and the owner; secrets refused; `start` prints the pins and matches under `== Memories ==`; `add --at <status>` (or `edit --at`) ties a fact to a step and `transition` prints it on entering that status, at most 3; `search @agent` / `'#topic'` filters by entity and `entities` lists who knows what; `--json`. See [Shared Memory](../memory.md) Usefulness from the traces (`memory show`, `list --json`), the Unhelpful group in `consolidate`, and a worker's add refused for a note on its own issue: [Memory as a cache](../memory.md#memory-as-a-cache). |
| `issue-cli spend [<box>]` | What HQ, Product and each department spent: tokens and estimated cost by week for 4 weeks, top 5 issues, the budget as "x of y" (never enforced), unattributed sessions; from Claude Code transcripts on this machine, prices from `prices.yaml`; `--json`. `--kinds [--all-projects] [--days N | --since/--until] [--prices <file>]` prints tokens, est. USD, share and % of the weekly limit by project and kind of session (workers by type and stage, leads, subagents, post-mortems, critics, sweeps, jobs, benchmark, yours), prices marked assumed or confirmed; `--turns [<slug>]` each issue's worker turns by cause (transition and refusals, validate, single-file read, red round, rest); both `--json`, for a [before/after measurement](../workflow-stats.md#measure-before-and-after). `--context [--days N]` (default 7 days) prints where the tokens come from instead: per box and for the top 5 sessions, turns, the floor (first turn's context), median and max context per turn, compactions and the floor's share. See [Spend and budgets](../departments.md#spend-and-budgets) and [Context per turn](../departments.md#context-per-turn-spend---context) |
| `issue-cli department week <group\|department>` | The week a group's lead plans from: per department, goals and how far each moved in the last 7 days, outputs waiting on review / reviewed / shipped, jobs, handoffs in and out, what is blocked and on whom, and the run issue's `## Week plan`. `--write` writes the week report to `<issues>/.reports/<key>/<ISO week>.md` (this week's replaced), shown in the group's room; `--json`. Only reads; nothing starts (docs/departments.md#the-week-plan-and-report) |
| `issue-cli department partners [--slot <slot>]` | The partners catalog: the outside tools a department can use per slot (analytics, email, seo, social, video, support, payments, cms), each with its licence and the date it was read, what it needs, whether it runs always or per run, and whether it costs money in words; AGPL rows name their exception or none; each slot has at most one default (social: Postiz). `--json` is the rows. Reads the bundled file only; turns nothing on (docs/departments.md#the-partners-catalog) |
| `issue-cli department list` | HQ and Product (built in) and the bundled department templates, each marked installed or with its `department add` command; `--json` |
| `issue-cli department add <template>` | Install a department template: its workflow file (`workflows/<key>.yaml`), role prompt (`departments/<key>/role.md`) and a `departments:` entry with its goals in projects.yaml. Never overwrites a file; a template installs once, under its own name (a second add is refused); `--parent <group>` (under a group, docs/departments.md#groups), `--dry-run`; workers refused. A group template (`gtm`) installs the group and its departments. A template with a partner (`video`) records `partner:` at `--partner-path <dir>` (default the template's) with the pinned commit and prints, never runs, the partner's install steps (docs/departments.md#video-studio). A template with handoffs (`sales-b2c`) prints the `connections:` to declare, never writes them (docs/departments.md#sales-b2c); a group's `optional:` departments are named after it, and a top-level add beside such a group says how to put it under. Ends naming the files git doesn't track yet and the commit to make; `--commit` commits exactly those. `--mission "…"` and `--goal "<id>=<name>/<target>/<period>"` (repeatable) set the project's own; without them the template's samples are written marked `# sample: replace` and the last line is the `department update` command that sets them (docs/departments.md#your-own-mission-and-goals) |
| `issue-cli department update <name>` | Bring an installed department up to its template's newer text: a file or a `role:` / `mission:` / goal `name:` is replaced only when it still holds an earlier template's text; edited ones are kept and named; `--side` writes the template's text beside each edited file, `--dry-run`; workers refused. With `--mission "…"`, `--goal "<id>=<name>/<target>/<period>"` or `--drop-goal <id>` (both repeatable) it sets the project's own mission and goals instead, on any department, splicing only those lines; a value set to the sample ends `# sample: kept`; `--json` prints `{department, config, set, dry_run}` |
| `issue-cli department remove <name>` | Take a department's entry out of projects.yaml (its files stay); refused while it has open issues, listing each with its `set-meta` fix; `--dry-run`; workers refused |
| `issue-cli output add --title "…" --kind <kind> (--url \| --file \| --path)` | Put what an issue or job made on its department's shelf (`--issue`, `--department`, `--job`, `--note`; `--report <file>` keeps a file beside a `--file` output, shown under the player: a reel's QA report), in review; `--kind video --file x.mp4` (up to 200 MB) plays in the reader and on the Station, with a poster when ffmpeg is on PATH; a `--path` missing on the project's checkout but present in the issue's worktree (an unmerged branch) is read there and records `branch`/`worktree`; `<issues>/.outputs/outputs.jsonl`. See [Departments](../departments.md#outputs-shelf) |
| `issue-cli output review <id> approve\|reject --quote "…"` | The owner's stamp on an output (the owner or the lead; never a worker) |
| `issue-cli output publish <id> [--url]` / `output show <id> [--text]` | Mark an approved output published; show one output, with its file's absolute path (`File:`); `--text` prints a markdown output's newest version, and refuses any other output, naming its path or link |
| `issue-cli outputs [<dept>]` / `output list [<dept>]` | The shelf by department, newest first (`--department`, `--state`, `--issue`, `--json`); `output list` is the same command |
| `issue-cli reel init\|schema\|validate\|render\|qa\|shelve` | The reel kit: storyboard, render, QA and the shelf for reels in 9:16, 4:5 or 1:1 (the storyboard's `format`). `init [<dir>]` copies the starter Remotion template (never over a file), `schema` prints the storyboard's JSON Schema, `validate` lists every problem at its JSON path, `render` fills the template and encodes with FFmpeg (mem-guard first), `qa` checks duration, the format's aspect, loudness, black frames, caption safe zones and sync, `shelve` puts a passing reel with its QA report on the shelf. See [Reel kit](../reels.md) |
| `issue-cli output use <id> --by <dept\|issue>` | Another department (or its issue) uses the output: a `use` event, drawn as an output line on the company page |
| `issue-cli output comments <id> [--all]` / `output reply <id> <thread> --text "..."` | The owner's comments on an output (from its reader page), with where each thread sits in the newest version; answer one |
| `issue-cli output revise <id> --file <file> [--note "..."]` | A new version of a file output: copied next to the earlier ones, back in review; the reader shows the changes |
| `issue-cli connections [--from <box>] [--to <box>] [--kind request\|output\|dependency]` | What passes between HQ, Product and the departments, with each line's open and recently closed issues (`--json`). `create` fills `requested_by:` for a department lead or another department's worker (`--requested-by`). With `connections: requests:` in projects.yaml, `create` and `set-meta --key requested_by` refuse a request off the declared routes (naming the routes the asker has), `dispatch` by a department lead waits for the declared forwarder, and each line is marked declared / not declared / declared, no traffic (`route:` in `--json`). See [Departments](../departments.md#declared-connections-and-routing) |
| `issue-cli owner-step add --title … --due <day> --body "<exact text>" [--repeat 7d] [--issue <slug>]`, `owner-step list [--all]`, `owner-step done <id> [--quote …]`, `owner-step drop <id> --why …` | A step only the owner can do by hand, with its due day and the exact text: a row on Waiting on you (red once overdue) until the owner ticks it; a repeating one opens again N days after each tick. Done is the owner's act (worker refused, a lead relays `--quote`); drop is a lead's or the owner's. See [Owner steps](../workflow.md#owner-steps) |
| `issue-cli handoff <to> --title … --why … [--for <slug>]`, `handoff forward <slug> [--note …]`, `handoff decline <slug> --reason …`, `issue-cli handoffs [--from] [--to] [--waiting]` | One box asks another for work as an issue: `requested_by:`, `handoff_route:` and `handoff_for:` recorded, routed by the declared routes (via HQ for engine work); the relay lead forwards or declines; the asker's issue gets a `handoff:` comment when it is done or declined. See [Departments](../departments.md#handoffs) |
| `issue-cli connectors [<dept>]` | The departments' connectors: kind, sends, last check and its age, each secret's reference as set or missing (never the value); `--json`. See [Departments](../departments.md#connectors) |
| `issue-cli connector exec <dept>/<name> [-- <cmd> …]` | Run a connector with its secrets in the child's environment only: the MCP server, or a tool's command. An mcp connector with `url:` is bridged in process to its Streamable HTTP server, its `headers:` secrets added to each request. That department's lead and workers only; the owner's shell with `--as-owner`. With `needs_approved_output: true` it waits for the worker's issue to have an approved output on the shelf: a tool or a `command:` server doesn't start, a `url:` server's `tools/call` is refused except its `read_tools:`; with `at:` a `url:` server's `tools/call` passes only as the issue's status allows, read at each call; a `url:` server with `per_run:` starts its tool at the first call once a session has saved its tool list; a `rest` connector takes paths after `--` and makes one GET each under its `url:` (only paths its `get:` allows), in process, one JSON line per path ([Connectors](../departments.md#connectors), [Per run](../departments.md#per-run)) |
| `issue-cli cms up [--port <n>] [--email <addr>] [--title "…"] [--dry-run]` | The owner's one command for the project's site: under `mem-guard check`, writes a loopback Ghost compose outside any repo (`$XDG_DATA_HOME/ai-workflow-engine/cms/<project>/`, an existing one kept), starts it, sets Ghost up with no browser, puts the owner password and the integration's Admin API key straight into Keychain (`ghost-owner`, `ghost-admin-key`, account the project; never printed, in argv or a file), turns outbound link tagging off; safe to rerun; writes no config. Refused for a worker (`--dry-run` is open), off macOS, without docker. `--json`: `{dir, url, email, steps, next}`. See [Stand up the CMS](../stand-up-the-cms.md) |
| `issue-cli page-check <url\|sitemap.xml> [...] [--max <n>] [--max-title <n>] [--timeout <d>] [--delay <d>]` | Check pages against the page rules every marketing department keeps: a `<title>` of at most 60 characters with the brand suffix (counted in characters, entities decoded; none or two fail) and `charset=utf-8` in the Content-Type of every text response (HTML, sitemap.xml, robots.txt, llms.txt). A sitemap or sitemap index is checked, then the `<loc>` of each `<url>` (or `<sitemap>`) in it: image and video extension entries (`image:loc`, `video:loc`) are not fetched, and `--max` counts pages only. Each failing URL gets its rule and fix; exit 1 when one fails. Read-only GETs, one at a time; needs no project or connector; `--json` ([Page rules](../departments.md#page-rules)) |
| `issue-cli reel-legal [<slug>]` | Run a reel's legal check (claims, music licence, AI label, testimonial, consent, trademarks) without moving it: the same `reel_legal_check` the reel's script → render and render → review gates run, over `## Script`, `## Legal check` and the storyboard it names, against the department's `reel-legal.yaml`. One line per finding with its fix, `reel-legal: PASS` or `HOLD`, exit 1 on a hold. Read-only ([Reels](../departments.md#reels)) |
| `issue-cli connector sweep [<dept>]` | Stop `per_run:` tools the engine started and no run holds any more (a killed run). The viewer does this every minute. Never starts one. The owner or the department's lead |
| `issue-cli connector spend [<dept>[/<name>]] [--json]` | This month's spend entries of the paid connectors, each with its id (`s-1a2b3c`), the call, the amount (or `cost unknown`), who, and `from the balance`, `for <id>`, `ref <generation id>` or `removed (<id>: <reason>)` ([Spend caps](../departments.md#spend-caps)) |
| `issue-cli connector spend add <dept>/<name> <amount> [--for <entry-id> \| --ref <generation id> \| --outside] [--note "…"]` | Record a paid connector's spend by hand: by default for the held call whose cost was unknown (it clears the hold on the next billing call), `--for`/`--ref` for a named call, `--outside` for spend made outside the engine. A call already priced is refused, naming the first entry. The owner or the department's lead ([Spend caps](../departments.md#spend-caps)) |
| `issue-cli connector spend remove <entry-id> --reason "…"` | Cancel one spend entry by appending a removal line (the ledger is never edited in place): it leaves the month's total and lifts its hold. The owner or the department's lead |
| `issue-cli connector check [<dept>[/<name>]]` | Run the read-only `check:` commands (20 s timeout, output thrown away) and record ok / failing / missing / no-check in `<issues>/.connectors/checks.jsonl`; the owner or the department's lead; exit 1 when one fails |
| `issue-cli worktree add [<slug>]` | Make the issue's worktree `.worktrees/<slug>` on `work/<slug>` from the main branch (an existing `work/<slug>` checked out as is), leaving the issues tree out as dispatch does, and run the workflow's `worktree_setup` in it once; the last line is the path. A worktree that exists is left as it is (`worktree exists: …`, no setup). The slug defaults to your issue. For projects without `worktree: true`, whose briefing names it. See [Agent Dispatch](../agent-dispatch.md#without-worktree-true) |
| `issue-cli worktree deps [--own \| --shared] [--dir <worktree>]` | A worktree's `node_modules` links to the main checkout's while its lock file is the same (every new worktree gets the links). With no flag: each folder's state (`shared: a link to …`, `its own folder`, `none`). `--own` removes the links (never what they point to) and runs the lock file's install in the worktree (`npm ci`, `pnpm install --frozen-lockfile`, `yarn install`, `bun install`): run it before you add, remove or update a package. `--shared` links again where `node_modules` is missing and the lock file matches. Refused in the main checkout. See [Agent Dispatch](../agent-dispatch.md#per-issue-worktrees) |
| `issue-cli worktrees prune [--dry-run] [--json]` | Remove issue worktrees whose branch is on main (past its dispatch base, main contains it): merged, clean and idle (3 h), the branch kept; lists each kept tree as dirty, unmerged, live, recent or special. The lead's tick runs it daily; workers may only `--dry-run`. See [Agent Dispatch](../agent-dispatch.md#removing-worktrees) |
| `issue-cli projects`             | List configured projects (slug, name, issue dir). `--json` for scripting |
| `issue-cli help [<command>\|<topic>]` | Show the CLI help, or one command's help with its flags (same as `issue-cli <command> --help`), or a topic (`workflow`, `transitions`, `format`, `schema`, …) |
| `issue-cli version`              | Print the issue-cli build version |
| `issue-cli viewer restart`       | Restart the running viewer in its own tmux pane (`respawn-pane -k`, same start command) and wait until it answers; prints the old and new version. The step after every `make install`. See [Restarting the viewer](#restarting-the-viewer) |
| `issue-cli telemetry report`     | Local usage report: calls per command, never-used commands/flags/topics, errors, unknown tokens, retry sequences. See [CLI Usage Telemetry](telemetry.md) |
| `issue-cli telemetry path`       | Print the active telemetry file and whether recording is enabled |

### Slug arguments

Every command that takes `<slug>` resolves it the same way (`findIssueOrErr` in `context.go`):

1. **Exact slug**, case-insensitive; a trailing `.md` or a path under the issue directory is accepted.
2. **Unique suffix** on `/<arg>`, so `sample` finds `cli/sample`.
3. **Unique substring**, so `first-partial` finds `cli/slug-lookup-silently-picks-the-first-partial-match`. The command then prints `note: resolved "<arg>" to <slug> (partial match)` on stderr before it acts.

When a step matches two or more issues, the command fails with exit status 1 and writes nothing:

```
Error: ambiguous slug "sim": 4 issues match:
  sim/a
  sim/b
  …

Retry with the full slug.
```

The list stops at 10 slugs and ends with `… and N more (issue-cli list)`. A system name is almost always ambiguous, so `issue-cli retrospective <System>` fails instead of filing the retro under some issue of that system.

#### A worker's own slug

A dispatched worker's session has `ISSUE_VIEWER_ISSUE_SLUG`, and the commands on its own issue may leave the slug out: `issue-cli check D1`, `issue-cli transition --to testing`, `issue-cli comment "progress: …"`. The commands are `tracker.OwnSlugCommands` (`internal/tracker/own_slug.go`): show/context, checklist, check, uncheck, check-add, check-edit, check-remove, transition, append, replace, comment, comments, decide, prove, rounds, clarify, retrospective, start, trace, critique, check-script, update and ship; `output add` takes it as `--issue` when neither `--issue`, `--department` nor `--job` is given. `affected` takes it when neither a slug nor `--files` is given; `attach` and `evidence add` take it when the first word names no issue (that word is then a file). The first default for an issue prints one line on stderr, `issue-cli: no slug given: using your issue <slug> (ISSUE_VIEWER_ISSUE_SLUG)` (a marker under `$XDG_STATE_HOME/issue-cli/own-slug/` keeps it to once).

- Nothing, or a flag, where the slug goes: the env's slug.
- A command that takes words after the slug (check, uncheck, check-edit, check-remove, comment, prove, retrospective, transition): the first word is the slug when it names an issue exactly or as a unique `/<suffix>`; otherwise it is the first id or word and the env's slug is used (`comment "tests: 5/5 passed"` too). A word shaped like `<system>/<name>` (slug characters only) that names no issue is a mistyped slug and fails as not found, never landing on the worker's own issue.
- Any other command: a first word is the slug, as above.

A slug on the command line always wins. Without the env (the owner's shell, a lead) nothing changes: `<cmd> requires <slug>`. The worker briefing (`briefing.BuildDispatchPrompt`) writes these commands without the slug (`tracker.ShortenOwnSlug`) and names it once, on its first line.

### Flags, `--help` and exit codes

Every command parses its arguments the same way:

- **Flags go anywhere.** `issue-cli list done --status done` and `issue-cli comment <slug> "text" --text more` parse every flag, wherever it is. Before 0.53 Go's flag parsing stopped at the first positional, so a flag after one was silently dropped: `list done --all` listed everything with exit 0. `--` ends the flags; everything after it is a positional, even `--text`.
- **A flag is a word shaped like one:** `-name`, `--name` or `--name=value`. Text that merely starts with a dash (`"- [ ] item"`, `"--- note"`, `-5`) stays a positional.
- **A flag that takes a value needs one.** A string flag written last, before `--`, or before another flag of the command (`check-script --new`, `check-script --new --yes`) fails with `flag needs an argument: --new` (exit 2) instead of taking the `--` that ends the flags, or the next flag, as its value. A word that only looks like a flag and names none of the command's (`-5`) is still a value; `--name=` gives an empty one.
- **A flag quoted with its value is refused.** A word that starts with a flag the command knows, then a space (`memory add "fact" "--supersedes m-1"`), exits 2 and stores nothing: as text it would go in with the flag not acted on. Text that names no flag of the command goes in (`"--force is risky"` on a command with no `--force`). The global `--json` doesn't count. Text that really starts with a flag goes after `--`: `comment <slug> -- "--body is misleading"`.
- **A value with a space needs quotes.** When the stray word follows a string flag's value (`append <slug> --section Test Plan`), the error names the quoted form: `A value with a space needs quotes: --section "Test Plan"`. Any other unexpected positional gets the plain message.
- **Mistakes exit 2.** An unknown flag, a bad flag value, a flag quoted with its value, an unexpected positional, or a flag where the slug belongs fails with **exit 2** and a short message on stderr:

  ```
  Error: unknown flag --al for 'list'
  Valid flags: --all --assignee --category --sort --status --system --type --version

  Run: issue-cli list --help
  ```

  Other failures keep exit 1. A removed flag (`--wait`) also exits 2.
- **An unknown command names the one that does the job.** `issue-cli body <slug>` fails with `unknown command: body`, then `Did you mean: issue-cli show — <its short help>`, then `Run: issue-cli help`. The guess comes from a short hint table for words that name a command called otherwise (`commandHints` in cmd/issue-cli/unknown_command.go: `body` → `show`, `list-comments` → `comments`, `move` → `transition`, …; `TestCommandHintsNameRealCommands` keeps each hint off the registry and pointing at a command), else the one command the name starts, else a command within two edits (one under five letters). With nothing near, no guess.
- **A guessed subcommand, page or flag names the real one too.** `workflow catalog` says `Did you mean: issue-cli workflow catalog-check`, `decision show` names `decisions --issue <slug>` (or `--in-force <slug>`), `job list` names `jobs` (`subcommandHints` in cmd/issue-cli/unknown_command.go; `TestSubcommandHintsNameRealCommands` keeps each pointing at a command). `ref <page> <words>` that finds no heading on that page lists up to three other pages whose heading holds the words (`ref testing catalog` → `issue-cli ref cli catalog`). `spend --context --since` says `--context` counts days back: `--days <n>`. `viewer` says the board is its own binary, `issue-viewer --help`. Each still exits non-zero. The global flags take both spellings, `--project <slug>` and `--project=<slug>` (also `--config=<path>`), before the command. `TestGuessedNamesAnswerWithTheRealCommand` is the table of guesses.
- **The slug comes first.** `issue-cli comment --text hi <slug>` fails with `comment takes <slug> first, then flags: …` instead of looking up an issue called `--text`.
- **`--help` anywhere.** `-h`, `-help` or `--help` in a command's arguments prints that command's help: its description, examples, and a **Flags** section read from the code (one per subcommand for `data`, `ci`, `lead`, `telemetry`, `workflow`). It exits 0 and needs no project, and it is never read as a slug or as text. `issue-cli help <command>` prints the same thing.
- **The global `--json`** also works after the command (`issue-cli list --all --json`). `--project`, `--config` and `--debug` must come before the command; after it they get a hint saying so.
- **`--debug`** (global) prints `debug: config: <path> (<why>)` on stderr, and the lookup's checked list when no config was found.
- **A stale branch build says so.** When the running binary is a branch build (a file named `issue-cli` at the root of a checkout that holds `cmd/issue-cli`, the one the briefing has a worker build) and it is older than that checkout's code (the newest commit under `cmd/` and `internal/`, or the newest Go source or embedded file there, as `check-script` compares), every command ends with one line on stderr: `issue-cli: warning: this ./issue-cli is older than its code (built <time>, <what> changed <time>): this call ran the old code; rebuild: (cd <root> && go build -o ./issue-cli ./cmd/issue-cli)`. The call itself runs and its exit code and stdout are unchanged; the check runs beside it (cmd/issue-cli/stale_build.go). The installed build prints nothing.

### `start`

`issue-cli start <slug>` is the single entry point for picking up an issue. It claims the issue (sets the assignee if unset), prints the checklist, status guidance, and next-transition contract, and is idempotent on re-runs.

From a **handoff status** (`backlog`, `human-testing`) it also *auto-advances* to the next work status when the matching approval is present — `backlog → in progress`, `human-testing → documentation`. The target is the next non-optional status in the workflow, not a hardcoded value; only the handoff set is fixed. Because crossing into implementation from a re-claim is surprising, the advance is **never silent**: it is announced with a prominent `⚠ AUTO-ADVANCED  <from> → <to>` banner that also notes the consumed approval. If the approval is missing, `start` fails without mutating assignee or status, unless [YOLO](../Workflow/yolo.md) is on and would pass the gate: then `start` passes it as `transition` does (the `approval: "YOLO"` marker, the `YOLO: gate … passed` line under the banner), judging it as if already claimed, so an unclaimed backlog issue still advances. From any non-handoff status, `start` only claims and reports `Status unchanged`. The `Status:` line in start's header is the status after that advance, the one `== Next ==` reads from. The backlog clear of the assignee is skipped when the issue's own worker moves it forward (`KeepOwnAssignee`: the identity `ownIssueOnly` checks), so a worker who goes on from design to in progress needs no second `start`; the lead's, the viewer's and any backward move still clear it. A transition refused for a missing assignee (backlog cleared it) points at `issue-cli start <slug>`, which re-claims the issue and moves it on, and does not blame an approval that is in place or that YOLO would pass.

When the approval is missing, the agent stops and ends its turn; the approval message from the viewer resumes it (see [At a human-approval gate](#at-a-human-approval-gate)). The advance is recorded in the stats sidecar like any other transition.

Under the header `start` also prints, so a worker doesn't need a lead message to know them:
- `Base: main at 4d4d4e7 (cut by the dispatch on 2026-10-30)`: the branch and commit the issue's work branch was cut from, as recorded by the dispatch that created it (the stats sidecar's `dispatches[].base_branch`/`base_sha`). No line for an issue never dispatched, or dispatched before dispatch recorded its base.
- `Depends on: cli/a (in progress, branch work/cli/a), cli/b (done)`: each `depends_on` issue with its status, and its `work/<slug>` branch while it is open (where its code is until it ships).

A `== Open branches ==` block (the briefing's `## Open branches`, same lines) says where the issue's branch stands, and is left out when there is nothing to say:
- `Your branch is 3 commits behind main. Merge it before editing anything: git merge main -m "chore(merge): main into <slug>"`: a resumed issue merges main first, so it never edits a stale tree.
- Each open `work/` branch whose diff touches a file the issue's system maps to (`workbench.systems` `paths:`) or a file the issue's own branch already changes, most shared files first, at most 8: `cli/a (in progress): 5 files, 2 also on your branch: …`. Changelogs don't count, and branches of closed issues are left out. Then the `issue-cli ask <sibling> "..."` to agree who changes what before the merge.

An `== Already on main? ==` block (the briefing's `## Already on main?`, same lines; see [Agent Dispatch](../agent-dispatch.md#lead-sessions)) names the commits main made since the issue was created to the Go tests its title and brief name, with the `go test` that checks each. A dispatched session gets it only when the briefing lacks one of its lines (main moved since the dispatch).

#### In a dispatched session

In the session a dispatch started for the issue (`ISSUE_VIEWER_ISSUE_SLUG` names it and `<workdir>/.agent-logs/agent-<slug>/dispatch-prompt.txt` is there), `start` prints only what that briefing lacks: the header, the claim, the checklist, `== Next ==` and the entry guidance, then what came since it was written: `== Comments since your briefing ==` (comments dated after the prompt file), `== Decisions since your briefing ==` (refs neither the briefing's sidecar record nor its text carries, logged as start's injected event) and `== Memories your briefing lacks ==` (one line each, cut to 160 runes; `memory show <id>` has the rest). The status guidance, entry guidance and open-branch lines the briefing already holds, its decisions in force, its pinned memories, the tick hint and a Local CI line it shows with the same state are left out; a last line says so. `start <slug> --full` prints everything, as `start` does outside a dispatch. The briefing's step 2 no longer runs `show`: it already embeds the body, and `show` is for re-reading after a comment or a restart. Measured on its own issue: start 12.3k → 4.5k chars, and the 8.9k of `show` drop out of the first two turns.

#### The ship status

The **ship status** is the last status on the issue's path before its first closed status: `shipping` on the development template, `documentation` on a path that goes `documentation → done`. When `start` prints the guidance of the ship status, or `transition` moves an issue into it, a `== Shipping ==` block follows the static guidance with what it can't know:

```
== Shipping ==
Main checkout: /home/me/repo on main
Ships into main: fast-forward it in the main checkout (issue-cli ship merges, checks and fast-forwards).
Ships after cli/a (in progress)
⚠ cli/a is not done: fast-forward only after it ships, or ask the lead.
```

When the main checkout (the first `git worktree list` entry) has another branch checked out than the issue's recorded base (with no base recorded: anything but `main` or `master`), the second line becomes `⚠ The main checkout has <branch> checked out, not <base>. Stop and ask the lead which branch this issue ships into before fast-forwarding.` A detached HEAD shows as `detached at <sha>`.

### `append`

`issue-cli append <slug> --body "..."` adds content to the issue body. Two routing modes:

- **Default** — content is appended after the existing body. Headings inside `--body` must be unique against the issue.
- **Section** — pass `--section "Name"` to append into an existing section (or create it if missing). Use `--force` to disambiguate when the same heading exists at multiple levels. When the body's first line is a heading with that name (`--section Design --body "## Design\n- [ ] x"`), it is dropped, so the heading is not written twice. `--section` matches a heading at any level (`--section Automated` finds `### Automated`); a transition's `append_section` matches only `## Name` (see [Workflow](../Workflow/overview.md#gate-sections-append_section)). A sub-heading in the body that the section already has at that level (`### Automated` into a Test Plan that holds one) is not written again: its lines go at the end of that sub-heading's text, text before the body's first sub-heading goes before the section's first one, a sub-heading the section lacks goes at its end, and a block already there word for word is skipped. The success line names the sub-headings it merged into; an append that still leaves a heading twice in one place (same level and title under the same headings, such as a sibling's `## Notes` inside a `--section Design` body) warns on stderr.

If `--body` starts with a heading that is already present in the issue (and the rest contains only deeper subheadings), the command auto-routes into that section. This means agents drafting `## Implementation\n…` style appends do not have to retry with `--section` after a duplicate-heading failure. Unlike `--section`, the block's level counts: it goes to the section of that name at its own level, or, with none there, a shallower one. A deeper heading of the name is a different section, so `## Clarify` on an issue with only a `### Clarify` under Design becomes a new `## Clarify`.

The duplicate-heading guard still fires when `--body` introduces a *peer* heading that collides (e.g., `--body "## New\n…\n## Existing"`); pass `--section` to disambiguate. Two sections the block could belong to are refused the same way (`--section` with `--force` takes the first).

#### Fix one line (`--replace`)

`issue-cli append <slug> --section S --replace "<old line>" --body "<new line>"` replaces the one line of S that reads `<old line>`, instead of adding a correction paragraph. The line is matched whole and literally (spaces at either end aside; backticks and brackets are plain text), through [`replace --match`](#replace): no match or several are refused with the matching lines numbered, nothing written. It needs `--section` and takes no `--force`; the new text is one line. The success line of every `append` names this form.

#### Body from a file or stdin (`--body-file`)

`--body "..."` puts the body inside a shell-quoted argument, so backticks and parentheses are interpreted by the calling shell *before* `issue-cli` runs — `` `Foo.Bar` `` becomes a command substitution and `(...)` triggers a parse error. Inline code spans and parentheticals are normal in design bodies, so this hits constantly.

Pass `--body-file <path>` (or `--body-file -` to read **stdin**) to deliver the body as raw bytes that never pass through a shell word:

```bash
issue-cli append <slug> --section "Design" --body-file design.md
cat design.md | issue-cli append <slug> --section "Design" --body-file -
```

Notes:

- File/stdin content is used **verbatim** — unlike `--body`, it is not run through `\n`-escape normalization, so literal backslash sequences in code samples are preserved.
- `--body`/`--text` and `--body-file` are mutually exclusive; supplying both is an error.
- The same `--body-file`/`-` support applies to `issue-cli comment <slug>`, `create` and `update`.

### `attach`

`issue-cli attach <slug> <file>... [--section <name>] [--alt <text>] [--json]` puts screenshots, or any other file (a CPU profile, `pprof -top` output, a log), on an issue without a running viewer. Each file is saved where the viewer's upload saves pasted images, `<issues>/attachments/<hash>.<ext>` (the first 16 bytes of its sha256), through the same code (`tracker.SaveAttachment`), so the same bytes always get the same name. Stdout carries one line per file, `![<alt>](/p/<project>/attachments/<name>)`, ready to paste or pipe into `append --body-file -`; the link shows on the issue page at once, because attachments live in the main checkout's issues folder, not on your branch.

- An image is PNG, JPEG, GIF or WebP sniffed from the bytes. Any other file is a **file attachment**: saved as `<issues>/attachments/<hash>/<name>` (`tracker.SaveAttachmentFile`), its name kept so `go tool pprof cpu.pprof` reads as written, cleaned to ASCII letters, digits, `.`, `_` and `-`; its line is a plain link, `[<name>](/p/<project>/attachments/<hash>/<name>)`. The viewer shows a text file (valid UTF-8, no NUL byte) as `text/plain` in the browser and serves anything else as a download, never as a page ([Security](../API/security.md)). A non-image named like one (a `.png` holding HTML) is refused. At most 10 MB each.
- A file must resolve (symlinks followed) inside your worktree (the git top level of the working directory), the project root or the temp folder (`$TMPDIR`, `/tmp`, where an agent's scratchpad lives). Anything else, such as the owner's Desktop or Downloads, is refused with the folders it accepts: copy the file into your scratchpad first.
- One bad file refuses the whole call (exit 1, `✗ <file>: <why> (nothing saved)`) and saves nothing.
- `--section <name>` also appends the links into that section, as `append --section` does; the `✓ Appended` note goes to stderr. `--alt` sets every link's alt text (default: the file name without its extension). `--json` prints `[{file, kind, name, url, markdown}]`, `kind` being `image`, `text` or `file`.

```bash
issue-cli attach <slug> "$SCRATCH/board-390.png" --section "Test Plan"
issue-cli attach <slug> "$SCRATCH/cpu.pprof" "$SCRATCH/top.txt" --section "Test Plan"
```

### `check`

`issue-cli check <slug>` ticks checkboxes. The preferred form is **ids, several at once**:

```bash
issue-cli check <slug> D3 D4 AC1                      # by id — several in one call
issue-cli check <slug> "Design#3"                     # long-form id
issue-cli check <slug> --section "Design" --all       # every open box in a section
issue-cli check <slug> "Code changes complete"        # by text (substring, case-insensitive)
issue-cli check <slug> --section "Design" --index 2   # by section + stable index
issue-cli check <slug> --index 5                      # by position in the whole body
```

Flags may come before or after the ids or text.

While the `criteria_proven` gate covers the issue, acceptance criteria can't be ticked here: they are ticked by
their proof (`issue-cli prove`, see [Proof Links](../Workflow/proof.md)), and `check` refuses them with a pointer
to `prove <slug> --list`.

#### Checkbox ids

`checklist`, `show`, `start`, and the checklist printed after `transition` show an id in front of every box, grouped by section, followed by a hint when boxes are still open:

```
== Checklist (3/8) ==
## Design
  D1 [x] Approach documented
  D2 [ ] Dependencies identified
## Acceptance Criteria
  AC1 [ ] Parser handles empty input
## Documentation
  Do1 [ ] Docs updated
Tick done boxes by id, several at once: issue-cli check <slug> <id> [<id>...]
```

- An id is the section's **initials** (first letter of each word, uppercased) plus the box's **1-based index within that section**: `D3` is the 3rd box under `## Design`, `AC2` the 2nd under `## Acceptance Criteria`, `TP1` the 1st under `## Test Plan`.
- When two sections share initials, the section that holds a checkbox **first in the document** keeps the short form. A later one extends its first word until it is unique: with `Design` holding `D`, `Documentation` becomes `Do` and `Deployment` `De`. In the default workflow `## Idea` holds boxes and claims `I`, so a later `## Implementation` is `Im`.
- Ids are **stable**. Indexes count checked and unchecked boxes, workflow transitions append sections at the end, and a box removed with `check-remove` keeps its slot, so a box's id never changes as work progresses. Rewriting a section's boxes with `replace` does renumber them.
- Ids are case-insensitive (`d3` = `D3`). The long form `<Section>#<n>` (`Design#3`, `"Acceptance Criteria#2"`) always works.
- Boxes before any `## ` heading have no id and show as `1.`, `2.` under `## (no section)`; their number names them (`check <slug> 2`, `#2` or `2.`), alone or beside ids (`check <slug> D1 1 2`). A number past them is an unknown id, and with no such boxes a number stays a text query. A section whose heading gives no initials (`## 2026`) also lists `1.`; its long form `2026#1` reaches those.
- Boxes inside fenced code blocks are illustrative, not workflow state. They get no id and are not counted by `checklist`, the progress line, or transition gates.
- Don't write the id into the box text. If a box's text already starts with its own id (`- [ ] TP4: run the suite`), the id is printed once (`TP4 [ ] run the suite`), not twice. The file keeps the text as written; `check-edit` drops a leading copy of the id when it rewrites a box.

#### Behaviour

- **Several ids are all-or-nothing.** If any id doesn't exist (`check <slug> D2 D9`), nothing is ticked: `check` prints `No checkbox with id D9 — nothing was ticked.`, lists the boxes, and exits non-zero. Duplicate ids are collapsed. A box that is already checked is reported as `Already checked`, not as an error. All boxes are ticked in one locked write.
- **The owner's boxes among other ids are skipped, not the whole call.** From an agent session, `check <slug> HT1 Do1 Do2` ticks Do1 and Do2 and prints `✗ Not ticked, the owner's: HT1 <text> (<why>)` per owner box, then the Owner checks page; it exits non-zero only when nothing was ticked, with the full `these boxes are the owner's` refusal. Each box is tested alone with the same guard the write runs (`ownerTicks` in cmd/issue-cli/cmd_uncheck.go), which also backs the whole write.
- **Criteria among other ids are skipped, not the whole call.** Under the proof gate, `check <slug> D1 D2 AC3` ticks D1 and D2 and prints `✗ Not ticked: AC3 is ticked by its proof, not by hand (issue-cli prove <slug> --list)`; it exits non-zero only when nothing was ticked (criteria alone, or every other box already done). Unknown ids still refuse the whole call.
- **Id or text?** Positional args are read as ids when every arg is shaped like an id (or a loose box's number, `2` or `#2`) and at least one names a box or section of this issue. Otherwise they are joined into a text query, so a word like `phase1` (no section abbreviates to `PHASE`) is still matched as text, and so is unquoted multi-word text.
- **`--section X --all`** ticks every open box in that section. It cannot be combined with ids, text, or `--index`. On a complete section it prints `Nothing to check: section "X" already complete (n/n)` and exits 0. A section with no boxes is an error.
- **Text queries** match a box whose label contains the query. If it matches **more than one unchecked box**, `check` errors, lists every candidate with its id and `[Section #index]` label, and suggests `issue-cli check <slug> <id>`.
- **Output.** Every form prints one line per box, then overall progress plus the progress of each touched section:

  ```
  ✓ Checked: D2 [Design #2] Dependencies identified
  ✓ Checked: I1 [Implementation #1] Parser added
    Already checked: D1 [Design #1] Approach documented
    Progress: 3/11 (Design 2/2, Implementation 1/2)
  file: …
  ```

`checklist --json` emits an `items` array (`id`, `section`, `index`, `text`, `checked`) alongside the `total`/`checked` counts. `transition --json` checklist items carry the same `id`, `section` and `index` fields.

#### Transition gate failures

When a `section_checkboxes_checked` (or `all_checkboxes_checked`) gate blocks a transition, the error says how many boxes are **still open**, lists each one with its id, and gives the command to tick them:

```
Error: failed to transition: 2 of 4 boxes still open in section "Implementation":
  I2   Automated tests added or updated where practical
  I4   Changelog line drafted

Tick the ones that are done (ids, several at once):
  issue-cli check <slug> I2 I4
```

Before v0.30.0 this read `2/4 checkboxes incomplete`, where 2 was the number of *checked* boxes.

The engine stops at the first unmet rule, but the refusal names the rest too, so one call shows everything the move waits on:

```
Also unmet for this move (1 more):
  - [validator] Validate section Test Plan checkboxes are checked (gates this transition)
      1 of 1 box still open in section "Test Plan":
      TP1  Unit tests
      Tick the ones that are done (ids, several at once):
      issue-cli check <slug> TP1
```

It lists them as a dry run's report does (gate commands aren't run again for it), so there's no need for a `--dry-run` before the move.

When boxes the agent may tick are all that is open, the refusal (and a dry run's report) ends with the one call that ticks them and moves:

```
Tick them and move in one call:
  issue-cli transition <slug> --to testing --check I2 I4
```

No line is printed when anything else is open: an owner box, a criterion its proof ticks, a missing approval YOLO wouldn't pass, a field or another rule. A gate command (`command_succeeds`) isn't re-run to decide; the one call runs it.

#### One call: tick and move

`issue-cli transition <slug> --to <status> --check <id> [<id>...]` ticks the boxes as `check <slug> <ids>` does, with its output and its refusals, then runs the transition. Ids follow one `--check`, comma-separated (`--check I2,I4`), or with the flag repeated; a box above any section goes by the number checklist lists for it, beside section ids too (`--check 1,I1`, `--check #2 I1`).

- **A refused tick moves nothing.** An unknown id, an id that names no section (`--check` takes ids only, not box text), or ids that tick nothing (only owner boxes or criteria) refuse the call with check's message and `Nothing was moved: the issue stays at <status>`; nothing is ticked.
- **Owner boxes among the agent's** follow check's rule: the agent's tick, each owner box gets its `Not ticked, the owner's` line (`Would not tick, the owner's` under `--dry-run`), and the move goes on; its gates decide.
- **Criteria under the proof gate** follow check's rule: skipped with a `Not ticked` line among other ids, refused alone.
- **A move refused after the tick** leaves the boxes ticked (`The --check boxes stay ticked`): the agent said they are done.
- **`--dry-run --check`** previews the move as if the ids were ticked, with the same refusals, and writes nothing.
- **An id outside the sections the move waits on** gets a line naming its section: ids are claimed by section in order, so at in progress `I2` is Idea's box (Implementation's are `Im1`, `Im2`, …): `⚠ I2, I3 ticked in ## Idea; this move waits on ## Implementation (open: Im1 Im2)`. Ids in a gated section, criteria, and any id once the gated sections are all ticked say nothing.
- Under `--json` check's lines and that note go to stderr; stdout stays the transition's JSON.
- The checklist printed after a move says `(add --check <id> [<id>...] to tick the last boxes and move in one call)` under `== Next ==` while boxes are open. Telemetry counts the flag as `check`.

### `check-add`

`issue-cli check-add <slug> --section "<name>" --text "…"` adds one open box to a `## ` section (any case) and prints its id. It goes right after the section's last box (or removed-box slot), with that box's indentation, so it takes the next index and every other box keeps its id; in a section with no boxes it goes after the last line. Hints: `add-check`, `checklist-add`, `add-box` and `box-add` name it.

```
✓ Added AC5 [Acceptance criteria #5]: Refuses an empty name
  The other boxes keep their ids.
  Tick it when done: issue-cli check <slug> AC5
file: …
```

`--json` prints `{"id", "section", "index", "text"}`. An unknown section is refused with the issue's sections (`append --section` starts a new one). The text must be one line; a leading copy of the new id is dropped. A first box in a box-less section can move the short ids of the sections after it (`Docs` claiming `Do` before `Done`): that add is refused, naming the id that would move. From an agent session it refuses a box the owner ticks, as `check-edit` does: text marked `(owner)`, or a box in `## Human Testing`.

### `check-edit`

`issue-cli check-edit <slug> <id> --text "…"` (or `check-edit <slug> <id> "…"`, the quoted text after the id) replaces the text of one checkbox. The id is the short or long form `check` takes (`D3`, `"Design#3"`). The box keeps its tick state, indentation and position, so its id doesn't change:

```
✓ Edited D3 [Design #3]
  was: Overlap reviewed
  now: Overlap reviewed: similar features listed
file: …
```

An unknown id lists the issue's ids. The text must be one line, given once: both `--text` and a text after the id, or more than one word after the id, are usage errors. From an agent session it refuses an edit that writes an N/A into the owner's box or drops its `(owner)` marker; rewording that keeps the marker passes.

### N/A boxes (`check --na`)

Some boxes don't fit the change: a test-only fix has no playtest session, a tmux bug has no page for ux-test. `issue-cli check <slug> <id> --na "<reason>"` ticks one box and writes the reason into it as `(N/A: <reason>)`, replacing an earlier N/A in the text (such as the template's own "(or N/A: …)"). The reason must be in a named form (`tracker.NAForms`; `check --help` lists them):

| Form | When |
|:--|:--|
| `the repro is an automated test (<TestName>)` | a test-only fix, or a bug whose repro is a go test |
| `no page changes` | a CLI, tmux, runner or docs change: nothing a playtest session or ux-test can see |
| `runner-internal, covered by <TestName>` | playtest runner, CI or check library internals |
| `covered by playtest session <name>` | the guided-check box when a playtest session drives the manual check |
| `cli flow covered by <TestName>` | a CLI-only change whose flow a go test walks: no page, no check script |
| `page state a scenario can't build (<what>), shown by <evidence>` | a page change a scenario board can't reach (git state, live agents): say what, and what shows it instead |
| `no git remote` | a repo with no git remote: Shipping's push and PR boxes (the move into shipping ticks them itself) |
| `<why, in a few words>` | anything else |

It refuses (nothing changes, it exits non-zero and the message starts with `✗ not ticked:`, then why): an empty reason, a `<placeholder>` left in it (the message names it, such as `<project>`), a named form without its test or session, a free reason under three words (the message gives the count and the minimum), more than one id, an unknown id, a `## Shipping` box (shipping steps have no N/A: the message says to tick it plainly when done). A `## Shipping` box whose text ends in "or none" is the exception: `--na` ticks it with its text unchanged and posts the reason as a `progress: <id> ticked as none: <reason>` comment (an empty reason is still refused). A push or PR box (its text names a push or a PR, not to forbid one) takes `--na "no git remote"` when the project's repo has no git remote; the transition into shipping already ticks those boxes itself on such a repo, with `(N/A: no git remote)`, and says so in its side effects (`No git remote: ticked S2, S3 …`). A repo with a remote, or a project folder git can't read, keeps the plain-tick rule. It refuses the owner's boxes for anyone, owner included: a `## Human Testing` box, a box marked `(owner)`, a criterion only the owner proves, a box a guided check left to the owner. Those wait for the owner, or under YOLO in the review queue.

```
$ issue-cli check cli/x T4 --na "no page changes"
✓ Checked: T4 [Testing #4] `make ux-test ISSUE=<slug> REPEAT=2` passed, run once before handoff (N/A: no page changes)
```

Lint check `na-boxes` (`issue-cli health lint`) reads the same rules over ticked boxes on open issues.

### `uncheck` and `check-remove`

`issue-cli uncheck <slug> <id>...` turns ticked boxes back into open ones. `issue-cli check-remove <slug> <id>...` removes boxes. Both take the ids `check` takes, several at once, and both are all-or-nothing: an unknown id changes nothing and lists the issue's ids.

```
✓ Unchecked: D2 [Design #2] Dependencies identified
  Already open: D3 [Design #3] Overlap reviewed
  Progress: 4/11
file: …
```

A removed box is replaced by a hidden line that keeps its slot, `<!-- removed AC6: <text> -->`. Every other box keeps its id: after `check-remove <slug> AC6`, AC7 is still AC7, and the `Proves: AC7` trailers already on commits still prove it. A removed id is never reused (the next box added to the section gets the next number). Links that proved the removed box are listed and show as strays in `prove --list`.

```
✓ Removed: AC6 [Acceptance criteria #6] First observable requirement
  The other boxes keep their ids.
  Progress: 9/16
file: …
```

Refusals (nothing changes):

- `check-remove` refuses a box the workflow's `append_section` wrote into a section a gate waits on (`section_checkboxes_checked`, such as Design or Implementation), because removing it would get round the gate. Tick it, or say why it doesn't apply with `check <slug> <id> --na "<reason>"` in a named form (below). The author's own boxes in that section can go.
- From an agent session (a lead, a worker, or any Claude Code session), both commands refuse the owner's boxes: a `## Human Testing` box, a box marked `(owner)` (anywhere, or starting `Owner visual check …`), a criterion only a playtest judgement step proves, and a box a guided check left to the owner. The owner, in their own shell, can change them.
- Ticking is refused the same way: from an agent session, `check` (ids, text, `--index`, `--section … --all`) and `append`, `replace` and `update --body` that would write `[x]` into one of the owner's boxes (or add one already ticked) refuse with `these boxes are the owner's; <who> can't tick them, and nothing changed:`, the boxes with why, and the Owner checks page. Among other ids an owner box is left open with a line and the rest tick; a call that ticks nothing refuses. Listings mark these boxes `HT1 [ ] (owner's) …` (`checklist`, `show`, `start`, a transition's checklist, check's own lists; `checklist --json` and `transition --json` items carry `owner: <why>`), with one line under the list naming the Owner checks page; the marker reads the same report the guard does (`ownerReport`). The rule is one function, `internal/proof/ownerbox.go`; see [Proof Links: Agents never tick an owner box](../Workflow/proof.md#agents-never-tick-an-owner-box).

`uncheck` withdraws a box's N/A: a closing `(N/A: …)` (or `— N/A: …`) goes with the tick, so a later tick doesn't claim it. The template's offer `(or N/A: …)`, a claim with words after it and `N/A:` in the box's own sentence stay, and an open box is left as it is.

`uncheck` on an acceptance criterion whose proof still passes notes that the proof will tick it again. `issue-cli prove <slug> <id> --clear` drops its prove links; a trailer stays on its commit.

`checklist` names the proven criteria next to the count: `Acceptance criteria are ticked by their proof, not by hand: 2/7 proven (AC1 AC3).`

### `replace`

`issue-cli replace <slug> --section S --body "…"` replaces the body of a section. `--body-file <path>` (or `-` for stdin) reads the new content as raw bytes, as [`append`](#body-from-a-file-or-stdin---body-file) does; it can't be combined with `--body`. To change **one line** of it instead:

```bash
issue-cli replace <slug> --section "Design" --line 3 --text "- Risk: none"
issue-cli replace <slug> --section "Design" --match "^- Risk:" --text "- Risk: none"
```

- `--line N` counts from the line after the heading, blank lines included (1-based).
- `--match RE` (Go regexp) must match exactly one line of the section.
- When the line can't be chosen (out of range, no match, several matches), nothing is written and the error prints the section with line numbers, so you can retry with `--line`.
- `--line` and `--match` can't be combined. The replacement is a single line. A heading that appears twice is an error.
- A whole-section replace whose new body lacks a `###` heading the old section had still writes, then prints `⚠ the new body drops `### Automated`, `### Manual`, which the old section had`. On `Test Plan` it adds that `has_test_plan` needs `### Automated` and `### Manual`, the check the next transition would otherwise be the first to report.
- A whole-section replace whose new body lacks a checkbox the old section had (matched by text; a re-ticked or moved box still counts as kept) also still writes, then prints `⚠ the new body drops 2 boxes the old section had; I1 I2 no longer name them:`, each dropped box with its old id, tick and text, and `put one back: issue-cli check-add <slug> --section Idea --text '<text>'`. Without it, prose over `## Idea` or `## Design` lost the template boxes silently and the next `transition --check I1` failed as an unknown id.
- **The design gate, as you save.** On an issue at a status a `criteria_checkboxes` gate leaves (in design → backlog), a `replace` or `append` that changes the criteria section or `## Must not` prints what that gate would say about it: `⚠ the backlog gate would refuse: criteria_checkboxes: …` for a plain bullet or sub-bullet (each named) or a template box, with the fix; `⚠ the backlog gate would warn: …` for a criterion no test could fail or a Must not line with no `→ ACn` (a warning at the move too, never a refusal). An edit to other sections, or that leaves both clean, prints nothing more.

### `comments`

`issue-cli comments <slug>` prints the comment trail without the body:

```
== Comments on cli/sample (2 of 4) ==
#2 · 2026-10-30 · agent-cli-sample · question · done
    question: which flag?
#4 · 2026-10-31 · lead-dark-factory · question
    question: and the docs?
```

- `#<id>` is the comment's id, the number the issue page, `comment edit` and `question:` replies use.
- The kind is the word before the first `:` (`question`, `progress`, `tests`, `approval`, …), none when the text starts with no such word.
- `--since` takes a day (`2006-01-02`, from its midnight local time) or an RFC3339 time; `--author` matches the author ignoring case; `--kind` is repeatable or comma-separated; `--open` drops comments marked done. Filters combine.
- `--json` prints an array of comments with `id`, `date`, `text`, `status`, `source`, `author`, `kind`, `open` and `edits`; `[]` when none match.

### `update`

`issue-cli update <slug> --title "…"` changes an issue's title and **keeps its slug**. An issue's slug comes from its title, so before 0.53 a retitle moved the issue to a new slug, and links, `parent:`/`depends_on:` references and agents holding the old slug broke. Now the old slug is written to a `slug:` frontmatter field (see [Issue File Format](../issue-format.md)), which wins over the title:

```
✓ Retitled: cli/old-name (slug kept: pinned with slug: in frontmatter)
  To let the slug follow the title: issue-cli update cli/old-name --title "New name" --rename
```

`--rename` opts into the old behaviour. It removes the pin, renames the file to the new slug, and lists the issues whose `parent:` or `depends_on:` still name the old slug. It doesn't rewrite them. If a file with the new name already exists, nothing is written. The title editor on the detail page keeps the slug the same way, and never renames.

`--body "…"` replaces the whole body (frontmatter and comments are kept); `--body-file <path>` (or `-` for stdin) reads the new body raw, as [`--body-file`](#body-from-a-file-or-stdin---body-file) does for append and create.

`--parent <epic>` sets `parent:`, the epic the issue belongs to; `create --parent` does the same for a new issue. The slug must be an epic on the board (`type: epic`, or already another issue's parent), and is written in the board's spelling; a missing slug, an issue that is no epic and the issue itself are refused and nothing is written. To clear it: `issue-cli set-meta <slug> --key parent --clear`.

### `list`

`issue-cli list` filters by `--status`, `--system`, `--assignee`, `--version` and, in projects with types of work, `--type` (untyped issues count as `default_type`; rows then show a type column), and `--parent <epic>`: the issues whose `parent:` (or legacy `umbrella:`) names that epic, whatever their version. A slug that is neither an issue nor anyone's parent is an error, not an empty list. `show` on an epic prints the same children on a `Children (N): <slug> (<status>), …` line.

When the project sets a `version:`, `list` shows only that version by default, and the footer says how many issues that hid: `12 issues (version 0.53; 40 more in other versions: issue-cli list --all)`. **`--all`** drops that default and lists every issue in every status and version. Filters you pass still apply (`list --all --status done`).

With `--json`, each entry is the full issue plus two scoring fields:

| Field            | Type                                | When populated                                                                                  |
|:-----------------|:------------------------------------|:------------------------------------------------------------------------------------------------|
| `Score`          | `float` (or `null`)                 | `workflow.yaml` has `scoring.enabled: true` and the issue contributes to at least one component |
| `ScoreBreakdown` | `{Total, Components[]}` (or `null`) | Same as above. `Components` is an ordered list of `{Name, Points, Detail}` entries              |

Both fields are `null` when scoring is disabled or the issue has no scoring inputs (no priority, no `due`, no `created`, no scored labels, no `score_boost`). The breakdown matches what the web viewer renders into the `⚡N` badge — same `tracker.ComputeScore` is the single source of truth.

`--sort score` orders the output by `Score` descending. When scoring is enabled and `default_sort: score_desc` is set in `workflow.yaml`, the sort is applied automatically with no flag.

```bash
issue-cli list --json | jq '.[] | select(.Score != null) | {Slug, Score}'
issue-cli list --sort score --status open
```

### `close`

`issue-cli close <slug> --duplicate-of <other>` (or `--fixed-by <other>`) parks an issue that another one covers, from any status, in one step:

- runs the workflow's park transition (the side status every status can move to, `parked` in the bundled templates), answering its required fields with "Duplicate of <other>." / "Fixed by <other>.";
- sets `duplicate_of: <other>` or `fixed_by: <other>` on the closed issue;
- comments `closed: duplicate of <other> (was <status>)` on it and `closed: <slug> as duplicate of this issue` on the other;
- changes no checkbox on either issue, and prints the undo (`issue-cli transition <slug> --to "<previous status>"`).

The rule is one sentence, the same in the worker brief, `close`'s help and its refusal (`tracker.CloseRule`):

> Who closes: the lead and the human close any issue; a worker closes only duplicates of its own issue (--duplicate-of or --fixed-by its own slug), when its lead asks. Moving any other issue stays the lead's job.

Any other worker, or a session whose issue is unknown, is refused and nothing changes. A worker refused `transition <other> --to parked` is pointed here.

It refuses, changing nothing: neither flag or both, the issue naming itself, an unknown issue, an issue already done or parked, a workflow without a park status.

### `transition`

`issue-cli transition <slug> --to "<status>"` runs the workflow engine — same path the board uses for drag-and-drop, so behavior matches. A `--to` naming the status the issue is already at (often after `start` auto-advanced it) changes nothing and exits 0 with `Status is already <status>: nothing changed.` and the next transition; `--json` prints `{"slug", "status", "already": true}`.

When a transition declares `fields[]` with `required: true`, supply answers in either of two ways:

```bash
# Inline: repeatable --field key=value
issue-cli transition <slug> --to "waiting-for-team-input" --field waiting="design review"

# Or set the frontmatter ahead of time; the validator reads it at transition time
issue-cli set-meta <slug> --key waiting --value "design review"
issue-cli transition <slug> --to "waiting-for-team-input"
```

`--field` only applies to the in-flight transition. `set-meta` persists the value, so subsequent transitions and views see it. Section-targeted fields (`target: section:<Title>`) ignore frontmatter and always need a fresh `--field` answer because they append a new line to the body each time.

#### The move line, first and last

A transition that moves prints `✓ <from> → <to>` as its first line, before the lead's comments, `--check`'s lines, the side-effects, the memories for the new status and the trace line, and prints it again as its last line, so `| tail -1` still shows that the issue moved. A refused transition, a dry run and `Status is already …` print no `✓ <from> → <to>` line; `--json` is unchanged.

#### The lead's comments since your last move

`transition --dry-run` and a refused `transition` print, before anything else (a move: right after its first `✓ <from> → <to>` line), every `lead:` comment (text starting with `lead:`, any case) posted since the issue's last recorded move in its stats sidecar, every one when no move is recorded, leaving out the caller's own:

```
== From the lead since your last move ==
  #4 2026-11-14 lead-dark-factory: lead: also cover the --json path
```

`--json` carries them as `lead_comments[{id, date, author, text, answered}]` on both outputs (left out when none). After a real move, each one with no later comment from someone else adds `⚠ lead comment #<id> has no reply after it: answer it with issue-cli comment <slug> "progress: …"`. Nothing refuses over a lead comment. This is how scope a lead adds after dispatch reaches the worker ([The Lead Session](../patterns/lead-session.md)).

#### Checking requirements (`--dry-run`)

`issue-cli transition <slug> --to "<status>" --dry-run` evaluates every requirement of the transition at once and changes nothing. Without it, an agent finds unmet requirements one failed call at a time. Each problem has a kind and, where possible, a fix command:

```
== Dry run: in design → backlog ==
✗ 2 unmet requirement(s):
  - [validator] Validate section Design checkboxes are checked (gates this transition)
      1 of 2 box still open in section "Design":
      D2   Dependencies identified
      Tick the ones that are done (ids, several at once):
      issue-cli check cli/sample D2
  - [approval] Must be human-approved for "backlog" in the issue viewer
      → a human approves at http://localhost:8080/p/demo/issue/cli/sample#approve-backlog
      → tell the human what to approve, then stop and end your turn; the approval nudge resumes you
Nothing was changed.
Skip --dry-run next time: transition --to <status> --check <ids> ticks and moves in one call, and a refusal prints this same list.
```

The last line, ready or not (text only, never in `--json`), points at the one call: a real transition runs the same checks and its refusal prints the same list, so a dry run first is a turn that re-reads the whole context for nothing (`spend --turns` counts them as `dry_runs`).

Kinds are `order` (not a legal next step), `field` (a required `--field` answer is missing), `validator`, `approval`, and `hold` (a red-round cap question the real transition would refuse on: open on a check this transition runs, or a check at the cap it would post first; see [Red-round cap](../workflow.md#red-round-cap)). In text output a `→` fix line is left out when the validator's message already contains that command. JSON always carries it in `fix[]`. When nothing is unmet, the output is `✓ Ready: <from> → <to>` plus the `Will:` side-effects and that hint. It exits 0 when ready and 1 otherwise. `--json` returns `{dry_run, ready, from, to, slug, problems[{kind, requirement, message, fix[]}], side_effects, warnings, lead_comments}`.

Into the [ship status](#the-ship-status), each `depends_on` issue that isn't done adds a warning: `⚠ depends_on cli/a is testing, not done: this issue ships after it.` (`warnings[]` in JSON). A warning never makes the transition unready or changes the exit code; the real transition prints the same facts in its `== Shipping ==` block and moves the issue.

It is built on `WorkflowConfig.PreviewTransitionAll`. That is the engine behind the viewer's transition preview, without the stop at the first failure, so the CLI and the viewer agree on what is missing.

#### At a human-approval gate

Agents don't block at an approval gate. They tell the human what to approve, then stop and end their turn. When the human approves in the viewer, the approve handler sends a message into the agent's tmux session (see [Human Approval Notifications](../agent-dispatch.md#human-approval-notifications)), and the agent re-runs the command.

`--wait`, `--timeout` and `--interval` were removed from `transition` and `start` in v0.49.0. Passing one changes nothing and fails with:

```
Error: --wait was removed in v0.49.0: stop and end your turn; the approval nudge resumes you
```

| Exit code | Meaning |
|:--|:--|
| 0 | Transitioned (or, with `--dry-run`, would succeed) |
| 1 | Error or unmet requirement; nothing changed |

The CLI gives this guidance itself, so it does not depend on any project's `workflow.yaml`:

- When the next step needs a human approval, the `== Next ==` block from `transition` and `start` prints the plain command. That is `start <slug>` from a handoff status, otherwise `transition …`. A one-line note follows it: tell the human what to approve, then stop and end your turn. It is never shown for `done`. `--json` carries this as `next_command` / `next_command_note`.
- After a move to the last status (`done`), `transition` prints `== Done ==` and the local CI line, never a `== Next ==` or the guidance of a side status like `parked`. `--json` carries `"done": true`.
- A move to `done` also archives the issue's playtest runs (see [`playtest prune`](#playtest-prune)) and says so in one line: `✓ playtest: archived 2 of 9 runs, deleted 70; the latest of issue-tab and issue-tab-phone (3.8 MB) moved to .playtest/runs/, 110 MB deleted` (N of the issue's own M runs archived, K runs deleted in all). Archiving none of a non-zero M prints `⚠ playtest: archived 0 of M runs, …` instead: the issue's Evidence tab would have no playtest. Nothing is printed when the worktree has no runs; a problem prints `playtest: <error>` and never fails the transition.
- A missing-approval error from `transition` or `start` ends with the approve link and tells the agent to stop and end its turn, and to re-run only after the human says it is approved.
- `--dry-run` lists the approval with the approve link and the same stop-and-end-turn fix.
- When the same command fails 3+ times in a row on a missing approval, the retry hint says retrying cannot help and the agent should stop and end its turn, instead of the generic "try a different approach".

Questions for the owner go in the design's clarify round (`## Clarify`, below). Outside it, a `question:` comment is for a blocker only. Answers are recorded as decisions.

### `clarify`

`issue-cli clarify <slug>` prints the issue's clarify round (format: [The clarify round](../workflow.md#the-clarify-round)). For each question it shows the options (recommendation and cost) and the answer's state: open, assumed, or the owner's with its date and D-n. It also lists the format's problems. Without the section it prints the format to write. Read-only without flags.

- `--assume [--reason R]` marks every open answer `assumed — R (D-k)` (default `took the recommendation`) and records each as a provisional D-k under `## Decisions` (`clarify round, provisional: Qn <question> → <recommended option>`), so no `decide --provisional` is needed for it. Under YOLO, a gate pass does this by itself. `--answer` on an assumed question replaces its D-k.
- `--answer Qn=<k> --quote "…" [--how chat]` records the owner's answer given in chat, as the answer line plus a D-n decision (how `clarify round` by default) in one write. `--quote` is required; an answer the owner already gave is refused.
- `--json` prints `{slug, clarify: {found, nothing_to_ask, questions, problems}, assumed, answered, decision}`.


### `decide` and `decisions`

`issue-cli decide <slug> --text "…"` appends a dated `D-n` line to the issue's `## Decisions` (format: [Issue File Format](../issue-format.md#decisions)).

- `--how` says how the decision was made (`chat`, `project rule`, `playtest`; default `recorded by agent`; no `:`).
- `--quote` adds the human's words.
- `--deferred` marks it as decide-later.
- `--provisional` adds `provisional` to the how (`chat, provisional`; `YOLO, provisional` under YOLO; `recorded by agent, provisional` by default): the decision waits for the owner's words in the Decisions log until `--confirm` (or the Confirm button) drops the mark. Under YOLO the rule `start` and `show` print says to record every answer you gave yourself this way. Not with `--confirm`.
- `--confirm D-n --quote "…"` records the human's words on an existing decision of the issue instead of a new line, the same rewrite as the Decisions log's Confirm (`POST …/decisions/<slug>/<D-n>/confirm`): quote set, `(provisional…)` mark removed, how ending `confirmed by owner <date>`. Only `--quote` goes with it, and it is required. A superseded decision, or one that already has the human's words, is refused. `--replaces` on a decision of the same issue that only lacks words prints the `--confirm` command instead.
- `--replaces <slug>#D-n` (repeatable, or `D-n` in the same issue) marks older decisions as replaced. Each ref must exist, or nothing is written and the error points at `issue-cli decisions --issue <slug>`.

The output names the ref to cite and lists, per replaced decision, the open issues still citing it, followed by a `Next:` line. `--json` returns `{decision, stale_citers, decisions_url}`.

- `--scope S` (`issue` default, `project`, `system:<S>`, `department:<key>`, `type:<t>`), `--at "s1, s2"`, `--expires YYYY-MM-DD`, `--hard`, `--paths "g1, g2"` (hard only) write the [attribute group](../workflow.md#decision-ledger). A system, department, type or status that the project doesn't have is refused with the list; a past `--expires` is refused. A worker may not set `project` or `department:` scope (ask the lead). `--hard` needs the owner's words on the line (`--quote`, not `--provisional`) and is an owner act.
- `--amend D-n [--scope …] [--hard|--soft] [--at …] [--paths …] [--expires …]` changes only that decision's attribute group; `""` clears `--at`, `--paths`, `--expires`, and `--scope issue` resets the scope. The rest of the line stays byte for byte. With `--text` or `--how` it is refused and prints the `--replaces` command; `--deferred`, `--provisional`, `--replaces` are refused too. A worker amends only its own issue's decisions; a replaced decision is refused (amend its replacer); an unchanged group writes nothing.
- **Owner acts.** `--hard`, any `--amend` of a hard line or one that adds `hard`, and `--replaces` of a hard decision follow `approve`'s rule: a worker is refused (naming its lead and the `comment … "question: …"` command), a lead must relay the owner's words with `--quote` (posted as a `decision: <ref> <what>, "<words>" — relayed by <lead>` comment in the same write, ending `(YOLO)` while YOLO is on, which puts it in `yolo queue`), the owner's own shell may. A decision replacing a hard one inherits `hard`, scope and `paths:` unless set; `--soft` drops hard.
- `--renew D-n --expires YYYY-MM-DD [--quote …]` gives a decision with `expires:` a new last day (today or later), before or after it passed; `--let-go D-n [--quote …]` closes out an expired one (`ended: <today>`). Only the group changes; each is logged (`renewed`, `expired`). Refused: renew of one with no `expires:` or one let go (final: `--replaces`), let go of one not expired. On a hard decision both are owner acts as above; from the owner's own shell `--quote` is posted as a `decision: … — the owner` comment. `--amend --expires` on an expired decision points at them. See [Expiry](../workflow.md#decision-ledger).

The output adds a `scope: … · soft|hard` line and, for an issue-scoped soft decision, the `--amend --scope` hint. Every write is logged in `<issues>/.decisions/log.jsonl`.

`issue-cli decisions [--system S] [--state active|expired|deferred|replaced|stale|provisional] [--issue <slug>] [--json]` prints the same index as the viewer's Decisions tab, newest first; `--state provisional` lists the active decisions marked YOLO or provisional and not confirmed (`DecisionEntry.ProvisionalOn`), the Stats card's Provisional count. An active decision still waiting for the human's words (no quote, or provisional) reads `· needs owner's words`, a hard one `· hard`, and a scope other than `issue` `· scope <s>`. JSON rows carry `scope`, `hard`, `at`, `paths`, `expires`.

`issue-cli decisions --in-force <slug> [--status S] [--json]` lists the decisions in force for that issue at its status (or S): not replaced, deferred or expired, `at:` empty or naming the status, and a scope that reaches it. Each row says why: `own`, `parent <slug>` (any epic above it), `cites`, or its scope. Hard first, then issue → type → system → department → project, then newest. It only reads. `--state replaced` includes stale ones. `answer` prints the answer's full ref (`decision_ref` in JSON).

`issue-cli decisions --touched <slug> [--json]` runs the `decisions_respected` audit and prints it: the files the issue's work branch changed (three-dot diff from its merge-base with main), each active hard decision whose `paths:` they match, whatever its scope or `at:`, with the files and `cited`, `NOT cited` or `this issue's challenge open`, and how many the gate holds. With no recorded base or no branch it prints the warning instead. It only reads ([Decision ledger](../workflow.md#decision-ledger)).

Both flags take the issue from the session when the slug is left out: `decisions --in-force`, `--in-force ""` (what `"$ISSUE_VIEWER_ISSUE_SLUG"` expands to outside a session) or `--in-force --status S` uses `ISSUE_VIEWER_ISSUE_SLUG`, as `show` does; with no session they say `--in-force: give the issue's slug` instead of looking up an issue. A slug given wins.

### `prove`

`issue-cli prove <slug>` lists every acceptance criterion with its proof links, where each came from (a `Proves:`
commit trailer, a playtest step's `proves:`, or `prove`), each link's check result, and `proven` / `UNPROVEN`. It
ticks the criteria that are proven, and ends with `Evidence (with the playtest shots): <viewer url>?tab=evidence`, the
issue page's [Evidence tab](../UI/overview.md#the-evidence-tab), which shows the same report next to the shots.
`--json` returns the same report, plus `tests_at`, `last_change` and `last_change_sha` (the newest `tests:` comment and
the last commit that changed code; commits touching only docs, Markdown, CHANGELOG, playtest sessions, guided check
scripts or tracker notes don't count).

`issue-cli prove <slug> --run [--json]` runs the tests the issue's test links name in its worktree, with `go test -run`
in a Go project, else the project's default-tier `ci:` commands (each name has to be in a tracked test file), and posts the `tests:` comment when every one passed (a name only an older trailer gives, renamed or
removed since and replaced by a newer link on its criterion, is shown as superseded and skipped); otherwise it prints the failures, posts nothing and
exits non-zero. Refusals and output: [Proof Links](../Workflow/proof.md).

`issue-cli prove <slug> <AC-id> --test <Name> | --step <id> | --commit <sha> | --value "…" | --shot <path>` adds
links (repeatable) to the data sidecar; `--clear` removes that criterion's prove links. Most links need no call:
agents declare them where they write the proof. `--step` refuses a step id that only another issue's
session has, naming the session and its issue: a step link resolves from the issue's own sessions only.
Sources, checks, refusals and the `criteria_proven` gate:
[Proof Links](../Workflow/proof.md).

### `balance`

`issue-cli balance <slug> --metric <id> --state <state> [--hypothesis ..] [--change ..] [--evidence ..] [--verdict ..] [--pass n]` writes a typed balance comment on the issue (format: [Issue File Format](../issue-format.md#balance-notes)). Metric and state are required. The state accepts `needs-pieces` and `not-this-branch`. Invalid input leaves the issue untouched.

The output names the note and the board URL. When the note replaces an earlier one (same issue, metric and change), it adds `Updates the note from <date> (was <state>)`. Without a `workbench.balance` block it warns that the Balance tab is hidden; the note is still saved. `--json` returns `{note, replaces, board_url, board_enabled}`.

`issue-cli balance --list [--metric m] [--state s] [--pass n] [--json]` prints the current notes per metric and the number of free-text notes. `issue-cli comment` prints a tip pointing at `balance` when a `balance:` comment is free text.

### `lead`

The CEO supervises workers: one per project (tmux `lead-<project>`), and every worker reports to it. `issue-cli ceo` is the same command. [The lead session](../patterns/lead-session.md) covers the roles; this is the command reference.

| Subcommand | Does |
|:--|:--|
| `lead spawn [--department <name>] [--headless] [--agent claude\|codex]` | Starts the project lead (the CEO) in the project checkout; with `--department`, that department's lead `lead-<project>-<dept>` (see [Department leads](../departments.md#department-leads); `product`, `hq` and unknown names are refused). A live lead is re-attached, never started twice. What opens follows `lead_terminal:` (unset: headless, printing the attach commands and, inside tmux, a `tip:` naming `lead_terminal`). `--headless` opens nothing; the old `--attach` is accepted and does nothing. `--json` prints the launch response. `--epic` fails with one line: there is one lead per project |
| `lead flush pack\|note\|summary\|close --run <slug>` | The `lead-flush` job's steps, for that run's worker or the owner's shell: read the day, write the handoff note (80 lines at most, six sections) and chat summary (30) under `sessions/`, close the lead once both exist and it isn't mid-turn. A refused note or summary writes nothing. See [The nightly flush](../patterns/lead-session.md#the-nightly-flush) |
| `lead tick [--watch] [--every 5m] [--exit-on-news [--verbose]] [--observe [--department <d>]]` | The lead's check-in in one command: `queue next --start` (a due job first), done issues whose work branch is not on main, issues at shipping whose branch has waited unmerged for `leads.green_branch_minutes` (default 30; `merge:` with the merge command, once an hour while it waits), local CI red, what waits on the lead, stalled agents and takeovers, the queue at 5 open items or fewer, workers' `report:` comments and report outputs on the shelf (`report: <slug>'s report has N follow-ups to file`), nothing started for `leads.idle_minutes` (default 60) while the project had room (`idle:`, again each such stretch; [The factory runs 24/7](../agent-dispatch.md#the-factory-runs-247)), the owner's comments since the last tick; closes finished worker sessions whose branch is on main, and the idle session of a report that waits on the owner's review with its output on the shelf (a held `closed:` line and a `session: closed` comment; [A report waiting on your review](../agent-dispatch.md#a-report-waiting-on-your-review)). Prints a prefixed line (`start:`, `land:`, `merge:`, `ci:`, `hold:`, `idle:`, `report:`, `queue:`, `owner:`, `⚠`, `tick error:`) only when the lead has to act, each thing once until it goes away and comes back (`$XDG_STATE_HOME/ai-workflow-engine/lead-tick/<project>.json`); a quiet tick prints nothing. `--watch` repeats every `--every` (at least 1m) and says nothing of a hold that clears by itself (memory, the load, the worker cap, the gap, another project's turn) until starts have been held an hour; `--exit-on-news` exits after the first tick with news the lead must act on and prints the plain starts and session closes held since above it, one line each (`--verbose` exits on those too; [What counts as news](../patterns/lead-session.md#what-counts-as-news)). Once a local day it says `lead context <n>k: rotate (<lead>): issue-cli lead rotate` when the lead's own transcript's last turn read over 300k tokens. Once a local day on the engine's own repository, when the running issue-cli is more than 10 commits behind main, a held (never news) `note: the installed issue-cli (<build>) is <n> commits behind main; <k> shipped fixes wait on an install: <up to 5 fix subjects> (+N more); install: cd <repo> && GOBIN=<its folder> make install`; it runs nothing. Every tick (`--observe` too) re-renders the lead's state file. `--observe` is read-only (never `queue next --start`, never closes a session): due jobs, status moves, comments, new shelf items and comments, ended workers, for `--department <d>` (default: your own department, else the project); its own state file. Workers are refused. See [The tick](../patterns/lead-session.md#the-tick) and [Waiting](../patterns/lead-session.md#waiting) |
| `lead watch --run '<cmd>' [--every 10m] [--until-change \| --until '<regex>']` | Runs a probe with `/bin/sh -c` in the project root every `--every` (at least 10s) and exits only when its output (stdout, stderr and `exit N` on a failure) differs from the first run's (`--until-change`, the default) or matches `--until` (the first run included). Quiet runs print nothing; the exit prints `watch: output changed after 40m (5 runs): <cmd>` and the output (40 lines at most). Run it as a background shell, never under a timed wake. While it runs it is listed in the lead's state file (so does `tick --watch`). Workers are refused. See [Waiting](../patterns/lead-session.md#waiting) |
| `lead state [--path] [--all] [--lead <name>]`, `lead state note "<text>" [--tomorrow]`, `lead state ask --quote "<words>"`, `lead state done <n>` | Renders and prints the lead's compact state file, `$XDG_STATE_HOME/ai-workflow-engine/lead/<lead>.md` (16 KB at most): `## Workers` (live `agent-*` sessions with status and last `progress:` line), `## Queue` (open items with the owner's quote), `## Owner asks`, `## Today` (decision refs and lead memory ids recorded today, not copies), `## Watches` (pid and command), `## Tomorrow` (open ones), `## Notes`. Today, Tomorrow and Notes show their 5 newest with a `+N more:` line; `--all` prints them uncapped without writing the file. A line that only repeats a pinned memory's id and title is left out. The notes, asks and watches are kept in `<lead>.json` beside it; a note from an earlier local day folds into `sessions/<day>-<lead>.summary.md` under `## Lead notes`. A note that looks like a secret is refused. Workers are refused. See [The state file](../patterns/lead-session.md#the-state-file) |
| `lead rotate [--lead <name>] [--no-start]` | Writes the handoff note from the state file (the flush's six sections) to `sessions/<date>-<lead>.md`, or `<date>-<lead>.<HHMM>.md` when the day's note exists, and a digest of the lead's transcripts since the last rotate beside it (`….digest.md`: every owner message, the lead lines that decide or report, the commands that changed something; no tool output, at most 400 lines). Then it starts a detached `lead rotate --finish`, which waits for the session's turn to end (30 minutes at most), closes it and spawns the same lead under the same name; for 2 hours a spawn's first prompt carries the state file, the watches to restart and the `lead history` line instead of the note and chat summary. `--no-start` writes the files only. Workers are refused. See [Rotate](../patterns/lead-session.md#rotate) |
| `lead history <words> [--days 30] [--max 40] [--lead <name>]` | Searches the lead's digests in `sessions/` and its transcripts on disk from the last `--days` (what was said and the Bash commands run, never tool output): lines holding every word, newest first, `<time> <who>: <line>  [<source>]`. See [History](../patterns/lead-session.md#history) |
| `lead brief --render [--department <name>] [--sections]` | Prints the first prompt a fresh lead (`lead spawn`, or with `--department` that department's lead) would get now, from any shell, a worker's included; starts no session and writes nothing. `--sections` prints each section instead, with its characters and tokens (chars/2.7), named as `spend --context` names them, so a brief cut is measured before a lead runs it. `--json`: `{session, prompt}`, with `--sections` `{session, kind, sections: [{name, chars, tokens}], chars, tokens}` |
| `lead whoami` | Your role (`project lead`, `worker`, `unmanaged`), guarded branch, your lead and whether it is live, how to reach it, and what you may tick; on a config with a sandbox marker, whether owner-only commands pass there and why not (`--json`: `"sandbox"`, docs/patterns/guided-check-scripts.md#owner-only-commands-in-a-sandbox) |
| `lead list` | `== CEO ==`: the CEO, live or not. `== Epics ==`: each open epic with its children running/total. A start hint when the lead isn't running. `--json` gives `{lead, epics}` |
| `lead message <target> "text"` | Pastes `[from <your session>] text` into the target's tmux session (load-buffer + paste-buffer + Enter), Codex sessions only. A Claude session (read from the pane's start command, `agentrun.SessionAgent`) is never pasted into: it exits 1 and prints the `SendMessage({to: "<session>", …})` call to make. `target`: `project`, a `lead-`/`agent-` session name, or an issue slug (its worker) |

The CLI works out who is asking from `ISSUE_VIEWER_LEAD_SCOPE`, `ISSUE_VIEWER_ISSUE_SLUG` and the tmux session/window name. It doesn't need the viewer running. A lead running with `--project` set to another project keeps its own name in what it records (`lead-<its ISSUE_VIEWER_PROJECT>`, else its `lead-…` tmux session name): `autostart`, an `approve` comment, a critique override and a relayed decision say `lead-dark-factory`, not the target's lead. Refusals name who can do it instead:

- `lead spawn` from a worker;
- `lead message` from a worker, which posts a `question:` comment instead;
- `dispatch` and `health review --spawn` from a worker, or without `--quote`;
- `start`, `claim` and `transition` from a worker on any slug but its own: "you work on <own-slug> only; ask your lead", and nothing changes. `start` and `claim` default the assignee to the worker's session name (`agent-<slug>`, `/` as `-`), its `SendMessage` address.

`show` also prints, under the header:
- `Base: <branch> at <sha> (cut by the dispatch on <date>)`, as `start` does (see [`start`](#start)); `show --json` carries it as `base: {branch, sha, at}`;
- `Depends on:` / `Depended on by:` lines from `depends_on`, each slug with its status, plus `· ask open` or `· agreed` when the two issues' workers used [`ask`](#ask-and-answer-sibling-asks); an open dependency with no ask yet adds `Building on <slug>'s format? Agree it with its worker: issue-cli ask <slug> "..."`;
- `Decided by:`, the decisions the body cites, with their state;
- `Guard metrics for <system>: N off (ids), N near. See <viewer>/metrics?system=<system>`, when `workbench.metrics` lists metrics for the issue's system.

When the issue's `work/<slug>` branch is open, `show` also prints `== Shared files ==`: up to 10 files that other open branches change too, with those branches. See [Workbench Files](../workbench-files.md). The branch scan's git answers between commits (merge-base, commit counts, small diffs, and "no common ancestor") are kept in `issue-cli-gitcache.jsonl` in the repo's git common dir, shared by every worktree, so a `show` with no new commits asks git only for the refs. The file is append-only and starts over past 16 MB; deleting it is safe.

`show` and `start` print a `Lead:` line naming the issue's lead and whether it is live. On an issue linked with the legacy `umbrella:` key they also print the `set-meta … --key parent` rename hint.

The lead's brief is a built-in default plus `leads.project_addendum` in `workflow.yaml` (see `issue-cli process schema`) and a fixed footer with the role, authority and handoff note. An old `leads.project` replaces the default, and lint warns about it. An old `leads.epic` is ignored, and `process workflow` warns about it.

### `ref`

`issue-cli ref` gives the compact form of a docs page, so an agent reads the part it needs: `docs/CLI/overview.md` is 181 KB and its outline 9 KB.

- `ref` lists the pages dispatched workers read most (measured from their transcripts over the 7 days to 2026-11-15, `docref.Top`), then the other pages by short name.
- `ref <page>` prints the title, size and first sentence, then each `##`–`####` heading outside code fences, indented by level, with its line (`[L228]`) and its first prose sentence.
- `ref <page> <heading words>` prints the first section whose heading holds every word (an exact heading first), to the next heading of its level, with its line range. No match lists the page's `##` sections.
- `<page>`: a path under docs/ (with or without `docs/` and `.md`), a short name (the file name lowercased; an `overview.md` or `README.md` takes its folder's name, and a top page wins a shared one), or words of one page's title.
- The docs are `./docs` when the working directory has one (a worktree reads its own branch), else the project's docs folder.
- `ref --measure [--days 7]` reads the top-level transcripts of the project's worktree sessions on this machine and prints the sessions, their median first-hour tokens (the sum over the first hour's assistant turns of input, cache read, cache write and output, one count per message id) and the docs pages read by sessions, counting `Read` calls and shell reads (`cat`, `sed`, `grep`, …). Only paths and numbers are read.

The worker briefing's `## Reading code and docs` names `codemap`, `affected` and `ref`.

### `ask` and `answer` (sibling asks)

Two workers whose issues build on each other (one reads the record or format the other writes) agree it through comments. They don't use SendMessage, a tmux paste or any other transport.

| Command | Writes | Shows |
|:--|:--|:--|
| `issue-cli ask <sibling> "…" [--from <slug>]` | `ask: from <your slug>: <words>` on the sibling's issue | at the bottom of the sibling's next issue-cli call on its issue, every call until answered |
| `issue-cli answer <asker> "…" [--from <slug>]` | `answer: from <your slug> (ask #<id>): <words>` on the asker's issue, and closes those asks (comment status `done`) | at the bottom of the asker's next issue-cli call on its issue, once |

- Your slug is `ISSUE_VIEWER_ISSUE_SLUG` (every dispatched worker has it), else `--from`.
- The block is `== Sibling asks (<slug>) ==`. It follows any command that looked up an issue (`show`, `checklist`, `check`, `transition`, …), lists the open asks with the `issue-cli answer …` line to run and the answers not yet delivered, and ends with "An ask or answer is between workers: it never approves a gate." `--json` output never carries it.
- An answer counts as delivered only when the issue's own worker was shown it. The lead running `show` on the asker's issue sees it without using it up.
- Refused, writing nothing: asking yourself, asking a done or parked issue (nobody is there to answer), no words, words starting with `approval:`, no slug for you, and `answer` with no open ask from that issue (the channel answers asks; it doesn't start them).
- Neither is an approval. The words sit after `ask: from …:` / `answer: from …:`, which no gate parser reads; an `answer:` saying "approved" still leaves the gate waiting.
- The lead sees the open asks in `issue-cli status` (status.md: `## Open asks between siblings (N)`, `asker → sibling (#id, age): words`) and in status.json's `SiblingAsks`. They don't count as waiting on the owner.
- The worker briefing's lead section names the channel. `show`'s `Depends on:` line marks it (above).

### `dispatch` and `status`

These two commands do what leads used to do with `curl` against the viewer. One `issue-cli` permission covers them, and nobody types a host.

**`issue-cli dispatch <slug> --quote "<the human's words>" [--how "…"] [--agent claude|codex] [--force] [--reprompt]`** starts the issue's worker.
- **Agent.** Without `--agent`, the workflow's `routing:` rules pick claude or codex, falling back while a subscription is resting after a usage limit; without `routing:`, claude. The first launch step says which and why ([Routing](../agent-dispatch.md#routing-between-claude-and-codex)). A Codex start with `routing: codex:` set lists the sandbox flags it got in a step of its own ([Codex's sandbox](../agent-dispatch.md#codexs-sandbox)).
- **Same as ▶.** It is the same launch: `agentrun.DispatchSpec` builds the prompt (`briefing.BuildDispatchPrompt`), session (`agent-<slug>`), worktree and reprompt hint for both. The CLI then starts it through tmux itself, so the viewer doesn't need to be running. The worktree is resolved against the project checkout (`workdir`, else the issues folder's parent), never the caller's cwd.
- **Output.** It prints `✓ Dispatched agent-<slug> for <slug>` (or `✓ Already live: …` when the worker runs: re-attached, prompt not re-sent), then the launch steps, then `Inside tmux:` (switch-client) and `From a shell:` (attach).
- **Gate refusal.** When everything the issue's next transition needs is done except your approval (`tracker.WaitingOnGate`, the same rule as the Status page's "waiting on you" gates), it starts nothing. It prints the gate, the approve link, the [Needs you list](../UI/overview.md#needs-you-list) link (`Several waiting?`, to approve ready gates together) and the `--force` command, and exits 3. A gate whose work isn't finished doesn't block, and a live worker is always re-attached.
- **`--autostart`** replaces `--quote` on a project where you turned [auto-start](../agent-dispatch.md#auto-start) on: it quotes your stored words, re-checks memory (mem-guard), the CPU load, the worker cap and the machine-wide start gap under a lock, and starts nothing (exit 1) when any of them holds. It takes neither `--quote`, `--reprompt` nor `--force`.
- **`--takeover`** replaces `--quote` for a worker stopped at its usage limit: it quotes the issue's newest `dispatch:` words, re-checks the pane, mem-guard and the machine-wide start gap under a lock, renames the limited session `limited-<slug>` and starts the other agent as `agent-<slug>` in the same worktree, its prompt naming `HANDOFF.md` and the branch. Once per issue; a hold exits 1 and starts nothing. `queue next`, `autostart next` and `agents` print the command ([Takeover](../agent-dispatch.md#takeover-at-a-usage-limit)).
- **`--fresh`** replaces `--quote` at a `session: fresh` status: after `transition` marked it (`stage: fresh session at <status>`), it quotes the issue's newest `dispatch:` words, re-checks the pane (out of a turn), mem-guard and the machine-wide start gap under a lock, closes the old `agent-<slug>` and starts a new one in the same worktree on the stage briefing (paths only, trimmed to the stage and its budget). Once per mark; a hold exits 1 and starts nothing. `queue next`, `autostart next` and `agents` print the command ([Fresh session per stage](../workflow.md#fresh-session-per-stage)).
- **`--reprompt`** pastes the current briefing into the live worker (`agentrun.Reprompt`, the Re-send prompt button). With no live worker, or a session that is only a shell (its agent exited, `agentrun.BareShell`), it pastes and creates nothing, names `issue-cli dispatch <slug>`, and exits 1. It takes neither `--agent` nor `--force`.
- **Workers can't dispatch.** The refusal is the same as `lead spawn`'s, and it points at a `question:` comment. A shell under a `.worktrees/` folder counts as a worker.
- **`--json`** prints the object `POST …/dispatch` returns: `{status, prompt, session, attach_cmd, switch_cmd, gate, steps}`, with `status` one of `dispatched`, `reattached`, `reprompted`, `waiting-on-gate`, `no-session`, `error`.
- **`--quote`** is required (exit 2 without it, nothing starts): a human starts the work. A start or re-prompt leaves `dispatch: "<words>" (<how>, <date>)` on the issue, plus ` — recorded by <session>` when an agent ran it; `--how` defaults to `lead chat` for the lead, `owner's shell` otherwise. A re-attach starts nothing and leaves no comment. One slug per call.
- **Exit codes:** 0 started, re-attached or re-prompted; 1 the launch failed, or `--reprompt` found no worker; 2 usage (no `--quote`, a bad `--agent` value); 3 waiting on a gate.

**`issue-cli status [--md|--json]`** prints `GET <viewer>/p/<project>/status.md` exactly as served, or with `--json`, `/status.json` (its `as_of` is each request's time). The viewer builds the Status model, so it must be running.
- When it doesn't answer, or answers with an error, the command exits 1 with `viewer not reachable at <url>: …`, how to start it, and the `viewer_url` hint.
- A slug by mistake (`status <slug>`) is a usage error pointing at `issue-cli show <slug>`.
- `--hq` prints `/p/<project>/hq/report.md` instead: the HQ report, one headline per department and group, then each one's detail ([Departments](../departments.md#the-hq-report)). It takes neither `--md` nor `--json`.

**The viewer's address** for `status` and every link issue-cli prints (approval hints, `show`'s Timeline, Balance, Decisions, Metrics) comes from `viewerBaseURL` → `viewerstate.BaseURL`, in this order:
1. `viewer_url:` at the top of `projects.yaml`
2. the URL in `viewer.json`, only when that viewer serves the same config file
3. `http://localhost:8080`

`ISSUE_VIEWER_URL` is gone: neither the viewer nor issue-cli reads or sets it. When `viewer_url` is unset, the approval hint ends with `(set viewer_url in projects.yaml if your viewer is on a different host/port)`.

### Restarting the viewer

`issue-cli viewer restart [--timeout 60s]` is the step after every `make install`: `cd <repo> && make install && issue-cli viewer restart`. The viewer runs as the start command of a tmux pane; killing it by hand can close that session with it, and a `pgrep` wait loop can match itself (both happened on 2026-11-22).

- **Which pane.** The pane holding the pid `viewer.json` records (the pane's own process or an ancestor of it), else the one live pane whose start command runs `issue-viewer`. None: exit 1 with one line naming both (`viewer restart: no tmux pane runs the viewer (looked for the pane holding pid N from …/viewer.json and a pane whose start command runs issue-viewer)`). Several and none holds the recorded pid: exit 1 naming them.
- **Restart.** `tmux respawn-pane -k -t <pane>` with no command, so tmux reruns the pane's own start command and the session stays. Nothing else is stopped.
- **Wait.** Until `viewer.json` names a new live pid and its URL answers HTTP (any status under 500); for a viewer that records no `viewer.json`, until the pane runs a new process, the old one is gone and the old URL answers. The pane dying, or `--timeout` passing, is exit 1 with the pane's last 15 lines (a busy port or a bad config shows there).
- **Output.** One line: `viewer restart: <old version> (pid A) → <new version> (pid B), answering at <url> after 1.4s (pane %3, session issue-viewer)`. The viewer records its build in `viewer.json` (`version`); one started before that reads `unknown`.
- **Who.** Leads and the owner. A dispatched worker is refused (restarting the viewer is the lead's or the owner's call), and so is any marked sandbox.

### `agents`

**`issue-cli agents [status] [--json]`** shows each agent CLI (claude, codex): installed or not, working / resting until HH:MM / unknown with the weekly percent once a CLI printed it and the stop point (`stop_at:`), the live agents whose pane shows a usage limit now (left running), the stalled agents, left as they are (`stalled at Codex's trust dialog` for a startup dialog a minute after launch, `stalled at Claude's approval prompt` for a worker at a prompt, `no issue-cli call in the 7 m since launch` for a worker silent past five minutes; also a `⚠` line in `queue next` and `autostart next`; [A stalled start](../agent-dispatch.md#a-stalled-start)), and the workflow's `routing:` rules. **`agents rest <agent> --until HH:MM`** marks a subscription resting by hand, and **`agents clear <agent>`** ends a rest. Only the owner or a lead can run either; workers are refused. **`agents sweep [--dry-run] [--json]`** closes the worker sessions left behind: `agent-<slug>` / `limited-<slug>` whose issue is closed, and any whose pane is only a shell; never a lead, a post-mortem agent, one mem-guard paused, one in a turn, one a person is attached to, or the caller's own. `--json` is `{dry_run, closed: [{name, slug, why, dead}], failed}`. The owner or a lead runs it; the viewer runs the same sweep every 10 minutes. `transition`, `done` and `close` close the issue's worker sessions themselves when it reaches a closed status, printing `Worker session: …`; see [Closing finished workers](../agent-dispatch.md#closing-finished-workers). See [Routing between Claude and Codex](../agent-dispatch.md#routing-between-claude-and-codex).

### `ci`

Local CI runs the `ci:` commands of `workflow.yaml` on the integration branch after every move. [Workflow: Local CI](../workflow.md#local-ci) covers the config and what it reports; this is the command reference. Without a `ci:` block, `ci status` prints `ci: not configured — add a ci: block to workflow.yaml (see docs/workflow.md, "Local CI")` and exits 0 (`--json`: `{"configured": false}`), and `ci run` fails with the same line.

| Subcommand | Does |
|:--|:--|
| `ci status [--tier <t>]` | The first line is the one-liner (contract below). While red it then lists `Landed since green <sha>:`, the bugfix issue (`Fix tracked in:`) and the log path. Green or red, `Last red log: <path> (<sha>[ tier <t>], <age> ago)` names the newest kept red log (`.ci/red/`) while it exists. Each non-default tier with a run follows the first line as `tier <t>: <state> at <sha> (<age> ago)[: <failed>]`, a red one marked `— inherited from <branch>, not caused by your branch`. Then `Recent runs:` (the last 5: sha, state, age, duration, tier, what failed). `--tier slow` with no slow run yet says `tier slow: no run yet`. When the newest default-tier run timed its steps (make validate's `validate: <step> <seconds>s` lines, kept as each command's `steps: [{name, duration_ms}]` in `.ci/runs/<sha>.json`), `Slowest step at <sha>: <step> <N>s (<cmd> <N>s)` comes after the tier lines. `--json`: `{configured, branch, line, state, runs, running?, last_red_log?, slowest_step?}` |
| `ci run [--tier <t>] [--detach] [--if-untested] [--reinstall]` | Runs in the foreground and streams the output. It re-tests the head, then keeps going while new commits land, testing only the newest. Exits 0 when green, 1 when red or CI can't run. A live run makes it print `already running at <sha> (pid N); it tests the newest head when done` and exit 0. `--detach` starts the same command in tmux `ci-<project>` (or a background process without tmux) and returns. `--if-untested` skips a head already tested; the watchers pass it. `--reinstall` forgets the install stamps, so an `npm ci` whose lock file is unchanged runs again (docs/workflow.md#how-a-run-happens). `--json` writes the runs to stdout and the live output to stderr |
| `ci reset` | Removes `.worktrees/.ci`, ignored build output included; the next run creates it again (a cold build). Refuses while a run is live |
| `ci watch [--interval 5s] [--once]` | Polls the branch head and starts a detached run when it is untested. For projects without the viewer running |
| `ci gotest [--out <file>]` | A filter for a `ci.run` command: reads `go test -json` on stdin, prints what plain `go test` prints (failing tests' output, the ok/FAIL line per package), saves the raw stream to `--out` (default `$CI_RESULTS_DIR/unit.json`) and exits 1 when a test or package failed, so the pipe keeps go test's verdict. Needs no project. No events on stdin is exit 1 with a hint |

The one-liner, also printed by `start` and the dispatch prompt (with a `Local CI: ` prefix):

```
main green at abc1234 (12m ago)
main red since def5678: make validate FAIL (test: internal/ci, internal/ship): TestA, TestB, TestC (+4)
main: CI can't run: <reason> — issue-cli ci reset
main: no CI run yet
… · running at 1234567 (3m)          # appended while a run is live
```

The test names come from the command's `failures:` regex; the red steps from make validate's `validate: FAIL (…)` line, the test step's packages from go test's `FAIL\t<pkg>` lines (module path cut off, five at most, then `(+n)`). A red run's first `tmuxguard: <bin> left …` line is kept too: `ci status` prints it as `Leftover: …`, and `start` and the dispatch prompt add it as `Local CI leftover: …`, so a file a test left in TMPDIR reads apart from a failing test or a load flake. While red, `start` and the dispatch prompt add `— inherited from main, not caused by your branch`. `transition --to done` ends with `Local CI tests <branch> at <sha> by itself: don't wait for it, you are done…` when `ci:` is set.

### `tracker` and `privacy`

The owner's choice lives in `privacy:` in workflow.yaml; [Workflow: Private work and code](../workflow.md#private-work-and-code) covers the model. Both commands need it: without `work_files: tracker`, `tracker` refuses with the snippet to record (`privacy.work_files is not tracker in workflow.yaml: the owner has not chosen a private tracker`).

| Subcommand | Does |
|:--|:--|
| `tracker init --remote <url>` | `<url>` is the private repo the owner created (agents never create repos); it can be left out only when an adopted tracker already has an origin, else init refuses (exit 2) asking for it. Creates the tracker git folder outside the checkout, or adopts an existing one whose work tree is this checkout (another checkout's is refused), adds `--remote` as origin, ignores the work paths in `.gitignore`, installs the pre-push guard, and runs a first sync. Work paths the code repo still tracks are printed with the `git rm -r --cached` to run; init never runs it |
| `tracker sync [--quiet]` | Commits every change under the work paths (`tracker: N files`), pulls with rebase, pushes. Prints what it did (`--quiet`: errors only). A conflict keeps the local commit, pushes nothing and exits 1. The result goes to the tracker's `issue-cli-sync.json`, which Status reads |
| `privacy check` | One line per badge plus the guard: `work   green  Work private: Work files private: <repo>, last backup 12m ago`, each amber or red line followed by `fix: …`. In every project a `secret` line says whether the pre-push guard is there to refuse a key (`secret amber  Secrets unguarded: … fix: issue-cli privacy hook`). Exit 1 when a line is red. `--json`: `{project, lines: [{kind, level, label, sentence, fix}], red}` |
| `privacy hook` | Installs the pre-push guard in the common git folder (`core.hooksPath` when set). A hook that isn't ours is kept as `pre-push.local` and still runs |
| `privacy pre-push <remote> <url>` | What the hook runs, with git's ref lines on stdin. In every project, with or without a `privacy:` block, refuses (exit 1) a push whose commits add a key or an env file (`secret guard: this push: 1 secret(s) would go into the repo: config.go:3: a GitHub token (ghp_…)`; [the secret scan](../workflow.md#the-secret-scan)). Refuses (exit 1) a push whose commits add a work path (`work_files: tracker`), a dev commit or tag to the public remote (`code: snapshots`), or a commit message quoting a decision (`code: public`) |

### `deploy`

The project's own deploy, as [Workflow: Deploy](../workflow.md#deploy) describes. Not DF's release (`release`, below).

| Form | What it does |
|---|---|
| `deploy --dry-run` | Prints the steps in order (run, probe, needs) and each need's verdict now (`✓ met now` or `✗ needs main-green: main red since abc1234 …`), and a note when no step needs `owner-ok`. Runs nothing, writes nothing; a worker may run it. `--json`: `{dry_run, steps: [{name, run, probe, needs, timeout, refused}], owner_step}` |
| `deploy [--quote "<owner's words>"]` | Runs every step in order from the main checkout: needs, then `run`, then `probe`, printing `✓ 1. backup (12s)` or `✗ 2. app: run exit 3 (4s)` with the log's tail. Stops at the first red or refused step (exit 1) and prints the command that goes on from it (`issue-cli deploy --from app`). A worker is refused; an `owner-ok` step refuses without `--quote`. Writes `.deploys/<id>.json` and `<id>.log` under the issues dir; a second deploy while one runs is refused. `--json`: the record |
| `deploy --step <name>` / `--from <name>` | Runs that one step, or that step and the ones after it. An unknown name lists the steps (exit 2) |
| `deploy --last` | The newest record: `Last deploy 2026-11-15 23:17 by lead-x: red at app, 41s`, then each step's mark and state. `--json`: the record |
| no `deploy:` | Prints a snippet to copy and the docs link; exit 0 |

### `release`

The branch flow of [Roadmap: Cutting a release](../roadmap.md#cutting-a-release-candidate-soak-publish); [Workflow: Private work and code](../workflow.md#private-work-and-code) covers `code: snapshots`. `cut` and `publish` read the running viewer's Waiting for release card (`/p/<project>/release.json`) and refuse unless it says the same: they print each unmet check (`✗ Local CI green on main's head: main red since abc1234`). Without the viewer they say how to start it.

| Subcommand | Does |
|:--|:--|
| `release setup --dev <url>` | Once, with `code: snapshots`: the old `origin` becomes the public remote (`remote.public.tagOpt --no-tags`), `<url>` becomes `origin` and gets every branch and non-candidate tag, the public tip is recorded as the base, the pre-push guard is installed. Prints the mirror clean and the release-workflow change; pushes nothing to public |
| `release cut <version>` | Creates `release/vX.Y` from main (the CI branch) with its worktree `.worktrees/release-vX.Y` when missing, and tags the next `<version>-rc.N` on its tip. Refuses a second candidate on the same tip. With `code: snapshots` the branch goes to origin (the dev repo); before `release setup` (no public remote, origin may still be the public repo) it refuses and prints `issue-cli release setup --dev <url>`, pushing nothing. A patch (`v0.N.1`) skips the milestone check and tags `v0.N.1-rc.K` on the existing `release/v0.N` after go build and go vet pass there; it refuses before that branch exists. Prints the soak steps |
| `release pick [--release <version>] <slug\|sha>...` | Cherry-picks (`-x`) onto the release branch, in its worktree, an issue's commits the branch lacks (by patch, `git cherry`, or a branch commit's `(cherry picked from commit <sha>)` line): the non-merge commits on main with a `Refs: <slug>` line, in any paragraph of the message (agents put it before `Co-Authored-By:`, where git's own trailers don't look), oldest first, or, only when main has none (not landed yet), the non-merge commits of `main..work/<slug>`; or a commit. Commits that change only guided check scripts (`scripts/checks/`: the script and its drop before the merge) are skipped. A work branch that merged main never brings other issues' commits along: before picking it prints each slug's list (`<slug>: N commits to pick`, then sha and subject) and refuses, naming them, when it holds a merge or a commit that isn't the issue's (`… are not its own …: nothing picked`). A conflict names the commit and files and leaves the branch as it was; a picked commit that touches the changelog, conflict or not (the changelog merges with `merge=union`), leaves the branch's changelog as it was plus that commit's new lines at the top of its first `## Unreleased` (opened on top when the first heading is a release), skipping any line the changelog already has; a commit that then adds nothing counts as already on the branch. After each pick the changelog is checked: at most one `## Unreleased`, and the first heading; the branch's own releases (`vX.Y.*`) as at their tags, older sections as before the pick. A red check puts the branch back and names the lines (`after picking <sha>, the CHANGELOG.md check failed: …`). Publish's changelog move on main (a commit changing only the changelog that opens a version heading and adds no `## Unreleased` line, `docs(changelog): vX is published`) is skipped and named (`✓ Skipped publish's changelog move on main (<sha>)`): the branch already has that heading. Then `go build ./... && go vet ./...` in the worktree (with a go.mod): a failure exits non-zero with the hint to pick the missing commit. Without `--release` it uses the only release worktree |
| `release check <version>` | Validates the candidate that ships: runs local CI's commands (`ci.run`'s default tier, else `make validate`) with `sh -c` in the release worktree, refusing uncommitted changes there and a blocked `mem-guard check`, and records the commit that passed (git config `issue-cli-release.<branch>.validated`). A failure clears the record and exits non-zero. The card's `validated` check, which publish reads, passes when that commit is the candidate tag's or the branch tip's. `release cut` names it as the next step |
| `release publish <version> [--first] [--dry-run]` | `code: snapshots` only, from the release worktree (elsewhere it names it), on a soaked candidate. Refuses a dirty worktree, a version already tagged, work paths in the tree, or a public tip that is not a snapshot (unless it is the recorded base, or `--first`). Then: the changelog heading on the branch, one commit on the public branch (subject `<version>`, body the release's changelog section), the dev tag, both pushed to origin, main's released changelog lines moved under the version. `--dry-run` runs the same checks and prints the parent, the message and a diff stat; it writes nothing, lists the card's checks it reads with ✓ or ✗ (on a release branch: local CI not red on main and the candidate validated, never main's head), and a YOLO window or an unmet card check is listed instead of refused. A refusal on either names the dry run |

### `codemap` and `affected`

The [code map](../workbench-files.md#code-map) (`workbench.layers`, `workbench.contexts`), read only.

| Command | Does |
|:--|:--|
| `codemap [--root <dir>] [--json]` | Prints each layer from the top, its contexts (`name [key] · N packages · N lines`, the summary, the packages biggest first), the imports between contexts (`from → to: N`, `↑ up a layer` when it points up), the upward imports (`from → to (Ctx → Ctx) · NEW\|known leak · file:line`), the unmapped packages and stale leaks. Without `contexts:` it exits 1 naming docs/workbench-files.md#code-map. `--root .` reads a worktree's tree and workflow.yaml instead of the project's checkout. `--json`: the map (`layers`, `contexts`, `packages` with `context`, `imports`, `up`, `main`), `between`, `upward` (`from`, `to`, `file`, `line`, `known`), `unmapped` |
| `affected <slug> \| --files <a,b\|-> [--root <dir>] [--json]` | A change's footprint: `Changed: N packages, N more reached`, `Works in:`, `Reaches:`, `Rebuild:` (main packages), `Watch:` (changed packages that import up a layer), then the changed packages, the reached ones by context, the surface files and the contexts they import, files mapped by `paths:`, new packages, the test count and `Not mapped:`. `<slug>` (a dispatched worker may leave its own out) reads the work branch's merge-base diff (the one `decisions_respected` reads; no recorded base and no path in the body is an error naming `--files`), `--files -` one file per line on stdin. The footprint's last line names main's sha the map is from. In the dark-factory repo it ends with the catalog impact: `Planned files named in the issue:` (the repository paths the body names, the footprint's files while the branch changes nothing, as in design), then `Catalog impact:` with each catalog entry whose `sources:` the changed and planned files match (the entries catalog-check will name), the `issue-cli workflow catalog-bump <ids>` command and the `Catalog: n/a <ids> <why>` line to write, or `none` when no entry matches. `--json`: the footprint (`changed`, `reached`, `works_in`, `reaches`, `binaries`, `watch`, `surface`, `by_paths`, `new`, `tests`, `other`, `no_imports`) with `names` (key → context name), `base`, `head`, `main`, `planned` and `catalog` (each entry's `id`, `version`, `matches`, `narrow`). `transition` into a verify or owner-test status prints its one-line summary |

### `health`

Project health snapshots: dated JSON files in `.health/snapshots/` of the main checkout. The viewer takes one a day and `ci run` takes one after each run. [Workbench files: Health snapshots](../workbench-files.md#health-snapshots) covers the file, the config and every metric.

| Subcommand | Does |
|:--|:--|
| `health [--family <name>]` | Prints the latest snapshot. First the header: `Health snapshot <date> · <branch> at <sha> · <trigger> · <ms> ms` and the file path. Then `Scores:`: the derived health with the share of the weight it is based on (or `not enough data`), the agent opinion with its age (or `no review yet`), and each component's measured value, sub-score and weight (`no data: <why>` when empty). Then the codebase: files and lines per kind with 7d and 30d growth (`n/a` without a base), source lines, test ratio and binary files; the top 10 areas and languages by lines; the big files. `Families:` gives each family `ok, N metrics` (plus ` · <hint>` when the family set one) or `error: <why>`. `Next:` names what is missing, with the command or doc: CI without `results:`, no local CI, no review or one over 7 days old, recommendations waiting for the owner. It ends with `Charts: <viewer>/p/<slug>/stats · scores and the review: <viewer>/p/<slug>/health`, and with fewer than two snapshots, `Trends need history (N snapshot so far): issue-cli health backfill`. `--family tests` (shape, score, opinion, codebase, ...) lists that family's rows by id instead; an unknown one names the families. Without a snapshot it prints `No health snapshot yet: run issue-cli health snapshot (the viewer takes one daily)` and exits 0. `--json` prints the snapshot file (`{"snapshot": null, "hint": ...}` without one) |
| `health review [--spawn [--attach] [--agent claude\|codex]]` | The agent opinion ([Workbench files: The agent opinion](../workbench-files.md#the-agent-opinion)). Writes the evidence pack and prints the review brief, for the agent running it. `--spawn` starts a review agent with the brief in tmux `review-<project>`, headless unless `--attach`, in the main checkout. `--schema` prints the JSON schema of the review file. `--json`: `{brief, evidence}` |
| `health review record <file> [--reviewer <name>]` | Validates the agent's review and stores it in `.health/opinions/`, then takes a snapshot (trigger `review`). Refused with `<file>: N problem(s), nothing recorded:` and one line per problem with its fix (score outside 0–100, assessment length, an unknown kind or effort, a metric id not in the snapshot, a path not at the main head, a repeated title). Unknown JSON fields are refused too. Prints `✓ Recorded the review: score N, M recommendation(s)` and that nothing becomes an issue by itself. `--reviewer` defaults to `$AGENT_NAME` |
| `health review show` | The newest review: score, age, reviewer, assessment, and each recommendation with its kind, effort, state (`filed → <slug>`), evidence and expected effect |
| `health review file <R#> --quote "<owner's words>" [--how "lead chat"]` | Creates an `idea` issue from a proposed recommendation, records the owner's decision on it and marks the recommendation filed. Without `--quote`: `needs the owner's approval: ask them, then pass --quote "<their words>"`. A filed or dismissed one, or an unknown id (the error lists the ids), is refused |
| `health review dismiss <R#> --quote "<owner's words>" [--reason "..."]` | Marks a proposed recommendation dismissed. Needs `--quote` like `file` |
| `health lint [--check a,b] [--list] [--all] [--check shas --fix]` | Checks workflow.yaml, each department's workflow file (the file checks run once per file, the department's prefixed with its file) and the board (`internal/lint`) and prints each warning as `<check> <where>: <message>` with `  fix: <command or edit>` under it, then `N warnings; the health score's lint component counts them`. Checks: `show-footer` (a section holding `show`'s own output, pasted when a section is rebuilt from it), `undated` (no `created:`; the fix is a `set-meta … --key created --value <earliest recorded date>`), `workflow` (the `process workflow` warnings: types:, workbench:, ci:, health:), `schema` (keys the parser doesn't know, with `did you mean`; a validate rule this build doesn't run, with its line; no `statuses:` or `transitions:`; deprecated keys: `actions:`, `statuses[].template`/`validation`/`side_effects`), `template` (the `template:` stamp behind the bundled template, unknown, malformed or missing), `placeholders` (a `{{name}}` its field never fills, `{{ name }}` with spaces where plain replacement fills it, any `{{…}}` in an untemplated field such as a status prompt) `department` (a department's workflow file that doesn't load, a project-wide key such as `ci:` in a department file, an issue whose status isn't in its department's file, with the `set-meta … --status` fix), `contexts` (the [code map](../workbench-files.md#code-map): an unmapped package, an import up a layer `leaks:` doesn't list with its file:line, a stale leak, a repeated context name, a package in two contexts), `paths` (a `workbench.systems.<name>.paths` glob that matches no file in the repository, or is absolute or has `..`, with its workflow.yaml line; the fix is to correct or remove the glob), `owner-only` (a path entering `publish`/`launch`/`send` through a gate without `owner_only`, a department with a `sends: true` connector whose workflow has no `owner_only` gate, `owner_only` on a non-approval action; the fix names the edge), `na-boxes` (a ticked box on an open issue whose N/A reason is out of form, or an N/A on the owner's box; the fix is `check <slug> <id> --na "…"`, see [N/A boxes](#na-boxes-check---na)) `owner-marker` (an open acceptance criterion that reads like the owner's check, "the owner confirms …", without the `(owner)` marker; the fix is `check-edit <slug> <id> --text "… (owner)"`) `criteria` (an open acceptance criterion that names no test, command or playtest step that could fail it, quoting a vague word such as "works" or "is clear"; owner, ticked, N/A and template boxes are skipped, and so is every box of a type whose `path:` leaves out the testing status (report, docs, chore: the reports check reads what a report holds); also a `## Must not` left as its placeholder on an issue past design, and a Must not item past design without `→ ACn` (an existing box) or `→ none: <why>`; the fix is `check-edit <slug> <id> --text "… (test: TestName)"`; `transition` prints the same warnings when an issue leaves design, without refusing) `types` (an open `docs`-type issue whose `## Wanted` or open criterion names a test, a package or a `.go` path; the fix is `set-type <slug> tweak` at its first status, or a rewording) `testids` (a playtest session step whose target, `targets:` entry or `data-testid="…"` string names an id `internal/testids` doesn't list, as `playtest/sessions/<file>.yaml:<line>` with the step id and a `did you mean`; only on a repository that has `internal/testids`) `memory` (embedder settings search can't use; a memory `--at` a status no workflow has; one that reads as a note on one issue; 3 or more memories in one scope retelling one fact, by id, fixed by `memory edit <first> --text` and `memory forget` on the rest; a project pitfall or howto that names a step with no `--at`, fixed by `memory edit <id> --at "<status>"`; see [Shared Memory](../memory.md)) `roadmap-behind` (`docs/roadmap.md`'s version table ends before the highest `version:` an open issue carries, in the same major; one warning naming each milestone it lacks, with a docs issue as the fix) and `sections` (a checkbox gate on a section no `append_section` or status template writes, or writes without a box; a `has_test_plan` template without exact `### Automated`/`### Manual`). Every finding is a warning: exit 0, 2 on bad usage or an unknown `--check`. History is the exception: a decision scoped to a removed department (one the board still holds work of; a name it never saw still warns) and outputs of one are printed after the warnings as `N info lines, kept as history (no warning, no score):`, one `· <check> <where>: <message>` each, and counted in no total. Text shows 20 warnings per check (`--all` for every one). `--list` names the checks. `--json`: `{project, checks, findings: [{check, where, message, fix}], warnings, history}` (`history` has the findings' shape, left out when empty). `--check shas --fix` swaps each stale `## Implementation` SHA for the one commit on main that names the issue's slug and has the same subject (one write per issue), printing `✓ <slug>: <old> → <new>  <subject>` per swap, `· <slug>: <ref> kept: <reason>` per ref it kept (with the subject and, when one main commit has it, a `hint:`), then `N refs swapped in M issues, K refs kept` and the check's count after; `--json` prints `{branch, swaps: [{slug, old, new, subject}], kept: [{slug, ref, subject, reason, hint}], issues}`; `--fix` with any other check is a usage error. The [lint family](../workbench-files.md#the-lint-family) records the count, and /health lists the warnings live |
| `health lint --workflow <file> [--projects <file>] [--check a,b]` | Lints a candidate before the merge that makes it live: the same checks, run once on the live files and once with `<file>` as Product's workflow file and/or the project's entry (same slug) in the candidate projects.yaml (its jobs, goals, departments, connections), over the same issues (`lint.RunCandidate`). Prints only the difference: `== health lint --workflow <file> --projects <file>: <project> (<checks>) ==`, a `+ <check> <where>: <message>` line (with `  fix:`) for each warning the candidate adds, a `- …` line for each it removes, `✓ the candidate adds and removes no warnings` when there are none, then `N added, M removed against the live files (live N warnings, candidate M warnings)`. Warnings match on check and message, with the candidate's root and file read as the live ones, so a moved line is no change; equal ones count as often as they occur. When the candidate workflow file (else the projects.yaml) sits in another git checkout than the live root, a worker's worktree, that checkout is the project root, so a job brief, role prompt or department workflow file the branch adds counts as there. Either flag works alone; a candidate that doesn't load is an error naming it (exit 1), a projects.yaml without the project too; `--fix` beside either flag is a usage error (it writes the live board). `--json`: `{project, workflow, projects, checks, added, removed, live_warnings, candidate_warnings}`, findings in the shape above. Department workflow files are not swapped (only Product's); `process workflow --file` previews one of them |
| `health snapshot` | Takes a snapshot now (trigger `manual`), prints `✓ Snapshot written: <path>`, then the same summary. Fails with `health snapshots are off for this project (health.off: true in workflow.yaml)` or `<dir>: not in a git repository`. `--json` prints the snapshot |
| `health backfill [--days 90]` | Computes one snapshot (trigger `backfill`) for each of the last N days that has none, as it stood at 23:59 that day: the codebase at the commit the branch had then, the workflow over the issues done by then. Prints one line per day (the path, `skipped: has a snapshot`, or `(before the first commit: no codebase)`), then `✓ Backfilled W of N days (S skipped, F failed) · charts: <url>`. The milestone family is left out (it knows only the present). `--days` outside 1–3650 is a usage error (exit 2). `--json` prints `{written, skipped, failed, days: [{day, path, skipped, error, no_commit}], charts}` |

### `bench`

The [factory benchmark](../benchmark.md) (`cmd/issue-cli/cmd_bench.go`, `internal/bench`). The benchmark lives in the owner's folder (`~/.config/ai-workflow-engine/bench/`, or `$ISSUE_VIEWER_BENCH_DIR`).

| Subcommand | Behaviour |
|:--|:--|
| `bench` | The benchmarks, task counts (holdout), the last run's line, and the `smoke:` line for this issue-cli; with none, the next steps (`candidates`, `task add`, `smoke`) |
| `bench smoke [--setup <ref>] [--engine <dir>] [--keep] [--json]` | Proves the harness before a real run, with stand-ins and no model: the fixture's three tasks through `bench run`'s code, one `✓`/`✗ <check>: <detail>` line per check (fixture, run, stages, trace, usage, calls, isolation, report, control), then `bench smoke: PASS (3 tasks, <s>s)` or `bench smoke: FAIL (<check>, …)`, exit 1. Anyone may run it, no `--quote`. A PASS writes `smoke.json` in the bench folder, nothing else. See [Smoke before a run](../benchmark.md#smoke-before-a-run) |
| `bench candidates [--all] [--limit <n>] [--benchmark <name>] [--department <key>] [--replay [<slug>...]]` | Done issues of the project usable as tasks: `<slug>  <type> · N lines in F files · H hidden test files (T tests) · base..merged`, 20 by default (`--limit`), the fewest risks first and across types, ending with the `task add` and `--agents` lines. `--all` shows every one and the rest with why (`no recorded branch head`, `no test files in its commits`, `N lines (over 600)`, `N files (over 15)`). `--replay` replays each proposed one in a throwaway worktree and adds `replay: solvable \| needs later main \| doesn't replay \| tests pass on base \| not checked — <why>` (go test after mem-guard, no model call). `--department <key>` lists output tasks instead: that department's done issues with an output the owner approved and an eval for their type (`<slug>  <type> · output <id> (<title>) · eval <id> (<score>)`); without it, an `Output tasks:` line names the departments whose workflow declares evals. Writes nothing |
| `bench task add <benchmark> <slug> [--base --merged --start --check --holdout --snapshot --no-verify]` | Owner only. Finds base, merged, hidden tests and the snapshot, checks merged and base once in a throwaway clone; a failing check refuses with `<slug> not added: the check fails at the base: …` |
| `bench task remove <benchmark> <task>`, `bench holdout <benchmark> <task> on\|off` | Owner only. `<task>` is `<project>/<slug>` or a unique slug |
| `bench budget <benchmark> [--tokens --usd --wall --task-wall]` | Prints the budget; with a value (owner only) sets it |
| `bench show <benchmark>` | Tasks, budget, worker, judge; holdout tasks named only from the owner's shell |
| `bench run <benchmark> [--agents claude,codex] [--model <name>] [--setup <ref>] [--engine <dir>] [--workflow <file>] [--label] [--resume <run>] [--keep] [--quote]` | Runs every task in a sandbox (progress on stderr), prints the run and the next commands. `--agents` runs every task on each agent, one run.json with the agent on each record ([Both agents](../benchmark.md#both-agents)). `--model` starts every worker on that model, for a compare beside a run on the project's own ([Another model](../benchmark.md#another-model)). An agent session needs `--quote` with the owner's words. `--json` prints the Run |
| `bench report [<benchmark>/<run>\|latest]` | The run: setup, stop, summary, one line per task, the holdout as n/m |
| `bench compare <run>` | A two-agent run: pass, time, tokens and cost by agent and type, and a `Routing:` line per type saying which `routing:` rule the numbers support or contradict. Cost without a price reads `cost unknown` |
| `bench compare <a> <b>` | Verdict, deltas, warnings, tasks with regressions first, and the table by agent when the runs hold two agents between them. Exit 0 no regression, 1 B regresses, 2 not comparable |
| `bench nightly [<benchmark>] [--run <job run>] [--quote] [--dry-run]` | The night: the benchmark on main's head, then at most one queued experiment judged against it (never adopted), then the morning numbers in `nights.jsonl` and `nights/<date>.md`. Held while memory is ≥ 50% or the load is at or over the core count (or auto-start's `max_load`). `--run` takes the owner's words from the nightly-improve job's standing start; a worker only for its own run. Exit 1 when tonight already ran or the job has no standing start ([Every night](../benchmark.md#every-night)) |

Refused in an agent session: `only the owner changes the benchmark; bench <verb> changed nothing.` with the comment to post instead.

### `playtest prune`

`make ux-test` runs every session, so every worktree collects every session's runs (`.playtest/runs/<session>/<time>-<mode>/`). The issue page's Evidence tab needs only the latest auto and judge run of each of the issue's own sessions (`issue:` in `results.yaml`). A game's runner writes modes `ux-test` and `playtest`; they count as auto and judge (the Timeline and Evidence tabs read them the same way).

```
issue-cli playtest prune <slug> [--dry-run]   # one issue's worktree
issue-cli playtest prune --all [--dry-run]    # every worktree that belongs to an issue
```

- **Done issue:** of each own session and mode, keep the newest run with steps (a header-only run, left when a runner stopped, doesn't count) and every run newer than it; move them to the project root's `.playtest/runs/` (a run already there stays put), delete the rest and the runner's cached binaries (`runs/.bin`, about 35 MB a worktree). An own run goes only when a newer run of its session and mode is kept. Also kept: the run a kept `-cached` run takes its shots from (its steps' `cached:`), an own run of a mode other than auto or judge, and a run it can't classify (a `results.yaml` that isn't YAML and names no other issue, or files with no `results.yaml`). Removing the worktree afterwards keeps the evidence. `transition --to done` does this by itself.
- **Open issue:** delete only the runs of other issues' sessions and empty aborted runs (no `results.yaml`, no files).
- Each done issue gets a line `<slug>: archived N of M runs, deleted K`, with a ⚠ when none of its own runs was archived.
- `--all` skips hidden folders (`.worktrees/.ci`) and names the worktrees with no issue it left alone.
- The report gives the MB deleted, moved and kept, and what is left (`After: 71 MB. Run without --dry-run to apply.`).
- The viewer's daily health pass later shrinks the shots of issues done 30+ days ago (`health.shrink_shots_after_days`, see [Workbench Files](../workbench-files.md)).

### `check-script`

Runs an issue's [guided check scripts](../patterns/guided-check-scripts.md): `scripts/checks/<name>.sh` files whose first lines carry `# issue: <slug>`, found in the issue's worktree: the workflow's, else the git worktree on `work/<slug>`, else (while the project folder holds no script for the slug) the one worktree on any branch whose script names it (several: the one you run it from, else check-script names them and exits 1); when the workflow has no worktrees, the worktree you run it from, else the project folder. Once that worktree's branch is merged into main and the worktree holds no uncommitted change, it runs from the project folder instead and says so (`--new` still writes into the worktree). The slug may hold a space (`# issue: level design/rock-sentinel`); words after it are a comment. Runs with a bash 4 or newer (PATH, then Homebrew and MacPorts folders; `brew install bash` on stock macOS). With the feature on, `transition --to human-testing` needs each script's newest `--yes` run to have no failed step.

```
issue-cli check-script <slug>               # the owner's run (stdin a terminal): questions, then the tick offer
issue-cli check-script <slug> <name>        # one script
issue-cli check-script <slug> --yes         # machine run: machine checks only, nothing ticked (agents, before handover)
issue-cli check-script <slug> --yes --cli <path>  # the scripts check that issue-cli ($ISSUE_CLI), e.g. a branch build
issue-cli check-script <slug> --list        # name, path, whether the owner has run it; exit 1 when there are none (--json)
issue-cli check-script <slug> --new <name>  # write a starting script: one step per unticked Test Plan ### Manual box
issue-cli check-script --lib                # print the helper library (needs no project)
```

- The script runs under bash in its checkout with `CHECKS_LIB` (the library, written to a cache file named after its content), `CHECKS_SLUG`, `CHECKS_PROJECT`, `CHECKS_ROOT`, `CHECKS_NAME`, `CHECKS_DIR` (the folder the script sits in: scripts/checks/, or the temporary folder a script from history and the `<name>-*` files removed with it are written to), `CHECKS_ISSUE_CLI` (this binary: the tick uses it), `ISSUE_CLI` (the issue-cli the script checks: `--cli <path>`, else the branch build `<worktree>/issue-cli` when the issue's own checkout has one, printed as `Checking the branch build …`, else this binary) and `ISSUE_VIEWER_CONFIG` (the config this call resolved). A machine run is also what you get when stdin isn't a terminal.
- `--cli` that isn't an executable file is a usage error.
- A machine run (`--yes`, or stdin not a terminal) whose `$ISSUE_CLI` is the checkout's `./issue-cli` exits 1 before any script runs when that build is older than the newest commit under `cmd/` and `internal/` or the newest Go source (not a `_test.go`) or embedded file there (a clilog or other file a run leaves doesn't count): it names the change and prints the build command. A `--cli` path elsewhere isn't compared.
- Exit codes: 0 when every script passed or none is needed (below), 1 when one failed (`check script failed: <names>`) or none was found (the message says how to start one), 130 when the owner stopped with `q` or Ctrl-C.
- An issue whose own path runs no guided checks while the workflow does (no status on its type's `path:` has a prompt naming `check-script`: a report, an epic) needs none: a run with no script prints `✓ No check scripts needed for <slug>: its type report (idea → in progress → human-testing → done) has no status that runs guided checks. Nothing to run.` and exits 0, before any missing-worktree refusal; `--list` still exits 1 and says `none needed` instead of how to start one. The YOLO line of `show` and `start` names `check-script --yes` only when the issue's path runs guided checks.
- With worktrees on and the issue's worktree missing, it says which worktree and the `git worktree add` line, unless git history has the issue's script (below).
- After the merge: shipping removes the scripts before the merge, so when the checkout has none for the slug, check-script reads git history (`git log --full-history`, reachable from HEAD and from `work/<slug>` while that branch exists) for the newest commit that removed a `scripts/checks/*.sh` whose `# issue:` line names the slug. It writes that copy to a temp folder and runs it from the project folder, first printing `scripts/checks/<name>.sh from history (removed in <sha> before the merge): running it from <root>`. `--list` adds `from history: removed in <sha> before the merge`; `--list --json` adds `"from": "<commit>"`. A script on disk always wins.
- `show`, `start` and `transition` print a **Guided checks** block at testing and human-testing when the workflow uses the feature (a testing or human-testing prompt names `check-script`): the scripts and who runs what, or, with open Manual boxes and no script, the `--new` line.

### `approve`

`issue-cli approve <slug> --to <status> --quote "<the human's words>" [--how "<where>"]` is how a lead records a gate the human approved in chat:

1. It writes an `approval: "<quote>" (<how>, <date>) → <status> — recorded by <session>` comment (`tracker.ApprovalComment`). `--how` defaults to `in the lead chat`. The viewer's words box writes the same comment with `viewer` as how, and the Timeline pairs these comments with their approvals.
2. It sets `human_approval` to the status. It never toggles an existing approval off, unlike the viewer's button.

`--to` and `--quote` are required. It is refused:

- from a worker session (the error suggests a `question:` comment to its lead);
- for Human Testing, always: the human approves that in the viewer.

These checks read the environment and the session name; they are guardrails for agents, not security.

### `report-bug`

`issue-cli report-bug "<what went wrong>"` is for bugs in issue-cli or the workflow engine, from any project. A project's own bugs go on its own board with `issue-cli create`.

**Where it goes.** The engine project in the same `projects.yaml` (found by the [config lookup](#config-lookup), so report-bug works from any folder once the viewer has run with `-config`): `engine_project: <slug>` at the top, else the project whose `repo:` is `kacperwerema/dark-factory`. The reporter needs no setup, and `--project` is optional.

**What it files.** An issue in the engine project at the bugfix type's first status (`idea`), system `CLI`, label `cli-bug`, with:

- the first sentence as the title (cut near 80 characters)
- a `## Report` section: the full text, plus the reporter's project and issue slug, agent, issue-cli version and OS, and the command. The command is `--cmd "<command line>"` if given, otherwise the agent's previous issue-cli call from its clilog (if it's under 10 minutes old), with free text shown as the clilog's `‹text …›` placeholders
- `reported_from: <project>:<issue slug>` and `reported_by: <agent>`

The `## Repro` section is left for triage, so the bugfix gate still needs a human repro.

**From a release candidate.** When the reporting build is a candidate (`v0.54.0-rc.2`, or a build past one), the bug belongs to the release being soaked. A new issue gets that milestone's `version:` (the label the board already uses for v0.54.0, e.g. `0.54`). A `report:` comment sets it on an issue that has no version, and leaves any other version alone. The output then says `Added to milestone 0.54 (found in release candidate v0.54.0-rc.2): the release waits for its fix`, or that the issue keeps its version. `--json` adds `milestone` and `found_in`. The engine's Status page lists these reports against the candidate ([Roadmap: Cutting a release](../roadmap.md#cutting-a-release-candidate-soak-publish)).

**Dedupe.** If an open `cli-bug` issue has the same title after normalizing (lowercase, punctuation dropped, spaces collapsed), the report becomes a `report:` comment on it instead. If the only match is closed (done or parked), a new issue `<title> (again)` is filed, linking the old one. When an issue file already sits at the new issue's slug (a closed `(again)`, or an issue with that title but no `cli-bug` label), the title takes the next free `(again 2)`, `(again 3)`, …, and the body names the issue in the way. The issue is read back from the file create wrote, so a slug that create cleaned of a private name ([Leak scan](../workflow.md#the-leak-scan)) still files. A lock makes two agents reporting at once produce one issue.

An open `cli-bug` whose `## Report` quotes the same error takes the report too, whatever its title: a quoted span (backticks, double quotes, or single quotes around words) of 3 words or more that is not a command line (`issue-cli …`, `go …`, `git …`, `make …`, `./…`), compared normalized. The output then says `✓ Already reported (same quoted error): …`, and `--json` has `"matched": "error"` (`"title"` for a title match). Closed issues are matched by title only.

**Maybe fixed since.** When the report's issue-cli version names a commit git knows in the engine checkout (a `git describe` stamp, a tag, or a Go pseudo-version's commit; not `dev`), report-bug looks through the engine's `main` (its `ci.branch` when set) since that build for commits whose diff adds or removes a quoted error (`git log -S`, the quote and its longest literal part, so a printed message finds its format string; at most 5). It lists them in the issue body, or in the `report:` comment, under `Maybe fixed since <version>:`, adds the `maybe-fixed` label, prints them with the hint to try a current build first, and puts them in `--json` as `maybe_fixed: [{sha, subject}]`. An unknown build, a missing checkout or a git error skips the search; the report always files.

**No engine project, or its issues directory is missing.** The report is saved in `${XDG_DATA_HOME:-~/.local/share}/issue-cli/bug-reports/` (per user) and the output says `saved locally, not sent` and why. With no config found at all, the why is the config lookup's error: every place checked and the fix. The next report-bug that finds the engine sends the queue, oldest first, and so does `issue-cli report-bug --send`. A queued report that fails again stays queued and the rest are still sent; the `still queued` line names each failed file and its error. Until then, the Status page shows *N issue-cli bug reports not sent* under Needs you.

**Old reports.** `issue-cli report-bug --import ~/Work/bugs/*.json` files the old JSON reports with the same dedupe. It finds the reporter's project from the `issue_slug`, and moves each filed file into `imported/` next to it, which the Retros page skips.

**Output.** Agent-facing; it always names the issue or the queue file:

```
✓ Reported as new cli-bug issue cli/<slug> on the dark-factory board
✓ Already reported: added your report to cli-bug issue cli/<slug> on the dark-factory board (3 reports)
saved locally, not sent: ~/.local/share/issue-cli/bug-reports/<file>.json
✓ Sent 2 queued report(s): cli/<a>, cli/<b>
```

`--json` prints `{"result": "created|commented|queued", "project", "slug", "file", "reports", "queued", "reason", "imported": [...], "sent_from_queue": [...], "still_queued"}`.

The `Sender` interface in `internal/clibug` is where a second transport (GitHub issues, for machines without the board) plugs in.

### `retro`

A retro sweep reads what `retrospective` and `postmortem write` left in `retros/`, then moves it to `retros/archive/`.

- `issue-cli retro list [--since <YYYY-MM-DD or RFC 3339>] [--json]` lists the files directly in `retros/` written since the last sweep's mark (every file there when no sweep is logged), oldest first, by each file's `Date:` line (else its file time). It prints the file's kind, the post-mortems still waiting for the owner, and the `retro archive` command that ends this sweep. `--since` reads from that time instead of the mark. It only reads.
- `issue-cli retro archive --until <time> [--since <date>] [--json]` moves the same window to `retros/archive/` and appends one line to `retros/archive/log.jsonl` (`at`, `by`, `since`, `until`, `moved`, `waiting`, `clashes`). `--until` is required and is the time `list` printed, so a retro written while the sweep read stays for the next one.

The mark is the log's newest `until`, so a sweep that dies before it archives moved nothing and marked nothing. Two kinds of file stay in `retros/`, and the output names them: a post-mortem whose newest actions the owner hasn't answered (`postmortem.Waits`, the rule Needs you counts by, which stays true in `archive/` too: one left in place stays in the next sweep's sight), and a file whose name `archive/` has already (nothing is overwritten). A post-mortem left behind the mark isn't listed again; once answered, `retro list --since <its day>` and that archive command move it. `archive/` is still read by the Retros page, `experiment candidates`, `memory candidates` and `postmortem list`.

A sweep files issues when its brief asks for them; otherwise it lists its proposals in its report and the lead files them. The `builtin:retro-sweep` job brief (`cmd/issue-cli/templates/jobs/retro-sweep.md`) asks for none, so its run files nothing; it is the sweep's steps, on a department's or the Product entry's `jobs:` ([Recurring jobs](../departments.md#recurring-jobs)).

### `process transitions`

**Candidate workflow file (`--file`).** `process workflow --file <path>` and `process transitions --file <path> [--type <t>] [--system <s>]` read any workflow file (relative to the shell's folder) and write nothing: a file being spliced before it replaces the live one ([Bringing a template change into a live department](../departments.md#bringing-a-template-change-into-a-live-department)). The output is the live file's layout with `Workflow file: <abs> (candidate, read only: not the file the board reads)`, then `== Lint (file checks) ==`: the `health lint` checks that read only a workflow file (`lint.File`: schema, workflow, template, placeholders, …), each with its fix, the `health lint --workflow <abs>` command that runs the board's checks on it as Product's file and prints what it changes, and the commands for the live files. The file stands alone: Product's project-wide blocks are not merged in. A missing file, a parse error or no `statuses:` is an error naming the file (exit 1). `--file` beside `--department` or an issue slug is refused (each names another file); it needs no project. `--workflow` (an alias for `--system`) with a value holding `/` or ending in `.yaml`/`.yml` is refused before any topic runs, pointing to `--file` and `--department`.

**Workflow file line.** `process workflow`, `process transitions` and `show` print `Workflow file: <absolute path>` under their header (`built-in default`, the bundled `development` template, when the project has no workflow file), so an agent can see which workflow it is being walked through. `show --json` carries it as `workflow_file`.

**Timeline and Evidence lines.** `show` prints `Timeline: <viewer url>/p/<project>/issue/<slug>?tab=timeline` and `Evidence: …?tab=evidence` after the lead and dependency lines: the issue page's Timeline tab (where its time went, every CLI call) and Evidence tab (is it proven: shots, criteria → proof), links a lead or agent can hand the owner. Not in `--json`.

**Brief line.** `show` prints `Brief: <text>` next: the sentence the issue page's Issue tab leads with (`tracker.Brief`, see [Issue File Format](../issue-format.md#the-brief)), so an agent sees what the owner will read first. With no brief it prints `Brief: none.` and how to write one (a lead paragraph or `**In short:**`).

Rules are rendered from the loaded workflow rather than a hardcoded list. Each
row lists the validation rules, human-approval gates, and side effects
(`set_fields`, `append_section`, `inject_prompt`) attached to the transition;
optional and global statuses are surfaced separately.

Three scopes are supported. Without arguments the default project workflow is
printed and a hint at the bottom names any per-system overlays and points at
the scoping flags. Passing `--system <name>` (alias `--workflow <name>`)
prints the rules merged with that system's overlay. Passing an issue slug
resolves the issue's `system` field and prints the scoped rules; issues with
no system explicitly say `(no system overlay; project default)` so agents do
not mistake the default output for an issue-specific one.

In projects with types of work, an issue slug also applies the issue's type
(base → type → system), and `--type <name>` scopes without an issue (combine
with `--system`). The header names the type: `== Transition Rules — type
"tweak", system "UI" (issue ui/x) ==`. Unscoped output in a typed project says
it shows the base rules and lists the types.

### Types of work

When `workflow.yaml` defines `types:` ([Types of Work](../Workflow/types.md)):

- `create --type <t>` writes `type:` and starts at the type's first status; without `--type` the issue gets `default_type`. The output names the other types and how to switch.
- `set-type <slug> <t>` works while the issue is at its type's first status; later it refuses and links the viewer, where the human changes Type. If the current status is not on the new path, the issue moves to the new type's first status.
- `show`, `start` and `transition` print `Type: <t>` (and `Path:` in `show`), and warn when `type:` names no defined type or the status is off the type's path. Bad-order errors name the type's path.
- `show --json` adds `type` and `type_path`; `transition --json` adds `type` and `type_path`.
- Projects without `types:` print exactly what they did before; `--type` there is an error pointing at the doc.

The renderer is backed by `tracker.DescribeAction` and
`tracker.ValidationSummary` so descriptions stay in sync with the same strings
shown in transition previews.

### `data`

`issue-cli data <sub> <slug>` reads and writes the per-issue structured data store (`<slug>.data.json` next to the issue's markdown file). Subcommands are `add`, `list`, `set-status`, `set-tier`, `set-comment`, `remove`. Agents must use the CLI rather than touching the JSON file directly so the on-disk shape can change without breaking them. Full reference: [Per-issue Data Store](../data-store.md).

```bash
id=$(issue-cli data add <slug> --description "finding" --tier "🔴 critical")
issue-cli data set-status  <slug> "$id" "🔥 must-fix"
issue-cli data set-tier    <slug> "$id" "🟢 nice"        # pass "" to clear
issue-cli data set-comment <slug> "$id" --text "see processor_test.go:142"
issue-cli data list <slug> --json
```

`--tier` and `set-tier` validate against the body's `<!-- data tiers=... -->` enum when present; without a tiers marker the workflow opts out of validation and any value is accepted.

`add` prints the assigned id on stdout (and a human line on stderr) so it composes in shell pipelines. `--json` on `list` emits the entries array exactly.

### `evidence`

`issue-cli evidence add [<slug>] <file>… [--step <id>] [--force]` copies each still, or any other proof file (a CPU profile, a text capture), into `<issues>/_evidence/<slug>/` of the project's issues folder: the primary checkout's, whatever folder the call runs from, so a worker in a worktree (which has no `issues/` tree) puts its stills where the issue's [Evidence tab](../UI/overview.md#the-evidence-tab) shows them. A dispatched worker leaves the slug out (`ISSUE_VIEWER_ISSUE_SLUG`); a first word that names no issue is a file.

- Files are read as `attach` reads them: any file, at most 10 MB, from inside the worktree, the project or the temp folder. An image (PNG, JPEG, GIF or WebP, sniffed from the bytes) is saved under its base name with the sniffed type's extension (`shot.jpeg` is saved as `shot.jpg`); any other file keeps its own name and extension, cleaned to ASCII letters, digits, `.`, `_` and `-`. A non-image named like an image (`fake.png` holding text) is refused, and so is a name starting with `.` or `_`.
- `--step TP3` files the still under a step: it is saved as `TP3-<name>` (a step id is letters then digits; lower case is raised), and the tab and `list` show the step.
- A name already in the folder is refused, naming it, unless `--force` replaces it. Every file and name is checked before the first write, so a refused call saves nothing (exit 1). A save goes through a temp file and a rename.
- Output: per file `✓ <path>` and, under it, the markdown link (`![<name>](/p/<project>/files/issues/_evidence/<slug>/<file>)`, a plain `[<file>](…)` for a file that is no image) to paste into the body; `--json` prints `[{file, name, path, url, markdown}]`.

`issue-cli evidence list [<slug>]` (alias `ls`) prints the folder with the count, then `name  size  date` per still, by name; with none, `No evidence for <slug> yet: issue-cli evidence add <slug> <file>` and exit 0. `--json` prints `[{name, path, step, size, modified}]`. Images or files for a comment or the body with no name to keep go through `issue-cli attach` (hash-named, under `attachments/`). Nothing is committed: issues/ is git-ignored where this matters.

### `workflow init`

`issue-cli workflow init` bootstraps a fresh project directory. It writes `workflow.yaml` from one of the bundled templates and scaffolds `issues/` and `docs/` if they do not already exist.

```bash
issue-cli workflow init --template development
issue-cli workflow init --template review --force
issue-cli workflow init                          # interactive prompt
```

Templates:

| Name          | Status set                                                                  | Use case                                  |
|:--------------|:----------------------------------------------------------------------------|:------------------------------------------|
| `development` | `idea → in design → backlog → in progress → testing → human-testing → documentation → shipping → done` | Software delivery flow (mirrors this repo) |
| `review`      | `inbox → triaged → reviewing → needs-changes → approved → archived`         | Review and triage of incoming items       |
| `writing`     | `idea → outline → drafting → editing → review → published`                  | Long-form content                         |
| `content`     | `brief → draft → owner review → publish → done`                              | Recurring outputs (posts, SEO pages) for a department: the owner's `publish` approval before anything goes out, the published link as proof |

Behaviour:

- `--template <name>` selects a template. Without it, the command shows a numbered prompt when stdin is a terminal; piped or scripted invocations must pass the flag and exit non-zero with the list of valid templates if they don't.
- `--out <path>` writes the template to that path instead of `workflow.yaml` (no `issues/` or `docs/` scaffolding) and prints the `departments:` lines to add to `projects.yaml`. Use it for a [department's workflow file](../workflow.md#department-workflows).
- `--force` overwrites an existing `workflow.yaml` (or the `--out` file). Without it, the command refuses to touch the existing file and exits non-zero.
- `issues/` and `docs/` creation is idempotent — running the command in an already-initialised project does not error and does not re-create directories.
- Templates live as plain YAML under `cmd/issue-cli/templates/workflow/*.yaml` and are embedded at build time via `//go:embed`. The list of valid `--template` names is derived from the embedded directory, so adding a new template is just dropping a new `<name>.yaml` file there and rebuilding.
- Each template starts with a `template: <name>@<release>` stamp and a `features:` record, so a new project is stamped and recorded from the start. A new template needs both; run `issue-cli workflow scan --template <name>` in the checkout to write the record. See [Template Stamp and Feature Record](../Workflow/overview.md#template-stamp-and-feature-record).

### `project init`

`issue-cli project init <dir>` makes a new project the viewer can show: the folder with `issues/`, `docs/` and `workflow.yaml` (a bundled template, with its `template:` stamp and `features:` record), and the project's projects.yaml entry with absolute paths (`issues`, `docs`, `workflow`, `workdir`).

```bash
issue-cli project init ~/src/shop --dry-run                    # list every folder, file and config change
issue-cli project init ~/src/shop --name "Acme Shop" --config ./projects.yaml
issue-cli project init ~/src/shop --template writing           # entry printed only
```

Behaviour:

- `--name` (default: the folder's name), `--slug` (default: from the name), `--template` (default `development`).
- `--config <file>` is the command's own flag, after `project init`. A file that doesn't exist is written with `projects:` and the entry; an existing one gets the entry spliced into its `projects:` list as text, every other line kept, and the result must load before it replaces the file (the same code as `department add`). Without `--config` the entry is printed and no config is touched: not the one issue-cli reads, not the viewer's. The global `--config` only picks what issue-cli reads.
- Nothing is overwritten. A folder already there is reported "already there"; a `workflow.yaml` the same as the template "already there, same as the template"; one that differs "kept your file". A config that has the project for this folder is left as it is; a project with the same slug elsewhere is refused (pick `--slug`). A second run lists what is there and exits 0.
- A worker is refused when `--config` is the config issue-cli reads (the owner's projects.yaml); a dry run or a sandbox config is fine.
- After it, `Next:` lists `git init` (when the folder is no git repo: workers commit with git, and `worktree: true` gives each issue its own worktree), `issue-viewer -config <file>` (the company page `/p/<slug>/company` has the New department tile), `lead spawn` (the CEO), `department list`, `create` and `workflow upgrade` (privacy and newer features), each with `--config` and `--project`.
- `--json` gives the plan: `dir`, `name`, `slug`, `template`, `files` (`path`, `kind`, `state`: create, write, there, same, keep), `config` (`path`, `state`: print, create, add, there; `entry`), `git_repo`, `dry_run`, `next`.

### `workflow upgrade`

`issue-cli workflow upgrade` (alias `issue-cli upgrade`) tells an agent which `workflow.yaml` features the project could adopt. It is **read-only**, always exits 0, and never writes a file.

```bash
issue-cli workflow upgrade                      # proposals and steps
issue-cli workflow upgrade --all                # every catalog feature the file lacks, declined ones flagged
issue-cli workflow upgrade --template review    # compare an unstamped file with this template (default development)
issue-cli --json workflow upgrade               # machine-readable plan
issue-cli workflow upgrade --department Marketing  # that department's own file, against its own stamp
```

- **Status:** with the current and bundled stamps:
  - `up_to_date`: the stamp is the bundled one and the record decides every feature.
  - `undecided`: the stamp is current, but proposals wait on the owner. The message says how many and that `workflow scan --installed/--declined` records each pick.
  - `stale_record`: the stamp is current, but the file has features its `features:` record doesn't list (the "detected but not in the record" line). `issue-cli workflow scan` records them.
  - `behind`, `unstamped`, `custom` or `unknown_template`.
- **Record:** the `features:` record decides what is installed when the file has one (`record_source: record`); without one, the catalog's probes do (`detection`).
- **Proposals** are numbered. Each one has the feature id, `kind` (`new`, or `update` from an older installed version), version, category, whether the bundled template ships it, and the text the agent relays: What, Why, Where, Ask (the question for the human), and a YAML Snippet. By default the list covers `propose: true` entries for the project's template that the file lacks and nobody declined. With a `features:` record the record decides and the stamp hides nothing; without one, only entries newer than the stamp are listed (all of them when unstamped). An entry with `requires:` waits until the project has those features; `--all` lists it tagged `needs <id>`.
- **Missing and record only:** the proposals print in two blocks, each with its count. `== Missing (N) ==` holds the features not in the file, each in full: the ones to adopt. `== Present, record only (N) ==` holds the features the file already has whose record is older (an `update` the probes find, one line each: recorded and catalog versions) or absent (detected but not in the record), and names `workflow scan --installed <ids>` and `workflow scan` to record them. A file whose every proposal is present says "Nothing missing" and its steps are the scan and the stamp, no adoption. A file with every feature present and recorded at a current stamp prints no proposal.
- After them come two lists: already present, and declined (with the re-open command `workflow scan --forget <id>`). These are never proposed.
- **Steps**, printed whenever there is something to propose:
  1. Continue only if the operator asked.
  2. Propose the items one at a time.
  3. `cp workflow.yaml workflow.oldN.yaml` (the first free N).
  4. Write the picked snippets, adapted to the project.
  5. Only once every proposal is installed or declined, set `template: <name>@<latest>`; while any is undecided, leave the stamp as it is.
  6. `issue-cli workflow scan --installed <picked> --declined <declined>`.
  7. `issue-cli process workflow`.
- `--json` emits one object: `status`, `workflow_file`, `template`, `current`, `latest`, `record_source`, `record`, `all`, `proposals[]` (an update the file has carries `present_in_file: true`), `missing` (the proposals to adopt), `record_only` (present updates, then the unrecorded ids), `already_present`, `declined`, `detected_unrecorded`, `undecided` (the ids proposed without `--all`), `backup_path`, `steps[]`, `message`.

**No hints.** No other command mentions upgrades: `start` and `process workflow` used to end with an upgrade hint, which reached every worker and confused them. The operator sees the same plan on the viewer's Updates tab (`/p/<project>/updates`). A stale record is different: it is a fact about the file, not an upgrade. `process workflow` and `health lint` (check `template`) warn "features: record is stale: <ids> detected in the file but not recorded" and name `issue-cli workflow scan`, whatever the stamp. `health lint`'s "template: … is behind the bundled …" line counts only the missing features ("N missing feature(s) to adopt") and names `issue-cli workflow scan --installed <ids>` for the present, record-only ones. Undecided proposals stay the owner's, and neither command mentions them.

### `workflow scan`

`issue-cli workflow scan` runs every catalog probe over the project's `workflow.yaml` and writes the `features:` record.

Run from another worktree of the repository that holds that file (a worker's `.worktrees/<slug>`), scan and `workflow upgrade` use the same file in your worktree, so the record lands on your branch and never as an uncommitted change in the primary checkout. A worktree without the file is refused: merge main first, or run it from the checkout that holds it.

```bash
issue-cli workflow scan --dry-run                         # report + the record it would write; writes nothing
issue-cli workflow scan                                   # write the features: record
issue-cli workflow scan --installed types-of-work,scoring # record features as installed at the catalog version
issue-cli workflow scan --declined worktree               # record features the human declined
issue-cli workflow scan --forget worktree                 # drop ids from the record (re-opens a declined feature)
issue-cli workflow scan --department Marketing            # scan that department's own file
issue-cli --json workflow scan --dry-run
issue-cli workflow scan --template development            # a bundled template: in a dark-factory checkout, rewrite its record from the checkout's catalog
```

- **Report:** every catalog feature grouped by category (each heading once: catalog order is by category, then id), marked ✓ detected, + recorded only, ✗ declined, or · absent. A detected line shows the evidence at its concrete path (`transitions[3].actions[1].rule = "has_test_plan"`, `+N more`). Then come the changes to the record, notes, and the block itself.
- **Merge rules:**
  - installed = detected ∪ already recorded ∪ `--installed`. A recorded version is kept; new entries and `--installed` ones get the catalog version.
  - declined = recorded ∪ `--declined`, minus anything installed.
  - A recorded feature that is no longer detected stays installed, with a note pointing at `--forget`.
  - Ids the catalog doesn't know (written by a newer binary) are kept.
  - `installed:` lists the entries in catalog order (by category, then id). A record with the same refs in another order is rewritten, with the change `~ installed: put in catalog order`.
- **`--template <name>`:** scans a bundled template instead of the project's file. In a dark-factory checkout it reads `cmd/issue-cli/templates/workflow/<name>.yaml` and the catalog from the checkout and writes the record into that file (`--dry-run` prints it); elsewhere it prints the bundled copy's record and writes nothing. It takes no `--installed`, `--declined`, `--forget` or `--department`.
- **Errors:** an unknown id, declining a feature that is present in the file, the same id in `--installed` and `--declined`/`--forget`, extra arguments, or a file that isn't a YAML mapping.
- **Writing:** scan writes only when the record changes, or when there is none yet; a second run reports "record already up to date". It edits text: it replaces the existing `features:` block, or inserts one (with a comment) after `template:`, or else before the first top-level key. It then re-parses the result and refuses to write if anything other than `features:` changed. Comments and layout elsewhere are kept. It never sets `template:`, so an unstamped file keeps its proposals.
- `--json` emits `workflow_file`, `dry_run`, `written`, `had_record`, `features[]` (`id`, `version`, `category`, `title`, `detected`, `matches`, `evidence`, `recorded`), `record`, `block`, `changes`, `notes`.

### `workflow commit-msg`

```bash
issue-cli workflow commit-msg .git/COMMIT_EDITMSG   # what the hook runs
issue-cli workflow commit-msg --install             # write the hook (make hooks, make install)
```

- **What it checks:** every `Catalog:` and `Proves:` line of the message, in any paragraph after the subject (lines starting with `#` and everything below git's scissors line are left out), read with the parsers `catalog-check` (`workflowupgrade.CheckTrailers`) and the proof check (`proof.CheckProves`) use. Refused: `Catalog: n/a` with no id, a colon after `n/a` or after the id (`Catalog: n/a some-id: why`), an id the catalog lacks (in a dark-factory checkout; elsewhere ids aren't checked), a `Catalog: <id>[,<id>] [<why>]` without `n/a` naming an entry that isn't bumped (its `version:` in the working tree isn't newer than the changelog's last release: the fix names the `catalog-bump` to run, or the n/a form; with the entry bumped the line is accepted and says what the bump is for); a `Proves:` line with no criterion, `<slug>#AC2`, a package path (`internal/memory`), a `<session>/<step>` or a bare box or step id (`TP4`), or a word that isn't a test name (`--project`; its hint says a JS/TS `it()` title with spaces goes in quotes). A Go subtest (`TestX/case`) is fine, and so is a quoted title (`"lista: id (ryba)"`), read as one test name.
- **Output:** nothing when the message is fine, but for notes on stderr (exit 0): each criterion a `Proves:` line links to a test "stays unproven until `issue-cli prove <slug> --run` … posts a tests: comment", and on an issue with no `## Acceptance criteria` "no Proves: line is needed here" (the issue is the checked-out branch's, looked up in the configured projects: `work/<slug>`, else, for a branch named otherwise, the one issue whose `branch:` field names it, the one whose slug it ends in, or the one slug the worktree's `HANDOFF.md` names; never on the main branch). Where the issue's workflow runs `commits_reference_issue`, the note "no commit on <branch> references the issue yet" prints the `Refs: <slug>` line, until this message or a commit on the branch references the issue (the rule's own reading, a slug in a subject counts): a fast-forward ship then needs no empty commit. In a session dispatched for that issue (`ISSUE_VIEWER_ISSUE_SLUG` names it, case aside) the hook writes the line into the message itself instead, through `git interpret-trailers` (in the trailer block, above the comment and scissors lines), and notes "added Refs: <slug> to the message": nothing is amended later, so a `tests:` comment's sha stays true. A merge under way (`MERGE_HEAD`), a session for another issue or none keeps the message as it is and gets the note; a failing `git interpret-trailers` falls back to the note and never blocks the commit. Otherwise `✗ commit refused: …`, each bad line with what is wrong and the form that works, on stderr, exit 1; the message stays in `.git/COMMIT_EDITMSG`. On an issue with no `## Acceptance criteria` a refused `Proves:` line gets that one fix instead of the per-word hints: "no Proves: line is needed here (drop it; box ids such as B1, I1 or TP1 are ticked with issue-cli check <slug> <id>)", never a playtest-step hint naming criteria the issue doesn't have.
- **The guard-check note:** in a checkout with `make guard-check` (`scripts/guard-check.sh`), the first commit on a branch other than main that stages a file under `cmd/` or `internal/` (no earlier commit since `main`, else `origin/main`, touched them; no merge under way) gets the note "the first commit on <branch> that touches cmd/ or internal/: run `make guard-check` now". Each guard failure there names its rule and its row in the [Guard tests](../testing.md#guard-tests) table.
- **A recorded line:** when `issue-cli workflow catalog-na` recorded a `Catalog: n/a` line in the checkout's git dir, an accepted message gets it (`git interpret-trailers`, merges too), the record is dropped, and the hook notes "added Catalog: n/a … to the message (recorded with issue-cli workflow catalog-na)". A failing `git interpret-trailers` leaves the record, and `catalog-check` says to paste it once everything is committed. The hook script adds a still-recorded line itself after an accepted message (or when `issue-cli` is missing or too old to check), noting "… the issue-cli on PATH didn't add it: make install": an installed `issue-cli` older than `catalog-na` accepts the message and leaves the line out, which on 2026-11-18 let a commit go without it.
- **The hook:** `--install` writes `commit-msg` in `core.hooksPath`, else the common `.git/hooks` every worktree shares. A commit-msg hook that isn't ours is kept as `commit-msg.local` and runs first. When `issue-cli` isn't on PATH, or the installed one is older than this command, the hook says so and lets the commit through: `catalog-check` and the proof check still read the lines later.

### `workflow catalog-check`

For issue-cli developers, in the dark-factory repository; `make validate` runs it after the tests, and the in progress guidance asks for it while the work is under way. It checks the git checkout of the current directory (a worktree checks its own branch, whatever `--project` names).

```bash
issue-cli workflow catalog-check               # compare the work branch with main
issue-cli workflow catalog-check --base main   # the branch it merges into
issue-cli --json workflow catalog-check
```

- **What it compares:** the files changed since `git merge-base <base> HEAD` (on a `release/vX.Y` branch, since its newest `vX.Y.Z` or `vX.Y.Z-rc.N` tag reachable from HEAD, the one with the fewest commits after it; the output says `catalog-check on release/vX.Y: reading the commits since <tag>`; the tagged commits are published and were checked, so a bad `Catalog:` line on one can't block a patch release; with no tag of its line yet it keeps the merge-base) (commits, the working tree, untracked files), less those whose working-tree content is the base branch's (the branch has them from a merge of main, one in progress too), against each catalog entry's `sources:` (a glob, or `file.go#Symbol`: that declaration's code, doc comments ignored) and against `cmd/issue-cli/templates/workflow/*.yaml`. The catalog, templates and changelog are read from the working tree, so it works with any binary.
- **Passes** when each touched entry's `version:` is newer than the last release (the newest `## vX.Y.Z` in `cmd/issue-cli/CHANGELOG.md`), or a commit on the branch (since that base) carries `Catalog: n/a <id>[,<id>] <why>`; and each touched template's stamp is newer than the last release. The `Catalog:` line counts in any paragraph of the message (also one before `Co-Authored-By`) and on merge commits; it is read with the same parser as `Proves:` (`proof.MessageLines`).
- **Before the commit:** `issue-cli workflow catalog-na <id>[,<id>] <why>` records the n/a line in the checkout's own git dir (`CATALOG_NA`, one per worktree). It counts as a line on the next commit (`✓ catalog <id>: marked n/a: …`, then "note: Catalog: n/a … is recorded for the next commit") while the working tree has uncommitted changes, so `make validate` is green on the uncommitted change; the commit-msg hook writes it into the commit that takes them. With everything committed and the record still there (no hook, or a hook too old) it counts for nothing, and the output says to paste it and run `catalog-na --clear`.
- **A bumped entry's id:** `Catalog: <id>[,<id>] [<why>]` without `n/a` is read too: it marks nothing the bump doesn't, and fails when a named entry isn't bumped past the last release, with the `catalog-bump` to run.
- **Fails** on a `Catalog:` line that marks nothing: no entry id after `n/a`, an id the catalog doesn't have (`Catalog: n/a refactor only`), or a bare id whose entry isn't bumped. The output names the commit and the problem, shows the form, and lists the entries the branch touches; reword the commit, or drop the line. `--json` carries them as `bad_trailers` (`commit`, `value`, `problem`).
- **Output:** one line per touched entry, template and bad trailer first (`✓ catalog <id>: bumped to …`, `✗ catalog <id>: needs a bump or an n/a (changes …)`, `✗ trailer on <sha>: …`), then the details, and a closing count (`catalog-check failed: 1 of 2 entries and 0 of 0 templates need a bump or an n/a; 0 Catalog: lines mark nothing`), so both the head and a truncated tail say what failed. Each changed file names the `sources:` pattern it matched (`changes internal/health/score.go (matches internal/health/**)`), so an unrelated match shows. A red run ends with what to paste: `→ looks like a feature change: issue-cli workflow catalog-bump <id>,…` for the entries whose own symbol (`file.go#Symbol`) or `detect:` region changed, and one `Catalog: n/a <id>,<id>,… <why>` line naming every other red entry, so one commit covers them all. The clilog record of a red run lists the red entry ids (and `template/<name>`) under `red`.
- **Fails** with, per entry: the changed files, the entry's version, the next release, and both fixes (`issue-cli workflow catalog-bump <id>` and update its what/snippet, or add the trailer). The catalog is read from the entry files at the head (`workflow-features/<id>.yaml`). A template gets the stamp to set and the `workflow scan --template <name>` to re-run; a change to its `features:` record alone needs no stamp. It then runs the catalog's own checks at the head, each with its fix: an unknown category, a source that matches no file (a bare folder: `<folder>/**`), `@prompt` beside other sources, and a bundled template whose `features:` record isn't what a scan records, in catalog order (the expected block is printed).
- **Skips** (exit 0) when the base branch itself is checked out (or HEAD is detached at it) and when there is no merge-base. A work branch with no commits yet is checked: its uncommitted changes count. Outside the dark-factory repository it fails.

### `workflow catalog-na`

For issue-cli developers, in the dark-factory repository: the `Catalog: n/a` line before the commit exists.

```bash
issue-cli workflow catalog-na health-tuning,parked-status a refactor, nothing projects adopt
issue-cli workflow catalog-na --clear
```

- **Writes** `n/a <id>[,<id>] <why>` to `CATALOG_NA` in the checkout's own git dir (`git rev-parse --git-path`: a worktree's, never the common one, so no other checkout reads it); a second call replaces it. Refuses the forms the hook refuses (an unknown id, a colon after it) and a call without a why.
- **Then:** `catalog-check` counts it while the change is uncommitted, and the commit-msg hook adds it to the next commit's message and drops the file (see [`workflow catalog-check`](#workflow-catalog-check)). Without our hook it says to paste the line yourself. With a hook installed before the hook script added the line itself, it asks the `issue-cli` on PATH (the one the hook runs) for `workflow catalog-na`: when that build is older, or there is none, it prints `✗ the issue-cli on PATH, which the commit-msg hook runs, is older than catalog-na: the next commit won't get the line`, with the fix (`make hooks`, or paste the line), instead of promising the hook writes it.

### `workflow catalog-bump`

For issue-cli developers, in the dark-factory repository: one step for a catalog version bump.

```bash
issue-cli workflow catalog-bump status-prompts                    # to the next release
issue-cli workflow catalog-bump a,b --version v0.58.0 --dry-run   # list the lines it would change
```

- **Changes:** the top-level `version:` line of the entry's file, `workflow-features/<id>.yaml` (a snippet's nested `version:` is left alone), the `template: <name>@` stamp of every bundled template the entry applies to when it is older, and that template's record line `- <id>@<old>`. Nothing else in any file changes.
- **Refuses** an unknown id, a version that isn't `vX.Y.Z`, and one older than the entry's. Bumping to the version it has only brings the templates in line ("nothing to change" when they are).
- **Output:** each changed file with what changed in it; `--json` the plan (`version`, `changes[]` with `path` and `what`).

### `projects` and multi-project resolution

`issue-cli projects` lists every project parsed from `projects.yaml`. Its first line after the heading is `config: <path> (<why>)`, the config the lookup chose. The active project (resolved via `--project` or the single-project default) is marked `(active)`; in a multi-project setup with no `--project` the historical fallback (`projects[0]`) is marked `(historical default)`. `--json` emits an array with `slug`, `name`, `issue_dir`, `viewer_url` (the address issue-cli uses for that project, see [`dispatch` and `status`](#dispatch-and-status); the issue-approve mod reads it), `active`, and `default` fields.

```bash
issue-cli projects
issue-cli projects --json
issue-cli --project cli projects   # marks "cli" as active
```

`projects` is intentionally tolerant of a missing or unreadable config file — it is the discovery surface a confused bot reaches for first, so it must succeed when nothing else does. `help` and `process` are in the same allow-list.

#### Config lookup

Which `projects.yaml` issue-cli reads (`findConfig`, `cmd/issue-cli/config_lookup.go`):

1. `--config <path>`
2. `$ISSUE_VIEWER_CONFIG`
3. `./projects.yaml`, **only if it exists**
4. the config named in `~/.local/state/ai-workflow-engine/viewer.json` (`$XDG_STATE_HOME`), if that config file still exists and its `pid` is alive (a record with no pid is still used). `issue-viewer -config` writes the file at start: `config` (absolute), `url`, `pid`, `started` (package `internal/viewerstate`). A sandbox viewer leaves it alone (`viewerstate.Claim`): a config in a temp folder while the state dir isn't, or another live viewer's record for a different config; `-record-viewer` overrides
5. `~/.config/ai-workflow-engine/projects.yaml` (`$XDG_CONFIG_HOME`), if it exists

Steps 1 and 2 are used even when the file is missing: the user named that file, so loading it fails loud instead of falling through to another config. When nothing is found, a command that needs a project fails with:

```
no projects config found. Checked, in order:
  1. --config: not passed
  2. $ISSUE_VIEWER_CONFIG: not set
  3. ./projects.yaml: not found
  4. ~/.local/state/ai-workflow-engine/viewer.json: not found (issue-viewer --config writes it at start)
  5. ~/.config/ai-workflow-engine/projects.yaml: not found

Fix: export ISSUE_VIEWER_CONFIG=/path/to/projects.yaml, or start the viewer (issue-viewer --config /path/to/projects.yaml)
```

A dead viewer's record shows as `4. …/viewer.json: its viewer (pid N, config …) is not running`. A `--project` the chosen config doesn't list fails with the file and where it came from:

```
project "dark-factory" not found in config /tmp/…/proj-18752.yaml
Available projects:
  rl
…
Config from: the running viewer's, http://127.0.0.1:18752 pid 4242, recorded in ~/.local/state/ai-workflow-engine/viewer.json
Wrong config? Pass --config <path> or export ISSUE_VIEWER_CONFIG
```

(The real message shows the absolute paths.) Bootstrap mode (a cwd with `./issues/`) still picks the project; the lookup only decides which config it matches cwd against. Tests in `cmd/issue-cli` run with their own `XDG_STATE_HOME` and `XDG_CONFIG_HOME` and no `$ISSUE_VIEWER_CONFIG` (`main_testmain_test.go`), so they never read this machine's config.

**Resolution order in `loadProjectOrErr`:**

1. **Explicit `--project <slug>` always wins.** A bot inside one project workdir (with its own `./issues/`) can still query a sibling project by passing `--project`. The bootstrap auto-detection on cwd yields to an explicit flag.
2. **No `--project` + a local `./issues/`** → bootstrap mode synthesizes a single project from cwd. `--project` is unused; `len(allProjects) == 1`.
   The workflow file comes from, in order: `workflow:` in `./project.yaml`, then `./workflow.yaml`, then the one `projects.yaml` entry whose `workdir` is cwd or whose `issues` is `cwd/issues` (paths compared absolute, symlinks resolved). `version` comes from `./project.yaml` or that same entry. An entry for any other directory contributes nothing. When no local file sets the workflow and several entries match cwd, it returns `errAmbiguousProject` and lists only the matching slugs. With no match and no local file, the project runs on the built-in default workflow.
3. **No `--project` + no local `./issues/`** → consult `projects.yaml`. With `>1` project, a cwd inside a git checkout picks the one project whose root (`workdir`, else the issues folder's parent) has the same git common dir: a linked worktree of a project, or a subfolder of one, works without `--project`. Otherwise it returns `errAmbiguousProject` (exit non-zero) and lists every configured slug, none marked the default (projects.yaml has none), so the bot retries with the right `--project`. Single-project setups silently use `projects[0]` — byte-identical to pre-multiproject behaviour.

**Error message contract.** When an issue isn't found in a multi-project setup, the error appends `Searched project: <slug>` and an `Available projects:` enumeration with a `Retry with: issue-cli --project <slug> ...` hint. Single-project setups keep the historical `issue not found: <slug>\n\nRun: issue-cli list` message verbatim — this is the regression guard for bots that already grep for it.

**Help output.** With `>1` project configured, `issue-cli` (no args) and `issue-cli help` print a `Configured projects:` block enumerating slugs with `(active)` / `(historical default)` markers. Single-project setups omit the block.

**Dispatched agent prompts.** When the web app dispatches a bot via `buildAgentPrompt`, every `issue-cli ` command the prompt tells it to run is rewritten to `issue-cli --project <slug>` so the bot doesn't have to discover the project. What the prompt only quotes stays as written: the issue's title, a body's blockquotes and fenced blocks, and a command inside a quoted string, such as the value of `prove --lead "issue-cli check-script <slug> --yes"` or the words of a `question:` comment. A single-quoted word after an `issue-cli` command, the way `shell.Quote` writes a value (`issue-cli check <slug> 'run issue-cli show'`), stays as written too. So does an `issue-cli` glued to a path or a name (`cmd/issue-cli`, `./cmd/issue-cli`, `my-issue-cli`): only one at the start of a word counts, plus the worker's own `./issue-cli` build. A command that already names a project keeps it, also when `--project` comes after another global flag (`issue-cli --config <path> --project <slug> show <slug>`, `--json`, `--debug`): only the global flags before the command count. Hints use the same rule (`tracker.InjectProject`), and every value a printed `issue-cli`, `tmux` or `git` hint carries (a box text, a status, the owner's quote) goes through `shell.Quote`, so it pastes into a shell unchanged: a plain value bare (`--to testing`), anything else single-quoted (`--to 'in progress'`). In bootstrap mode (`proj == nil` or empty slug) no rewrite happens — there is nothing to inject.

### `process schema` and `process changes`

`process schema` is driven off reflection on the YAML struct tags defined
in `internal/tracker/workflow_config.go`. Every `yaml:"..."` field must
also carry a `desc:"..."` tag — the tracker tests fail otherwise, so the
schema output cannot drift from the parser. Action types and validation
rules are documented via explicit registries (`WorkflowActionTypes`,
`WorkflowValidationRules` in `internal/tracker/workflow_schema.go`). Tests
hold the switch statements that handle them to those lists
(`internal/tracker/rule_registry_test.go`).

`process changes` embeds `cmd/issue-cli/CHANGELOG.md` at build time via
`//go:embed` and prints it newest-first, capped to the 20 most recent `## v`
entries. The CHANGELOG lives under `cmd/issue-cli/` (not the project root)
because Go's `//go:embed` cannot reference files outside the embedding
package's directory. Shipped changes collect under `## Unreleased` at the
top. To cut a release (once per milestone, see docs/roadmap.md), rename that
heading to `## vX.Y.Z — YYYY-MM-DD` and push main to origin; the release-marker
Action tags, writes the GitHub Release and builds the binaries. A push with
`## Unreleased` on top releases nothing.

## Design Considerations

When working on CLI changes:

- Output is consumed by agents, not humans — prioritize machine comprehension over aesthetics
- Document the output contract early: whether text is human-facing or agent-facing
- Consider whether `--json` or other machine-readable output is needed
- Script compatibility matters — avoid breaking existing agent prompts that parse CLI output
- After making changes, run `make install` to update the binary
