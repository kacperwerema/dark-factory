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
- **The second exception: recurring jobs, opt-in per job.** A department's recurring job (a weekly SEO pass, a daily lead search) can have a standing start too: `issue-cli job auto on <dept>/<job> --quote "<your words>"` (or `--all`). From then on, when the job is due, the lead's check-in starts its run (`issue-cli job due`, then `issue-cli job run <job> --scheduled`) inside the same budget as auto-start: mem-guard, the worker cap and the machine-wide gap. It's off by default. Without it, a due job waits in Needs you until you press ▶ Start run or name it. Every run quotes the words it started on in its `dispatch:` comment. The tool still runs nothing on a timer. See [Recurring jobs](departments.md#recurring-jobs).

- **The third exception: the post-mortem at close, opt-in per workflow.** With `postmortem: start: auto` in a workflow file, the transition into `at:` (done) starts one read-only post-mortem agent for that issue, `postmortem-<slug>`, headless, after `mem-guard check` passes. The owner's words are the workflow setting itself and the issue's own dispatch. It starts once per issue, never for another issue, and it changes nothing until you agree to its actions. With `start: ask` (the default) the transition only prints `issue-cli postmortem start <slug> --quote "…"` for the lead. See [Post-mortem](workflow.md#post-mortem). Source: factory/post-mortem-actions-each-lesson-to-one-place-only-if-a-fresh-agent-would-act-differently-applied-after-the-owner-agrees#D-1, #D-10 (provisional).

Agents message each other with Claude Code's own `ListAgents` / `SendMessage`. `issue-cli` never pastes into a Claude session on an agent's behalf: `issue-cli lead message` pastes only into Codex sessions, and for a Claude session it prints the `SendMessage` call to make instead.

### Where the tool types into a session

Every place the engine pastes into or starts a session, and what starts it:

| What | Code | Started by |
|---|---|---|
| Dispatch a worker | `agentrun.Start` via ▶ / `POST …/dispatch` | your click |
| | `issue-cli dispatch --quote` | your words, quoted (the lead or your shell) |
| | `issue-cli dispatch --autostart` | your stored [auto-start](#auto-start) words, at the lead's check-in |
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

Nothing runs on a timer that reaches a session. The viewer's timers (local CI's watcher, the daily health snapshot) run `make` and `git` and write files; they never paste into or prompt a session. Local CI reports to the Status page, `status.md`, the `ci-red` markers and `ci:` comments, and the lead reads `issue-cli ci status` when you ask.

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

A gate whose work isn't finished doesn't block: the worker still has something to do. That includes the boxes of the section the move into the current status appended (Implementation on entering in progress), even when the gate's own rules don't check them; the week report's blocked lines and auto-start follow the same rule. Re-attaching to a live worker is never refused.

## How the prompt is delivered

The prompt is saved to `<workdir>/.agent-logs/<session>/dispatch-prompt.txt`, in the project's checkout even when the agent runs in a worktree (the agent's clilog and `messages/` sit next to it), so removing a worktree loses none of the agent's history. Each new launch on an issue is also appended to the issue's stats sidecar as `dispatches: [{at, session, agent}]` ([Workflow Stats](workflow-stats.md)); re-attaching to a live session is not. The launch that creates the issue's work branch also records `base_branch` and `base_sha`, the branch and commit it was cut from, which `issue-cli show` and `start` print as `Base:`. The agent starts with it as its positional argument (`claude "<prompt>"`, the same as codex). It is never pasted into a booting agent. Claude is also started with `-n <session name>` (`claude -n agent-<slug> "<prompt>"`, `-n lead-<project>` for the lead), so its tmux name is its address for Claude Code's built-in messaging between sessions (`ListAgents` / `SendMessage`); codex gets no name. Earlier versions typed `claude`, waited a fixed 3 s and pasted the prompt. When Claude took longer to start, the paste was dropped, yet every step still reported ok.

### How the agent starts

The agent is the window's own command. Dispatch creates the session or window detached, in one tmux call, with the working directory (`-c`), the [environment](#environment-variables) (`-e`) and the command:

```sh
tmux new-window -d -t '=work:' -n agent-<slug> -c <worktree> \
  -e ISSUE_CLI_LOG=… -e ISSUE_VIEWER_PROJECT=… … \
  "$SHELL" -lic 'trap : INT; claude -n agent-<slug> "$(cat "<log dir>/dispatch-prompt.txt")"; exec "$SHELL" -l'
```

(`new-session -d -s …` when there is no shared session yet, or no `tmux_session`.) The agent runs in your login shell, interactive, so it is found on the same `PATH`, with the same aliases, as when you type it. When it exits, or is stopped with Ctrl-C, the window keeps a shell in the worktree. Nothing is typed into a shell, so keys you type into an attached tmux client while a dispatch runs can't reach the start command, and dispatch never selects the new window. Before this, dispatch typed `export …`, `cd …` and the start command into the window's shell after selecting it or opening its terminal, and whatever you were typing landed in front of the command (`aclaude: command not found`). The pane's output is no longer copied to a `rawlog` file.

Dispatch then polls the pane for the prompt's last line in the conversation. Text still in Claude Code's input box does not count: while it sits there, Enter is pressed again, up to twice. It reports the step **Prompt delivered**, or a failed step after 20 s. The text itself is never re-sent automatically.

### Re-send prompt

If an agent came up without its briefing, use **Re-send prompt** in the issue sidebar, the link in the reattach banner, or `issue-cli dispatch <slug> --reprompt --quote "<the human's words>"`. All three run `agentrun.Reprompt` (the button through `POST /p/<project>/issue/<slug>/dispatch/reprompt`), which pastes the current briefing into the issue's live session through a per-session named tmux buffer and submits it. It asks for confirmation first. If no agent session is alive, it fails with "No live agent session" and creates nothing; dispatch instead.

### Read the prompt without dispatching

`GET /p/<project>/issue/<slug>/dispatch-prompt` (`?format=json` for `{"prompt","slug","status","worktree","branch","session"}`) and `issue-cli dispatch-prompt <slug> [--json]` print the prompt a dispatch would send right now. Both share one builder (`tracker.BuildDispatchPrompt`), so the two outputs are byte-identical. Neither creates a worktree, session or file. The prompt follows the issue's current status, so it can differ from the saved `dispatch-prompt.txt` of an earlier dispatch.

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
worktree_sparse_exclude: ["issues/"]   # the default; [] keeps everything
```

- The worktree is `<workdir>/.worktrees/<slug>` on a new branch `work/<slug>`, created from the current `HEAD` of the project's workdir. An existing worktree is reused, so re-dispatching after a restart picks up where the agent left off.
- `issues/` is left out with sparse-checkout, so issues are only ever edited on the main checkout. Each `worktree_sparse_exclude` path is relative to the repo root (written as `!/issues/` in the pattern file), so a nested folder with the same name, such as `demo/issues/`, stays in the worktree. The dispatch prompt tells the agent it is in a worktree and that its `issue-cli` commands already point at the main checkout.
- `worktree_setup` runs on creation only. If it fails, the dispatch stops and reports the error.
- Worktrees stay after the issue ships. Nobody has to remove them; `git worktree remove` is there if you want the space back.

Epics and their children get worktrees like any other issue: `work/<slug>` from `HEAD`. An old `base:` on an epic is ignored. See [The lead session](patterns/lead-session.md#epics).

## Auto-start

Auto-start is your standing start for one project: while memory is low and the CPU has room, the lead starts one more worker at a time from the project's own list, without you naming each one. It's off for every project until you turn it on.

```bash
issue-cli autostart on --quote "start a new job when memory is under 50%" [--below 50] [--max-load <cores>] [--max-workers 2] [--gap 5]
issue-cli autostart status      # on / paused / off, your words, the limits, the last start, the verdict now
issue-cli autostart next        # start <slug> and the command to run, or hold: <reason>
issue-cli dispatch <slug> --autostart
issue-cli autostart pause [--reason "..."] | resume | off [--reason "..."]
```

- **Your words.** `autostart on` stores them with who relayed them and when, in `.autostart/<project>.json` in the main checkout (next to `.yolo/`, never committed). Only you, or the lead with your words, can turn it on, pause, resume or turn it off. Workers are refused. `off` logs the window in `.autostart/<project>.log.jsonl`.
- **The check.** `autostart next` holds, with the reason, when:
  - auto-start is off or paused
  - `mem-guard` isn't on `PATH`, or `mem-guard check` refuses (the hard stop)
  - `mem-guard used` is at or above `--below` (default 50%)
  - the 1-minute load average is at or above `--max-load` (default the core count): `hold: load 16.0 on 14 cores (max 14)`. It's read from `sysctl -n vm.loadavg` on macOS and `/proc/loadavg` on Linux; the cores are `runtime.NumCPU()`. Where there's no reading, load doesn't hold and the verdict says `load unknown` (mem-guard stays the hard stop)
  - the project has `--max-workers` live `agent-*` sessions (default 2; the lead doesn't count)
  - any project on this machine auto-started a worker less than `--gap` minutes ago (default 5)
  - nothing in the list is ready
- **The list.** Unclaimed `backlog` issues of the project's version, by priority then file order, the same order as `issue-cli next`. An issue is skipped while its worker is live, a `depends_on` issue isn't done, or it only waits on your approval (unless YOLO is on, which passes that gate).
- **The start.** `dispatch <slug> --autostart` takes a lock shared by every project on the machine (`$XDG_STATE_HOME/ai-workflow-engine/autostart/`), decides again for that issue, starts it like any dispatch and records the start. Its `dispatch:` comment quotes your stored words: `dispatch: "<your words>" (auto-start, turned on by lead-x in the lead chat 2026-10-30; memory 31% < 50%, load 4.3 < 14 on 14 cores, 1/2 workers, 2026-10-30)`. When anything holds, it exits 1 and starts nothing.
- **Who ticks.** The lead, at its check-ins. Its brief carries the rule while auto-start is on, and `issue-cli lead whoami` prints it. The tool still runs nothing on a timer, and the viewer never starts a worker for it.
- **Where it shows.** An Auto-start card on the Report (`/p/<project>/status`) with your words, the verdict now, the load and cores (`CPU: load 4.3 on 14 cores (max 14)`) and the last start on this machine, plus **Pause** / **Resume** / **Turn off** buttons (`POST /p/<project>/autostart/pause|resume|off`). The sidebar shows an Auto-start line while it's on, and `status.md` / `issue-cli status` print `Auto-start: …`.

## The lead's queue

The lead's queue is the project's ordered plan: what the lead's tick starts next while memory and CPU allow. It is the engine's version of the hand-kept night plan (numbered items, `[>] <slug>` once started, `[x] <sha>` once shipped, `blocked:`, and "think up more" kept last). Source: factory/the-leads-queue-as-an-engine-feature-an-ordered-plan-of-items-the-leads-tick-starts-while-memory-and-cpu-allow (D-1 to D-6, provisional).

```bash
issue-cli queue add <slug> --quote "<your words>" [--how ...] [--top|--before N|--after N] [--blocked-on <item|slug>]
issue-cli queue add --idea "<one line to file>" --quote "<your words>"
issue-cli queue list [--all]          # active items with their holds, done today, the refill line
issue-cli queue move N --top|--bottom|--before M|--after M
issue-cli queue block N --on <item|slug>   /   unblock N [--on ...]
issue-cli queue link N <slug>         # the idea's issue was filed
issue-cli queue quote N --quote "<your words>"
issue-cli queue drop N [--reason ...] /   done N [--sha ...]   # by hand: no-code items
issue-cli queue limits [--below 50] [--max-workers 0]
issue-cli queue next [--start]        # what starts next, or why nothing does
```

- **The file.** `<issues>/.queue/queue.json`, one per project, with the work files (a private tracker keeps it private). Written atomically under a lock (`.queue/queue.lock`), so the lead and a worker's `transition` can write at once. `<item>` is the item's number (`12`, `#12`) or its slug.
- **"Think up more" is always last.** It isn't stored: every list ends with `∞ Out of work: think up more…`, so it can't be done, moved or dropped. When nothing open is left, `queue next` prints it with the `queue add` command.
- **Your words.** An item starts only on your words: its own `--quote`, or auto-start's stored words while [auto-start](#auto-start) is on and not paused. An item with neither holds (`no owner's words: issue-cli queue quote N --quote …`). Never an agent's words. The started worker's `dispatch:` comment quotes them: `dispatch: "<your words>" (queue item 2, in the CEO chat; memory 31% < 50%, load 4.0 < 14 on 14 cores, 0 workers, no worker cap, 2026-10-31)`.
- **What holds an item**, in order: a queue blocker not done (an item number done or dropped, or a slug at its workflow's last status); an idea with no issue yet (`file it, then queue link N <slug>`); no issue with that slug; the issue already shipped or parked; already started (`started, worker live`, or `started, no live worker`: it is never restarted by itself, re-dispatch by hand); a live worker, a `depends_on` not done, or waiting on your approval (YOLO lets that through), as for auto-start; routing resting; no words.
- **The machine checks** are auto-start's (`Decide`): `mem-guard` found and `mem-guard check` passing, memory under the limit, the 1-minute load under the core count, the worker cap, and one start per 5 minutes across all projects (the same lock and last start as `dispatch --autostart`). Plus: **local CI red on main holds every start** (`local CI red on main at abc1234: no new starts until it is green`); no `ci:` block doesn't hold. Limits are auto-start's when it is on; otherwise the queue's own, `queue limits`: memory under 50% and **no worker cap** by default (your words, 2026-10-31: "don't limit yourself on the 5 individual workers - just on the memory you have").
- **Start.** `queue next --start` decides again under the lock, starts the first startable item like `dispatch`, marks it `[>]` (started, by whom, when) and prints the next item. When anything holds it exits 1 and starts nothing, listing why each item before it can't start.
- **Done.** `transition --to <last status>` marks the issue's items done with the merge SHA: the tip of `work/<slug>` when main (or master) contains it, else main's head (`✓ Queue item 4 marked done at becb563`). An item whose issue reached its last status another way (close, a lead's move) shows as done without a SHA.
- **Who.** The lead (or you); workers may read the queue but never change it or start from it. Nothing runs on a timer: the lead's check-in runs `queue next --start`. `issue-cli lead whoami` prints `Queue: N active, next <slug>. At each check-in: issue-cli queue next --start`, and `dispatch` without `--quote` points at `queue next` when the project has a queue.
- **Where it shows.** A Queue card on the Report (`/p/<project>/status`): the next 5 items with their holds, the blocked count, done today with SHAs, the refill line and what `queue next` says now. `status.md` / `issue-cli status` print `Queue: 3 active · 1 blocked · 2 done today · next <slug> · now: …`. The viewer only reads: it never writes the queue or starts a worker.

- **Your pause.** The Crew page across projects (`/crew`) has Pause and Resume for each project's queue (owner-only). A paused queue holds `queue next` before anything else, even items with their own quote; `queue list` and `queue next` print `hold: the queue is paused by the owner (Crew page) at <time>: resume it on the viewer's /crew page`. issue-cli has no resume: an agent can't lift your pause.

**Not yet:** editing the queue from the page; importing a markdown night plan; a queue per department.

## Routing between Claude and Codex

With both Claude Code and Codex logged in on this machine, a `routing:` block in the workflow file picks which one starts each worker, lead and job run, and moves new starts to the other one while a subscription is resting after a usage limit.

```yaml
routing:
  default: claude            # when no rule matches (no routing: block: claude, as before)
  fallback: [claude, codex]  # the order to try when the picked one is resting
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
- **While one rests.** A routed start takes the next agent in `fallback:` that is installed and not resting. When none is free, auto-start and scheduled job runs hold with the reason (`every agent to try is resting (claude until 17:00, codex until 18:30)`). A start you asked for goes ahead on the routed agent, with a warning.
- **The worker that hit the limit** is left running: nothing is killed, typed into or retried. Its Crew card says `hit its codex usage limit · left running` with the message and the reset. `status.md` lists it on its Working on line, and `agents.json` has its `limit`.
- **Where it shows.** Each live Crew card names its agent CLI, and the Crew header shows **Subscriptions**: `claude working · codex resting until 18:30 (limit seen in agent-x)`, `unknown` or `not installed`. `status.md` has an `Agents:` line. `issue-cli agents` prints the same, the panes showing a limit now and the rules. HQ's Spend block and `issue-cli spend` split the spend by subscription ([Departments](departments.md#spend-and-budgets)).
- **By hand.** `issue-cli agents rest <agent> --until HH:MM` (you saw the limit yourself) and `issue-cli agents clear <agent>` (a wrong match, or an early reset). Only you or a lead can run them; a worker is refused.
- **What it never does.** It never uses API keys, creates accounts, logs in, reads either CLI's login, or shares a subscription. It doesn't work around a provider's limits: it only chooses which of your installed tools starts the next task.
- **Lint.** The `routing` check (`issue-cli health lint`) warns about an agent that isn't a command name or isn't on `PATH`; a type, status or department the file or projects.yaml doesn't know; a rule with no `agent:`; a rule after a catch-all; and a `fallback:` that repeats an agent or leaves out the default. It also flags a department file that routes another department's work.
- **Judging the rules.** `issue-cli bench run <benchmark> --worker "<codex exec …>"` runs the same golden tasks on another CLI. run.json records the setup's `agent`, so `bench compare` puts time, tokens and score side by side ([Factory Benchmark](benchmark.md)).
- **claude-code-router (CCR).** CCR is a local proxy that routes Claude Code's model requests to other providers using API keys. The engine doesn't bundle, install or call it (factory/route-work-between-claude-and-codex-subscriptions-ccr-pick-the-agent-per-task-fall-back-when-one-hits-its-limit#D-1). If you set CCR up yourself inside a Claude Code session, routing still picks only the CLI.

## Lead Sessions

There is one lead per project. It runs in tmux like a worker, as `lead-<project>`, in the project checkout. Start it with `issue-cli lead spawn`, or the **Spawn lead** button in the sidebar's Actions or on the agents canvas (`POST /p/<project>/lead/spawn`, body `{}`). A live lead is re-attached, never started twice.

- Leads use the same launcher, `tmux_session` and `agent_model_source` settings as dispatched workers. What opens for them is [`lead_terminal:`](#lead-terminal), not `terminal:`.
- Its brief comes from `leads.project` in `workflow.yaml`, or the built-in default.
- A `lead-*` session is never counted as an active agent on an issue, even when its name contains the issue's slug.
- Every worker's dispatch prompt has a `## Your lead` section naming the project lead and the issue's epic.
- Workers don't message each other. To agree a format with a sibling issue (one they build on, or one the lead's brief names), the `## Your lead` section points them at `issue-cli ask <sibling> "…"`. The ask is a comment that shows on the sibling's next issue-cli call, and its `issue-cli answer` comes back the same way. The lead sees open asks in `issue-cli status`. See [CLI: ask and answer](CLI/overview.md#ask-and-answer-sibling-asks).
- After it comes `## Shell on this machine`: the zsh, rtk and BSD-tool pitfalls the dispatching machine has (from `$SHELL`, the OS and `rtk` on PATH), one line each. A machine with none of them gets no section.
- On the engine's own repository (a project with `cmd/issue-cli`), when the installed build is behind main (or `ci.branch`), `## Your issue-cli build` follows: how many commits behind, build the branch's issue-cli in the worktree with `go build -o ./issue-cli ./cmd/issue-cli` (the path is git-ignored) and use `./issue-cli` for the issue's transitions and gates, `./issue-cli check-script <slug> --yes` for the scripts, and never make install, during a soak or not: installing and restarting the viewer are the lead's or the owner's call. A build at main, a `dev` build or another project gets no section.
- Agents message each other with Claude Code's own `ListAgents` / `SendMessage`, addressed by tmux name. The tool builds nothing on top of it; the briefs just say it is there. `issue-cli lead message` pastes only into Codex sessions; for a Claude session it refuses and prints the `SendMessage` call.
- The lead dispatches only the workers you name in its chat, with `issue-cli dispatch --quote` (see [A human starts the work](#a-human-starts-the-work)), or, where you turned [auto-start](#auto-start) on, one at a time with `issue-cli dispatch --autostart`. A session in a different permission mode from the sender holds incoming messages for its human's approval, as Claude Code does out of the box.

See [The lead session](patterns/lead-session.md) for roles, routing and authority.

## Lead chat

The **CEO chat** (`/p/<project>/lead`, L1 · HQ on the Workshop Board; the company page's HQ desk, the map's *Talk to the CEO* and the sidebar's Lead chat all link it) is you talking to the project lead in the board, without `tmux attach`. It replaced the classic lead chat; `?classic=1` serves the same page. **All CEOs** (`/leads`, the map's *All CEOs* link; `wb/ceo-grid` of `wb/ceo-card`s, or "No project is configured in projects.yaml.") shows each configured project's lead as a card: live or not, how many things wait on you there, and *Talk to the CEO*.

- **The conversation.** The page reads the lead's Claude Code transcript: the newest `*.jsonl` under `~/.claude/projects/<cwd>/` (`$CLAUDE_CONFIG_DIR/projects` when set; the folder is the lead's working directory with every character that isn't a letter or digit as `-`, cut at 200 characters plus a hash when longer) whose `agent-name` record is the lead's session name, which `claude -n lead-<project>` writes (`leadTranscript`, ceo_transcript.go). The last 4 MB are read, and read again only when the file changes. Your messages are on the right, as you typed them: Claude Code's reminders are cut, and ticks, peer messages, task notifications and subagent turns are left out. Each CEO message is one bubble with its markdown rendered (lists, code, tables; raw HTML is dropped). A turn's tool calls fold into one line ("used 3 tools: Bash ×2, Read") that opens to each call's name and its description, command or file; tool output is left out. Each AskUserQuestion stays in the turn as a card: grey and marked *answered* with what was chosen, once its answer is in the transcript. The last 60 turns show, and a note says older ones are in tmux. Without a transcript (a lead started by hand without `-n`, or another cwd), the page falls back to `tmux capture-pane -p -J -S -2000` split into turns (`parseCEOPane`), and says so above the conversation. **Terminal** shows the raw pane. The page polls `GET …/lead?part=live` every 1.5 s while the tab is visible; each part (conversation, pane, menu, side panel) is replaced only when it changed, the box you type in is never touched, and the conversation follows the bottom unless you have scrolled up.
- **Working and menus.** The spinner line (`… esc to interrupt`) shows as the CEO working, with **Interrupt (Esc)**. A numbered menu under a question at the bottom of the pane (a permission prompt) is the *CEO waits on your choice* card: what it is about (the command), the question, and one button per option that presses its digit. The session mode in the footer (`auto`, `plan`, `accept edits`) is the chip under the CEO's name.
- **The CEO's questions.** Claude Code writes an AskUserQuestion to the transcript only after it is answered, so an open one is read from its widget on the pane (`parsePaneAsk`, ceo_question.go): the tab headers, the question on screen, its options with descriptions (ticked when several may be picked) and *Other*. It is the *The CEO asks you* card above the message box: pick an option (or several) or type Other, then **Submit** (**Next** when more questions follow). `POST …/lead/answer` presses the widget's keys for the question on screen: a digit for a single choice; Other's digit, the text and Enter; for several choices the digits that change a tick, Down to Other and the text, Down to Submit and Enter. When the screen shows another question, or none, it presses nothing (409) and you answer in **Terminal**. The last tab lists your answers with **Submit answers** and **Cancel** (keys 1 and 2). A card you started isn't redrawn by the poll while the same question is on screen.
- **The message box.** Enter sends, Shift+Enter adds a line. The text goes in like Re-send prompt: `agentrun.Paste` loads it into a tmux buffer named after the session, pastes it as one bracketed paste and presses Enter, then checks its last line shows up in the conversation, pressing Enter again while it sits in the input box. Nothing passes through a shell. Control characters other than newline and tab are dropped (so a message can't close the bracketed paste and press keys), a message is at most 20 KB, and a copy is kept in `.agent-logs/lead-<project>/chat/`. **Send now** sends and then presses ctrl+x ctrl+s. **Mode · switch** presses shift+tab, which cycles Claude Code's permission mode.
- **Images.** Paste an image in the box, drop one on it, or pick one with **📎 Attach**: PNG, JPEG, GIF or WebP (read from the content, not the name), at most 10 MB each and 5 per message. Each goes to `POST …/lead/image` right away, is kept in `.agent-logs/lead-<project>/chat/images/<hash>.<ext>` (the same image twice is one file) and shows as a thumbnail with × above the text. Send waits for uploads, then ends the message with one `[Image: <absolute path>]` line per image; the CEO reads the file with its Read tool. A message may be images alone. The send refuses any path that isn't a file in that folder (400). Your turn in the conversation shows those lines as thumbnails (through `/files/`) that open the full image.
- **Files.** Any other file goes through the same box: a .zip, a PDF, notes, an .html. So does an image over 10 MB, or one `lead/image` refuses. It goes to `POST …/lead/file` (owner only: sign in at `/owner`), at most 50 MB and 5 per message, and an empty file is refused. The upload is streamed into `.agent-logs/lead-<project>/chat/files/<hash>/<name>` (mode 0600, name cleaned to ASCII `A-Za-z0-9._-`, the same bytes under the same name stored once) and shows as a chip (type badge, name, size) with ×. Send ends the message with one `[File: <absolute path>]` line per file, after the image lines; the agent reads it with its own tools. Department chats (`?dept=`) use the same project store. Your turn shows each file as a chip that downloads it: `GET …/lead/file/<hash>/<name>`, owner only, always `application/octet-stream` as an attachment, never shown in the viewer. The viewer never unpacks or opens a stored file.
- **Keys.** **Interrupt** is Esc. **Stop session** is Ctrl+C, after a confirm; a second Ctrl+C within 5 s is refused (429), because Claude Code exits on two. The window is checked and recorded under the lead's lock, so it holds while a message is still being delivered and when two presses race; a press whose keys fail to send does not start it. A send holds that lock over the paste and Enter only, not the wait for its last line to show up in the pane. ↑ ↓ ⏎ 1 2 3 answer menus. **Send now · interrupt** presses ctrl+x ctrl+s (a message queued while the lead works goes in now), **Show details** ctrl+o, **To background** ctrl+b ctrl+b. A chord's keys go 60 ms apart. Only these keys are accepted.
- **Start the CEO** shows when the lead isn't running (`POST …/lead/spawn`); the box and the keys are disabled until it runs.
- **Side panel.** *Waiting on you* is the Status page's list for the project (gates link to the issue's approve button, `#approve-<status>`; playtests, check scripts and `question:` comments link to the issue). *CEO's questions* are only the open ones (`openCEOQuestions`): the question widget on its screen, and the sentences ending in `?` in its last message until you write anything after it. Answered questions drop off; they stay in the conversation, marked answered. **Answer in chat** starts your reply in the box (`Re: "<question>" `) and sends nothing. `lead.json` has the same list as `open_questions` (`kind` ask or sentence, `text`), and `questions` is its texts: Needs-you and scripts read it rather than the pane. *Working in the background* lists the project's live workers (issue, status, working / idle / waiting on you). Last, the `tmux attach` command.

Not on the page yet (the design has them, the engine has no data for them): a queue of messages you can edit, the "your words authorize" card, notes from other CEOs.

What you send there is you typing in the lead's chat, so it counts as your words: when you tell the lead to dispatch a worker there, that is what its `--quote` reports. The page never sends anything by itself: no timer, no retry, no queued message. The tmux target comes from the project (`tracker.LeadSessionName`, `agentrun.Target`), never from the request; an unknown project is a 404. `POST …/lead/send`, `POST …/lead/key` and `POST …/lead/answer` take `application/json` only (`POST …/lead/image` and `POST …/lead/file` multipart) and sit behind the same origin check as every other write (403 cross-origin).

Before this page the agents canvas deliberately showed no pane and no message box (UI decision D-12); the lead chat is the owner's request to have both, for the lead only. Workers are still reached in tmux.

Limits: the pane is plain text, so colours and the TUI's layout are lost, and Claude Code's live areas (the spinner, the prompt box) are captured as they were drawn at that moment. Reading turns out of it is a best effort: a line it doesn't recognise stays text in the CEO's turn, and Terminal always has the raw pane. A long paste shows as `[Pasted text …]` in Claude Code's prompt until it is sent. A second Ctrl+C still needs tmux.

### The CEO chat on a phone

The owner uses the chat from a phone through the remote viewer, so at 640px and below the page changes shape (static/workshop.css, static/ceo-chat.js):

- **The box above the keyboard.** The chat column is as tall as the visible viewport (`visualViewport.height`, which shrinks when the on-screen keyboard opens while the layout viewport does not, on iOS), the conversation is the only scroller, and while the box has focus with the keyboard up the band and the keys hide and the page scrolls the chat to the top. The side panel sits below the chat.
- **Keys.** Interrupt and ↑ ↓ ↵ 1 2 3 stay, each at least 44px; **More** shows Send now · interrupt, Show details, To background, the mode switch and Stop session. Open in tmux is hidden (no terminal on a phone).
- **🎤 Dictate.** Shown only where the browser has speech recognition (`SpeechRecognition` / `webkitSpeechRecognition`: Chrome, Edge, Safari; not Firefox). The browser does the recognition: Chrome sends the audio to Google, Safari to Apple; nothing goes through the viewer. The **PL / EN** chip picks Polish or English (kept per browser; the first default follows the browser's language). Final words go into the box at the caret, the words still being heard show in grey above it, and nothing is sent until you press Send. Send aborts the recognition and drops anything it still delivers. Errors say what to do (microphone blocked, heard nothing, needs the network).
- **📷 Camera** (touch screens) opens the camera; **📎 Attach** still offers the photo library and files. A HEIC photo or an image over 10 MB is drawn on a canvas (at most 2560px on its long side) and sent as a JPEG to `…/lead/image`; one the browser can't decode goes to `…/lead/file`. The routes' caps and owner checks are unchanged.
- **Switch.** The button in the band opens a sheet (a panel on a desktop) with every configured project's CEO and its department leads, a group's leads nested under it. Each row links to that chat and shows the lead's state: *working* (its working line), *waits on you* (a menu or a question on its screen), *idle*, *not running*, or *couldn't read its screen* (a capture that failed, came back empty or took over 2 s). A CEO row shows the project's Needs-you count, the same number as `needs-you.json`. The sheet is `GET …/lead?part=switcher` (same route and gates as the page); it reads the panes only when opened, each `tmux capture-pane` under a 2 s deadline that kills it, and a second open waits for a capture still running instead of starting another. Opening it or following a row never starts a lead. **All CEOs** (`/leads`) shows the same states.
- **Drafts.** What you typed and didn't send is kept per chat in the browser (localStorage) and comes back when you return to that chat; it is cleared as Send starts and put back if the send fails.

## Local CI and dispatched agents

When `workflow.yaml` has a `ci:` block ([Workflow: Local CI](workflow.md#local-ci)):

- The dispatch prompt has a `## Local CI` section with the one-line state of the integration branch. While main is red it says the failure is inherited, so the agent doesn't debug it as its own, and it tells the agent never to wait for CI after its merge.
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
