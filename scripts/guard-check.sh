#!/usr/bin/env bash
# Runs the repo-wide guard tests listed in scripts/guard-tests.txt in one
# go test, so a branch that adds a command, a rule or a doc number without its
# docs line goes red in about a minute instead of after the full suite.
#   make guard-check
set -u
root=$(cd "$(dirname "$0")/.." && pwd -P)
cd "$root" || exit 2
list=scripts/guard-tests.txt
pkgs=$(awk '!/^#/ && NF == 2 { print $1 }' "$list" | sort -u)
names=$(awk '!/^#/ && NF == 2 { print $2 }' "$list" | sort -u | paste -sd '|' -)
if [ -z "$pkgs" ] || [ -z "$names" ]; then
  echo "guard-check: $list lists no tests"
  exit 1
fi
# In make validate the suite skips these tests, so their share of
# cmd/issue-cli's coverage is written here (GUARD_COVER_PKG and
# GUARD_COVER_PROFILE, both set or neither) for cover-cli to add.
cover=()
if [ -n "${GUARD_COVER_PKG:-}" ] && [ -n "${GUARD_COVER_PROFILE:-}" ]; then
  cover=("-coverpkg=$GUARD_COVER_PKG" "-coverprofile=$GUARD_COVER_PROFILE")
fi
# shellcheck disable=SC2086 # one package per word
go test $pkgs -run "^($names)\$" -count=1 ${cover[@]+"${cover[@]}"}
