---
title: "The Fresh Critic"
order: 5
---

# The Fresh Critic

The worker that wrote the work also judges it, so its blind spots reach the owner. Under YOLO nobody else reads the work before the after-the-fact review. A **fresh critic** is a read-only agent that did not write the work. It is given only paths, told to assume the work is wrong, and reports findings. A `blocks` finding holds the gate until a new run clears it or the owner overrides it.

Source: the spec-flow epic (factory/spec-flow-practices-in-the-workflow-…#D-1), factory/a-fresh-critic-that-never-wrote-the-work-…#D-1 to #D-4 (provisional).

## Turning it on

Add the `critique` rule to the edges the critic should hold. The `critique:` block is optional.

```yaml
critique:            # optional
  agent: codex       # claude | codex; default: routing's pick, switched to the other installed agent
  command: ""        # replaces the agent's command: sh -c, the brief on stdin, findings on stdout
  timeout: 15m       # default 15m
transitions:
  - from: "in design"
    to: "backlog"
    actions:
      - type: "validate"
        rule: "critique"
  - from: "testing"
    to: "human-testing"
    actions:
      - type: "validate"
        rule: "critique"
```

This project runs it on `in design → backlog`, `in design → in progress`, `testing → human-testing`, and the tweak path's `testing → shipping`. The feature catalog entry is `critique-gate`.

## Running the critic

```
issue-cli critique <slug> [--agent claude|codex]   # run it, record it, say whether the gate passes
issue-cli critique <slug> --brief                  # print the brief; start nothing
issue-cli critique <slug> --show                   # the newest run from the current status, notes included
issue-cli critique <slug> --override --quote "<the owner's words>"
```

The worker runs it before the gated transition. Until it does, the transition refuses and names the command, and so do `transition --dry-run` and `show` (`Critique: none from in design yet; the next gate wants one`).

1. **Only the current edge.** The command refuses when no edge out of the issue's status has the rule.
2. **Machine first.** `mem-guard check` must pass and the 1-minute load must be under the core count (the auto-start rules). Otherwise nothing starts. Without mem-guard on PATH it starts.
3. **The CLI.** `--agent` wins, then `critique: agent:`. Otherwise it takes routing's pick for the issue (the worker's agent) and switches to the other known agent (claude ↔ codex) when that one is installed and not resting.
4. **Paths only.** The brief names the issue file (its body carries the decisions), the epic's file, `.git-critique/branch.diff` (the branch's diff against main, written into the worktree and excluded from git), the changed files and `docs/departments.md` (settled rules). It never carries comments, `progress:` lines, the body's text or the worker's summary. `critique.Input` holds paths only, so nothing else can get in.
5. **Read-only and headless.** claude runs as `claude -p` with only Read, Grep and Glob allowed, and Edit, Write, NotebookEdit, Bash and web tools refused. codex runs as `codex exec --sandbox read-only`. Both start in the issue's worktree, so `issue-cli spend` counts their tokens for the issue's department with no extra bookkeeping. The run is part of the human-dispatched worker's session, not a new worker. It is one-shot and has no tmux pane.
6. **Recorded.** One line in `<issues>/.critique/runs.jsonl` (findings, malformed lines, Coverage, Not read, agent, branch head, exit). A `critique:` comment on the issue lists the verdict, counts, the blocks and should findings and Coverage. Notes stay in the log and `--show`.

7. **Usage limit.** When the critic exits non-zero (not a timeout) and its last lines are that CLI's usage-limit message (routing's `FindLimit`), the subscription is marked resting in routing's state as for a worker (`issue-cli agents`). The critic then runs **once** more on the other known agent that is installed and not resting. The recorded run is the second one, and its `critique:` comment says `codex hit its usage limit (resting until …); ran on claude`. If the fallback also hits its limit, it is marked resting too and the run is recorded as failed. There is no fallback under `critique: command:` or when `--agent` or `critique: agent:` chose the agent; the limit is still marked and the run is recorded as failed.

## The finding format

Other spec-flow steps (clarify, red rounds, post-mortem) use the same format. The parser is `internal/critique.Parse`.

```
blocks internal/x/y.go:42 — the empty list returns nil and the caller dereferences it
should docs/foo.md:10-14 — claims "always" with no decision cited
note cmd/issue-cli/cmd_x.go:7 — flag name differs from its siblings
Coverage: read 6 of 8 files
Not read: static/a.js, templates/b.html
```

- One finding per line, with an optional leading `- ` or `**bold**` severity. The severity is lowercase `blocks`, `should` or `note`. Next comes `path:line` or `path:line-end`, or several lines and ranges separated by commas (`path:29,114`, `path:14-16,144-147`; every one is kept), then a separator (`—`, `–`, `-` or `:`), then the text.
- **Malformed** findings are reported and kept: a line that starts with a severity word but has no `path:line` or no text is kept at that severity, with its raw text and the problem. A malformed `blocks` still blocks.
- `Coverage:` is required. Without it the run counts one blocks problem ("no Coverage: line"), so a critic that crashed or rambled never clears a gate. `Not read:` takes a comma-separated list, or `none`.
- A finding whose text ends in `:` is a lead-in. The bullet lines right after it (`- `, `* `, `+ `, `1. `, `1) `) are its cases, up to the first line that is not a bullet or a bullet that is a well-formed finding. A case is only a line the parser would otherwise ignore. A bullet that the parser always read as something, a lowercase severity word after `- ` or `* `, or `Coverage:` / `Not read:`, reads as it always did (a malformed `- blocks …` still blocks), so output that held the gate still holds it, and the cases after such a bullet still join the lead-in. The cases join the finding's text with newlines. They show indented under it in the comment, the CLI and the transition's refusal, and on their own lines on the station.
- Other lines are the critic's prose and are ignored. That includes a line right after a finding that does not end in `:`, and a line that is not a bullet right after a lead-in's cases. A finding wrapped by hand over two lines keeps only its first.

## The gate

`critique` passes when the newest run **from the current status** is clear, meaning no blocks finding (malformed included), no output problem, and it didn't fail or time out. It also passes when the owner overrode that run. Otherwise it refuses and names each blocks finding:

```
the critic found 2 blocks findings (run 20261031T…):
  blocks internal/x.go:42 — nil on an empty list
  blocks (malformed: no path:line) blocks the whole design is unsafe

Fix each, then run the critic again (the new run is what counts):

  issue-cli critique <slug>

Or the owner overrides: issue-cli critique <slug> --override --quote "<the owner's words>"
```

- `should` findings are shown by the run and on the station. They don't hold the gate.
- `note` findings are kept in the log.
- **Red rounds.** Each run with a blocks finding is one red round of the gate's critique check (`critique in design → backlog`), counted per run and not per branch head, because in design the head doesn't move between runs. A clear run or an override ends the streak. A failed run, or one held only by an output problem (no Coverage line), is the critic's fault and doesn't count. At `max_red_rounds` (4) the run posts `question: red rounds: critique <from> → <to> — red 4 of 4 …`. While that question is open, `issue-cli critique` and the transition both refuse until the owner resolves it ([Red-round cap](../workflow.md#red-round-cap)).
- **Override** is the owner's. It is refused from a worker's shell, and YOLO doesn't pass it: a blocks finding is a failed check, not an approval ([YOLO Mode](yolo.md)).

## Where it shows

- **The station** (`/p/<project>/issue/<slug>`) has a **Critique** block (`wb/station-critique`). It shows the newest run's verdict, edge, agent and age, the blocks and should findings with path:line, malformed lines, notes folded, Coverage and Not read. With a gate and no run yet, it names the command.
- `issue-cli show` prints a `Critique:` line.
- `issue-cli process transitions` lists "Validate a fresh critic found no blocks finding (issue-cli critique <slug>)".

## Lint

The `critique` check (`issue-cli health lint --check critique`) reports these problems:

- a `timeout:` that doesn't parse
- an `agent:` with no read-only command and no `command:`
- a `critique:` block that no edge uses
- a critique gate out of the first status, where nothing is written yet

## Upgrading

An `issue-cli` built before this rule existed refuses a transition that has it. Builds from this fix on name themselves in the refusal: "unknown validation rule: critique — this build (v0.58.0-rc.1, <its path>) doesn't know it … the installed build is behind the AI Workflow Engine's main", followed by the fix: in the engine's checkout (not the project's), `git pull && make install`, with `$(go env GOPATH)/bin` first on PATH. Without a checkout, install the newest release. Any rule a build doesn't know gets the same refusal. Install the new build before a workflow file turns it on.
