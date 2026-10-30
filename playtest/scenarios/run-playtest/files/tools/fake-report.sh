#!/bin/sh
# Stand-in for make playtest-report APPLY=1: leaves a comment the session
# checks for.
issue-cli --project rp comment "$1" "playtest: stand-in report ran"
