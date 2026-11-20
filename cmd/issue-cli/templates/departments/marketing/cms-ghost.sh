#!/bin/sh
# cms-ghost.sh: the cms connector's helper for Ghost (MIT, ghost.org), our
# own code over Ghost's Admin API; no Ghost code is in it. The marketing
# department's publish step runs it through the connector:
#
#   issue-cli connector exec marketing/cms -- sh departments/marketing/cms-ghost.sh \
#     publish --file <approved.md> --title "<title>" --kind post|page \
#       [--slug s] [--excerpt e] [--meta-description d] [--at <ISO time>]
#   ... cms-ghost.sh update --id <id> --file <approved.md> [--title t] [--kind post|page]
#   ... cms-ghost.sh get --slug <slug> [--kind post|page]
#
# Each prints one JSON line, {"id":…,"status":…,"url":…}: url is what
# `issue-cli output publish <id> --url` records. On an error it prints the
# reason on stderr, nothing on stdout, and exits 1 (2 for a wrong call).
#
# - GHOST_ADMIN_KEY is the Admin API key, "<id>:<hex secret>", which the
#   connector reads from Keychain (keychain:ghost-admin-key). The helper
#   signs a 5-minute HS256 JWT with it (kid <id>, aud /admin/); the secret
#   never goes on a command line: the HMAC is computed here over stdin,
#   and the token reaches curl through a header file.
# - GHOST_URL is the site, default http://127.0.0.1:2368.
# - publish puts the piece out at once (status published), or schedules it
#   with --at (status scheduled). It never makes a draft and never passes
#   ?newsletter=, so publishing mails no one.
# - The markdown file goes as one Lexical markdown card, byte for byte: the
#   helper reads the card back out of the request it built and refuses to
#   send when it differs from the file (a file that isn't UTF-8).
# - update reads the post's updated_at first (Ghost refuses a write without
#   the current one), then writes; its status stays as it was.
#
# Needs curl, openssl and jq (1.6 or later).

set -u

die() {
	printf 'cms-ghost: %s\n' "$*" >&2
	exit 1
}

usage() {
	cat >&2 <<'EOF'
usage: cms-ghost.sh publish --file <md> --title <t> --kind post|page [--slug s] [--excerpt e] [--meta-description d] [--at <ISO time>]
       cms-ghost.sh update --id <id> --file <md> [--title t] [--kind post|page]
       cms-ghost.sh get --slug <slug> [--kind post|page]
env:   GHOST_ADMIN_KEY (<id>:<hex>, the Admin API key), GHOST_URL (default http://127.0.0.1:2368)
EOF
	exit 2
}

for tool in curl openssl jq; do
	command -v "$tool" >/dev/null 2>&1 || die "$tool not found on PATH (the helper needs curl, openssl and jq)"
done

[ $# -gt 0 ] || usage
verb=$1
shift
case $verb in
publish | update | get) ;;
*) usage ;;
esac

file='' title='' kind='' slug='' excerpt='' meta='' at='' id=''
while [ $# -gt 0 ]; do
	[ $# -ge 2 ] || { printf 'cms-ghost: %s needs a value\n' "$1" >&2; usage; }
	case $1 in
	--file) file=$2 ;;
	--title) title=$2 ;;
	--kind) kind=$2 ;;
	--slug) slug=$2 ;;
	--excerpt) excerpt=$2 ;;
	--meta-description) meta=$2 ;;
	--at) at=$2 ;;
	--id) id=$2 ;;
	*) printf 'cms-ghost: unknown flag %s\n' "$1" >&2; usage ;;
	esac
	shift 2
done

case $kind in
'' | post | page) ;;
*) die "--kind is post or page, not $kind" ;;
esac
case $verb in
publish)
	[ -n "$file" ] && [ -n "$title" ] && [ -n "$kind" ] || { printf 'cms-ghost: publish needs --file, --title and --kind\n' >&2; usage; }
	[ -z "$id" ] || die "publish takes no --id (update does)"
	;;
update)
	[ -n "$id" ] && [ -n "$file" ] || { printf 'cms-ghost: update needs --id and --file\n' >&2; usage; }
	[ -z "$slug$excerpt$meta$at" ] || die "update changes the text and title only: no --slug, --excerpt, --meta-description or --at"
	;;
get)
	[ -n "$slug" ] || { printf 'cms-ghost: get needs --slug\n' >&2; usage; }
	[ -z "$file$title$id$excerpt$meta$at" ] || die "get takes --slug and --kind only"
	;;
esac
# An id or slug goes into the path: letters, digits, - and _ only, so no
# ?, / or .. can reach Ghost through one.
case $id$slug in
*[!A-Za-z0-9_-]*) die "--id and --slug take letters, digits, - and _ only" ;;
esac
if [ -n "$file" ]; then
	[ -f "$file" ] && [ -r "$file" ] || die "no readable file $file"
fi

key=${GHOST_ADMIN_KEY:-}
case $key in
?*:?*) ;;
*) die "GHOST_ADMIN_KEY is not <id>:<hex> (the Admin API key; the cms connector reads it from keychain:ghost-admin-key)" ;;
esac
kid=${key%%:*}
secret=${key#*:}
case $secret in
*[!0-9a-fA-F]*) die "GHOST_ADMIN_KEY's secret is not hex" ;;
esac
[ $((${#secret} % 2)) -eq 0 ] || die "GHOST_ADMIN_KEY's secret has an odd number of hex digits"
[ ${#secret} -le 128 ] || die "GHOST_ADMIN_KEY's secret is longer than 64 bytes"

base=${GHOST_URL:-http://127.0.0.1:2368}
base=${base%/}
api=$base/ghost/api/admin

umask 077
tmp=$(mktemp -d) || die "no temp folder"
trap 'rm -rf "$tmp"' EXIT
trap 'rm -rf "$tmp"; exit 130' INT TERM

b64url() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }

# pad_octal <xor byte>: the key's 64 bytes (zero padded) XORed with the
# byte, as printf octal escapes. Builtins only: the secret is on no argv.
pad_octal() {
	rest=$secret out='' n=0
	while [ $n -lt 64 ]; do
		if [ -n "$rest" ]; then
			pair=${rest%"${rest#??}"}
			rest=${rest#??}
			b=$((0x$pair ^ $1))
		else
			b=$1
		fi
		out="$out\\$(printf '%03o' "$b")"
		n=$((n + 1))
	done
	printf '%s' "$out"
}

# jwt: the HS256 token, HMAC-SHA256 (RFC 2104) over openssl's stdin.
jwt() {
	now=$(date +%s)
	h=$(printf '{"alg":"HS256","typ":"JWT","kid":"%s"}' "$kid" | b64url)
	c=$(printf '{"iat":%s,"exp":%s,"aud":"/admin/"}' "$now" "$((now + 300))" | b64url)
	ipad=$(pad_octal 54)
	opad=$(pad_octal 92)
	# shellcheck disable=SC2059 # the pads are octal escapes on purpose
	{ printf "$ipad"; printf '%s.%s' "$h" "$c"; } | openssl dgst -sha256 -binary >"$tmp/inner" || die "openssl failed"
	# shellcheck disable=SC2059
	s=$({ printf "$opad"; cat "$tmp/inner"; } | openssl dgst -sha256 -binary | b64url) || die "openssl failed"
	printf '%s.%s.%s' "$h" "$c" "$s"
}

{
	printf 'Authorization: Ghost %s\n' "$(jwt)"
	printf 'Accept-Version: v6.0\n'
	printf 'Content-Type: application/json\n'
} >"$tmp/headers"

# call <method> <path> [<body file>]: the answer in $tmp/resp; a non-2xx
# answer returns its code as status 1 with $code set.
call() {
	set -- "$1" "$2" "${3:-}"
	if [ -n "$3" ]; then
		code=$(curl -sS -X "$1" -H @"$tmp/headers" --data-binary @"$3" -o "$tmp/resp" -w '%{http_code}' "$api$2") || die "$1 $2: curl failed (is Ghost up at $base?)"
	else
		code=$(curl -sS -X "$1" -H @"$tmp/headers" -o "$tmp/resp" -w '%{http_code}' "$api$2") || die "$1 $2: curl failed (is Ghost up at $base?)"
	fi
	case $code in
	2??) return 0 ;;
	esac
	return 1
}

fail() {
	msg=$(jq -r '[.errors[0].message, .errors[0].context] | map(select(. != null and . != "")) | join(": ")' "$tmp/resp" 2>/dev/null)
	die "$1: HTTP $code${msg:+: $msg}"
}

# check_card <coll>: the request body's markdown card, read back, equals
# $file byte for byte.
check_card() {
	jq -j --arg k "$1" '.[$k][0].lexical | fromjson | .root.children[0].markdown' "$tmp/body" >"$tmp/card" || die "could not read the request back"
	cmp -s "$tmp/card" "$file" || die "$file would not go out byte for byte (is it UTF-8?); nothing sent"
}

lexical='{root: {children: [{type: "markdown", version: 1, markdown: $md}], direction: null, format: "", indent: 0, type: "root", version: 1}} | tojson'

emit() {
	jq -c --arg k "$1" '.[$k][0] | {id, status, url}' "$tmp/resp" || die "Ghost's answer has no $1"
}

# find <how> <what>: the post or page (posts first) into $tmp/resp, its
# collection in $coll.
find_one() {
	colls='posts pages'
	[ -z "$kind" ] || colls=${kind}s
	for coll in $colls; do
		if call GET "/$coll/$1/"; then
			return 0
		fi
		[ "$code" = 404 ] || fail "GET /$coll/$1/"
	done
	die "no post or page at $1 (HTTP 404)"
}

case $verb in
publish)
	coll=${kind}s
	status=published
	[ -z "$at" ] || status=scheduled
	jq -n --rawfile md "$file" --arg k "$coll" --arg title "$title" --arg status "$status" \
		--arg slug "$slug" --arg excerpt "$excerpt" --arg meta "$meta" --arg at "$at" \
		"{(\$k): [{title: \$title, status: \$status, lexical: ($lexical)}
			+ (if \$slug != \"\" then {slug: \$slug} else {} end)
			+ (if \$excerpt != \"\" then {custom_excerpt: \$excerpt} else {} end)
			+ (if \$meta != \"\" then {meta_description: \$meta} else {} end)
			+ (if \$at != \"\" then {published_at: \$at} else {} end)]}" >"$tmp/body" || die "could not build the request from $file"
	check_card "$coll"
	call POST "/$coll/" "$tmp/body" || fail "POST /$coll/"
	emit "$coll"
	;;
update)
	find_one "$id"
	updated=$(jq -r --arg k "$coll" '.[$k][0].updated_at // empty' "$tmp/resp")
	[ -n "$updated" ] || die "GET /$coll/$id/: no updated_at in Ghost's answer"
	jq -n --rawfile md "$file" --arg k "$coll" --arg title "$title" --arg updated "$updated" \
		"{(\$k): [{updated_at: \$updated, lexical: ($lexical)}
			+ (if \$title != \"\" then {title: \$title} else {} end)]}" >"$tmp/body" || die "could not build the request from $file"
	check_card "$coll"
	call PUT "/$coll/$id/" "$tmp/body" || fail "PUT /$coll/$id/"
	emit "$coll"
	;;
get)
	find_one "slug/$slug"
	emit "$coll"
	;;
esac
