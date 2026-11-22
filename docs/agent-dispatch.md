---
title: "Agent Dispatch"
order: 6
---

## Overview

The board and detail views can dispatch issues to AI agents (Claude or Codex) via tmux sessions. Dispatch creates a session, opens a terminal, and starts the agent with a generated prompt.

## Requirements

- **tmux** is mandatory (3.2 or newer). The whole dispatch lifecycle is tmux: the agent starts as the command of a new session or window, with its environment and working directory set by tmux.
- **The agent CLI**: `claude` or `codex` on the `PATH` of the server.
- **git**, when the workflow sets `worktree: true`.
- **A terminal command**, or `terminal: "none"` to attach yourself (see [Terminal Configuration](#terminal-configuration)). Left unset, it is i3 + alacritty where `i3-msg` is on `PATH`, and headless (like `none`) everywhere else, such as a Mac.

> **Tip.** Not sure which `terminal` value fits your setup? Ask your coding agent: *"Look at my OS, window manager and installed terminals, then set the `terminal:` field in `projects.yaml` to something that works here."* It can detect i3, GNOME, macOS or WSL, check what's on `$PATH`, and pick the right command, or fall back to `"none"`.

## A human starts the work

The tool runs agents only when a human directs it. Once started, agents work on their own.

- **A human starts the work.** Each worker starts from a dispatch on the board (▶), or from `issue-cli dispatch <slug> --quote "<the human's words>"`, where the words name that worker. The lead runs that command only when you tell it to in its chat, and only for the issues you name. It never starts work on its own: not when planning finishes, not as the next issue, not more workers than you named, unless you turned auto-start on for the project (below). The quote is the lead's report of what you said: the tool records it in a `dispatch:` comment but can't check it, and the trail makes it reviewable.
- **Then agents work on their own.** A started worker runs in auto mode through its one issue's workflow and stops there. It stops at the gates the workflow puts in front of you. In a worker session, `start`, `claim` and `transition` on any other issue fail, and the worker prompt says never to pick the next issue.
- **No unattended runs.** The tool has no scheduler, timer, overnight batch or auto-dispatch of its own. Don't wrap anything around it (cron, a loop script, a scheduled check-in) that starts or prompts sessions without a human. Unattended runs are outside this tool: use `claude -p` or the Agent SDK with your own API key, not a Claude subscription.
- **One exception: auto-start, opt-in per project.** Your words can be a standing start: `issue-cli autostart on --quote "<your words>"` stores them for one project, and from then on the lead may start one worker at a time from the project's `next` order while memory is low (`issue-cli autostart next`, then `issue-cli dispatch <slug> --autostart`). It's off by default, only you (or the lead with your quoted words) turn it on, and every auto-start quotes your stored words in its `dispatch:` comment with the memory reading. The tool still runs nothing on a timer: the lead's check-in starts each worker, and `autostart pause` or `off` stops it. See [Auto-start](#auto-start).
- **The second exception: recurring jobs, opt-in per job.** A department's recurring job (a weekly SEO pass, a daily lead search) can have a standing start too: `issue-cli job auto on <dept>/<job> --quote "<your words>"` (or `--all`). From then on, when the job is due, the lead's check-in starts its run (`issue-cli job due`, then `issue-cli job run <job> --scheduled`; the tick's `queue next --start` starts it first, and `queue next` and `autostart next` print its command) inside the project's budget: mem-guard, the memory line and worker cap from auto-start's window or else the queue's limits (`queue limits`; with neither set, memory under 50% and no worker cap), and the machine-wide gap. It's off by default. Without it, a due job waits in Needs you until you press ▶ Start run or name it. Every run quotes the words it started on in its `dispatch:` comment. The tool still runs nothing on a timer. See [Recurring jobs](departments.md#recurring-jobs).

- **The third exception: the post-mortem at close, opt-in per workflow.** With `postmortem: start: auto` in a workflow file, the transition into `at:` (done) starts one read-only post-mortem agent for that issue, `postmortem-<slug>`, headless, after `mem-guard check` passes. The owner's words are the workflow setting itself and the issue's own dispatch. It starts once per issue, never for another issue, and it changes nothing until you agree to its actions. With `start: ask` (the default) the transition only prints `issue-cli postmortem start <slug> --quote "…"` for the lead. See [Post-mortem](workflow.md#post-mortem). Source: factory/post-mortem-actions-each-lesson-to-one-place-only-if-a-fresh-agent-would-act-differently-applied-after-the-owner-agrees#D-1, #D-10 (provisional).

Agents message each other with Claude Code's own `ListAgents` / `SendMessage`. `issue-cli` never pastes into a Claude session on an agent's behalf: `issue-cli lead message` pastes only into Codex sessions, and for a Claude session it prints the `SendMessage` call to make instead.

### Where the tool types into a session

Every place the engine pastes into or starts a session, and what starts it:

| What | Code | Started by |
|---|---|---|
| Dispatch a worker | `agentrun.Start` via ▶ / `POST …/dispatch` | your click |
| | `issue-cli dispatch --quote` | your words, quoted (the lead or your shell) |
| | `issue-cli dispatch --autostart` | your stored [auto-start](#auto-start) words, at the lead's check-in |
| | `issue-cli dispatch --takeover` | the issue's own `dispatch:` words, at the lead's check-in ([takeover](#takeover-at-a-usage-limit)) |
| | `issue-cli dispatch --fresh` | the issue's own `dispatch:` words, at the lead's check-in, after the worker stopped at a `session: fresh` status ([fresh session per stage](workflow.md#fresh-session-per-stage)) |
| | `issue-cli job run <job> --quote` / ▶ Start run on Needs you | your words or your click: one [recurring job](departments.md#recurring-jobs) run |
| | `issue-cli job run <job> --scheduled` | the job's stored standing-start words, at the lead's check-in |
| Re-send prompt | `agentrun.Reprompt` via the button / `POST …/dispatch/reprompt` | your click |
| | `issue-cli dispatch --reprompt --quote` | your words, quoted |
| Approval nudge into the worker | `tmuxSendKeys` (`agentrun.Nudge`) in the approve handler | your Approve click in the viewer, or Approve on Needs you |
| Send-back nudge into the worker | `tmuxSendKeys` (`agentrun.Nudge`) via `POST …/issue/<slug>/send-back` | your Send back for fixes click (or Back in the YOLO review) |
| Spawn the lead | `issue-cli lead spawn`, **Spawn lead** (`startLead`) | you |
| Health review agent | **Run review** on `/health` | your click |
| | `issue-cli health review --spawn --quote` | your words, quoted |
| Action buttons (`project_actions:`, issue `actions:`), Review retros | `startAgentSession` via `POST …/action/<id>`, `POST …/retros/review` | your click |
| Body editor | `nvim` typed into a tmux window (`tmuxTypeCommand`, not an agent) | your click |
| `issue-cli lead message` | `agentrun.Paste` | the lead; Codex sessions only |
| [Lead chat](#lead-chat): message | `agentrun.Paste` via `POST …/lead/send` | your words, typed and sent in the page |
| Lead chat: Esc, Ctrl+C, ↑ ↓ 1 2 3 ⏎, Claude Code shortcuts (ctrl+x ctrl+s, ctrl+o, ctrl+b ctrl+b) | `agentrun.SendKey` via `POST …/lead/key` | your click |
| Lead chat: answering the CEO's question card | `agentrun.SendKey` / `SendText` via `POST …/lead/answer`, only while that question is on the lead's screen | your click |

Nothing runs on a timer that reaches a session. The viewer's timers (local CI's watcher, the daily health snapshot) run `make` and `git` and write files; they never paste into or prompt a session. The one timer that touches sessions only closes them: the [session sweep](#closing-finished-workers) ends worker sessions whose issue is closed. Local CI reports to the Status page, `status.md`, the `ci-red` markers and `ci:` comments, and the lead reads `issue-cli ci status` when you ask.

## How to Dispatch

- **Board view** — hover a card, click the play button, pick Claude or Codex
- **Detail view** — two buttons in the sidebar (Claude / Codex)
- **CLI** — `issue-cli dispatch <slug> --quote "<the human's words>" [--how "lead chat"] [--agent claude|codex] [--force] [--reprompt]`. `--quote` is required: without the human's words nothing starts (exit 2). Each start or re-prompt leaves `dispatch: "<words>" (<how>, <date>)` on the issue, signed by the lead when the lead ran it; `--how` defaults to `lead chat` for the lead and `owner's shell` otherwise. One slug per call. It builds the same spec as ▶ (`agentrun.DispatchSpec`: prompt, session, worktree) and starts it through tmux itself, so the viewer doesn't need to be running. It prints the steps, then `Inside tmux:` / `From a shell:` commands to reach the session. Exit codes: 0 started, re-attached or re-prompted; 1 the launch failed, or `--reprompt` found no live worker; 2 usage; 3 waiting on a gate (below). `--json` prints the object the endpoint returns. Workers can't dispatch. Agents should use this rather than the endpoint: a single `issue-cli` permission covers it, so no `curl` allow is needed.
- **API** — `POST /p/<project>/issue/<slug>/dispatch` with `{"agent": "claude"}` or `{"agent": "codex"}`, plus `"force": true` to skip the gate refusal

### Waiting on a gate

When everything an issue's next transition needs is done except the owner's approval (the same rule as the Status page's "waiting on you" gates, `tracker.WaitingOnGate`), a new worker would only stop at that gate. Dispatch refuses there and starts nothing:

- ▶ shows "Waiting on your approval for <status>", with an **Approve** link and a **Dispatch anyway** button.
- `POST …/dispatch` answers 409 `{"status": "waiting-on-gate", "gate": "<status>", …}`.
- `issue-cli dispatch` exits 3 and prints the approve link and the `--force --quote` command.

A gate whose work isn't finished doesn't block: the worker still has something to do. That includes the boxes of the section the move into the current status appended (Implementation on entering in progress), even when the gate's own rules don't check them. A box only the owner may tick (the Human Testing box, or one marked "(owner)") is not the worker's: an issue in human-testing whose HT1 is still open waits on the owner, and Needs you lists its gate. The week report's blocked lines and auto-start follow the same rule. Re-attaching to a live worker is never refused.

## How the prompt is delivered

The prompt is saved to `<workdir>/.agent-logs/<session>/dispatch-prompt.txt`, in the project's checkout even when the agent runs in a worktree (the agent's clilog and `messages/` sit next to it), so removing a worktree loses none of the agent's history. Each new launch on an issue is also appended to the issue's stats sidecar as `dispatches: [{at, session, agent}]` ([Workflow Stats](workflow-stats.md)); re-attaching to a live session is not. The launch that creates the issue's work branch also records `base_branch` and `base_sha`, the branch and commit it was cut from, which `issue-cli show` and `start` print as `Base:`. The agent starts with it as its positional argument (`claude "<prompt>"`, the same as codex). It is never pasted into a booting agent. Claude is also started with `-n <session name>` (`claude -n agent-<slug> "<prompt>"`, `-n lead-<project>` for the lead), so its tmux name is its address for Claude Code's built-in messaging between sessions (`ListAgents` / `SendMessage`); codex gets no name. A codex start also carries `-c projects={"<its folder>"={trust_level="trusted"}}` and `-c check_for_update_on_startup=false`, so it reaches its prompt with nothing to press (see [A stalled start](#a-stalled-start)). Earlier versions typed `claude`, waited a fixed 3 s and pasted the prompt. When Claude took longer to start, the paste was dropped, yet every step still reported ok.

### How the agent starts

The agent is the window's own command. Dispatch creates the session or window detached, in one tmux call, with the working directory (`-c`), the [environment](#environment-variables) (`-e`) and the command:

```sh
tmux new-window -d -t '=work:' -n agent-<slug> -c <worktree> \
  -e ISSUE_CLI_LOG=… -e ISSUE_VIEWER_PROJECT=… … \
  "$SHELL" -lic 'trap : INT; claude -n agent-<slug> "$(cat "<log dir>/dispatch-prompt.txt")"; exec "$SHELL" -l'
```

(`new-session -d -s …` when there is no shared session yet, or no `tmux_session`.) The agent runs in your login shell, interactive, so it is found on the same `PATH`, with the same aliases, as when you type it. When it exits, or is stopped with Ctrl-C, the window keeps a shell in the worktree. Nothing is typed into a shell, so keys you type into an attached tmux client while a dispatch runs can't reach the start command, and dispatch never selects the new window. Before this, dispatch typed `export …`, `cd …` and the start command into the window's shell after selecting it or opening its terminal, and whatever you were typing landed in front of the command (`aclaude: command not found`). The pane's output is no longer copied to a `rawlog` file.

Dispatch then polls the pane for the prompt's last line in the conversation. Text still in Claude Code's input box does not count: while it sits there, Enter is pressed again, up to twice. It reports the step **Prompt delivered**, or a failed step after 20 s. The text itself is never re-sent automatically.

### Worker setup

An issue's Claude Code worker starts lean: it loads only what its issue needs, not every MCP server, plugin and skill of your own Claude Code. Each of those adds tool names, server instructions or a skill description to the worker's first turn, which the worker reads again on every turn (measured 2026-11-15: a worker's first turn was 82k tokens).

What a lean worker gets:

- **MCP servers**: `--strict-mcp-config --mcp-config=<log dir>/mcp.json`. The file holds the department's [connectors](workflow-stats.md) and each server `workers: mcp:` names whose `systems:`, `types:` or `labels:` match the issue (an item with none of them matches every worker). The definition is copied from your Claude Code config as you wrote it: `~/.claude.json` (user servers, then its entry for the project), and the project's `.mcp.json`. No other server loads.
- **claude.ai connectors** (Claude Docs and the like) are off: the session runs with `ENABLE_CLAUDEAI_MCP_SERVERS=false`, since `--strict-mcp-config` does not cover them.
- **Plugins and skills**: `--settings=<log dir>/settings.json` names every plugin you have on (`enabledPlugins` in your and the project's settings, and plugins synced from claude.ai as `<name>@synced`) as `false`, and every skill as `"off"` in `skillOverrides`, except those `workers: plugins:` and `skills:` keep for the issue. The skills are the personal (`~/.claude/skills`) and project (`.claude/skills`) folders, and the ones with no folder: bundled with Claude Code (dataviz, artifact-design, claude-in-chrome, …) and synced from your claude.ai account (`anthropic-skills:docx`, …). Their names come from the `skill_listing` of the project's 20 newest transcripts (the root's and its worktrees', first 256 KB of each) and from the `skillOverrides` of the 20 newest workers' settings, so the set does not shrink once every new transcript is a lean worker's. `disableBundledSkills: true` covers the bundled ones no transcript listed yet, unless the worker keeps a skill with no folder. Claude Code's built-in slash commands stay typable.
- **The Artifact tool** (publishing claude.ai pages) is off: the session runs with `CLAUDE_CODE_DISABLE_ARTIFACT=1` unless `workers: tools: [Artifact]` keeps it. Its schema was the largest single part of a worker's first turn (12k of 54.5k tokens, measured 2026-11-16).
- **Workflow, SendFeedback, ReportFindings and ListAgents** (Claude Code built-in tools for multi-agent workflows, feedback drafts, code-review findings and listing sessions) are off: `--disallowedTools=Workflow,SendFeedback,ReportFindings,ListAgents`, in the same list as the connectors' denied tools, takes their schemas out of the context. Workers made no call to any of them in 3 days (about 10k tokens of the first turn, 2026-11-18). `workers: tools:` keeps one by name. `Agent` and `ScheduleWakeup` stay: workers call them. A worker still receives a lead's `SendMessage`.
- **Kept as they are**: your hooks, CLAUDE.md files, model and permissions. Your own Claude Code files are never written; both files live in the session's log dir (`.agent-logs/<session>/`, mode 0600).

Leads (the CEO, department and group leads) have their own lean setup ([Lead setup](#lead-setup)); codex workers and agents started without an issue keep your whole setup. The dispatch report says what a worker got: `Worker setup (workers:): chrome-devtools; plugins off 4, skills off 57 (31 listed by Claude Code), bundled skills off, Artifact, Workflow, SendFeedback, ReportFindings, ListAgents off, claude.ai connectors off`. A server `workers:` names that your config does not define is a warning step (`Worker MCP server not found`), and the worker starts without it; `issue-cli health lint` (check `workers`) names it too, with the servers you do have.

To give workers something back, add it to the workflow file (a department's own file sets its own workers):

```yaml
workers:
  mcp:
    - {server: chrome-devtools, systems: [UI], labels: [ui]}   # a name from claude mcp list
    - {server: playwright, types: [bugfix]}
  plugins: [frontend-design@claude-plugins-official]          # an id from claude plugin list
  skills:                                                     # a name as Claude Code lists it
    - stageshow                                               # every worker
    - {skill: "anthropic-skills:pdf", types: [report]}        # only report workers (systems:, labels: too)
  tools: [Artifact]                                           # keep a built-in tool: Artifact, Workflow, SendFeedback, ReportFindings, ListAgents
  # setup: full    # every worker gets your whole setup, as before
```

A `skills:` item is a name (every worker keeps it) or a map like an `mcp:` item, kept by the issues its `systems:`, `types:` or `labels:` match. `issue-cli health lint` (check `workers`) warns on a kept name no skill folder holds and no transcript listed, with the names it knows, on a `types:` the file does not define, and on a `tools:` name other than those five.

No `workers:` block means lean with nothing added. This repo gives `chrome-devtools` and `playwright` to UI workers; its playtest runner drives Chrome through `make ux-test` and `make shot` (chromedp), not MCP, so every worker can run those.

### Lead setup

A Claude Code lead (the CEO, a department or group lead) starts without three things leads did not use in a week of transcripts (report o-20261120-07ad, 2026-11-20: 20 lead sessions, 4,226 turns). They were about 29k tokens of a 56k to 67k first turn, which every lead turn reads again (about 119M tokens a week across the leads):

- **The skill list** (about 9.9k tokens; no Skill call in 7 days): `--settings=<log dir>/settings.json` names every skill `"off"` in `skillOverrides` and sets `disableBundledSkills: true`, from the same lists a lean worker's settings use. Plugins are not named: a lead keeps every plugin you have on. Claude Code's built-in slash commands stay typable; the lead brief already rules out a `/loop` cron.
- **The Artifact tool** (about 13.4k tokens; 3 calls in 7 days, all in one lead session): `CLAUDE_CODE_DISABLE_ARTIFACT=1`.
- **Workflow, SendFeedback, ReportFindings** (about 4.9k tokens; no call): `--disallowedTools`, in one list with a department's connector tools. ListAgents stays (leads call it).

The rest is as before: your MCP servers (no `--strict-mcp-config`), the department's connectors, claude.ai connectors, hooks, CLAUDE.md files, model and permissions. Your own Claude Code files are never written. The spawn's steps (`issue-cli lead spawn`, a rotate, the viewer's start) say what the lead started without: `Lead setup (leads:): skills off 66 (31 listed by Claude Code), bundled skills off, Artifact, Workflow, SendFeedback, ReportFindings off`. A codex lead and a re-attach to a live lead are unchanged; the next spawn or rotate picks the setup up.

To give a project's leads something back, add it to its workflow file (department and group leads read the project's `leads:`):

```yaml
leads:
  tools: [Artifact]   # keep a built-in tool: Artifact, Workflow, SendFeedback, ReportFindings
  # setup: full       # leads get your whole setup, as before
```

`issue-cli health lint` warns on a `leads.tools` name other than those four and a `leads.setup` other than `lean` or `full`. `issue-cli spend --context` has a `Leads:` line beside `Workers:` (and each lead session's floor in its top-sessions row), each with its floor by part (system, tools with the biggest named, MCP, CLAUDE.md files, skills, brief), so the floor before and after a change, and which part moved, shows there.

### Re-send prompt

If an agent came up without its briefing, use **Re-send prompt** in the issue sidebar, the link in the reattach banner, or `issue-cli dispatch <slug> --reprompt --quote "<the human's words>"`. All three run `agentrun.Reprompt` (the button through `POST /p/<project>/issue/<slug>/dispatch/reprompt`), which pastes the current briefing into the issue's live session through a per-session named tmux buffer and submits it. It asks for confirmation first. If no agent is alive it fails with "No live agent session" and creates nothing; dispatch instead, which replaces a dead session. A session that is only a shell counts as no agent (`agentrun.BareShell`): its agent exited, the briefing would run as commands, so nothing is pasted.

### Read the prompt without dispatching

`GET /p/<project>/issue/<slug>/dispatch-prompt` (`?format=json` for `{"prompt","slug","status","worktree","branch","session"}`) and `issue-cli dispatch-prompt <slug> [--json]` print the prompt a dispatch would send right now. Both share one builder (`briefing.BuildDispatchPrompt`), so the two outputs are byte-identical. Neither creates a worktree, session or file. The prompt follows the issue's current status, so it can differ from the saved `dispatch-prompt.txt` of an earlier dispatch.

## Re-dispatching to a live session

Dispatching to an issue whose tmux session is still alive does **not** error out and does **not** re-prompt the agent. The handler probes `tmux has-session -t <name>` first; if the session exists it skips `new-session`, all logging/env setup, and the prompt, and just opens a terminal attached to the existing session.

The response in this case is:

```json
{
  "status": "reattached",
  "session": "agent-<slug>",
  "steps": [
    {"name": "Existing session — attaching", "status": "reattached"},
    {"name": "Open terminal", "status": "ok"}
  ]
}
```

The dispatch modal renders this with a yellow warning banner. The banner links **Re-send prompt** for an agent that never got its briefing. Kill the existing session manually (`tmux kill-session -t agent-<slug>`) if you want a fresh dispatch instead.

## A launch that dies

`dispatched` means an agent is running. `agentrun.Start` watches every launch for `LaunchSettle` (3 seconds), and for Claude looks once more after the prompt delivery check. An agent that exited in that time comes back as `status: "error"` with an `Agent running` step quoting the pane's last lines (the shell's `command not found`, say), and its session is removed. The same goes for every path through the launcher: dispatch, auto-start, the queue, lead spawn, job runs, the post-mortem and the health review. No `dispatch:` comment is written, no queue item is marked started, no job run is logged, and the briefing's decisions aren't logged as injected; the worktree stays for the next dispatch. A run issue stays on the board, so restart its worker with `issue-cli dispatch`. An agent that dies after this window is past the check.

An agent that dies later leaves a session with only a shell in it, which still looks live to anything that asks whether the session exists. Three things look at the pane instead: both Crew pages (`/crew` and `/p/<project>/agents`) show the worker as *agent exited · only a shell is left*, sorted up with what waits on you, with no Nudge (the server refuses one too) and with Stop still there; push sends one *Worker exited* (Getting Started, phone notifications); `--reprompt` and Re-send prompt refuse (see above). Plain `issue-cli dispatch` replaces the session. These are looks: nothing restarts the worker. The [session sweep](#closing-finished-workers) closes such a session within 10 minutes unless someone is attached to it; the worktree stays for the next dispatch.

## Closing finished workers

A worker's session has nothing left to do once its issue is closed (done, parked, or a side exit after done), but an idle Claude Code session still holds 100 to 200 MB, plus its MCP servers. Left alone they add up: 117 sessions sat in tmux for up to 11 days on one machine, 49 of them workers of `done` issues, until memory ran out. So the tool closes them:

- **At the close.** `issue-cli transition` into a closed status, `issue-cli done` and `issue-cli close` end the issue's `agent-<slug>` and the `limited-<slug>` a takeover left (in a shared `tmux_session`, their windows), and print one line: `Worker session: closed agent-cli-x (cli/x is done).` When the command runs in that very pane (the worker moving its own issue to done), or the agent there is in a turn, the session closes when the turn ends instead: a detached shell watches the pane for the working line and closes it after two looks without one (at most 30 minutes), so the agent reads the output and can still add a memory. A session mem-guard paused is left (it means to resume it) for the sweep. Leads, the post-mortem agent the close may start and any other name are never closed.
- **The sweep.** `issue-cli agents sweep [--dry-run] [--json]` closes what is left: a worker session whose issue is closed, any `agent-*` / `limited-*` pane that is only a shell (its agent exited), and the idle session of a [report waiting on your review](#a-report-waiting-on-your-review). It skips a session paused by mem-guard, one in a turn, one a person is attached to (standalone sessions), and this process's own. The owner or a lead runs it; workers are refused. The viewer that holds `viewer.json` runs the same sweep every 10 minutes and logs each close (`session sweep: <project>: closed agent-x (x is done)`); a sandbox viewer never sweeps. A closed issue's session whose name an open issue of another project holds (projects share one tmux server) is kept.

The sweep only closes: it starts, types into and prompts nothing.

### A report waiting on your review

A report's worker is done once the report is on the shelf and the issue waits on your review (an issue whose path gates on `has_output`, the [report type](Workflow/types.md#the-report-type), at or past that gate, or before it with an unticked box marked yours (`… (owner)`), not closed or parked, with a good output of the gate's kind: not rejected, its file still there; `outputs.Shelved`). Its session would sit idle for hours: on 2026-11-17 three such sessions held 3 of 4 worker slots twice, about 2 hours each time, while the backlog waited.

On 2026-11-20 one of two finished reports was still at idea, waiting on an owner box, so it counted as at work, and both sessions held the two slots all night: a report before its gate with an open owner box and its output on the shelf counts as waiting on you too.

- **It holds no slot.** The worker cap (`agentrun.LiveWorkers`: auto-start, `queue next`, a job's standing start) doesn't count its session.
- **The tick closes it.** `issue-cli agents sweep` and `issue-cli lead tick` close its session once it is idle (not in a turn, not paused, nobody attached), and the tick prints a held line, `closed: agent-<slug> (<slug> waits on the owner's review at human-testing; its output <id> "<title>" is on the shelf, so it holds no worker slot; re-dispatch resumes it)`, and posts a `session: closed agent-<slug> …` comment on the issue. The viewer's 10-minute sweep keeps every open issue's session, this one too.
- **The Agents tab says why.** The stopped worker on the Crew page carries *session closed: its output waits on your review on the shelf* (`agents.json`'s `closed` has the whole comment; the classic canvas shows it on the node) until a `dispatch:` line brings a worker back.
- **A re-dispatch resumes it.** Its briefing (both forms) gets *Resuming after a closed session*: the comment, the `progress:` comments before it, your comments after it (they are why it was dispatched again) and `issue-cli outputs --issue <slug>` for the output and your review. A rejected output is not shelved: that worker keeps its session and its slot until it adds a new one.

The same pattern applies to **Edit in nvim**: re-triggering it while the previous edit session is still alive returns `{"status": "reattached", "reattached": true, ...}` and opens a terminal attached to the existing nvim instance. The reattach request does NOT register a new save-on-exit handler — the original request's goroutine still owns the sync-back when nvim exits.

## Terminal Configuration

The terminal that opens for the agent session is configurable via the `terminal` field in `projects.yaml`:

```yaml
- name: "My Project"
  terminal: "alacritty -e tmux attach -t {{session}}"
```

The handler fills in the placeholders below and runs the command via `sh -c`. Each value is shell-quoted when it isn't safe to type unquoted.

| Placeholder   | Becomes |
|:--------------|:--------|
| `{{session}}` | the tmux session to attach: the shared session in shared mode, otherwise the agent's own |
| `{{project}}` | the project slug |
| `{{name}}`    | the agent's name: `agent-<slug>`, `lead-<project>`, or `agent-<slug>-edit` for **Edit in nvim** |
| `{{role}}`    | `lead`, `worker` or `editor` |
| `{{window}}`  | the agent's exact tmux target: `'=agent-<slug>:'`, or `'=work:=agent-<slug>'` in shared mode, ready for `tmux select-window -t {{window}}` |

`terminal:` is for workers and the editor. The project lead follows [`lead_terminal:`](#lead-terminal) instead.

### Examples

| Platform                   | Config                                                                       |
|:---------------------------|:-----------------------------------------------------------------------------|
| Linux + alacritty          | `alacritty -e tmux attach -t {{session}}`                                    |
| Linux + i3 + alacritty     | `i3-msg exec "alacritty -T {{session}} -e tmux attach -t {{session}}"`       |
| macOS + iTerm2             | `osascript -e 'tell app "iTerm2" to create window with default profile command "tmux attach -t {{session}}"'` |
| macOS + Terminal.app       | `osascript -e 'tell app "Terminal" to do script "tmux attach -t {{session}}"'` |
| Headless (attach manually) | `none`                                                                       |

If `terminal` is unset and `i3-msg` is on `PATH` (or `i3_workspace` is set), it defaults to i3 + alacritty, with the window titled after the session (`alacritty -T <session>`). On i3, opening an agent from the agents canvas when its session already has a terminal focuses the window with that exact title instead of opening another, so give your own `terminal` command `-T {{session}}` (or your terminal's equivalent) to get the same. Unset on a machine without i3 (a Mac, most Linux desktops) is headless, and the dispatch step says how to set `terminal:` to get a window. Set to `none` to only create the tmux session. Nothing on screen changes, and the response carries two ways to reach the agent:

- `switch_cmd` is for a shell already inside tmux, e.g. `tmux switch-client -t '=agent-<slug>:'` (an exact target). A plain `tmux attach` refuses to nest there.
- `attach_cmd` is for a plain shell, e.g. `tmux attach -t agent-<slug>`.

`issue-cli lead spawn` prints both, as `Inside tmux:` and `From a shell:`, whenever the lead is headless.

A `terminal:` at the top of `projects.yaml` is the default for every project that doesn't set its own (see [One terminal, a session per project](#one-terminal-a-session-per-project)).

When the viewer itself runs with `TMUX_TMPDIR` set (a sandbox or playtest scenario on a private tmux server), `attach_cmd` starts with `TMUX_TMPDIR=<dir>`, so it reaches that server from any shell. `switch_cmd` has no prefix, because inside tmux `$TMUX` already names the server. A `terminal` command inherits the variable through `sh -c`; the i3 default passes it on with `env`. The board waits for the `terminal` command to exit, so use one that returns at once (`i3-msg exec …`, or `setsid -f alacritty -e … >/dev/null 2>&1`).

## Shared tmux Session

By default every dispatch gets its own tmux session (`agent-<slug>`) and its own terminal window. Set `tmux_session` on a project to run all its agents as **windows inside one shared session** instead:

```yaml
- name: "My Project"
  tmux_session: "work"
  terminal: "alacritty -e tmux attach -t {{session}}"
```

On dispatch:

- If the `work` session doesn't exist, it is created with the agent as its first window (`agent-<slug>`).
- If it exists, a new `agent-<slug>` window is added to it in the background. Dispatch never selects it, so whoever is looking at the session (usually the lead) keeps their window and their typing. With `terminal: none` a re-dispatch doesn't select it either. Opening the agent from the agents canvas selects it.
- A terminal (via `terminal`, where `{{session}}` becomes the shared session name) is opened **only when no client is attached** to the shared session. If you already have it open, the new window just appears there.
- Re-dispatching an issue whose window is still alive reattaches (selects the window) instead of re-prompting.

"Edit in nvim" follows the same rule: it opens as an `agent-<slug>-edit` window, and only that window is closed when the edit finishes. Agent windows are listed as `work:agent-<slug>` and receive approval notifications like per-agent sessions. The lead window (`lead-<project>`) lives in the same shared session and is listed as `work:lead-<project>`. With `terminal: none`, `attach_cmd` is `tmux attach -t work \; select-window -t agent-<slug>`, and `switch_cmd` is `tmux switch-client -t '=work:=agent-<slug>'`, which switches session and window at once. The target is quoted because zsh expands a leading `=`.

To give every project its own shared session without setting `tmux_session` on each, put `tmux_session_per_project: true` at the top of `projects.yaml`. Each project without a `tmux_session` then uses one named after its slug.

## One terminal, a session per project

This recipe is for a machine with one terminal running tmux and no window manager to open terminals in, such as a Mac. Each project is one tmux session holding its lead and the lead's workers as windows. Switching session switches lead, and nothing ever opens a window.

```yaml
# projects.yaml
terminal: none                  # never open a terminal
tmux_session_per_project: true  # each project's agents share a session named after its slug
projects:
  - name: "Issue Viewer"
    slug: "issue-viewer"         # → tmux session "issue-viewer"
  - name: "Kestrel"
    slug: "kestrel"
```

A project can still set its own `terminal:` or `tmux_session:`; its own value wins.

```
┌──────────────────────────────────────────────────────────┐
│ lead-issue-viewer                                        │
│ > _                                                      │
│[issue-viewer] 0:lead-issue-viewer* 1:agent-a 2:agent-b   │
└──────────────────────────────────────────────────────────┘
```

1. **Start the lead.**
   - `issue-cli --project <slug> lead spawn` creates the session with `lead-<slug>` as its first window.
   - Or run claude yourself in a window of that session and rename the window `lead-<slug>` (`prefix ,`). `lead list`, the agents canvas and the polybar crown then see it as the live lead.
2. **Attach once:** `tmux attach -t <slug>` from the terminal. After that you stay inside tmux.
3. **Dispatch.** Each worker becomes an `agent-<slug>` window in the background. Its tab appears in the status bar, and the lead's window keeps the focus.
4. **Move around:**
   - `prefix n` / `prefix p` (or `prefix <number>`) moves between the lead and its workers.
   - `prefix s` picks another project's session, which means another lead and its workers.
5. **Reach a specific agent:** clicking it on the agents canvas selects its window. **Copy switch** in its panel copies `tmux switch-client -t '=<slug>:=agent-…'`; paste it anywhere inside tmux to jump there.

**See every project in the status bar.** `prefix s` lists the sessions on demand. To keep them in view all the time, add this to `~/.tmux.conf` (tmux 3.2 or newer):

```
set -g status-left-length 120
set -g status-left '#{S:#{?#{m:_*,#{session_name}},,#{?session_attached,#[fg=black#,bg=colour114#,bold],#[fg=colour250]} #{session_name}#{?#{e|>:#{session_windows},1}, +#{e|-:#{session_windows},1},}#{?session_alerts, !,} #[default]}}#[fg=colour240]│ #[default]'
```

```
 issue-viewer +2  kestrel +1 !  │ 0:lead-issue-viewer* 1:agent-a 2:agent-b
```

- Every session is listed (one per project, so one per lead); the one you're in is highlighted.
- `+N` counts its workers: the windows besides the lead.
- `!` marks a session where a window rang the bell or has an alert.
- Sessions whose names start with `_` are left out.
- Reload with `tmux source-file ~/.tmux.conf`.

The project lead and every worker, epic children included, live in the same project session, so the grouping is per project, not per epic.

## Lead terminal

`lead_terminal:` decides what opens when the project lead starts or is opened: `issue-cli lead spawn`, **Spawn lead**, and **Open** on the agents canvas. Workers (▶ and `issue-cli dispatch`) keep using `terminal:`, so you can see the lead and leave the workers headless.

| `lead_terminal:` | What happens |
|:-----------------|:-------------|
| unset or `none`  | Nothing opens. The response carries `switch_cmd` and `attach_cmd`, as with `terminal: none`. This holds even when `terminal:` is set. |
| `tmux-window`    | The lead becomes a tab in your own tmux session (below). |
| any other text   | A shell command, run like `terminal:` with the same placeholders (`{{role}}` is `lead`). |

`lead_terminal:` and `lead_window_session:` can go at the top of `projects.yaml` as defaults for every project, or on a project. `lead_window:` is per project only. `issue-cli lead spawn --headless` opens nothing whatever `lead_terminal` says.

### Leads as tabs in your own session

With `lead_terminal: tmux-window`, starting the lead adds one window to the tmux session you work in. That window runs a nested tmux client showing the lead.

```yaml
# projects.yaml
terminal: none                  # workers: never open or select anything
tmux_session_per_project: true  # each project's lead and workers share a session
lead_terminal: tmux-window      # leads: a tab in your session
lead_window_session: main       # the session you work in (required for tmux-window)
projects:
  - name: "Issue Viewer"
    slug: "issue-viewer"
    lead_window: 1              # optional: put this lead's tab at window 1
  - name: "Kestrel"
    slug: "kestrel"           # next free window
```

```
┌──────────────────────────────────────────────────────────────┐
│ ┌──────────────────────────────────────────────────────────┐ │
│ │ lead-issue-viewer                                        │ │
│ │ > _                                                      │ │
│ │[view-lead-issue-viewer] 0:lead-issue-viewer* 1:agent-a   │ │
│ └──────────────────────────────────────────────────────────┘ │
│[main] 0:shell 1:view-lead-issue-viewer* 2:view-lead-kestrel│
└──────────────────────────────────────────────────────────────┘
```

- **Detached.** The tab is created in the background. Your current window and your typing stay where they are.
- **One tab per lead.** The tab is named `view-lead-<project>` and tagged with the tmux option `@iv_lead_view`. Spawning or opening the lead again finds it and leaves it as it is.
- **Fixed slot.** `lead_window: <n>` puts the tab at index n. If n is taken, the tab goes to the next free index and the steps carry a warning naming both.
- **Pinned on the lead.** The nested client attaches to `view-lead-<project>`, a session grouped with the project session that selects the lead's window. A grouped session has its own current window, so a worker window selected in the project session doesn't move your tab. It is never listed as an agent.
- **Closing the tab is safe.** Kill the window, or detach the nested client with `prefix prefix d`. Either way only the view session goes (`destroy-unattached`). The lead keeps running in the project session.
- **Nested keys.** The outer tmux takes your prefix. Press it twice to send it to the nested client, e.g. `prefix prefix n`.
- **Misconfigured.** Without `lead_window_session`, or with a session that isn't running, nothing is created. The steps carry a warning naming what's missing, and the lead is headless with the attach commands. If `lead_window_session` names the lead's own project session, the lead window is already there and nothing is added.

**Don't move the lead's real pane** into your session with `join-pane` or `break-pane`. That empties the project session, tmux then closes it, and the lead dies. The tab is the safe way to keep a lead in view. To reach the lead without a tab, use `tmux switch-client -t '=<project>:=lead-<project>'` from inside tmux.

## Per-issue Worktrees

Set `worktree: true` at the top of `workflow.yaml` to give every dispatched issue its own git worktree:

```yaml
worktree: true
worktree_setup: "make"            # optional: runs once inside a new worktree
worktree_deps: shared             # the default: link the main checkout's node_modules; own: copy them (a clone), no links
worktree_sparse_exclude: ["issues/"]   # the default; [] keeps everything
```

- The worktree is `<workdir>/.worktrees/<slug>` on a new branch `work/<slug>`, created from the current `HEAD` of the project's workdir. An existing worktree is reused, so re-dispatching after a restart picks up where the agent left off.
- `issues/` is left out with sparse-checkout, so issues are only ever edited on the main checkout. Each `worktree_sparse_exclude` path is relative to the repo root (written as `!/issues/` in the pattern file), so a nested folder with the same name, such as `demo/issues/`, stays in the worktree. The dispatch prompt tells the agent it is in a worktree and that its `issue-cli` commands already point at the main checkout. Its Worktree section names the issues tree by absolute path and, for a department's issue, the `departments/<key>/` folder in the main checkout where department files such as brand.md and icp.md go; a path under `issues/` in the brief is rewritten to its absolute path.
- `worktree_setup` runs on creation only. If it fails, the dispatch stops and reports the error. A setup that would empty `node_modules` through a symlink (it links node_modules to the main checkout's, then runs `npm ci`) is refused before it runs, naming the fix (docs/workflow.md#how-a-run-happens).
- **Shared node_modules.** Every new worktree (dispatch, the viewer's dispatch, `worktree add`, the critic; `dispatch --fresh` into a tree that has none) links each `node_modules` folder the main checkout has (the root and workspace packages, up to 3 deep) when the worktree has the same folder with a `package.json` and the same lock file as the main checkout (`package-lock.json`, `pnpm-lock.yaml`, `yarn.lock`, `bun.lock`), before `worktree_setup` runs. A worktree with its own install wrote ~29k files each time, every one into fseventsd's journal (docs/fs-churn.md). It never replaces a real folder; a lock file that differs links nothing and says `not shared, install in the worktree`. When a `.gitignore` says `node_modules/` (folders only), `node_modules` goes into the repository's `info/exclude`, so the link leaves the tree clean and it can go after landing. `worktree_deps: own` makes no links: where it would link, the new worktree gets its own copy of the main checkout's folder instead (a clone on APFS, no download; an install in the worktree writes only into the copy), and with a `worktree_setup` it places nothing and leaves the install to it. A link or copy that fails is said in the step (`not made, install in the worktree`). `worktree add` then checks the tree: a `node_modules` the main checkout has (with a lock file) that the new tree still lacks makes it print `✗ no node_modules … in the worktree`, name the install command and exit non-zero, instead of printing the path as if the tree were ready (a worker's `npm run` then failed with `spawnSync node_modules/.bin/tsx ENOENT`). Before a worker adds, removes or updates a package it runs `issue-cli worktree deps --own`: each link becomes the worktree's own copy of the folder it points to (a clone on APFS, a reflink copy where Linux has them: no download, the main checkout's untouched), then the lock file's install runs over that copy; `worktree deps` shows each folder's state and `--shared` links again. No install runs through a link at any depth (D-8; on 2026-11-19 23:39 a workspace `npm ci` in a worktree emptied the main checkout's root and `packages/*` `node_modules`): before every command `ship`, local CI's `ci.run`, `prove --run` and `worktree_setup` run, `ci.PrepareInstall` looks for `node_modules` links anywhere in the tree (root and workspace packages, 3 deep). A whole-tree install (`npm ci`, `pnpm install`, `yarn`, `bun install`) is skipped while every linked folder's lock file and `package.json` are the main checkout's (`skipped: npm ci: node_modules is shared with the main checkout …`); any other install (a chain such as `npm ci && npm test`, a changed lock file, `npm install <pkg>`) first turns every link into the tree's own copy, said in a `before npm ci: … is its own now (a clone of …)` line. A command it can't read into (a `make` target that runs `npm ci`) is not caught: put the install in `ci.run` as its own command.
- An existing worktree that is gone (removed after a ship or by hand) is made again on its branch by `dispatch`, `dispatch --fresh` and the critic (`issue-cli critique`, not `--brief`). A stale record of a folder deleted by hand is pruned first (`git worktree prune`), so git checks the branch out again.

### Without worktree: true

A project that leaves `worktree:` off gets no worktree from dispatch, and its workers make their own. So that they
pick the name the engine looks for first, the briefing of a git project without `worktree: true` has a `## Branch`
section:

```text
## Branch

This project's workflow has no worktree: true, so dispatch made no worktree. Before your first code change, make the issue's own: it makes .worktrees/<slug> on work/<slug> from main, runs the project's worktree_setup there (node_modules and the like) and prints the path last:
  issue-cli --project <project> worktree add <slug>
- Then cd into that path and work there: docs and code under <workdir> are main's, not your branch's.
- prove, check-script and the commit-message hint look for work/<slug> first, then a worktree whose branch ends in the slug or whose HANDOFF.md names it.
- Already on another branch? Name it once: issue-cli set-meta <slug> --key branch --value <your-branch>
```

`issue-cli worktree add` is dispatch's own worktree step (sparse excludes, checkout, `worktree_setup` once) cut from the main branch: a worktree that exists is left as it is and says so, and a `work/<slug>` that exists with no folder is checked out as is. A slug git can't name a branch after (one with a space) and a folder outside git get no section. How each command
finds the branch: [Finding the issue's branch](Workflow/proof.md#finding-the-issues-branch). `issue-cli health lint`'s
`worktrees` check warns when such a project's open issues have worktrees under `.worktrees/` all the same, and
suggests `worktree: true`; the project may keep it off.

### Removing worktrees

A worktree is no use once its branch is on main, and hundreds of checked-out trees cost gigabytes and keep the file-system watcher busy (on 2026-11-15 this machine held 293 here, 16 GB, and 117 in another project, 30 GB, almost all merged). A worktree is removed only when every rule holds:

- its branch (`work/<slug>`) is on the main branch (`ci.branch`, else origin's HEAD, `main`, `master`, the main checkout's branch) by the one rule the queue uses too (`gitmain.WorkAt`): main contains its head and the head is past the issue's dispatch base. A branch still at its base has no commits of its own and is kept (`unmerged: work/<slug> has no commits past its dispatch base`), except at done, when nothing is left to land and the tree goes;
- `git status --porcelain` in it is empty: untracked files count, ignored ones (HANDOFF.md, a built `./issue-cli`) don't;
- no tmux pane's current path and no process's arguments are inside it;
- for a sweep, nothing at its top level and not its git dir's `HEAD` changed in the last 3 hours, for a branch whose dispatch base no stats record;
- it isn't special: `.ci` or another dot folder, a detached tree, a branch outside `work/`, a folder outside `.worktrees/`.

A release tree (`.worktrees/release-vX.Y` on `release/vX.Y`) has its own rules instead of the branch ones: its line is published (a `vX.Y.N` tag), or was never published while a newer line is (a line left for the next); it isn't the newest published line (`special: vX.Y is the newest published release: a patch may still come`); and its branch has no commits past its newest `vX.Y.N` (`unmerged: release/vX.Y has N commit(s) past vX.Y.N`, a pick or a candidate waiting). A line newer than the newest published one is kept (`special: release vX.Y is not published`). Then the live, idle and clean rules above. `release cut` adds the tree back for a later patch. Trees outside `.worktrees/` (a worker's scratchpad tree, a subagent's under `.claude/worktrees/`) are `issue-cli doctor leaks`' (getting-started.md, Troubleshooting).

Removal is `git worktree remove` (no `--force`, so git refuses a dirty tree too) and then `git worktree prune`. The branch always stays. Removal at done and at the tick also finds the tree a worker made with `issue-cli worktree add` in a project without `worktree: true` (`tracker.IssueWorktreeOnDisk`).

- **At done.** The transition into the last status closes the issue's worker session and then removes its worktree, saying `Worktree: removed <path>` or `Worktree: kept <path>: <why>`. When the worker runs that transition itself, its own pane is still in the tree (it closes when the turn ends), so the tree is kept as `live`.
- **The lead's tick.** Each `issue-cli lead tick` removes the worktree of an open issue whose branch is already on main, with commits since it was made (its reflog) and no live session for the issue, when it is clean and nothing is live inside (`agentrun.RemoveLandedTree`), so a landed tree goes at the next tick, not at the sweep's 3-hour mark. It also removes the worktree of every closed issue (done, parked or a side exit after done) whose branch is on main or has no commits of its own, quietly (this covers the worker's own done above, a parked issue whose session reclaim closed, and a done branch the lead lands later); a closed tree kept as dirty is said once, and a parked one with commits not on main keeps its tree. A done issue's branch with commits not on main is said as `land: <slug> is done but <branch> has N commit(s) not on main`, the branch being the one `checks.IssueBranch` finds (its `branch:` field, `work/<slug>`, or a worktree naming the slug). Once a local day it runs the sweep below and says `worktrees: removed N …` when any went. The day is in the tick state (`pruned_on`).
- **`issue-cli worktrees prune [--dry-run] [--json]`** sweeps one project (`--project P`) with all the rules: it lists each tree it removes and each it keeps with why (`dirty`, `unmerged`, `live`, `recent`, `special`), then `Removed N worktrees; kept …`. `--dry-run` removes nothing; workers may only dry-run.

The Stats page's Disk use card shows each project's worktree count and size (`storage.worktrees`, `storage.worktrees_mb` in the storage health family; local CI's `.worktrees/.ci` is `storage.ci_worktree_mb`, on its own). The rules are `agentrun.Judge` (internal/agentrun/prune.go).

Epics and their children get worktrees like any other issue: `work/<slug>` from `HEAD`. An old `base:` on an epic is ignored. See [The lead session](patterns/lead-session.md#epics).

## Auto-start

Auto-start is your standing start for one project: while memory is low and the CPU has room, the lead starts one more worker at a time from the project's own list, without you naming each one. It's off for every project until you turn it on.

```bash
issue-cli autostart on --quote "start a new job when memory is under 50%" [--below 50] [--max-load <cores>] [--max-workers 2] [--gap 5]
issue-cli autostart status      # on / paused / off, your words, the limits, the last start, the verdict now
issue-cli autostart next        # start <slug> and the command to run, or hold: <reason>
issue-cli dispatch <slug> --autostart
issue-cli autostart pause [--reason "..."] | resume | off [--reason "..."]
issue-cli autostart weight <n> --quote "<your words>"   # start_weight: in projects.yaml
```

- **Your words.** `autostart on` stores them with who relayed them and when, in `.autostart/<project>.json` in the main checkout (next to `.yolo/`, never committed). Only you, or the lead with your words, can turn it on, pause, resume or turn it off. Workers are refused. `off` logs the window in `.autostart/<project>.log.jsonl`.
- **The check.** `autostart next` holds, with the reason, when:
  - auto-start is off or paused
  - `mem-guard` isn't on `PATH`, or `mem-guard check` refuses (the hard stop)
  - `mem-guard used` is at or above `--below` (default 50%)
  - both the 1-minute and the 5-minute load average are at or above `--max-load` (default the core count): `hold: load 16.0/14.2 on 14 cores (max 14)`, the 1-minute average first. The 1-minute one alone doesn't hold: one `make validate` pushes it over the cores for a minute or two while the 5-minute one stays under, and the machine is fine. Both are read from `sysctl -n vm.loadavg` on macOS and `/proc/loadavg` on Linux (the first two numbers); the cores are `runtime.NumCPU()`. Where there's no reading, load doesn't hold and the verdict says `load unknown` (mem-guard stays the hard stop)
  - the project has `--max-workers` live `agent-*` sessions (default 2; the lead doesn't count, nor does a worker whose [report waits on your review](#a-report-waiting-on-your-review) with its output on the shelf)
  - any project on this machine auto-started a worker less than `--gap` minutes ago (default 5)
  - it is another project's turn: `hold: atlas' turn (weight 3; dark-factory next in 2 starts)` (see [The turn between projects](#the-turn-between-projects))
  - nothing in the list is ready
- **The list.** Unclaimed `backlog` issues of the project's version, by priority then file order, the same order as `issue-cli next`. An issue is skipped while its worker is live, a `depends_on` issue isn't done, or it only waits on your approval (unless YOLO is on, which passes that gate).
- **The start.** `dispatch <slug> --autostart` takes a lock shared by every project on the machine (`$XDG_STATE_HOME/ai-workflow-engine/autostart/`), decides again for that issue, starts it like any dispatch and records the start. Its `dispatch:` comment quotes your stored words: `dispatch: "<your words>" (auto-start, turned on by lead-x in the lead chat 2026-10-30; memory 31% < 50%, load 4.3/5.0 on 14 cores (max 14), 1/2 workers, 2026-10-30)`. When anything holds, it exits 1 and starts nothing.
- **Who ticks.** The lead, at its check-ins. Its brief carries the rule while auto-start is on, and `issue-cli lead whoami` prints it. `autostart next` also prints `⚠ job <dept>/<job> is due (<period>) on its standing start: issue-cli job run <dept>/<job> --scheduled` for each recurring job its standing start lets start now. The tool still runs nothing on a timer, and the viewer never starts a worker for it.
- **Where it shows.** An Auto-start card on the Report (`/p/<project>/status`) with your words, the verdict now, the load and cores (`CPU: load 4.3/5.0 on 14 cores (max 14)`), the last start on this machine and the share between projects, plus **Pause** / **Resume** / **Turn off** buttons (`POST /p/<project>/autostart/pause|resume|off`). The sidebar shows an Auto-start line while it's on, and `status.md` / `issue-cli status` print `Auto-start: …`, ending with the share.

### The factory runs 24/7

The factory and its agents work around the clock: no night may pass with room to work and nothing started. The lead's tick (`issue-cli lead tick --watch --exit-on-news`, [The tick](patterns/lead-session.md#the-tick)) checks it in every project, its department and group leads too, with no setup:

- **Idle is news.** When the project has started nothing for `leads.idle_minutes` (workflow.yaml, default 60) while it has room, the tick wakes the lead: `idle: nothing started for 1h05m (since 23:00) with room to start (the factory runs 24/7): <why>: <what to do>`, and again each such stretch it stays so (at 2h, 3h, …). Room means the start was held by the project itself: the queue is empty (`file work and queue it`), every open item is held (the hold, `queue list`), auto-start is off or paused (start one by hand, or ask the owner), or the worker cap is reached by sessions sitting idle at their prompt (named, with `dispatch <slug>` to look). It is not idle while the machine holds (memory, mem-guard, the load), another project has the gap or the turn, or busy workers fill the cap. A negative `idle_minutes` turns it off.
- **A report's follow-ups are news.** Any worker's `report:` comment on an open issue in the lead's scope is said once: `report: <slug>'s report has N follow-ups to file: …`, with `create` and `queue add`. A report output in review on the shelf with no `report:` after it is said once too.
- **An idle session waiting on you frees its slot.** A [report waiting on your review](#a-report-waiting-on-your-review), at its gate or before it with an owner box open, holds no slot and the tick closes it. Under the worker cap, as under a memory hold, the tick also closes idle sessions whose issue is closed or waits on your approval or a `question:` comment (`freed under the worker cap: closed …`) and asks the queue again.
- **An empty queue sweeps itself.** With YOLO and auto-start on, the queue at its refill line gets a retro sweep queued by the tick ([The lead's queue](#the-leads-queue)); while it stays empty, the tick queues another once the last one closed, up to 4 a day, so a night with no lead turn still finds new work. Each starts on auto-start's words, never on its own.

- **Under YOLO nothing waits at a gate YOLO passes.** A worked issue (it has an assignee) whose worker session is gone and that `transition --dry-run` finds Ready at its next gate YOLO passes is moved on by the tick: `moved: <slug> human-testing → done (YOLO passed the gate; its worker has gone; the owner reviews it after the fact)`, a held line (cli/under-yolo-transition-human-testing-documentation-dry-run-says-ready-the#D-2). Never into the work status (nobody would work it), and never through a gate that stays yours in YOLO: an experiment's, an `owner_only` one, a design-first design gate, a gate up to the build while an `(owner)` box due before it is open, release publish. On 2026-11-21 eleven finished reports sat at human-testing for up to 4 days this way.

2026-11-20: two report workers finished by 22:20, the queue was empty and their idle sessions held both slots; nothing started until 07:49, and the tick said how long nothing had started only then, with the next real news.

### The turn between projects

The gap is one start every 5 minutes for the whole machine. Without a turn, whichever lead ticks first after it opens takes it, so a project whose lead ticks often takes nearly every start. With a turn, the starts go by each project's `start_weight:` in projects.yaml: a whole number, at least 1, and 1 when unset.

```yaml
projects:
  - name: Atlas
    slug: atlas
    start_weight: 3   # 3 of every 4 starts while both wait
```

- **Who takes part.** Every `autostart next`, `dispatch --autostart` and `queue next [--start]` a lead runs records the project's ask in `$XDG_STATE_HOME/ai-workflow-engine/autostart/share.json`, under the budget's lock. A project is **waiting** when it has a ready item and only the gap, the machine's memory or load, or the turn holds it. A project held by its own limits (paused, its worker cap, nothing ready) is not waiting. When the gap opens, the turn is taken among this project and every other one whose last ask was waiting, is less than 2 of its gaps old, and whose own memory and load lines the reading is under (otherwise it couldn't start now, so it holds no one up).
- **The order.** A smooth weighted round robin: each contender has a credit, the one with the most credit plus weight goes (ties go to the higher weight, then the name), and on its start every contender gains its weight while the starter gives up their total. With weights 3 and 1 that is atlas, atlas, dark-factory, atlas, then the same again. No waiting project waits more than the sum of the weights in starts.
- **Nothing saved up.** A project that stops asking for 2 gaps drops out and loses its credit, and credits are re-centred to sum zero whenever the turn is computed. One that comes back after an idle hour gets its share from then on, not the starts it missed.
- **The hold.** The project whose turn it isn't holds with `hold: atlas' turn (weight 3; dark-factory next in 2 starts)`, which names the holder, its weight and how many starts go before this project.
- **Where it shows.** `autostart status`, the Auto-start and Queue cards and status.md's Auto-start line show `share of the start gap (24h, by start_weight:): atlas ×3 6 starts · dark-factory ×1 2 starts`, or the project's weight alone while no other project waited. The viewer only reads the share; a page load is never an ask.
- **Setting it.** `issue-cli autostart weight 3 --quote "<your words>"` writes the one `start_weight:` line into the project's entry, keeping every other byte of projects.yaml. You can also edit the file by hand. Workers are refused. The CLI uses the new weight from the next ask on; the viewer shows it after its next restart.
- **What takes no turn.** A takeover (`dispatch --takeover`), a fresh session (`dispatch --fresh`) and a job run (`job run --scheduled`) continue or run work that is already planned. They still use the gap but don't wait for a turn.

## The lead's queue

The lead's queue is the project's ordered plan: what the lead's tick starts next while memory and CPU allow. It is the engine's version of the hand-kept night plan (numbered items, `[>] <slug>` once started, `[x] <sha>` once shipped, `blocked:`, and "think up more" kept last). Source: factory/the-leads-queue-as-an-engine-feature-an-ordered-plan-of-items-the-leads-tick-starts-while-memory-and-cpu-allow (D-1 to D-6, provisional).

```bash
issue-cli queue add <slug> --quote "<your words>" [--how ...] [--top|--before N|--after N] [--blocked-on <item|slug> [--merged]]
issue-cli queue add --idea "<one line to file>" --quote "<your words>" [--goal <key>]
issue-cli queue list [--all]          # active items with their holds, done today, the refill line
issue-cli queue move N --top|--bottom|--before M|--after M
issue-cli queue block N --on <item|slug> [--merged]   /   unblock N [--on ...]
issue-cli queue link N <slug>         # the idea's issue was filed
issue-cli queue quote N --quote "<your words>"
issue-cli queue drop N [--reason ...] /   done N [--sha ...]   # by hand: no-code items
issue-cli queue limits [--below 50] [--max-workers 0]
issue-cli queue next [--start]        # what starts next, or why nothing does
```

- **The file.** `<issues>/.queue/queue.json`, one per project, with the work files (a private tracker keeps it private). Written atomically under a lock (`.queue/queue.lock`), so the lead and a worker's `transition` can write at once. `<item>` is the item's number (`12`, `#12`) or its slug.
- **"Think up more" is always last.** It isn't stored: every list ends with `∞ Out of work: think up more…`, so it can't be done, moved or dropped. When nothing open is left, `queue next` prints it with the `queue add` command, and the goals and epics nothing serves after `look first at:` ([Work behind a goal](departments.md#work-behind-a-goal)).
- **Your words.** An item starts only on your words: its own `--quote`, or auto-start's stored words while [auto-start](#auto-start) is on and not paused. An item with neither holds (`no owner's words: issue-cli queue quote N --quote …`). Never an agent's words. The started worker's `dispatch:` comment quotes them: `dispatch: "<your words>" (queue item 2, in the CEO chat; memory 31% < 50%, load 4.0/4.6 on 14 cores (max 14), 0 workers, no worker cap, 2026-10-31)`.
- **What holds an item**, in order: a queue blocker not done (an item number done or dropped, or a slug at its workflow's last status; a `--merged` blocker only until its work is on main, below); an idea with no issue yet (`file it, then queue link N <slug>`); no issue with that slug; the issue already shipped or parked; already started (`started, worker live`, or `started, no live worker`: it is never restarted by itself, re-dispatch by hand); a live worker, a `depends_on` not done, or waiting on your approval (YOLO lets that through), as for auto-start; routing resting; no words.
- **Waiting for the code, not the done: `--merged`.** A blocker normally waits for its issue's last status, which can wait on your approval long after its code is on main. `queue block N --on <slug> --merged` (or `queue add … --blocked-on <slug> --merged`) makes the item wait only until the blocker's work is on main: the `merge_sha` its stats recorded, else the tip of `work/<slug>`, or a branch head recorded at an earlier transition, that the main branch (`ci.branch`, else origin's HEAD, `main`, `master`, the main checkout's branch) contains. A commit that is the dispatch's recorded base doesn't count: a branch cut with no commits of its own is on main trivially. The same rule (`gitmain.WorkAt`) decides `queue done`'s SHA, the worktree prune and the lead tick: an empty branch gets no done SHA and no `not on main` note. `queue list` shows `waits for <slug> on main` until then and `cleared: <slug> on main at <sha>` after; a blocker that reaches its last status first clears as always. An item-number blocker (`#N`) uses that item's issue; an idea item has no branch and waits for its done. Blocking again without `--merged` drops the mark; without the flag nothing changes. A plain blocker whose work is already on main says so in its hold, with the command: `blocked on <slug> (its work is on main at <sha>: issue-cli queue block N --on <slug> --merged to start on that)`. In the file the marked blockers are listed again under `blocked_on_merged`. One case it can't tell: a branch fast-forwarded to main before it had commits of its own reads as on main. Source: factory/a-queue-item-can-wait-for-its-blockers-branch-on-main-instead-of-its-done-queue-block-merged.
- **The machine checks** are auto-start's (`Decide`): `mem-guard` found and `mem-guard check` passing, memory under the limit, the load not over the core count on both its 1- and 5-minute averages, the worker cap, and one start per 5 minutes across all projects (the same lock and last start as `dispatch --autostart`). Plus: **local CI red on main holds every start** (`local CI red on main at abc1234: no new starts until it is green`); no `ci:` block doesn't hold. Limits are auto-start's when it is on; otherwise the queue's own, `queue limits`: memory under 50% and **no worker cap** by default (your words, 2026-10-31: "don't limit yourself on the 5 individual workers - just on the memory you have").
- **What a memory hold could free.** On a memory hold `queue next` and `queue next --start` list each idle worker session that holds memory for nothing: `could free: agent-x (human-testing, waits on the owner's approval for documentation, idle since 14:05)`. A session counts when every issue naming it is closed (done, even with its branch not on main yet) or waits on the owner (a ready gate, or a last comment starting `question:`), its agent made no issue-cli call for 15 minutes, is not in a turn, is not paused by mem-guard and nobody is attached. `--json` has them under `could_free`, and the hold's kind under `hold_kind` (`memory` or `mem-guard` for a memory hold; `load`, `workers`, `gap`, `turn`, `paused`, `mem-guard-missing`, `off`, `nothing-ready` otherwise): the tick reads that kind, not the hold's words. `issue-cli lead tick` closes them and asks again; an issue still in progress is never closed, and the worktree and branch stay.
- **Start.** `queue next --start` decides again under the lock, starts the first startable item like `dispatch`, marks it `[>]` (started, by whom, when) and prints the next item. When anything holds it exits 1 and starts nothing, listing why each item before it can't start. One run reads the machine once (tmux's session list, each pane, mem-guard, the load, the main checkout; has-session comes from the list): the decision under the lock reads it afresh, and a start watches its new pane afresh.
- **Done.** `transition --to <last status>` marks the issue's items done with the merge SHA: the tip of `work/<slug>` when the project's main branch (`ci.branch`, else the branch the main checkout has out) contains it (`✓ Queue item 4 marked done at becb563`). A tip that isn't on it gets no SHA and the note `not on main`: `queue` prints `done (not on main)`, and so does the Report's Queue card. With no work branch at all the SHA is the main branch's head. An item whose issue reached its last status another way (close, a lead's move) shows as done without a SHA. A done item with no SHA gets one once its branch lands: `queue`, `queue next` and every queue change check `work/<slug>` again, and when the main branch now contains its tip they record that tip and drop the `not on main` note. `queue done N` by hand does the same and says so (`✓ Item 220 done at 23d4e8f: work/<slug> is on main`); while the tip is still ahead of main it marks the item done with no SHA and prints `isn't on main yet` instead of ✓. An item with no work branch keeps no SHA: main's head says nothing about it.
- **Who.** The lead (or you); workers may read the queue but never change it or start from it. Nothing runs on a timer: the lead's check-in, `issue-cli lead tick` ([The tick](patterns/lead-session.md#the-tick)), runs `queue next --start`. `issue-cli lead whoami` prints `Queue: N active, next <slug>. At each check-in: issue-cli lead tick (it runs queue next --start)`, and `dispatch` without `--quote` points at `queue next` when the project has a queue.
- **What waits on the lead.** `queue next` and `queue next --start` first print `Waits on you (N):`: each open issue's criterion linked to a command only the lead can run (`prove <slug> <AC> --lead "<command>"`) that has no passing run, with `issue-cli prove <slug> --lead-run` to run it. `--json` has it as `waits_on_lead`. See [Proof that waits on the lead](Workflow/proof.md#proof-that-waits-on-the-lead).
- **The queue-low sweep.** At the refill line (5 open items or fewer) with YOLO and auto-start both on, `issue-cli lead tick` files a run of the bundled `builtin:retro-sweep` brief and queues it with no quote, so auto-start's words start it. It says so on its `queue:` line (`queued sweep <slug> as item N`), queues at most one a day and none while one is open, and turns on no job's standing start. The sweep proposes; the lead files its proposals, and the tick says its `report:` comment as news when it lands. See [The Lead Session](patterns/lead-session.md).
- **Where it shows.** A Queue card on the Report (`/p/<project>/status`): the next 5 items with their holds, the blocked count, done today with SHAs, the refill line and what `queue next` says now. `status.md` / `issue-cli status` print `Queue: 3 active · 1 blocked · 2 done today · next <slug> · now: …`. The viewer only reads: it never writes the queue or starts a worker.

- **Your pause.** The Crew page across projects (`/crew`) has Pause and Resume for each project's queue (owner-only). A paused queue holds `queue next` before anything else, even items with their own quote; `queue list` and `queue next` print `hold: the queue is paused by the owner (Crew page) at <time>: resume it on the viewer's /crew page`. issue-cli has no resume: an agent can't lift your pause.

**Not yet:** editing the queue from the page; importing a markdown night plan; a queue per department.

## A stalled start

Nobody watches a worker's pane, and a dialog or a prompt stops the work without changing the issue. On 2026-11-05 the first three workers routed to Codex sat at its "Trust this folder?" dialog for half an hour, so:

- **A codex start trusts exactly its own folder, for that run.** Every interactive start through `agentrun.Start` (dispatch, ▶, auto-start, lead spawn, job runs, post-mortems) passes the folder it runs in as an inline `-c projects={…}` table. Codex splits a `-c` key on every dot and keeps the quotes, so the dotted form `-c projects."<path>".trust_level="trusted"` leaves the dialog up. `~/.codex/config.toml` is not written, and no sandbox or approval setting changes. Codex shows one startup warning for it: a run with `-c` works without its shared background server.
- **It skips the update prompt.** That prompt stops a start the same way, and its first choice runs `curl … | sh`. Update Codex yourself with `codex update`.
- **The critic is left as it is.** `codex exec` shows no dialog, and trusting a branch's folder would load its folder settings into a read-only critic.
- **A pane still at a startup dialog a minute after its launch is reported.** `stalled at Codex's trust dialog` (or `Codex's update prompt`, `Claude's trust dialog`) shows in `issue-cli agents`, as a `⚠` line in `queue next` and `autostart next` (the commands a lead's tick runs), on the Crew card, in status.md's agent column and as the phone's Worker stuck push. Nothing answers the dialog for it: attach and choose, or stop the session.
- **A worker at a prompt is reported the same way.** `stalled at Codex's approval prompt` (or `Claude's approval prompt`, `Claude's question`) shows on the same surfaces, for workers only: a lead's prompt is in the owner's chat. A prompt is known by the pane's last line and its first choice together, so an agent printing one is not at one. It has no age of its own, as tmux's activity times keep moving while a CLI sits at a prompt, so it shows from the worker's first minute on.
- **A worker with no issue-cli call is reported.** Five minutes after its launch (`FirstCallLimit`), a worker with no call for this project since that launch reads `no issue-cli call in the 7 m since launch`, whatever stopped it, named here or not. A worker's first step is `issue-cli start`, so this is the net under every kind of failed start. A re-dispatch keeps its session name and clilog, so a call from the earlier launch doesn't count. A slow first turn can trip it too: the line says only that no call came, and asks you to look.

These reports read panes and clilogs, and send nothing to a pane.

## Routing between Claude and Codex

With both Claude Code and Codex logged in on this machine, a `routing:` block in the workflow file picks which one starts each worker, lead and job run, and moves new starts to the other one while a subscription is resting after a usage limit.

```yaml
routing:
  default: claude            # when no rule matches (no routing: block: claude, as before)
  fallback: [claude, codex]  # the order to try when the picked one is resting
  stop_at: {claude: 98, codex: 95}  # percent of the weekly limit; unset means 98, 100 is off
  rules:                     # first match wins; a rule matches when every key it names matches
    - {type: docs, agent: codex}
    - {type: report, agent: codex}
    - {label: design, agent: claude}
    - {status: in design, agent: claude}     # the issue's current status
    - {department: marketing, agent: codex}  # a box key
    - {lead: true, agent: claude}            # the CEO and department leads
```

- **Where it applies.** `issue-cli dispatch`, ▶ on the issue page (**Dispatch · codex**, with the rule as its tooltip), `dispatch --autostart` and auto-start, `issue-cli lead spawn` and the Crew page's Start buttons, and `job run` (scheduled or not). Each launch report starts with `Agent: codex (type docs → codex)`. A department's own workflow file routes its own work.
- **`--agent` wins.** `--agent claude|codex` on dispatch, lead spawn and job run, an action's `agent:` and the page's **Claude** / **Codex** buttons are your choice. Routing doesn't change them. When that subscription is resting the launch says so (a warning step) and starts it anyway.
- **Limits.** Before each routed start, when the Crew or Status page loads and on `issue-cli agents`, the last lines of every live agent and lead pane in the project are read for the CLI's own limit message:
  - Claude Code: `You've hit your 5-hour limit · resets 5pm (Europe/Warsaw)` (also the weekly, Opus, Sonnet and Fable limits, and the older `Claude usage limit reached … reset at 5pm`).
  - Codex: `You’ve hit your usage limit. … try again at 6:30 PM.` (or a date).
  That subscription then rests until the reset it names, or one hour when it names none. The rest is machine-wide (`$XDG_STATE_HOME/ai-workflow-engine/subscriptions/state.json`, under a lock) and ends by itself. The same message still on a pane after the reset doesn't start a new rest. Spend caps and usage credits are paid extras, not the plan, so they don't count.
- **Stop points.** The same scan reads each CLI's weekly usage line, and an agent at or over its `stop_at:` rests until the line's reset, like one at its limit:
  - Claude Code: `You've used 98% of your weekly limit · resets Nov 5 at 8am (Europe/Warsaw)` (or `Over 98% of your weekly limit used`). It prints 99% at most, so 100 turns the stop point off.
  - Codex: `Heads up, you have less than 5% of your weekly limit left.` That reads as 95% used. It is Codex's last warning and names no reset, so at the default 98 Codex rests only on its limit message; a `stop_at:` of 95 or lower rests it for an hour from the line.
  Only the weekly limit counts: a 5-hour window resets within hours, and the Opus and Sonnet limits leave the other models free (their limit messages still rest the subscription). The line must start its row, and a pane running the other agent is skipped, so a quoted line rests nothing. The week's highest percent stands, whichever pane shows less, and a line whose reset has passed is ignored. `stop_at:` is read from the project's `workflow.yaml`, never a department's. A rest is machine-wide, so with two projects on different stop points the lower one rests the agent for both. A running worker is left alone, and the factory stops starting work only when every agent to try is at its stop point or its limit.
- **While one rests.** A routed start takes the next agent in `fallback:` that is installed and not resting. When none is free, auto-start and scheduled job runs hold with the reason (`every agent to try is resting (claude until 17:00, codex until 18:30)`, or `claude until Thu 08:00 at its stop point 98%`). A start you asked for goes ahead on the routed agent, with a warning.
- **The worker that hit the limit** is left running: nothing is killed, typed into or retried. Its Crew card says `hit its codex usage limit · left running` with the message and the reset. `status.md` lists it on its Working on line, and `agents.json` has its `limit`. Under a `routing:` block the other agent can take its branch over ([below](#takeover-at-a-usage-limit)).
- **Where it shows.** Each live Crew card names its agent CLI, and the Crew header shows **Subscriptions**: `claude working · 92% of the weekly limit · codex resting until 18:30 (limit seen in agent-x)`, `unknown` or `not installed`, with the percent once a CLI printed it. `status.md` has an `Agents:` line. `issue-cli agents` prints the same with each stop point, the panes showing a limit now and the rules; `--json` has `percent` and `stop_at`. HQ's Spend block and `issue-cli spend` split the spend by subscription ([Departments](departments.md#spend-and-budgets)).
- **By hand.** `issue-cli agents rest <agent> --until HH:MM` (you saw the limit yourself) and `issue-cli agents clear <agent>` (a wrong match, or an early reset). A cleared stop point stays cleared until a higher percent shows. Only you or a lead can run them; a worker is refused.
- **What it never does.** It never uses API keys, creates accounts, logs in, reads either CLI's login, or shares a subscription. It doesn't work around a provider's limits: it only chooses which of your installed tools starts the next task.
- **Lint.** The `routing` check (`issue-cli health lint`) warns about an agent that isn't a command name or isn't on `PATH`; a type, status or department the file or projects.yaml doesn't know; a rule with no `agent:`; a rule after a catch-all; a `fallback:` that repeats an agent or leaves out the default; a `stop_at:` that isn't a percent from 1 to 100 or sits in a department's file; and an unknown `codex:` value. It also flags a department file that routes another department's work.
- **Judging the rules.** `issue-cli bench run <benchmark> --agents claude,codex` runs the same golden tasks on both CLIs, and `bench compare <benchmark>/latest` prints pass rate, time, tokens and cost by agent and type with a `Routing:` line naming each rule the numbers support or contradict ([Both agents](benchmark.md#both-agents)). The Report's Benchmark card shows the same.
- **The model per type of work.** A rule can name the model its starts run on, passed as `--model` (the [model per type](#the-model-per-type-of-work) below). Claude workers on `docs` and `report` issues start on `claude-sonnet-5-5` by default.
- **claude-code-router (CCR).** CCR is a local proxy that routes Claude Code's model requests to other providers using API keys. The engine doesn't bundle, install or call it (factory/route-work-between-claude-and-codex-subscriptions-ccr-pick-the-agent-per-task-fall-back-when-one-hits-its-limit#D-1). If you set CCR up yourself inside a Claude Code session, routing still picks only the CLI.

### The model per type of work

A rule's `model:` names the model the starts it matches run on: the launch passes `--model <name>` to that rule's agent. With none, a start runs on the project's own model ([Agent Model](#agent-model)), except the built-in default below. Source: the owner, 2026-11-15 ("Zdecydowanie domyślnie prostszy model dla docs i raportów": docs and reports on a simpler model by default).

```yaml
routing:
  rules:
    - {type: docs, agent: claude, model: claude-sonnet-5-5}    # written out; it is the default anyway
    - {type: tweak, agent: claude, model: claude-sonnet-5-5}   # switch a type on, once the bench says it holds
    - {type: report, agent: claude, model: default}            # keep the project's model for reports
```

- **The default.** With no rule naming a model, Claude workers on `docs` and `report` issues start on `claude-sonnet-5-5`; every other type and every lead keeps the project's model. A job run counts as its job's `type:`, so a report job (the growth scout) starts on Sonnet too. This holds in every project, with no `routing:` block at all.
- **Which rule decides.** The first rule that matches the start and names the agent that is starting. `model: <name>` runs it on that model, `model: default` on the project's own (`agent_models`, else the CLI's), and a rule with no `model:` leaves the default above in force. A rule's model wins over `agent_models` for what it matches. A model name is one CLI's: a docs start that falls back from claude to codex gets no Claude model, and `--agent claude` on a docs issue still gets the docs model (`--agent` picks the CLI, not the model).
- **Where it shows.** The launch report's first line names it: `Agent: claude · claude-sonnet-5-5 (type docs → claude; default for docs)`. The issue's stats sidecar records it on the dispatch (`"model"`), and `issue-cli stats --by-model` and the Stats page's **By model** table compare issues, reopen rate, red rounds and tokens by type and model ([Workflow Stats](workflow-stats.md#by-model)).
- **Before you switch a type.** Run the golden tasks on the model first and put the run beside one on the project's model: `issue-cli bench run <benchmark> --model claude-sonnet-5-5`, then `issue-cli bench compare <benchmark>/<run on the project model> <benchmark>/<run on sonnet>` ([Benchmark](benchmark.md#another-model)). A type whose pass rate holds goes on with a rule like the `tweak` one above. To switch the docs or reports default off, add `model: default` on their rule.
- **Lint.** The `routing` check names a `model:` that is not a plain model name (letters, digits and `._:/[]-`); such a model is never passed and the launch says so in a warning step.

### Takeover at a usage limit

When a worker stops at its usage limit, the other agent takes its branch over from a note the worker keeps. Source: factory/two-agents-and-a-short-lived-lead-routing-on-benchmarks-cross-model-critique-handover-on-limits-memory-as-the-one-source-a-nightly-lead-flush#D-3.

- **The note.** The worker briefing asks for `HANDOFF.md` at the worktree's root on every issue, kept current at each step: what's done, the next step, open questions. `transition` reminds a worker of it. A type of work that builds nothing (its path ships in its work status or the owner's test: report, chore, epic) has no worktree to keep it in: its briefing and the reminder ask for a `progress:` comment at each step instead, and the next agent reads the issue trail. It is never committed: `agentrun.Start` adds it to the repository's `info/exclude`. A [fresh session per stage](workflow.md#fresh-session-per-stage) starts from the same file.
- **Who starts it.** The lead, at its check-in. `issue-cli queue next`, `autostart next` and `agents` print one line per stopped worker: `issue-cli dispatch <slug> --takeover` when it may run, the reason when it may not. Nothing runs on a timer, a worker is refused, and it works with auto-start off.
- **What counts as stopped.** The pane of `agent-<slug>` on an open issue shows its own CLI's limit, is not in a turn, and doesn't say `continuing automatically`. Anything less would put two writers in one worktree. An issue that only waits on your approval gets no new worker, outside YOLO.
- **The start.** It quotes the issue's newest `dispatch:` words, so you add none, and leaves `dispatch: "<words>" (takeover from claude at its usage limit, <date>)`. The other agent must be installed and not resting. `mem-guard check` stays the hard stop, and it shares the one start per 5 minutes across the machine, so a whole subscription stopping at once becomes one takeover per check-in. The worker cap doesn't apply: the count stays the same.
- **The sessions.** The limited session is renamed `limited-<slug>` (no keys are sent, and a failed launch renames it back) and the other agent starts as `agent-<slug>` in the same worktree. The prompt opens with `## You are taking this branch over`, naming `HANDOFF.md` and the branch. So nudges, the Crew card, the assignee and the critic's dispatch record all follow the working agent, and nothing reaches the old session. The old session keeps its memory until someone closes it: `agents` lists `limited-*` sessions, and the tick prints the command to close one once its issue is closed. Claude Code's own `-n` name stays on the old session, so `ListAgents` still shows `agent-<slug>` there while the new one is a Codex.
- **Once.** One takeover per issue: the comment or a live `limited-<slug>` refuses a second, and a limit after that prints `taken over once already: no third worker; the owner decides`. A lead or job run has no takeover.

### Codex's sandbox

A Codex start runs in Codex's `workspace-write` sandbox, which covers its worktree. `issue-cli` writes the project's issue files and git writes the shared git dir, both outside it, so every such call stops at an approval prompt that nobody is watching. `routing: codex:` lets you choose what a start gets. Nothing is set by default, and with no block a start's command line is exactly what it was. A department's own file can carry its own block; one without it follows `workflow.yaml`.

```yaml
routing:
  codex:
    writable: [issues, git]  # extra folders a Codex start may write; default none
    approval: ask            # ask (default) | auto-review | never
```

Each launch report shows the flags as a `Codex sandbox (routing: codex:)` step. The launcher builds them from these words only, and any of them also adds `--sandbox workspace-write`, so the setting doesn't depend on your own `~/.codex/config.toml` (a permission profile set there gives way for that run). It never writes that file, never adds `--dangerously-bypass-approvals-and-sandbox` and never opens the network.

| Value | Codex flags | Risk |
| --- | --- | --- |
| `writable: issues` | `--add-dir <issue dir>` | The agent's commands can edit any issue file directly, not only through `issue-cli`, so the worker guard and owner-only checks no longer stand between it and a status, a gate marker or an owner box. A private tracker repo's `.git` inside this folder stays read-only, so its commits still stop. |
| `writable: git` | `--add-dir <common dir>`, and in a worktree `--add-dir <its git dir>` | The agent's commands can write every branch and tag, `.git/config` and `.git/hooks`. A hook or config line it writes runs later, outside the sandbox, the next time anyone uses git in that repository. |
| `approval: ask` | none | Today's behaviour: a request outside the sandbox waits at the pane for a person. Safest, but a worker nobody watches stalls until someone looks ([reported](#a-stalled-start), never answered). |
| `approval: auto-review` | `--ask-for-approval on-request -c approvals_reviewer="auto_review"` | A separate reviewer agent decides each request instead of you. It spends tokens, can be wrong and may not be available on your account. Codex calls it no deterministic guarantee, and it does not widen the sandbox. |
| `approval: never` | `--ask-for-approval never` | Nothing is reviewed and nothing stalls: a blocked action fails back to the agent, which may stop or look for another way. The sandbox stays, but no person or reviewer sees those attempts. |

- **Why `git` is two paths in a worktree.** Codex keeps the dir a worktree's `.git` file points at read-only, so with the common dir alone a commit still fails at `index.lock`. Naming that dir opens it. It sits inside the common dir, so it adds no reach. `TestRealCodexSandboxFlags` pins this, with the real CLI and no model call.
- **What this doesn't cover.** Tried on codex-cli 0.163.0. The network stays closed, so `git push`, `gh` and module downloads don't work inside the sandbox. `~/.local/state` stays read-only, so anything written there fails or asks. These flags clear the writes named above. They have not been proven on a full worker run, so try one watched run before turning Codex routing back on.
- **Lint.** The `routing` check names an unknown `writable:` or `approval:` value and lists the allowed ones. A start skips an unknown value, so it adds nothing.

## Lead Sessions

There is one lead per project. It runs in tmux like a worker, as `lead-<project>`, in the project checkout. Start it with `issue-cli lead spawn`, or the **Spawn lead** button in the sidebar's Actions or on the agents canvas (`POST /p/<project>/lead/spawn`, body `{}`). A live lead is re-attached, never started twice.

- Leads use the same launcher, `tmux_session` and `agent_model_source` settings as dispatched workers. What opens for them is [`lead_terminal:`](#lead-terminal), not `terminal:`.
- Its brief is the built-in one plus `leads.project_addendum` in `workflow.yaml`.
- A `lead-*` session is never counted as an active agent on an issue, even when its name contains the issue's slug.
- Every worker's dispatch prompt has a `## Your lead` section naming the project lead and the issue's epic.
- When the type's merge status (shipping) has the worker merge or fast-forward main, `## Your lead` ends with the one rule on who lands the branch (`tracker.LandPromptLine`): the worker, with `issue-cli ship`, even when a brief's Lead rules say the lead merges. The lead's brief carries the other side: never write "the lead merges" into a brief, and land a branch yourself only when the lead tick's `merge:` line names one whose worker stalled.
- Workers don't message each other. To agree a format with a sibling issue (one they build on, or one the lead's brief names), the `## Your lead` section points them at `issue-cli ask <sibling> "…"`. The ask is a comment that shows on the sibling's next issue-cli call, and its `issue-cli answer` comes back the same way. The lead sees open asks in `issue-cli status`. See [CLI: ask and answer](CLI/overview.md#ask-and-answer-sibling-asks).
- After `## Your lead` comes `## The owner's words on this issue` when the owner commented on it: the owner's newest five comments, oldest first, each cut to 400 characters, and a pointer to `issue-cli show` for older ones (`tracker.OwnerWordsPromptSection`). The dispatched `issue-cli start` prints only comments made after the briefing, so this is how an answer the owner wrote while no session was live reaches the next one. A stage briefing carries it too.
- Before it comes `## Already on main?` when the issue's title or brief (the body before its first heading, its Why and Bug sections) names a Go test (`TestX`, `BenchmarkX`, `FuzzX`) that main defines, and main has commits since the issue's `created` day that change that test function (`git log -L`) or name it in their message, not counting merges and commits with `Refs: <slug>` of the issue itself: each commit (at most 3 per test, 5 tests), the `go test -count=1 -run '^<Name>$' ./<dir>/` that checks it, and to say so in a `progress:` comment and stop when main holds it (`tracker.MainHolds`). It is left out once the issue's `work/<slug>` branch has commits of its own. `issue-cli dispatch` says it to the lead too, one line under `✓ Dispatched`: `main may already hold it: <sha> <subject> (<Name>); the briefing says so`; it still starts the worker, since a flake that came back is a fair re-dispatch. A stage briefing carries the section and folds it to that line. Retro 20261116-033105: a worker spent a session learning that 2c4c60e9 had fixed its brief's test the day before.
- After it comes `## Shell on this machine`: the zsh, rtk and BSD-tool pitfalls the dispatching machine has (from `$SHELL`, the OS and `rtk` on PATH), one line each. A machine with none of them gets no section.
- On the engine's own repository (a project with `cmd/issue-cli`), when the installed build is behind main (or `ci.branch`), `## Your issue-cli build` follows: how many commits behind, build the branch's issue-cli in the worktree with `go build -o ./issue-cli ./cmd/issue-cli` (the path is git-ignored) and use `./issue-cli` for the issue's transitions and gates, `./issue-cli check-script <slug> --yes` for the scripts, and never make install, during a soak or not: installing and restarting the viewer are the lead's or the owner's call. A build at main, a `dev` build or another project gets no section.
- A lead reaches a worker with Claude Code's own `ListAgents` / `SendMessage`, addressed by tmux name; the tool builds nothing on top of it. A worker doesn't message first. Its brief says: "Don't message lead-<project> or any other agent (SendMessage) unless the human, in your chat, gives you permission or asks you to. Ask the human in your chat, or post a question: comment. Your address is agent-<slug>, in case the lead messages you." `issue-cli lead message` pastes only into Codex sessions; for a Claude session it refuses and prints the `SendMessage` call.
- The lead dispatches only the workers you name in its chat, with `issue-cli dispatch --quote` (see [A human starts the work](#a-human-starts-the-work)), or, where you turned [auto-start](#auto-start) on, one at a time with `issue-cli dispatch --autostart`. A session in a different permission mode from the sender holds incoming messages for its human's approval, as Claude Code does out of the box.

See [The lead session](patterns/lead-session.md) for roles, routing and authority.

## Lead chat

The **CEO chat** (`/p/<project>/lead`, L1 · HQ on the Workshop Board; the company page's HQ desk, the map's *Talk to the CEO* and the sidebar's Lead chat all link it) is you talking to the project lead in the board, without `tmux attach`. It replaced the classic lead chat; `?classic=1` serves the same page. **All CEOs** (`/leads`, the map's *All CEOs* link; `wb/ceo-grid` of `wb/ceo-card`s, or "No project is configured in projects.yaml.") shows each configured project's lead as a card: live or not, how many things wait on you there, and *Talk to the CEO*.

- **The conversation.** The page reads the lead's Claude Code transcript: the newest `*.jsonl` under `~/.claude/projects/<cwd>/` (`$CLAUDE_CONFIG_DIR/projects` when set; the folder is the lead's working directory with every character that isn't a letter or digit as `-`, cut at 200 characters plus a hash when longer) whose `agent-name` record is the lead's session name, which `claude -n lead-<project>` writes (`leadTranscript`, ceo_transcript.go). The last 4 MB are read, and read again only when the file changes. Your messages are on the right, as you typed them: Claude Code's reminders are cut, and ticks, peer messages, task notifications and subagent turns are left out. Each CEO message is one bubble with its markdown rendered (lists, code, tables; raw HTML is dropped). A turn's tool calls fold into one line ("used 3 tools: Bash ×2, Read") that opens to each call's name and its description, command or file; tool output is left out. Each AskUserQuestion stays in the turn as a card: grey and marked *answered* with what was chosen, once its answer is in the transcript. The last 60 turns show, and a note says older ones are in tmux. Without a transcript (a lead started by hand without `-n`, or another cwd), the page falls back to `tmux capture-pane -p -J -S -2000` split into turns (`parseCEOPane`), and says so above the conversation. **Terminal** shows the raw pane. The page polls `GET …/lead?part=live` every 1.5 s while the tab is visible; each part (conversation, pane, menu, side panel) is replaced only when it changed, the box you type in is never touched, and the conversation follows the bottom unless you have scrolled up.
- **Working and menus.** The spinner line (`… esc to interrupt`) shows as the CEO working, with **Interrupt (Esc)**. A numbered menu under a question at the bottom of the pane is the *CEO waits on your choice* card: what it is about, the question, and one button per option that presses its digit (an option the TUI wrapped onto two lines is one option). Claude Code's permission prompt for a tool call (a question starting "Do you want to …?") is drawn at the end of the conversation instead of above the message box, marked *permission · Bash* (the tool), with the command it asks about and the call's description apart; the working line then says *Waiting on a permission prompt: Bash · <command>*, not working. Other menus stay above the box. A tool call that Claude Code's permission layer refused shows under the CEO's turn as a red *blocked* item: the tool, the command, who refused it (the auto mode classifier or a built-in safety check) and why (the classifier's stated reason, or what the safety check flagged). It is read from the call's tool result in the transcript (`Permission for this … was denied …`), so it stays in the history after the pane scrolls, and from the `⎿ Error: Permission …` line under the call on the pane when there is no transcript (ceo_blocked.go); a failed command or your own *No* is not one. A lead waiting on a permission prompt is *waits on you* in the switcher and on `/leads`, as for any menu; Needs you and push don't count a lead's menus. The session mode in the footer (`auto`, `plan`, `accept edits`) is the chip under the CEO's name.
- **The CEO's questions.** Claude Code writes an AskUserQuestion to the transcript only after it is answered, so an open one is read from its widget on the pane (`parsePaneAsk`, ceo_question.go): the tab headers, the question on screen, its options with descriptions (ticked when several may be picked) and *Other*. It is the *The CEO asks you* card above the message box: pick an option (or several) or type Other, then **Submit** (**Next** when more questions follow). `POST …/lead/answer` presses the widget's keys for the question on screen: a digit for a single choice; Other's digit, the text and Enter; for several choices the digits that change a tick, Down to Other and the text, Down to Submit and Enter. When the screen shows another question, or none, it presses nothing (409) and you answer in **Terminal**. The last tab lists your answers with **Submit answers** and **Cancel** (keys 1 and 2). A card you started isn't redrawn by the poll while the same question is on screen.
- **The message box.** Enter sends, Shift+Enter adds a line. The text goes in like Re-send prompt: `agentrun.Paste` loads it into a tmux buffer named after the session, pastes it as one bracketed paste and presses Enter, then checks its last line shows up in the conversation, pressing Enter again while it sits in the input box. Nothing passes through a shell. Control characters other than newline and tab are dropped (so a message can't close the bracketed paste and press keys), a message is at most 20 KB, and a copy is kept in `.agent-logs/lead-<project>/chat/`. **Send now** sends and then presses ctrl+x ctrl+s. **Mode · switch** presses shift+tab, which cycles Claude Code's permission mode.
- **Images.** Paste an image in the box, drop one on it, or pick one with **📎 Attach**: PNG, JPEG, GIF or WebP (read from the content, not the name), at most 10 MB each and 5 per message. Each goes to `POST …/lead/image` right away, is kept in `.agent-logs/lead-<project>/chat/images/<hash>.<ext>` (the same image twice is one file) and shows as a thumbnail with × above the text. Send waits for uploads, then ends the message with one `[Image: <absolute path>]` line per image; the CEO reads the file with its Read tool. A message may be images alone. The send refuses any path that isn't a file in that folder (400). Your turn in the conversation shows those lines as thumbnails (through `/files/`) that open the full image.
- **Files.** Any other file goes through the same box: a .zip, a PDF, notes, an .html. So does an image over 10 MB, or one `lead/image` refuses. It goes to `POST …/lead/file` (owner only: sign in at `/owner`), at most 50 MB and 5 per message, and an empty file is refused. The upload is streamed into `.agent-logs/lead-<project>/chat/files/<hash>/<name>` (mode 0600, name cleaned to ASCII `A-Za-z0-9._-`, the same bytes under the same name stored once) and shows as a chip (type badge, name, size) with ×. Send ends the message with one `[File: <absolute path>]` line per file, after the image lines; the agent reads it with its own tools. Department chats (`?dept=`) use the same project store. Your turn shows each file as a chip that downloads it: `GET …/lead/file/<hash>/<name>`, owner only, always `application/octet-stream` as an attachment, never shown in the viewer. The viewer never unpacks or opens a stored file.
- **Keys.** **Interrupt** is Esc. **Stop session** is Ctrl+C, after a confirm; a second Ctrl+C within 5 s is refused (429), because Claude Code exits on two. The window is checked and recorded under the lead's lock, so it holds while a message is still being delivered and when two presses race; a press whose keys fail to send does not start it. A send holds that lock over the paste and Enter only, not the wait for its last line to show up in the pane. ↑ ↓ ⏎ 1 2 3 answer menus. **Send now · interrupt** presses ctrl+x ctrl+s (a message queued while the lead works goes in now), **Show details** ctrl+o, **To background** ctrl+b ctrl+b. A chord's keys go 60 ms apart. Only these keys are accepted.
- **What stays in view.** At every width the box shows **📎 Attach** and **Send**, and the controls **Interrupt (Esc)** and **More**; More unfolds Send now, the mode switch, Send now · interrupt, Show details, To background, Stop session and ↑ ↓ ⏎ 1 2 3 in place (one primary action, docs/UI/workshop-board.md). A menu the CEO shows is answered by its own choice buttons above the box.
- **Start the CEO** shows when the lead isn't running (`POST …/lead/spawn`); the box and the keys are disabled until it runs.
- **Side panel.** *Waiting on you* is the Status page's list for the project (gates link to the issue's approve button, `#approve-<status>`; playtests, check scripts and `question:` comments link to the issue). *CEO's questions* are only the open ones (`openCEOQuestions`): the question widget on its screen, and the sentences ending in `?` in its last message until you write anything after it. Answered questions drop off; they stay in the conversation, marked answered. **Answer in chat** starts your reply in the box (`Re: "<question>" `) and sends nothing. `lead.json` has the same list as `open_questions` (`kind` ask or sentence, `text`), and `questions` is its texts: Needs-you and scripts read it rather than the pane. *Working in the background* lists the project's live workers (issue, status, working / idle / waiting on you). Last, the `tmux attach` command.

Not on the page yet (the design has them, the engine has no data for them): a queue of messages you can edit, the "your words authorize" card, notes from other CEOs.

What you send there is you typing in the lead's chat, so it counts as your words: when you tell the lead to dispatch a worker there, that is what its `--quote` reports. The page never sends anything by itself: no timer, no retry, no queued message. The tmux target comes from the project (`tracker.LeadSessionName`, `agentrun.Target`), never from the request; an unknown project is a 404. `POST …/lead/send`, `POST …/lead/key` and `POST …/lead/answer` take `application/json` only (`POST …/lead/image` and `POST …/lead/file` multipart) and sit behind the same origin check as every other write (403 cross-origin).

Before this page the agents canvas deliberately showed no pane and no message box (UI decision D-12); the lead chat is the owner's request to have both, for the lead only. Workers are still reached in tmux.

Limits: the pane is plain text, so colours and the TUI's layout are lost, and Claude Code's live areas (the spinner, the prompt box) are captured as they were drawn at that moment. Reading turns out of it is a best effort: a line it doesn't recognise stays text in the CEO's turn, and Terminal always has the raw pane. A long paste shows as `[Pasted text …]` in Claude Code's prompt until it is sent. A second Ctrl+C still needs tmux.

### The CEO chat on a phone

The owner uses the chat from a phone through the remote viewer, so at 640px and below the page changes shape (static/workshop.css, static/ceo-chat.js):

- **The box above the keyboard.** The chat column is as tall as the visible viewport (`visualViewport.height`, which shrinks when the on-screen keyboard opens while the layout viewport does not, on iOS), the conversation is the only scroller, and while the box has focus with the keyboard up the band and the keys hide and the page scrolls the chat to the top. The side panel sits below the chat.
- **Keys.** Interrupt and More stay, each at least 44px; **More** shows the rest as on a desktop, the key pad included, each at least 44px. Open in tmux is hidden (no terminal on a phone).
- **🎤 Dictate.** Shown only where the browser has speech recognition (`SpeechRecognition` / `webkitSpeechRecognition`: Chrome, Edge, Safari; not Firefox). The browser does the recognition: Chrome sends the audio to Google, Safari to Apple; nothing goes through the viewer. The **PL / EN** chip picks Polish or English (kept per browser; the first default follows the browser's language). Final words go into the box at the caret, the words still being heard show in grey above it, and nothing is sent until you press Send. Send aborts the recognition and drops anything it still delivers. Errors say what to do (microphone blocked, heard nothing, needs the network).
- **📷 Camera** (touch screens) opens the camera; **📎 Attach** still offers the photo library and files. A HEIC photo or an image over 10 MB is drawn on a canvas (at most 2560px on its long side) and sent as a JPEG to `…/lead/image`; one the browser can't decode goes to `…/lead/file`. The routes' caps and owner checks are unchanged.
- **Switch.** The button in the band opens a sheet (a panel on a desktop) with every configured project's CEO and its department leads, a group's leads nested under it. Each row links to that chat and shows the lead's state: *working* (its working line), *waits on you* (a menu or a question on its screen), *idle*, *not running*, or *couldn't read its screen* (a capture that failed, came back empty or took over 2 s). A CEO row shows the project's Needs-you count, the same number as `needs-you.json`. The sheet is `GET …/lead?part=switcher` (same route and gates as the page); it reads the panes only when opened, each `tmux capture-pane` under a 2 s deadline that kills it, and a second open waits for a capture still running instead of starting another. Opening it or following a row never starts a lead. **All CEOs** (`/leads`) shows the same states.
- **Drafts.** What you typed and didn't send is kept per chat in the browser (localStorage) and comes back when you return to that chat; it is cleared as Send starts and put back if the send fails.

## Local CI and dispatched agents

When `workflow.yaml` has a `ci:` block ([Workflow: Local CI](workflow.md#local-ci)):

- The dispatch prompt has a `## Local CI` section with the one-line state of the integration branch. While main is red it says the failure is inherited, so the agent doesn't debug it as its own. A failure main already has is not the agent's to fix: with no issue it files one (`issue-cli create`, no need to ask), with one it names it in its `tests:` comment. The section also tells the agent never to wait for CI after its merge.
- `issue-cli start` prints the same line, and `transition --to done` reminds the agent that CI runs without it.
- CI runs live in tmux as `ci-<project>`: a session of their own, or a window in the project's `tmux_session`. The name is not an agent prefix, so the agents canvas never shows it as an agent.
- CI never messages the lead: CI runs on a timer, and nothing on a timer prompts a session. A red run reaches the Status page, `status.md`, the `ci-red` markers and the merged issues' `ci:` comments; the lead reads `issue-cli ci status` when you ask, at check-ins and before a push. (Before 0.54 a red run pasted a `[from ci]` message into the live lead.)

## Other machines and cloud sessions

A session started anywhere but this machine works against the same repos this one does. With `privacy.work_files: tracker` it clones the private tracker too and runs `issue-cli tracker sync` before work (pull) and after (push). With `privacy.code: snapshots` it clones the private dev repo, never the public one, and opens its PRs there: a cloud session that opens a PR on the public repo publishes it, and GitHub never lets a PR be removed (that is how PR #62 went public). See [Workflow: Private work and code](workflow.md#private-work-and-code).

## Agent Model

Each project chooses whether dispatched agents get a pinned model or use their own global settings:

```yaml
- name: "My Project"
  agent_model_source: project   # or "global" (default)
  agent_models:
    claude: claude-opus-5-5
    codex: gpt-5
```

- `global` (default) — no `--model` flag is passed; the agent uses its own configured model.
- `project` — the model in `agent_models` for that agent type is passed as `--model <name>`. An agent type with no entry still launches without a flag.
- A routing rule's `model:`, and the built-in docs and report default, win over either for the starts they match ([The model per type of work](#the-model-per-type-of-work)).

Model names may contain only letters, digits and `._:/[]-`; anything else is ignored (it becomes part of the agent's shell command). Any other `agent_model_source` value fails config loading.

## Human Approval Notifications

When a human approves a status transition in the web UI, the server sends a natural-language message to the active agent's tmux session. The message is randomized from a set of conversational templates so the agent receives a human-like prompt rather than a structured signal. When the human typed words into the approve box, the message is `Approved for <status>: "<words>"` instead. An approve-then keeps its "run issue-cli start" line and adds `Owner's words: "<words>"`. The words are also saved as an `approval:` comment, which `issue-cli show` lists.

This nudge is the hand-off: at a gate the agent asks in chat, then stops and ends its turn, and the approval message resumes it. Agents don't block or poll (`issue-cli transition|start --wait` was removed in v0.49.0, see [CLI overview](CLI/overview.md#at-a-human-approval-gate)). If an agent wasn't reachable (no matching tmux session), tell it yourself that the approval landed. The nudge goes in like the lead chat's messages (`agentrun.Nudge`): a buffer named after the session, one bracketed paste, Enter, then the pane is read until the text is in the conversation. Claude Code can take the Enter as part of the paste and leave the text typed in its input box; while it sits there, Enter is pressed again, up to twice. A nudge that never lands within 20 s is reported as the notification error (`not delivered to <session>: …`), never as sent. The approve handler also timestamps the approval in the issue's stats sidecar (`last_approval`), and logs nudges that fail (`agent not nudged: …`) to the server log, so their failure rate can be measured.

## Approve Buttons in the Agent's Session

The `issue-approve` Claude Code mod (`claude-mods/issue-approve/` in this repo) puts the issue's approvals above the prompt of a dispatched Claude agent: `Human approval · in design: ☐ backlog ☐ in progress`. Pressing one does what the detail page's checkbox does (`POST …/approve`, a toggle, and the agent gets the approval nudge), so you can approve from the terminal you're watching.

- The buttons come from `GET /p/<project>/issue/<slug>/approvals.json`: the approval-gated transitions out of the issue's current status, with the workflow's type and system overlays applied. It is the same list as the detail page's Human Approval box (both read `issueApprovals`).
- The mod refreshes at session start, every 15 seconds, after each agent turn and after a press.
- It reads `ISSUE_VIEWER_ISSUE_SLUG` and `ISSUE_VIEWER_PROJECT`, and gets the board's address from `issue-cli projects --json` (`viewer_url`). An agent dispatched before `ISSUE_VIEWER_PROJECT` existed asks `GET /project-for?cwd=<its folder>`, which names the project whose work directory holds that folder (the most nested wins).
- A session with no issue slug (a lead, a session you started yourself) shows nothing.
- **Playtest:** when the issue waits on your playtest (status `human-testing`, or a project's own `playtest…` status) and the worktree's Makefile has a `playtest` target (`make -n playtest` at session start), the band also shows **▶ Playtest**. It runs `make playtest ISSUE=<slug>` in the agent's worktree for as long as you need; when it ends, **✓ Report** runs `make playtest-report ISSUE=<slug> APPLY=1`, which ticks the passed boxes, and the approve buttons are back for documentation. A toast says how each ended.

- **Run checks:** when the issue waits on the owner (the same rule as Playtest) and `issue-cli --project <p> check-script <slug> --list` finds a [guided check script](patterns/guided-check-scripts.md) (probed at session start and after each turn, since the agent writes the script during the session), the band shows **▶ Run checks**. A script asks questions, so the mod can't run it itself: the button POSTs the board's `…/check-script`, which opens tmux window `check-script-<slug>` in the worktree, and a toast says it started (or, headless, how to reach it). With a playtest due as well, both buttons show.

Install it once, at user scope, from this repo's folder:

```
claude plugin marketplace add <your clone>/claude-mods
claude plugin install issue-approve@ai-workflow-engine --scope user
```

Running sessions pick it up with `/reload-plugins`, and so do later edits to the folder (it is read from there, so no reinstall).

## Approval-gate Deep Links

When a CLI command (`start`, `transition`) fails because a human approval is missing, the error includes a clickable URL pointing at the issue's approve button:

```
Error: cannot start <slug>: human approval for "in progress" is missing; no changes were made

A human must approve this in the issue viewer:
  http://localhost:8080/p/<project>/issue/<slug>#approve-in-progress
Several waiting? Approve them together:
  http://localhost:8080/p/<project>/needs-you

Tell the human what needs approving, then stop and end your turn.
Do not wait or poll: re-run this command only after the human says it is approved.
```

The second link is the project's [Needs you list](UI/overview.md#needs-you-list), where the owner can approve several ready gates in one go. `issue-cli dispatch`'s waiting-on-gate refusal prints the same link on a `Several waiting?` line.

The fragment `#approve-<status>` matches the `id` on the approve button in the detail view, so clicking the link scrolls to and visually flashes the right control. For optional approvals (the ones hidden behind a "Divert to..." CTA), the page also auto-reveals the widget when the URL fragment matches.

The base URL comes from the same place for every link the CLI prints and every call it makes to the viewer (`viewerstate.BaseURL`):

1. `viewer_url:` at the top of `projects.yaml`, e.g. `viewer_url: "https://issues.example.com"` (http or https with a host; a trailing `/` is trimmed, a path prefix is kept; anything else fails the config load)
2. The running viewer's own address, from `viewer.json`, when that viewer serves the same config
3. Default `http://localhost:8080`

The viewer doesn't rebuild URLs from the request's `Host` header, and `ISSUE_VIEWER_URL` is no longer read or set. `issue-cli projects --json` gives each project's `viewer_url`.

## Environment Variables

Dispatched agents start with these set (passed to tmux with `-e`):

- `ISSUE_CLI_LOG` — the agent's own [clilog](CLI/telemetry.md#the-clilog), `<workdir>/.agent-logs/<session>/<session>.clilog` in the project checkout
- `ISSUE_VIEWER_SERVER_PWD` — server working directory
- `ISSUE_VIEWER_ISSUE_SLUG` — slug of the dispatched issue (attached to bug reports automatically)
- `ISSUE_VIEWER_LEAD_SCOPE` — the lead's session only: `project`. `issue-cli lead whoami`, `approve` and `lead spawn` read it to know who is asking; any other value is ignored.
- `ISSUE_VIEWER_PROJECT` — the project's slug (read by the `issue-approve` mod)
- `ISSUE_VIEWER_CONFIG` — the config path the viewer was started with, when it has one in its environment
