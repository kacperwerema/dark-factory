# Dark Factory (issue-viewer + issue-cli)

Dark Factory runs Claude Code and Codex agents through a process written down in `workflow.yaml`, one issue at a time. It ships two binaries: `issue-viewer`, the board (a Go web app over markdown issue files: the Workshop Board pages, list, board and docs views), and `issue-cli`, the CLI agents use to walk issues through the workflow. The repo and the Go module are `github.com/kacperwerema/dark-factory`. Config and state folders and env vars keep earlier names (`ai-workflow-engine`, `ISSUE_VIEWER_*`): see docs/getting-started.md#legacy-names, and don't rename them.

## Running

```bash
go build -o issue-viewer ./cmd/issue-viewer && ./issue-viewer -config ~/.config/ai-workflow-engine/projects.yaml
```

Flags (`-config`, `-dir`, `-docs`, `-host`, `-port`, `-owner-code`, `-record-viewer`, `-version`) and the host and owner sign-in rules: docs/getting-started.md and docs/API/security.md.

## Validation

`make validate` runs `make merge-check`, `gofmt -l` (fmt-check), `go vet ./...` (vet), `make catalog-check`, `make link-check`, `make leak-check`, `make codemap-check`, `make guard-check`, `make playtest-check`, the full test suite (test) and the `cmd/issue-cli` coverage gate (cover-cli). Use it before proposing a change that touches `cmd/issue-cli/`. Its verdict is its last `validate:` line, `validate: PASS` or `validate: FAIL (<step>, <step>)`: read that line, not a wrapper's exit code; `validate: red so far: <step>` means stop and fix that step. `VALIDATE_STEPS=` runs a subset. It has to pass on macOS and Linux (`make linux-check` for ci.yml, lib.sh, file ordering, tmux parsing or case-insensitive paths) and on a loaded machine.

What each step checks, and the rules every test follows (the test cache list, guard tests, tmux sockets, stand-in CLIs, timing checks through `timing.Check`, the live board, git in tests, `scripts/under-load.sh`): [docs/testing.md](docs/testing.md). Read it before writing or changing a test.

UI checks are playtest sessions: `make ux-test [ISSUE=<slug>] [S=<session>]`, `make shot URL=<page>`, `make serve SCENARIO=<name>` (instead of a hand-started sandbox viewer); after editing docs/patterns/playtest run `go generate ./cmd/issue-cli/...`. All of it: [docs/testing.md#ui-checks](docs/testing.md#ui-checks).

## Project Structure

- `cmd/issue-viewer/` — the board's thin main; the viewer itself is package viewer in `internal/viewer/` (templates/ and static/ embedded beside it)
- `cmd/issue-cli/` — the CLI agents walk issues with; bundled templates under `cmd/issue-cli/templates/`
- `internal/<pkg>/` — one package per concern (tracker, agentrun, briefing, memory, ci, …); `cmd/playtest/` the playtest runner, `playtest/` its sessions and scenarios, `scripts/` shell helpers

What each file and package holds is in [docs/code-structure.md](docs/code-structure.md). `issue-cli codemap` and `issue-cli affected <slug>` read packages, contexts and a change's footprint from the code.

## Issues and doc pages

Issues are markdown files with YAML frontmatter in `./issues/` or `./issues/<System>/`; only `title` is required, and `status` is one of the statuses in the project's `workflow.yaml`. Fields, custom fields and file names: [docs/issue-format.md](docs/issue-format.md). Change an issue only with issue-cli. Doc pages are markdown files in `docs/` with optional `title` and `order` frontmatter (docs/getting-started.md).

## Documentation

`docs/` is viewable at `/docs` in the web UI. Every page with what it covers: [docs/doc-map.md](docs/doc-map.md). The ones a worker reaches for most:

- [Workflow](docs/workflow.md) — lifecycle, clarify, local CI, fresh sessions, red rounds, rules, decisions, post-mortem
- [Agent Dispatch](docs/agent-dispatch.md) — starting work, leads, auto-start, the queue, routing
- [Validation and Test Rules](docs/testing.md) — make validate's steps, every rule a test follows
- [Code Structure](docs/code-structure.md) — what each file and package holds
- [CLI](docs/CLI/overview.md), [API](docs/API/overview.md), [Security](docs/API/security.md), [UI](docs/UI/overview.md), [Workshop Board](docs/UI/workshop-board.md), [Workflow engine](docs/Workflow/overview.md)

## Sessions

`sessions/` holds lead-session handoff notes, one file per handoff (`YYYY-MM-DD-lead-<project>.md`; docs/patterns/lead-session.md). When resuming lead work, read the newest file first; before a shutdown or a long break, write a new one.

## Adding New Statuses

Status colors are defined in the `statusColor` template function in `template_funcs.go` `funcMap`. Board column order and descriptions come from `workflow.yaml` statuses.
