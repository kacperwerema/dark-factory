# Guided check library: what an issue's scripts/checks/<name>.sh sources.
# issue-cli ships it (`issue-cli check-script --lib` prints it, its parts in
# lib/ inlined) and `issue-cli check-script <slug>` runs the issue's scripts
# with it:
#
#   #!/usr/bin/env bash
#   # issue: <slug>                      ← check-script finds the script by this line
#   source "${CHECKS_LIB:?run me with: issue-cli check-script <slug>}"
#   checks_intro "What changed, in a line or two. About 3 minutes."
#
#   step MV1 "The command answers" --box HT2 \
#     --what "what it runs" --why "why it matters" --look "what to look at" --expect "the expected result"
#   run issue-cli status                 # shows the command, its output and exit code; sets $out and $code
#   expect '[ $code = 0 ]' "status answered"
#   ask "Does the stand-up list the worker?"
#
#   summary --all HT1
#
# Steps
#   step <id> <title> [--box <ids>] [--what <text>] [--why <text>] [--look <text>]
#        [--expect <text>] [--touches-real <what it creates>]
#       Opens a step; the previous one closes (its verdict, then Enter to go on).
#       --box names the checkboxes the step proves (comma or space separated,
#       repeatable); without it the box is the step id. A box passes only when
#       every step naming it passed. The same id twice adds a second part to
#       one step: it passes only if both parts did.
#       --touches-real marks a step that changes the owner's real environment
#       (their tmux session, their config): it lists what it creates and runs
#       only on an explicit y. step returns 1 when the step is skipped, so wrap
#       such a step's own commands:  if step MV9 … --touches-real "…"; then …; fi
#   run <cmd…>          show the command, run it, show its output and exit code;
#                       sets $out (stdout+stderr) and $code
#   expect <cond> <meaning>
#                       a machine check: eval's <cond>, ✓ or ✗; a ✗ fails the step.
#                       $? in <cond> is the exit code of the command before
#                       expect (wait_for_url …; expect '[ $? = 0 ]' …)
#   ask <question>      the owner's y/n (q stops); a n fails the step. In a
#                       machine run the step is marked "needs your eyes".
#   eventually <cond> <meaning> [seconds]
#                       expect, retried every 0.2 s for up to [seconds] (default 10)
#   skip <why>          skips the rest of the step (run/expect/ask do nothing)
#   step_active         true while a step is open and not skipped: guard a step's
#                       own commands with it (if step_active; then …; fi)
#   step_end            closes the open step now (its verdict), before summary
#   checks_default_box <ids>
#                       the boxes of every later step that names none (else its id)
#   keep <file> [name]  copies a file (a screenshot in a sandbox) next to the run
#                       records, .agent-logs/checks/<name>/files/, so it outlives
#                       the sandbox; prints that path and sets $kept
#   show <file>         keeps the file, then opens the copy in the desktop viewer
#                       (xdg-open, or open on macOS) in an owner run, else prints
#                       its path
#   pause               Enter to go on, q stops (interactive runs only)
#   summary [--all <boxes>] [--then <text>]
#                       one line per step, the boxes that passed, a run record
#                       under .agent-logs/checks/<name>/; from a terminal it
#                       offers to tick the passed boxes and to comment the
#                       failures as `checks:`. --all passes its boxes (comma or
#                       space separated, repeatable) only when every step
#                       passed; --then is the owner's closing line when
#                       nothing failed. Returns 0 when nothing failed.
#
# Cleanup and sandboxes
#   on_exit <cmd…>      runs on exit, q, Ctrl-C or a kill; last registered runs first
#   sandbox_dir [var]   a fresh temp folder outside any .worktrees/, removed on
#                       exit; its resolved path (no //, symlinks resolved) goes
#                       in $var (default SANDBOX). The first one exports
#                       XDG_STATE_HOME and XDG_CONFIG_HOME into it (state/,
#                       config/), so nothing the script starts touches the
#                       shared viewer.json or projects.yaml
#   sandbox_project <slug> [template] [--workflow <file>] [--name <name>]
#                       a throwaway project in a sandbox_dir: a git repo, its
#                       workflow (issue-cli init --template, default development,
#                       or a copy of <file>), its own projects.yaml (headless:
#                       terminal: "none"). Append the project's own settings
#                       to $SANDBOX_CONFIG indented four spaces (they go under
#                       the project); top-level keys (viewer_url,
#                       allowed_hosts) go above projects:, unindented
#                       (stand_in_viewer does viewer_url). Sets
#                       SANDBOX_PROJECT, SANDBOX_PROJECT_DIR and SANDBOX_CONFIG,
#                       SANDBOX (the sandbox folder, SANDBOX_PROJECT_DIR's
#                       parent; an earlier sandbox_dir's $SANDBOX is kept), and
#                       exports XDG_STATE_HOME, XDG_CONFIG_HOME and
#                       ISSUE_VIEWER_CONFIG into the sandbox. It marks the
#                       sandbox (.issue-cli-sandbox beside projects.yaml): a
#                       worker's owner-only commands pass there while every
#                       project path and both XDG dirs stay inside it (keep
#                       your own settings' paths inside too); issue-cli lead
#                       whoami says whether it passes and why not. Scratch
#                       projects must live outside .worktrees/. Building
#                       the branch yourself? Build from $CHECKS_ROOT (the
#                       library cds there; $ISSUE_CLI is already that build).
#   sandbox_project --add <slug> [template] [--workflow <file>] [--name <name>]
#                       a second project in the same sandbox and projects.yaml
#                       (after the first): its folder next to the first, set
#                       up the same way. Sets SANDBOX_ADDED_PROJECT and
#                       SANDBOX_ADDED_DIR; sic still runs on the first
#                       (pass --project $SANDBOX_ADDED_PROJECT to reach it).
#   sandbox_git_repo [var] [name]
#                       a git repo in the sandbox ($SANDBOX/<name>, default
#                       repo; a sandbox_dir first when there is none) with
#                       .worktrees/ ignored, one commit on main, and a local
#                       bare remote <name>.git as origin, main pushed and
#                       tracked. Its local config has a git identity, no
#                       signing or hooks, and no auto maintenance or gc. Its
#                       path goes in $var (default SANDBOX_REPO), the
#                       remote's in SANDBOX_REMOTE
#   sandbox_git_identity
#                       exports a git author/committer and GIT_CONFIG_GLOBAL=/dev/null
#                       (no signing, hooks or aliases) for git commands in sandboxes
#   sic <args…>         issue-cli against the sandbox project, from its folder
#                       ($SANDBOX_ISSUE_CLI, else $ISSUE_CLI: the branch build check-script found or --cli named);
#                       through clean_shell once a stand-in exists, so a dispatch
#                       or lead spawn gets the stand-in. The worker's identity
#                       goes along: on the marked sandbox sandbox_project makes,
#                       the worker guard passes owner-only commands (department
#                       add, transitions on sandbox issues, yolo on, output
#                       review, job run…) and says so on stderr; dispatch, lead
#                       spawn and other agent starts stay refused
#   private_tmux [--clean]
#                       a private tmux server (short TMUX_TMPDIR, marker file);
#                       exports TMUX_TMPDIR and unsets TMUX for the rest of the
#                       script; on exit kill-server runs on it only. A tmux
#                       server keeps the environment of whatever started it, and
#                       panes inherit it: with --clean the server starts at once
#                       from clean_shell's environment (stand-ins first on PATH,
#                       no tmux.conf), so agents in its windows can't find the
#                       real claude; ptmux then runs in that environment too (a
#                       new session takes the tmux client's environment). Without
#                       it, start sessions through clean_shell when agents run there.
#   ptmux <args…>       tmux on the private server ($CHECKS_TMUX_DIR is its
#                       TMUX_TMPDIR, for an attach command the owner can type)
#   stand_in [--agent] <name> [script]
#                       puts a recording stand-in for <name> (e.g. claude) first
#                       on the PATH clean_shell uses; [script] is its body
#                       ($CHECKS_STAND_IN_DIR holds the records). --agent: it
#                       says "<name> stand-in ready" and stays alive, appending
#                       what it is sent to <name>.input (a re-sent prompt lands
#                       there, never in a shell). Each distinct script is
#                       kept once, already run, under $TMPDIR and linked in:
#                       macOS checks a new executable on its first run (up to
#                       seconds), so a body that is the same every run (what
#                       differs comes from the environment) starts at once
#   expect_stand_in_ran <name> [meaning] [seconds]
#                       a machine check that the stand-in, not the real <name>, ran
#                       (eventually: agents start a moment later; default 30 s,
#                       a loaded machine can take seconds to start a process;
#                       it returns as soon as the stand-in ran)
#   expect_stand_in_got <name> <text> [meaning] [seconds]
#                       eventually: the --agent stand-in was sent <text> (default 30 s)
#   clean_shell <cmd…>  runs <cmd> under bash without the owner's profile or rc
#                       (env -i, SHELL set to a no-profile bash, stand-ins first
#                       on PATH). Never use $SHELL -lic in a check that starts
#                       agents: a login shell rereads the profile and the real
#                       claude gets in front of the stand-in. env -i also drops
#                       the tmux session and the worktree folder, so it carries
#                       the caller's identity instead (ISSUE_VIEWER_ISSUE_SLUG,
#                       ISSUE_VIEWER_LEAD_SCOPE, ISSUE_VIEWER_WORKER, AGENT_NAME,
#                       CLAUDECODE): the worker guards still see a worker.
#                       Owner-only commands pass only on a marked sandbox
#                       (sandbox_project); never strip those variables.
#   is_worker           0 when issue-cli's worker guard takes this shell for a
#                       worker, 1 when not, 2 when issue-cli couldn't answer.
#                       It asks issue-cli lead whoami (the guard's own test:
#                       ISSUE_VIEWER_ISSUE_SLUG, the tmux name, a folder under
#                       .worktrees/; AGENT_NAME isn't one), with the sandbox's
#                       config once there is one. It only reports: nothing in
#                       the library clears a worker's identity to get past the
#                       guard.
#   background <var> [clean_shell] <cmd…>
#                       starts <cmd> in the background (through clean_shell if
#                       named), its pid in $var, killed on exit
#   free_port [var]     a free local TCP port in $var (default PORT)
#   wait_for_url <url> [seconds]
#                       true once <url> answers (default 60 s: a freshly
#                       built viewer can take over 10 s to start on a loaded Mac)
#
# Shared stand-ins (each needs sandbox_project, the box private_tmux)
#   stand_in_viewer     a stand-in viewer: python3's http.server on a free port
#                       serving $STAND_IN_VIEWER_DIR (put release.json and the
#                       like under it, e.g. p/<project>/release.json); sets
#                       $VIEWER_URL and writes viewer_url: at the top of
#                       $SANDBOX_CONFIG, replacing any; killed on exit
#   stand_in_claude_box <session> [--swallow-first-enter]
#                       a stand-in Claude Code input box in a ptmux session:
#                       submitted lines above a ❯ line between rules, "⏵⏵ auto
#                       mode on" under it. --swallow-first-enter takes the first
#                       Enter after a paste as part of the paste, as the real
#                       box sometimes does (paste and nudge bugs)
#   expect_box_submitted <session> <text> [meaning]
#                       eventually: the box submitted a line holding <text>
#   expect_box_empty <session> [meaning]
#                       eventually: the box's ❯ line is empty (nothing left typed)
#   sandbox_yolo_on     turns YOLO on in the sandbox by writing its own
#                       .yolo/<project>.json (kept for older scripts; on a
#                       marked sandbox sic yolo on works too); check it with
#                       sic yolo status
#   seed_department <template> [--parent <group>]
#                       a department template on the sandbox as department add
#                       would put it, without running it (a worker is refused):
#                       the dry run's files (workflows from issue-cli workflow
#                       init --out, the rest from $SEED_DEPARTMENT_TEMPLATES,
#                       default the engine's cmd/issue-cli/templates/departments)
#                       and its departments: entry. Call it after the project's
#                       other settings: the entry goes at the end of $SANDBOX_CONFIG
#
# Environment from check-script: CHECKS_LIB, CHECKS_SLUG, CHECKS_PROJECT (the
# --project ticking uses), CHECKS_ROOT (the issue's checkout; the library cds
# there), CHECKS_NAME, CHECKS_DIR (the folder the script and the files beside
# it sit in), CHECKS_ISSUE_CLI (the binary that ticks), CHECKS_YES,
# ISSUE_CLI (the binary a script checks: --cli, the branch build, else CHECKS_ISSUE_CLI).
#
# Gotchas
#   - The script may not sit in the repository: after the merge check-script
#     runs it from history, written into a temporary folder. Never compute
#     paths from BASH_SOURCE or $0: the repository is $CHECKS_ROOT, and files
#     beside the script (scripts/checks/<name>-*, <name>.*, <name>/, restored
#     from the same commit) are under $CHECKS_DIR.
#   - run runs its command in a subshell (to capture it): call helpers that
#     change the script's state (sandbox_*, private_tmux, on_exit, background,
#     cd, export) directly, never through run.
#   - shellcheck can't see that sandbox_dir assigns its variable: declare it
#     first (build=; sandbox_dir build).
#   - Don't set -e: a failed check belongs to its step, it doesn't stop the script.
#   - A viewer writes viewer.json under XDG_STATE_HOME: run sandbox viewers
#     after sandbox_dir or sandbox_project (which move it into the sandbox;
#     the project's own state when there is one). Without them a
#     viewer with a temp-folder config leaves the shared viewer.json alone,
#     so issue-cli outside the script can't find it; never pass
#     -record-viewer from a script.
#   - sandbox_dir is removed on exit, and with it any screenshot in it: show
#     or keep the file before the script ends.
#   - shellcheck sees $out and $code used only inside expect strings, and
#     variables set by sandbox_dir, as unused or unassigned: put
#     `# shellcheck source=/dev/null disable=SC2034,SC2154` above the source line.
#
# Modes: from a terminal the run is the owner's (questions, Enter between
# steps, the tick offer). With --yes (CHECKS_YES=1) or stdin not a terminal it
# is a machine run: machine checks only, questions are marked "needs your
# eyes", --touches-real steps are skipped, and nothing is ticked.
# NO_COLOR, or stdout not a terminal, turns colour off.

CHECKS_LIB_VERSION=4

if [ -z "${BASH_VERSINFO:-}" ] || [ "${BASH_VERSINFO[0]}" -lt 4 ]; then
	echo "guided checks need bash 4 or newer (this is bash ${BASH_VERSION:-unknown}): brew install bash, then run the script with issue-cli check-script <slug>, which finds it" >&2
	exit 2
fi
set +e

# The parts in lib/ beside this file, one concern each. issue-cli inlines them
# where they are sourced at the end (checks.Lib) and drops the _ck_parts
# lines; sourced straight from a checkout, lib.sh finds them here, before the
# cd to $CHECKS_ROOT below.
_ck_parts=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/lib

# ── Context ──────────────────────────────────────────────────────────
_ck_script=${CHECKS_SCRIPT:-$0}
CHECKS_NAME=${CHECKS_NAME:-$(basename "$_ck_script" .sh)}
if [ -z "${CHECKS_SLUG:-}" ]; then
	CHECKS_SLUG=$(sed -n '1,15s/^# issue:[[:space:]]*\([^[:space:]]*\).*/\1/p' "$_ck_script" 2> /dev/null | head -n 1)
fi
CHECKS_ROOT=${CHECKS_ROOT:-$PWD}
CHECKS_DIR=${CHECKS_DIR:-$(cd "$(dirname "$_ck_script")" 2> /dev/null && pwd)}
CHECKS_ISSUE_CLI=${CHECKS_ISSUE_CLI:-issue-cli}
# The issue-cli a script checks: check-script --cli, else the branch build.
ISSUE_CLI=${ISSUE_CLI:-$CHECKS_ISSUE_CLI}
cd "$CHECKS_ROOT" || exit 2

if [ "${CHECKS_YES:-}" = 1 ] || [ ! -t 0 ]; then
	CHECKS_MODE=machine
else
	CHECKS_MODE=owner
fi

if [ -z "${NO_COLOR:-}" ] && [ -t 1 ]; then
	_ck_b=$'\e[1m' _ck_d=$'\e[2m' _ck_g=$'\e[32m' _ck_r=$'\e[31m' _ck_y=$'\e[33m' _ck_c=$'\e[36m' _ck_0=$'\e[0m'
else
	_ck_b='' _ck_d='' _ck_g='' _ck_r='' _ck_y='' _ck_c='' _ck_0=''
fi

# The caller's environment as it was before any sandbox helper changed it:
# ticking the real issue runs with it (_ck_real). ISSUE_CLI_LOG goes at once:
# what a script runs is the script's, and in a worker's shell every sandbox
# call would land in its clilog; only the tick and comment on the real issue
# are the caller's own.
_ck_saved_vars=(TMUX TMUX_TMPDIR XDG_STATE_HOME XDG_CONFIG_HOME ISSUE_VIEWER_CONFIG SHELL PATH ISSUE_CLI_LOG)
declare -A _ck_saved=() _ck_saved_set=()
for _ck_v in "${_ck_saved_vars[@]}"; do
	if [ -n "${!_ck_v+x}" ]; then
		_ck_saved[$_ck_v]=${!_ck_v}
		_ck_saved_set[$_ck_v]=1
	fi
done
unset _ck_v ISSUE_CLI_LOG

_ck_real() {
	local unsets=() sets=() v
	for v in "${_ck_saved_vars[@]}"; do
		if [ -n "${_ck_saved_set[$v]:-}" ]; then
			sets+=("$v=${_ck_saved[$v]}")
		else
			unsets+=(-u "$v")
		fi
	done
	env "${unsets[@]}" "${sets[@]}" "$@"
}

# shellcheck source=lib/steps.sh
. "$_ck_parts/steps.sh"

# shellcheck source=lib/sandbox.sh
. "$_ck_parts/sandbox.sh"

# shellcheck source=lib/tmux_shell.sh
. "$_ck_parts/tmux_shell.sh"

# shellcheck source=lib/stand_ins.sh
. "$_ck_parts/stand_ins.sh"

# shellcheck source=lib/summary.sh
. "$_ck_parts/summary.sh"
unset _ck_parts
