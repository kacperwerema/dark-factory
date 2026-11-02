# Guided check library: what an issue's scripts/checks/<name>.sh sources.
# issue-cli ships it (`issue-cli check-script --lib` prints this file) and
# `issue-cli check-script <slug>` runs the issue's scripts with it:
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
#                       ISSUE_VIEWER_CONFIG into the sandbox. Scratch projects must live outside
#                       .worktrees/: there issue-cli takes the caller for a
#                       worker and refuses dispatch and lead spawn. Building
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
#                       or lead spawn gets the stand-in
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
#                       there, never in a shell)
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
#                       a worker's identity (AGENT_NAME, its tmux session), so
#                       the worker guards don't see it: never use clean_shell
#                       (or sic once a stand-in exists) to run an owner-only
#                       command from a worker's run. Leave those steps to the
#                       owner's run (if is_worker; then skip "…"; fi).
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
#                       true once <url> answers (default 10 s)
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
#   sandbox_yolo_on     turns YOLO on in the sandbox (sic yolo on refuses a
#                       worker) by writing its own .yolo/<project>.json; check
#                       it with sic yolo status
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
# there), CHECKS_NAME, CHECKS_ISSUE_CLI (the binary that ticks), CHECKS_YES,
# ISSUE_CLI (the binary a script checks: --cli, the branch build, else CHECKS_ISSUE_CLI).
#
# Gotchas
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

CHECKS_LIB_VERSION=3

if [ -z "${BASH_VERSINFO:-}" ] || [ "${BASH_VERSINFO[0]}" -lt 4 ]; then
	echo "guided checks need bash 4 or newer (this is bash ${BASH_VERSION:-unknown}): brew install bash, then run the script with issue-cli check-script <slug>, which finds it" >&2
	exit 2
fi
set +e

# ── Context ──────────────────────────────────────────────────────────
_ck_script=${CHECKS_SCRIPT:-$0}
CHECKS_NAME=${CHECKS_NAME:-$(basename "$_ck_script" .sh)}
if [ -z "${CHECKS_SLUG:-}" ]; then
	CHECKS_SLUG=$(sed -n '1,15s/^# issue:[[:space:]]*\([^[:space:]]*\).*/\1/p' "$_ck_script" 2> /dev/null | head -n 1)
fi
CHECKS_ROOT=${CHECKS_ROOT:-$PWD}
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
# ticking the real issue runs with it (_ck_real).
_ck_saved_vars=(TMUX TMUX_TMPDIR XDG_STATE_HOME XDG_CONFIG_HOME ISSUE_VIEWER_CONFIG SHELL PATH)
declare -A _ck_saved=() _ck_saved_set=()
for _ck_v in "${_ck_saved_vars[@]}"; do
	if [ -n "${!_ck_v+x}" ]; then
		_ck_saved[$_ck_v]=${!_ck_v}
		_ck_saved_set[$_ck_v]=1
	fi
done
unset _ck_v

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

# ── Cleanup ──────────────────────────────────────────────────────────
_ck_cleanups=()

on_exit() {
	_ck_cleanups+=("$(printf '%q ' "$@")")
}

_ck_run_cleanups() {
	local i
	for ((i = ${#_ck_cleanups[@]} - 1; i >= 0; i--)); do
		eval "${_ck_cleanups[$i]}" > /dev/null 2>&1
	done
	_ck_cleanups=()
}

_ck_stop() {
	echo
	echo "Stopped."
	exit 130
}

trap _ck_run_cleanups EXIT
trap _ck_stop INT TERM HUP

_ck_quit_if_q() {
	[[ ${1:-} == [qQ]* ]] && _ck_stop
	return 0
}

# ── Steps ────────────────────────────────────────────────────────────
_ck_order=()
declare -A _ck_result=() _ck_note=() _ck_boxes=() _ck_title=()
_ck_cur=
_ck_failed=0
_ck_eyes=0
_ck_skipping=0
_ck_skip_why=
_ck_reason=
_ck_default_box=

_ck_add_boxes() { # _ck_add_boxes <id> <box list>
	local b
	for b in ${2//,/ }; do
		[[ " ${_ck_boxes[$1]:-} " == *" $b "* ]] || _ck_boxes[$1]="${_ck_boxes[$1]:+${_ck_boxes[$1]} }$b"
	done
}

_ck_fail() {
	_ck_failed=1
	[ -n "$_ck_reason" ] || _ck_reason=$1
}

# _ck_close records the open step's verdict. A repeated id merges: fail wins,
# then skip, then needs-eyes, then pass.
_ck_close() {
	[ -n "$_ck_cur" ] || return 0
	local id=$_ck_cur verdict note=
	if [ "$_ck_failed" = 1 ]; then
		verdict=fail note=$_ck_reason
	elif [ "$_ck_skipping" = 1 ]; then
		verdict=skip note=$_ck_skip_why
	elif [ "$_ck_eyes" = 1 ]; then
		verdict=eyes note="needs your eyes"
	else
		verdict=pass
	fi
	local prev=${_ck_result[$id]:-}
	if [ -z "$prev" ] || [ "$verdict" = fail ] || { [ "$verdict" = skip ] && [ "$prev" != fail ]; } ||
		{ [ "$verdict" = eyes ] && [ "$prev" = pass ]; }; then
		_ck_result[$id]=$verdict
		_ck_note[$id]=$note
	fi
	case "$verdict" in
	pass) echo "  ${_ck_g}${_ck_b}$id passed${_ck_0}" ;;
	fail) echo "  ${_ck_r}${_ck_b}$id failed${_ck_0}: $note" ;;
	skip) echo "  ${_ck_y}${_ck_b}$id skipped${_ck_0}: $note" ;;
	eyes) echo "  ${_ck_y}${_ck_b}$id machine checks passed${_ck_0}; the rest needs your eyes" ;;
	esac
	_ck_cur=
	pause
}

checks_intro() {
	echo "${_ck_b}Manual checks for ${CHECKS_SLUG:-$CHECKS_NAME}${_ck_0} (${CHECKS_NAME})"
	local line
	for line in "$@"; do echo "$line"; done
	if [ "$CHECKS_MODE" = machine ]; then
		echo "${_ck_d}Machine run: machine checks only; questions are left for the owner and nothing is ticked.${_ck_0}"
	fi
	pause
}

step() {
	_ck_close
	local id=${1:?step needs an id} title=${2:?step needs a title}
	shift 2
	local what='' why='' look='' exp='' real='' boxes=''
	while [ $# -gt 0 ]; do
		case "$1" in
		--box) boxes="$boxes ${2:-}" ;;
		--what) what=${2:-} ;;
		--why) why=${2:-} ;;
		--look) look=${2:-} ;;
		--expect) exp=${2:-} ;;
		--touches-real) real=${2:-} ;;
		*)
			echo "step $id: unknown option $1" >&2
			exit 2
			;;
		esac
		shift 2
	done
	if [ -z "${_ck_title[$id]:-}" ]; then
		_ck_order+=("$id")
		_ck_title[$id]=$title
	fi
	_ck_add_boxes "$id" "${boxes:-${_ck_default_box:-$id}}"
	_ck_cur=$id _ck_failed=0 _ck_eyes=0 _ck_skipping=0 _ck_skip_why='' _ck_reason=''

	echo
	echo "${_ck_b}${_ck_c}━━ $id · $title ━━${_ck_0}  ${_ck_d}(box ${_ck_boxes[$id]// /, })${_ck_0}"
	[ -n "$what" ] && echo "  ${_ck_b}What:${_ck_0}    $what"
	[ -n "$why" ] && echo "  ${_ck_b}Why:${_ck_0}     $why"
	[ -n "$look" ] && echo "  ${_ck_b}Look at:${_ck_0} $look"
	[ -n "$exp" ] && echo "  ${_ck_b}Expect:${_ck_0}  $exp"
	if [ -n "$real" ]; then
		echo "  ${_ck_y}${_ck_b}Touches your real environment:${_ck_0} $real"
		if [ "$CHECKS_MODE" = machine ]; then
			skip "touches your real environment; run it from a terminal"
			return 1
		fi
		local answer
		read -r -p "  Run this step? It creates the above and removes it at the end [y/N, q stops] " answer
		_ck_quit_if_q "$answer"
		if [[ $answer != [yY]* ]]; then
			skip "you chose not to run it"
			return 1
		fi
	fi
	echo
	return 0
}

checks_default_box() {
	_ck_default_box="$*"
}

step_active() {
	[ -n "$_ck_cur" ] && [ "$_ck_skipping" = 0 ]
}

step_end() {
	_ck_close
}

skip() {
	[ -n "$_ck_cur" ] || return 1
	_ck_skipping=1
	_ck_skip_why=${1:-skipped}
	return 1
}

# _ck_words prints a command the way you would type it: plain words as they
# are, others in single quotes.
_ck_words() {
	local a sep='' s=''
	for a in "$@"; do
		if [[ $a =~ ^[A-Za-z0-9_./:=@%+,-]+$ ]]; then
			s+="$sep$a"
		else
			s+="$sep'${a//\'/\'\\\'\'}'"
		fi
		sep=' '
	done
	printf '%s' "$s"
}

run() {
	[ "$_ck_skipping" = 1 ] && return 0
	echo "${_ck_d}\$ $(_ck_words "$@")${_ck_0}"
	out=$("$@" 2>&1)
	code=$?
	[ -n "$out" ] && printf '%s\n' "$out" | sed 's/^/    /'
	echo "${_ck_d}  (exit $code)${_ck_0}"
	return 0
}

# _ck_rc sets $? to its argument: expect and eventually give <cond> the exit
# code of the caller's last command, which their own tests overwrite.
_ck_rc() { return "$1"; }

expect() {
	local _ck_last=$?
	[ "$_ck_skipping" = 1 ] && return 0
	_ck_rc "$_ck_last"
	if eval "$1"; then
		echo "  ${_ck_g}✓ $2${_ck_0}"
		return 0
	fi
	echo "  ${_ck_r}✗ $2${_ck_0}"
	_ck_fail "$2"
	return 1
}

eventually() {
	local _ck_last=$?
	[ "$_ck_skipping" = 1 ] && return 0
	local tries=$((${3:-10} * 5))
	while [ "$tries" -gt 0 ]; do
		_ck_rc "$_ck_last"
		eval "$1" && break
		sleep 0.2
		tries=$((tries - 1))
	done
	_ck_rc "$_ck_last"
	expect "$1" "$2"
}

ask() {
	[ "$_ck_skipping" = 1 ] && return 0
	if [ "$CHECKS_MODE" = machine ]; then
		echo "  ${_ck_y}? $1${_ck_0} ${_ck_d}(needs your eyes)${_ck_0}"
		_ck_eyes=1
		return 0
	fi
	local answer
	read -r -p "  $1 [y/n, q stops] " answer
	_ck_quit_if_q "$answer"
	[[ $answer == [yY]* ]] && return 0
	_ck_fail "you answered no: $1"
	return 1
}

pause() {
	[ "$CHECKS_MODE" = owner ] || return 0
	local answer
	read -r -p "${_ck_d}  Enter to continue, q to stop…${_ck_0}" answer
	_ck_quit_if_q "$answer"
}

keep() {
	local src=${1:?keep needs a file} dir="$CHECKS_ROOT/.agent-logs/checks/$CHECKS_NAME/files"
	kept=$src
	if [ ! -f "$src" ] || ! mkdir -p "$dir" 2> /dev/null; then
		echo "  ${_ck_y}keep: can't keep $src${_ck_0}"
		return 1
	fi
	kept="$dir/$(date -u +%Y%m%dT%H%M%SZ)-${2:-$(basename "$src")}"
	cp -- "$src" "$kept" || return 1
	echo "  Kept $kept"
}

show() {
	local file=$1 opener=
	keep "$file" > /dev/null && file=$kept
	if [ "$CHECKS_MODE" = owner ]; then
		if [ "$(uname)" = Darwin ] && command -v open > /dev/null; then
			opener=open
		elif [ -n "${DISPLAY:-}${WAYLAND_DISPLAY:-}" ] && command -v xdg-open > /dev/null; then
			opener=xdg-open
		fi
	fi
	if [ -n "$opener" ]; then
		"$opener" "$file" > /dev/null 2>&1 &
		echo "  Opened $file"
	else
		echo "  Look at $file"
	fi
}

# ── Sandboxes ────────────────────────────────────────────────────────
_ck_tmp_base() {
	local base=${TMPDIR:-/tmp}
	[[ $base == */.worktrees/* ]] && base=/tmp
	echo "$base"
}

# _ck_sandbox_state points XDG_STATE_HOME and XDG_CONFIG_HOME into <dir>, so
# a viewer or issue-cli the script starts never reads or writes the shared
# viewer.json and projects.yaml. Ticking the real issue gets the caller's back
# (_ck_real).
_ck_sandbox_state() {
	mkdir -p -- "$1/state" "$1/config"
	export XDG_STATE_HOME=$1/state XDG_CONFIG_HOME=$1/config
	_ck_state_isolated=1
}

# sandbox_dir's locals are prefixed: printf -v would set a local of the same
# name instead of the caller's variable. The path is resolved (pwd -P): no //
# from a TMPDIR ending in a slash, and /private/var for /var on macOS, as
# issue-cli and a viewer print it. The first sandbox_dir isolates the state;
# a later one keeps it where it is (sandbox_project moves it to its own).
sandbox_dir() {
	local _ck_sd_var=${1:-SANDBOX} _ck_sd_path
	_ck_sd_path=$(mktemp -d "$(_ck_tmp_base)/ck-XXXXXX") || {
		echo "sandbox_dir: mktemp failed" >&2
		exit 2
	}
	on_exit rm -rf -- "$_ck_sd_path"
	_ck_sd_path=$(cd -P -- "$_ck_sd_path" && pwd -P) || {
		echo "sandbox_dir: could not resolve the sandbox path" >&2
		exit 2
	}
	[ -n "${_ck_state_isolated:-}" ] || _ck_sandbox_state "$_ck_sd_path"
	printf -v "$_ck_sd_var" '%s' "$_ck_sd_path"
}

sandbox_project() {
	if [ "${1:-}" = --add ]; then
		shift
		_ck_sandbox_add "$@"
		return
	fi
	local name=${1:?sandbox_project needs a slug} template=development workflow='' display='' dir
	shift
	_ck_sandbox_opts "$@"
	dir=''
	sandbox_dir dir
	# $SANDBOX is the sandbox folder (projects.yaml, state/, the project), as
	# after a bare sandbox_dir; a script's own earlier sandbox_dir keeps it.
	[ -n "${SANDBOX:-}" ] || SANDBOX=$dir
	SANDBOX_PROJECT=$name
	SANDBOX_PROJECT_DIR=$dir/$name
	SANDBOX_CONFIG=$dir/projects.yaml
	mkdir -p "$SANDBOX_PROJECT_DIR"
	_ck_sandbox_state "$dir"
	export ISSUE_VIEWER_CONFIG=$SANDBOX_CONFIG
	echo "projects:" > "$SANDBOX_CONFIG"
	_ck_sandbox_entry "$name" "$SANDBOX_PROJECT_DIR"
	_ck_sandbox_repo "$SANDBOX_PROJECT_DIR"
}
# _ck_sandbox_opts [template] [--template t] [--workflow f] [--name n]: sets
# the caller's template, workflow and display.
_ck_sandbox_opts() {
	if [ $# -gt 0 ] && [[ $1 != --* ]]; then
		template=$1
		shift
	fi
	while [ $# -gt 0 ]; do
		case "$1" in
		--template) template=${2:-} ;;
		--workflow) workflow=${2:-} ;;
		--name) display=${2:-} ;;
		*)
			echo "sandbox_project: unknown option $1" >&2
			exit 2
			;;
		esac
		shift 2
	done
}
# _ck_sandbox_entry <slug> <dir>: one project entry at the end of $SANDBOX_CONFIG.
_ck_sandbox_entry() {
	cat >> "$SANDBOX_CONFIG" << YAML
  - name: "${display:-$1}"
    slug: "$1"
    issues: "$2/issues"
    workflow: "$2/workflow.yaml"
    docs: "$2/docs"
    workdir: "$2"
    terminal: "none"
YAML
}
# _ck_sandbox_repo <dir>: the project's git repo, workflow and first commit.
_ck_sandbox_repo() {
	(
		cd "$1" &&
			git init -q -b main &&
			if [ -n "$workflow" ]; then
				cp -- "$workflow" workflow.yaml && mkdir -p issues docs
			else
				"${SANDBOX_ISSUE_CLI:-$ISSUE_CLI}" init --template "$template" > /dev/null
			fi &&
			printf '.agent-logs/\n.playtest/\n' > .gitignore &&
			git add -A && git -c user.name=checks -c user.email=checks@localhost -c commit.gpgsign=false -c core.hooksPath=/dev/null commit -q -m init
	) || {
		echo "sandbox_project: could not set up $1" >&2
		exit 2
	}
}
# sandbox_project --add <slug> [template] [--workflow f] [--name n]: a second
# project in the same sandbox and projects.yaml.
_ck_sandbox_add() {
	local name=${1:?sandbox_project --add needs a slug} template=development workflow='' display='' dir
	shift
	[ -n "${SANDBOX_CONFIG:-}" ] && [ -f "$SANDBOX_CONFIG" ] || {
		echo "sandbox_project --add: call sandbox_project <slug> first" >&2
		exit 2
	}
	_ck_sandbox_opts "$@"
	dir=$(dirname -- "$SANDBOX_CONFIG")/$name
	if [ -e "$dir" ]; then
		echo "sandbox_project --add: $dir already exists (a second project needs its own slug)" >&2
		exit 2
	fi
	mkdir -p "$dir"
	_ck_sandbox_entry "$name" "$dir"
	_ck_sandbox_repo "$dir"
	SANDBOX_ADDED_PROJECT=$name
	SANDBOX_ADDED_DIR=$dir
}
sandbox_git_identity() {
	export GIT_AUTHOR_NAME=checks GIT_AUTHOR_EMAIL=checks@localhost
	export GIT_COMMITTER_NAME=checks GIT_COMMITTER_EMAIL=checks@localhost
	export GIT_CONFIG_GLOBAL=/dev/null
}

# sandbox_git_repo [var] [name]: a git repo in the sandbox with a local bare
# remote (origin, main pushed and tracked) and .worktrees/ ignored.
sandbox_git_repo() {
	local _ck_gr_var=${1:-SANDBOX_REPO} _ck_gr_name=${2:-repo} _ck_gr_base _ck_gr_dir
	_ck_gr_base=${SANDBOX:-}
	if [ -z "$_ck_gr_base" ]; then
		sandbox_dir
		_ck_gr_base=$SANDBOX
	fi
	_ck_gr_dir=$_ck_gr_base/$_ck_gr_name
	if [ -e "$_ck_gr_dir" ] || [ -e "$_ck_gr_dir.git" ]; then
		echo "sandbox_git_repo: $_ck_gr_dir already exists (pass another name)" >&2
		exit 2
	fi
	(
		git init -q --bare -b main "$_ck_gr_dir.git" &&
			git init -q -b main "$_ck_gr_dir" &&
			cd "$_ck_gr_dir" &&
			git config user.name checks && git config user.email checks@localhost &&
			git config commit.gpgsign false && git config tag.gpgsign false &&
			git config core.hooksPath /dev/null &&
			git config maintenance.auto false && git config gc.auto 0 &&
			printf '.worktrees/\n.agent-logs/\n.playtest/\n' > .gitignore &&
			git add .gitignore && git commit -q -m init &&
			git remote add origin "$_ck_gr_dir.git" &&
			git push -q -u origin main 2> /dev/null
	) || {
		echo "sandbox_git_repo: could not set up $_ck_gr_dir" >&2
		exit 2
	}
	SANDBOX_REMOTE=$_ck_gr_dir.git
	printf -v "$_ck_gr_var" '%s' "$_ck_gr_dir"
}
# is_worker: 0 when issue-cli's worker guard takes this shell for a worker,
# 1 when not, 2 when issue-cli couldn't say. It reports only.
is_worker() {
	local _ck_iw_cli=${SANDBOX_ISSUE_CLI:-${ISSUE_CLI:-${CHECKS_ISSUE_CLI:-issue-cli}}} _ck_iw_out _ck_iw_rc _ck_iw_probe=''
	local _ck_iw_args=()
	if [ -n "${SANDBOX_CONFIG:-}" ]; then
		_ck_iw_args=(--config "$SANDBOX_CONFIG" --project "$SANDBOX_PROJECT")
	else
		# No sandbox project: a throwaway one-project config, so the answer
		# never depends on (or reads) the owner's projects.yaml.
		_ck_iw_probe=$(mktemp -d "$(_ck_tmp_base)/ck-whoami-XXXXXX") || return 2
		mkdir -p "$_ck_iw_probe/issues"
		printf 'projects:\n  - name: "probe"\n    slug: "probe"\n    issues: "%s/issues"\n    workdir: "%s"\n' \
			"$_ck_iw_probe" "$_ck_iw_probe" > "$_ck_iw_probe/projects.yaml"
		_ck_iw_args=(--config "$_ck_iw_probe/projects.yaml" --project probe)
	fi
	_ck_iw_out=$("$_ck_iw_cli" "${_ck_iw_args[@]}" --json lead whoami 2> /dev/null)
	_ck_iw_rc=$?
	[ -z "$_ck_iw_probe" ] || rm -rf -- "$_ck_iw_probe"
	[ "$_ck_iw_rc" = 0 ] || return 2
	case "$_ck_iw_out" in
	*'"role": "worker"'*) return 0 ;;
	*'"role":'*) return 1 ;;
	*) return 2 ;;
	esac
}
sic() {
	local argv=("${SANDBOX_ISSUE_CLI:-$ISSUE_CLI}" --config "$SANDBOX_CONFIG" --project "$SANDBOX_PROJECT" "$@")
	(
		cd "${SANDBOX_PROJECT_DIR:?sic needs sandbox_project first}" || exit 2
		if [ -n "${CHECKS_STAND_IN_DIR:-}" ]; then
			clean_shell "${argv[@]}"
		else
			"${argv[@]}"
		fi
	)
}

private_tmux() {
	CHECKS_TMUX_DIR=$(mktemp -d /tmp/ckt-XXXXXX) || {
		echo "private_tmux: mktemp failed" >&2
		exit 2
	}
	: > "$CHECKS_TMUX_DIR/.checks-private-tmux"
	on_exit _ck_kill_private_tmux "$CHECKS_TMUX_DIR"
	export TMUX_TMPDIR=$CHECKS_TMUX_DIR
	unset TMUX
	if [ "${1:-}" = --clean ]; then
		_ck_tmux_clean=1
		_ck_clean_argv tmux -f /dev/null new-session -d -s checks-keepalive 'sleep 2147483647'
		"${_ck_argv[@]}" || {
			echo "private_tmux: could not start the private tmux server" >&2
			exit 2
		}
	fi
}

_ck_kill_private_tmux() {
	[ -f "$1/.checks-private-tmux" ] || return 0
	env -u TMUX tmux -S "$1/tmux-$(id -u)/default" kill-server 2> /dev/null
	rm -rf -- "$1"
}

# ptmux on a --clean server runs tmux itself in clean_shell's environment: a
# new session takes its environment from the tmux client.
ptmux() {
	: "${CHECKS_TMUX_DIR:?ptmux needs private_tmux first}"
	if [ -n "${_ck_tmux_clean:-}" ]; then
		_ck_clean_argv tmux "$@"
		"${_ck_argv[@]}"
	else
		env -u TMUX TMUX_TMPDIR="$CHECKS_TMUX_DIR" tmux "$@"
	fi
}

_ck_stand_in_dir() {
	if [ -z "${CHECKS_STAND_IN_DIR:-}" ]; then
		CHECKS_STAND_IN_DIR=''
		sandbox_dir CHECKS_STAND_IN_DIR
		export CHECKS_STAND_IN_DIR
		mkdir -p "$CHECKS_STAND_IN_DIR/bin"
		printf '#!/bin/sh\nexec bash --noprofile --norc "$@"\n' > "$CHECKS_STAND_IN_DIR/shell"
		chmod +x "$CHECKS_STAND_IN_DIR/shell"
	fi
}

stand_in() {
	local agent=''
	if [ "${1:-}" = --agent ]; then
		agent=1
		shift
	fi
	local name=${1:?stand_in needs a name}
	_ck_stand_in_dir
	local f=$CHECKS_STAND_IN_DIR/bin/$name body=${2:-exit 0}
	if [ -n "$agent" ] && [ -z "${2:-}" ]; then
		body="echo \"$name stand-in ready\"
exec cat >> $(printf '%q' "$CHECKS_STAND_IN_DIR/$name.input")"
	fi
	{
		printf '#!/bin/sh\n'
		printf 'echo "$0 $*" >> %q\n' "$CHECKS_STAND_IN_DIR/$name.calls"
		printf '%s\n' "$body"
	} > "$f"
	chmod +x "$f"
}

expect_stand_in_ran() {
	local name=${1:?expect_stand_in_ran needs a name}
	eventually "[ -s $(printf '%q' "${CHECKS_STAND_IN_DIR:-/nonexistent}/$name.calls") ]" "${2:-the stand-in $name ran, not the real one}" "${3:-30}"
}

expect_stand_in_got() {
	local name=${1:?expect_stand_in_got needs a name} text=${2:?expect_stand_in_got needs a text}
	eventually "grep -qF -- $(printf '%q' "$text") $(printf '%q' "${CHECKS_STAND_IN_DIR:-/nonexistent}/$name.input") 2> /dev/null" "${3:-the stand-in $name was sent: $text}" "${4:-30}"
}

# _ck_clean_argv fills _ck_argv with <cmd> under clean_shell's environment:
# HOME, TERM, USER, LANG, the caller's PATH behind the stand-ins, and the
# sandbox variables.
_ck_clean_argv() {
	_ck_stand_in_dir
	local keep=() v
	for v in HOME TERM USER LANG TMUX_TMPDIR XDG_STATE_HOME XDG_CONFIG_HOME ISSUE_VIEWER_CONFIG CHECKS_STAND_IN_DIR ${CLEAN_SHELL_KEEP:-}; do
		[ -n "${!v+x}" ] && keep+=("$v=${!v}")
	done
	_ck_argv=(env -i "${keep[@]}" SHELL="$CHECKS_STAND_IN_DIR/shell" PATH="$CHECKS_STAND_IN_DIR/bin:$PATH"
		bash --noprofile --norc -c "exec $(printf '%q ' "$@")")
}

clean_shell() {
	_ck_clean_argv "$@"
	"${_ck_argv[@]}"
}

background() {
	local _ck_bg_var=${1:?background needs a variable} _ck_bg_pid
	shift
	if [ "${1:-}" = clean_shell ]; then
		shift
		_ck_clean_argv "$@"
		"${_ck_argv[@]}" &
	else
		"$@" &
	fi
	_ck_bg_pid=$!
	on_exit kill "$_ck_bg_pid"
	printf -v "$_ck_bg_var" '%s' "$_ck_bg_pid"
}

free_port() {
	local _ck_fp_var=${1:-PORT} _ck_fp_port _ck_fp_try
	for _ck_fp_try in $(seq 1 50); do
		_ck_fp_port=$((20000 + RANDOM % 20000))
		if ! (: > "/dev/tcp/127.0.0.1/$_ck_fp_port") 2> /dev/null; then
			printf -v "$_ck_fp_var" '%s' "$_ck_fp_port"
			return 0
		fi
	done
	echo "free_port: no free port found" >&2
	return 1
}

wait_for_url() {
	local url=${1:?wait_for_url needs a url} tries=$((${2:-10} * 5))
	while [ "$tries" -gt 0 ]; do
		curl -s -o /dev/null --max-time 1 "$url" && return 0
		sleep 0.2
		tries=$((tries - 1))
	done
	return 1
}

# ── Shared stand-ins ─────────────────────────────────────────────────
_ck_need_sandbox() {
	if [ -z "${SANDBOX_PROJECT_DIR:-}" ] || [ -z "${SANDBOX_CONFIG:-}" ]; then
		echo "$1 needs sandbox_project first" >&2
		exit 2
	fi
}

# stand_in_viewer serves $STAND_IN_VIEWER_DIR with python's http.server, as
# far as issue-cli is concerned the viewer (release.json and the like).
stand_in_viewer() {
	_ck_need_sandbox stand_in_viewer
	if ! command -v python3 > /dev/null; then
		echo "stand_in_viewer needs python3" >&2
		exit 2
	fi
	local port='' pid=''
	STAND_IN_VIEWER_DIR=''
	sandbox_dir STAND_IN_VIEWER_DIR
	free_port port || exit 2
	background pid python3 -m http.server "$port" --bind 127.0.0.1 --directory "$STAND_IN_VIEWER_DIR"
	VIEWER_URL=http://127.0.0.1:$port
	# viewer_url is a top-level key: it goes above projects:, replacing any.
	{
		printf 'viewer_url: "%s"\n' "$VIEWER_URL"
		grep -v '^viewer_url:' "$SANDBOX_CONFIG"
	} > "$SANDBOX_CONFIG.tmp" && mv "$SANDBOX_CONFIG.tmp" "$SANDBOX_CONFIG"
	if ! wait_for_url "$VIEWER_URL/" 10; then
		echo "stand_in_viewer: the stand-in viewer at $VIEWER_URL did not answer" >&2
		exit 2
	fi
}

stand_in_claude_box() {
	local session=${1:?stand_in_claude_box needs a session name} swallow=0
	[ "${2:-}" = --swallow-first-enter ] && swallow=1
	: "${CHECKS_TMUX_DIR:?stand_in_claude_box needs private_tmux first}"
	if [ -z "${CHECKS_BOX_DIR:-}" ]; then
		CHECKS_BOX_DIR=''
		sandbox_dir CHECKS_BOX_DIR
		# The input box: submitted lines ("> …") above a ❯ line between two
		# rules. With $2 = 1 the first Enter after text arrives is taken as
		# part of it. A submitted line is written aside and moved into place.
		cat > "$CHECKS_BOX_DIR/box.sh" << 'SH'
log=$1 swallow_first=$2 buf="" swallow=0
draw() { printf '\033[H\033[2J'; [ -s "$log" ] && sed 's/^/> /' "$log"; echo; printf '%s\n' "────────────────────" "❯ $buf" "────────────────────" "  ⏵⏵ auto mode on"; }
stty -icanon -echo min 1
printf '\033[?2004h'
: > "$log"
draw
while IFS= read -r -N1 c; do
	case $c in
	$'\e') IFS= read -r -N5 _ ;;
	$'\r' | $'\n')
		if [ "$swallow" = 1 ]; then
			swallow=0
		elif [ -n "$buf" ]; then
			{ cat "$log"; printf '%s\n' "$buf"; } > "$log.new" && mv "$log.new" "$log"
			buf=""
		fi
		;;
	*)
		[ -z "$buf" ] && [ "$swallow_first" = 1 ] && swallow=1
		buf+=$c
		;;
	esac
	draw
done
SH
	fi
	ptmux new-session -d -s "$session" -x 140 -y 30 "$BASH" "$CHECKS_BOX_DIR/box.sh" "$CHECKS_BOX_DIR/$session.submitted" "$swallow" || {
		echo "stand_in_claude_box: could not start $session" >&2
		exit 2
	}
	local tries=50
	until ptmux capture-pane -p -t "$session" 2> /dev/null | grep -q 'auto mode on'; do
		tries=$((tries - 1))
		if [ "$tries" -le 0 ]; then
			echo "stand_in_claude_box: $session never drew its box" >&2
			exit 2
		fi
		sleep 0.2
	done
}

expect_box_submitted() {
	local session=${1:?expect_box_submitted needs a session} text=${2:?expect_box_submitted needs a text}
	eventually "grep -qF -- $(printf '%q' "$text") $(printf '%q' "${CHECKS_BOX_DIR:-/nonexistent}/$session.submitted") 2> /dev/null" "${3:-$session submitted: $text}"
}

expect_box_empty() {
	local session=${1:?expect_box_empty needs a session}
	eventually "ptmux capture-pane -p -t $(printf '%q' "$session") | grep -q '^❯ *\$'" "${2:-the input box of $session is empty}"
}

# sandbox_yolo_on writes the sandbox's YOLO window (sic yolo on refuses a
# worker): the sandbox's own .yolo/, never anywhere else.
sandbox_yolo_on() {
	_ck_need_sandbox sandbox_yolo_on
	local dir=$SANDBOX_PROJECT_DIR/.yolo
	case "$dir" in
	"$(dirname "$SANDBOX_CONFIG")"/*) ;;
	*)
		echo "sandbox_yolo_on: $dir is outside the sandbox" >&2
		exit 2
		;;
	esac
	mkdir -p "$dir" || exit 2
	printf '{"on": true, "since": "%s", "forever": true, "by": "owner", "words": "guided check sandbox", "how": "check script", "valves": {"ci_red_minutes": 0, "releases": false}}\n' \
		"$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$dir/.tmp" && mv "$dir/.tmp" "$dir/$SANDBOX_PROJECT.json"
}

# seed_department puts a department template on the sandbox the way
# department add would, without running it (a worker is refused): it reads
# the dry run's plan, writes its files and adds its entry to $SANDBOX_CONFIG.
seed_department() {
	_ck_need_sandbox seed_department
	local tmpl=${1:?seed_department needs a template (issue-cli department list)} plan files entries src
	shift
	src=${SEED_DEPARTMENT_TEMPLATES:-$CHECKS_ROOT/cmd/issue-cli/templates/departments}
	if ! plan=$(sic --json department add "$tmpl" "$@" --dry-run 2>&1); then
		echo "seed_department: department add $tmpl --dry-run failed:" >&2
		printf '%s\n' "$plan" >&2
		exit 2
	fi
	files=$(python3 -I -c '
import json, sys
def walk(p):
    yield p
    for c in p.get("children") or []:
        yield from walk(c)
for p in walk(json.load(sys.stdin)):
    for f in p.get("files") or []:
        if f.get("state") == "write":
            print(f["path"])
' <<< "$plan") || exit 2
	entries=$(python3 -I -c '
import json, sys
def walk(p):
    yield p
    for c in p.get("children") or []:
        yield from walk(c)
for p in walk(json.load(sys.stdin)):
    for line in p["entry"].rstrip("\n").split("\n"):
        print("      " + line)
' <<< "$plan") || exit 2
	local f name
	while IFS= read -r f; do
		[ -n "$f" ] || continue
		mkdir -p "$SANDBOX_PROJECT_DIR/$(dirname "$f")"
		case "$f" in
		*.yaml)
			name=$(basename "$f" .yaml)
			if ! sic workflow init --template "$name" --out "$f" > /dev/null; then
				echo "seed_department: could not write $f (workflow template $name)" >&2
				exit 2
			fi
			;;
		departments/*)
			if [ -f "$src/${f#departments/}" ]; then
				cp -- "$src/${f#departments/}" "$SANDBOX_PROJECT_DIR/$f"
			else
				printf '# %s\n\nSeeded by a guided check (no template source at %s).\n' "$(basename "$f")" "$src" > "$SANDBOX_PROJECT_DIR/$f"
			fi
			;;
		esac
	done <<< "$files"
	grep -q '^    departments:' "$SANDBOX_CONFIG" || echo "    departments:" >> "$SANDBOX_CONFIG"
	printf '%s\n' "$entries" >> "$SANDBOX_CONFIG"
}

# ── Summary ──────────────────────────────────────────────────────────
summary() {
	_ck_close
	local all_boxes=() then='' b
	while [ $# -gt 0 ]; do
		case "$1" in
		--all)
			for b in ${2//,/ }; do all_boxes+=("$b"); done
			shift
			;;
		--then) then=${2:-} && shift ;;
		esac
		shift
	done

	local id failed=() eyes=() skipped=() passed_boxes=() lines=()
	declare -A box_ok=() box_seen=()
	local box_order=()
	echo
	echo "${_ck_b}Summary${_ck_0}"
	for id in "${_ck_order[@]}"; do
		local r=${_ck_result[$id]:-fail} n=${_ck_note[$id]:-}
		case "$r" in
		pass) echo "  ${_ck_g}✓ $id pass${_ck_0}" ;;
		fail) echo "  ${_ck_r}✗ $id fail${_ck_0}: $n" && failed+=("$id") ;;
		skip) echo "  ${_ck_y}- $id skipped${_ck_0}: $n" && skipped+=("$id") ;;
		eyes) echo "  ${_ck_y}? $id needs your eyes${_ck_0} (machine checks passed)" && eyes+=("$id") ;;
		esac
		lines+=("step $id $r boxes=${_ck_boxes[$id]// /,}${n:+ note=$n}")
		for b in ${_ck_boxes[$id]}; do
			if [ -z "${box_seen[$b]:-}" ]; then
				box_seen[$b]=1
				box_order+=("$b")
				box_ok[$b]=1
			fi
			[ "$r" = pass ] || box_ok[$b]=0
		done
	done
	for b in "${box_order[@]}"; do
		[ "${box_ok[$b]}" = 1 ] && passed_boxes+=("$b")
	done
	if [ ${#all_boxes[@]} -gt 0 ] && [ ${#_ck_order[@]} -gt 0 ] && [ ${#failed[@]} = 0 ] && [ ${#eyes[@]} = 0 ] && [ ${#skipped[@]} = 0 ]; then
		for b in "${all_boxes[@]}"; do
			[ -n "${box_ok[$b]:-}" ] || passed_boxes+=("$b")
		done
	fi
	echo "  Boxes passed: ${passed_boxes[*]:-none}"

	local dir="$CHECKS_ROOT/.agent-logs/checks/$CHECKS_NAME" stamp
	stamp=$(date -u +%Y%m%dT%H%M%SZ)
	if mkdir -p "$dir" 2> /dev/null; then
		{
			echo "script: $CHECKS_NAME"
			echo "issue: ${CHECKS_SLUG:-}"
			echo "mode: $CHECKS_MODE"
			echo "lib: $CHECKS_LIB_VERSION"
			printf '%s\n' "${lines[@]}"
			echo "boxes passed: ${passed_boxes[*]:-}"
		} > "$dir/$stamp-$CHECKS_MODE.txt"
	fi

	if [ "$CHECKS_MODE" = machine ]; then
		# The boxes of steps that need the owner's eyes or were skipped: the
		# machine run ticks nothing, and these are the owner's to check.
		local owner_boxes=() r
		for b in "${box_order[@]}"; do
			for id in "${_ck_order[@]}"; do
				r=${_ck_result[$id]:-fail}
				if [[ " ${_ck_boxes[$id]} " == *" $b "* ]] && { [ "$r" = eyes ] || [ "$r" = skip ]; }; then
					owner_boxes+=("$b")
					break
				fi
			done
		done
		echo "Machine run: ${#failed[@]} failed, ${#eyes[@]} need the owner's eyes, ${#skipped[@]} skipped. Left to the owner: ${owner_boxes[*]:-none from this script}."
		[ ${#failed[@]} = 0 ]
		return
	fi

	if [ -n "${CHECKS_SLUG:-}" ]; then
		local answer
		if [ ${#passed_boxes[@]} -gt 0 ]; then
			read -r -p "  Tick the passed boxes (${passed_boxes[*]}) on ${CHECKS_SLUG}? [y/n] " answer
			[[ $answer == [yY]* ]] && _ck_tick "${passed_boxes[@]}"
		fi
		if [ ${#failed[@]} -gt 0 ]; then
			local text="checks: $CHECKS_NAME:" sep=
			for id in "${failed[@]}"; do
				text="$text$sep $id failed: ${_ck_note[$id]}"
				sep=";"
			done
			read -r -p "  Comment the failures on ${CHECKS_SLUG} for the agent? [y/n] " answer
			[[ $answer == [yY]* ]] && _ck_comment "$text"
		fi
	fi
	if [ ${#failed[@]} = 0 ] && [ ${#skipped[@]} = 0 ]; then
		echo "${then:-All steps passed.}"
	elif [ ${#failed[@]} -gt 0 ]; then
		echo "Tell the agent which step failed and what you saw."
	fi
	[ ${#failed[@]} = 0 ]
}

_ck_project_args() {
	[ -n "${CHECKS_PROJECT:-}" ] && printf '%s\n' --project "$CHECKS_PROJECT"
}

_ck_tick() {
	local proj=()
	mapfile -t proj < <(_ck_project_args)
	_ck_real "$CHECKS_ISSUE_CLI" "${proj[@]}" check "$CHECKS_SLUG" "$@"
}

_ck_comment() {
	local proj=()
	mapfile -t proj < <(_ck_project_args)
	_ck_real "$CHECKS_ISSUE_CLI" "${proj[@]}" comment "$CHECKS_SLUG" "$1"
}
