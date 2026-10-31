#!/bin/sh
# security-check.sh: attack a throwaway viewer on loopback the way the
# internet would, and fail when a barrier from docs/API/security.md is down.
#
#   scripts/security-check.sh [-k]      (make security-check)
#
# It builds issue-viewer, starts it on 127.0.0.1 with a one-issue board, its
# own HOME / XDG dirs and its own tmux dir, runs the probes, then stops the
# viewer and every tmux server it may have started. Nothing is sent anywhere
# but that viewer; payloads that would run a command only touch a marker file
# in the sandbox. -k keeps the sandbox folder for a look afterwards.
#
# The Go tests (security_test.go, identity_test.go) hold the same barriers
# inside `make validate`; this script checks the built binary end to end.
set -u

keep=0
[ "${1:-}" = "-k" ] && keep=1

if command -v mem-guard >/dev/null 2>&1 && ! mem-guard check >/dev/null 2>&1; then
	echo "security-check: mem-guard check is BLOCKED; not starting" >&2
	exit 3
fi

repo=$(cd "$(dirname "$0")/.." && pwd)
sb=$(mktemp -d "${TMPDIR:-/tmp}/secchk.XXXXXX")
# A tmux socket path must stay short (~104 bytes): its own dir under /tmp.
tmuxdir=$(mktemp -d /tmp/secchk-tmux.XXXXXX)
port=$((20000 + $$ % 20000))
base="http://127.0.0.1:$port"
pid=""
fails=0

cleanup() {
	[ -n "$pid" ] && kill "$pid" 2>/dev/null && wait "$pid" 2>/dev/null
	env -u TMUX TMUX_TMPDIR="$tmuxdir" tmux kill-server 2>/dev/null
	rm -rf "$tmuxdir"
	if [ $keep -eq 1 ]; then echo "sandbox kept: $sb"; else rm -rf "$sb"; fi
}
trap cleanup EXIT INT TERM

mkdir -p "$sb/home" "$sb/state" "$sb/config" "$sb/repo/issues" "$sb/repo/docs" "$sb/markers"
cat >"$sb/repo/issues/probe.md" <<'EOF'
---
title: "Probe"
status: "backlog"
---

Body.
EOF
echo "# Doc" >"$sb/repo/docs/index.md"
echo SECRET >"$sb/home/secret.txt"
cat >"$sb/projects.yaml" <<EOF
terminal: none
projects:
  - name: Sec
    slug: sec
    issues: $sb/repo/issues
    docs: $sb/repo/docs
    workdir: $sb/repo
EOF

(cd "$repo" && go build -o "$sb/issue-viewer" .) || { echo "security-check: build failed" >&2; exit 1; }
env -u TMUX HOME="$sb/home" XDG_STATE_HOME="$sb/state" XDG_CONFIG_HOME="$sb/config" \
	TMUX_TMPDIR="$tmuxdir" ISSUE_VIEWER_TELEMETRY=off \
	"$sb/issue-viewer" -config "$sb/projects.yaml" -host 127.0.0.1 -port "$port" -record-viewer=false \
	>"$sb/viewer.log" 2>&1 &
pid=$!
i=0
until curl -s -o /dev/null "$base/"; do
	i=$((i + 1))
	if [ $i -gt 50 ]; then echo "security-check: viewer did not start" >&2; cat "$sb/viewer.log" >&2; exit 1; fi
	sleep 0.2
done

# expect <name> <want-code-pattern> <curl args...>: the status must match.
expect() {
	name=$1 want=$2
	shift 2
	got=$(curl -s --path-as-is -o "$sb/body" -w '%{http_code}' "$@")
	case $got in
	$want) echo "ok   $name ($got)" ;;
	*) echo "FAIL $name: got $got, want $want"; fails=$((fails + 1)) ;;
	esac
}
# refute <name> <text>: the last body must not contain text.
refute() {
	if grep -qF -- "$2" "$sb/body"; then echo "FAIL $1: body has $2"; fails=$((fails + 1)); else echo "ok   $1"; fi
}

echo "== Host, proxy and identity"
expect "foreign Host refused (rebinding)" 421 -H "Host: attacker.example" "$base/"
expect "public request without identity: 403" 403 -H "Cf-Ray: 1" "$base/p/sec/"
expect "forwarded request without identity: 403" 403 -H "X-Forwarded-For: 203.0.113.9" "$base/p/sec/issues.json"
expect "forged token without identity config: 403" 403 -H "Cf-Access-Jwt-Assertion: a.b.c" -H "Cf-Ray: 1" "$base/"
expect "loopback answered" 200 "$base/p/sec/"

echo "== Cross-origin writes"
expect "cross-site POST refused" 403 -X POST -H "Origin: https://attacker.example" -H "Content-Type: application/json" -d '{"title":"x"}' "$base/p/sec/issues/create"
expect "Origin null POST refused" 403 -X POST -H "Origin: null" -H "Content-Type: application/json" -d '{"title":"x"}' "$base/p/sec/issues/create"

echo "== Headers"
curl -s -D "$sb/headers" -o /dev/null "$base/p/sec/"
for h in "X-Content-Type-Options: nosniff" "X-Frame-Options: SAMEORIGIN" "frame-ancestors 'self'"; do
	if grep -qiF -- "$h" "$sb/headers"; then echo "ok   header $h"; else echo "FAIL header $h missing"; fails=$((fails + 1)); fi
done

echo "== Injection"
for path in retros/review issue/probe/dispatch lead/spawn; do
	expect "shell agent refused: $path" 400 -X POST -H "Content-Type: application/json" \
		-d "{\"agent\":\"touch $sb/markers/agent;true\",\"force\":true}" "$base/p/sec/$path"
done
expect "system path traversal refused" "4??" -X POST -H "Content-Type: application/json" -d '{"title":"esc","system":"../../markers"}' "$base/p/sec/issues/create"
expect "create over an existing issue refused" "4??" -X POST -H "Content-Type: application/json" -d '{"title":"Probe","body":"x"}' "$base/p/sec/issues/create"
expect "crafted approve status refused" 400 -X POST -H "Content-Type: application/json" -d '{"status":"done\nx"}' "$base/p/sec/issue/probe/approve"
expect "frontmatter injection escaped" 201 -X POST -H "Content-Type: application/json" -d '{"title":"fm","version":"1\"\nassignee: evil"}' "$base/p/sec/issues/create"
if grep -q '^assignee:' "$sb/repo/issues/fm.md" 2>/dev/null; then echo "FAIL frontmatter key injected"; fails=$((fails + 1)); else echo "ok   no injected key"; fi

echo "== XSS"
expect "issue with raw HTML created" 201 -X POST -H "Content-Type: application/json" \
	-d '{"title":"xss","body":"a <img src=x onerror=alert(1)> [l](javascript:alert(2))"}' "$base/p/sec/issues/create"
expect "issue page" 200 "$base/p/sec/issue/xss"
refute "no live onerror" "<img src=x onerror"
refute "no javascript: link" 'href="javascript:'
printf 'GIF89a<script>alert(1)</script>' >"$sb/x.html"
curl -s -X POST -F "file=@$sb/x.html;filename=x.html" "$base/p/sec/upload" >"$sb/upload"
url=$(sed -n 's/.*"url":"\([^"]*\)".*/\1/p' "$sb/upload")
case $url in *.gif) echo "ok   upload saved as .gif" ;; *) echo "FAIL upload saved as $url"; fails=$((fails + 1)) ;; esac
expect "attachment listing refused" 404 "$base/p/sec/attachments/"

echo "== Paths"
mkdir -p "$sb/repo/issues/attachments"
ln -sf "$sb/home/secret.txt" "$sb/repo/issues/attachments/0123456789abcdef0123456789abcdef.png"
expect "symlinked attachment refused" 404 "$base/p/sec/attachments/0123456789abcdef0123456789abcdef.png"
for p in "files/..%2f..%2fhome%2fsecret.txt" "shelf/file/..%2f..%2f..%2fhome%2fsecret.txt" "ci/log/..%2f..%2fsecret" "page-raw/..%2f..%2fhome%2fsecret.txt" "attachments/..%2f..%2f..%2fhome%2fsecret.txt"; do
	expect "traversal refused: $p" "4??" "$base/p/sec/$p"
	refute "no secret: $p" SECRET
done

echo "== Limits"
head -c 5000000 /dev/zero | tr '\0' a >"$sb/big"
expect "oversized body refused" "4??" -X POST -H "Content-Type: application/json" --data-binary "@$sb/big" "$base/p/sec/issue/probe/comments"

if [ -e "$sb/markers/agent" ]; then echo "FAIL a payload ran a command"; fails=$((fails + 1)); fi
echo
if [ $fails -gt 0 ]; then
	echo "security-check: $fails barrier(s) down"
	exit 1
fi
echo "security-check: all barriers hold"
