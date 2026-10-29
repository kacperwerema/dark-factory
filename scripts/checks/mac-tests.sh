#!/usr/bin/env bash
# issue: factory/mac-tmux-tests
# The owner's manual checks for factory/mac-tmux-tests, one step at a time. Run it with:
#   issue-cli check-script factory/mac-tmux-tests
# The helpers (step, run, expect, ask, summary, sandbox_project, private_tmux, …): issue-cli check-script --lib
# shellcheck source=/dev/null disable=SC2034,SC2154
source "${CHECKS_LIB:?run me with: issue-cli check-script <slug>}"

checks_intro "The tmux, script(1) and /private/var test failures on macOS are fixed in the tests." \
	"This runs make validate on this branch on your Mac: about three minutes, nothing outside the checkout."

repo=$(git rev-parse --show-toplevel)
sandbox_dir LOGS
log=$LOGS/validate.log

# ── TP2 ───────────────────────────────────────────────────────────────
step TP2 'make validate is green on this Mac' --box TP2 \
	--what "Runs make validate (vet, every test, the issue-cli coverage floor, catalog-check, playtest-check) on the issue branch." \
	--why "The Repro: 28 tests failed on macOS (tmux socket path too long, BSD script has no -c, /var vs /private/var)." \
	--look "The lines below: any failing test, then the end of the log" \
	--expect "✓ validate passed, exit 0"
run sh -c 'cd "$1" && make validate >"$2" 2>&1; s=$?; grep -E -- "--- FAIL|^FAIL" "$2"; tail -3 "$2"; exit $s' _ "$repo" "$log"
expect '[ $code = 0 ]' "make validate exits 0"
expect 'grep -q "✓ validate passed" "$log"' "the log ends in ✓ validate passed"

summary
