---
title: "The Lead Session"
order: 1
---

The **CEO** is what was called the project lead: the viewer and issue-cli say CEO, while the tmux name (`lead-<project>`), the `issue-cli lead` command (alias `issue-cli ceo`), the `/lead` pages (`/ceo` redirects there) and the `lead_terminal` / `lead_window_session` keys keep the old name.

A way to run several agents on one project without the human talking to each of them. The tool knows about **lead agents**: it spawns them, names them, routes workers to them and shows them on the agents canvas. Raid League, a game built this way, is the worked example at the end.

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

This repo's `leads.project` brief says the same in two lines.

### Brief

The lead starts with the brief in `leads.project` of `workflow.yaml`. A workflow without it gets a built-in default, so every project has a lead without editing anything.

```yaml
leads:
  project: |
    You are the project lead of {{project}}. You run in {{lead}}. ...
```

Placeholders: `{{project}}` and `{{lead}}`. `issue-cli` commands in the brief get `--project` injected, as in dispatch prompts. The CLI appends a fixed footer: the lead's role, what it may tick, its handoff note, and `issue-cli lead whoami`. An old `leads.epic` key still loads, does nothing, and `issue-cli process workflow` warns about it.

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

Agents can also message each other with Claude Code's built-in messaging between sessions (`ListAgents` / `SendMessage`): Claude agents are launched with `-n <tmux name>`, so `lead-<project>` and `agent-<slug>` are their addresses, and the lead may message a worker this way. Workers don't message the lead or a peer unless the human, in the worker's own chat, gives permission or asks them to (owner, 2026-10-24: "they can only talk to lead after my permission or when i explicitly ask them"); they ask the human in their chat or post `question:` comments. Nothing in the tool gates or records these messages. `lead message` is the tmux-paste fallback for Codex only: `issue-cli` never pastes into a Claude session. Workers can't use it; they post `question:` comments, which their lead reads from the issue trail. A lead still reads everything without interrupting anyone:

| Channel | Command | Shows |
|---|---|---|
| Issue | `issue-cli context <slug>` | Status, checklists, comments |
| Branch | `git worktree list`, `git log --oneline main..<branch>` | Code written so far, overlaps between branches |
| Logs | `.agent-logs/<session>/` | Every `issue-cli` call, the full transcript |
| Screen | `tmux capture-pane -p -t <session> \| tail -40` | What the agent is doing or waiting on |

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

While workers run, the lead checks in when you ask (no timer or loop prompts it; see [A human starts the work](../agent-dispatch.md#a-human-starts-the-work)) and reports only what changed: questions waiting, a gate reached, red tests, an idle agent, two branches touching the same files. On request it gives a **stand-up**: one line per dispatched issue with status, last progress and blockers, then the approvals waiting for you.

### Local CI

With a `ci:` block ([Workflow: Local CI](../workflow.md#local-ci)) nobody re-runs the checks on main by hand: the viewer starts a run after every merge. CI never messages the lead: it runs on a timer, and nothing on a timer prompts a session. Red reaches the Status page, `status.md`, the `ci-red` markers and the merged issues' `ci:` comments. The lead reads `issue-cli ci status` when you ask, at each check-in and before a push. When main is red it brings it to you and you decide together whether to revert, fix forward or dispatch a fix; CI never reverts. Before any push, and always before a release, the lead runs `issue-cli ci status`, and it never pushes a release while main is red. The lead brief says so when `ci:` is set.

With `workbench.release_candidates: true` (issue-viewer has it), a release goes through a release candidate first ([Roadmap: Cutting a release](../roadmap.md#cutting-a-release-candidate-soak-publish)). When the milestone is ready, the lead tags `<version>-rc.1` locally, installs it, restarts the viewer and tells the other projects' leads. Their bugs come in through `issue-cli report-bug` and join the release's milestone. Every merge after the candidate, bugfixes included, needs the next one. The lead never publishes (the `## <version>` heading and the push) without your explicit ok on a soaked candidate. The lead brief says so when the key is set, and the Waiting for release card names each step.

### Relaying questions

Workers that need a decision ask in chat: they message the lead (Claude Code's SendMessage, addressed by tmux name) with the options and their recommendation, say they're waiting, and stop. The lead brings questions to you **one at a time**, with the worker's recommendation and its own. It records your answer in your words with `issue-cli decide <slug> --text "…" --how "lead chat" --quote "…"`, which writes the `D-n` line under `## Decisions`, then messages the asker.

The lead recalls decisions; it never invents them. It answers a worker only from what is recorded (the issue body and its `## Decisions`, comments, design docs, approvals) and cites the source (`D-2`, the comment, the doc path). When nothing recorded covers the question, it doesn't decide: it asks you and tells the worker to wait. Every lead brief carries these rules (`tracker.LeadRules`), whatever `leads.project` says.

### Worker rules

The dispatch prompt and the status prompts in `workflow.yaml` already tell workers these, but a short doc your `CLAUDE.md` points to helps sessions that weren't dispatched:

- Own your issue only. New ideas become `idea` issues (`issue-cli create`), referenced with `#slug`. In a worker session, `start`, `claim` and `transition` on another slug fail ("you work on <own-slug> only; ask your lead").
- Duplicates of your issue are the one exception. When the lead asks, a worker closes an issue that duplicates its own, or that its change fixes: `issue-cli close <dup> --duplicate-of <own-slug>` (or `--fixed-by <own-slug>`) parks it from any status in one step, sets `duplicate_of:` / `fixed_by:`, comments on both issues and leaves every checkbox alone. Any other worker is refused with the rule; the lead and the human close any issue. A worker may also file a child issue of its own when the lead asks; other new issues still need the human's ok.
- One task, then stop. When the issue reaches its last status, or a gate that waits on the human, stop: don't run `issue-cli next`, don't pick up another issue, don't start other agents.
- Stop at human gates. Ask once in chat, then stop and end your turn; don't wait or poll. Continue when the human messages you that it is approved. A "go" in your chat is not an approval.
- Leave a trail: tick boxes as they're done, add `progress:` comments at milestones, and the comments your gates require (`tests:`, `docs:`, …).
- Commit on your own branch as you go; only shipping merges.
- Write a retrospective when you stop (`issue-cli retrospective`).

### Before dispatching

- Commit process changes (`CLAUDE.md`, `workflow.yaml`) first: worktrees branch from the checkout's `HEAD`, so workers read whatever is committed there.
- Dispatch only when you say so in the lead's chat, and only the issues you name: `issue-cli dispatch <slug> --quote "<your words>"`, one call per issue (the same as ▶ on the board; `--agent codex`, `--reprompt --quote` to re-send a live worker's briefing), never with raw HTTP. Without `--quote` it starts nothing; each dispatch leaves `dispatch: "<words>" (lead chat, <date>)` on the issue. For an epic the lead proposes which children to start, you say which, and it dispatches exactly those; starting more later needs a new instruction. It never dispatches when planning finishes or picks the next issue on its own. See [A human starts the work](../agent-dispatch.md#a-human-starts-the-work).
- Don't dispatch onto a pending gate. When the issue only waits for your approval, `issue-cli dispatch` and ▶ refuse (exit 3, the gate named); ask for the approval instead, since a worker would claim the issue and stop. `--force` overrides it.

### Handoff and restarts

The lead writes a handoff note before it exits: `sessions/YYYY-MM-DD-lead-<project>.md`. The note ends with a `## Next` section: the ordered next steps. The viewer's Status page (`/p/<project>/status`) shows that `## Next` list in its What's next tile, and its text form, `issue-cli status` (the viewer's `status.md`; `--json` for the data), is a ready-made stand-up to paste. Its lists are capped: each "+K more" line names the `issue-cli list` command that shows the rest. Its **Waiting for release** block tells the lead when the current milestone is ready to cut (or what blocks it), what is merged and not released, and what is not pushed. A new lead's brief names its newest note to read first, falling back to the older `sessions/*-lead-session.md`.

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

## Worked example: Raid League

Raid League is a football-manager-style game about a fantasy siege tourney, built in Godot and C# by one person and Claude agents since September 2026. It ran this pattern on one board project, starting as a convention before leads were part of the tool:

- **235 issues**, a **710-line `workflow.yaml`** with ten statuses and 13 system overlays, and three board buttons (*Balance report*, *Speed sweep*, *Idea sweep*).
- **Four human gates**: discussion → design, design → backlog, starting work, and playtest → documentation. The lead may tick discussion → design once every question was answered by the human and recorded, and any gate the human approves in its chat, always after an `approval:` comment.
- **Workers** run as windows in one shared tmux session for the project, each in a worktree under `.worktrees/` with `issues/` left out.
- **Retros** from every worker are swept by the lead, which fixes the process and files tool issues; swept files move to `retros/archive/`.
- **An epic** (a new game mode) staged around 18 child issues on its own experiment branch, before the tool settled on one lead and children shipping to `main`.

![Raid League's ten statuses, each with the check that guards it; four of them need a human](../images/lifecycle.png)

One issue there, replayed from its logs, went from discussion to pushed in 3 h 50 m with four approvals and a few chat answers:

![A worker's issue-cli calls and the human's words from discussion to git push, beside the issue as the human saw it](../images/issue-replay.png)
