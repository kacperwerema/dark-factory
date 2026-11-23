.PHONY: build build-cli install hooks fmt-check merge-check leak-check codemap-check guard-check demo readme-shots clean validate test vet cover-cli catalog-check link-check playtest-check ux-test playtest playtest-report shot serve serve-stop security-check ported-checks linux-check

# The build's version is `git describe`: the tag on a release or release
# candidate (v0.54.0-rc.1), "<tag>-N-g<sha>" past one, "-dirty" with
# uncommitted changes. `issue-cli version`, the sidebar and the usage log
# show it. Override with make install VERSION=...
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.Version=$(VERSION)

build:
	go build -ldflags "$(LDFLAGS)" -o issue-viewer ./cmd/issue-viewer/

build-cli:
	go build -ldflags "$(LDFLAGS)" -o issue-cli ./cmd/issue-cli/

# Where make install puts both binaries: go install's folder ($GOBIN, else
# the first GOPATH's bin) unless BINDIR= names one (BINDIR=~/.local/bin).
BINDIR ?= $(or $(shell go env GOBIN),$(firstword $(subst :, ,$(shell go env GOPATH)))/bin)

# Each binary is built to a temporary file in BINDIR and renamed over the old
# one: a new inode, in one step. Never cp a build over a binary in place:
# macOS keeps the old file's code signature for that inode and kills the new
# one at exec (exit 137, no output), and a running agent may run it half
# written.
install: hooks
	@mkdir -p "$(BINDIR)"
	@for b in issue-cli issue-viewer; do \
	  tmp="$(BINDIR)/.$$b.new.$$$$"; \
	  go build -ldflags "$(LDFLAGS)" -o "$$tmp" ./cmd/$$b/ && chmod 0755 "$$tmp" && mv -f "$$tmp" "$(BINDIR)/$$b" \
	    || { rm -f "$$tmp"; exit 1; }; \
	  echo "installed $$b $(VERSION) → $(BINDIR)/$$b"; \
	done

# The commit-msg hook: a Catalog: or Proves: line that would mark or link
# nothing is refused at commit time, with the fix (issue-cli workflow
# commit-msg). Written to the common .git/hooks, shared by every worktree.
hooks:
	@go run ./cmd/issue-cli workflow commit-msg --install

# make demo PORT=8081 when your own viewer already has 8080.
PORT ?= 8080

demo: build
	./issue-viewer -config demo/projects.yaml -port $(PORT)

# The README's screenshots (docs/images/shots.yaml) from a staged copy of the
# demo, with a CEO and workers on a private tmux server. Needs Chrome
# (PLAYTEST_CHROME on a Mac without it in /Applications).
readme-shots:
	scripts/readme-shots.sh

clean:
	rm -f issue-viewer issue-cli cover.out $(CLI_COVER_PROFILE)
	rm -rf .playtest

# The coverage gate on the cmd/issue-cli package fails the build when coverage
# drops below the configured floor so regressions can't slip in unnoticed.
CLI_COVERAGE_FLOOR ?= 70.0

vet:
	go vet ./...

# In local CI ($CI_RESULTS_DIR set) the tests also write go test -json for the
# health tests family; ci gotest prints the same text plain go test would.
# The slowest packages are listed before ./... so they start first: go test
# runs packages in the order they are named (each once), and the viewer named
# only through ./... started about 26 s after the first package.
TEST_FIRST ?= ./internal/viewer ./cmd/issue-cli ./internal/bench
# The slowest packages' tests can't run in parallel inside one binary
# (t.Setenv, os.Chdir, package globals), so tools/test-shards splits each
# by recorded time into go test processes that run beside the rest:
# TEST_SHARD_PKGS names each package with its count (<pkg>=<n>, n=1: one).
# cmd/issue-cli takes TEST_SHARDS. TestShardedPackagesRunEveryTestOnce
# checks every package here splits into shards that run each test once.
TEST_SHARDS ?= 4
TEST_SHARD_PKGS ?= ./cmd/issue-cli=$(TEST_SHARDS) ./internal/viewer=3 ./internal/bench=3 ./internal/agentrun=2

# The suite also writes cmd/issue-cli's coverage profile (CLI_COVER_PROFILE):
# -coverpkg instruments only cmd/issue-cli's packages, in each test binary
# that links them, so cover-cli reads that profile instead of running those
# tests a second time (about 135 s of make validate). The old profile is
# emptied first, so a run that writes none can't hand cover-cli stale numbers;
# emptied, not removed, so its folder's mtime stays put.
CLI_COVER_PKG := ./cmd/issue-cli/...
# The profile lives outside the checkout, one per checkout (by its path): a
# test that walks the repo hashes every folder it opens with each entry's
# size and mtime, so a profile rewritten in the tree on each run would send
# all of them back to running fresh.
CLI_COVER_PROFILE ?= $(patsubst %/,%,$(or $(TMPDIR),/tmp))/dark-factory-cover-$(shell printf '%s' '$(CURDIR)' | cksum | cut -d' ' -f1).out

# Packages come from go test's cache when nothing they read changed: a
# second validate on an unchanged tree reruns only what changed or must
# always run. scripts/test-cache.txt lists each test package cached or fresh
# (fresh: -count=1, with why) and the fresh-test tests that run on their own;
# TestEveryTestPackageIsInTheCacheList keeps it whole. TEST_CACHE=0 runs
# everything with -count=1 (local CI's slow tier does).
TEST_CACHE ?= 1
TEST_CACHE_LIST := scripts/test-cache.txt
# SLOW_TESTS=1 (local CI's slow tier) also runs the tests that call
# slowtier.Only: whole-run tests beside a cheaper fast-tier one that proves
# the same (docs/testing.md#test-tiers). make validate leaves it at 0.
SLOW_TESTS ?= 0
ifeq ($(SLOW_TESTS),1)
export DF_SLOW_TESTS := 1
endif
# cmd/playtest's Chrome tests (~60 s of the suite) run in make test only
# when the change since main touches what they drive (scripts/touches.sh on
# these paths), and always under SLOW_TESTS=1: the suite gets
# DF_PLAYTEST_E2E=1 or 0, and a plain go test (unset) runs them
# (docs/testing.md#test-tiers; TestChromeGatePathsMatchTheMakefile).
PLAYTEST_E2E_PATHS := cmd/playtest/ playtest/ internal/viewer/templates/ internal/viewer/static/
PLAYTEST_E2E = $(if $(filter 1,$(SLOW_TESTS)),1,$(shell scripts/touches.sh $(PLAYTEST_E2E_PATHS)))
test: export DF_PLAYTEST_E2E = $(PLAYTEST_E2E)

test:
	@command -v shellcheck >/dev/null 2>&1 || echo "test: no shellcheck on PATH: TestLibPassesShellcheck skips, lib.sh goes unchecked (make linux-check runs it)"
	@: > $(CLI_COVER_PROFILE)
	go run ./tools/test-shards $(addprefix -shard ,$(TEST_SHARD_PKGS)) -cache-list $(TEST_CACHE_LIST) -- $(TEST_FIRST) ./... $(if $(filter 0,$(TEST_CACHE)),-count=1) -coverpkg=$(CLI_COVER_PKG) -coverprofile=$(CLI_COVER_PROFILE) $(if $(filter 1,$(GUARD_SKIP)),-skip '^($(GUARD_TESTS))$$') $(if $(CI_RESULTS_DIR),-json | go run ./cmd/issue-cli ci gotest --out $(CI_RESULTS_DIR)/unit.json)

# The floor check. In validate with test among its steps (COVER_FROM_TEST=1)
# it reads the profile the test step wrote, and a missing one is red; run
# alone, it runs cmd/issue-cli's tests itself first.
# TestValidateCoverageFloorReadsTheTestRun keeps it so.
cover-cli:
	@if [ "$(COVER_FROM_TEST)" != 1 ]; then rm -f $(CLI_COVER_PROFILE); go test -coverpkg=$(CLI_COVER_PKG) -coverprofile=$(CLI_COVER_PROFILE) $(CLI_COVER_PKG) -count=1 >/dev/null; fi
	@[ -s "$(CLI_COVER_PROFILE)" ] || { echo "cover-cli: no coverage profile at $(CLI_COVER_PROFILE): the test step wrote none (make cover-cli alone runs the tests itself)"; exit 1; }
	@prof=$(CLI_COVER_PROFILE); \
	  if [ "$(COVER_FROM_TEST)" = 1 ] && [ -s "$(GUARD_COVER_PROFILE)" ]; then \
	    prof=$(GUARD_COVER_PROFILE).merged; { cat $(CLI_COVER_PROFILE); tail -n +2 $(GUARD_COVER_PROFILE); } > $$prof; \
	  fi; \
	  pct=$$(go tool cover -func=$$prof | awk '/^total:/ { sub("%","",$$3); print $$3 }'); \
	  printf "cmd/issue-cli coverage: %s%% (floor: %s%%)\n" "$$pct" "$(CLI_COVERAGE_FLOOR)"; \
	  awk -v p="$$pct" -v f="$(CLI_COVERAGE_FLOOR)" 'BEGIN { exit (p+0 < f+0) }' || \
	    { echo "FAIL: coverage below floor"; exit 1; }

# A branch that changes a catalog feature's code (its sources:) or a bundled
# template must bump its version past the last release, or say why not with
# a `Catalog: n/a <id> <why>` trailer. Skipped on main.
catalog-check:
	@go run ./cmd/issue-cli workflow catalog-check

# No merge left half done: the checkout's own git dir (a worktree's, not the
# shared one) has no MERGE_HEAD, and no tracked text file has a line that
# starts with a conflict marker (seven < or > and a space). Each hit is named
# file:line. A fixture that needs marker lines goes on MERGE_CHECK_ALLOW, as
# git pathspecs (internal/foo/testdata/conflict.txt).
MERGE_CHECK_ALLOW ?=

merge-check:
	@gd=$$(git rev-parse --git-dir) || exit 1; red=""; \
	if [ -f "$$gd/MERGE_HEAD" ]; then \
	  echo "merge-check: unfinished merge ($$gd/MERGE_HEAD is set): finish it with git commit or drop it with git merge --abort"; red=1; \
	fi; \
	hits=$$(git grep -n -I -E '^(<{7}|>{7})( |$$)' -- . $(foreach p,$(MERGE_CHECK_ALLOW),':(exclude)$(p)')); \
	if [ -n "$$hits" ]; then \
	  echo "merge-check: conflict markers (file:line):"; echo "$$hits" | cut -d: -f1,2; red=1; \
	fi; \
	[ -z "$$red" ]

# Every tracked .go file is gofmt-clean (testdata included: fixtures are Go
# too). Fix with gofmt -w <file>.
fmt-check:
	@files=$$(git ls-files -z -- '*.go' | xargs -0 gofmt -l); \
	  if [ -n "$$files" ]; then echo "gofmt -l (run gofmt -w on these):"; echo "$$files"; exit 1; fi

# Every relative link in the tracked markdown (docs/, README, the guides)
# points at a file; the fragment is not checked (internal/doclinks).
link-check:
	@go test ./internal/doclinks -run TestRepoDocLinks -count=1

# The two tests that scan the tracked tree for home paths, private names and
# the upstream name: the same tests `test` runs, as a step of its own so a leak
# shows before the suite. TestValidateRunsItsCheapStepsFirst catches a renamed
# test: -run would match nothing and pass.
leak-check:
	@go test ./internal/privacy -run '^(TestTrackedFilesCarryNoLeaks|TestTrackedFilesNameUpstreamOnlyInLicense|TestTrackedFilesCarryNoSecrets)$$' -count=1

# The engine's code map (workbench.contexts in workflow.yaml) maps every
# package and knows every upward import: the same test `test` runs, as a step
# of its own so a new internal/ package nobody mapped shows before the suite.
# Its message names the package and the workbench.contexts fix.
codemap-check:
	@go test ./internal/codemap -run '^TestEngineMapHasNoNewUpwardImport$$' -count=1

# The repo-wide guard tests (scripts/guard-tests.txt: a docs table listing
# every command, every lint check in the docs, doc numbers that agree...) run
# on their own, so a missing docs line shows in seconds; each failing one gets
# its rule from docs/testing.md's Guard tests table, and the commit-msg hook
# names this step at a branch's first commit touching cmd/ or internal/.
# TestGuardCheckListsEveryGuardTest keeps the list whole: a TestEvery*,
# Test*ListedInDocs or TestDocNumbers* test missing from it, or a listed name
# no package has, fails. In validate with test among its steps they run here
# only: test skips them (GUARD_SKIP=1: -skip on GUARD_TESTS) and guard-check
# writes their share of cmd/issue-cli's coverage to GUARD_COVER_PROFILE, which
# cover-cli adds to the suite's (TestValidateRunsTheGuardTestsOnce).
GUARD_TESTS = $(shell awk '!/^\#/ && NF == 2 { print $$2 }' scripts/guard-tests.txt 2>/dev/null | sort -u | paste -sd '|' -)
GUARD_COVER_PROFILE ?= $(CLI_COVER_PROFILE:.out=.guard.out)

guard-check:
	@GUARD_COVER_PKG=$(if $(filter 1,$(COVER_FROM_TEST)),$(CLI_COVER_PKG)) GUARD_COVER_PROFILE=$(if $(filter 1,$(COVER_FROM_TEST)),$(GUARD_COVER_PROFILE)) scripts/guard-check.sh

# With test among the steps, the steps listed before it (the cheap ones) run
# one by one in the background while the suite runs in front: each cheap
# step's output goes to a file and is printed whole when the step ends, each
# line led by `[<step>]`. The steps after test (cover-cli) wait for both.
# Without test the steps run one by one as listed. A red step doesn't stop
# the next, so one known red can't hide what the later steps would say. A red
# step prints `validate: red so far: <step>, <step>` at once: a worker watching
# the log can stop there instead of waiting for the suite. A red test step on a
# work branch is followed by one `validate: main has <sha> <subject> touching
# <path>: merge main and re-run` line per commit main has since the merge base
# that touches a failing test's file or package (tools/main-fix-hint,
# ship.MainFixHints): main may have fixed it already. The last line on stdout is
# `validate: PASS` or `validate: FAIL (<step>, <step>)`, every red step named
# in VALIDATE_STEPS order, so a run in the background with its output in a
# log can't hide a red step behind a wrapper's exit 0: read that line. An
# empty list is a FAIL, not a PASS: a subset is VALIDATE_STEPS="vet test".
# Each step prints its wall time when it ends, `validate: <step> <seconds>s`
# (a cheap step's after its output), and `validate: total <seconds>s` comes
# just before the verdict: local CI keeps those lines as the run's steps
# (internal/ci ParseSteps) and ci status names the slowest.
VALIDATE_STEPS ?= merge-check fmt-check vet catalog-check link-check leak-check codemap-check guard-check playtest-check test cover-cli
# The red test step's hint command, given the step's output file (a test stands it in).
MAIN_FIX_HINT ?= go run ./tools/main-fix-hint
VALIDATE_FLAGS = $(if $(filter test,$(VALIDATE_STEPS)),COVER_FROM_TEST=1 $(if $(filter guard-check,$(VALIDATE_STEPS)),GUARD_SKIP=1))

# The whole output, stderr too, also goes to VALIDATE_LOG as it runs (tee), so
# a worker can run it in the background and read that file instead of a
# shortened stdout; the first line says where: `validate: log <path>`. In a
# linked worktree (.git is a file) VALIDATE_LOG defaults to the worktree's own
# git dir (git rev-parse --git-path validate.log): one file per worktree, so
# another session's run can't overwrite it as a shared /tmp name can, and
# outside the work tree, so no go test cache sees it change (a file at the
# root that grows reruns internal/viewer, whose tests walk the repo) and no
# leak or catalog scan reads it. The main checkout has none; VALIDATE_LOG=
# turns it off, VALIDATE_LOG=<file> names another. When stdout already is that
# file (make validate > <log>) nothing is teed. The exit code and the last
# line stay the verdict either way (TestValidateWritesItsLogIntoTheWorktreesGitDir).
ifeq ($(origin VALIDATE_LOG),undefined)
VALIDATE_LOG = $(if $(wildcard .git/HEAD),,$(if $(wildcard .git),$(shell git rev-parse --path-format=absolute --git-path validate.log 2>/dev/null)))
endif

validate:
	@tmp=$$(mktemp -d "$${TMPDIR:-/tmp}/validate.XXXXXX") || exit 1; \
	trap '[ -n "$$tmp" ] && rm -rf "$$tmp"' EXIT; : > "$$tmp/red"; echo 1 > "$$tmp/rc"; \
	red() { echo "$$1" >> "$$tmp/red"; echo "validate: red so far: $$(paste -sd , "$$tmp/red" | sed 's/,/, /g')"; }; \
	took() { echo "validate: $$1 $$(( $$(date +%s) - $$2 ))s"; }; \
	step() { st=$$(date +%s); $(MAKE) --no-print-directory "$$1" $(VALIDATE_FLAGS); rc=$$?; took "$$1" "$$st"; return $$rc; }; \
	run() { \
	t0=$$(date +%s); \
	before=""; after=""; suite=""; ran=""; \
	for s in $(VALIDATE_STEPS); do \
	  ran=1; \
	  if [ -n "$$suite" ]; then after="$$after $$s"; elif [ "$$s" = test ]; then suite=1; else before="$$before $$s"; fi; \
	done; \
	if [ -z "$$ran" ]; then echo "validate: FAIL (no steps ran)"; return; fi; \
	if [ -z "$$suite" ]; then \
	  for s in $$before; do step "$$s" || red "$$s"; done; \
	else \
	  : > $(GUARD_COVER_PROFILE); \
	  ( for s in $$before; do \
	      st=$$(date +%s); \
	      if nice -n 10 $(MAKE) --no-print-directory "$$s" $(VALIDATE_FLAGS) > "$$tmp/$$s.log" 2>&1; then ok=1; else ok=""; fi; \
	      sed "s/^/[$$s] /" "$$tmp/$$s.log"; took "$$s" "$$st"; \
	      [ -n "$$ok" ] || red "$$s"; \
	    done ) & cheap=$$!; \
	  { step test; echo $$? > "$$tmp/test.rc"; } 2>&1 | tee "$$tmp/test-out.log"; \
	  if [ "$$(cat "$$tmp/test.rc")" != 0 ]; then red test; $(MAIN_FIX_HINT) "$$tmp/test-out.log" 2>/dev/null; fi; \
	  wait $$cheap; \
	  for s in $$after; do step "$$s" || red "$$s"; done; \
	fi; \
	took total "$$t0"; failed=""; \
	for s in $(VALIDATE_STEPS); do grep -qxF "$$s" "$$tmp/red" && failed="$${failed:+$$failed, }$$s"; done; \
	if [ -n "$$failed" ]; then echo "validate: FAIL ($$failed)"; return; fi; \
	echo 0 > "$$tmp/rc"; echo "validate: PASS"; \
	}; \
	log='$(VALIDATE_LOG)'; exec 3>&1; \
	inode() { ls -iL "$$1" 2>/dev/null | awk '{ print $$1 }'; }; \
	if [ -n "$$log" ] && { [ ! -e "$$log" ] || [ "$$(inode /dev/fd/3)" != "$$(inode "$$log")" ]; }; then \
	  { echo "validate: log $$log"; run; } 2>&1 | tee "$$log"; \
	else run; fi; \
	exit "$$(cat "$$tmp/rc")"

# The ported guided check scripts (internal/checks/testdata/ported) as
# machine runs: this checkout's issue-cli, the library from LIB= (default
# internal/checks/lib.sh). ONLY=<name> runs one of them.
ported-checks:
	@LIB='$(LIB)' ONLY='$(ONLY)' scripts/ported-checks.sh

# The public CI's steps (.github/workflows/ci.yml) in an ubuntu:24.04 container:
# apt tmux, shellcheck and curl, Go through GOTOOLCHAIN, a non-root user,
# PLAYTEST_CHROME=none, on this checkout's working tree. Refuses when mem-guard
# is blocked or docker is missing. Not part of validate (needs docker and takes
# minutes). Its last line is `linux-check: PASS` or `linux-check: FAIL (<step>)`.
# PKG=./internal/x RUN=TestY runs only `go test $(PKG) -run $(RUN) -count=1`
# there (either alone works; PKG="./a ./b" names several), and the run says it
# was a subset.
linux-check:
	@LINUX_CHECK_PKG='$(PKG)' LINUX_CHECK_RUN='$(RUN)' scripts/linux-check.sh

# The security barriers end to end (docs/API/security.md): a throwaway viewer
# on loopback attacked as if it were public, then govulncheck. Not part of
# validate (govulncheck needs the network); the same barriers are Go tests.
security-check:
	@scripts/security-check.sh
	@go run golang.org/x/vuln/cmd/govulncheck@latest ./...

# Playtest sessions (playtest/sessions/*.yaml): an issue's manual checks run
# against a throwaway scenario board in Chrome. See docs/patterns/playtest/.
#   make playtest-check                       validate every session (no browser)
#   make ux-test [ISSUE=slug] [S=session] [STEP=TP2,TP3] [REPEAT=2] [JOBS=3]
#                                             machine checks, headless, desktop + phone
#   make ux-test ALL=1                        every session, whatever the branch
#                                             (JOBS=n sessions side by side, default 3;
#                                             1 while mem-guard says BLOCKED)
#                                             (local CI's nightly slow tier)
#   make playtest [ISSUE=slug] [S=session]    the owner's judgement steps, in an overlay
#   make playtest-report ISSUE=slug [APPLY=1] tick passed boxes, comment failures
#   make shot URL=… [W=390] [H=844] [FULL=1] [SCROLL=sel] [OUT=file.png]
#             [BASE=ref] [HTML=1] [SCENARIO=name]
#                                             one screenshot of one page (headless);
#                                             URL=/p/… shoots the board make serve
#                                             runs, BASE= its before board too
#                                             (OUT and OUT.base), HTML=1 saves both DOMs
#   make serve SCENARIO=name [BASE=main] [PORT=n] [DIR=dir]
#                                             a scenario board left running, isolated
#                                             (own HOME/XDG, never the shared
#                                             viewer.json); BASE= a second board
#                                             built from that ref
#   make serve-stop [SCENARIO=name]           stop what make serve started
# In a dispatched worktree (branch work/<slug>) ISSUE defaults to <slug>, so a
# plain `make playtest` runs that issue's sessions. Pass ISSUE= to run them all.
# With S= the branch's slug isn't used: S= runs that session, whoever's it is.
# ALL=1 drops every selector (ISSUE= and S= included): all sessions run.
BRANCH_ISSUE := $(patsubst work/%,%,$(filter work/%,$(shell git rev-parse --abbrev-ref HEAD 2>/dev/null)))
ISSUE ?= $(if $(S),,$(BRANCH_ISSUE))
PLAYTEST = go run ./cmd/playtest
PLAYTEST_SEL = $(if $(ALL),,$(if $(ISSUE),-issue $(ISSUE)) $(if $(S),-s $(S)))

playtest-check:
	@$(PLAYTEST) check

ux-test:
	@$(PLAYTEST) ux-test $(PLAYTEST_SEL) $(if $(STEP),-step $(STEP)) $(if $(REPEAT),-repeat $(REPEAT)) $(if $(JOBS),-jobs $(JOBS))

playtest:
	@$(PLAYTEST) playtest $(PLAYTEST_SEL)

playtest-report:
	@$(PLAYTEST) report -issue $(or $(ISSUE),$(BRANCH_ISSUE)) $(if $(S),-s $(S)) $(if $(APPLY),-apply)

shot:
	@$(PLAYTEST) shot -url '$(URL)' $(if $(W),-w $(W)) $(if $(H),-h $(H)) $(if $(FULL),-full) $(if $(SCROLL),-scroll '$(SCROLL)') $(if $(OUT),-out $(OUT)) $(if $(BASE),-ref '$(BASE)') $(if $(HTML),-html) $(if $(SCENARIO),-scenario '$(SCENARIO)')

serve:
	@$(PLAYTEST) serve -scenario '$(SCENARIO)' $(if $(BASE),-ref '$(BASE)') $(if $(filter command line,$(origin PORT)),-port $(PORT)) $(if $(DIR),-dir '$(DIR)')

serve-stop:
	@$(PLAYTEST) stop $(if $(SCENARIO),-scenario '$(SCENARIO)')
