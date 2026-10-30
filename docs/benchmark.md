---
title: "Factory Benchmark"
order: 9
---

# Factory Benchmark

The factory changes all the time: prompts, `workflow.yaml`, issue-cli, the lead's rules. The live numbers on Stats mix a change with a different week of issues. The benchmark measures a change on the **same work**: done issues with a known good result (*golden tasks*) are solved again by a worker in a sandbox, from the commit they started at, and two runs are compared.

It is the first half of the self-improvement loop; the second, `factory/factory-experiment-loop`, proposes changes and adopts the ones that win. That loop uses the benchmark only through `bench run`, `bench compare` and the run files ([Interface](#interface-for-the-experiment-loop)).

## The owner's benchmark

Everything lives in the owner's folder, outside every repo and worktree:

| Path | What |
|:--|:--|
| `<config>/bench/<name>.yaml` | the benchmark: tasks, budget, prices, worker and judge commands |
| `<config>/bench/tasks/<project>/<slug>/issue.md` | the issue as it stood when the task starts |
| `<config>/bench/runs/<name>/<run>/run.json` | a run: setup, summary, every non-holdout task record |
| `<config>/bench/runs/<name>/<run>/holdout.json` | the holdout tasks' records |

`<config>` is `$XDG_CONFIG_HOME/ai-workflow-engine`, default `~/.config/ai-workflow-engine`; `$ISSUE_VIEWER_BENCH_DIR` replaces `<config>/bench`. Private projects' tasks are named here by project and slug only, and their snapshots stay in this folder, not in any repo.

```yaml
name: factory-v1
budget: {tokens: 6000000, usd: 40, wall: 6h, task_wall: 45m}
prices:                       # USD per million tokens, when the worker reports no cost
  claude-opus-5-5: {in: 0, out: 0, cache_read: 0, cache_write: 0}
worker: ""                    # default: claude -p --output-format json --permission-mode bypassPermissions --disallowedTools SendMessage,ListAgents
judge: ""                     # optional: a command that reads the judge prompt on stdin and prints SCORE: 0-5 and REASON: …
tasks:
  - project: dark-factory
    slug: cli/<slug>
    type: bugfix
    base: <sha>               # the commit the worker starts from
    merged: <sha>             # the known good result
    start: in progress        # the status the worker gets the issue in
    hidden: [cmd/issue-cli/<name>_test.go]
    hidden_tests: [<TestName>]
    check: go vet ./...       # must pass too, run in the task's checkout
    holdout: false
```

### Safety

- **Only the owner changes the benchmark.** `bench task add|remove`, `bench holdout` and `bench budget` with a value refuse in any agent session (a worker, a lead, or any Claude Code session): `only the owner changes the benchmark; …`, with the comment to post instead. Reading (`show`, `report`, `compare`, `candidates`) is open.
- **Holdout.** A holdout task runs with the rest; `report`, `compare`, `run.json`, the run's log, the health family and the Report card show the holdout only as passed n/m. Its records are in `holdout.json`. This keeps it out of every prompt and output an agent reads; it does not hide the file from a shell that opens it.
- **Budget.** A run stops a task at `task_wall` (default 45m) and stops the run when `tokens`, `usd` or `wall` is spent. While a worker runs, its transcript is read every 30 s, so a task that blows the budget is stopped mid-way (`budget`). The remaining tasks are recorded as `budget` and `bench run --resume <run>` continues.
- **A human starts a run.** From an agent session `bench run` needs `--quote "<the owner's words>"`, stored in `run.json` (`started_by`, `quote`). Nothing runs on a schedule.
- **Product goals are measured apart.** A per-project product eval (a solver penalty, kcal checks) can be a task's `check:`; anything bigger is its own issue in its project.

## Picking tasks

`issue-cli --project <p> bench candidates [--all] [--benchmark <name>]` lists the project's done issues that make good tasks: a recorded branch head (the stats sidecar's `branch_head`/`merge_sha`), test files in the branch's own commits, at most 600 changed lines. `--all` lists the rest with why they are left out. It writes nothing. An agent proposes from this list on its issue trail; the owner adds.

`issue-cli --project <p> bench task add <benchmark> <slug>` (creates the benchmark):

- **base and merged** come from the issue's stats: merged is the move to done's merge commit, else the last branch head; base is the parent of the oldest commit on the branch head's first-parent line since the issue's first dispatch. `--base` / `--merged` set them.
- **hidden** are the test files the branch's own commits changed (merges from main left out) that exist at merged; **hidden_tests** the Go test functions they add (or, when they only change tests, every test in them).
- **the snapshot** is the issue file in its tracker's git history just after it entered `--start` (default `in progress`, else the issue's first status), with `status:` set and `assignee`, approvals and `done_at` removed. `--snapshot <file>` gives it by hand.
- **the check**: in a throwaway clone, the hidden tests run at merged (one that fails there is dropped, with a warning) and `--check` must pass at merged and at base without the hidden files. Either failing refuses the task. `--no-verify` skips it.

## A run

`issue-cli --project <p> bench run <benchmark> [--label <name>] [--setup <engine ref>] [--engine <dir>] [--workflow <file>] [--resume <run>] [--keep]`, one task at a time:

1. `mem-guard check`; while it says BLOCKED the task waits (up to 30 minutes, then the run stops as `paused`). Without mem-guard on PATH there is a warning and no wait.
2. **The sandbox** under `$TMPDIR/awe-bench/<run>/<n>/`: a `git clone --shared` of the project with its remote removed, checked out detached at base, hidden files deleted; a scratch tracker holding only the snapshot and the workflow under test; its own `projects.yaml` (`terminal: none`), `XDG_STATE_HOME` and YOLO on in the clone's `.yolo/`, so gates pass by themselves. When the workflow uses worktrees, the issue's worktree is made as dispatch would.
3. **The worker** runs the dispatch prompt of the issue-cli under test (`dispatch-prompt --json`) plus a fixed note (no human will answer; record choices as provisional), on stdin, in its own process group. Its environment points issue-cli at the sandbox, puts the issue-cli under test first on PATH, gives tmux an empty socket folder (no live session is visible) and git no global config.
4. **Score:** the hidden test files from merged replace the worker's, the hidden Go tests run (`go test -json -run`), then the check. **pass** = every hidden test and the check green; **fail** otherwise; **broken** = the check already fails at base (left out of the pass rate); **timeout**, **budget**, **error** as named. The judge, when set, compares the worker's diff with the merged one for a 0-5 score; it never decides pass.
5. **The record** (`TaskRecord`): outcome and reason, hidden passed/total, check, judge, wall time, usage (tokens in/out/cache, cost, model, Claude Code version, from the worker's JSON result, else its transcripts; `known: false` when neither was readable, never 0), issue-cli calls and refused calls, gates passed on the first try, backward transitions and the statuses the issue went through (the sandbox's clilog and stats), the lines of the worker's diff, and files its tools wrote outside the sandbox.
6. The sandbox is removed (`--keep` leaves it). `run.json` is written after every task.

When the run ends, a health snapshot is taken of each project with a task (trigger `bench`), so the trend gets the run. The output ends with `bench report` and `bench compare <previous> <run>`.

**The setup under test:** by default this issue-cli and each project's current workflow file. `--setup <ref>` builds issue-cli from that commit of the engine (`--engine`, default the current repository) in a throwaway clone; `--workflow <file>` uses that workflow file in every sandbox. `--label` names the setup in reports and on the trend.

## Reading results

- `bench` lists the benchmarks with their last run; with none it says how to start.
- `bench show <benchmark>`: tasks (holdout names only from the owner's shell), budget, worker, judge.
- `bench report [<benchmark>/<run>|<benchmark>/latest]`: setup, who started it, stop, `6/8 passed, holdout 2/2, judge 4.1, … tokens, $…`, first-try gates, refused calls, backward moves, then one line per task.
- `bench compare <a> <b>`: the verdict, the pass rate (holdout included) and judge, token, cost and wall deltas, warnings (other benchmark, a run not finished, different tasks or holdout, a different model or Claude Code version), then the tasks with regressions first. Exit **0** no regression, **1** B regresses (a task that passed fails, the pass rate drops, or the holdout passes fewer), **2** not comparable.
- **The Report** (`/p/<project>/status`) has a **Benchmark** card for each benchmark with a task in the project: passed n/m, the holdout count, cost, tokens and the change against the previous finished run (red on a regression). Without a run it names `issue-cli bench run <name>`. Without a benchmark, **To unlock** offers `issue-cli bench candidates`.
- **Stats** draws "Factory benchmark pass rate" from the [bench family](workbench-files.md#the-bench-family), one line per benchmark.

## Interface for the experiment loop

`internal/bench/record.go` holds the types; fields are added, never renamed.

- `bench run <benchmark> [--setup <ref>] [--workflow <file>] [--label <name>] --json` prints the `Run`.
- `bench compare <a> <b> --json` prints `{a, b, comparable, regressions, improved, tasks, pass_rate_a, pass_rate_b, pass_rate_delta, judge_delta, tokens_delta, usd_delta, wall_delta_sec, holdout_a, holdout_b, warnings}` and exits 0, 1 or 2.
- `run.json`: `{id, benchmark, started, ended, stop: done|budget|paused|error, stop_note, setup {ref, issue_cli, issue_cli_version, workflow, label}, started_by, quote, budget, tasks: [TaskRecord], holdout {passed, total}, summary {tasks, scored, passed, pass_rate, judge, tokens, tokens_known, usd, wall_sec, cli_refused, rework_backward, gates_first_try_rate}, spent_tokens, spent_usd}`. `summary.pass_rate` leaves the holdout out; compare's rates include it.

The loop never writes these files and never edits the benchmark.
