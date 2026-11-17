#!/bin/sh
# under-load.sh: run a command beside a synthetic CPU and memory hog, the way
# CI runs while several workers run `make validate` on the same machine.
#
#   scripts/under-load.sh [-c CPU_HOGS] [-m MEM_MB] [-x MAX_PCT] -- <command...>
#
# -c  busy loops to start (default: the core count)
# -m  memory the hog holds, in MB (default 2048)
# -x  stop everything once mem-guard reports this much used (default 70)
#
# It refuses to start when `mem-guard check` says BLOCKED, and it only ever
# kills the hogs it started itself (and the command, if memory gets past -x).
# Exit status is the command's; 3 when it was stopped for memory.
set -u

cpu=$(sysctl -n hw.ncpu 2>/dev/null || nproc)
mem_mb=2048
max_pct=70
while getopts c:m:x: opt; do
	case $opt in
	c) cpu=$OPTARG ;;
	m) mem_mb=$OPTARG ;;
	x) max_pct=$OPTARG ;;
	*) exit 2 ;;
	esac
done
shift $((OPTIND - 1))
[ "${1:-}" = "--" ] && shift
if [ $# -eq 0 ]; then
	echo "usage: $0 [-c cpu] [-m mb] [-x pct] -- <command...>" >&2
	exit 2
fi

used_pct() {
	mem-guard used 2>/dev/null
}

if command -v mem-guard >/dev/null 2>&1; then
	if ! mem-guard check >/dev/null 2>&1; then
		echo "under-load: mem-guard check is BLOCKED; not starting" >&2
		exit 3
	fi
	guard=1
else
	echo "under-load: no mem-guard on PATH; memory is not watched" >&2
	guard=0
fi

hogs=""
cmd_pid=""
stop_hogs() {
	# shellcheck disable=SC2086
	[ -n "$hogs" ] && kill $hogs 2>/dev/null
	hogs=""
}
# A signal goes on to the command as itself (Ctrl-C reaches it from the
# terminal too; a second INT is harmless).
trap 'stop_hogs; [ -n "$cmd_pid" ] && kill -INT "$cmd_pid" 2>/dev/null; exit 130' INT
trap 'stop_hogs; [ -n "$cmd_pid" ] && kill -TERM "$cmd_pid" 2>/dev/null; exit 143' TERM
trap 'stop_hogs' EXIT

i=0
while [ "$i" -lt "$cpu" ]; do
	sh -c 'while :; do :; done' &
	hogs="$hogs $!"
	i=$((i + 1))
done
if [ "$mem_mb" -gt 0 ]; then
	perl -e '$x = "a" x ($ARGV[0] * 1024 * 1024); sleep 86400' "$mem_mb" &
	hogs="$hogs $!"
fi
echo "under-load: $cpu busy loops, ${mem_mb} MB held, stop at ${max_pct}% (pids$hogs)" >&2

# A non-interactive sh starts a background job with SIGINT and SIGQUIT
# ignored, and a child cannot trap a signal ignored on entry: perl puts them
# back to the default before it execs the command, so the command sees
# Ctrl-C as it would outside this script.
perl -e '$SIG{INT} = $SIG{QUIT} = "DEFAULT"; exec { $ARGV[0] } @ARGV or do { print STDERR "under-load: $ARGV[0]: $!\n"; exit 127 }' -- "$@" &
cmd_pid=$!
over=0
while kill -0 "$cmd_pid" 2>/dev/null; do
	if [ "$guard" = 1 ]; then
		pct=$(used_pct)
		if [ -n "$pct" ] && [ "$pct" -ge "$max_pct" ]; then
			echo "under-load: memory at ${pct}%, stopping" >&2
			stop_hogs
			kill "$cmd_pid" 2>/dev/null
			over=1
			break
		fi
	fi
	sleep 2
done
wait "$cmd_pid"
status=$?
stop_hogs
[ "$over" = 1 ] && exit 3
exit "$status"
