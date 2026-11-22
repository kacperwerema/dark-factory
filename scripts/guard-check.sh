#!/usr/bin/env bash
# Runs the repo-wide guard tests listed in scripts/guard-tests.txt in one
# go test, so a branch that adds a command, a rule or a doc number without its
# docs line goes red in seconds instead of after the full suite. Each guard
# test that fails gets a `guard-check: <test>` line after go test's output:
# its rule from the "Guard tests" table in docs/testing.md, the row's
# file:line and the page that says more.
#   make guard-check
#   GUARD_LIST=<file> scripts/guard-check.sh   # another list (a test's stand-in)
set -u
root=$(cd "$(dirname "$0")/.." && pwd -P)
cd "$root" || exit 2
list=${GUARD_LIST:-scripts/guard-tests.txt}
table=docs/testing.md
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
out=$(mktemp "${TMPDIR:-/tmp}/guard-check.XXXXXX") || exit 2
trap 'rm -f "$out"' EXIT
# shellcheck disable=SC2086 # one package per word
go test $pkgs -run "^($names)\$" -count=1 ${cover[@]+"${cover[@]}"} 2>&1 | tee "$out"
rc=${PIPESTATUS[0]}
[ "$rc" -eq 0 ] && exit 0
# A top-level `--- FAIL: TestX` line (subtests are indented) of a listed test.
failed=$(sed -n 's/^--- FAIL: \(Test[A-Za-z0-9_]*\).*/\1/p' "$out" | sort -u)
for t in $failed; do
  case "|$names|" in *"|$t|"*) ;; *) continue ;; esac
  # The table row: | `TestX` | rule | [page](link) |
  row=$(awk -v t="$t" -F'|' 'index($2, "`" t "`") { print NR "\t" $3 "\t" $4; exit }' "$table")
  if [ -z "$row" ]; then
    echo "guard-check: $t failed; it has no row in $table's Guard tests table (add one)"
    continue
  fi
  line=${row%%$'\t'*}
  rest=${row#*$'\t'}
  rule=$(printf '%s' "${rest%%$'\t'*}" | sed 's/^ *//; s/ *$//')
  more=$(printf '%s' "${rest#*$'\t'}" | sed -n 's/.*](\([^)]*\)).*/\1/p')
  case "$more" in
  "#"*) more="$table$more" ;;
  ?*) more="docs/$more" ;;
  esac
  echo "guard-check: $t failed. Rule: $rule ($table:$line${more:+; more: $more})"
done
exit "$rc"
