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

While YOLO is on, `issue-cli transition` (and `issue-cli start` on a backlog or other handoff issue) passes a gate when all of these hold:

- the step is forward along the issue's path (not backwards);
- the target status is not optional (diverting to `parked` still needs the owner);
- the approval is not Human Testing's own (`approve --to human-testing` keeps refusing);
- the issue is not labelled `experiment`: an experiment's change is adopted by the owner, so every gate of it waits (the refusal and `transition --dry-run` say so; see [Experiments](../workflow-stats.md#experiments));
- the gate is not `owner_only` (see [Gates YOLO never passes](#gates-yolo-never-passes));
- nothing else is missing: every checkbox, proof, field and shell gate still applies.

The pass writes the marker on the issue, the same shape as any approval comment:

```
approval: "YOLO" (yolo · agent-shop · from in design, 2026-10-29) → backlog
```

Stats, the Timeline and the Evidence tab read it as an approval. The rest of the run is unchanged.

At `human-testing → documentation` the worker runs the guided checks and playtests itself (`issue-cli check-script <slug> --yes`, `make ux-test ISSUE=<slug>`) and leaves the evidence; the `YOLO:` line in `start` and `show` says so.

These still wait for the owner: `owner_only` gates, `release publish` (unless unlocked), merging and pushing rules the lead holds, and anything the lead's brief reserves.

## Gates YOLO never passes

A review after the fact can't take back money spent or a post that went out. A step that spends money or publishes outside the machine sits behind a gate marked `owner_only`, and YOLO never passes it:

```yaml
transitions:
  - from: "owner review"
    to: "publish"
    actions:
      - type: "require_human_approval"
        status: "publish"
        owner_only: true
```

- With YOLO on, `transition` and `start` refuse it: "YOLO is on, but this gate is the owner's even in YOLO: the step spends money or publishes outside the machine … Stop here and tell your lead." `transition --dry-run` keeps the approval among what's missing and says the same, and the Next hint in `start` / `show` stops saying YOLO will pass it. Nothing is written on the issue.
- The issue keeps waiting on that approval, so it is in Needs you like any gate. The owner approves it as today: in the viewer (the issue, or Needs you's bulk approve), or the CEO relays their words with `issue-cli approve <slug> --to <status> --quote "…"`. It is not a YOLO pass, so it doesn't go in the after-the-fact queue.
- With YOLO off, `owner_only` changes nothing: it is an ordinary approval.
- `owner_only` belongs on the `require_human_approval` action; on any other action it does nothing (lint says so). A type or system overlay can add it to an edge, and `replace: true` on an overlay edge without it drops it, so lint reads each type's merged path.

The gates YOLO never passes, listed by `issue-cli yolo status` (`never_passes` in `--json`), `yolo on`, the turn-on dialog ("Yours even in YOLO"), the sidebar and hazard bands ("never passes: N", the list on hover) and `yolo.json`:

- each `owner_only` gate on the paths of Product's workflow and of every department with its own file, with the types that walk it when not all do;
- the Human Testing approval;
- every gate of an issue labelled `experiment`;
- `release publish`, while releases are locked (the default; `yolo on --allow-releases` unlocks them for the window, and the valves line says so).

The bundled `content` and `marketing` templates have `owner_only` on `owner review → publish`: every marketing channel (piece, seo-pass, and the ppc / social / cold-outreach slots once uncommented) launches, posts or sends in `publish`. A project on an older copy gets it from `issue-cli workflow upgrade` (catalog entry `owner-only-gates`).

`issue-cli health lint` (check `owner-only`) warns when:

- a path enters a status named `publish`, `launch` or `send` through a gate that isn't `owner_only` (or has no approval at all);
- a department has a connector with `sends: true` (it publishes or spends, [Connectors](../departments.md#connectors)) and the workflow its issues walk has no `owner_only` gate;
- `owner_only` sits on an action that isn't an approval.

## Safety valves

- **CI red for more than 30 minutes** turns YOLO off. The viewer's watcher checks every 30 s, and `issue-cli transition` checks too, so the valve works without the viewer running. The clock starts at the first red run (`red_at` in `.ci/state.json`). Without local CI the valve can't work, and `yolo on` says so.
- **No external releases**: `release publish` refuses while YOLO is on unless it was turned on with `--allow-releases`. `release publish <version> --dry-run` still runs: it writes nothing and lists the YOLO window as what the real run waits on.
- **The end**: an expired window reads as off everywhere, even before anything writes it down.

## How agents find out

- `start`, `show` and `context` print `YOLO: on until 08:00 (…)` next to the Lead line.
- They also print the YOLO rule, one line: don't ask the owner and don't stop at gates; record your answers with `decide <slug> --provisional`, put what you can't answer in a `question:` comment and go on. It names what still waits for the owner in YOLO: experiments, product-policy rules, and anything that spends money or publishes outside the machine (owner_only gates). `start` and `transition` print it in their guidance block in place of the status and entry guidance sentences that say to ask, clarify with or tell the human, or to stop and end the turn (`tracker.YOLOGuidance`); the other sentences print as written. With YOLO off the guidance is unchanged.
- The dispatch prompt follows: under YOLO its "stop at every gate" lines, the start / ask / transition steps and the in design / backlog reminders carry the YOLO wording, and its status guidance goes through the same filter with the rule after it.
- `transition` prints `YOLO: gate in design → backlog passed without the owner, marked for review after the fact`. Its next step says to run the gated transition instead of stopping.
- `start` on a backlog issue passes the gate the same way, judging it as already claimed (in design → backlog clears the assignee), and prints the same line under its AUTO-ADVANCED banner.
- `transition` passes the gate only when the approval is all it still lacks. When something else is open, it refuses with that and not with the approval: after backlog cleared the assignee, `transition --to "in progress"` says `no assignee — re-claim the issue with: issue-cli start <slug>` and `YOLO passes the "in progress" approval once this is done`, and leaves no YOLO approval behind. A gate that YOLO doesn't pass (YOLO off, owner-only) still says `not human-approved`.
- `transition --dry-run` drops the approval requirement YOLO would pass and says why.
- When YOLO has just ended, a refused gate says when and why (`YOLO turned off at 07:12 (valve: CI red 34 min)`).
- Decisions made with `decide` and no `--quote` while YOLO is on are recorded with how `YOLO`. `--provisional` adds the word explicitly (`YOLO, provisional`), with or without YOLO. The Decisions log shows them as "without your words": Confirm records your words on the same D-n (no CEO needed), Question it goes to the CEO. Decisions marked `(provisional…)` in their text get the same box ("Provisional — not confirmed by you yet."), even with a relayed quote.

## Review after the fact

Every pass waits in the "after the fact" queue until the owner reviews it:

- `issue-cli yolo queue` lists them (slug, from → to, by whom, when; an owner check by its box, text and why);
- **Agree** (sidebar, the issue page, or `issue-cli yolo review <slug> --agree --quote "…"`) writes `yolo-review: agreed "<words>" (<how>, <date>) → <status>`;
- **Back to <status>** (viewer only) moves the issue back to the status it left at the gate, with the owner's words, and writes `yolo-review: back to <status> "<words>" …`. The move is the same one Send back for fixes uses (`tracker.MoveBack`), so it works on workflows that declare no backward edge. The lead and the worker see it on the trail.

The queue is derived from comments: a YOLO approval with no later `yolo-review:` for the same status.

### Owner checks

A box only the owner can check (see [Proof Links](proof.md#owner-checks): a playtest judgement step, a guided
check step the machine run left to the owner, a criterion marked `(owner)`) neither blocks YOLO nor passes
silently:

- `criteria_proven` at `testing → human-testing` doesn't count an unproven owner check; every other criterion
  still needs its proof.
- The box stays unticked: no trailer ticks it. The transition writes, once per box while it waits,
  `yolo-owner-check: AC6: owner check, not done (playtest judgement step TP9) "<box text>" · from testing`
  and prints the same line.
- It waits in the queue as "AC6: owner check, not done" (`yolo queue`, the sidebar, the company's list, Needs
  you, and on the issue under Passed in YOLO with its text and why). It leaves the queue when the box is
  ticked, or on the owner's review: Agree (`yolo review <slug> --agree --status AC6 --quote "…"`) or Back to
  the status it left.

Boxes outside the criteria that are the owner's join the same queue: every box in `## Human Testing` (HT1,
"Human verification complete if required") and any box marked `(owner)` (anywhere in its text, or starting `Owner visual check …`; [Proof Links](proof.md#owner-checks)), such as the bugfix path's
Manual box "Repro steps re-run and the bug is gone (owner)". Nothing gates on them, so they are queued when a
YOLO gate passes from `testing` on (an approval gate, the proof or guided-check gate at `human-testing`) and
when the issue reaches `done`. A worker never ticks them.

An owner check also leaves the queue when the owner sends the issue back (Send back for fixes, a `rejected:`
comment by the owner); the next YOLO gate queues it again if the box is still unticked.

`check-script <slug> --yes` ticks nothing either: a passing machine run ends with "Left to the owner: TP4." per
script and lists every box on the issue still the owner's (`Left to the owner on <slug>`), with why.

An experiment's owner checks still block, like its gates. With YOLO off nothing changes.

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
