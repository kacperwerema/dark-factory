# shellcheck shell=bash
# lib.sh's part (lib.sh sources it, issue-cli inlines it): the private tmux
# server, stand-ins, clean_shell, background, free_port, wait_for_url.

private_tmux() {
	CHECKS_TMUX_DIR=$(mktemp -d /tmp/ckt-XXXXXX) || {
		echo "private_tmux: mktemp failed" >&2
		exit 2
	}
	: > "$CHECKS_TMUX_DIR/.checks-private-tmux"
	on_exit _ck_kill_private_tmux "$CHECKS_TMUX_DIR"
	_ck_pin_identity
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
		_ck_put_stand_in "$CHECKS_STAND_IN_DIR/shell" '#!/bin/sh
[ -n "${STANDIN_WARM:-}" ] && exit 0
exec bash --noprofile --norc "$@"
'
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
	# The records sit beside bin/ ("${0%/bin/*}"), so the script is the same
	# in every sandbox and _ck_put_stand_in finds it already checked.
	local dir='"${0%/bin/*}"'
	if [ -n "$agent" ] && [ -z "${2:-}" ]; then
		body="echo \"$name stand-in ready\"
exec cat >> $dir/$(printf '%q' "$name.input")"
	fi
	_ck_put_stand_in "$f" "#!/bin/sh
[ -n \"\${STANDIN_WARM:-}\" ] && exit 0
echo \"\$0 \$*\" >> $dir/$(printf '%q' "$name.calls")
$body
"
}

# _ck_put_stand_in <file> <script> puts an executable <script> at <file>. Its
# copy under $TMPDIR/dark-factory-standins/<sha256> ($CHECKS_STAND_IN_CACHE
# names another folder: the Go tests share internal/standin's) was run once (as a no-op,
# STANDIN_WARM=1) when made, so macOS's first-run check is paid once per
# distinct script; <file> is a hard link to it. With no checksum tool or a
# link that fails (another file system), a fresh file as before.
_ck_put_stand_in() {
	local f=$1 script=$2 cache sum tmp
	rm -f -- "$f"
	cache=${CHECKS_STAND_IN_CACHE:-${TMPDIR:-/tmp}}
	[ -n "${CHECKS_STAND_IN_CACHE:-}" ] || cache=${cache%/}/dark-factory-standins
	sum=$(printf '%s' "$script" | { shasum -a 256 2> /dev/null || sha256sum 2> /dev/null; })
	sum=${sum%% *}
	if [ -n "$sum" ] && mkdir -p "$cache" 2> /dev/null; then
		if ! printf '%s' "$script" | cmp -s - "$cache/$sum" &&
			tmp=$(mktemp "$cache/.tmp-XXXXXX" 2> /dev/null); then
			printf '%s' "$script" > "$tmp" && chmod 555 "$tmp" &&
				STANDIN_WARM=1 "$tmp" > /dev/null 2>&1 && mv -f "$tmp" "$cache/$sum"
			rm -f -- "$tmp"
		fi
		ln "$cache/$sum" "$f" 2> /dev/null && return 0
	fi
	printf '%s' "$script" > "$f"
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
	_ck_pin_identity
	local keep=() v
	for v in HOME TERM USER LANG TMUX_TMPDIR XDG_STATE_HOME XDG_CONFIG_HOME ISSUE_VIEWER_CONFIG CHECKS_STAND_IN_DIR \
		ISSUE_VIEWER_ISSUE_SLUG ISSUE_VIEWER_LEAD_SCOPE ISSUE_VIEWER_WORKER AGENT_NAME CLAUDECODE ${CLEAN_SHELL_KEEP:-}; do
		[ -n "${!v+x}" ] && keep+=("$v=${!v}")
	done
	_ck_argv=(env -i "${keep[@]}" SHELL="$CHECKS_STAND_IN_DIR/shell" PATH="$CHECKS_STAND_IN_DIR/bin:$PATH"
		bash --noprofile --norc -c "exec $(printf '%q ' "$@")")
}

# _ck_pin_identity: once per script, before anything drops the tmux name or
# leaves the worktree, a worker whose identity rests on those signals gets it
# as a variable clean_shell carries: ISSUE_VIEWER_ISSUE_SLUG when its issue is
# known, else ISSUE_VIEWER_WORKER. A lead's ISSUE_VIEWER_LEAD_SCOPE is carried
# as it is. It only keeps an identity; it never clears one.
_ck_pin_identity() {
	[ -z "${_ck_identity_pinned:-}" ] || return 0
	_ck_identity_pinned=1
	[ -z "${ISSUE_VIEWER_ISSUE_SLUG:-}${ISSUE_VIEWER_LEAD_SCOPE:-}${ISSUE_VIEWER_WORKER:-}" ] || return 0
	local cli=${SANDBOX_ISSUE_CLI:-${ISSUE_CLI:-${CHECKS_ISSUE_CLI:-}}} probe out role slug signal
	[ -n "$cli" ] || return 0
	probe=$(mktemp -d "$(_ck_tmp_base)/ck-whoami-XXXXXX") || return 0
	mkdir -p "$probe/issues"
	printf 'projects:\n  - name: "probe"\n    slug: "probe"\n    issues: "%s/issues"\n    workdir: "%s"\n' \
		"$probe" "$probe" > "$probe/projects.yaml"
	out=$("$cli" --config "$probe/projects.yaml" --project probe --json lead whoami 2> /dev/null)
	rm -rf -- "$probe"
	role=$(printf '%s\n' "$out" | sed -n 's/^ *"role": *"\([^"]*\)".*/\1/p' | head -n 1)
	[ "$role" = worker ] || return 0
	slug=$(printf '%s\n' "$out" | sed -n 's/^ *"issue": *"\([^"]*\)".*/\1/p' | head -n 1)
	signal=$(printf '%s\n' "$out" | sed -n 's/^ *"signal": *"\([^"]*\)".*/\1/p' | head -n 1)
	if [ -n "$slug" ]; then
		export ISSUE_VIEWER_ISSUE_SLUG=$slug
	else
		export ISSUE_VIEWER_WORKER=${signal:-worker}
	fi
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
	local url=${1:?wait_for_url needs a url} tries=$((${2:-60} * 5))
	while [ "$tries" -gt 0 ]; do
		curl -s -o /dev/null --max-time 1 "$url" && return 0
		sleep 0.2
		tries=$((tries - 1))
	done
	return 1
}
