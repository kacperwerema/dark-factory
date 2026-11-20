#!/bin/sh
# fs-churn.sh: how many files a step writes, and how the fseventsd journal
# grows (docs/fs-churn.md). Every file written on macOS goes into the
# journal under /System/Volumes/Data/.fseventsd, and fseventsd replays it
# to clients that ask for history, so its CPU grows with the journal.
#
#   scripts/fs-churn.sh mark <stamp>          start counting: touch <stamp>
#   scripts/fs-churn.sh count <stamp> <dir>   files and links under <dir> changed since <stamp>
#   scripts/fs-churn.sh tree <dir>            files and links under <dir> (what a full install writes)
#   scripts/fs-churn.sh journal               the journal's size and file count (needs sudo)
#
# A CI run:   mark /tmp/ci.stamp; issue-cli ci run; count /tmp/ci.stamp <root>/.worktrees/.ci
# A worker:   mark /tmp/wt.stamp; issue-cli worktree add <slug>; count /tmp/wt.stamp <root>/.worktrees/<slug>
# A day:      journal, then the same 24 hours later; the difference is the growth.
set -eu

usage() {
	sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'
	exit 2
}

[ $# -ge 1 ] || usage
cmd=$1
shift
case $cmd in
mark)
	[ $# -eq 1 ] || usage
	: >"$1"
	echo "marked $1 at $(date '+%Y-%m-%d %H:%M:%S')"
	;;
count)
	[ $# -eq 2 ] || usage
	[ -e "$1" ] || { echo "fs-churn: no stamp $1 (run: $0 mark $1)" >&2; exit 2; }
	# -newer reads the modification time; a link counts once, never followed.
	n=$(find "$2" \( -type f -o -type l \) -newer "$1" 2>/dev/null | wc -l | tr -d ' ')
	echo "$n files changed under $2 since $1"
	;;
tree)
	[ $# -eq 1 ] || usage
	n=$(find "$1" \( -type f -o -type l \) 2>/dev/null | wc -l | tr -d ' ')
	echo "$n files under $1"
	;;
journal)
	j=/System/Volumes/Data/.fseventsd
	if [ "$(id -u)" -ne 0 ]; then
		echo "the journal is root's; run as the owner:"
		echo "  sudo sh -c 'du -sh $j; ls $j | wc -l; date'"
		exit 0
	fi
	du -sh "$j"
	echo "$(find "$j" -type f | wc -l | tr -d ' ') files in $j at $(date '+%Y-%m-%d %H:%M:%S')"
	;;
*)
	usage
	;;
esac
