# Dark Factory (issue-viewer + issue-cli)

Dark Factory runs Claude Code and Codex agents through a process written down in `workflow.yaml`, one issue at a time. It ships two binaries: `issue-viewer`, the board (a Go web app over markdown issue files: the Workshop Board pages, list, board and docs views), and `issue-cli`, the CLI agents use to walk issues through the workflow. The repo and the Go module are `github.com/kacperwerema/dark-factory`. Config and state folders and env vars keep earlier names (`ai-workflow-engine`, `ISSUE_VIEWER_*`): see docs/getting-started.md#legacy-names, and don't rename them.

## Running

```bash
go build -o issue-viewer ./cmd/issue-viewer && ./issue-viewer -config ~/.config/ai-workflow-engine/projects.yaml
```

Flags:

- `-config` — the projects.yaml to serve (every project in it); without it the viewer serves one project from `-dir` and `-docs`
- `-dir` — issue markdown directory (default `./issues`), supports subdirectories
- `-docs` — documentation markdown directory (default `./docs`)
- `-version` — print the version and exit
- `-host` — interface to listen on (default `127.0.0.1`; anything else is an opt-in and warns at startup). Requests whose `Host` isn't loopback, `*.localhost`, the bind address or in `allowed_hosts:` (projects.yaml) get 421. Any public request (non-loopback Host, or proxy headers) gets 403 unless `identity:` is configured and its owner token verifies (docs/API/security.md)
- `-port` — HTTP port (default `8080`)
- `-owner-code` — where the owner sign-in code shows: `dialog` (macOS), `terminal`, `auto` (default: the dialog, else the terminal). Owner decisions (the O routes) need a browser signed in at `/owner`
- `-record-viewer` — write viewer.json even for a sandbox viewer (a temp-folder config, or another live viewer holds it); `=false` never writes it

## Validation

`make validate` runs `gofmt -l` (fmt-check), `go vet ./...`, the full test
suite, a coverage gate on `cmd/issue-cli` (floor: `CLI_COVERAGE_FLOOR`,
default 70), `make catalog-check`, then `make playtest-check`. Use it before proposing a change that touches
`cmd/issue-cli/`.

It has to pass on macOS as well as Linux. On a Mac, `t.TempDir()` is under
`/var`, a link to `/private/var`: a test that compares printed paths uses the
resolved dir (`realTempDir` in cmd/issue-cli). That dir is also too deep for
a tmux socket (~104 bytes), so `isolateTmux` keeps `TMUX_TMPDIR` under `/tmp`.
A test talks to its tmux server only with `-S <own socket>` (`testTmux`), and
kills it only that way (`TestTmuxCallsInTestsNameTheirSocket`). Every test
package's `TestMain` calls `internal/tmuxguard`, which drops `$TMUX` for the
whole binary (`TestEveryTestBinaryDropsTMUX`).
BSD `script` has no `-c` (internal/checks `ptyCommand`), and tests that
dispatch use `terminal: none`; an unset terminal is i3-msg where it is on
PATH and headless elsewhere (`agentrun.TerminalNone`).

It also has to pass on a loaded machine: CI runs while several workers run
`make validate`. A timing test keeps its absolute limit for a quiet machine
and, when the machine is slow, compares against a cheaper request timed in
the same run (`TestStatsPageFastOnABigProject`). A test waits for a file or
pane with a retry loop, never one fixed sleep. Code that dates two records
that must agree reads the clock once. A stand-in that writes a record a test
polls for writes it aside and renames it into place: sh line-buffers printf,
so a record written in place can be read half done. A test that runs git in a
TempDir turns off `maintenance.auto` and `gc.auto` (git detaches a
maintenance run after commit, merge and fetch that can still be writing
`.git/objects` at cleanup). A stand-in clock that ticks on every `now()`
must not be read by a poll loop whose tick count depends on speed. To check a test under
load, `scripts/under-load.sh [-c cpu] [-m mb] -- <command>` runs it beside
a CPU and memory hog it starts and stops itself (refuses when `mem-guard
check` is blocked, stops at 70%).

Shell: the worker briefing's `## Shell on this machine` section says what
applies on yours (zsh, macOS BSD tools, rtk), detected from `$SHELL`, the OS
and `rtk` on PATH; the same notes are in docs/getting-started.md#shell-notes.

UI checks run as playtest sessions:

- `make ux-test [ISSUE=<slug>] [S=<session>] [REPEAT=2]` runs them in headless
  Chrome against a throwaway scenario board. `S=` runs that session even when
  it belongs to another issue than the branch's; that run keeps its shots and
  goes to the project root's `.playtest/runs`, where its issue's Evidence tab
  reads it.
- `make ux-test ALL=1` runs every session, on any branch. Local CI runs it
  every night (`ci.nightly`: the slow tier after 03:00 when mem-guard allows;
  `issue-cli ci status --tier slow` names each red `<session>/<step>`).
  Console `no_errors` (the default) also catches CSP violations.
- `make shot URL=<page> [W=390] [FULL=1] [SCROLL=<sel>] [OUT=…]` takes one
  screenshot of one page (headless, waits for load, not network idle);
  FULL=1 on a page that only scrolls inside (the Workshop floor) unrolls its
  biggest scroller, SCROLL= names it. `URL=/p/…` shoots the board
  `make serve` runs; `BASE=main` shoots its before board too (OUT and
  OUT.base), `HTML=1` saves both pages' HTML for a diff.
- `make serve SCENARIO=<name> [BASE=main]` leaves an isolated scenario
  board running (and one built from BASE beside it), prints the URLs;
  `make serve-stop [SCENARIO=<name>]` stops them. Use it instead of a
  hand-started sandbox viewer.
- `make playtest ISSUE=<slug>` shows the owner the judgement steps in an
  overlay.
- `make playtest-report ISSUE=<slug> [APPLY=1]` ticks the boxes that passed.

See [docs/patterns/playtest/web-app.md](docs/patterns/playtest/web-app.md).

## Project Structure

```
cmd/issue-viewer/main.go    — the issue-viewer binary: sets viewer.Version from main.Version (-X main.Version), calls viewer.Run
internal/viewer/            — the viewer, package viewer: every file below, with templates/ and static/ embedded beside them (go:embed cannot reach ..)
  main.go                   — Run: run() (flags, listener), server timeouts, graceful shutdown on SIGINT/SIGTERM
  background.go             — goroutines that outlive a request (s.bg.Go), waited for and named at shutdown
  routes.go                 — Server struct, NewServer, Routes, dispatcher, project list
  owner.go                  — the owner credential on loopback: /owner sign-in (a one-time code shown in a macOS dialog or the viewer's terminal, -owner-code), the HttpOnly SameSite=Strict iv_owner session cookie kept in memory, fromOwner, and the owner gate on every O route (ownerOnlyRoute); docs/API/security.md "Owner sign-in"
  identity.go               — identityGate: a public request (non-loopback Host or any proxy header) needs projects.yaml identity: and a verified Cloudflare Access owner token, else 403 (fails closed); docs/API/security.md
  headers.go                — securityHeaders: nosniff, X-Frame-Options, Referrer-Policy, base CSP on every answer; request body caps
  csp.go                    — script CSP: per-request nonce (nonceWriter stamps the cspNonce marker), onCall for data-click & co (bound by static/events.js), home folder scrubbed from error bodies
  origin.go                 — hostAllowed (421 for a Host not loopback/allowed_hosts: DNS rebinding), sameOriginOnly (403 for cross-origin writes), -host / listen-address helpers
  nav.go                    — sidebar views (navViews) and the Nav the sidebar partial renders
  template_funcs.go         — funcMap, status/priority colors, link rewriters
  helpers.go                — projectRoot, fileExists, small utilities
  images.go                 — relative image src rewrite, /files/ image route
  tmux.go                   — agent session listing/matching/notification
  handlers_list.go          — list view, filters, /hash, /issues.json
  handlers_board.go         — classic board + graph views
  handlers_workboard.go     — /workboard: the sidebar Board, cards with markers, epic progress, agent footer
  radio.go                  — the company page Radio's comment lines: progress: and question: comments by author from the last week (radioComments)
  markers.go                — card markers (cardMarkers, blocked_on, needs-revision) and epic progress, shared by workboard and system pages
  agent_state.go            — worker state for the agents canvas and card footers (workerState, idle from the clilog's last call)
  handlers_canvas.go        — issues canvas (/canvas): version columns × epic or system rows
  handlers_detail.go        — detail page, transition preview, data-table render
  jobs_view.go              — recurring jobs in the viewer: Needs you's Due jobs rows and POST …/jobs/<dept>/<job>/run (▶ Start run), the department room's job lines, status.md's Jobs: line
  connections_view.go       — connections between departments: the company page's Connections block and data-box rooms, room feet ⇄ in · out, the map lot's ⇄ N, /p/<project>/connections and connections.json; lines drawn by static/connections.js
  spend_view.go             — the rooms' Spend block (wb/room-spend): tokens and estimated cost by week, budget "x of y", top issues, from internal/spend
  week_view.go              — a group's week report in the viewer: the group room's Week block (latest report's counts, or the --write command) and /p/<project>/reports/<group>/<week> (the file rendered; only a valid key under the work files)
  hq_report.go              — the HQ report: /p/<project>/hq/report (one folded headline per box in the company tree, detail below; components in templates/_workshop_hq_report.html, JS in static/hq-report.js) and /hq/report.md (issue-cli status --hq), from the company model, weekplan.Sections, the newest health snapshot, spend and the leads' progress: lines
  company_floor.go          — /company, the floor (L1): the fixed plan (floorPlan: tiles sized by counts only, wings, the station strip), server-routed roads, the rail (Waiting on you, Room | After YOLO, inspectors), the YOLO strip; classic at ?classic=1; components in templates/_workshop_floor.html, JS in static/company-floor.js
  handoffs_view.go          — a room's Handoffs block on the company page (Asked, Inbox, To forward) from internal/connections handoffs
  shelf.go                  — the outputs shelf: /p/<project>/shelf (filters, Approve / Reject POST …/shelf/review), …/shelf/file/ (sandboxed), the rooms' 📦 links, HQ's block, waiting rows
  shelf_read.go             — the output reader: /p/<project>/shelf/read/<id> (a markdown output as a document, contents, Day / Night, Changes since the last version), the owner's margin comments (POST …/shelf/comment, …/comment/resolve, fromOwnerBrowser) posted on the issue as revise:; components in templates/_workshop_shelf_read.html, JS in static/shelf-read.js
  hall_part.go              — the hall's drill-in: GET /hall/station/<status>, /hall/gate/<status>, /hall/ci (HallPartData, actionWords); components in templates/_workshop_hall_part.html
  hall_edit.go              — a station's Edit form: GET /hall/station/<status>/edit, POST …/edit/preview and …/edit/save over internal/wfedit; components in templates/_workshop_hall_edit.html, JS in static/hall-edit.js
  station.go                — L3, the station: GET /issue/<slug> (classic page at ?classic=1 or ?tab=); components in templates/_workshop_station.html, JS in static/station.js
  issue_tab.go              — the detail page's Issue tab model: brief, Needs-you band, path, what you're approving, folded body, side column
  evidence.go               — the detail page's Evidence tab model and its label: shots per linked playtest step, criteria → proof, verdict, playtest per step, watch (evidence_change.go: commits and files by area; stale_text.go: stale Open/pending lines matched to a D-n)
  timeline.go               — the detail page's Timeline tab model: where the time went (agent, waiting on you, idle, rework), life by status, filters
  timeline_sources.go       — what the Timeline tab reads: internal/timeline.Load plus comments, decisions, prompt, local CI; lists what it could not read
  handlers_issue_mutate.go  — update/create/delete/approve/upload, body editor
  handlers_data.go          — /data sidecar endpoints
  handlers_comments.go      — comment add/get/toggle/delete
  handlers_workflow.go      — retros, bug status, docs handlers
  handlers_dispatch.go      — agent dispatch + prompt builders
  handlers_github.go        — github page, fetch, import
  handlers_stats.go         — /stats: the dashboard over the tables (time tables, workflow token cost, CLI usage)
  health_view.go            — /health: derived score and agent opinion gauges, components, the review (file/dismiss/run), count-vs-runtime trends; the Status page's Health card
  lint_view.go              — /health's live Lint section (LintPanel) and the Status Health card's lint line
  dept_health_view.go       — the departments health family on the pages: /health#departments (DeptHealth), the rooms' "♥ N% on track" foot item, the Report Health card's Departments line, the Stats "Departments on track" trend
  bench_view.go             — the Report's Benchmark card (reportBench), its To unlock item, the Stats "Factory benchmark pass rate" series
  nightly_view.go           — the nightly loop in the viewer: the Report's Factory last night card (wb/nightly), status.md's Last night: line, the Needs you row for a pass rate under last week's median; nights from internal/bench nightly.go
  stats_view_wb.go          — /stats on the Workshop Board ("Hall stats"; classic at ?classic=1): StatsData as wb/* cards; components in templates/_workshop_stats.html, overview stats_overview_wb.html
  stats_dashboard.go        — the stats dashboard model: options, gauges, trends from snapshots, time bars, drill-downs; stats_storage.go the Disk use card
  stats_cache.go            — per-project cache of the aggregate and CLI usage report, warmed at start
  stats_overview.go         — /stats without a project: every project compared from its snapshots
  telemetry_view.go         — /telemetry: the owner's view of other installs' anonymous usage from a collector's batches.jsonl (telemetry_data: in projects.yaml); templates/telemetry_wb.html
  updates.go                — the Updates tab (/updates): workflow features to adopt, Decline / Re-open / Install with an agent, /updates across projects
  handlers_checks.go        — ▶ Run checks: POST …/check-script opens tmux window check-script-<slug> running the issue's guided check scripts
  handlers_playtest.go      — ▶ Run playtest: POST …/playtest opens tmux window playtest-<slug> running playtest_command (?report=1: playtest_report), GET its state and judged steps; JS in static/playtest-run.js
  handlers_agents.go        — the classic /agents canvas (?classic=1), agents.json, agent detail, open-in-tmux and lead spawn endpoints
  crew_all.go               — /crew, the Crew page across projects: every project's leads and workers with state (paused by mem-guard from agentrun.Snapshot), memory and load; the owner-only actions crew/lead|stop|nudge|tail and queue/pause|resume, each logged in $XDG_STATE_HOME/ai-workflow-engine/crew/actions.jsonl; components in templates/_workshop_crew_all.html, JS in static/crew-all.js
  crew.go                   — /agents, the Crew page (L1): the CEO, a column per department with its lead and workers, stopped jobs, ?agent= panel; components in templates/_workshop_crew.html, JS in static/crew.js
  handlers_lead_chat.go     — the lead chat's endpoints (lead.json, message box via agentrun.Paste, Esc and menu keys, Waiting on you) and /leads (Workshop Board cards)
  ceo_chat.go               — /lead, the CEO chat (L1 · HQ): the page and its polled ?part=live; ceo_pane.go reads the pane as turns, working line, menu and mode; ceo_images.go the box's images (…/lead/image, [Image: <path>] lines, turn thumbnails); ceo_switcher.go the switcher (?part=switcher: every project's leads with their state) and /leads' rows; components in templates/_workshop_ceo.html, JS in static/ceo-chat.js (phone: keyboard, dictation, camera, drafts)
  handlers_systems.go       — /systems index and system / family pages (model in systems.go, design-doc panel in docsections.go)
  system_wb.go              — /systems/<System>[/<Family>], the Workshop page (L2): one system's floor, issues, families, decisions, code map, doc, metrics; classic at ?classic=1; components in templates/_workshop_systems.html
  handlers_metrics.go       — /metrics view (guard metrics, knobs, branches), metrics.json; handlers_metrics_panels.go feeds the system-page partials
  handlers_status.go        — /status page (?classic=1) and /status.md (model in status.go: buildStatus, statusMarkdown)
  report.go                 — /status: the Report (L1 · Workshop Board look) built from the Status model; components in templates/_workshop_report.html, page templates/report.html
  handlers_pages.go         — project pages (workbench.status_page / pages): frame, /pages/<n>, sandboxed /page-raw/, /status.json
  ci_watch.go               — local CI watcher: polls each project's CI branch, starts detached `issue-cli ci run`
  health_watch.go           — daily project health snapshot: every 10 min, one per repository when none is from today; then shrinks shots of issues done 30+ days ago
  tracker_watch.go          — private tracker sync: every 10 min, commit, pull and push each project's tracker repo (privacy.work_files: tracker)
  privacy_view.go           — the privacy badges: Status title and Health card lines, projects list and /stats overview markers
  yolo_view.go              — YOLO mode in the viewer: the sidebar band and after-the-fact queue, yolo/on|extend|off, yolo.json, issue …/yolo-review, the watcher closing expired or valve-tripped windows
  yolo_review.go            — the review after YOLO: /p/<project>/yolo/review (the after-the-fact queue one entry per issue, oldest first, with evidence; POST …/yolo/review: one Agree for an issue's gates, Back to once per issue; owner boxes ticked or agreed one by one; reviewYOLOPass shared with …/yolo-review); components in templates/_workshop_yolo_review.html, JS in static/yolo-review.js
  queue_view.go             — the lead's queue in the viewer: the Report's Queue card (wb/queue: next 5, blocked, done today, the refill line, the verdict) and status.md's Queue: line; reads only
  autostart_view.go         — auto-start in the viewer: the Report's Auto-start card (words, verdict, last start), POST …/autostart/pause|resume|off, status.md's Auto-start: line
  release_wait.go           — Status "Waiting for release" card: milestone readiness, ## Unreleased entries, unpushed commits (one git call)
  release_candidate.go      — the card's release candidate (workbench.release_candidates): newest local <cut>-rc.N tag, commits since (on release/vX.Y once cut), soaked and picked checks, release.json for issue-cli release; on the engine the builds running, projects that ran it (usage logs), reports from it
  ci_view.go                — local CI on the pages: Status card (last runs boxes, Last red log), GET …/ci/log/<name> (kept red logs, plain text), waiting entry, ci-red markers
  send_back.go              — Send back for fixes: POST …/issue/<slug>/send-back moves the issue back on its path (tracker.BackTarget / MoveBack), rejected: comment, worker nudge; the Station's button and the YOLO review's Back
  clarify_view.go           — the clarify round in the viewer: the Station's Clarify card (one form: every ## Clarify question, options with cost, the recommendation first), POST …/issue/<slug>/clarify (owner's browser only; each answer an answer line and a D-n decision via tracker.AnswerQuestion), Needs you's Clarify row (clarifyWaiting: open answers, and assumed ones no YOLO review holds)
  push_view.go              — phone notifications: the push watcher (needsYouRows, stuck workers, usage limits → events), the sender binding (viewerstate.Claim + the bound config), /push and its owner-only POSTs, /manifest.webmanifest, /sw.js, /apple-touch-icon.png, status.md's Push: line; templates/push.html, static/push.js, static/sw.js, static/icons/
  needs_you.go              — the Needs you list: /p/<project>/needs-you, /needs-you across projects, needs-you.json, POST …/needs-you/approve (ready gates stamped together); needsYouRows is the list every waiting-on-you count is the length of
  decision_challenges_view.go — decision challenges in the viewer: one Needs you row per open challenge (challengeNeedsYou: the decision, why, the issues also on it, branches touching its paths:), POST …/decisions/challenge/resolve (owner-only Keep it); state from tracker.LoadOpenChallenges (internal/tracker/decision_challenge.go), issue-cli decision (cmd/issue-cli/cmd_decision.go)
  decisions_expiry.go       — expired hard decisions in the viewer: one Needs you row each (decisionExpiryNeedsYou over tracker.ExpiredHard, internal/tracker/decision_expiry.go), POST …/decisions/<slug>/<D-n>/renew and …/let-go (owner-only); decide --renew / --let-go in cmd/issue-cli/cmd_decide_expiry.go
  postmortem_view.go        — the post-mortem's one round: /p/<project>/postmortem/<slug> (the newest post-mortem's actions, Agree / Refuse, one Submit), POST …/postmortem/<slug>/decide (fromOwner; postmortem.Decide), the Needs you row; templates/postmortem.html, static/postmortem.js
  memory_view.go            — the agents' shared memory in the viewer: /p/<project>/memory (filters, history, Save / Pin / Delete from the owner's browser only, a delete scrubs the text), memory.json, the HQ room's 🧠 link; components in templates/_workshop_memory.html
  owner_checks.go           — the Owner checks page: /p/<project>/owner-checks?epic=<slug> lists every open owner box (proof OwnerWaiting) under an epic with its playtest shot; POST …/owner-checks/tick, the owner's browser only (fromOwnerBrowser); components in templates/_workshop_owner_checks.html, JS in static/owner-checks.js
  attention.go              — what waits on the owner (ownerAttention, waitingOnOwner): Status page, sidebar count, system pages, board markers
  templates/                — Go HTML templates (list.html, board.html, detail.html, docs.html); _sidebar.html on every project page
  static/style.css          — CSS of the classic pages: theme tokens (dark workbench, Dracula, light), shared classes, views
  static/workshop.css       — CSS of every Workshop Board page (wb/head loads it, never style.css): one theme, one block per page; docs/UI/workshop-board.md
  static/fonts/             — Archivo and JetBrains Mono woff2 (OFL)
internal/timeline/          — the Timeline's derivation (Load, Split: agent / waiting on you / idle / rework per status), the project Aggregate, the `workflow` health family
internal/health/            — project health snapshots: family registry (Register), .health/snapshots/ files, retention, Range, Backfill; families codebase, shape, tests, opinion, storage (MB on disk by area), lint (issue-cli health lint warnings); the derived score (Derive); the agent review (evidence pack, prompt, record, file/dismiss)
internal/testresults/       — test result parsers (go test -json, TRX, JUnit, playtest e2e) and their summary, for ci.run results:
internal/bench/             — the factory benchmark: the owner's benchmark file (~/.config/ai-workflow-engine/bench/), golden tasks from done issues (FindRange, hidden tests, snapshot) and from outputs the owner approved (output_task.go), candidates, evals (evals.go: a workflow file's evals:, the By workflow verdict, Estimate; scorer.go: promptfoo; affected.go: a branch's files mapped to the workflows they change, compare --branch), the sandboxed run (worker, budget, mem-guard, scoring, judge), usage from Claude Code results and transcripts, compare, the `bench` health family; issue-cli bench
internal/decisionstats/     — the decision ledger counted (Measure: in force by scope, hard / soft, provisional, open challenges, held and injected events in a window), the `decisions` health family; read by the Stats page's Decisions card (stats_decisions.go) and `issue-cli decisions log` (cmd/issue-cli/decisions_log.go)
internal/deptfamily/        — the `departments` health family: per department goals moving, outputs reviewed in time, jobs on time, open handoffs, on track (Measure); feeds the optional `departments` score component
internal/memory/            — shared memory between agents: event log folded into a Set (history, validity, pins, use), Store.Add with the update step (hash NOOP, near-duplicate SimilarError, updates / supersedes / new), Edit, Forget, Pin, OwnerDelete (scrubs), scopes and Viewer, FindSecret, the BM25 Ranker seam and the Hybrid ranker over a local Embedder (embed.go, hybrid.go: memory: embedder:, mem-guard before each embed, .memory/vectors/), SplitPins; entity.go (Entities, Mentions, Index: #topics and @names, who knows what); decay.go (Strength, the Decayed ranker), consolidate.go (the consolidation pack: clusters, stale, expired, promote); issue-cli memory (candidates and consolidate in cmd/issue-cli/cmd_memory_consolidate.go), start's == Memories ==, the briefing's ## Memory (internal/tracker/memory_prompt.go)
internal/critique/          — the fresh critic: the finding format (Parse: blocks/should/note with path:line, malformed kept, Coverage, Not read), the paths-only Brief, the run log <issues>/.critique/runs.jsonl (Record, Load, Latest, overrides), the `critique` gate (GateError), the read-only command (Argv, Exec); issue-cli critique (cmd/issue-cli/cmd_critique.go), the station's block (station_critique.go)
internal/postmortem/        — the post-mortem at close: the six sections, proposed actions (Differently:, Place: memory|file|skill|workflow|none, Text:, Under:) and Check (the parser's refusals), the paths-only Brief, Save / Of / WaitingIn under retros/ (Kind: postmortem), Decide (applies agreed actions, writes Applied:/Refused: back), Splice; issue-cli postmortem (cmd/issue-cli/cmd_postmortem.go), postmortem: in workflow.yaml (internal/tracker/postmortem_config.go)
internal/gitfile/           — write one file and commit only it on main (checks, restore on a failed commit): wfedit's Save and the post-mortem's file edits
internal/lint/              — issue-cli health lint: named checks over workflow.yaml and the board (undated, workflow, schema, template, placeholders, sections); every finding a warning
internal/autostart/         — auto-start: the owner's standing start per project (.autostart/<project>.json: TurnOn, Pause, Resume, TurnOff), the machine-wide start budget (WithBudget: lock + last start under $XDG_STATE_HOME), mem-guard's reading, the CPU load reading (load.go: ReadLoad, max_load, sysctl vm.loadavg or /proc/loadavg), Decide; the project half (AutostartCandidates, LiveWorkers, AutostartEvaluate) is in internal/agentrun/autostart.go
internal/redrounds/         — the red-round cap: per-check streaks from the stats sidecar's rounds: (written by transition through WorkflowConfig.CheckHold/CheckDone) and local CI runs naming the issue (Count, FromCI), the cap question (QuestionText, Questions, Since), the hold (State.Hold); issue-cli rounds (cmd/issue-cli/redrounds.go), the Station's red checks
internal/queue/             — the lead's queue: <issues>/.queue/queue.json (items: slug or idea, quote, blocked_on, open/started/done/dropped, done SHA), Update under a lock, Add/Move/Block/MarkDone, the fixed refill line; the verdict (QueueEvaluate over autostart.Decide, holds, CI red) in internal/agentrun/queue.go; issue-cli queue (cmd/issue-cli/cmd_queue.go), transition's done mark
internal/yolo/              — YOLO mode: the window in .yolo/<project>.json (TurnOn, Extend, TurnOff, Active with the expiry and CI valve), the closed-window log, passes, owner checks (yolo-owner-check: comments) and the review queue from comments (Entries: one per issue, the unit every count counts), the gates it passes
internal/connections/       — what passes between a project's boxes (HQ, Product, departments): requests (requested_by:, decisions cited across), outputs (linked or used), dependencies (depends_on / blocked_on across); Derive, Load, Select, CountsByBox; declared.go: projects.yaml connections: (Declared, Mark, CheckRequest, RelayPending); handoff.go: handoffs (PlanRoute, HandoffOf states, ListHandoffs); issue-cli connections, cmd/issue-cli/routes.go, cmd/issue-cli/cmd_handoff.go
internal/outputs/           — the departments' outputs shelf: events in <issues>/.outputs/outputs.jsonl (add, review, publish, use, revise) folded into outputs (Load), files copied to .outputs/files, versions; comments.go the owner's comment threads (.outputs/comments.jsonl, anchors, Locate); render.go the markdown render; diff.go LineDiff; the has_output gate (gate.go, the report type), counts; issue-cli output / outputs, shelf.go, the station, lint
internal/connectors/        — department connectors at run time: Find, Allowed (the department's lead and workers only), Env (secrets into the child only), RunCheck and <issues>/.connectors/checks.jsonl, List, the MCP servers an agent is launched with (Servers, MCPConfig, CodexFlags); config and secret refs are tracker.Connector (connector.go); connectors_view.go the room block, Check now, connectors.json
internal/depttemplate/      — department templates (cmd/issue-cli/templates/departments): List, Load, Plan / Apply for `department add` (files never overwritten), AddEntry / RemoveEntry splicing projects.yaml as text
internal/projectinit/       — issue-cli project init: plan (folders, workflow.yaml from a bundled template, the projects.yaml entry) and Apply, never overwriting a file; the entry goes into a config only when --config names one (depttemplate.AddProject splices it as text)
internal/jobs/              — recurring jobs: a department's jobs: (Parse, builtin.go: brief: builtin:<name> bundled in cmd/issue-cli/templates/jobs, schedule periods, Latest/Next/Missed), runs as issues (Runs, PlanRun, WriteRun), the run log <issues>/.jobs/runs.jsonl, the owner's standing start (.jobs/<project>.json), Evaluate (due / start / waits for the owner / hold); StartRun in internal/agentrun/jobs.go
internal/spend/             — what each box spent: Claude Code transcripts on this machine (bench.EachMessage) attributed by worktree and agent name, by week, prices.yaml estimates, roll-up into groups and HQ, budgets shown only (tracker.Budget); issue-cli spend, department show's Spend: line
internal/weekplan/          — a group's week: Gather (goals and their move over 7 days, outputs, jobs, handoffs, blocked and on whom, the run's ## Week plan), Markdown, the report under <issues>/.reports/<group>/<week>.md (Write, Latest, header counts); issue-cli department week, week_view.go
internal/goals/             — department and HQ goals (projects.yaml company:/departments:): values from the manual log (<issues>/.goals/values.jsonl) or a metric file, progress, the roll-up into HQ (Resolve); issue-cli goals / goal set / department show, the company page
internal/webpush/           — Web Push with the standard library: RFC 8291 encryption (Encrypt/Decrypt), VAPID ES256 (LoadOrCreate keys in push/, VAPID, VerifyVAPID), AllowedEndpoint (browser push services only), Send with no redirects and typed outcomes
internal/push/              — the owner's push state under the state dir's push/ (Store: one mutex + flock, files 0600): devices, settings (mute, quiet hours, subject, bound config), the events ledger (Observe by presence, Plan per device, seeding, retries, the quiet-hours digest, Record)
internal/buildversion/      — the running build's version for both binaries: the -ldflags main.Version stamp, else the module version from debug.ReadBuildInfo (go install …@vX.Y.Z), else "dev"
internal/milestone/         — the current milestone (Pick, Released), shared by the Status page and the `milestone` health family
internal/chart/             — server-rendered SVG charts (line, stacked bars, columns, gauge, sparkline) with table views
internal/ci/                — local CI runner: .ci/ state and run records, lock, persistent .worktrees/.ci, blame, watcher, one-liner
internal/privacy/           — private work files and code: the tracker repo (git folder outside the checkout: Init, Sync, sync record), releases (Setup, Cut, Pick, Publish, IsSnapshot), the badges (WorkBadge, CodeBadge), the pre-push guard (InstallHook, PrePush); upstream.go the upstream-name guard (only LICENSE.md may carry it)
internal/experiment/        — the experiment loop: the experiment block in an issue's sidecar (Load/Save), Judge (verdict over two bench run.json), Guard and Protected (paths a branch may not touch), Candidates from the retros' Workflow findings, AdoptedCommits / PrepareRevert; issue-cli experiment, the YOLO exception in cmd/issue-cli/yolo_gate.go
internal/retro/             — retros/ files: Load, Parse (header lines), Sections, Items, Terms; read by the Retro sorting room and experiment candidates
internal/proof/             — acceptance criteria → proof links: Collect (Proves: trailers, playtest proves:, prove links in the sidecar), each link's check (docs-only commits don't stale a test), the criteria_proven gate, ticking; owner checks (owner.go: judgement steps, guided-check steps left to the owner, "(owner)" boxes; never ticked from trailers, deferred under YOLO); ownerbox.go the one owner-box rule every tick path asks (OwnerBoxWhy, OwnerTicks: agents refused)
internal/playtestruns/      — playtest run folders: List, Archive at done (latest own runs move to <root>/.playtest/runs), Prune (`issue-cli playtest prune`), Shrink (old shots to small JPEGs)
internal/checks/            — guided check scripts: lib.sh (embedded; issue-cli check-script --lib), Find by `# issue:` header, owner run records, Due, WindowName, Skeleton; testdata/ported/ the first three scripts on the library
internal/wfedit/            — edit one station of a workflow file: Open, Apply (splice only the edited items, diff, checks, read-back guard), Save (one-file commit on main quoting the owner); the hall's Edit
internal/workflowupgrade/   — feature catalog, detection, features: record, upgrade plan (BuildPlan), catalog-check (CheckBranch); guard tests that every workflow.yaml key has an entry
cmd/issue-cli/templates/    — bundled workflow templates and the feature catalog (workflow-features/<id>.yaml), embedded as package templates
internal/agentrun/          — shared tmux/worktree launcher used by the server and issue-cli (leads, dispatch); route.go: RouteAgent (routing: rules + the pane scan for limit messages, ScanLimits); paused.go: Snapshot (one tmux list-panes and one ps), Paused (a process in state T under a pane: mem-guard paused it)
internal/routing/           — which agent CLI (claude, codex) starts each worker, lead or job run: Pick over a workflow's routing: rules (tracker.RoutingConfig) with fallback, FindLimit (each CLI's own usage-limit text and reset), the machine-wide resting state ($XDG_STATE_HOME/ai-workflow-engine/subscriptions: Mark, SetRest, Clear); issue-cli agents, the Crew page's Subscriptions, status.md's Agents: line
internal/telemetry/share/   — anonymous usage sharing (on by default, nothing sent without a collector URL): the daily batch over a closed vocabulary (Build, Validate, ValidateShape), the share setting and first-run notice, sent log, Sender, the collector's Handler and store, Summarize for /telemetry
cmd/telemetry-collector/    — self-hostable receiver for those batches (POST /v1/batch, loopback by default)
internal/clilog/            — the clilog: one record per issue-cli call (result, no free text), paths, Redact, Scrub (tools/clilog-scrub)
internal/tmuxguard/         — every test binary's TestMain: TMUX and TMUX_PANE dropped, TMUX_TMPDIR a private /tmp folder, its server killed by -S (TestEveryTestBinaryDropsTMUX)
internal/viewerstate/       — viewer.json: the running viewer's config/URL/pid, how issue-cli finds the config from any folder
internal/testids/           — every data-testid the templates carry; TestTemplateTestIDs keeps them in step
cmd/playtest/               — playtest runner: check | ux-test | playtest (overlay) | report (chromedp)
playtest/sessions/          — playtest sessions (step id = issue checkbox id)
playtest/scenarios/         — scenario fixtures (issues under tracker/, never issues/)
scripts/sync-issues.sh      — downloads issues from GitHub Project into ./issues/<System>/
```

## Issue File Format

Issues are markdown files with YAML frontmatter stored in `./issues/` or `./issues/<System>/` subdirectories.

```markdown
---
title: "Issue title"
status: "in progress"
system: "Combat"
version: "0.1"
labels:
  - bug
  - enhancement
priority: "high"
created: "2025-01-15"
number: 42
repo: "owner/repo"
---

Markdown body here. Supports `[x]` checkboxes.
```

### Required fields

- `title` — issue title

### Optional fields

- `status` — one of the statuses in the project's `workflow.yaml` (the bundled `development` template: `idea`, `in design`, `backlog`, `in progress`, `testing`, `human-testing`, `documentation`, `shipping`, `done`)
- `system` — categorization tag (also used as subdirectory name by sync script)
- `version` — version string, filterable on the board view
- `labels` — list of label strings
- `priority` — one of: `low`, `medium`, `high`, `critical`
- `created` — date string for sorting (newest first)
- `number` — GitHub issue number (used in board card display as `#number`)
- `repo` — GitHub repo in `owner/repo` format
- `parent` — slug of the epic this issue belongs to (`umbrella` is a legacy alias). Old `base`/`lead` fields on epics are plain custom fields; nothing reads or writes them
- `type` — type of work when `workflow.yaml` defines `types:` (picks the issue's path; see docs/Workflow/types.md)

### Custom fields

Any other frontmatter key is preserved as a custom field and displayed in the detail view sidebar. String values that start with `http://` or `https://` render as clickable links. List values render as bullet lists. Example:

```yaml
pr: "https://github.com/org/repo/pull/456"   # renders as a link
pr_author: "jsmith"                            # renders as text
participants:                                  # renders as a list
  - alice
  - bob
```

### Filename convention

- For GitHub issues: `<number>.md` (e.g., `42.md`)
- For draft issues: slugified title (e.g., `my-feature-idea.md`)
- The slug used in URLs (`/issue/<slug>`) comes from the title (with the system folder in front), not the filename; a `slug:` field pins it. `issue-cli update --title` keeps the slug by writing that pin

## Doc Page Format

Documentation pages are markdown files in the docs directory.

```markdown
---
title: "Page Title"
order: 1
---

Page content in markdown.
```

- `title` — defaults to titlecased filename if omitted
- `order` — numeric sort order (lower first), pages with same order sort alphabetically
- Frontmatter is optional; a plain markdown file works too

## Documentation

Detailed documentation lives in `docs/` and is viewable at `/docs` in the web UI:

- [Why a Workflow Harness](docs/why.md) — the argument behind the tool
- [Roadmap](docs/roadmap.md) — goals, epics and phase order (set 2026-10-22); the board's epics are the source of truth
- [Getting Started](docs/getting-started.md) — install, server flags, first issue, docs page format; phone notifications (`/push`: Web Push when something new waits on you, owner-only subscribe, mute, quiet hours with one digest; the installable app opening Needs you; VAPID keys and devices under the state dir's `push/`, only the viewer holding viewer.json sends)
- [Issue File Format](docs/issue-format.md) — frontmatter fields, custom fields, file organization
- [Workflow](docs/workflow.md) — lifecycle, prompts, the clarify round (`## Clarify`: every question at once with options, cost and the recommendation first; `issue-cli clarify`, the Station's form, assumed answers under YOLO), side-effects, system overlays; department workflows (a department's own file in projects.yaml `departments: workflow:`, the `content` template, `--department` flags)
- [Agent Workflow Flow](docs/agent-workflow-flow.md) — full dispatch-to-done agent flow
- [Board Configuration](docs/board-configuration.md) — columns, card fields
- [Agent Dispatch](docs/agent-dispatch.md) — a human starts the work (the disclaimer: `dispatch --quote`, no unattended runs, every paste into a session and what starts it); terminal config, `lead_terminal` (leads as tabs in your own session), approval notifications, the lead session, worktrees; `issue-cli dispatch` / `status`, the waiting-on-gate refusal, auto-start (`issue-cli autostart`, `dispatch --autostart`: the owner's standing start while memory is low and the load is under the core count, off by default), recurring jobs' standing start (`job auto on`, `job run --scheduled`), the lead's queue (`issue-cli queue`, `<issues>/.queue/queue.json`, `queue next --start` on the owner's words with auto-start's checks, done with the SHA at ship, the Report's Queue card), `viewer_url`; the CEO chat page (`/p/<project>/lead`, `/leads`); routing between Claude and Codex (`routing:` rules, the fallback while a subscription rests after a usage limit, `issue-cli agents`, lint check `routing`, no CCR)
- [GitHub Integration](docs/github-integration.md) — sync, issue reference, auto-close
- [Per-issue Data Store](docs/data-store.md) — sidecar JSON, `<!-- data -->` marker, `issue-cli data` commands
- [Workbench Files](docs/workbench-files.md) — metric runs, targets and knob exports the Metrics view reads, branch matching, hot files, `depends_on`; `workbench.systems` `paths:` (the Systems page code map, lint check `paths`); project pages (`status_page`, `pages`: sandboxed HTML, postMessage status); health snapshots (`.health/snapshots/`, `health:` config, `limits` and `score`, metric names, the tests and shape families, the derived score, the agent opinion and `issue-cli health review`, `issue-cli health backfill`, the lint family and `issue-cli health lint`, the departments family, adding a family)
- [Local CI](docs/workflow.md#local-ci) — opt-in `ci:` block: commands run on local main after every merge in `.worktrees/.ci`, results in `.ci/`, red reported to Status, markers, `ci:` comments and the lead; `results:` test results via `CI_RESULTS_DIR`; `issue-cli ci status|run|reset|watch|gotest`
- [Red-round cap](docs/workflow.md#red-round-cap) — `max_red_rounds:` (4 when unset): a gate's command, the proof check or a local CI command still red after that many fix rounds (one per branch head) becomes a `question: red rounds:` comment (a round counts only when the same failure comes back; an owner comment starts the count over), and transition refuses without re-running it until the owner resolves it; `issue-cli rounds <slug> [--cannot-fix <check> --why …]`, the Station's red checks
- [Decision ledger](docs/workflow.md#decision-ledger) — a D-n line's attribute group `{scope: …; hard; at: …; paths: …; expires: …}` (old lines read as scope issue, soft), `decide --scope/--hard/--at/--paths/--expires`, `decide --amend D-n` (only the group changes), owner acts on hard decisions (worker refused, the lead relays `--quote` as a `decision:` comment, the Decisions log's Hard), `decisions --in-force <slug>` (`tracker.InForce`) and its capped view "Decisions in force" in start, show, the worker and lead briefings and the critic's brief (`decisions: brief_cap:`, `decisions --lead`), the audit log `<issues>/.decisions/log.jsonl`; the design rules `decisions_cited` (hard decisions in force cited in `## Design`, or `n/a <ref>: <why>`) and `citations_current` (no replaced or expired ref cited), the diff audit `decisions_respected` (a branch touching an active hard decision's `paths:`, whatever its scope, cites it with no open challenge of its own; `decisions --touched <slug>`, the critic's brief lists them; internal/tracker/decision_respected.go), `since <date>`, YOLO holds as `yolo-hold:` comments (internal/tracker/decision_cited.go)
- [Private work and code](docs/workflow.md#private-work-and-code) — opt-in `privacy:` block, the owner's choice: work files in a private tracker repo (git folder outside the checkout, `issue-cli tracker init|sync`, the 10-minute watcher); private development with one public snapshot commit per release (`code: snapshots`, `issue-cli release setup|publish`, the first switch from a mirror); the pre-push guard, the Work and Code badges, `issue-cli privacy check`
- [Releases](docs/roadmap.md#cutting-a-release-candidate-soak-publish) — cutting a release on its branch: `issue-cli release cut` (release/v0.N and its `v0.N.0-rc.K` tags, `make install` from the release worktree stamps the build with `git describe`), the soak on this machine's projects with fixes picked from main (`release pick`), publish only on the owner's ok (`release publish`, or push main without `code: snapshots`)
- [Departments: Role, Goals, Outputs and Jobs](docs/departments.md) — `company:` and `departments:` in projects.yaml: mission, role prompt file, goals (manual via `issue-cli goal set` or `file:<path>#<metric id>`), the roll-up into HQ, the company page, `issue-cli goals` / `department show`, lint check `departments`; the outputs shelf (`issue-cli output add|review|publish`, `outputs`, `/p/<project>/shelf`, the owner's review per output); department leads (`issue-cli lead spawn --department`, `lead-<project>-<dept>` under the CEO, `## Your department` in the worker briefing, `/lead?dept=`); groups (`parent:` on a department, the group's lead as the lead of leads, goals rolling up through the group, `department add gtm` / `--parent`, the group room on the company page); the HQ report (`/p/<project>/hq/report`, `issue-cli status --hq`: one headline per department, then its detail); connections between departments (requests, outputs, dependencies derived from issues and the shelf; `requested_by:`, `issue-cli output use`, `issue-cli connections`, the company page's lines, `/p/<project>/connections`; declared `connections: requests:` / `feeds:` with routing refusals, via the CEO or a group lead, and the declared / not declared / no traffic marks); the week plan and report (`issue-cli department week <group> [--write]`, the GTM template's `planning` workflow and `weekly-plan` job, `<issues>/.reports/`, the group room's Week block); spend and budgets (tokens and estimated cost by week per room from this machine's Claude Code transcripts, `prices.yaml`, `budget:` shown as "x of y" and never enforced, `issue-cli spend`); department templates (`issue-cli department list|add|remove`, bundled in cmd/issue-cli/templates/departments, never overwriting a file, the Updates page's Departments you can add); channels as types (the `marketing` workflow: `piece`, `seo-pass` with its `measure` step, PPC / social / cold outreach as commented slots, a job's `type:`); the brand foundation in research mode (`foundation`: research → brand → icp → owner review → publish, writing `departments/<key>/brand.md` and `icp.md`; `market-size` into `market.md`; channel briefs gated on brand.md and icp.md by `file_exists`, lint's missing-file warning, the `brand-foundation` catalog entry); recurring jobs (`jobs:` per department, each run an issue, `issue-cli jobs` / `job due|run|auto`, the owner's standing start per job, Needs you's Due jobs, `<issues>/.jobs/runs.jsonl`); turning on a department (the owner's go-live steps, Marketing as the example, rehearsed by the pilot's guided check on its issue branch)
- [Post-mortem](docs/workflow.md#post-mortem) — `postmortem:` (at, start auto|ask, agent): a fresh agent at close reads the run from paths only and writes a retro (Steps, Worked, Did not work, Decisions, Known, Proposed actions); each action passes the "act differently" test and names one place (memory, file, skill, workflow experiment, none); `issue-cli postmortem start|brief|write|show|list`; the owner's one round on `/p/<project>/postmortem/<slug>` applies them, linked from the retro
- [Shared Memory](docs/memory.md) — facts agents learned, with kind, scope and source: `issue-cli memory add|search|list|show|edit|forget|pin|unpin`, the update step (`--updates`, `--supersedes`, `--new`), scopes project / department / agent / machine (machine refused from a private project), history in `<issues>/.memory/events.jsonl`, pins in the briefing, `== Memories ==` at start, secrets refused, the owner's `/p/<project>/memory` page; semantic search with a local embedder (`memory: embedder:`, loopback or a command, mem-guard gated, `scripts/memory-embed-macos.swift`, lint `memory`); `memory candidates <slug>` from an issue's trail, `memory consolidate` and the `memory-consolidate` job (`brief: builtin:memory-consolidate`), decay ranking; entities (`#topic`, `@agent` in text or tags, `memory search @x`, `memory entities`, the page by entity); the measured footprint; why not mem0
- [Factory Benchmark](docs/benchmark.md) — golden tasks (done issues with a known good result) solved again in a sandbox from their base commit: the owner's benchmark file and holdout, `issue-cli bench candidates|task add|run|report|compare`, the budget and mem-guard, scoring (hidden tests, check, judge), the run record (the experiment loop's interface), the Report's Benchmark card and the Stats trend; every night (`builtin:nightly-improve` on the Product entry's jobs, `issue-cli bench nightly`: main's baseline, one experiment the owner queued with `experiment queue`, never adopted, held at 50% memory or a load over the cores; `nights.jsonl`, the morning report, the Factory last night card); evals per workflow (`evals:` in a workflow file, scored by tests or by promptfoo on the run's shelf output, lint `evals`, output tasks from approved outputs, the By workflow verdict with time and cost, what a sandbox isolates, the run estimate)
- [Workflow Stats](docs/workflow-stats.md) — the experiment loop (`issue-cli experiment candidates|init|run|record|rollback`: retros propose, the benchmark measures, the owner adopts even in YOLO; protected paths); the Stats page (`/p/<project>/stats`: gauges, trends from health snapshots, where the time goes, options, drill-downs; `/stats` across projects), per-issue stats sidecar, token-cost estimation; the issue page's Timeline tab (`?tab=timeline`), its time rules and the project aggregate; connectors (`connectors:` per department, secrets as `env:` / `keychain:` references, `issue-cli connector exec|check`, `connectors`, MCP at launch, the room's list and Check now)
- [The Lead Session](docs/patterns/lead-session.md) — the project lead, one per project (`issue-cli lead`, `approve`), routing, authority, handoff, epics, agents canvas
- [Guided Check Scripts](docs/patterns/guided-check-scripts.md) — manual checks run from a shell as `scripts/checks/<name>.sh` on the issue branch: `issue-cli check-script <slug>` (`--yes`, `--new`, `--lib`), the helper library (steps and boxes, sandboxes, private tmux, stand-ins), ▶ Run checks (tmux window `check-script-<slug>`), the `guided-check-scripts` feature
- [Playtest Sessions](docs/patterns/playtest/README.md) — an issue's manual checks as YAML sessions: shared contract, ▶ Run playtest from the page (`playtest_command:` / `playtest_report:`), [web app example](docs/patterns/playtest/web-app.md) (this repo, chromedp), [game example](docs/patterns/playtest/game.md); `issue-cli process playtest`; `playtest: owner|agents|off` (who runs them, `wf.PlaytestForOwner()`)

Per-system docs:

- [API](docs/API/overview.md) — endpoints, handlers, server logic
- [Security](docs/API/security.md) — threat model, barriers (identity gate, Host, Origin, headers, handler checks), route inventory with who may call each, the go-public checklist, `make security-check` (scripts/security-check.sh + govulncheck), known gaps
- [CLI](docs/CLI/overview.md) — issue-cli commands, output contracts
- [CLI Usage Telemetry](docs/CLI/telemetry.md) — local names-only usage log, `issue-cli telemetry report`, opt-out, `parseFlags` rule for new commands; sharing usage (on by default, first-run notice, `telemetry share|send|sent`, exactly what is sent, `cmd/telemetry-collector`, `/telemetry` and `telemetry_data:`)
- [UI](docs/UI/overview.md) — templates, views, client-side behavior; the issue page tabs (Issue, Timeline, Evidence)
- [Workshop Board](docs/UI/workshop-board.md) — rules for the new look, where components and styles live, wb-* class names (`TestWorkshopCSSClassesDefinedOnce`), moving a page behind `?classic=1`, the `wb/*` component catalog (`TestWorkshopCatalogMatchesDefines`)
- [Workflow](docs/Workflow/overview.md) — workflow engine, transitions, overlays; template stamp, feature catalog, upgrade, keeping the catalog complete (`sources:`, `workflow catalog-check`)
- [Proof Links](docs/Workflow/proof.md) — each acceptance criterion linked to its proof: `Proves:` trailers, playtest `proves:`, `issue-cli prove`; the checks, the `criteria_checkboxes` / `criteria_proven` gates, ticking; the Evidence tab reads the same report
- [Types of Work](docs/Workflow/types.md) — per-type paths (feature/tweak/bugfix/epic/docs) as overlays with `path:`, `set-type`, lint; named N/A forms for boxes that don't fit (`check --na`, lint `na-boxes`); the `report` type (research and report-only: the report on the outputs shelf, `has_output`, lint `reports`, no commit)
- [The Fresh Critic](docs/Workflow/critique.md) — `issue-cli critique <slug>`: a read-only agent that did not write the work, given only paths, reports blocks/should/note findings with path:line; the `critique` rule holds a gate on a blocks finding until a new run clears it or the owner overrides (`--override --quote`); `critique:` block (agent, command, timeout), runs in `<issues>/.critique/runs.jsonl`, the station's Critique block, lint check `critique`
- [YOLO Mode](docs/Workflow/yolo.md) — for a window the owner chose, gates pass without them and wait for review after the fact: `issue-cli yolo on|extend|off|status|queue|review`, `.yolo/`, the CI and release valves, the `approval: "YOLO"` marker, Agree / Back to

## Sessions

`sessions/` holds lead-session handoff notes, one file per handoff (`YYYY-MM-DD-lead-session.md`). Each note records running agents, issue and branch state, owner decisions and rules, and how to resume after a restart. When resuming lead work, read the newest file first. Before a shutdown or a long break, write a new one.

## Adding New Statuses

Status colors are defined in the `statusColor` template function in `template_funcs.go` `funcMap`. Board column order and descriptions come from `workflow.yaml` statuses.
