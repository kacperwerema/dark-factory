# shellcheck shell=bash
# lib.sh's part (lib.sh sources it, issue-cli inlines it): cleanup on exit,
# and the steps: step, run, expect, eventually, ask, skip, pause, keep, show.

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
