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
budget: {tokens: 250000000, usd: 40, wall: 6h, task_wall: 45m}   # 10 tasks at 25M tokens each
prices:                       # USD per million tokens, when the worker reports no cost
  claude-opus-5-5: {in: 0, out: 0, cache_read: 0, cache_write: 0}
worker: ""                    # default: claude -p --output-format json --permission-mode bypassPermissions --disallowedTools SendMessage,ListAgents
judge: ""                     # optional: a command that reads the judge prompt on stdin and prints SCORE: 0-5 and REASON: …
grader: ""                    # optional: grades evals' rubric lines (prompt on stdin, {reason, pass, score} out), e.g. claude -p --model claude-haiku-5-5
scorer: ""                    # default: npx --yes promptfoo@0.124.1 (output evals, see Evals)
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
- **Budget.** A run stops a task at `task_wall` (default 45m) and stops the run when `tokens`, `usd` or `wall` is spent. `tokens` counts input, output, cache writes and **cache reads**. Cache reads are most of it: the worker re-reads its cached context (prompt, briefing, files read so far) on every turn, so a dark-factory task spends about 15M tokens (2026-10-31), of which well under 1M is new input and output. Size `tokens` per task: unset, the cap is 25M a task × the benchmark's tasks (`bench.DefaultTaskTokens`), and `bench budget --tokens` under 25M gets a note that the first task will likely stop part way. Cache reads are cheap per token, so `usd` is the cap that follows money. While a worker runs, its transcript is read every 30 s, so a task that blows the budget is stopped mid-way (`budget`). The remaining tasks are recorded as `budget` and `bench run --resume <run>` continues.
- **A human starts a run.** From an agent session `bench run` needs `--quote "<the owner's words>"`, stored in `run.json` (`started_by`, `quote`). Nothing runs on a schedule.
- **Product goals are measured apart.** A per-project product eval (a solver penalty, kcal checks) can be a task's `check:`; anything bigger is its own issue in its project.

## Picking tasks

`issue-cli --project <p> bench candidates [--all] [--benchmark <name>]` lists the project's done issues that make good tasks: a recorded branch head (the stats sidecar's `branch_head`/`merge_sha`), test files in the branch's own commits with at least one Go test (the scorer runs only Go tests, so one without would be judged by its check alone), at most 600 changed lines. `--all` lists the rest with why they are left out. A proposed row has a `risk:` line when its hidden tests' own bodies drive tmux, a headless browser (or it changed playtest files), sleeps or time limits (`timing`), when it has over 20 hidden tests (`broad`: it only changed tests, so every test in its files judges it), when the issue has an "(owner)" box (`owner steps`), or when main merged into the branch after base (`main merged in (N files not its own)`: base..merged differs in files the branch never changed, which the worker starts without, so hidden tests leaning on them may not build). `--json` adds `system`, `tests` (the hidden test names) and `risks`. It writes nothing. An agent proposes from this list on its issue trail; the owner adds.

`bench candidates --replay [<slug>...]` also replays each proposed candidate (only the named slugs when given) to see whether it is solvable from its base. One at a time, in a throwaway `git worktree add --detach` under the temp dir (never `.worktrees/`; the repo's hooks are off), removed with `git worktree remove` and `git worktree prune` when it finishes or fails. The replayed `go test` gets a TMPDIR inside that folder, so what old tests leave in the temp dir goes with it:

1. Base, with the hidden files from the merged commit put in (as scoring does): the hidden tests run. All passing → **tests pass on base** (they judge nothing).
2. Base plus the branch's own commits (`cherry-pick --no-commit`, first-parent, merges from main left out, oldest first). A commit that doesn't apply → **doesn't replay**, with the commit and its conflicting files.
3. The hidden tests on that replay. All passing → **solvable** (tests that already passed on base are named). Some failing → they run on the merged commit: passing there → **needs later main** (the failing tests and how many files main merged in); failing there too → **not checked**.

Every `go test` waits on `mem-guard check`: BLOCKED, or a reading of 70% or more, marks the candidate **not checked: memory …** and the list goes on. Each row gets a `replay: <verdict> — <why>` line; `--json` adds `replay` and `replay_why`. Progress (`replaying <slug> (i/n)`) goes to stderr. No model is called and nothing is written, so agents may run it; the replay labels, it never drops a candidate.

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

## Evals

Each workflow file says how the benchmark scores a run of each of its types, so a workflow change gets a verdict for each workflow: net positive, net negative or no change, with the time and cost beside the score.

### Declaring them

At the top of the workflow file (a department's own file has its own):

```yaml
evals:
  - id: code                        # unique in the file
    types: [feature, tweak, bugfix] # the issue types it scores; empty = the rest
    score: tests                    # hidden Go tests + the task's check (the default)
  - id: piece
    types: [piece]
    score: output                   # the run's newest output on the shelf for the issue
    rubric:                         # graded by a model: the benchmark's grader:
      - "Does what the brief asks for the reader the ICP names"
      - "As good as or better than the REFERENCE the owner approved: {{reference}}"
    assert:                         # promptfoo assertions that need no model
      - {type: not-icontains, value: "lorem ipsum"}
    pass: 0.7                       # mean score 0-1 a task needs (default 0.7)
    checks: ["test -f departments/marketing/brand.md"]   # optional shell commands
```

The development template has `code` and `report`; the marketing template has `foundation` (foundation, market-size), `piece` and `seo-pass`. Catalog entry: `workflow-evals`.

A department's golden task is a done issue whose output the **owner** approved on the shelf. `issue-cli bench task add <benchmark> <slug> [--output <id>] [--base <sha>] [--start <status>] [--snapshot <file>]` (owner only) copies the approved file as the task's reference (`{{reference}}` in a rubric line) and runs the task on the department's own workflow file. An output in review, rejected or link-only is refused.

### Checking them

`issue-cli health lint --check evals` (and every `health lint`) warns about an eval with no id or a repeated one, a `score` other than `tests` or `output`, a `pass` outside 0-1, or a type the file doesn't define.

### When they run

Only inside `bench run`, which the owner starts: from their own shell, or by an agent with `--quote "<the owner's words>"` (and `experiment run`, which calls it the same way). Nothing runs on a schedule, no other command calls a model, and nothing is adopted by itself.

1. **Before**, `bench run` prints an estimate: the task count × the newest finished run's mean time and cost per task, or "no previous run". The budget caps stay as above.
2. **Each task** runs in its own sandbox (below). The worker works the issue. Then a `tests` eval runs the hidden tests and checks, and an `output` eval runs promptfoo on the newest output the run put on the sandbox's shelf. No output is a **fail**.
3. **promptfoo** (MIT, local, pinned 0.124.1, `npx --yes promptfoo@0.124.1`; `scorer:` in the benchmark replaces it) gets a config whose "model" is its `echo` provider over the output text, with `reference.txt` and `brief.md` as vars. Each rubric line becomes an `llm-rubric` assertion graded by the benchmark's `grader:` command: the prompt on stdin, `{reason, pass, score}` JSON out, for example `claude -p --model claude-haiku-5-5`. Without a grader, rubric lines are left out and listed in the record's `eval_skipped`. An eval with nothing it can grade stops the task as `error` before the worker starts. **pass** = score ≥ `pass` and checks green.
4. **Time and cost**: every task records the worker's wall time plus scoring time (`score_wall_sec`), and tokens and cost for the worker and grader. Cost uses the benchmark's `prices:`, then `prices.yaml` as the rooms' spend does. `bench report` shows them per task.

Why promptfoo (D-1, provisional): its YAML assertions are what a workflow file declares, it is one pinned command with no venv, and `exec:` lets the owner's own command grade. Inspect (MIT, Python) owns the agent loop and sandboxes the benchmark already has. DeepEval sends telemetry unless you opt out, and its LLM metrics default to OpenAI. OpenAI Evals is mostly unmaintained.

### What is isolated

Each task's sandbox is `$TMPDIR/awe-bench/<run>/<n>/`, removed after the task (`--keep` leaves it):

- a `git clone --shared` of the project at the task's base with no remote, and a scratch tracker holding only the snapshot and the workflow under test;
- for the worker and the scorer: `XDG_STATE_HOME` and `XDG_CONFIG_HOME` in the sandbox (no owner's viewer.json, projects.yaml, prices or YOLO state), `ISSUE_VIEWER_CONFIG` the sandbox's projects.yaml, tmux's socket folder in the sandbox (no live session is visible), git with no global config, and `ISSUE_VIEWER_BENCH_DIR`, the `TMUX*` and lead/worker variables removed, so the benchmark folder and its holdout aren't visible;
- for the scorer, also `HOME` in the sandbox (`eval/home`), with promptfoo's config and cache under `eval/.promptfoo`, telemetry, update check and sharing off, and `--no-write` (no results database). npm's cache stays where it is, so npx downloads promptfoo once. Its network use is that first download and whatever the grader command does.
- The worker keeps the owner's `HOME` (D-10): Claude Code's login and the transcripts the usage is read from live there. Files its tools wrote outside the sandbox are recorded (`outside_touched`) and shown in `bench report`.

A test runs a whole task with a worker and scorer that write a viewer.json, a projects.yaml and a HOME file, and checks that all of them land in the sandbox and nothing reaches the real state or config dirs.

### The verdict per workflow

`bench compare <a> <b>` prints **By workflow (A → B)** when the runs hold more than one workflow or any eval score. `--json` adds `workflows`. For each workflow's non-holdout tasks (the holdout stays only in the overall passed n/m) it shows the pass rate and the mean score (eval score, or judge/5) A → B, and wall time, tokens and cost A → B with the change in percent. Then the verdict:

- **not comparable**: a run didn't finish, the workflow's tasks differ, or the models or Claude Code versions differ;
- **net negative**: a task that passed now fails, the pass rate drops by at least one task (1/n), or the score drops by 0.05 or more; or the quality holds and tokens or cost rise by 10% or more, or wall time by 25% or more (D-5, D-9);
- **net positive**: the same gains with no regression, or the quality holds and tokens or cost fall by 10%, or wall time by 25%;
- **no change**: anything else.

## Interface for the experiment loop

`internal/bench/record.go` holds the types; fields are added, never renamed.

- `bench run <benchmark> [--setup <ref>] [--workflow <file>] [--label <name>] --json` prints the `Run`.
- `bench compare <a> <b> --json` prints `{a, b, comparable, regressions, improved, tasks, pass_rate_a, pass_rate_b, pass_rate_delta, judge_delta, tokens_delta, usd_delta, wall_delta_sec, holdout_a, holdout_b, warnings, workflows}` and exits 0, 1 or 2. `workflows` is `[{workflow, tasks, verdict, why, pass_rate_a, pass_rate_b, score_a, score_b, tokens_a, tokens_b, usd_a, usd_b, wall_a_sec, wall_b_sec, regressions, improved}]` ([The verdict per workflow](#the-verdict-per-workflow)).
- A task record also has `workflow`, `eval`, `eval_score` (-1 when no eval graded it), `eval_passed`, `eval_total`, `eval_failed`, `eval_skipped` and `score_wall_sec`.
- `run.json`: `{id, benchmark, started, ended, stop: done|budget|paused|error, stop_note, setup {ref, issue_cli, issue_cli_version, workflow, label}, started_by, quote, budget, tasks: [TaskRecord], holdout {passed, total}, summary {tasks, scored, passed, pass_rate, judge, tokens, tokens_known, usd, wall_sec, cli_refused, rework_backward, gates_first_try_rate}, spent_tokens, spent_usd}`. `summary.pass_rate` leaves the holdout out; compare's rates include it.

The loop never writes these files and never edits the benchmark.
