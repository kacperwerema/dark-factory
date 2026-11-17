# Tools

One-off helpers, not part of the binaries.

- `clilog-scrub/` — brings older issue-cli logs in line with v0.54: merges
  worktrees' `.agent-logs`, strips free text, backs up the originals.
  `go run ./tools/clilog-scrub -root <primary checkout> [-dry-run]`
  ([docs/CLI/telemetry.md](../docs/CLI/telemetry.md)).
- `mockup-shots/` — screenshots of design mockups and HTML pages with
  Python Playwright; design artboards render through `dc-shim.js`.
  `tools/mockup-shots/shots.py <folder>`
  ([docs/UI/overview.md](../docs/UI/overview.md)).
- `playtest-guides/` — copies docs/patterns/playtest/*.md into
  cmd/issue-cli/guides/playtest, the folder issue-cli embeds (internal/guidesync).
  Run by `go generate ./cmd/issue-cli/...`.
- `doc-numbers/` — writes docs/numbers.md from the constants (internal/docnumbers).
  Run by `go generate ./internal/docnumbers/...`.
- `test-shards/` — make test's runner: splits each named package's tests
  into N go test processes by recorded time (internal/testshard) beside one
  go test for the rest, and keeps a package's plan while its tests stay the
  same. `go run ./tools/test-shards -shard ./cmd/issue-cli=3 -shard ./internal/viewer=3 -- ./... -count=1`
  ([docs/code-structure.md](../docs/code-structure.md)).
