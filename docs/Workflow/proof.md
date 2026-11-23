---
title: "Proof Links"
order: 3
---

# Proof Links

Every acceptance criterion (`AC1`, `AC2`, … under `## Acceptance Criteria`) is linked to the proof that it works:
a test, a playtest step, a commit, or a measured value. A link points at something that exists. Nothing is copied
into the issue or the repo, and the engine checks every link each time it reads it. A ticked criterion means
"proven", not "the agent said so".

A heading with a parenthetical suffix is the same section: boxes under `## Acceptance criteria (lead, 2026-11-21)`,
the heading a lead adds criteria under after the fact, are criteria too, with that section's ids (`ACL1`, `ACL2`).
prove, the proof gate and the commit-msg hook read them alike, so `Proves: ACL1 TestX` links, and one line may name
`AC` and `ACL` ids on an issue with both. Other words after the name (`## Acceptance criteria for the UI`) make a
different section.

The `proof-links` workflow feature turns this on. It is in this repo's `workflow.yaml`, in the bundled
`development` template, and `issue-cli workflow upgrade` offers it to other projects.

## Where links come from

Most links need no command: the agent declares them where it writes the proof.

| Declared in | Example | Link |
|:--|:--|:--|
| a playtest step | `proves: [AC2]` on the step in the session YAML | step |
| a commit trailer | `Proves: AC2 TestBadgeCount` on the commit that adds or changes the test | test |
| a commit trailer | `Proves: AC2` on the commit that is the change | commit |
| `issue-cli prove` | `issue-cli prove <slug> AC2 --value "690 vs 380 (1.82×)"` | any kind |
| `issue-cli prove --lead` | `issue-cli prove <slug> AC2 --lead "issue-cli check-script <slug> --yes"` | lead |

- **Trailers** are `Proves:` lines in the commit message, one or more, in any paragraph after the subject (one
  in its own paragraph before `Co-Authored-By` counts too):
  `Proves: AC1, AC3 TestA TestB` links AC1 and AC3 to both tests. Words shaped like a criterion id (the section's
  prefix plus a number) are ids, and the rest are test names. A test name with spaces (a JS/TS `it()` or
  `describe()` title) goes in double or single quotes and is read whole: `Proves: AC3 "lista: id i z (ryba)"`;
  `prove --test "<title>"` takes it whole too. Written without quotes (`Proves: AC3 lista ukryte przepisy`), the
  words after the ids are read as one test when a test file on the branch has that whole phrase, else one test per
  word; the commit-msg hook's note names the quoted form. Test names only: no `<slug>#AC2` (the commit is on the issue's branch), no
  package path (`internal/memory`: prove finds each test's package itself), no flags or prose.
  - **Playtest steps never prove through a trailer.** A step proves through `proves: [AC1]` on the step in its
    session file (or `issue-cli prove <slug> AC1 --step TP4`). A step id in a trailer (`Proves: AC1 TP4`,
    `Proves: AC1 lead-tab/TP4`) would be read as a test that isn't found.
  - **A brief's boxes take `check`, not `Proves:`.** An issue with no `## Acceptance criteria` has nothing for a
    trailer to link. A brief's boxes (`B1`) are ticked with `issue-cli check <slug> B1`, never a `Proves:` line, and
    the commit-msg hook refuses one there with that single note.
  - **Checked at commit time.** The repository's commit-msg hook (`make hooks`, or `issue-cli workflow commit-msg
    --install`) reads each `Proves:` and `Catalog:` line with the same parsers and refuses the commit, with the
    fix, when a line would link or mark nothing.
  - Criteria ids stay put when a criterion goes: `issue-cli check-remove <slug> AC6` keeps AC6's slot, so AC7's
    trailers still prove AC7. AC6's links become strays.
  - Only the work branch's own first-parent commits count. While the branch is live they are read as
    `main..<branch>`, so commits merged in from main never count. Which branch that is: see
    [Finding the issue's branch](#finding-the-issues-branch).
  - After the merge they are read from the recorded branch head, back to the issue's first dispatch.
  - A commit another issue's branch held first is not this issue's: some other issue recorded a branch head
    (`branch_head` in its stats sidecar) containing it before this issue recorded one. That leaves out the
    commits of a base branch the work branch was started on before it reached main, and, after the merge, the
    commits other issues fast-forwarded onto main meanwhile. Their trailers don't count and they don't move the
    last code commit.
  - A live branch that only fast-forwarded to main, with no commits of its own, is walked as if merged. Other
    issues' commits on main are left out by the rule above, so it lists none of their trailers. A `Proves:` commit
    made straight on main, which no issue recorded a head for, still counts.
- **`proves:`** is an optional field on a playtest step (see the [playtest contract](../patterns/playtest/README.md)).
  The runner copies it into each run's `results.yaml`, which is what the engine reads. `make playtest-check`
  rejects an id that isn't one of the issue's criteria.
- **`issue-cli prove`** is the fallback, and the only way to record a value. Its links are stored in the issue's
  data sidecar under `proofs` (see [Per-issue Data Store](../data-store.md)).

## Finding the issue's branch

prove, `prove --run`, the `commits_reference_issue` rule, check-script and the commit-msg hook's `Refs:` hint all
find the issue's branch and worktree the same way (`checks.IssueBranch`, `proof.FindIssueRef`):

1. the branch its `branch:` frontmatter field names, when that branch exists
   (`issue-cli set-meta <slug> --key branch --value <name>`);
2. `work/<slug>`, when it exists. A project with `worktree: true` always stops here;
3. the recorded branch head or merge sha of a branch since deleted (the stats sidecar);
4. the one linked worktree whose branch ends in the slug (`<anything>/<slug>`, or its last segment is the slug's
   last segment: `work/ocena-jakosci` for `testy/ocena-jakosci`), else the one whose `HANDOFF.md` names the slug as
   a whole word. Two or more matches at one step are ambiguous: none is used, and the message lists them.

The worktree is `git worktree list`'s entry for the found branch, whatever its folder is called, else
`.worktrees/<slug>`. Steps 1 and 4 are for projects without `worktree: true`, whose workers make their own worktree;
their briefing names `work/<slug>` in `.worktrees/<slug>` (a `## Branch` section,
[Agent Dispatch](../agent-dispatch.md#per-issue-worktrees)).

When nothing matches, the report and the rule say what was tried and how to fix it, instead of
"not checked: no work branch":

```text
no branch for testy/ocena: looked for a branch: field (unset), work/testy/ocena (no such branch), a worktree whose
branch ends in ocena or whose HANDOFF.md names testy/ocena (none of 3). Fix: git branch -m <your-branch>
work/testy/ocena, or issue-cli set-meta testy/ocena --key branch --value <your-branch>
```

A test or commit link then reads `not checked: no branch found`, with that text as its fix.

## What the engine checks

| Link | Passes when | Otherwise |
|:--|:--|:--|
| test | the name (one word, or a quoted title as one phrase) is a whole word in a test file on the branch (`git grep -w` at the branch head, each name looked up on its own; paths such as `_test.`, `test_`, `.test.`, `.spec.`, `tests/`, `*Tests.cs`, or a `.rs` file with a test attribute such as `#[test]` or `#[cfg(test)]`, where Rust keeps its unit tests inline), **and** a `tests:` comment is newer than the branch's last code commit (see below) | not found · stale (code changed since the last tests: comment) · no tests: report |
| step | the step's latest verdict (pass or fail, any mode) is pass, in a run of one of the issue's own sessions (`issue:` names the issue) | failed · not run |
| commit | a trailer's commit; or, for `prove --commit`, the sha is reachable from the work branch or its merge, or a branch commit has its patch (amended or rebased) | not found |
| value | always; shown as "reported" | — |
| shot | the file exists in the worktree or the project checkout | missing |
| lead | the lead's newest recorded run of the command passed, after the branch's last code commit (or at a commit holding it) | waits on the lead: `<command>` · … (last run failed …) · … (ran before the last change) |

A criterion is **proven** when one of its links passes. Any kind counts on its own, a value included (D-1 of
`workflow/issue-cli-prove-link-each-criterion-to-its-proof`).

**The last code commit** is the newest of the branch's own first-parent commits that changed code. Two kinds don't count, so they never make a `tests:` report stale:
- merges of main into the branch;
- non-code commits, where every file is under `docs/`, `playtest/sessions/`, `scripts/checks/`, `issues/`, `retros/` or `sessions/`, a Markdown file (`*.md`), or a `CHANGELOG` in any folder (`ui/evidence-tab-proof-shots-and-criteria` D-6, widened by `cli/prove-run-runs-the-trailers-tests-and-posts-the-tests-comment` D-3). The docs commit an agent makes in the documentation status, a playtest session or a guided check script added after the tests would otherwise make every test link read stale. Other non-Go files still count: templates, static assets, testdata and YAML read through `go:embed` change what a test sees.

**An amended commit keeps its report.** Amending or rebasing a commit gives it a new sha and a new commit time, so a `tests:` comment posted before it would read stale by time alone. When the newest `tests:` comment names a commit (`prove --run at <sha>`, or any sha-shaped word in it), the report still holds if the last code commit is that commit or under it, or if a branch commit from the last code commit on has the named commit's `git patch-id --stable`: a message-only amend matches, an amend that changes code doesn't. The test link then reads "reported for the last change", and the Evidence tab counts the tests as after it. A `prove --commit` link to a sha no longer on the branch is found the same way. Simplest still: without a merge of main, put the `Refs: <slug>` line in the last code commit before running the tests, so nothing is amended afterwards. In a dispatched worker's session the commit-msg hook adds that line to the branch's first commit itself.

**Posting the report: `prove --run`.** In a Go project, `issue-cli prove <slug> --run` writes the `tests:` comment for
you. It takes every test link of the issue (trailers and `prove --test`), finds the package of each in the issue's
worktree (the tracked `_test.go` that declares `func <Name>(`; a sub-test `TestA/case` runs `TestA`), runs one
`go test -json -run '^(…)$' <packages>` there, and posts
`tests: prove --run at <sha>: <command> — N passed: <names>` only when every named test passed. A failing,
skipped, not-run (its package didn't build) or not-found name prints its output and posts nothing; the command exits
non-zero. A name that is not found is **superseded**, not a failure, when only an older commit's trailer names it and,
on every criterion it is linked to, a newer commit's trailer (or a `prove --test` link) names a test that is found: a
test renamed or removed later (an owner decision changed the design) can't be taken out of a pinned commit. It prints
as `– <name> — superseded by <newer> (trailer on <sha>)`, is skipped, and the comment ends with `; N superseded:
<names>`; `prove` lists that link as `superseded by …`. A missing name in the newest trailer still blocks (a typo).
**Without a `go.mod`** (a Node or TypeScript project, say), `prove --run` runs the project's own tests instead: the
default-tier `ci: run:` commands from its `workflow.yaml`, in order, in the issue's worktree, the way local CI runs
them (`sh -c`, the `ci:` nice level and timeout, `CI=true`). A suite has no per-test filter, so each linked name only
has to be in a tracked test file as a whole word (the test check's own lookup, shown as `✓ <name> (<file>)`); when
every command exits 0 the comment is posted with the command, `npm ci && npm test` say, and when one fails every name
fails with the output's tail and nothing is posted. A name in no test file is not found and blocks, green suite or
not. The worktree is the issue branch's own, wherever `git worktree list` has it; `.worktrees/<slug>` is only the
fallback.
It refuses, with the manual `issue-cli comment <slug> --text "tests: ..."` as the way out, when the issue has
no test links, no worktree, or neither a `go.mod` nor a `ci:` command; it refuses uncommitted code in the worktree (commit first: that commit
would stale the report anyway) and runs `mem-guard check` first when `mem-guard` is on PATH. `--json` adds a `run`
object (`command`, `head`, `packages`, `results` with each name's `outcome` and output tail, `posted`, and `suite: true`
for a `ci:` run). The "no tests: report" and "stale" hints under a test link name `prove --run` in a Go project or
one with a `ci:` command. So the order is: the test commit with
its `Proves:` trailer, any further code commits, then `prove --run`.

**Limits of the test check** (D-6): the engine checks that the test exists and that the report is newer than the
code. It does not check:
- that the commit carrying the trailer touched the test;
- that the match is a test definition rather than a mention;
- that the test passed. The `tests:` comment is free text, so "passed" is the agent's report (a `prove --run`
  comment is posted only after go test said so).

`prove --shot` is for projects with no playtest runner. It is refused when the workflow records the
`playtest-sessions` feature and the project has a runner behind it (`playtest_command:`, a
`playtest/sessions/*.yaml`, or a run under `.playtest/runs/`), or when the criterion's text doesn't end in `(visual)`. Each criterion has at most one
shot, and a new one replaces the old.

## Proof that waits on the lead

Some proof needs an act only the lead can do. A guided check script that walks `dispatch`, `lead spawn` or
`department add` is refused from a worker's shell by the worker guard, so the worker can't run it, and a
`question:` comment doesn't reach the lead's tick. The worker links the criterion to the command instead and
stops:

```bash
issue-cli prove <slug> AC2 --lead "issue-cli check-script <slug> --yes"
```

The criterion then reads **waits on the lead: `<command>`** in `prove --list`, in the `criteria_proven` gate's
message (in its own "Waits on the lead" block, which says not to post a question), on the Station (a `lead` tag on
the criterion) and on the Evidence tab. The lead's tick, `issue-cli queue next [--start]`, lists every open issue's
waiting criteria before its verdict:

```
Waits on you (1):
  workflow/foo AC2: issue-cli check-script workflow/foo --yes (never ran)  → issue-cli prove workflow/foo --lead-run
```

The Report's Queue card counts them, and `status.md`'s `Queue:` line adds `N waiting on the lead`. `queue next
--json` adds `waits_on_lead`. The tick reads only the data sidecars, never git, so it lists a link whose command
never ran, last failed, or last ran before the link was made. The gate's own check also knows the branch's last
code commit.

The lead runs `issue-cli prove <slug> --lead-run [<AC-id>…]`, which:
- refuses a worker (the guard's identity), and a department lead outside its department;
- runs `mem-guard check` first;
- runs each waiting command once, as this issue-cli with `--project` set, from the project checkout (not the
  worktree, which would read as a worker);
- records each run in the sidecar under `lead_runs` (command, time, the worktree's HEAD, exit code, who, the last
  20 output lines with the home folder written as `~`);
- prints the report and ticks what is proven. A failed run prints its tail and exits 1.

**The command is checked when it's linked, and never run through a shell.** The worker writes it and the lead's
rights run it, so it is split into words (quotes group them; `;`, `|`, `&`, `$`, backquotes, redirections and
globs are refused) and must be one of:
- `[NAME=value …] issue-cli check-script <slug> [--yes] [--cli <path in the worktree>]`
- `[NAME=value …] issue-cli prove <slug> --run`

Both must name the issue's own slug, and neither names a project: `--project` is refused (the runner sets the
lead's). Env names must be `NAME=value` in capitals. Names that change how a program is found or run (`PATH`,
`HOME`, `SHELL`, `BASH_ENV`, `ENV`, `IFS`, `ISSUE_CLI`, `LD_*`, `DYLD_*`, `GIT_*`, `GO*`, `TMUX*`, `XDG_*`,
`ISSUE_VIEWER_*`) are refused.

A lead link holds the gate under YOLO like any unproven criterion: the lead is around, unlike the owner. The
worker guard's refusals for `dispatch` and `lead spawn` end with the `prove --lead` line, since that's when a
worker needs it. Source: workflow/a-proof-that-waits-on-the-leads-act-shows-as-waits-on-the-lead-command-on-the-leads-tick-and-the-station
(the retro sweep 2026-11-02, I and O).

## Proof after the install

Some criteria can only be proven by the installed build: "the installed issue-cli works in every project", or a
number measured over the issues that ran after the change reached agents. A worker never installs, and the lead
installs only main, so the worker can't prove such a criterion before landing. Held at testing → human-testing, it
cost a lead round (2026-11-19); an unlanded branch build installed to prove one emptied a production checkout's
`node_modules` the same evening.

The briefing text handles it without an engine marker:

- **Design:** the criterion names its check, `- [ ] AC4 Every project's lead sees the new line (after install:
  issue-cli check-script <slug> --yes)`. A measure-after window adds T0 (the install of main with the change), T1
  (when the window ends) and what to revert on no gain.
- **Testing:** the worker records it, `issue-cli prove <slug> AC4 --value "after install: <command>"`, with a
  provisional decision saying so. The `criteria_proven` gate lists it as reported and moves on; its hint names the
  form.
- **The lead:** after installing a build of main, `issue-cli search "after install:"` lists the issues whose
  criteria name a check. The lead runs each landed one and posts `tests: after install of <version>: AC4 pass|fail`
  on the issue; a failure is a bugfix issue, a measure-after window a queue item or a job due at T1.

Two things keep such a check from staying prose. Lint check `after-install` (and `issue-cli transition` on the move
into shipping) warns on a Test Plan line opening with "After install" or "Live:" that names no `after install:`
criterion, with the `check-add` fix. And a change to a guard the installed issue-cli runs (the commit-msg hook, the
pre-push guard, the leak scan) needs no criterion at all: once the installed build holds the commit, `lead tick` says
it in one `after install:` line with the guard's live check and the comment to post (queue sweep 2026-11-21, C6).

A criterion that only the lead can run before landing (a check script the worker guard refuses) is the lead link
above, not this. An engine-read `(after landing)` marker that the gate and the lead's tick know is the owner's
experiment (post-mortem 2026-11-20 A4). Source: the retro sweep 2026-11-20, N3.

## The command

```bash
issue-cli prove <slug> <AC-id> --test <TestName>       # every link flag is repeatable
issue-cli prove <slug> <AC-id> --step <TP3>            # a step of one of the issue's own sessions
issue-cli prove <slug> <AC-id> --commit <sha>          # stored as the full sha; refused when unknown
issue-cli prove <slug> <AC-id> --value "690 vs 380"
issue-cli prove <slug> <AC-id> --shot <path>           # see above
issue-cli prove <slug> <AC-id> --clear                 # removes this criterion's prove links
issue-cli prove <slug> [--list] [--json]               # every criterion, its links and checks
issue-cli prove <slug> --run [--json]                  # run the test links' tests, post the tests: comment
issue-cli prove <slug> <AC-id> --lead "<command>"      # a command only the lead can run; see above
issue-cli prove <slug> --lead-run [<AC-id>…]           # the lead: run them, record each run
```

A step link resolves from the issue's own sessions only: proof reads the runs
of the sessions whose `issue:` is the issue, never another issue's runs. So
`prove --step` refuses a step id that only another issue's session has, naming
that session and its issue: put `proves:` on a step of the issue's own
session, or record the other session's result with `--value`. A step id no
session has yet is saved (the session may be written later).

The list shows each criterion as `proven` or `UNPROVEN`, and under it each link with where it came from and its
check result (with a `→` fix line when it fails).
- After the criteria it lists the ways to prove the unproven ones once, then any link naming a criterion the issue
  doesn't have ("Unknown criterion AC9").
- A prove link whose criterion was rewritten or renumbered since is marked `[criterion text changed since linked]`.
- `--json` returns the report: `criteria[]` with `links[]` and each link's `check`, plus `strays`, `missing`,
  `proven`, `total` and `ticked`.

## Ticking

`prove`, `prove --list` and the transition that runs `criteria_proven` tick every proven criterion that is still
unticked. A ticked box whose proof stops passing is reported, not unticked.

### Criteria a command can fail

A criterion is worth its box when something could fail it. Write each one so it names its proof: a test
(`The lint flags a vague box (TestCriteriaFlags…)`, `(tests with fixtures)`), a command in backticks
(`` `issue-cli status` names the issue``, "exits 1 on a bad flag"), or a playtest step (`(visual)`,
"the playtest step shows the badge"). Lint check `criteria` warns about an open criterion that names none of
these, quoting the vague word it leans on ("works", "is clear", "looks good"); `issue-cli transition` prints
the same warnings when the issue leaves design. They are warnings: the check reads words, so it can misfire,
and it never refuses a move. The owner's boxes (`(owner)`) are skipped: their proof is the owner's eyes.

The proof links stay the gate table: Behaviour is the criterion, Command is its proof (a test, a step, a
commit), Result is that proof's check at `criteria_proven`.

### ## Must not

The development template's idea → in design edge appends `## Must not` after Acceptance Criteria: plain
bullets, one thing the change must not do per line ("Change the issue file format", "Refuse a transition").
They are not checkboxes: nobody ticks them and no gate counts them. A line may name a test or a command in
backticks the way a criterion does (`` Touch projects.yaml (`TestProjectsYAMLUnchanged`) ``); the fresh critic
checks the diff against the list and runs what a line names. `tracker.MustNot(body)` reads the lines, without
the placeholder. Lint check `criteria` warns about an issue past design whose `## Must not` still holds only
the placeholder: write what the change must not do, or `- nothing beyond the scope above`. Each item past
design ends with the criterion that fails when it is broken, `→ AC2` (`->` reads the same; several: `→ AC1,
AC3`), or says why no criterion does, `→ none: covered by the identity gate`; lint check `criteria` warns
about an item with neither, an empty `→ none:`, and an `→ ACn` naming a box the issue doesn't have
(`- nothing beyond the scope above` needs no pair). The in-design prompt says so. Catalog entry
`must-not-section` adds it to an older workflow file (`issue-cli workflow upgrade`).

### Owner checks

Some criteria only the owner can prove. A criterion is the owner's when:

- it links a playtest step with `judgement_step: true` (by `proves:` or `prove --step`);
- a guided check step names it with `--box`, and that script's newest machine run (`check-script --yes`) left
  the step to the owner: `needs your eyes` or `skipped`;
- it is marked for the owner: `(owner)` anywhere in its text (`(owner) The owner picks …`, `… (owner)`), outside
  quotes and backticks (`"(owner)"` names the marker), or it starts with `Owner ` and names a
  check, read-through, review or decision (`Owner visual check of the board (after ship)`, `Owner decides the
  price`), unless those words name a status or a gate: followed by an arrow or by status, gate, stage, step,
  column or transition (`owner review → publish stays owner_only` is not the owner's box). One rule (`tracker.IsOwnerMarked`, behind `proof.OwnerBoxWhy`) for every path: proof, the tick and
  check-edit refusals, the YOLO queue, the owner-checks page, lint.

Write an owner criterion as `- [ ] The board reads well on a phone (owner)`. Lint check `owner-marker` warns on
an open criterion that reads like the owner's check ("the owner confirms it reads well") with no marker.

Only the owner ticks such a box: a passing judgement step (their playtest) ticks it, and so does the owner
ticking it by hand. A `Proves:` trailer, test, value or commit never does. `prove --list` shows
`owner check (<why>): only the owner ticks it`. With YOLO off, the gate still counts every link as before.
When a judgement step is what makes a criterion the owner's while a test or commit link proves it too,
`prove --list` and the transition into `criteria_proven` print `Warning: AC1 is the owner's only because
judgement step TP4 (session <s>) lists it, while test TestX (…) proves it too: drop AC1 from that step's
proves: …` (`Report.Overlaps`, `overlaps` in `--json`). A warning, not a gate.

Which steps are judgement steps, and which criteria a step proves, comes from the newest run of each session
that has the step: an older run's `judgement_step` or `proves:` never outlives the session file that dropped
or retargeted it (a step moved from `proves: [AC2]` to `[AC6]` links AC6 only once ux-test runs again). A
`-step` run of other steps leaves a step's own newest run in place. When an agent's `check` is refused on a box
a judgement step made the owner's, the refusal says `TPn is a judgement step (do:/expect: in <session>); drop
them if the step is automated, then re-run make ux-test`.

Under YOLO (not for experiments) `criteria_proven` skips unproven owner checks, and the transition leaves each
unticked owner check (and any other box that is the owner's: one a guided check left to them, a box whose id is a
playtest judgement step (step id = box id, as `TP7 … (owner's look)`), a `## Human Testing` box, a box marked
`(owner)`) in the after-the-fact queue, by name.

Every open owner box of an epic and its children is on one page, `/p/<project>/owner-checks?epic=<slug>`
([UI](../UI/overview.md#owner-checks)), with its shot; only the owner's browser can tick there.
See [YOLO Mode](yolo.md#owner-checks).

#### Agents never tick an owner box

One rule in `internal/proof/ownerbox.go` says which boxes are the owner's (`OwnerBoxWhy`: a Human Testing box, a
box marked `(owner)`, an owner-only criterion, a box a guided check or a playtest judgement step leaves to the
owner) and whether a write would tick one (`OwnerTicks`: ticked after, open or missing before). Every path that
ticks or rewrites a box asks it, and refuses an agent; nothing is written and the refusal points at the Owner
checks page:

| Path | Agent | Owner |
|---|---|---|
| `issue-cli check` (ids, text, `--index`, `--section … --all`), `append`, `replace`, `update --body` | refused | ticks |
| `issue-cli check --na`, `uncheck`, `check-remove`, `check-edit` dropping `(owner)` | refused | allowed |
| `prove`, `prove --run`, the `criteria_proven` transition (`TickProven`) | an owner box ticks only from the owner's own playtest (a judge-mode pass of its judgement step) | same |
| `playtest report -apply` (`make playtest-report APPLY=1`) | machine steps tick; the owner's judgement steps go in a call of their own, which `check` refuses | ticks both |
| the viewer's box toggle and body save | 403 unless the request is from the owner's browser on the viewer's page | ticks |
| Owner checks page tick | 403 for curl and agents | ticks, adds `owner-check:` |

An agent is what the worker guard sees: a dispatched worker or lead, or any Claude Code session (`CLAUDECODE`).
In the viewer, it is a request without the browser's same-origin headers. Workers never untick an owner box an
agent ticked either; the owner unticks or keeps it.

`check` by several ids from an agent ticks the agent's boxes and leaves each owner box open with a
`✗ Not ticked, the owner's` line (so does `transition --check`); it fails only when it ticked nothing. Box
listings (`checklist`, `show`, `start`, a transition's checklist) mark each owner box `(owner's)` from the same
`OwnerBoxWhy`, so an agent sees the box is the owner's before it tries.

While the gate covers an issue, `issue-cli check <slug> AC2` (by id, text, index or `--section … --all`) is
refused, with a pointer to `prove --list`. Ticking a box in the web UI from the owner's browser stays allowed, because the owner decides.
`issue-cli checklist` adds a line with the count ("2/3 proven").

## The gates

Two validation rules:

- `criteria_checkboxes: Acceptance Criteria` runs at `in design → backlog` and `in design → in progress`. The
  section must have checkboxes, every list item in it must be one, and no box may still hold the text the
  workflow's `append_section` put there ("First observable requirement").
- `criteria_proven: since YYYY-MM-DD` runs at `testing → human-testing`. Every criterion needs a passing link.
  - The error names each unproven criterion, why each of its links fails, and the ways to prove it.
    `transition --dry-run` shows it next to the other unmet requirements.
  - Issues `created` before the date are exempt, so issues in flight when a project adopts the feature keep working.
    So are issues without criteria checkboxes (bugfix, tweak). Both pass with no message; `prove --list` says
    when the gate doesn't cover an issue. On an issue with no `## Acceptance Criteria` section, `prove --list`
    prints `no acceptance criteria: nothing to prove` and exits 0, unless the issue's path asks for criteria
    (`criteria_checkboxes` or `section_has_checkboxes` on that section): then it names that transition and the
    `issue-cli append` that adds one. The `Requires:` list under `start` and `transition` leaves the gate out
    while it would pass the issue anyway.
  - Entering the first work status of its path, an issue without criteria hears in the entry guidance where its
    path asks for them, or that they are optional and each one written needs a proof link at the gate, so the
    section is written before the code. An issue without `created:` counts as new (covered). Every way of
    creating an issue writes the date, `issue-cli health lint` flags an undated one, and the Evidence tab's label
    follows the same rule (`ProofRequired`).
  - With no argument (`rule: "criteria_proven"`, as in the bundled template), it covers every issue.

`criteria_proven` needs git and the playtest runs, which the `tracker` package can't reach. `internal/proof`
registers the check with `tracker.SetProofChecker` in its `init`. issue-cli imports it, and the viewer imports it
in `main.go` for transition previews; a program that doesn't import it fails the rule with a pointer to issue-cli.

## Prompts

The feature adds two status prompt lines:

- **`in progress`:** declare each criterion's proof where you write it (`proves:`, `Proves:` trailers).
- **`in design`, `testing` and the lead addendum:** a criterion only the installed build can prove ([Proof after the install](#proof-after-the-install)).
- **`shipping`:** post a `risk:` comment for anything to watch after the ship, or "risk: none".

## Code

- `internal/proof/`: `Collect(root, issue, comments)` builds the report (criteria, links, checks); `GateError`
  and `TickProven` work from it. The Evidence tab reads the same report. owner.go: owner checks (`Owner`,
  `Tickable`, `OwnerWaiting`, `YOLOGateError`).
- `internal/tracker/proof.go`: the `ProofLink` sidecar type, `Criteria`, both rules, `ProofGateSince`,
  `ProofRequired`, `TransitionChecksProof`.
- `cmd/issue-cli/cmd_prove.go`; the refusal is in `cmd_check.go`, and ticking before the gate in `cmd_transition.go`.
- `cmd/playtest`: `proves:` on `Step` and `StepResult`; `Validator.Checklist` returns each id's section.
