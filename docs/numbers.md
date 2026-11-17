---
title: "Numbers"
order: 91
---

# Numbers

The numbers the docs and pages quote, each read from the constant that holds it.

This page is generated: run `go generate ./internal/docnumbers/...` after changing a constant, and don't edit it by hand (`TestDocNumbersInSync`). A sentence elsewhere that quotes one of these numbers has to agree with it (`TestDocNumbersCopiesAgree`).

| Number | Value | What it is | Source |
|---|---|---|---|
| Idle gap | 15 min | With no agent call for this long, an agent counts as idle, or as waiting on you in a status only you can move on | `timeline.IdleAfter` |
| Timeline window | 30 days | The project aggregate reads issues done this recently | `timeline.Window` |
| Health trend | 30 days | How far back the score's trend components compare | `health.TrendDays` |
| Churn window | 30 days | The shape family's churn and hotspots read git this far back | `health.ChurnDays` |
| Shots stay full size | 30 days | After an issue is done this long its playtest shots shrink | `tracker.HealthDefaultShrinkDays` |
| Decisions brief cap | 12 | Lines the decisions-in-force block carries in start, show and the briefings | `tracker.DefaultDecisionBriefCap` |
| Red-round cap | 4 | Fix rounds a check may stay red before it becomes a question for you, when max_red_rounds is unset | `tracker.DefaultMaxRedRounds` |
| mem-guard start limit | 75% | `mem-guard check` refuses at or over this much memory used | `memguard.StartMax` |
| mem-guard pause | 82% | The watchdog pauses the largest heavy process | `memguard.PauseAt` |
| mem-guard resume | 70% | The watchdog resumes; optional work (a benchmark check, an embed) waits for this too | `memguard.ResumeAt` |
| mem-guard hard limit | 85% | Memory use is never to exceed this | `memguard.HardLimit` |
| Auto-start memory line | 50% | Auto-start, the queue and the nightly loop start only while memory used is under this | `autostart.DefaultBelow` |
| Auto-start workers | 2 | Live workers per project auto-start allows when --max-workers is unset | `autostart.DefaultMaxWorkers` |
| Auto-start gap | 5 min | Time between two auto-starts, across the machine | `autostart.DefaultGapMinutes` |
| Start weight floor | 1 | The least start_weight: a project counts with in the turn between projects; unset or lower is this | `autostart.MinWeight` |
| Turn drop-out | 2 gaps | A project whose last ask is older than this many of its gaps drops out of the turn and loses its credit | `autostart.StaleGaps` |
| Start share window | 24 h | How far back the share line counts each project's starts | `autostart.ShareWindow` |
| Queue refill line | 5 | At this many open queue items or fewer the lead's tick says the queue wants refilling | `leadstate.RefillLine` |
| Long hold | 1 h | Starts held this long make the lead's tick say so under --watch, even when each hold clears by itself | `leadstate.HeldLong` |
| Worktree idle | 3 h | A sweep removes a merged, clean worktree only when nothing in it changed for this long | `agentrun.PruneIdle` |
| Health snapshot check | 10 min | How often the viewer looks for a project with no snapshot from today | `viewer.HealthCheckInterval` |
| Tracker sync | 10 min | How often the viewer commits, pulls and pushes each private tracker | `viewer.TrackerSyncInterval` |
