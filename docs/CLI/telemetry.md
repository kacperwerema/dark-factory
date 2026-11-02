---
title: "CLI Usage Telemetry"
order: 2
---

`issue-cli` records one small JSON line per invocation to a **local** file. The log shows how bots actually use the CLI, and which commands, flags and help topics nobody uses, so they can be removed or merged. Separately, [sharing usage](#sharing-usage) sends a daily, anonymous, names-only summary of these lines (never the lines themselves). It is on by default, you can turn it off, and nothing is sent while no collector URL is set.

Telemetry is separate from the [clilog](#the-clilog), the log of every call with its arguments (free text removed) that feeds the issue page's Timeline tab (its CLI calls filter) and the repeated-failure hint. Telemetry stores **names only**.

## Where events go

| Situation | File |
|:--|:--|
| A project resolves (`--project`, single-project config, or `./issues` bootstrap) | `<workdir>/.agent-logs/telemetry.jsonl`. Without a `workdir`, the parent of the project's issues directory is used. |
| No project resolves (bad `--project`, ambiguous multi-project config, missing config) | `${XDG_STATE_HOME:-~/.local/state}/issue-cli/telemetry.jsonl` |
| A read-only call on a project you aren't working in (see [Read-only calls on another project](#read-only-calls-on-another-project)) | your own project's file, or the global one |

`issue-cli telemetry path [--global]` prints the active file and whether recording is enabled.

**Rotation.** Before a write, a file of 10 MB or more is renamed to `telemetry.jsonl.1`, replacing any older `.1`, and a fresh file is started. Readers read `.1` and then the current file, so at most about 20 MB is kept per location.

**Concurrency.** Stat, rotate and append run under an exclusive `flock` on `telemetry.jsonl.lock`, so concurrent agents can't double-rotate. Each event is a single `O_APPEND` write capped at 4 KB, so lines never interleave. If the lock isn't acquired within 500 ms, the event is dropped.

**Failure policy.** Telemetry never changes CLI behaviour. A failed write, an unwritable directory, a lock timeout or a panic in event building is swallowed. Stdout, stderr and the exit code are identical with telemetry on or off, except for the one-time [sharing notice](#sharing-usage) on the first run's stderr.

## Opting out

- `ISSUE_CLI_TELEMETRY=off` (also `0`, `false`, `no`) disables recording for that shell.
- `telemetry: false` on a project in `projects.yaml` disables recording for that project. Leaving it out, or setting `telemetry: true`, means enabled.

```yaml
projects:
  - name: "Issue Viewer"
    slug: "issue-viewer"
    workdir: "/home/me/Work/issue-viewer"
    telemetry: true   # default; false opts this project out
```

Opting out doesn't affect the Timeline tab or the retry hint, which use the separate [clilog](#the-clilog).

`ISSUE_CLI_TELEMETRY_SKIP=1` is an internal variable. It skips recording only the current call and doesn't count as an opt-out. The viewer sets it on its own `telemetry report` call so `/stats` page loads aren't counted as CLI usage.

Telemetry files live under `.agent-logs/`. Make sure each project repo gitignores that directory, or the file shows up as untracked.

## Sharing usage

Sharing is **on by default** (opt-out, the owner's decision D-7 on factory/usage-telemetry-from-other-peoples-installs-opt-in-anonymous-and-a-view-for-the-owner). Each install sends one small, anonymous summary per day to a collector, so the engine's makers can see which commands, flags and features people use and where they fail.

- **Nothing is sent while no collector URL is set.** The URL comes from `ISSUE_CLI_TELEMETRY_URL=<url>`, from `"url"` in the settings file, or from the release's built-in default. Today the engine ships **no** default (`share.DefaultURL` is empty), so nothing leaves any machine until the owner deploys a collector and sets it in a release.
- **The first run says so once.** The first `issue-cli` call on an install prints one line on stderr:
  `issue-cli: anonymous usage stats are on (command and flag names and counts only, once a day; no collector is set yet, so nothing is sent). What is sent: docs/CLI/telemetry.md. Turn off: issue-cli telemetry share off, or ISSUE_CLI_TELEMETRY=off.`
  It then writes the settings file, with a random install id and the date the notice was shown, so the line never comes again.
- **Turning it off:** `issue-cli telemetry share off` (kept in the settings file, and the install id is forgotten), or `ISSUE_CLI_TELEMETRY=off`, which stops recording too and also suppresses the notice. A project's `telemetry: false` keeps that project's calls out of the summary.

### Commands

| Command | Does |
|:--|:--|
| `issue-cli telemetry share on\|off\|status` | Turn sharing on or off. `off` forgets the install id, so turning it on again counts as a new, unlinked install. `status` says whether anything is sent and why not. |
| `issue-cli telemetry send [--quiet]` | Sends pending days now: the full days of the last week that have calls and haven't been sent yet. It tries 3 times (backoff 1 s, 4 s, 16 s, with jitter; 10 s timeout) and doesn't retry a refusal (4xx other than 429). A day that fails stays pending. |
| `issue-cli telemetry sent [--full]` | The sent log: when, which day, where, and the collector's answer. `--full` adds each body. |

`telemetry path` and `telemetry report` end with a `share:` line that gives the current state and how to turn it off. No call ever stops to ask.

There is no preview step (D-9). The sent log keeps every body exactly as it was sent.

**The daily send.** While sharing is on and a URL is set, any `issue-cli` call checks a stamp file (one `stat`). When the stamp is a day old, the call takes it under a lock and starts `issue-cli telemetry send --quiet` as a detached child with `ISSUE_CLI_TELEMETRY_SKIP=1`. The call's own output, exit code and timing don't change. Only an installed `issue-cli` binary does this, never a test binary.

### Files

| File | Holds |
|:--|:--|
| `${XDG_CONFIG_HOME:-~/.config}/ai-workflow-engine/telemetry-share.json` | `share`, `install_id`, `notice_shown`, `decided` (the day of the last `share on`/`off`), and optionally `url`. One per install (per user account), not per project. With no file, sharing is on and the notice hasn't been shown yet. |
| `${XDG_STATE_HOME:-~/.local/state}/issue-cli/telemetry-sent.jsonl` | Every send attempt with the body that was sent. |
| `${XDG_STATE_HOME:-~/.local/state}/issue-cli/telemetry-send.stamp` | When the last daily send started. |

### Exactly what is sent (batch v1)

One JSON body per UTC day, `POST <url>/v1/batch`:

```json
{"v":1,"install":"3f9c…(32 hex)","day":"2026-10-30","ver":"v0.55.0","os":"darwin","arch":"arm64",
 "commands":[{"cmd":"transition","calls":12,"fails":3,"p50_ms":40,
              "flags":{"to":12,"dry-run":2},"errors":{"validation":3}}],
 "templates":["development"],"features":["local-ci","yolo"],
 "counts":{"projects":2,"issues":140,"departments":3}}
```

| Field | What it is |
|:--|:--|
| `install` | A random id made on the first run (or on `share on` after an off). It isn't derived from the machine, the user or a path. |
| `day` | The UTC day the counts cover. Today is never sent, because it isn't over yet. |
| `ver`, `os`, `arch` | The issue-cli build (`v0.55.0`, `v0.55.0-rc.1-3-gabc1234`, `dev`; anything else is sent as `other`) and Go's platform names (an unknown one is sent as `other`). |
| `commands` | One row per command or command + subcommand that was called that day: calls, failures, median duration, how often each **flag name** was used, and failures by error class. |
| `templates` | The bundled workflow templates the configured projects were stamped from. `custom` stands for any other workflow. |
| `features` | The workflow-feature catalog ids found in the projects' workflow files. |
| `counts` | The number of configured projects, issue files and departments. |

**Never sent:** project names or slugs, issue slugs, titles or text, assignees, agent names, paths, repo names, emails, flag **values** (not even `--to`), the mistyped token of an unknown command, process ids, the args hash, or anything else from the local event that isn't listed above.

**Why free text can't get in.** A batch is built from a closed vocabulary, never by copying events:
- Command, subcommand and flag names come from issue-cli's own manifest (the same introspection as `telemetry report`, see [Adding a command](#adding-a-command)).
- Error classes come from the list [above](#error-classes).
- Template names come from the bundled templates, and feature ids from the catalog.

A name outside the vocabulary is dropped. `share.Validate` checks the finished batch before it is sent.

The collector runs `share.ValidateShape`:
- Every string is a lowercase token `[a-z0-9-]`, at most 40 characters (the version and the id have their own patterns).
- Error classes are from the list.
- Numbers are in range and lists are capped.
- The body is at most 64 KB, and unknown fields are refused.

A space, slash, dot, `@`, capital letter or long string is refused at both ends. `TestBatchSchemaEveryStringFieldHasARule` fails when someone adds a string field without a rule.

### The collector

`cmd/telemetry-collector` is a small receiver you can host yourself. It uses only the standard library:

```bash
go run ./cmd/telemetry-collector -data ./telemetry-data [-listen 127.0.0.1:8787]
```

- `POST /v1/batch` validates the body and appends it to `<data>/batches.jsonl` under a lock. It answers 204, 400 with the reason, or 413.
- `GET /healthz` answers `ok`.

When the same install sends the same day again, the newer batch replaces the older one when the file is read. The collector listens on loopback by default. Putting it in front of other machines (a proxy, TLS, a domain) and choosing the URL a release sends to is the operator's step; the engine doesn't do it.

**Off-the-shelf alternatives.** These were compared on paper only; nothing was set up.
- Self-hosted PostHog is full product analytics, but it needs ClickHouse, Kafka, Redis and Postgres. That is far more than a few KB a day.
- Plausible CE and Umami are light, but they are built for web page views. Per-command aggregates fit them poorly.

The batch is plain JSON, so an adapter that forwards it to PostHog's capture API can be added later without changing the client.

### The owner's view

The viewer's `/telemetry` page reads the collector's store. Set `telemetry_data:` at the top of `projects.yaml` (beside `viewer_url:`) to the collector's `-data` folder or its `batches.jsonl`. A relative path is resolved against the config's folder. Restart the viewer after setting it. The page shows:
- installs per day
- the versions in use
- the most and least used commands
- the features that are on
- the workflows in use
- platforms
- the error rate per command

It has a 7 / 30 / 90 day / All range. Without the key, or with an empty store, the page says what's missing and which command fills it. The page only reads; it sends nothing.

Key files: `internal/telemetry/share/` (`batch.go` schema, vocabulary, Build and Validate; `settings.go` the on/off setting, first run, URL, sent log; `send.go` retry; `collector.go` handler and store; `summary.go` the page's aggregates), `cmd/issue-cli/telemetry_share.go`, `cmd/telemetry-collector/`, `telemetry_view.go`, `templates/telemetry_wb.html`.

## The clilog

Every `issue-cli` call also writes one line to the clilog, the log the issue page's Timeline tab, the agents canvas, `report-bug` and the repeated-failure hint read. Since v0.54 the line is written **when the call ends**, so it carries the result:

```json
{"ts":"2026-10-23T08:37:12Z","pid":1437994,"ppid":1437795,
 "args":["--project","sb","transition","cli/demo","--to","in progress"],
 "exit":1,"err_class":"approval_missing","dur_ms":4}
```

- `ts` is when the call **started** (RFC3339, seconds); `dur_ms` is how long it ran, so it ended at `ts + dur_ms`. A long call (`ci run`) shows up only when it ends, so lines are not always in `ts` order.
- `exit` is the process exit code (2 for a panic), `err_class` the same class telemetry uses (`approval_missing`, `validation`, `invalid_transition`, `usage`, …), left out on success.
- A call killed outright (SIGKILL, power loss) leaves no line.
- Lines written before v0.54 have no `exit`, `err_class` or `dur_ms`; readers treat them as calls of unknown result.

**No free text.** `args` keep the command and subcommand, flag names, the slug, box ids, statuses, and the values of structural flags (`--to`, `--section`, `--status`, `--how`, `--key`, `--type`, `--priority`, `--system`, `--epic`, `--since`, `--project`, `--config`, `--body-file`, …). Everything else is replaced by `‹text n=<characters> h=<6 hex of its SHA-256>›`: the values of `--text`, `--body`, `--quote`, `--title`, `--value` and any flag not on the structural list, the value part of `--field key=value`, and positionals with spaces or over 100 characters. The text is already in the issue; the hash lets identical calls compare equal and lets the timeline match a `comment` call to its comment. The rule lives in `internal/clilog/redact.go` (`Redact`); a test keeps its list of value-less flags in step with `cmd/issue-cli`.

**Where it goes.** One record is appended, under the same lock and 10 MB rotation as telemetry, to each of:

| File | Holds |
|:--|:--|
| `<workdir>/.agent-logs/clilog.jsonl` (`${XDG_STATE_HOME:-~/.local/state}/issue-cli/clilog.jsonl` when no project resolves) | every call in the project, by agents, the lead and you |
| `$ISSUE_CLI_LOG`, when set | the dispatched agent's own log (see [Agent Dispatch](../agent-dispatch.md#environment-variables)) |
| `<workdir>/.agent-logs/<assignee>/<assignee>.clilog` | calls naming an issue with an assignee; the path is resolved before the call, so the transition that clears the assignee still lands here, and after it for `start`, which sets one |

A path listed twice is written once. A read-only call on another project skips the first and third rows there, see below. All of them live in the project's checkout, so removing a worktree loses none of them, and `.agent-logs/` is gitignored. Before v0.54 the shared copy was `$TMPDIR/issue-cli-logs/actions.jsonl`, lost on reboot; nothing writes it now, and the Timeline tab still reads it while it exists.

### Read-only calls on another project

A read-only call (`show`, `list`, `checklist`, `connections`, `workflow upgrade`, `transition --dry-run`, …) from someone who isn't working in the project leaves nothing in it: no clilog line, no assignee clilog line, no telemetry event. "Working in" means your working directory is under the project's root (its checkout and `.worktrees/`) or its issues folder. The record goes instead to your own project, the configured project whose root holds your working directory, or to the global file under `${XDG_STATE_HOME:-~/.local/state}/issue-cli/` when there is none. `$ISSUE_CLI_LOG` is still written: it is the caller's own log. A project's `telemetry: false` still holds for calls on it. A writing call on another project logs there as before, since it changes that project anyway.

Which calls are read-only is the `commandAccess` table in `cmd/issue-cli/readonly.go`, next to the registry: each command is `reads`, `writes`, or read-only for some subcommands or flags (`readSubs`, `readWithFlag`). When in doubt a command is marked `writes`.

**One-off scrub.** `go run ./tools/clilog-scrub -root <primary checkout> [-dry-run]` brings older logs in line: it merges every worktree's `.agent-logs/<agent>/` into the project's (clilogs unioned and deduplicated, `dispatch-prompt.txt` and `messages/` copied; test sandboxes left alone), rewrites every clilog without free text, and removes the `$TMPDIR` log. Originals are copied to `.agent-logs/.scrub-backup-<time>/` first; delete that once the timeline looks right. A second run changes nothing.

## Event schema (v1)

```json
{"v":1,"ts":"2026-10-02T18:51:06.423Z","dur_ms":4,
 "cmd":"transition","sub":"","alias":"",
 "flags":["config","project","to"],
 "project":"issue-viewer","issue":"cli/foo","assignee":"agent-foo",
 "safe":{"to":"testing"},
 "exit":1,"err_class":"invalid_transition","unknown":"",
 "caller":"agent","agent":"claude-code","tmux":true,
 "pid":3639775,"ppid":3639693,"args_hash":"6d0807cacc5a8c5c"}
```

| Field | Meaning |
|:--|:--|
| `v` | Schema version. Readers skip lines with a higher `v` than they understand. |
| `ts`, `dur_ms` | Start time (RFC 3339, UTC) and wall-clock duration. |
| `cmd` | Canonical command name. Empty for an unknown command. |
| `sub` | Known subcommand (`data add`, `telemetry report`, `workflow init`) or help/process topic (`help transitions` → `transitions`; `help transition` → `transition`, the command). Unknown values are never stored here. |
| `alias` | The alias typed, when it differs from `cmd` (e.g. `show` → `context`). |
| `flags` | Flag **names** used, global flags included, sorted. Never values. |
| `project`, `issue`, `assignee` | Resolved project slug, and the issue slug and assignee when the command resolved a real issue. For an unresolvable project, `project` is the raw `--project` token (≤ 64 chars). |
| `safe` | The only stored values: `to` (`--to`) and `section` (`--section`), ≤ 64 chars. |
| `exit` | Process exit code: 0, 1 or 2 (panic). Events recorded before v0.49.0 may also hold 3 (a `--wait` timeout). |
| `err_class` | See below. Empty on success. |
| `unknown` | The offending token for unknown command, subcommand, topic or flag errors, ≤ 64 chars. Only that one token is kept, never the rest of the args. |
| `caller` | `dispatched` (`ISSUE_VIEWER_ISSUE_SLUG` set by viewer dispatch), `agent` (`CLAUDECODE`, `AI_AGENT` or `CODEX_*` env), `tty` (interactive stdin) or `script`. |
| `agent`, `tmux` | Agent name from env (`claude-code`, `codex`, …), and whether `$TMUX` is set. |
| `pid`, `ppid`, `args_hash` | Process ids and a 16-hex-char SHA-256 prefix of the full raw args. `ppid` groups calls from one agent shell; `args_hash` spots identical repeats without storing values. |
| `ver` | The issue-cli build (`git describe` at `make install`: `v0.54.0-rc.1`, `v0.54.0-rc.1-3-gabc1234`, `dev` for a plain `go build`). The engine's Status page counts each project's calls on a release candidate from it. Added in v0.54.0 without a schema bump; older events have none. |

Titles, comment and append bodies, retrospective text, `set-meta` values, `--field` answers, descriptions and positional values never reach telemetry. `cmd/issue-cli/telemetry_test.go` asserts this with a sentinel string.

### Error classes

`unknown_command`, `unknown_subcommand`, `unknown_topic`, `unknown_flag` (including a removed flag such as `--wait`), `flag_parse`, `usage` (missing positional or required flag), `project_resolution`, `approval_missing`, `issue_not_found`, `invalid_transition` (target status not reachable), `validation` (a workflow rule, a missing field answer, or `--dry-run` reporting unmet requirements), `panic`, `other`. Events recorded before v0.49.0 may also hold `wait_timeout` (exit 3, from the removed `--wait`).

## `issue-cli telemetry report`

```
issue-cli --project <slug> telemetry report [--since 30d|12h|2026-09-01|all] [--global] [--top N]
issue-cli --project <slug> --json telemetry report ...
```

- `--since` sets the window (default `30d`). It accepts a duration with a `d`, `h` or `m` suffix, a date, or `all`.
- `--global` reads the no-project fallback file instead.
- `--top` caps the Errors, Unknown and Retry sequences sections (default 10).

The text output is agent-facing, with stable section headings:

| Section | Contents |
|:--|:--|
| `== Summary ==` | Source file, window, event and failure counts, callers and distinct assignees |
| `== Commands ==` | Calls, error % and median duration per `cmd` / `cmd sub` |
| `== Never used ==` | Commands, aliases, subcommands, topics, global flags and per-command flags with no calls in the window |
| `== Errors ==` | Top `err_class` × command |
| `== Unknown ==` | Top unknown command, subcommand, topic and flag tokens: what bots *expect* to exist |
| `== Retry sequences ==` | A failed call paired with the next call from the same `ppid` within 120 s, e.g. `transition (validation) ⇒ same args again` or `?add-comment (unknown_command) ⇒ comment --text → ok` |

### How "never used" knows every flag

Commands define their flags inside `Run`, so there's no static flag list. Every command parses through **`parseFlags`**, `parseFlagsArgs` or `parsePositionals` (in `context.go`). In introspection mode it panics with the fully defined FlagSet before any command logic runs. `buildManifest` uses this for every command and subcommand, with an empty temp issues dir, discarded output and empty stdin. Hand-parsed flags are declared on the command as `ExtraFlags`: transition's `--field`. The same introspection builds the **Flags** section of `<command> --help`. `TestIntrospectFlagsEveryCommandWithoutSideEffects` fails if a command can't be introspected or creates files.

### `--json` contract (report v1)

```json
{
  "v": 1, "source": "/…/.agent-logs/telemetry.jsonl", "enabled": true, "disabled": "",
  "since": "2026-09-02T18:51:06Z",
  "summary": {"events": 18, "failures": 6, "error_pct": 33.3, "first": "…", "last": "…",
              "callers": {"agent": 17, "dispatched": 1}, "assignees": 0},
  "commands": [{"command": "transition", "calls": 5, "failures": 4, "error_pct": 80, "p50_ms": 2}],
  "never_used": {"commands": [], "aliases": [], "subcommands": [], "topics": [],
                 "global_flags": [], "flags": {"list": ["sort"]}},
  "errors": [{"class": "validation", "command": "transition", "count": 2}],
  "unknown": [{"class": "unknown_command", "token": "add-comment", "count": 1}],
  "retry_sequences": [{"failed": "transition (validation)", "next": "same args again", "count": 2}]
}
```

Lists are always arrays, never `null`. `enabled: false` with a `disabled` reason means recording is opted out; previously recorded events are still reported. The viewer's `/stats` page renders this JSON (see [Workflow Stats](../workflow-stats.md)).

## Adding a command

New commands must call `parseFlags(ctx, fs, args)` (or `parseFlagsArgs` / `parsePositionals`, see [Adding a command](overview.md#adding-a-command)) instead of `fs.Parse`. Group commands declare `Subcommands` (and `SubAliases`), and anything parsed by hand goes in `ExtraFlags`. Otherwise the flags and subcommands don't show up in events or in the never-used report.

Every command also needs an entry in `commandAccess` (`cmd/issue-cli/readonly.go`): `TestEveryCommandHasAccess` fails without one. A command with a read mode also needs a sample call in `readSamples`; `TestReadCommandsLeaveAnotherProjectUnchanged` runs each one on a sandbox git repo from another project's folder and fails if the repo's git status changes.

## Key files

- `internal/telemetry/event.go`: `Event`, `Safe`, error and caller constants, `HashArgs`, `Truncate`
- `internal/telemetry/store.go`: `FileSink` (lock, rotate, append), `ReadFiles`, path and opt-out helpers. `Sink` is the extension point for a future event-hub sink.
- `internal/telemetry/report.go`: `Manifest`, `Aggregate`, `Report`, `RenderText`
- `cmd/issue-cli/telemetry.go`: event building, `classifyError`, `detectCaller`, flag introspection, `buildManifest`
- `cmd/issue-cli/cmd_telemetry.go`: `telemetry report` and `telemetry path`
- `cmd/issue-cli/telemetry_share.go`: `telemetry share|send|sent`, the batch's vocabulary and sources, the daily send (`maybeAutoSend`), the first-run notice (`shareNotice`)
- `internal/telemetry/share/`: the shared batch, its validation, the sender, the collector's handler and store, and the `/telemetry` summary
- `cmd/issue-cli/main.go`: `main` times `runTraced` and calls `recordTelemetry` after every invocation, including panics
- `cmd/issue-cli/readonly.go`: `commandAccess` (which calls are read-only) and `logProject` (where a call's clilog record and telemetry event go)
