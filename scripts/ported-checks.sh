#!/usr/bin/env bash
# Runs the ported guided check scripts (internal/checks/testdata/ported/*.sh)
# as machine runs, as `issue-cli check-script <slug> --yes` would: this
# checkout's issue-cli built into a temp folder, the library from LIB= (a path
# to a lib.sh) or else this checkout's internal/checks/lib.sh, nothing ticked.
#   make ported-checks [LIB=<path>] [ONLY=<name>]
# Exits non-zero when any script fails.
set -u
root=$(cd "$(dirname "$0")/.." && pwd -P)
cd "$root" || exit 2

bash4=''
for b in bash /opt/homebrew/bin/bash /usr/local/bin/bash /opt/local/bin/bash; do
	p=$(command -v "$b" 2> /dev/null) || continue
	if [ "$("$p" -c 'echo ${BASH_VERSINFO[0]}')" -ge 4 ] 2> /dev/null; then
		bash4=$p
		break
	fi
done
[ -n "$bash4" ] || {
	echo "ported-checks: needs bash 4 or newer (brew install bash)" >&2
	exit 2
}

tmp=$(mktemp -d "${TMPDIR:-/tmp}/ported-checks-XXXXXX") || exit 2
trap 'rm -rf -- "$tmp"' EXIT
go build -o "$tmp/issue-cli" ./cmd/issue-cli || exit 2
lib=${LIB:-$root/internal/checks/lib.sh}
[ -f "$lib" ] || {
	echo "ported-checks: no library at $lib" >&2
	exit 2
}
lib=$(cd "$(dirname "$lib")" && pwd -P)/$(basename "$lib")

failed=()
for f in internal/checks/testdata/ported/*.sh; do
	name=$(basename "$f" .sh)
	[ -z "${ONLY:-}" ] || [ "$ONLY" = "$name" ] || continue
	slug=$(sed -n 's/^# issue: *//p' "$f" | head -1)
	echo "== $name ($slug)"
	if ! CHECKS_LIB=$lib CHECKS_SLUG=$slug CHECKS_PROJECT='' CHECKS_ROOT=$root \
		CHECKS_NAME=$name CHECKS_ISSUE_CLI=$tmp/issue-cli ISSUE_CLI=$tmp/issue-cli CHECKS_YES=1 \
		"$bash4" "$f" < /dev/null; then
		failed+=("$name")
	fi
done
if [ ${#failed[@]} -gt 0 ]; then
	echo "ported-checks: failed: ${failed[*]}" >&2
	exit 1
fi
echo "✓ ported-checks passed"
