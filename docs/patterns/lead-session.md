---
title: "The Lead Session"
order: 1
---

The **CEO** is what was called the project lead: the viewer and issue-cli say CEO, while the tmux name (`lead-<project>`), the `issue-cli lead` command (alias `issue-cli ceo`), the `/lead` pages (`/ceo` redirects there) and the `lead_terminal` / `lead_window_session` keys keep the old name.

A way to run several agents on one project without the human talking to each of them. The tool knows about **lead agents**: it spawns them, names them, routes workers to them and shows them on the agents canvas. Siege Cup, a game built this way, is the worked example at the end.

## The roles

| Role | tmux name | Runs in | Does |
|---|---|---|---|
| **The human** | (you) | | Decides. Raises ideas, answers questions, approves gates, playtests. |
| **Project lead** | `lead-<project>` | the project checkout | Runs the project with you: grooms ideas, dispatches the workers you name, checks on them, relays their questions, reviews designs and diffs, ticks the gates you approved in its chat, fixes the process from the retros. One per project. |
| **Department lead** | `lead-<project>-<dept>` | the project checkout | One per department in `projects.yaml` `departments:`, reporting to the CEO. Runs its department's issues the way the project lead runs the rest, and only those. The human starts it. See [Department leads](../departments.md#department-leads). |
| **Workers** | `agent-<slug>` | `.worktrees/<slug>` | Own one issue each. Walk it step by step on auto mode, leave a trail of comments, stop at every human gate and when it is done. Never tick a gate, never pick up another issue. |

```
human
  └─ project lead            lead-<project>      project checkout
       ├─ worker             agent-<slug>        .worktrees/<slug>, branched from main
       ├─ worker             agent-<child-slug>  epic children too
       └─ department lead    lead-<project>-<dept>   project checkout (one per department)
            └─ worker        agent-<slug>        its department's issues
```

There is one project lead (the CEO) per project, and it leads the epics itself (workbench epic D-13). Each department adds one lead of its own under the CEO. You talk to the lead, and the lead talks to the workers. You can still attach to any session's tmux. The board stays the source of truth for status and approvals.

A human starts the work: workers start from ▶ on the board or from the lead's `issue-cli dispatch --quote` with your words naming them, and nothing runs on a timer. See [A human starts the work](../agent-dispatch.md#a-human-starts-the-work), the disclaimer this pattern runs under.

![Who does what: the human, the lead session, the board, worker agents in their worktrees, issue-cli and workflow.yaml; finished work merges into local main, local CI re-tests it, and the lead pushes once per milestone release](../images/lead-and-workers.png)

The name is the identity, the same rule as `agent-<slug>` for workers. Dots and spaces in the project slug become `-`. The lead's session also exports `ISSUE_VIEWER_LEAD_SCOPE=project`, so `issue-cli lead whoami` and the refusals below know who is asking.

## Spawning the lead

Start it with `issue-cli lead spawn`, or **Start CEO** on the company page (`/p/<project>/company`; the classic pages also have **Spawn lead**). The CLI and the buttons start the same session; the CLI doesn't need the viewer running. On the board the lead is the project's CEO: you talk to it in the CEO chat (`/p/<project>/lead`), which shows its whole messages, its questions as cards you answer on the page, and the workers running beside it.

- `lead spawn` starts `lead-<project>` in the project checkout, or a `lead-<project>` window in the shared session when `tmux_session` is set. What opens follows [`lead_terminal:`](../agent-dispatch.md#lead-terminal): headless when unset, a tab in your own tmux session with `tmux-window`, or a command. The CLI and the buttons behave the same. `--headless` opens nothing either way. Workers follow `terminal:` instead. `--agent codex` runs Codex instead of Claude.
- A lead that is already live is re-attached and its name printed. A second lead is never started.
- A worker can't spawn the lead: it is told to post a `question:` comment, which its lead reads.

### What is an epic

An epic is an issue that other issues name as their parent, or that has `type: epic`. A child links to it with `parent: <epic-slug>`. The older `umbrella: <slug>` is read as an alias; `show` and `start` print a hint to rename it:

```bash
issue-cli set-meta <child-slug> --key parent --value <epic-slug>
```

### Which type to file

When the lead files an issue (`issue-cli create --type <t>`, or `issue-cli set-type <slug> <t>` while it is at idea), it picks the type from what the work makes ([Types of Work](../Workflow/types.md)):

- `report`: research or a report only (a benchmark pack, an upgrade plan, an audit). The report goes on the outputs shelf with the issue and the owner reviews it; no code, no branch to ship, no commit. Code fixes it finds are filed as their own issues.
- `docs`: docs, README or screenshots in the repo, no code.
- `bugfix`: something that worked is broken. `tweak`: a small change the owner already decided. `epic`: tracking only.
- `feature` otherwise.

This repo's `leads.project_addendum` says the same in two lines.

### Brief

The lead starts with a built-in brief, so every project has a lead without editing anything. What a project adds goes in `leads.project_addendum` of `workflow.yaml`, after the built-in brief:

```yaml
leads:
  project_addendum: |
    - In this repo: designs live in docs/design/, and you review every diff.
```

Placeholders: `{{project}}` and `{{lead}}`. `issue-cli` commands in the brief get `--project` injected, as in dispatch prompts. The CLI appends a fixed footer: the lead's role, what it may tick, its handoff note, and `issue-cli lead whoami`. An old `leads.project` still replaces the built-in brief, and lint warns: a replaced brief misses every later change to it. An old `leads.epic` key still loads, does nothing, and `issue-cli process workflow` warns about it.

## Routing: who is my lead

Every worker reports to the project lead, epic children included. Whether the lead is live comes from the tmux sessions; nothing is stored.

The worker's dispatch prompt names its lead and its epic under `## Your lead`. `issue-cli show` and `start` print the issue's lead, for example `Lead: lead-<project> (live)`.

## Seeing and reaching leads

| Command | Shows or does |
|---|---|
| `issue-cli lead whoami` | Your role (project lead, worker or unmanaged), the branch you guard, your lead and whether it is live, how to reach it, and what you may tick |
| `issue-cli lead list` | The project lead, live or not, then the open epics with their children running of total |
| `issue-cli lead message <target> "text"` | Pastes the text into a Codex session, signed `[from lead-x]`. For a Claude session it refuses and prints the `SendMessage` call to make. Target: `project`, a `lead-`/`agent-` name, or an issue slug (its worker) |
| `/p/<project>/lead` | The CEO chat: the lead's conversation, its open questions, what waits on you and the live workers |
| `/p/<project>/agents` | The Crew page: the CEO, a column per department with its lead and workers, each agent's state and the stopped jobs. Pick an agent for its panel: Open in a terminal, the attach (switch with `terminal: none`) command, its gate's approve link at the station, its branch. The classic canvas is at `?classic=1` |

Agents can also message each other with Claude Code's built-in messaging between sessions (`ListAgents` / `SendMessage`): Claude agents are launched with `-n <tmux name>`, so `lead-<project>` and `agent-<slug>` are their addresses, and the lead may message a worker this way. Workers don't message the lead or a peer unless the human, in the worker's own chat, gives permission or asks them to (owner, 2026-10-24: "they can only talk to lead after my permission or when i explicitly ask them"); they ask the human in their chat or post `question:` comments. Nothing in the tool gates or records these messages. `lead message` is the tmux-paste fallback for Codex only: `issue-cli` never pastes into a Claude session. Workers can't use it; they post `question:` comments, which their lead reads from the issue trail.

The other way, a lead adds scope after dispatch as a `lead:` comment (`issue-cli comment <slug> "lead: …"`). Such a comment reaches the worker at its next transition: `issue-cli transition` (and its `--dry-run`) prints every `lead:` comment posted since the issue's last move, first, under `== From the lead since your last move ==`, and `--json` carries them as `lead_comments`. With no move recorded yet it prints every one. A real transition then warns on each that has no reply after it; nothing refuses. A comment the worker must act on before its next step still needs a message.

A lead still reads everything without interrupting anyone:

| Channel | Command | Shows |
|---|---|---|
| Issue | `issue-cli context <slug>` | Status, checklists, comments |
| Branch | `git worktree list`, `git log --oneline main..<branch>` | Code written so far, overlaps between branches |
| Logs | `.agent-logs/<session>/` | Every `issue-cli` call, the full transcript |
| Screen | `tmux capture-pane -p -e -t <session> \| tail -40` | What the agent is doing or waiting on |

Read the screen with `-e`. In an idle Claude Code pane the input box can show a dimmed suggestion (`❯ ` then text in `ESC[2m`). A plain `capture-pane -p` prints it like typed text. It is Claude Code's guess at your next message, never an answer the owner left unsent: on 2026-11-13 a lead read three of them that way. The owner's answers are comments on the issue trail; a worker reads them on its next `issue-cli` call, and a fresh session in its briefing (`## The owner's words on this issue`). The Crew page's **Tail** marks a suggestion as `(greyed suggestion, not typed: …)`.

A lead started by hand (plain `claude` in the project root) still works, but the tool can't see it: `lead whoami` reports it as unmanaged and tells it to run `issue-cli lead spawn`.

## Authority: who may tick a gate

Approvals are the `human_approval` field in the issue's frontmatter. The human sets it with the board's checkbox. A lead records an approval you gave in chat with:

```bash
issue-cli approve <slug> --to <status> --quote "<the human's words>" [--how "on a call"]
```

It writes an `approval: "<words>" (<how>, <date>) → <status>` comment, then sets `human_approval`. It never toggles an existing approval off. When you approve in the viewer with words in the box under the approve buttons, the viewer writes the same comment itself, so there is nothing for the lead to record.

| Who | May tick |
|---|---|
| Project lead | Any gate you approved in its chat (`--how` defaults to `in the lead chat`). |
| Worker | Never. |
| Any agent | Never Human Testing; you approve that in the viewer. |

These checks read the environment and the session name. They are guardrails for agents, not security.

## Running it

### Check-ins and stand-ups

While workers run, the lead checks in when you ask and on its own tick ([The tick](#the-tick); the tool itself runs nothing on a timer, see [A human starts the work](../agent-dispatch.md#a-human-starts-the-work)) and reports only what changed: questions waiting, a gate reached, red tests, an idle agent, two branches touching the same files. On request it gives a **stand-up**: one line per dispatched issue with status, last progress and blockers, then the approvals waiting for you.

### The tick

The project runs while the lead keeps its tick going: nothing in the tool runs on a timer, so the queue, auto-start and due jobs move only when the lead checks in. That check-in is one command:

```bash
issue-cli lead tick                                  # one tick
issue-cli lead tick --watch [--every 5m]             # tick every 5 minutes, for a Monitor
issue-cli lead tick --watch --exit-on-news           # exit at the first news, for a background shell
issue-cli lead tick --watch --exit-on-news --verbose # exit on a start or a close too
```

Each tick does what the lead used to script by hand:

- runs `queue next --start`: a due job on its standing start first, then the first startable queue item, under auto-start's machine-wide lock and every hold it keeps (mem-guard, the load, the worker cap, the start gap, local CI red, the owner's words);
- looks for issues at their last status whose `work/<slug>` branch has commits not on main (one `git for-each-ref --no-merged` call);
- closes finished worker sessions as `agents sweep` does, except those whose branch is not on main yet: the lead lands it first; the idle session of a report waiting on the owner's review with its output on the shelf closes too, with a held `closed:` line and a `session: closed` comment on the issue ([A report waiting on your review](../agent-dispatch.md#a-report-waiting-on-your-review));
- on a memory hold, closes the idle worker sessions the hold names (`could free:`: the issue is done, landed or not, or waits on the owner's gate or an open `question:`; no issue-cli call for 15 minutes, not in a turn, not paused by mem-guard), says `freed under the memory hold: closed agent-x (done), …` in one line and runs `queue next --start` once more;
- reads local CI's state, the criteria waiting on the lead (`prove --lead`), stalled agents and takeovers;
- once a day (its first tick of the local day), runs `issue-cli doctor leaks` and says what the factory left running in one `leaks:` line (`leaks: 3 things left running (2 chrome, 1 agent-home); see issue-cli doctor leaks, stop with --stop`); it stops nothing ([Troubleshooting](../getting-started.md#troubleshooting));
- once a day, on the engine's own repository, when the running issue-cli is more than 10 commits behind main, says so in one `note:` line with up to 5 shipped fixes it lacks (non-merge `fix:`/`fix(…)` commits touching `cmd/issue-cli/` or `internal/` outside `internal/viewer/`) and the install command (`note: the installed issue-cli (v0.62.0-rc.1-174-g27afdabb) is 37 commits behind main; 6 shipped fixes wait on an install: fix(lead): …; …; install: cd ~/Repo/ai-workflow-engine && GOBIN=~/.local/bin make install`); it runs nothing: installing is the lead's or the owner's call;
- counts the queue's open items and reads the owner's comments since the last tick.

It prints a line only when the lead has to act, each with its prefix: `start:` (it started something), `land:` (a done issue's branch to land), `merge:` (an issue at its merge status, shipping, whose branch has waited unmerged for `leads.green_branch_minutes`, default 30: said once an hour while it waits, with the `git -C <main checkout> merge …` command that lands it; the merge stays the lead's), `ci:` (main red, or CI can't run), `hold:` (a start held for a reason not said before), `queue:` (5 open items or fewer, or none: think up more), `idle:` (nothing started for an hour with room), `report:` (a worker's report and its follow-ups), `owner:` (the owner's comment), `⚠` (waits on you, a stalled agent, a takeover), `leaks:` (the daily doctor leaks found something), `tick error:` (a step that failed). A thing is said once and said again only after it went away and came back; a hold's numbers don't count (`memory 62% ≥ 60%` and `63%` are one hold), and the gap after a start and an empty queue's hold say nothing by themselves. Under `--watch` a hold that clears by itself says nothing: memory or the load over its line, the worker cap, the start gap, another project's turn, a resting subscription, another lead's start in progress. The lead has nothing to do for those. Once starts have been held for an hour with no start in between, the watch says it once: `hold: nothing started for 1h (since 09:00), now: <the hold>`. When the project had room instead (the queue empty, everything held, auto-start off, idle sessions on the worker slots), it says `idle: nothing started for 1h (since 09:00) with room to start (the factory runs 24/7): <why>: <what to do>`, by hand or under `--watch`, after `leads.idle_minutes` (default 60) and again each such stretch it stays so ([The factory runs 24/7](../agent-dispatch.md#the-factory-runs-247)). A hold the lead can act on is said at once, as before: local CI red, the queue empty or at its refill line, auto-start off or paused, the queue paused, nothing ready to start. The hold said last stays said through ticks where one that clears by itself wins quietly: when it comes back with the same reason and first item it is no news, and `--exit-on-news` prints it once above the next news (a different reason or first item is news once; a start forgets it). A tick without `--watch` still says every new hold, so a check-in by hand says why nothing started. A quiet tick prints nothing. What it said is kept in `$XDG_STATE_HOME/ai-workflow-engine/lead-tick/<project>.json`.

Every line a Monitor prints wakes the lead for a whole turn, and a Monitor expires every 30 minutes, which wakes it too. Measured on 2026-11-15 before this command: 125 wakes in an 11-hour night with no owner, 105 of them a status line that said nothing new and 20 Monitor expiries. So the lead runs `--watch --exit-on-news` in a background shell (Claude Code's Bash `run_in_background`): the shell exits at the first tick with news the lead must act on ([What counts as news](#what-counts-as-news)), which wakes the lead once; the lead acts and starts it again. Workers can't run it.

#### What counts as news

With `--exit-on-news` the watch exits only on a line the lead must act on:

- `ci:` local CI red on main, or CI that can't run;
- `land:` an issue at its last status whose branch is not on main;
- `merge:` an issue at its merge status (shipping) whose branch has waited unmerged for 30 minutes (`leads.green_branch_minutes`), again each hour it still waits;
- `queue:` the queue at its refill point (5 open items or fewer, or none; with none it adds `look first at:` and the goals and epics nothing serves, [Work behind a goal](../departments.md#work-behind-a-goal)). With YOLO and auto-start both on (not paused), the tick also files a retro sweep and queues it itself, with no quote, so it starts on auto-start's words: `queued sweep product/queue-sweep-<date> as item N (YOLO and auto-start on; …)`. The run uses `builtin:retro-sweep`, as a run of the project's own job on that brief when it has one, else of the implicit `product/queue-sweep`, typed report when the workflow has that type. It queues one at the refill line a day, and while the queue stays empty another once the last one closed, up to 4 a day; none while an earlier one is still open (then the line says `sweep <slug> is still open`). The sweep files nothing: you file its proposals when its `report:` lands;
- `report:` a `report:` comment on an open run of that sweep's job, once per comment (`report: product/queue-sweep-<date>: o-…, 6 proposals: file its proposals …`): file them before the queue runs dry. On 2026-11-18 a report landed at 02:01 with no line, the queue ran dry at 02:35 and the next start was 07:30. Any other worker's `report:` comment on an open issue in your scope is said once too (`report: <slug>'s report has N follow-ups to file: …`), and a report output in review on the shelf with no `report:` after it;
- `question:` an open issue whose newest comment is a worker's `question:` (not the owner's or a lead's), once per comment: answer it or relay it to the owner, since the worker holds a slot until then;
- `⚠` a stalled worker, a takeover, a criterion that waits on the lead; `start:` a start that failed (an error, not a hold); `freed under the memory hold: … not closed …` a session that wouldn't close;
- `hold:` a hold the lead can act on, or starts held for 1h or more by the machine or another project;
- `idle:` nothing started for `leads.idle_minutes` (default 60) while the project had room (the queue empty, everything held, auto-start off, idle sessions on the slots), again each such stretch ([The factory runs 24/7](../agent-dispatch.md#the-factory-runs-247));
- `leaks:` a `doctor leaks` finding;
- `lead context <n>k:` the context-rotate nudge;
- `owner:` the owner's comment, `worktree:` a done issue's tree that stays dirty, `tick error:` a step that failed.

A plain start (`start: ✓ Started queue item 7: …`, a due job's `✓ Dispatched`), sessions closed under a memory hold or the worker cap (`freed under the memory hold: closed …`, `freed under the worker cap: closed …`), the daily `worktrees: removed …`, a YOLO move of an issue whose worker has gone (`moved: <slug> human-testing → done …`, [The factory runs 24/7](../agent-dispatch.md#the-factory-runs-247)) and the daily install-lag `note:` are no news: the lead has nothing to do about them. The watch holds them and prints them, one line each in the order they happened, above the next news when it exits. In one evening of 2026-11-15 plain starts woke the DF lead's whole context four times for nothing. `--verbose` exits on them as before.

Under `--observe` the same split holds: `job:`, `comment:`, `shelf:`, `shelf comment:`, `leaks:` and the context nudge are news; a `status:` move is news only when someone must act on it, said in brackets after it: the issue is ready for a gate's approval and waits on nothing else (`(waits on the in progress gate)`: the owner's, or the lead's to pass under YOLO), it is at the owner's own testing (`(the owner's to test)`), or it was parked (`(parked)`). Any other status move and `ended:` (a worker session gone) are held for the next news.

Each tick also reads the lead's own Claude Code transcript (the newest one named `lead-<project>`, or the department lead's under `--observe`): when its last turn read more than 300k tokens of context, it says once a local day `lead context 412k: rotate (lead-<project>): issue-cli lead rotate (docs/patterns/lead-session.md#rotate)`. The lead runs it and ends its turn ([Rotate](#rotate)). A Codex lead has no transcript and gets no line. Every tick, `--observe` too, also re-renders the lead's [state file](#the-state-file).

### Waiting

A lead waits in exactly one way: a **background shell that exits on news**. Start it with Bash `run_in_background`, and start it again each time it exits:

```bash
issue-cli lead tick --watch --exit-on-news                           # the CEO
issue-cli lead tick --watch --exit-on-news --observe [--department <d>]   # a lead that doesn't start work
issue-cli lead watch --run '<probe>' [--every 10m] [--until-change | --until '<regex>']   # anything else
```

Never wait on a timer. A `/loop` cron, a `ScheduleWakeup` poll and a Monitor (which expires every 30 minutes) wake the whole session each time they fire, news or not, and every wake re-reads the lead's whole context. The owner's `/usage` for the 24 hours to 2026-11-15, weighted at Opus prices (input 1, cache read 0.1, cache write 2, output 5): lead sessions took 38% of all usage (another project's 28%, DF's 10%), workers 57%. Most of it was timed wakes of a session holding 0.5–1 M tokens:

| Loop | Runs a day | Tokens | Per run |
| --- | --- | --- | --- |
| A marketing department lead's "Marketing lead tick", every 20 min | 68 | 32.4 M | 476 k |
| "Overnight watchdog" | 45 | 23.7 M | 526 k |
| "Lead check-in (hourly queue tick)" | 12 | ≈11.5 M | 960 k |
| "D-5 image probe", every 30 min (a shell script; the LLM only read its output) | 23 | 10.4 M | ≈452 k |
| Dynamic "Check whether plan/…" wakeups | | | 2.2 M |

A shell that runs a probe costs no tokens; only the one wake when it exits does.

**A tick that is gone is said on the lead's next call.** A session that ends (a crash, a watchdog's resume) takes its background shells with it, and a resume, unlike a [rotate](#rotate), brings no `Restart these watches` list. Each `lead tick --watch` records itself in the lead's store (`lead/<lead>.json` beside the [state file](#the-state-file)), and when it exits on its own, when. After any issue-cli call by a lead (the tick's own runs, `lead watch`, `rotate`, `flush` and `spawn` aside, and never under `--json`), stderr says `lead tick: no background tick is running (…)` with the command to start again with Bash `run_in_background` (the last tick's own, else the role's), when: the recorded tick's process is gone without a word (said at once: killed, or the session that ran it ended), or a signal stopped the last tick (said at once, with the signal), or the last tick exited on news more than 10 minutes ago and none started since (`leadstate.TickGrace`), or no tick ever ran. issue-cli can't start the tick for the lead: only a background shell of the lead's own session wakes it.

**A tick a signal stops says so.** A background shell can be ended from outside (measured on 2026-11-18 and 11-20: a department lead's tick ended with `exit code 144` and no output, while its session was alive and no install was near). A `lead tick --watch` catches SIGTERM, SIGINT and SIGHUP: it prints the lines it held for the next news, then `lead tick: stopped by SIGTERM (pid N); start it again with Bash run_in_background:` and its command, and exits 128+n, so the lead's task notification carries them. Under `--exit-on-news` the tick also keeps what it holds in the lead's store on each tick; a tick killed without a word (SIGKILL) leaves them there, and the lead's next tick prints them above its own: `lead tick: the tick before this one (pid N) ended without a word at or after <time>; it held:`. Reinstalling issue-cli doesn't stop a running tick: `make install` moves the new binary over the old one, and the running process keeps its own.

**`--observe`** is the tick of a lead that doesn't start work, such as a department lead that dispatches only on the owner's words. It is read-only: it never runs `queue next --start`, never closes a session, runs no prune and no `doctor leaks`. It says, each once, for its scope (`--department <d>`, else the caller's own department when it is a department lead, else the whole project; a group covers its members): `job:` a due job not yet running, with its `job run` command; `status:` an issue that moved (`status: seo/blog in design → backlog`); `comment:` a new comment on one of its issues by anyone but itself; `shelf:` a new output and `shelf comment:` a new message on one; `ended:` a worker session of its issues that was live at the last tick and is gone. With `--exit-on-news` a plain status move and `ended:` wait for the next news ([What counts as news](#what-counts-as-news)). Its first tick takes the baseline and says only due jobs. It keeps its own state (`lead-tick/<project>.observe-<dept|all>.json`), apart from the CEO's tick.

**`lead watch`** runs a probe in the shell every `--every` (default 10m, at least 10s) with `/bin/sh -c` in the project root. It compares stdout and stderr together with the exit code (`exit 3` on a failure), so a probe that starts failing is a change too. `--until-change` (the default) exits at the first run whose output differs from the first run's; `--until '<regex>'` exits at the first run that matches, the first included. Quiet runs print nothing; on exit it prints one line and the output (40 lines at most):

```
watch: output changed after 40m (5 runs): ls out/d5 | wc -l
4
```

The D-5 image probe, which ran every 30 minutes as a cron that woke the lead to read a script's output, is one background shell:

```bash
issue-cli lead watch --every 10m --until 'ready|failed' \
  --run 'scripts/d5-image-status.sh'      # prints "pending", then "ready: 4 images" or "failed: …"
```

A probe whose output carries a clock (a timestamp, an age) changes every run: filter it out in the command, or use `--until`. Workers can't run `lead watch`: a worker never polls, it stops and says what it waits on.

### The state file

A lead's conversation grows all day, and every wake re-reads it: 476k to 960k tokens a wake on 2026-11-15. What the lead needs to act on fits in a few kilobytes, so it keeps that apart from the chat, in one file per lead:

```bash
issue-cli lead state                                   # render and print it
issue-cli lead state note "landed x; CI flaky on TestY" [--tomorrow]
issue-cli lead state ask --quote "<the owner's words>"   # an owner request, open until done
issue-cli lead state done <n>
```

The file is `$XDG_STATE_HOME/ai-workflow-engine/lead/<lead>.md` (`lead-<project>.md`, a department lead's `lead-<project>-<dept>.md`), 16 KB at most (about 4k tokens). It is rendered, never edited: the tick rewrites it on every run from the board and from the lead's own notes in `<lead>.json` beside it.

| Section | From |
| --- | --- |
| Workers | the live `agent-*` sessions in the lead's scope, each with its issue's status and its last `progress:` line |
| Queue | the open items with the owner's quote, how many are open and the refill line |
| Owner asks | `lead state ask --quote`, in the owner's words, open until `lead state done <n>`; then `issue-cli status` for the gates and questions |
| Today | the decision refs (`<issue>#D-n`) and lead memory ids recorded today, as links with a few words, never copies |
| Watches | each background shell the lead waits on: `lead tick --watch` and `lead watch` register their pid and command while they run |
| Tomorrow | `lead state note --tomorrow` |
| Notes | `lead state note`; a note from an earlier local day folds into that day's `sessions/<day>-<lead>.summary.md` under `## Lead notes` |

Each list is capped and ends with a `+N more:` line naming the command that shows the rest; past the cap the oldest notes go first. A note that looks like a secret is refused, since the file goes into the next session's first prompt. Workers are refused: their notes go in HANDOFF.md and on their issue.

### Rotate

When the tick says `lead context <n>k: rotate`, the lead runs `issue-cli lead rotate` and ends its turn. Rotate:

1. renders the state file;
2. writes the handoff note from it into `sessions/` with the flush's six sections: `<date>-<lead>.md`, or `<date>-<lead>.<HHMM>.md` when the day's note is there (the nightly flush's or an earlier rotate's), so it never overwrites one; the Status page and the next spawn take the newest by day and then by when it was written;
3. writes a digest of the old chat beside it, `….digest.md` ([History](#history));
4. starts a detached `issue-cli lead rotate --finish`, in its own session so it outlives the lead's. It waits until the pane is out of its turn (checked every 5 s, 30 minutes at most; then it leaves the lead running and says so in `lead/<lead>.rotate.log`), closes the session and spawns the same lead under the same tmux name, as `lead spawn` (`--department` for a department lead).

For two hours after a rotate, the spawned lead's first prompt carries, under `## Where the last lead left off`, the state file as it was, `## Restart these watches` with each background shell the old session ran (they die with it), and one line: look older context up with `issue-cli lead history <words>`. The note and the chat summary stay on disk instead of in the prompt. `--no-start` writes the note and digest and changes nothing else; a lead that isn't live gets the spawn command to run. Workers are refused.

Measured on 2026-11-15, before rotate: the CEO's spawn prompt was 27.8 KB (about 7k tokens: the brief 18 KB, the note and the last day's chat 9.7 KB) and its first turn read about 50k tokens, so about 43k of a first turn is Claude Code's own (its system prompt, the tool schemas, the CLAUDE.md files). A rotated lead starts at that floor plus its brief and state file instead of the 0.5–1 M it had grown to.

The nightly flush stays: it is a worker's richer note at the end of the day. Rotate is the daytime path and needs no worker run and no owner start.

### History

The old chat isn't lost, it is searched:

```bash
issue-cli lead history dispatch board          # every word must match
issue-cli lead history "release" --days 60 --max 20
```

It reads the lead's digests in `sessions/` and its Claude Code transcripts still on disk from the last `--days` (30 by default; Claude Code deletes old transcripts, the digests stay). It searches what the owner and the lead said and the Bash commands the lead ran, never a tool's output, and prints only the matching lines, newest first: `2026-11-14 10:05 command: issue-cli dispatch board --quote "…"  [2026-11-14-lead-proj.digest.md]`.

A digest keeps every owner message (2 KB each at most), the lead lines that decide or report something (decided, approved, dispatched, landed, a rule, a `D-n`, next), and the commands that changed something (`issue-cli` dispatch, approve, decide, transition, close, queue, memory add, job, release, comment; `git` merge, push, tag, revert, reset), the newest 400. A line that looks like a secret is left out. A Codex lead keeps no transcript: its digest says so.

### Local CI

With a `ci:` block ([Workflow: Local CI](../workflow.md#local-ci)) nobody re-runs the checks on main by hand: the viewer starts a run after every merge. CI never messages the lead: it runs on a timer, and nothing on a timer prompts a session. Red reaches the Status page, `status.md`, the `ci-red` markers and the merged issues' `ci:` comments. The lead reads `issue-cli ci status` when you ask, at each check-in and before a push. When main is red it brings it to you and you decide together whether to revert, fix forward or dispatch a fix; CI never reverts. Before any push, and always before a release, the lead runs `issue-cli ci status`, and it never pushes a release while main is red. The lead brief says so when `ci:` is set.

With a `deploy:` list ([Workflow: Deploy](../workflow.md#deploy)) the lead doesn't walk a project's deploy from its notes: `issue-cli deploy --dry-run` shows the steps and what would refuse each now, and `issue-cli deploy --quote "<your words>"` runs them, stopping at the first red step, with your words for the steps that need `owner-ok`. The record is the Report's Last deploy card; after a fix the lead goes on with `issue-cli deploy --from <step>`.

With `workbench.release_candidates: true` (issue-viewer has it), a release goes through a release candidate first ([Roadmap: Cutting a release](../roadmap.md#cutting-a-release-candidate-soak-publish)). When the milestone is ready, the lead tags `<version>-rc.1` locally, installs it, restarts the viewer and tells the other projects' leads. Their bugs come in through `issue-cli report-bug` and join the release's milestone. Every merge after the candidate, bugfixes included, needs the next one. The lead never publishes (the `## <version>` heading and the push) without your explicit ok on a soaked candidate. The lead brief says so when the key is set, and the Waiting for release card names each step.

### Relaying questions

Workers that need a decision ask in chat: they message the lead (Claude Code's SendMessage, addressed by tmux name) with the options and their recommendation, say they're waiting, and stop. The lead brings questions to you **one at a time**, with the worker's recommendation and its own. It records your answer in your words with `issue-cli decide <slug> --text "…" --how "lead chat" --quote "…"`, which writes the `D-n` line under `## Decisions`, then messages the asker.

The lead recalls decisions; it never invents them. It answers a worker only from what is recorded (the issue body and its `## Decisions`, comments, design docs, approvals) and cites the source (`D-2`, the comment, the doc path). When nothing recorded covers the question, it doesn't decide: it asks you and tells the worker to wait. Every lead brief carries these rules (`tracker.LeadRules`), whatever the workflow adds.

### Retro sweeps

A sweep worker reads what's new (`issue-cli retro list`) and proposes one issue per cause. A sweep files issues when its brief asks for them; otherwise it lists its proposals in its report and the lead files them. The `builtin:retro-sweep` brief asks for none: you file its proposals. The owner still decides each post-mortem action on its page, and an issue never applies one. The sweep ends with `issue-cli retro archive --until <time>`; see [`retro`](../CLI/overview.md#retro), and `brief: builtin:retro-sweep` for the steps.

### Worker rules

The dispatch prompt and the status prompts in `workflow.yaml` already tell workers these, but a short doc your `CLAUDE.md` points to helps sessions that weren't dispatched:

- Own your issue only. New ideas become `idea` issues (`issue-cli create`), referenced with `#slug`. In a worker session, `start`, `claim` and `transition` on another slug fail ("you work on <own-slug> only; ask your lead").
- Duplicates of your issue are the one exception. When the lead asks, a worker closes an issue that duplicates its own, or that its change fixes: `issue-cli close <dup> --duplicate-of <own-slug>` (or `--fixed-by <own-slug>`) parks it from any status in one step, sets `duplicate_of:` / `fixed_by:`, comments on both issues and leaves every checkbox alone. Any other worker is refused with the rule; the lead and the human close any issue. A worker may also file a child issue of its own when the lead asks; other new issues still need the human's ok, apart from a red on main (next rule).
- A red already on main gets an issue. The first worker to see a failure that `issue-cli ci status` also shows on main files it (`issue-cli create`) or, when one is filed, names that issue in its `tests:` comment. It is not that worker's to fix, and a comment alone leaves the next worker to diagnose it again.
- One task, then stop. When the issue reaches its last status, or a gate that waits on the human, stop: don't run `issue-cli next`, don't pick up another issue, don't start other agents.
- Stop at human gates. Ask once in chat, then stop and end your turn; don't wait or poll. Continue when the human messages you that it is approved. A "go" in your chat is not an approval.
- Proof that only the lead can run (a guided check script the worker guard refuses: `dispatch`, `lead spawn`): link the criterion to it with `issue-cli prove <slug> <AC> --lead "issue-cli check-script <slug> --yes"` and stop, without a `question:` comment. The lead's tick (`queue next`) lists it as "Waits on you", and the lead runs `issue-cli prove <slug> --lead-run` ([Proof that waits on the lead](../Workflow/proof.md#proof-that-waits-on-the-lead)).
- Leave a trail: tick boxes as they're done, add `progress:` comments at milestones, and the comments your gates require (`tests:`, `docs:`, …).
- Commit on your own branch as you go; only shipping merges.
- Write a retrospective when you stop (`issue-cli retrospective`).

### Before dispatching

- Commit process changes (`CLAUDE.md`, `workflow.yaml`) first: worktrees branch from the checkout's `HEAD`, so workers read whatever is committed there.
- Dispatch only when you say so in the lead's chat, and only the issues you name: `issue-cli dispatch <slug> --quote "<your words>"`, one call per issue (the same as ▶ on the board; `--agent codex`, `--reprompt --quote` to re-send a live worker's briefing), never with raw HTTP. Without `--quote` it starts nothing; each dispatch leaves `dispatch: "<words>" (lead chat, <date>)` on the issue. For an epic the lead proposes which children to start, you say which, and it dispatches exactly those; starting more later needs a new instruction. It never dispatches when planning finishes or picks the next issue on its own. See [A human starts the work](../agent-dispatch.md#a-human-starts-the-work).
- Put your rules for the worker in its issue, not only in a message: `issue-cli append <slug> --section "Lead rules" --body "- …"` before dispatch (the section is made when missing). The worker reads the body at `start` and `show`, so the rules survive a restart and are there for the review; a `SendMessage` can only point at them.
- A scope change mid-issue is a box, not a message: append the criterion (`issue-cli append <slug> --section "Acceptance criteria" --body "- [ ] …"`) and record why with a `D-n` (`issue-cli decide <slug> …`). Then message the worker naming the box and the `D-n`. A cut scope is a `D-n` too, sent early: code already written for the cut part is wasted.
- When you give a boxes rule, it reads: owner boxes and other issues' boxes are untouched; your own Implementation/Testing/Shipping boxes are yours to tick as you finish them. A blanket "don't tick any box" contradicts the worker's own gates.
- Don't dispatch onto a pending gate. When the issue only waits for your approval, `issue-cli dispatch` and ▶ refuse (exit 3, the gate named); ask for the approval instead, since a worker would claim the issue and stop. `--force` overrides it.

### Handoff and restarts

The lead writes a handoff note before it exits: `sessions/YYYY-MM-DD-lead-<project>.md`. The note ends with a `## Next` section: the ordered next steps. The viewer's Status page (`/p/<project>/status`) shows that `## Next` list in its What's next tile, and its text form, `issue-cli status` (the viewer's `status.md`; `--json` for the data), is a ready-made stand-up to paste. Its lists are capped: each "+K more" line names the `issue-cli list` command that shows the rest. Its **Waiting for release** block tells the lead when the current milestone is ready to cut (or what blocks it), what is merged and not released, and what is not pushed. A new lead's brief names its newest note to read first, falling back to the older `sessions/*-lead-session.md`.

### The nightly flush

A lead session doesn't live for days. During the day it [rotates](#rotate) when its tick says its context passed 300k tokens. The `lead-flush` job ends it each night, and the lead comes back by itself: the close starts a fresh session of the same lead at once, so its tick, the queue and the jobs go on through the night. The same close then rotates each department and group lead of the project that has run 20 hours or more, as `lead rotate --lead <it>` does (a handoff note from its state file, then it is closed after its turn and started again with its tick); one it can't rotate is said in the run and tried again the next night. A lead that still runs past 26 hours (its project's flush has no standing start, or the project has no tick) is named by `issue-cli health`, the Needs you list and the engine project's tick, with its command (docs/fs-churn.md#what-grows-over-time-and-what-bounds-it). It is a recurring job on the Product entry of projects.yaml, with an `at:` after the day's work:

```yaml
departments:
  - name: Product
    jobs:
      - id: lead-flush
        name: Lead flush
        every: day
        at: "23:30"
        brief: builtin:lead-flush
```

Nothing runs until the owner turns on its standing start (`issue-cli job auto on product/lead-flush --quote "<their words>"`). The lead's check-in then starts it from `job due`, or from `queue next --start`, which starts a due job on its standing start before any queue item. Starting a run takes the workflow's `chore` type ([Types of Work](../Workflow/types.md#the-chore-type)).

It needs no `type:`: the brief asks for `chore`. The run's worker takes `issue-cli lead flush` step by step, each with `--run <its run>`:

| Step | What it does |
| --- | --- |
| `pack` | Reads only: the previous note, the commands that show the state, and the last 24 hours of lead chat. The chat comes from the lead's Claude Code transcript, or its pane for a Codex lead, with tool calls left out |
| `note --file <f>` | Writes `sessions/<date>-lead-<project>.md`. Refuses over 80 lines, without the sections State, Queue, Waiting on the owner, Rules in force, How to resume and Next, or with a secret |
| `summary --file <f>` | Writes `sessions/<date>-lead-<project>.summary.md`, 30 lines at most, no secret |
| `close` | Closes the lead's session, stops its old tick and starts the lead again under the same name. Refuses until both files exist and while the lead is in the middle of a turn; the worker tries twice more, ten minutes apart, then leaves it open and says so. A start that fails after the close says so with the owner's `issue-cli lead spawn` |

`<date>` is the run's created day. Between `summary` and `close` the worker tidies memory from `issue-cli memory consolidate`. Proven memories are only named in its progress comment: the flush files nothing. It reads the lead scope (`memory list --scope lead`), which no other worker does, and changes none of it: the lead-scope memories that look stale go in its progress comment, and the next lead tidies them. A worker can take these steps only on its own run. The flush sends and decides nothing, and starts only the lead it closed, again: a restart of the lead the owner started, as [rotate](#rotate)'s finisher does, never a new agent. `issue-cli lead spawn` still refuses every worker.

`issue-cli lead spawn` puts the newest note and its summary into the CEO's first prompt under `## Where the last lead left off`, beside the memory pins the brief always carries. After a flush close (for two hours, like a rotate's handover) the fresh lead's prompt adds `## Your state file`, the state file as it was at the close without its Workers, Queue and Owner asks sections (the note above holds them, written from that state the same minute; `issue-cli lead state` prints them as they are now), and `## Restart these watches`, which always holds the lead's tick (the one on record, else `issue-cli lead tick --watch --exit-on-news`).

One tick per lead: the close stops the old tick if it outlived its session, and a new `lead tick --watch` stops any older tick of the same lead that still runs (it checks the pid still runs a lead tick first), so a respawned lead never ticks twice. An older note over the cap is cut, with its path. Department leads keep the line naming their note.

After a restart (tmux and the board gone, worktrees kept):

1. Start the board and run `issue-cli lead spawn`.
2. The lead reads its note.
3. The canvas's **Unfinished jobs** lane lists the issues with a worktree or an active status and no live agent. Re-dispatch those from the board or with `issue-cli dispatch <slug>`; it reuses their worktrees.

## Epics

A large feature built in many pieces runs as an **epic** with **child issues**. The project lead leads it. Each child is an ordinary issue: it branches from `main` and ships to `main`.

1. Link each child to the epic: `issue-cli set-meta <child> --key parent --value <epic>`.
2. Dispatch the children from the board, in the order the epic's design gives.
3. The epic issue runs the workflow too: its build boxes tick when every child has shipped.

An epic may still carry `base:` or `lead:` from when each epic had its own branch and lead. Both are read as plain custom fields and change nothing.

The [issues canvas](../UI/overview.md#issues-canvas) shows epics with their children and roll-up progress per version. To show the human an epic's state, send `/p/<project>/canvas?hl=<epic slug>`.

## Worked example: Siege Cup

Siege Cup (a stand-in name) is a football-manager-style game, built in Godot and C# by one person and Claude agents since September 2026. It ran this pattern on one board project, starting as a convention before leads were part of the tool:

- **235 issues**, a **710-line `workflow.yaml`** with ten statuses and 13 system overlays, and three board buttons (*Balance report*, *Speed sweep*, *Idea sweep*).
- **Four human gates**: discussion → design, design → backlog, starting work, and playtest → documentation. The lead may tick discussion → design once every question was answered by the human and recorded, and any gate the human approves in its chat, always after an `approval:` comment.
- **Workers** run as windows in one shared tmux session for the project, each in a worktree under `.worktrees/` with `issues/` left out.
- **Retros** from every worker are swept by the lead, which fixes the process and files tool issues; swept files move to `retros/archive/`.
- **An epic** (a new game mode) staged around 18 child issues on its own experiment branch, before the tool settled on one lead and children shipping to `main`.

![Siege Cup's ten statuses, each with the check that guards it; four of them need a human](../images/lifecycle.png)

One issue there, replayed from its logs, went from discussion to pushed in 3 h 50 m with four approvals and a few chat answers:

![A worker's issue-cli calls and the human's words from discussion to git push, beside the issue as the human saw it](../images/issue-replay.png)
