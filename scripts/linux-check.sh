#!/bin/sh
# linux-check.sh: the public CI's steps (.github/workflows/ci.yml) in an
# ubuntu:24.04 container, the base of its ubuntu-latest runner, for a change
# that has to pass on Linux. Behind `make linux-check`.
#
# It runs this checkout's working tree, uncommitted and untracked files
# included, ignored files not. They are copied in over stdin: nothing of the
# checkout is mounted, so the container can't change it. The history comes
# along in a git bundle, as ci.yml's fetch-depth: 0 has it.
#
# The image (dark-factory-linux-check, about 1 GB) has apt tmux and shellcheck,
# Go through GOTOOLCHAIN and a non-root user, and is kept between runs so the
# second run skips apt: docker rmi dark-factory-linux-check takes it away.
#
# Not golang:1.25: that is Debian, whose /etc/profile resets PATH in login
# shells and whose tmux 3.5a behaves differently. On an Apple Silicon Mac the
# run is arm64 and the runner amd64.
#
# LINUX_CHECK_PKG and LINUX_CHECK_RUN (make linux-check PKG=./internal/x
# RUN=TestY) run only `go test <pkg> -run <run> -count=1` in the container,
# for debugging one test on Linux: a package alone runs all its tests, a run
# alone looks in ./... . Such a run says it was a subset, and its PASS is not
# CI's.
#
# LINUX_CHECK_MEMORY caps the container (default 6g). The last line on stdout
# is `linux-check: PASS` or `linux-check: FAIL (<step>, <step>)`, every red step
# named, as make validate does. Exit 0 on PASS, 1 on a red step, 2 when it
# could not run them. It refuses to start when `mem-guard check` is blocked.
set -u

# go.mod's bare `go 1.25` can't be auto-downloaded, so a full version is named
# here (TestLinuxCheckGoFollowsGoMod keeps it on go.mod's line).
go_version=go1.25.14
image=dark-factory-linux-check
memory=${LINUX_CHECK_MEMORY:-6g}
pkg=${LINUX_CHECK_PKG:-}
run=${LINUX_CHECK_RUN:-}

say() {
	echo "linux-check: $*"
}

red=''
run_step() {
	name=$1
	shift
	say "== $name: $*"
	"$@" || red="${red:+$red, }$name"
}

# The steps of ci.yml, in its order; every one runs even after a red one.
# TestLinuxCheckRunsCIsSteps fails when ci.yml gains a step this doesn't have.
inside() {
	if [ -n "$pkg$run" ]; then
		subset
		return
	fi
	run_step gofmt make fmt-check
	run_step "go vet" go vet ./...
	run_step "go test" env PLAYTEST_CHROME=none go test ./... -count=1
	if [ -n "$red" ]; then
		say "FAIL ($red)"
		return 1
	fi
	say PASS
}

# One package or test, nothing else of CI's steps.
subset() {
	say "subset: only go test ${pkg:-./...}${run:+ -run $run}, not CI's full steps"
	if [ -n "$run" ]; then
		run_step "go test" env PLAYTEST_CHROME=none go test "${pkg:-./...}" -run "$run" -count=1
	else
		run_step "go test" env PLAYTEST_CHROME=none go test "$pkg" -count=1
	fi
	say "that was a subset, not CI's full steps"
	if [ -n "$red" ]; then
		say "FAIL ($red)"
		return 1
	fi
	say PASS
}

root=$(cd "$(dirname "$0")/.." && pwd -P) || exit 2
cd "$root" || exit 2

if [ "${1:-}" = --inside ]; then
	inside
	exit
fi

if command -v mem-guard > /dev/null 2>&1; then
	if ! out=$(mem-guard check 2>&1); then
		say "not starting, the container counts as heavy: $out" >&2
		exit 2
	fi
fi
if ! command -v docker > /dev/null 2>&1; then
	say "docker is not installed; this target needs it and won't install it" >&2
	exit 2
fi
if ! docker info > /dev/null 2>&1; then
	say "docker is installed but its daemon doesn't answer: start it (Docker Desktop on a Mac)" >&2
	exit 2
fi

say "building $image if it changed (apt and Go, minutes the first time)"
docker build --quiet -t "$image" - > /dev/null << EOF || exit 2
FROM ubuntu:24.04
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates git golang-go make shellcheck tmux \
 && useradd -m ci
USER ci
ENV LANG=C.UTF-8 GOTOOLCHAIN=$go_version
RUN go version
EOF

tmp=$(mktemp -d "${TMPDIR:-/tmp}/linux-check-XXXXXX") || exit 2
trap 'rm -rf -- "$tmp"' EXIT

git bundle create -q "$tmp/head.bundle" HEAD --tags || exit 2
# bsdtar on a Mac would add ._ files and xattr headers without these.
COPYFILE_DISABLE=1
export COPYFILE_DISABLE
git -c core.quotePath=false ls-files --cached --others --exclude-standard |
	while IFS= read -r f; do
		if [ -e "$f" ] || [ -L "$f" ]; then
			printf '%s\n' "$f"
		fi
	done > "$tmp/files"
tar --no-xattrs -cf "$tmp/tree.tar" -T "$tmp/files" || exit 2
rm -f "$tmp/files"

# A real clone (make fmt-check asks git), then the working tree over it.
# shellcheck disable=SC2016 # expanded inside the container
unpack='set -e
mkdir /tmp/in
tar -xf - -C /tmp/in
git clone -q /tmp/in/head.bundle "$HOME/work" 2> /dev/null
cd "$HOME/work"
git ls-files -z | xargs -0 rm -f
tar -xf /tmp/in/tree.tar
rm -rf /tmp/in
git add -A
exec sh scripts/linux-check.sh --inside'

say "running in ubuntu:24.04 with $go_version, as a non-root user, memory cap $memory"
if [ -n "$pkg$run" ]; then
	say "a subset: go test ${pkg:-./...}${run:+ -run $run} only"
fi
tar --no-xattrs -cf - -C "$tmp" head.bundle tree.tar |
	docker run --rm -i --init --memory "$memory" \
		-e "LINUX_CHECK_PKG=$pkg" -e "LINUX_CHECK_RUN=$run" \
		"$image" sh -c "$unpack"
# The pipeline's status is docker's: its command's exit, 125 and up for its own.
status=$?
if [ "$status" -ne 0 ] && [ "$status" -ne 1 ]; then
	say "the container stopped without a verdict (exit $status)" >&2
	exit 2
fi
exit "$status"
