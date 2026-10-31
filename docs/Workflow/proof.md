---
title: "Proof Links"
order: 3
---

# Proof Links

Every acceptance criterion (`AC1`, `AC2`, … under `## Acceptance Criteria`) is linked to the proof that it works:
a test, a playtest step, a commit, or a measured value. A link points at something that exists. Nothing is copied
into the issue or the repo, and the engine checks every link each time it reads it. A ticked criterion means
"proven", not "the agent said so".

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

- **Trailers** are `Proves:` lines in the commit message, one or more, in any paragraph after the subject (one
  in its own paragraph before `Co-Authored-By` counts too):
  `Proves: AC1, AC3 TestA TestB` links AC1 and AC3 to both tests. Words shaped like a criterion id (the section's
  prefix plus a number) are ids, and the rest are test names, so a test name with spaces (an `it()` title) goes
  through `prove --test` instead. A playtest step id in a trailer (`Proves: AC1 TP4`) is read as a test that
  isn't found. Its fix says so and points at `proves: [AC1]` on step TP4 in its session, or `prove --step`.
  - Criteria ids stay put when a criterion goes: `issue-cli check-remove <slug> AC6` keeps AC6's slot, so AC7's
    trailers still prove AC7. AC6's links become strays.
  - Only the work branch's own first-parent commits count. While the branch is live they are read as
    `main..work/<slug>`, so commits merged in from main never count.
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

## What the engine checks

| Link | Passes when | Otherwise |
|:--|:--|:--|
| test | the name is a whole word in a test file on the branch (`git grep -w` at the branch head; paths such as `_test.`, `test_`, `.test.`, `.spec.`, `tests/`, `*Tests.cs`), **and** a `tests:` comment is newer than the branch's last code commit (see below) | not found · stale (code changed since the last tests: comment) · no tests: report |
| step | the step's latest verdict (pass or fail, any mode) is pass | failed · not run |
| commit | a trailer's commit; or, for `prove --commit`, the sha is reachable from the work branch or its merge | not found |
| value | always; shown as "reported" | — |
| shot | the file exists in the worktree or the project checkout | missing |

A criterion is **proven** when one of its links passes. Any kind counts on its own, a value included (D-1 of
`workflow/issue-cli-prove-link-each-criterion-to-its-proof`).

**The last code commit** is the newest of the branch's own first-parent commits that changed code. Two kinds don't count, so they never make a `tests:` report stale:
- merges of main into the branch;
- non-code commits, where every file is under `docs/`, `playtest/sessions/`, `scripts/checks/`, `issues/`, `retros/` or `sessions/`, a Markdown file (`*.md`), or a `CHANGELOG` in any folder (`ui/evidence-tab-proof-shots-and-criteria` D-6, widened by `cli/prove-run-runs-the-trailers-tests-and-posts-the-tests-comment` D-3). The docs commit an agent makes in the documentation status, a playtest session or a guided check script added after the tests would otherwise make every test link read stale. Other non-Go files still count: templates, static assets, testdata and YAML read through `go:embed` change what a test sees.

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
It refuses, with the manual `issue-cli comment <slug> --text "tests: ..."` as the way out, when the issue has
no test links, no worktree, or no `go.mod`; it refuses uncommitted code in the worktree (commit first: that commit
would stale the report anyway) and runs `mem-guard check` first when `mem-guard` is on PATH. `--json` adds a `run`
object (`command`, `head`, `packages`, `results` with each name's `outcome` and output tail, `posted`). The "no tests:
report" and "stale" hints under a test link name `prove --run` in a Go project. So the order is: the test commit with
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

## The command

```bash
issue-cli prove <slug> <AC-id> --test <TestName>       # every link flag is repeatable
issue-cli prove <slug> <AC-id> --step <TP3>            # a playtest session step id
issue-cli prove <slug> <AC-id> --commit <sha>          # stored as the full sha; refused when unknown
issue-cli prove <slug> <AC-id> --value "690 vs 380"
issue-cli prove <slug> <AC-id> --shot <path>           # see above
issue-cli prove <slug> <AC-id> --clear                 # removes this criterion's prove links
issue-cli prove <slug> [--list] [--json]               # every criterion, its links and checks
issue-cli prove <slug> --run [--json]                  # run the test links' tests, post the tests: comment
```

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
the placeholder: write what the change must not do, or `- nothing beyond the scope above`. Catalog entry
`must-not-section` adds it to an older workflow file (`issue-cli workflow upgrade`).

### Owner checks

Some criteria only the owner can prove. A criterion is the owner's when:

- it links a playtest step with `judgement_step: true` (by `proves:` or `prove --step`);
- a guided check step names it with `--box`, and that script's newest machine run (`check-script --yes`) left
  the step to the owner: `needs your eyes` or `skipped`;
- it is marked for the owner: `(owner)` anywhere in its text (`(owner) The owner picks …`, `… (owner)`), outside
  quotes and backticks (`"(owner)"` names the marker), or it starts with `Owner ` and names a
  check, read-through, review or decision (`Owner visual check of the board (after ship)`, `Owner decides the
  price`). One rule (`tracker.IsOwnerMarked`, behind `proof.OwnerBoxWhy`) for every path: proof, the tick and
  check-edit refusals, the YOLO queue, the owner-checks page, lint.

Write an owner criterion as `- [ ] The board reads well on a phone (owner)`. Lint check `owner-marker` warns on
an open criterion that reads like the owner's check ("the owner confirms it reads well") with no marker.

Only the owner ticks such a box: a passing judgement step (their playtest) ticks it, and so does the owner
ticking it by hand. A `Proves:` trailer, test, value or commit never does. `prove --list` shows
`owner check (<why>): only the owner ticks it`. With YOLO off, the gate still counts every link as before.

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
    when the gate doesn't cover an issue. An issue without `created:` counts as new (covered). Every way of
    creating an issue writes the date, `issue-cli health lint` flags an undated one, and the Evidence tab's label
    follows the same rule (`ProofRequired`).
  - With no argument (`rule: "criteria_proven"`, as in the bundled template), it covers every issue.

`criteria_proven` needs git and the playtest runs, which the `tracker` package can't reach. `internal/proof`
registers the check with `tracker.SetProofChecker` in its `init`. issue-cli imports it, and the viewer imports it
in `main.go` for transition previews; a program that doesn't import it fails the rule with a pointer to issue-cli.

## Prompts

The feature adds two status prompt lines:

- **`in progress`:** declare each criterion's proof where you write it (`proves:`, `Proves:` trailers).
- **`shipping`:** post a `risk:` comment for anything to watch after the ship, or "risk: none".

## Code

- `internal/proof/`: `Collect(root, issue, comments)` builds the report (criteria, links, checks); `GateError`
  and `TickProven` work from it. The Evidence tab reads the same report. owner.go: owner checks (`Owner`,
  `Tickable`, `OwnerWaiting`, `YOLOGateError`).
- `internal/tracker/proof.go`: the `ProofLink` sidecar type, `Criteria`, both rules, `ProofGateSince`,
  `ProofRequired`, `TransitionChecksProof`.
- `cmd/issue-cli/cmd_prove.go`; the refusal is in `cmd_check.go`, and ticking before the gate in `cmd_transition.go`.
- `cmd/playtest`: `proves:` on `Step` and `StepResult`; `Validator.Checklist` returns each id's section.
