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
  agent: codex       # claude | codex; default: the other agent than the worker's, the same one when the other rests
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

With the gate, give the `in design` status prompt the line the catalog snippet carries: a finding is answered by editing the design **in place** where it is wrong (`issue-cli replace <slug> --section "Design"`, or `--line` / `--match` for one line), never by appending an "after critic run N" note. The critic reads the design, not its history: appended notes stack up and each run flags the old text against the new. The gate's refusal says the same.

This project runs it on no edge: `critique-gate` is declined in its workflow.yaml (the owner, 2026-10-31), so the critic runs only by hand here. `TestCritiqueDocMatchesTheWorkflow` keeps this line in step with that file.

## Running the critic

```
issue-cli critique <slug> [--agent claude|codex]   # run it, record it, say whether the gate passes, or that no gate waits on it
issue-cli critique <slug> --brief                  # print the brief; start nothing
issue-cli critique <slug> --show                   # the newest run from the current status, notes included
issue-cli critique <slug> --reparse <run id>       # parse that run's kept raw output again with the current parser
issue-cli critique <slug> --override --quote "<the owner's words>"
```

With a gate, the worker runs it before the gated transition. Until it does, the transition refuses and names the command, and so do `transition --dry-run` and `show` (`Critique: none from in design yet; the next gate wants one`).

1. **The current edge, or by hand.** With the rule on an edge out of the issue's status, the run judges that gate. Without one it is a run **by hand**: the same read-only critic, brief, record and comment, with `to` empty in the record. It holds nothing, counts no red round and has nothing to override, and a later gate wants a run of its own from its own status. The output, the comment (`critique: in progress, by hand (no gate), findings (codex) — …`) and the station say so. The verdict word for its blocks findings is `findings`, not `holds`.
2. **Machine first.** `mem-guard check` must pass and the 1- and 5-minute load must not both be at or over the core count (the auto-start rules). Otherwise nothing starts. Without mem-guard on PATH it starts.
3. **The CLI.** `--agent` wins, then `critique: agent:`. Otherwise the critic is the agent that did not write the work: the other known agent (claude ↔ codex) than the newest worker launch on the issue's dispatch record (`dispatches:` in its stats sidecar; only the `agent-<slug>` session counts, so on a branch taken over at a limit the newest writer is the one that counts). With no record it compares with routing's pick and the reason says so. When the other agent is resting or not installed, the critic runs on the same agent as the worker, and both the run and its comment say why (`same agent as the writer: codex resting until 17:00; on the worker's claude`). That includes both resting: a limit it meets then fails the run (step 7).
4. **Paths only.** The brief names the issue file (its body carries the decisions), the epic's file, `.git-critique/branch.diff` (the branch's diff against main, written into the worktree and excluded from git), the changed files and `docs/departments.md` (settled rules). It never carries comments, `progress:` lines, the body's text or the worker's summary. `critique.Input` holds paths only, so nothing else can get in. The one exception is derived, not written: with a [code map](../workbench-files.md#code-map), the brief also lists the branch's footprint (contexts it works in and reaches, binaries, Watch) under "Footprint (from the code map, not the author)", computed from the import graph.
5. **Read-only and headless.** claude runs as `claude -n critic-<issue> -p` with only Read, Grep and Glob allowed, and Edit, Write, NotebookEdit, Bash and web tools refused. codex runs as `codex exec --sandbox read-only`. Both start in the issue's worktree, so `issue-cli spend` counts their tokens for the issue's department with no extra bookkeeping; the claude critic's name lets `spend --kinds` show it apart from the worker (kind `critic`). The run is part of the human-dispatched worker's session, not a new worker. It is one-shot and has no tmux pane.
6. **Recorded.** One line in `<issues>/.critique/runs.jsonl` (findings, malformed lines, Coverage, Not read, agent, the worker's agent as `worker`, the reason as `why`, `same_agent` when the critic is the writer, branch head, exit, and `took_ms`: how long the critic ran, both runs when a usage limit fell back; a run from before it was kept has none and shows "—"). Under `critique: command:` the run names no agent, since the command may run anything. A `critique:` comment on the issue lists the verdict, counts, the time (`took 3m12s`), the blocks and should findings and Coverage. Notes stay in the log and `--show`. The critic's stdout is kept beside the run in `<issues>/.critique/raw/<run id>.txt`, scrubbed by the same secret scan as memory (`memory.RedactSecrets`: each secret becomes `[redacted: <kind>]`), and the run's `raw:` names the file (`raw_redacted:` lists the kinds scrubbed). `--reparse <run id>` parses that file with the current parser and prints the recorded report beside the new one. It records nothing, so a parser fix can be tried on a real run. Runs from before raw outputs were kept have no file and say so.

7. **Usage limit.** When the critic exits non-zero (not a timeout) and its last lines are that CLI's usage-limit message (routing's `FindLimit`), the subscription is marked resting in routing's state as for a worker (`issue-cli agents`). The critic then runs **once** more on the other known agent that is installed and not resting. The recorded run is the second one, and its `critique:` comment says `codex hit its usage limit (resting until …); ran on claude`, with the same-agent note when claude wrote the work. If the fallback also hits its limit, it is marked resting too and the run is recorded as failed. There is no fallback under `critique: command:` or when `--agent` or `critique: agent:` chose the agent; the limit is still marked and the run is recorded as failed.

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

Fix each where it is wrong, in place (the code, or the issue text with issue-cli replace <slug> --section <name>), not by appending a note: the critic reads the text, not its history. Then run the critic again (the new run is what counts):

  issue-cli critique <slug>

Or the owner overrides: issue-cli critique <slug> --override --quote "<the owner's words>"
```

- `should` findings are shown by the run and on the station. They don't hold the gate.
- `note` findings are kept in the log.
- **Red rounds.** In the briefing's words: a gate's command, the proof check, a local CI command or the critic's held runs; one round per commit, each held critic run its own round; only a failure that comes back counts, and an owner comment starts the count over. Each gated run with a blocks finding is one red round of the gate's critique check (a run by hand, from a status with no gate, holds nothing and is not a round) (`critique in design → backlog`), counted per run and not per branch head, because in design the head doesn't move between runs. A run counts only when one of its blocks findings (path and text, the line left out) was in an earlier run of the streak; a run whose findings are all new starts the count over at 1. A clear run or an override ends the streak. A failed run, or one held only by an output problem (no Coverage line), is the critic's fault and doesn't count. At `max_red_rounds` (4) the run posts `question: red rounds: critique <from> → <to> — red 4 of 4 …`. While that question is open, `issue-cli critique` and the transition both refuse until the owner resolves it ([Red-round cap](../workflow.md#red-round-cap)).
- **Override** is the owner's. It is refused from a worker's shell, and YOLO doesn't pass it: a blocks finding is a failed check, not an approval ([YOLO Mode](yolo.md)).

## Where it shows

- **The station** (`/p/<project>/issue/<slug>`) has a **Critique** block (`wb/station-critique`). It shows the newest run's verdict, edge, who wrote the work and who read it (`written by claude · read by codex`; a run from before the record names only its agent) and age, why when they are the same agent, the blocks and should findings with path:line, malformed lines, notes folded, Coverage and Not read. With a gate and no run yet, it names the command. A run by hand says `By hand: no gate waits on this run`.
- `issue-cli show` prints a `Critique:` line.
- `issue-cli stats --spec-flow` and the Stats page's **Spec flow** section count every run: findings by severity, which blocks held a gate and how each was cleared (fixed, overridden, held again, still holds), and the time ([Spec flow](../workflow-stats.md#spec-flow)).
- `issue-cli process transitions` lists "Validate a fresh critic found no blocks finding (issue-cli critique <slug>)".

## Lint

The `critique` check (`issue-cli health lint --check critique`) reports these problems:

- a `timeout:` that doesn't parse
- an `agent:` with no read-only command and no `command:`
- a `critique:` block that no edge uses
- a critique gate out of the first status, where nothing is written yet

## Upgrading

An `issue-cli` built before this rule existed refuses a transition that has it. Builds from this fix on name themselves in the refusal: "unknown validation rule: critique — this build (v0.58.0-rc.1, <its path>) doesn't know it … the installed build is behind Dark Factory's main", followed by the fix: in the engine's checkout (not the project's), `git pull && make install`, with `$(go env GOPATH)/bin` first on PATH. Without a checkout, install the newest release. Any rule a build doesn't know gets the same refusal. Install the new build before a workflow file turns it on.
