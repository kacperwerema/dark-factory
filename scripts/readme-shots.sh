#!/usr/bin/env bash
# The README's screenshots, from a staged copy of the demo (make readme-shots).
#
#   scripts/readme-shots.sh            shoot docs/images/shots.yaml into docs/images
#   scripts/readme-shots.sh --serve    stage the same board and keep it running
#
# The repo is public, so nothing here touches the owner's setup: the demo is
# copied to a temp folder, HOME, XDG_STATE_HOME and CLAUDE_CONFIG_DIR point
# into it (the viewer writes viewer.json under HOME; the CEO chat reads the
# lead's transcript under CLAUDE_CONFIG_DIR), and the lead's tmux session
# runs on a private socket with TMUX unset. Teardown kills only that socket's
# server and this viewer.
set -euo pipefail

mode=shoot
case "${1:-}" in
	--serve) mode=serve ;;
	"") ;;
	*) echo "usage: $0 [--serve]" >&2; exit 2 ;;
esac

repo=$(git rev-parse --show-toplevel)
cd "$repo"
# Build first: HOME changes below, and Go keeps its caches under it.
go build -o .playtest/readme-shots/issue-viewer ./cmd/issue-viewer
go build -o .playtest/readme-shots/playtest ./cmd/playtest
viewer="$repo/.playtest/readme-shots/issue-viewer"

# pwd -P: the CEO chat finds the transcript by the project's absolute path,
# so stage under the resolved one (/tmp is /private/tmp on a Mac).
stage=$(cd "$(mktemp -d /tmp/demo-shots.XXXXXX)" && pwd -P)
cp -R demo "$stage/demo"
rm -rf "$stage/demo/shots"
mkdir -p "$stage/home/.local/state" "$stage/tmux"
# Spread the issues' times over the last few days, so waiting ages differ.
python3 -I - "$stage/demo/issues" <<'PY'
import os, sys, time, zlib
for root, _, files in os.walk(sys.argv[1]):
    for f in files:
        if f.endswith(".md"):
            t = time.time() - (2 + zlib.crc32(f.encode()) % 70) * 3600
            os.utime(os.path.join(root, f), (t, t))
PY
(
	cd "$stage/demo"
	git init -q -b main
	git -c user.name=Demo -c user.email=demo@example.com add -A
	git -c user.name=Demo -c user.email=demo@example.com commit -qm "demo: sample project"
)

export HOME="$stage/home" XDG_STATE_HOME="$stage/home/.local/state"
export CLAUDE_CONFIG_DIR="$stage/claude" TMUX_TMPDIR="$stage/tmux"
unset TMUX
tmuxs() { env -u TMUX tmux -S "$TMUX_TMPDIR/tmux-$(id -u)/default" "$@"; }

# The lead: its transcript (whole messages, question cards, an image) and
# a pane showing the open question, as Claude Code draws it.
slug=$(printf '%s' "$stage/demo" | sed 's/[^A-Za-z0-9]/-/g')
images="$stage/demo/.agent-logs/lead-demo/chat/images"
funnel=$(md5 -q demo/shots/checkout-funnel.png 2>/dev/null || md5sum demo/shots/checkout-funnel.png | cut -d' ' -f1)
mkdir -p "$CLAUDE_CONFIG_DIR/projects/$slug" "$images"
cp demo/shots/checkout-funnel.png "$images/$funnel.png"
sed -e "s#@STAGE@#$stage#g" -e "s#@FUNNEL@#$funnel#g" demo/shots/lead.jsonl >"$CLAUDE_CONFIG_DIR/projects/$slug/demo-lead.jsonl"

mkdir -p "$TMUX_TMPDIR/tmux-$(id -u)" && chmod 700 "$TMUX_TMPDIR/tmux-$(id -u)"
tmux_pane() { printf 'cat %q; exec sleep 86400' "$1"; }
tmuxs new-session -d -s lead-demo -x 150 -y 45 -c "$stage/demo" "$(tmux_pane "$repo/demo/shots/lead-pane.txt")"
# Workers on a few issues, so the counts and the CEO's side panel have them.
for w in backend-signed-webhook-payloads backend-add-rate-limiting-to-api-endpoints content-release-digest-for-1-0; do
	tmuxs new-session -d -s "agent-$w" -x 150 -y 45 -c "$stage/demo" "$(tmux_pane "$repo/demo/shots/worker-pane.txt")"
done

port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')
base="http://127.0.0.1:$port"
(cd "$stage" && exec "$viewer" -config demo/projects.yaml -port "$port") >"$stage/viewer.log" 2>&1 &
vpid=$!

cleanup() {
	kill "$vpid" 2>/dev/null || true
	env -u TMUX tmux -S "$TMUX_TMPDIR/tmux-$(id -u)/default" kill-server 2>/dev/null || true
	rm -rf "$stage"
}
trap cleanup EXIT

for _ in $(seq 50); do
	curl -fs -o /dev/null "$base/" && break
	sleep 0.2
done

if [ "$mode" = serve ]; then
	echo "demo board: $base   (stage: $stage, Ctrl-C to stop)"
	wait "$vpid"
	exit
fi

cd "$repo"
.playtest/readme-shots/playtest shots -list docs/images/shots.yaml -base "$base" -out docs/images
