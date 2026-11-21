---
title: "File churn and fseventsd"
order: 40
---

# File churn and fseventsd

On macOS every file written goes into the fseventsd journal under
`/System/Volumes/Data/.fseventsd`, and fseventsd replays old journal files
to every client that asks for history (Spotlight, Time Machine, editors,
file watchers). The more the factory writes, the bigger the journal and the
more CPU fseventsd takes, idle or not. On 2026-11-19 it ran at 76-88% CPU
with a journal of 90,429 files and 3.1 GB, and that held worker starts
(mem-guard and the machine status read it as load).

Most of the writes were `node_modules`:

| What | Files per time (measured 2026-11-19) | Now |
|------|------|------|
| Local CI's `npm ci` in `.worktrees/.ci` on every main move | 29,276 deleted and written again | 0 while the lock file, package.json and node are unchanged (docs/workflow.md#how-a-run-happens) |
| A worker's worktree with its own install | 29,279 | one link per `node_modules` folder (5 in an npm workspace with 4 packages); its own copy (an APFS clone, then an install over it) only after `issue-cli worktree deps --own` or an install with a changed lock file (docs/agent-dispatch.md#per-issue-worktrees) |
| Merged worktrees waiting for the nightly sweep | 36 folders, 22 GB in one project | removed at the next lead tick after landing |

## What grows over time and what bounds it

The factory runs around the clock, so everything it writes or keeps alive
needs a bound that works beside live workers (none of these holds a
start). One row per place; `clean <area>` is an area of the nightly clean
(issue-cli clean, docs/workbench-files.md#the-nightly-clean), and a test
fails when the clean gets an area this table doesn't list.

| What grows | Where | Bound | Setting |
|------|------|------|------|
| Go build cache | `GOCACHE` | clean `gocache`: the oldest entries go over the cap | `clean.gocache_cap_gb`, 40 GB |
| Agent transcripts | Claude Code `projects/`, Codex `sessions/` | clean `transcripts` | `clean.transcript_days`, 14 days |
| Temp leftovers of the factory and its tests | `$TMPDIR` | clean `tmp` (old, unheld, the factory's names only); every test binary runs on a private `$TMPDIR/dft-*` that is removed at exit and fails the binary when a test left anything (internal/tmuxguard) | `clean.tmp_hours`, 24 h; `clean.tmp_prefixes` for other projects |
| Health snapshots | `.health/snapshots` | clean `snapshots`: one a day past the window | `clean.snapshot_days`, 7 days |
| Worker worktrees | `.worktrees/` | removed at the lead tick after landing; clean `worktrees` sweeps the rest | worktrees prune's rules |
| Playtest runs | `.playtest/` in each worktree | clean `playtest` | playtest prune's rules |
| Test tmux folders | `/tmp/tmg-*` | each test binary's guard sweeps stale ones at start (`tmuxguard.SweepStale`) | none |
| Resource trace | `trace/resources.jsonl` in the state dir | rotated to `.1` at 10 MB (internal/restrace) | off unless `DF_TRACE=resources` |
| CLI logs | `.agent-logs/<agent>/<agent>.clilog` | each rotated to `.1` at 10 MB; finished agents' folders stay (70 MB on 2026-11-21) | none |
| Local CI | `.ci/` | 50 run records per tier; red logs: 10, none over 30 days | none |
| fseventsd readings | `machine/fseventsd.json` | the last 6 readings | none |
| Long-lived processes' RSS | `machine/rss.json` | the last 26 h | none |
| Clean results | `machine/clean.json` | the last 14 and the last hold | none |
| `viewer.log` | the state dir | not bounded: it grows only on viewer starts and errors (44 KB on 2026-11-21) | none |
| A lead session's memory | tmux `lead-<project>[-<dept>]` | rotation: the nightly flush rotates the CEO and every department and group lead that ran 20 h; `lead rotate` past 300k tokens; `issue-cli health`, Needs you and the tick name one over 26 h, and a `lead-*` session no project owns | the `lead-flush` job and its standing start |
| A worker session's memory | tmux `agent-*` | the worker cap; the lead tick closes finished sessions and idle ones that wait on the owner; `issue-cli health` names an idle worker holding a slot | the worker cap (auto-start's window or `queue limits`), mem-guard |
| A long-lived process growing | the viewer, each lead's pane, `issue-cli ci run` | `issue-cli health`, Needs you and the tick name one whose RSS more than doubled in 24 h (over 256 MB) with the command that restarts or rotates it | none |
| The machine's memory | every process | mem-guard: no start at 75%, the watchdog pauses the largest heavy process at 82% | mem-guard's own |

## Measuring

`scripts/fs-churn.sh` counts what a step writes:

```sh
scripts/fs-churn.sh mark /tmp/ci.stamp
issue-cli --project <p> ci run
scripts/fs-churn.sh count /tmp/ci.stamp <root>/.worktrees/.ci     # files changed by the run

scripts/fs-churn.sh mark /tmp/wt.stamp
issue-cli --project <p> worktree add <slug>
scripts/fs-churn.sh count /tmp/wt.stamp <root>/.worktrees/<slug>  # files a worker start wrote

scripts/fs-churn.sh tree <root>/.worktrees/.ci/node_modules       # what a full install writes
```

The journal is root's. Its growth over a day is two readings 24 hours apart,
run by the owner:

```sh
sudo sh -c 'du -sh /System/Volumes/Data/.fseventsd; ls /System/Volumes/Data/.fseventsd | wc -l; date'
```

`scripts/fs-churn.sh journal` prints that line when run without root.

## Option: a volume without a journal for `.worktrees`

Not applied; for the owner to decide. APFS keeps one fseventsd journal per
volume, and a volume whose root holds `.fseventsd/no_log` keeps none. The
worktrees could live on their own volume in the same container (it shares
the free space, no partitioning):

```sh
sudo diskutil apfs addVolume disk3 APFS Worktrees -mountpoint /Volumes/Worktrees
sudo mkdir -p /Volumes/Worktrees/.fseventsd && sudo touch /Volumes/Worktrees/.fseventsd/no_log
sudo chown "$USER" /Volumes/Worktrees
# per project, with no live worker in it:
git -C <root> worktree list            # note the trees
mv <root>/.worktrees /Volumes/Worktrees/<project> && ln -s /Volumes/Worktrees/<project> <root>/.worktrees
git -C <root> worktree repair /Volumes/Worktrees/<project>/*
mdutil -i off /Volumes/Worktrees       # Spotlight off there too
```

(`disk3` is the container: `diskutil list` names it, the one holding
"Macintosh HD - Data".)

Risks:
- No journal means nothing that watches files sees changes there: an
  editor's or a dev server's file watcher (vite, tsc --watch) inside a
  worktree stops reloading, and the viewer's own watchers on those paths
  too. Watchers on the main checkout and `issues/` are not affected.
- `.worktrees` becomes a link: git stores each worktree's absolute path,
  so a tree moved without `git worktree repair` loses its record, and the
  sweep and `worktrees prune` see it as gone. Paths printed by the engine
  (`/Volumes/Worktrees/…` after a resolve) differ from `<root>/.worktrees/…`
  in logs and tests that compare them.
- Time Machine skips no volume by itself: exclude it, or the backups grow
  with every checkout.
- If the volume is not mounted (a boot without it, a disk error), every
  worktree is missing at once; dispatch makes them again from their
  branches, but uncommitted work in them is lost.
- `no_log` is undocumented by Apple beyond the man page of fseventsd and
  has been honoured since 10.5; a macOS update could change it.

The three changes above remove most of the churn without any of this; the
volume is worth it only if the journal still grows after them.
