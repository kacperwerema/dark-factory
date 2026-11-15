.PHONY: build build-cli install hooks fmt-check merge-check leak-check codemap-check demo readme-shots clean validate test vet cover-cli catalog-check link-check playtest-check ux-test playtest playtest-report shot serve serve-stop security-check ported-checks linux-check

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
	rm -f issue-viewer issue-cli cover.out
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

test:
	@command -v shellcheck >/dev/null 2>&1 || echo "test: no shellcheck on PATH: TestLibPassesShellcheck skips, lib.sh goes unchecked (make linux-check runs it)"
	go test $(TEST_FIRST) ./... -count=1 $(if $(CI_RESULTS_DIR),-json | go run ./cmd/issue-cli ci gotest --out $(CI_RESULTS_DIR)/unit.json)

cover-cli:
	@go test -coverprofile=cover.out ./cmd/issue-cli/... -count=1 >/dev/null
	@pct=$$(go tool cover -func=cover.out | awk '/^total:/ { sub("%","",$$3); print $$3 }'); \
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
	@go test ./internal/privacy -run '^(TestTrackedFilesCarryNoLeaks|TestTrackedFilesNameUpstreamOnlyInLicense)$$' -count=1

# The engine's code map (workbench.contexts in workflow.yaml) maps every
# package and knows every upward import: the same test `test` runs, as a step
# of its own so a new internal/ package nobody mapped shows before the suite.
# Its message names the package and the workbench.contexts fix.
codemap-check:
	@go test ./internal/codemap -run '^TestEngineMapHasNoNewUpwardImport$$' -count=1

# The steps run one by one, the cheap ones first, and a red step doesn't stop
# the next, so one known red can't hide what the later steps would say. A red
# step prints `validate: red so far: <step>, <step>` at once: a worker watching
# the log can stop there instead of waiting for the suite. The last line on stdout is
# `validate: PASS` or `validate: FAIL (<step>, <step>)`, every red step named,
# so a run in the background with its output in a log can't hide a red step
# behind a wrapper's exit 0: read that line. An empty list is a FAIL, not a PASS:
# a subset is VALIDATE_STEPS="vet test".
VALIDATE_STEPS ?= merge-check fmt-check vet catalog-check link-check leak-check codemap-check playtest-check test cover-cli

validate:
	@red=""; ran=""; \
	for step in $(VALIDATE_STEPS); do \
	  ran=1; \
	  $(MAKE) --no-print-directory $$step || { red="$${red:+$$red, }$$step"; echo "validate: red so far: $$red"; }; \
	done; \
	if [ -z "$$ran" ]; then echo "validate: FAIL (no steps ran)"; exit 1; fi; \
	if [ -n "$$red" ]; then echo "validate: FAIL ($$red)"; exit 1; fi; \
	echo "validate: PASS"

# The ported guided check scripts (internal/checks/testdata/ported) as
# machine runs: this checkout's issue-cli, the library from LIB= (default
# internal/checks/lib.sh). ONLY=<name> runs one of them.
ported-checks:
	@LIB='$(LIB)' ONLY='$(ONLY)' scripts/ported-checks.sh

# The public CI's steps (.github/workflows/ci.yml) in an ubuntu:24.04 container:
# apt tmux and shellcheck, Go through GOTOOLCHAIN, a non-root user,
# PLAYTEST_CHROME=none, on this checkout's working tree. Refuses when mem-guard
# is blocked or docker is missing. Not part of validate (needs docker and takes
# minutes). Its last line is `linux-check: PASS` or `linux-check: FAIL (<step>)`.
# PKG=./internal/x RUN=TestY runs only `go test $(PKG) -run $(RUN) -count=1`
# there (either alone works), and the run says it was a subset.
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
#   make ux-test [ISSUE=slug] [S=session] [STEP=TP2,TP3] [REPEAT=2]
#                                             machine checks, headless, desktop + phone
#   make ux-test ALL=1                        every session, whatever the branch
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
	@$(PLAYTEST) ux-test $(PLAYTEST_SEL) $(if $(STEP),-step $(STEP)) $(if $(REPEAT),-repeat $(REPEAT))

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
