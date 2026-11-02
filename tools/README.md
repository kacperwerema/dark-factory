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
