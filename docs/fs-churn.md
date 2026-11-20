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
