# shellcheck shell=bash
# lib.sh's part (lib.sh sources it, issue-cli inlines it): the shared
# stand-ins (viewer, Claude input box), sandbox_yolo_on, seed_department.

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
	# shellcheck disable=SC2034 # pid: background needs a variable to set
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

