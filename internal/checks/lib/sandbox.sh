# shellcheck shell=bash
# lib.sh's part (lib.sh sources it, issue-cli inlines it): sandboxes:
# sandbox_dir, sandbox_project, sandbox_git_repo, is_worker, sic.

# ── Sandboxes ────────────────────────────────────────────────────────
_ck_tmp_base() {
	local base=${TMPDIR:-/tmp}
	[[ $base == */.worktrees/* ]] && base=/tmp
	echo "$base"
}

# _ck_stop_homes <dir>: stop every process whose CODEX_HOME or
# CLAUDE_CONFIG_DIR (or command line) is inside <dir>, at any parent. Only
# issue-cli can read another process's environment (ps no longer shows it
# on macOS): doctor leaks --under does the matching.
_ck_stop_homes() {
	"$CHECKS_ISSUE_CLI" doctor leaks --under "$1" --stop --quiet
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
	_ck_pin_identity
	_ck_sd_path=$(mktemp -d "$(_ck_tmp_base)/ck-XXXXXX") || {
		echo "sandbox_dir: mktemp failed" >&2
		exit 2
	}
	on_exit rm -rf -- "$_ck_sd_path"
	_ck_sd_path=$(cd -P -- "$_ck_sd_path" && pwd -P) || {
		echo "sandbox_dir: could not resolve the sandbox path" >&2
		exit 2
	}
	# Run before the rm above (cleanups go newest first): a real CLI a step
	# started with its home in here, and the daemon it detached (codex
	# app-server-daemon), outlive the script otherwise.
	on_exit _ck_stop_homes "$_ck_sd_path"
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
	_ck_sandbox_mark "$dir"
	_ck_sandbox_entry "$name" "$SANDBOX_PROJECT_DIR"
	_ck_sandbox_repo "$SANDBOX_PROJECT_DIR"
}
# _ck_sandbox_mark <dir>: the sandbox marker beside $SANDBOX_CONFIG. On a
# marked sandbox issue-cli's worker guard passes owner-only commands (except
# those that start agents) when every project path and both XDG dirs sit in
# <dir> under a temp folder. It declares; containment is the check.
_ck_sandbox_mark() {
	printf '{"folder": "%s", "created": "%s", "by": "lib.sh sandbox_project"}\n' \
		"$1" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$1/.issue-cli-sandbox"
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
	# shellcheck disable=SC2034 # set for the check script that sourced lib.sh
	SANDBOX_ADDED_PROJECT=$name
	# shellcheck disable=SC2034 # set for the check script that sourced lib.sh
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
	# shellcheck disable=SC2034 # set for the check script that sourced lib.sh
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
	# Before the cd: a worker known by its folder under .worktrees/ is known
	# here, not in the sandbox.
	[ -z "${CHECKS_STAND_IN_DIR:-}" ] || _ck_pin_identity
	(
		cd "${SANDBOX_PROJECT_DIR:?sic needs sandbox_project first}" || exit 2
		if [ -n "${CHECKS_STAND_IN_DIR:-}" ]; then
			clean_shell "${argv[@]}"
		else
			"${argv[@]}"
		fi
	)
}
