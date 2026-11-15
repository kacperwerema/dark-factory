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
| Auto-start gap | 5 min | Time between two auto-starts, across the machine | `autostart.DefaultGapMinutes` |
| Health snapshot check | 10 min | How often the viewer looks for a project with no snapshot from today | `viewer.HealthCheckInterval` |
| Tracker sync | 10 min | How often the viewer commits, pulls and pushes each private tracker | `viewer.TrackerSyncInterval` |
