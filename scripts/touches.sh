#!/bin/sh
# scripts/touches.sh <path>... prints 1 when the checkout's change since its
# merge-base with main touches any of the paths (a folder ends in /), in
# commits, staged, unstaged or untracked; 0 when it touches none. A move
# counts at both ends. No main or no git: 1, so a gate that can't tell runs
# what it gates. make test gates cmd/playtest's Chrome tests with it
# (PLAYTEST_E2E_PATHS; docs/testing.md#test-tiers).
base=$(git merge-base HEAD "${TOUCHES_BASE:-main}" 2>/dev/null) || { echo 1; exit 0; }
hits=$({ git diff --no-renames --name-only "$base" -- "$@" && git ls-files --others --exclude-standard -- "$@"; } 2>/dev/null) || { echo 1; exit 0; }
if [ -n "$hits" ]; then echo 1; else echo 0; fi
