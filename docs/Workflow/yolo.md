---
title: "YOLO Mode"
order: 4
---

# YOLO Mode

YOLO mode is the strongest switch in the engine. For a window the owner chose, `require_human_approval` gates pass without the owner. The gates don't go away: each pass is marked on the issue, and the owner reviews it after the fact.

It skips only the human. CI, agent tests, proofs, agent code review, retros and the agents' permission prompts stay on. YOLO starts no agents: it only stops gates from blocking the ones already running.

## Turning it on

YOLO is always turned on with an end and the owner's words:

- in the viewer, from the company level (L1) of the Workshop Board: the dialog lists the gates that will pass, what still guards, the duration and the valves;
- or by the project lead, when the owner asks in lead chat:

```bash
issue-cli yolo on --until 08:00 --quote "go yolo till morning"   # until morning (the default in the dialog)
issue-cli yolo on --until 4h --quote "…"
issue-cli yolo on --until off --quote "…"                          # until turned off: spelled out on purpose
```

Workers can never turn it on, extend it or review passes. `--no-ci-valve` drops the CI valve, and `--allow-releases` unlocks `release publish`.

`issue-cli yolo status`, `extend --until …`, `off [--reason …]`.

State lives in the main checkout under `.yolo/` (ignored by git, like `.ci/`): `.yolo/<project>.json` holds the current window (since, until, who, the words, valves), and `.yolo/<project>.log.jsonl` the closed ones with why they ended (`expired`, `valve: CI red 34 min`, `turned off by …`).

## What passes, and what still waits

While YOLO is on, `issue-cli transition` passes a gate when all of these hold:

- the step is forward along the issue's path (not backwards);
- the target status is not optional (diverting to `parked` still needs the owner);
- the approval is not Human Testing's own (`approve --to human-testing` keeps refusing);
- nothing else is missing: every checkbox, proof, field and shell gate still applies.

The pass writes the marker on the issue, the same shape as any approval comment:

```
approval: "YOLO" (yolo · agent-shop · from in design, 2026-10-29) → backlog
```

Stats, the Timeline and the Evidence tab read it as an approval. The rest of the run is unchanged.

At `human-testing → documentation` the worker runs the guided checks and playtests itself (`issue-cli check-script <slug> --yes`, `make ux-test ISSUE=<slug>`) and leaves the evidence; the `YOLO:` line in `start` and `show` says so.

These still wait for the owner: `release publish` (unless unlocked), merging and pushing rules the lead holds, and anything the lead's brief reserves.

## Safety valves

- **CI red for more than 30 minutes** turns YOLO off. The viewer's watcher checks every 30 s, and `issue-cli transition` checks too, so the valve works without the viewer running. The clock starts at the first red run (`red_at` in `.ci/state.json`). Without local CI the valve can't work, and `yolo on` says so.
- **No external releases**: `release publish` refuses while YOLO is on unless it was turned on with `--allow-releases`.
- **The end**: an expired window reads as off everywhere, even before anything writes it down.

## How agents find out

- `start`, `show` and `context` print `YOLO: on until 08:00 (…)` next to the Lead line.
- `transition` prints `YOLO: gate in design → backlog passed without the owner, marked for review after the fact`. Its next step says to run the gated transition instead of stopping.
- `transition --dry-run` drops the approval requirement YOLO would pass and says why.
- When YOLO has just ended, a refused gate says when and why (`YOLO turned off at 07:12 (valve: CI red 34 min)`).
- Decisions made with `decide` and no `--quote` while YOLO is on are recorded with how `YOLO`. The Decisions log shows them as "without your words" (Confirm / Question it).

## Review after the fact

Every pass waits in the "after the fact" queue until the owner reviews it:

- `issue-cli yolo queue` lists them (slug, from → to, by whom, when);
- **Agree** (sidebar, the issue page, or `issue-cli yolo review <slug> --agree --quote "…"`) writes `yolo-review: agreed "<words>" (<how>, <date>) → <status>`;
- **Back to <status>** (viewer only) moves the issue back to the status it left at the gate, with the owner's words, and writes `yolo-review: back to <status> "<words>" …`. The lead and the worker see it on the trail.

The queue is derived from comments: a YOLO approval with no later `yolo-review:` for the same status.

## Where it shows

- Every classic project page: a hazard line in the sidebar while on (until, passed, valves, Extend / Turn off now), and "N after the fact" with Agree / Back until the queue is empty.
- The Workshop Board (yolo_wb.go, templates/_workshop_yolo.html):
  - L1, the company: the **YOLO** switch in the header opens the turn-on dialog (the gates from workflow.yaml, what still guards, until 08:00 / 4 hours / until turned off, the valves, your words). While on, the Product room's gates stand open and the room carries "YOLO · N"; the side panel lists "To review after YOLO". The switch lives only here.
  - Every project screen (company, hall and its tabs, Report, the issue): the hazard band under the header while on ("Gates pass without you · since 22:10 · turns off at 08:00 · passed: N · CI guards · no releases") with Extend (until 08:00, 4 more hours, until turned off) and Turn off now; "N to review after the fact" in the header while the queue waits.
  - L0, the map: the lot in yellow-black stripes with "YOLO until 08:00", and "N after the fact" in place of the orange "!". The counter stays after YOLO ends until the queue is empty.
  - L2, the hall: gates drawn raised (barrier up) while on; a crate that passed a gate in YOLO carries the yellow YOLO marker to the end of the line, reviewed or not.
  - L3, the issue: "Passed in YOLO" above Your move, one form per unreviewed pass, with Agree and Back to <status>.
  - Decisions: a decision recorded in YOLO (how `YOLO`) carries the YOLO tag in the Decisions log ("The agent decided alone in YOLO", Confirm / Question it) and on the issue ("YOLO · without your words").

Yellow-black stripes mean YOLO only; orange stays the owner's colour.

## Endpoints

- `GET /p/<project>/yolo.json`: state, passes, queue.
- `POST /p/<project>/yolo/on` `{until, words, ci_valve, releases}`, `/yolo/extend` `{until}`, `/yolo/off` `{reason}`.
- `POST /p/<project>/issue/<slug>/yolo-review` `{status, verdict: agree|back, words}`.

All writes go through the usual same-origin check.
