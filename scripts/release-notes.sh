#!/bin/sh
# release-notes.sh: write a GitHub release's notes from the CHANGELOG's
# "## <version> — <date>" section (the Release Marker action's notes step).
#
#   scripts/release-notes.sh <changelog> <version> <owner/repo> <out>
#
# GitHub refuses a release body over 125,000 characters (v0.62.0's section
# was 136,306). A section over RELEASE_NOTES_LIMIT bytes (default 120000;
# bytes are never fewer than characters) goes out short: each entry's bold
# title (an entry without one keeps its first sentence) and a link to the
# changelog at the tag. If even that is over, the list stops at the limit
# and says how many entries are left. A smaller section goes out in full.
set -eu

if [ "$#" -ne 4 ]; then
	echo "usage: $0 <changelog> <version> <owner/repo> <out>" >&2
	exit 2
fi
CHANGELOG=$1
VERSION=$2
REPO=$3
OUT=$4
LIMIT=${RELEASE_NOTES_LIMIT:-120000}
SOURCE=${RELEASE_NOTES_SOURCE:-cmd/issue-cli/CHANGELOG.md}

SECTION="$(mktemp)"
trap 'rm -f "$SECTION"' EXIT

awk -v v="$VERSION" '
	/^## / { if (found) done = 1; else if ($2 == v) { found = 1; next } }
	found && !done { print }
' "$CHANGELOG" | sed -e '/./,$!d' > "$SECTION"

if [ ! -s "$SECTION" ]; then
	echo "_No ${VERSION} section in ${SOURCE}._" > "$OUT"
	exit 0
fi

SIZE="$(wc -c < "$SECTION" | tr -d ' ')"
if [ "$SIZE" -le "$LIMIT" ]; then
	cp "$SECTION" "$OUT"
	exit 0
fi

LINK="https://github.com/${REPO}/blob/${VERSION}/${SOURCE}"
awk -v limit="$LIMIT" -v link="$LINK" -v source="$SOURCE" -v version="$VERSION" -v size="$SIZE" '
	# short is an entry line cut to its bold title, else its first sentence.
	function short(line,   rest, i, s, ticks, cut) {
		if (substr(line, 1, 4) == "- **") {
			rest = substr(line, 5)
			i = index(rest, "**")
			if (i > 0) return "- **" substr(rest, 1, i - 1) "**"
		}
		s = line
		i = index(s, ". ")
		if (i > 0) s = substr(s, 1, i)
		cut = 0
		if (length(s) > 240) {
			s = substr(s, 1, 240)
			sub(/ [^ ]*$/, "", s)
			cut = 1
		}
		ticks = gsub(/`/, "`", s)
		if (ticks % 2 == 1) s = s "`"
		if (cut) s = s " …"
		return s
	}
	{
		if (substr($0, 1, 2) == "- ") { row[n++] = short($0); entries++ }
		else row[n++] = $0
	}
	END {
		head = "The " version " section of the changelog (" size " bytes, " entries " entries) is over GitHub'"'"'s limit for release notes, so each entry is its title here. The full text: [" source "](" link ").\n"
		total = length(head) + 1
		for (i = 0; i < n; i++) total += length(row[i]) + 1
		print head
		if (total <= limit) {
			for (i = 0; i < n; i++) print row[i]
			exit
		}
		used = length(head) + 1
		left = entries
		for (i = 0; i < n; i++) {
			foot = "\n… and " left " more in the changelog: [" source "](" link ")."
			if (used + length(row[i]) + 1 + length(foot) + 1 > limit) break
			print row[i]
			used += length(row[i]) + 1
			if (substr(row[i], 1, 2) == "- ") left--
		}
		print "\n… and " left " more in the changelog: [" source "](" link ")."
	}
' "$SECTION" > "$OUT"
