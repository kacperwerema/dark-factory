---
title: "Workshop Board"
order: 2
---

The Workshop Board is the viewer's new look: Map › Company › Hall › Station,
drawn as objects from a board game (thick outline, hard shadow, no gradients).
This page is what a worker needs before building or moving a Workshop Board
page: the rules, where the code and styles live, how to name a CSS class, what
to check when a page moves behind `?classic=1`, and every `wb/*` component.

## Rules

- **Pages are built from `wb/*` components.** A new element is a new
  component in a `templates/_workshop*.html` file first, listed in that file's
  header comment and in the [catalog](#component-catalog) below. Look there
  before writing one: most things (chips, cards, empty states, folds, data
  tables, seg links) already exist.
- **English only.** The owner's own quotes stay as they wrote them.
- **Honest empty states.** When there is nothing to show, say what is missing
  and how to get it (`wb/empty`); never draw a fake or sample row.
- **No sidebar.** Workshop Board pages carry `wb/header` and its breadcrumb,
  not `_sidebar.html`. Workshop pages load `static/workshop.css` only, never
  `static/style.css`.
- **Orange waits on the owner.** If something is orange, it waits on you;
  nothing else is orange. Yellow-black stripes mean YOLO only.
- **Classic pages.** A page that replaced a classic one keeps the old page at
  `?classic=1` until the Workshop page does everything the old one did; then
  the classic page goes. A page born on the board has no `?classic=1`.
  What is still classic-only is listed, page by page, in the gap table of
  `ui/classic-page-gaps-move-what-still-lives-only-on-classic1-pages-into-the-workshop-board`
  (swept 2026-10-31): each row moved, sent to a child issue, or dropped
  with a decision. Before retiring a classic page, check its rows there;
  before adding to a classic page, add the Workshop half too.
- **Thumb-sized at phone width.** At 640px and below every button, link and
  key the owner acts with is at least 44px tall (square keys 44px wide). The
  rule lives in one block at the end of `static/workshop.css` ("phone width:
  tap targets"): add a new button class there, and give the page's phone
  session a `size: { target: …, min_height: 44 }` check (`phone-*.yaml`).
- **Visual changes are the owner's call.** A worker shows a screenshot
  (`make shot URL=…`) and leaves an owner box; it never ticks it.

## Where things live

| What | Where |
|---|---|
| Components | `templates/_workshop.html` (shared) and one `templates/_workshop_<page>.html` per page; the header comment of each lists its components |
| Pages | `templates/<page>_wb.html`, `report.html`, `station.html`, `crew.html`… compose the components |
| View models | `workshop.go` (shared), `<page>_wb.go` or the page's Go file (named in the template's header) |
| Styles | `static/workshop.css`, one block per page under a `/* ---- <name> ---- */` banner ("L1: the report", "L2: hall health", "L3: the station") |
| Behaviour | `static/workshop.js` (shared), `static/<page>.js` |
| Test ids | `internal/testids/<page>.go`, one file per page; `TestTemplateTestIDs` keeps them in step with the templates |

`static/style.css` also has a few `wb-*` classes (`wb-chip`, `wb-empty`,
`wb-panel`…): those belong to the classic workbench pages, which never load
`workshop.css`. Don't touch them for a Workshop page.

## CSS class names

`static/workshop.css` is one file loaded by every Workshop page, so a class
defined twice is a clash: the later rule wins on every page, not only the one
that wrote it. That is how the docs page title was drawn at 12.5px with an
ellipsis: the system page had defined its own `.wb-doc-title` further down.

- Search `workshop.css` for a name before you use it. Generic names
  (`wb-ccard`, `wb-floor`, `wb-shelf`, `wb-h2`) are taken.
- A page's own classes carry the page in the name: `wb-sysdoc-title` on the
  system page, `upd-*` on Updates, `wb-crew-*` on Crew.
- A class shared by several pages (`wb-h2`, `wb-small`, `wb-cmd`,
  `wb-app--page`, `wb-h1--xl`) has one rule. Use it as it is, or add a
  modifier (`wb-h2--big`). Don't write a second rule for it.
- A class's rule sits in one banner block. A grouped selector
  (`.wb-a, .wb-b { … }`) may repeat a class only in the block that defines
  it. Rules inside `@media` are free to restate it.

`TestWorkshopCSSClassesDefinedOnce` (workshop_guide_test.go) fails when a
`wb-*` class has two plain rules, or rules in two banner blocks, and names
both lines.

## Moving a page behind ?classic=1

When a Workshop page takes over a route, every test and playtest session that
opens the route for the old page breaks, on your branch and on every branch
that merges after you. Before you open the issue for review:

1. Grep for the route in Go tests, sessions and the runner:
   `/usr/bin/grep -rn --include='*_test.go' '<route>' .`,
   `/usr/bin/grep -rln '<route>' playtest/sessions cmd/playtest`.
2. For each hit, decide: does it check the old page (point it at
   `<route>?classic=1`) or the thing the new page now does (port it to the
   new page's test ids)?
3. Port the old page's sessions with the page, or retire them in the same
   issue. Don't leave them for the next worker.
4. Links elsewhere that should open the old page (sidebar, board cards,
   classic tabs) get `?classic=1` too.
5. Run `make validate` and `make ux-test S=<session> ISSUE=<slug>` for every
   session you touched. `S=` alone filters by the branch's issue and says "no
   session matches".
6. Then run every session once: `make ux-test ISSUE=` (an empty `ISSUE=`
   runs them all; `mem-guard check` first, one run at a time). The grep in
   step 1 misses sessions that reach the page by a link: workboard-markers
   TP10 clicked "more in the list" and landed on the moved list page, and
   updates TP8 and workflow-time TP4 stayed red on main after their pages
   moved because only the touched sessions ran. A red step is either a
   stale session (port it to the new ids, keeping every check it had) or a
   page that lost something (fix the page); say which in the commit.

## Component catalog

Every `{{define "wb/…"}}` in `templates/_workshop*.html`, by file.
`TestWorkshopCatalogMatchesDefines` (workshop_guide_test.go) fails when a
component is missing here, listed under the wrong file, or listed but no
longer defined. Add the row when you add the component.

### templates/_workshop.html

| Component | What it draws | Data |
|---|---|---|
| `wb/head` | &lt;head&gt; links: fonts, styles | `page title (string)` |
| `wb/header` | brand, Map › Company › Hall, counters | `WbHeader` |
| `wb/crumbs` | the level breadcrumb | `[]WbCrumb` |
| `wb/robot` | one agent | `WbRobot` |
| `wb/robots` | a row of agents, "+N" past wbCrewMax | `WbCrew` |
| `wb/chip` | a pill | `WbChip` |
| `wb/chips` | a row of pills | `[]WbChip` |
| `wb/ceo` | the CEO robot with its latest line | `WbCEO` |
| `wb/progress` | a labelled bar | `*WbProgress` |
| `wb/bang` | the orange "!" with a count | `int` |
| `wb/skyline` | departments as buildings, plus a free lot | `[]WbBuilding` |
| `wb/bld-group` | a group's building, its rooms (and nested groups) inside | `WbBuilding` |
| `wb/lot` | a project on the map (L0) | `MapLot` |
| `wb/site` | a project not kicked off yet (L0) | `MapLot` |
| `wb/plan-card` | a blueprint card: something still to build | `WbPlanCard` |
| `wb/queue-card` | one thing waiting on the owner | `WbQueueCard` |
| `wb/side` | the right panel: Waiting on you + Radio | `[]WbQueueCard` |
| `wb/goal` | a goal in the header or a room | `WbGoal` |
| `wb/room-goals` | a room's goals as goal chips | `[]WbGoal` |
| `wb/mission` | a room's mission line | `string` |
| `wb/shelf-link` | a room's way to its outputs shelf: in review, made this week | `WbShelfLink` |
| `wb/connectors` | a room's connectors (outside tools) with Check now | `*WbConnectors` |
| `wb/shelf-item` | one output on a shelf list | `WbShelfRow` |
| `wb/room-jobs` | a room's recurring jobs, last and next run | `[]WbJobLine` |
| `wb/room-spend` | a room's spend: tokens and estimated cost by week, its budget as "x of y", top issues (spend_view.go) | `*WbSpend` |
| `wb/week-report` | a group's latest week report: week, counts, plan or not, its file; or the command that writes one | `*WbWeekReport` |
| `wb/room-handoffs` | a room's handoffs: To forward, Asked, Inbox, each with its state chip (handoffs_view.go) | `*WbHandoffs` |
| `wb/handoff-list` | one list of a room's handoffs, at most 3 rows and +N more | `handoffListView` (`handoffList` func) |
| `wb/hq-shelf` | the shelf in HQ: in review across departments, the newest | `*WbHQShelf` |
| `wb/shelf-wait` | a department's outputs waiting for review, a Waiting on you row | `WbShelfWait` |
| `wb/room-head` | a department room's roof | `WbRoomHead` |
| `wb/room-foot` | a room's footer line | `[]WbFootItem` |
| `wb/ceo-desk` | the CEO at its desk, latest line, talk | `WbCEODesk` |
| `wb/dept-lead` | a department's lead at its desk: Talk to it or spawn it | `WbDeptLead` |
| `wb/room-hq` | HQ: the CEO's room (L1) | `WbRoomHQ` |
| `wb/stations` | the factory line from workflow.yaml | `[]WbStation` |
| `wb/room-dept` | a department projects.yaml adds (L1) | `WbRoomDept` |
| `wb/room-group` | a group: its lead and goals, its departments' rooms inside (L1) | `WbRoomDept with Children` |
| `wb/room-factory` | Product, the software factory (L1) | `WbRoomFactory` |
| `wb/new-tile` | a dashed "+" tile still to build | `WbNewTile` |
| `wb/new-dept-tile` | the "+" tile that adds a department from a template | `WbNewDeptTile` |
| `wb/queue-row` | a compact "waiting on you" row | `WbQueueCard` |
| `wb/radio` | what the CEO and agents said | `[]WbMessage` |
| `wb/message-box` | the box to message the CEO | `WbMessageBox` |
| `wb/crate` | an issue on the line, with its worker | `WbCrate` |
| `wb/gate-card` | the owner's stamp between stations (L2) | `WbGateCard` |
| `wb/stamp-icon` | the stamp drawing | `none` |
| `wb/hall-station` | a station with its crates (L2) | `WbHallStation` |
| `wb/hall-stop` | a station or a gate | `WbHallStop` |
| `wb/done-station` | the end of the line | `WbDoneStation` |
| `wb/ci-scanner` | local CI on the line | `WbCIScanner` |
| `wb/parking` | parked issues and why | `WbParking` |
| `wb/agent-panel` | the selected issue and its worker | `WbAgentPanel` |
| `wb/numbers` | a titled list of figures | `WbNumbers` |
| `wb/conn-key` | the line swatch of one connection kind | `string` |
| `wb/connections` | the company page's Connections block (its `Roads` feed the floor's roads) | `*WbConnections` |
| `wb/parcel-key` | the legend's parcel of one kind of road traffic (request, handoff, output, dependency) | `string` |
| `wb/conn-item` | one request, output or dependency on /connections | `WbConnItem` |

### templates/_workshop_ceo.html

| Component | What it draws | Data |
|---|---|---|
| `wb/ceo-title` | the CEO, live, mode, view toggle, Open in tmux | `CEOTitle` |
| `wb/ceo-conv` | the conversation (turns, working line) | `CEOLive` |
| `wb/turn-you` | the owner's message | `CEOTurn` |
| `wb/turn-ceo` | the CEO's paragraphs and folded tools | `CEOTurn` |
| `wb/ceo-working` | the CEO at work, with Interrupt | `*CEOWorking` |
| `wb/ceo-terminal` | the raw pane | `CEOLive` |
| `wb/choice-card` | a menu the CEO waits on | `*CEOPrompt` |
| `wb/ask-history` | an AskUserQuestion in the conversation | `*CEOQuestion` |
| `wb/ask-card` | the question widget on the pane, to answer | `CEOLive (.Pane.Ask)` |
| `wb/composer` | the message box | `CEOComposer` |
| `wb/session-controls` | the session's keys | `CEOChatPage` |
| `wb/ceo-side` | the right panel | `CEOLive` |
| `wb/waiting-band` | Waiting on you | `[]LeadWaiting` |
| `wb/ceo-questions` | the CEO's open questions | `[]CEOOpenQuestion` |
| `wb/ceo-workers` | the project's live workers | `[]CEOWorker` |
| `wb/ceo-live` | the polled parts, one &lt;template&gt; each | `CEOLive` |
| `wb/ceo-prompts` | the question or menu the session waits on | `CEOLive` |
| `wb/ceo-grid` | every CEO card on /leads, or the empty state | `[]LeadRow` |
| `wb/ceo-card` | one project's CEO on /leads | `LeadRow` |
| `wb/leads-dept` | one department lead on a /leads card | `LeadDeptRow` |

### templates/_workshop_crew.html

| Component | What it draws | Data |
|---|---|---|
| `wb/crew-card` | one agent: robot, name, state, issue | `WbCrewCard` |
| `wb/crew-column` | a department: its lead and its workers | `WbCrewColumn` |
| `wb/crew-row` | a titled row of cards (stopped, others) | `WbCrewRow` |
| `wb/crew-panel` | the selected agent: reach it, its issue | `*WbCrewPanel` |

### templates/_workshop_decisions.html

| Component | What it draws | Data |
|---|---|---|
| `wb/stat-big` | a big number with its label | `WbStat` |
| `wb/summary-box` | a one-line verdict in a box | `WbSummaryBox` |
| `wb/filter-pill` | a filter as a link | `WbFilterPill` |
| `wb/search-bar` | a GET search box with filter pills | `WbSearchBar` |
| `wb/day-label` | a day across the log | `string` |
| `wb/you-quote` | the owner's words, with the YOU face | `string` |
| `wb/decision-alone` | "no words from you" / provisional: Confirm (records your words) / Question it (to the CEO) | `WbDecisionRow` |
| `wb/decision-row` | one D-n line | `WbDecisionRow` |
| `wb/decision-thread` | one issue's decisions on a day | `WbDecisionThread` |
| `wb/bars` | a titled list of bars; rows in `More` fold under "+N more" | `WbBars` |
| `wb/count-bar` | one bar of `wb/bars` (label, track, count) | `WbCountBar` |
| `wb/command-card` | a titled note with the command to run | `WbCommandCard` |

### templates/_workshop_dept_new.html

| Component | What it draws | Data |
|---|---|---|
| `wb/dept-new-files` | the files adding a department writes or keeps | `DeptNewPlan` |
| `wb/dept-new-plan` | the preview of adding a department, with its groups' children | `DeptNewData` |
| `wb/dept-new-refused` | why a department can't be added, and the way back | `DeptNewData` |

### templates/_workshop_docs.html

| Component | What it draws | Data |
|---|---|---|
| `wb/binder` | the list of documents, by folder | `WbBinder` |
| `wb/binder-item` | one document in the binder | `WbBinderItem` |
| `wb/doc-head` | kind, system, date, title and lead | `*WbDoc` |
| `wb/guard-rule` | the guard rule callout | `string` |
| `wb/doc-sections` | chips linking to the "## " sections | `[]WbDocSection` |
| `wb/doc-body` | the rendered Markdown | `template.HTML` |
| `wb/side-label` | a small caps label over a side block | `string` |
| `wb/doc-guards` | the code a document guards | `*WbDocSide` |
| `wb/freshness` | how long ago it changed, as a bar | `WbFreshness` |
| `wb/doc-citer` | an issue or decision tied to the doc | `WbDocCiter` |
| `wb/doc-agents` | issues and decisions tied to the doc | `*WbDocSide` |
| `wb/doc-edit` | Edit in nvim | `string (POST URL)` |

### templates/_workshop_floor.html

| Component | What it draws | Data |
|---|---|---|
| `wb/fl-header` | one header row: crumbs, goal chips, counts | `FloorData` |
| `wb/fl-yolo` | the 34px YOLO strip (and the YOLO script's hook) | `WbYOLO` |
| `wb/fl-tile` | one room on the plan (HQ, Product, dept, wing, plot) | `FloorTile` |
| `wb/fl-stations` | a line as a fixed strip | `FloorTile` |
| `wb/fl-sub` | a department or nested group inside a wing | `FloorSub` |
| `wb/fl-roads` | corridors and connection roads under the tiles | `FloorPlan` |
| `wb/fl-rail` | Waiting on you, Room | After YOLO, message box | `FloorData` |
| `wb/fl-insp` | one room in the rail | `FloorInspector` |
| `wb/fl-num` | one of a tile's three numbers | `FloorNum` |
| `wb/fl-goals` | a tile's goals as bars (detail level) | `[]WbGoal` |

### templates/_workshop_hall_edit.html

| Component | What it draws | Data |
|---|---|---|
| `wb/edit-row` | one action row: type and its fields | `HallEditRow` |
| `wb/edit-edge` | one edge: gate switch and its rows | `HallEditEdge` |
| `wb/edit-preview` | the diff, its checks, Confirm | `HallEditData` |

### templates/_workshop_hall_part.html

| Component | What it draws | Data |
|---|---|---|
| `wb/part-edge` | one transition | `WbPartEdge` |
| `wb/part-edges` | transitions as rows, with actions | `[]WbPartEdge` |
| `wb/part-overlays` | the types and systems that change it | `[]WbPartOverlay` |
| `wb/part-prompt` | the status description and prompt | `HallPartData` |
| `wb/part-station` | the station opened | `HallPartData` |
| `wb/part-gate` | the gate opened | `HallPartData` |
| `wb/part-ci` | the CI scanner opened | `WbPartCI` |

### templates/_workshop_health.html

| Component | What it draws | Data |
|---|---|---|
| `wb/hall-tabs` | the hall's view toggle: Line \| Report \| Health | `*WbHallTabs` |
| `wb/health-range` | the 7 / 30 / 90 days / All toggle | `[]statsOption` |
| `wb/health-score` | "Score from measurements" | `WbHealthScore` |
| `wb/weight-bar` | the score's weight, a segment per sensor | `WbWeightBar` |
| `wb/health-hint` | the shortest way to (more of) a score | `*WbHealthHint` |
| `wb/give-agent` | hand a task to the CEO (lead/send) | `WbGiveAgent` |
| `wb/health-opinion` | "Agent opinion": the review agent and score | `WbHealthOpinion` |
| `wb/health-recs` | the review's proposed recommendations | `WbHealthOpinion` |
| `wb/health-rec` | a proposed recommendation: file or dismiss | `WbHealthRec` |
| `wb/copy-cmd` | a command with a Copy button | `string` |
| `wb/sensor-group` | a group of sensors (Code, Tests) | `WbSensorGroup` |
| `wb/sensor` | one sensor on its good → bad scale | `WbSensor` |
| `wb/look-list` | a ranked list with bars | `WbLookList` |
| `wb/refactor` | file a refactor issue for a file on both lists | `*WbRefactor` |
| `wb/trend-card` | a trend chart, or locked with what it lacks | `WbTrendCard` |
| `wb/health-setup` | "Measurement setup": warnings and lint | `WbHealthSetup` |
| `wb/lint-row` | one lint finding with its fix | `lint.Finding` |
| `wb/health-departments` | the departments family per department: on track, goals, outputs, jobs, handoffs (`#departments`) | `DeptHealth` |

### templates/_workshop_hq_report.html

| Component | What it draws | Data |
|---|---|---|
| `wb/hqr-box` | one box's section: a fold whose summary is the headline | `HQRBox` |
| `wb/hqr-headline` | the dot, the box's name and its headline parts | `HQRBox` |
| `wb/hqr-rows` | the detail: labelled rows of lines, or the empty line | `HQRBox` |

### templates/_workshop_issues.html

| Component | What it draws | Data |
|---|---|---|
| `wb/shelf-label` | a shelf's status label | `string` |
| `wb/crate-icon` | the crate drawing | `none` |
| `wb/crate-card` | one issue as a crate card | `WbCrateCard` |
| `wb/shelf` | one status's shelf of crate cards, "+N more" | `WbShelf` |
| `wb/create-issue` | the New issue dialog: title, system, status, type, priority, labels, body from the status's template; posts to /issues/create | `WbCreateIssue` |

### templates/_workshop_needs_you.html

| Component | What it draws | Data |
|---|---|---|
| `wb/ny-bar` | Select all ready and Approve selected | `NeedsYouData` |
| `wb/ny-group` | one project's sections | `NeedsYouGroup` |
| `wb/ny-section` | one kind of row: To approve, To test, … | `NeedsYouSection` |
| `wb/ny-row` | one item waiting on you, its actions | `WbQueueCard` |
| `wb/ny-after` | YOLO's after-the-fact queue: counts, oldest age, Review all, at most 8 rows | `NeedsYouGroup` |
| `wb/ny-empty` | nothing is waiting | `none` |
| `wb/ny-confirm` | the Approve dialog | `none` |

### templates/_workshop_owner_checks.html

| Component | What it draws | Data |
|---|---|---|
| `wb/oc-epic-bar` | the epic select, its status and counts | `OwnerChecksData` |
| `wb/oc-pick` | the epic cards to pick from | `OwnerChecksData` |
| `wb/oc-issue` | one issue's open owner boxes | `OwnerChecksIssue` |
| `wb/oc-box` | one box: tick, text, why, shot | `OwnerCheckBox` |
| `wb/oc-empty` | nothing waits for the owner's eyes | `OwnerChecksData` |

### templates/_workshop_yolo_review.html

| Component | What it draws | Data |
|---|---|---|
| `wb/yr-summary` | issues, gates, owner checks, oldest, page | `YOLOReviewData` |
| `wb/yr-group` | one issue: head, evidence, gates, checks, its Agree and Back to | `YOLOReviewGroup` |
| `wb/yr-evidence` | the issue's shots, tests line and change | `YOLOReviewEvidence` |
| `wb/yr-gate` | one gate passed without the owner | `WbYOLOPass` |
| `wb/yr-check` | one owner box left open: Tick, Agree without it | `WbYOLOPass` |
| `wb/yr-question` | one answer the agent assumed in its clarify round, linked to the Station's form | `WbYOLOPass` |
| `wb/yr-pager` | newer / older issues | `YOLOReviewData` |

### templates/_workshop_memory.html

| Component | What it draws | Data |
|---|---|---|
| `wb/mem-filters` | the scope, kind, state, pin and group seg links | `[]MemoryFilter` |
| `wb/mem-entities` | the entity index: each #topic and @name with its count and who knows it, a link that filters to it (`?entity=`) | `MemoryData` |
| `wb/mem-group` | the memories under one entity (`?group=entity`), "No entity" last | `MemoryGroup` |
| `wb/mem-recent` | what agents learned in the last 7 days | `[]MemoryEventLine` |
| `wb/mem-row` | one memory: kind, scope, state, pin, source, links, the text to edit, Save / Pin / Delete, history | `MemoryRow` |
| `wb/mem-empty` | no memory matches, and the add command | `MemoryData` |

### templates/_workshop_report.html

| Component | What it draws | Data |
|---|---|---|
| `wb/view-toggle` | Board \| Report | `[]WbViewTab` |
| `wb/lock-icon` | a padlock | `none` |
| `wb/badge` | a small pill with a padlock, linking | `WbBadge` |
| `wb/report-title` | the title band: title, badges, buttons | `WbReportTitle` |
| `wb/action` | a button, link or copy-command | `WbAction` |
| `wb/stack-bar` | a stacked bar | `[]WbSegment` |
| `wb/squares` | one square per agent | `[]WbSquare` |
| `wb/legend` | counted lines under a big number | `[]WbLegendRow` |
| `wb/count-card` | a card with a big number | `WbCountCard` |
| `wb/bottleneck` | the dark banner: what holds things up | `WbBottleneck` |
| `wb/bar` | a labelled done/total bar | `WbBar` |
| `wb/where` | Where we are | `WbWhere` |
| `wb/shipped` | Shipped in 7 days by system | `WbShipped` |
| `wb/score-box` | a score in a small box | `WbScoreBox` |
| `wb/health` | Code health | `WbHealth` |
| `wb/unlock-item` | one thing to enable | `WbUnlockItem` |
| `wb/unlock` | To unlock | `WbUnlock` |
| `wb/check-mark` | a met / not-met mark | `bool` |
| `wb/release` | Waiting for release: milestone, checks, release candidate, next steps, open issues, ## Unreleased, unpushed commits | `*StatusModel` |
| `wb/ci` | Local CI: state, last runs, what failed | `*StatusModel` |
| `wb/behind` | the installed build behind main (engine project only): `installed <describe> · main is N commits ahead`, the changelog entries since, the install command | `*StatusModel` |
| `wb/active-list` | Working on: each issue in flight with its status, age and agent; agents on issues not started | `WbActiveList` |
| `wb/active-row` | one issue in flight | `WbActiveRow` |
| `wb/released` | Recently released: the newest releases, or what the first one will be | `WbReleased` |
| `wb/queue` | the lead's Queue card: next 5, blocked, done today, the refill line, the verdict | `*StatusModel` |
| `wb/autostart` | the Auto-start card | `*StatusModel` |
| `wb/bench` | the Benchmark card | `*WbBench` |

### templates/_workshop_retro.html

| Component | What it draws | Data |
|---|---|---|
| `wb/retro-presort` | Claude \| Codex and "Let an agent pre-sort" | `WbRetroPresort` |
| `wb/retro-loop` | the loop strip and its counters | `WbRetroLoop` |
| `wb/retro-list` | a column of retros (To sort, Processed) | `WbRetroList` |
| `wb/retro-item` | one retro in a list | `WbRetroItem` |
| `wb/retro-head` | the selected retro: chips, title, file | `WbRetroView` |
| `wb/retro-progress` | "Distributed k/n" and close / reopen | `WbRetroProgress` |
| `wb/retro-note` | the agent's note as written | `WbRetroView` |
| `wb/retro-finding` | one finding and where it goes | `WbRetroFinding` |
| `wb/retro-send` | the "Send to" box and its action | `WbRetroSend` |
| `wb/retro-bin` | a bin: how many findings go there | `WbRetroBin` |
| `wb/retro-pattern` | a term other issues' retros mention too | `*WbRetroPattern` |
| `wb/retro-bugs` | the Bugs view (`?view=bugs`): open / closed counts, a status filter, one card per tool bug report | `*WbRetroBugs` |
| `wb/retro-bug` | one tool bug report: issue, tool, time, text, Open / Fixed / Won't fix | `WbRetroBug` |

### templates/_workshop_shelf_read.html

| Component | What it draws | Data |
|---|---|---|
| `wb/odoc-toc` | the output reader's contents: headings with open comment counts | `[]OdocTocItem` |
| `wb/odoc-head` | an output's state, department, kind, version, issue and title | `ShelfReadData` |
| `wb/odoc-bar` | Read / Changes, Day / Night, Raw file, Approve / Reject | `ShelfReadData` |
| `wb/odoc-sheet` | the rendered markdown output, or the missing-file state | `ShelfReadData` |
| `wb/odoc-diff` | the previous version against this one, unchanged runs folded | `*OdocDiff` |
| `wb/odoc-margin` | the comment box and the open, moved and resolved threads | `ShelfReadData` |
| `wb/odoc-thread` | one comment thread with its messages, Reply and Resolve | `OdocThread` |

### templates/_workshop_station.html

| Component | What it draws | Data |
|---|---|---|
| `wb/issue-title` | the crate, title, chips and slug | `WbIssueTitle` |
| `wb/belt` | the issue's path, time per status | `WbBelt` |
| `wb/time-split` | where the time goes | `WbTimeSplit` |
| `wb/your-move` | the owner's steps, then the stamp | `WbYourMove` |
| `wb/move-task` | one numbered step before the stamp | `WbMoveTask` |
| `wb/stamp` | the gate approval with your words | `*WbStamp` |
| `wb/quality-card` | one card of the quality check | `WbQualityCard` |
| `wb/clarify` | the clarify round as one form: every question, its options with the cost, the recommendation first, Answer all | `*WbClarify` |
| `wb/clarify-q` | one question: options as radios, the owner's words, its assumed or answered state | `WbClarifyQuestion` |
| `wb/decisions` | the D-n list | `WbDecisions` |
| `wb/decision` | one decision | `WbDecision` |
| `wb/change-card` | the files the work branch changed | `WbChangeCard` |
| `wb/tests-card` | tests, proofs and the checklist | `WbTestsCard` |
| `wb/check-group` | one section's checkboxes | `WbCheckGroup` |
| `wb/timeline-list` | the issue's life, newest first | `WbTimelineList` |
| `wb/station-agent` | the issue's agent | `WbStationAgent` |
| `wb/sessions` | tmux sessions on the issue | `WbSessionList` |
| `wb/body-index` | the body by section, with counts | `WbBodyIndex` |
| `wb/fields` | the fields: Status, Type, Priority, Assignee, Version and Labels edited in place, then read-only rows (system, created, epic, GitHub, score, shared files, CEO, other fields) | `WbFields` |
| `wb/move-preview` | a status change's transition preview: each rule, required fields, your words when it needs your approval | `*WbFieldEdit` |
| `wb/brief` | what this is: the lead paragraph, your words, the parent epic's bar | `*WbBrief` |
| `wb/approving` | what you're approving since the last gate: counts and events | `WbApproving` |
| `wb/mockups` | the issue's mockup images and the canvas link | `*WbMockups` |
| `wb/relations` | depends on, depended on by, decided by, each with its state | `*WbRelations` |
| `wb/epic-children` | an epic's children by version | `*WbEpicChildren` |
| `wb/full-body` | the body folded by section: tick a box, Expand all, Edit with the body-hash guard | `WbBody` |
| `wb/dispatch` | Dispatch to Claude / Codex, the workflow's custom actions, the steps, the dispatch prompt | `WbDispatch` |
| `wb/station-missing` | the sources the page could not read | `[]string` |
| `wb/station-outputs` | what the issue made, from the shelf | `*WbStationOutputs` |
| `wb/station-critique` | the fresh critic's newest run: verdict, blocks and should with path:line, notes folded, Coverage (docs/Workflow/critique.md) | `*WbStationCritique` |

### templates/_workshop_stats.html

| Component | What it draws | Data |
|---|---|---|
| `wb/seg-links` | a row of options as links (seg.&lt;Name&gt;) | `WbSegLinks` |
| `wb/empty` | what is missing and what fills it | `WbEmpty` |
| `wb/gauge` | one gauge, locked or empty when no data | `WbGauge` |
| `wb/series-legend` | series swatches; a link toggles one | `[]LegendItem` |
| `wb/chart-card` | a chart, its table view and drill-down | `WbChartCard` |
| `wb/drill` | the issues behind a point or bar | `Drill` |
| `wb/data-table` | a table | `WbTable` |
| `wb/cell` | one data-table cell: text, code or a link | `WbCell` |
| `wb/table-card` | a titled table, or its empty state | `WbTable` |
| `wb/fold` | folded tables | `WbFold` |
| `wb/see-also` | a number another page owns, linked | `WbSeeAlso` |
| `wb/project-stat` | one project on /stats | `OverviewRow` |

### templates/_workshop_systems.html

| Component | What it draws | Data |
|---|---|---|
| `wb/figures` | big numbers with labels | `[]WbFigure` |
| `wb/crate-dot` | an issue as a small crate, by its state | `WbCrateDot` |
| `wb/crate-legend` | what the crate colours mean | `[]WbCrateKey` |
| `wb/code-chip` | a file on a workshop's CODE row | `WbCodeChip` |
| `wb/workshop` | a system as a workshop card | `WbWorkshop` |
| `wb/code-file` | a file on the code map | `WbCodeFile` |
| `wb/code-package` | a package on the code map | `WbCodePackage` |
| `wb/code-map` | packages → files → systems (draft w/o paths) | `WbCodeMap` |
| `wb/warning` | a red note | `*WbWarning` |
| `wb/unlock-card` | a locked feature and its action | `*WbUnlockCard` |
| `wb/trail` | the page crumb Systems › System › Family | `WbTrail` |
| `wb/link-row` | a row of links | `[]WbLink` |
| `wb/status-bar` | a bar per workflow status, with legend | `WbStatusBar` |
| `wb/floor` | a system's crates, legend and status bar | `WbFloor` |
| `wb/family-card` | a family with its progress | `FamilyCard` |
| `wb/epic-card` | an epic with issues in the system | `EpicCard` |
| `wb/families` | the families (or epics) panel | `*WbFamilies` |
| `wb/sys-decisions` | the latest decisions, decision-log rows | `WbSysDecisions` |
| `wb/design-doc` | the design doc's sections and their state | `*DocPanel` |
| `wb/guard-metrics` | the system's guard metrics | `*MetricsPanel` |
| `wb/knobs` | the knobs the system moves | `*KnobsPanel` |
| `wb/balance-note` | a balance: comment | `BalanceItem` |
| `wb/not-found` | the index's note for an unknown system | `string` |

### templates/_workshop_updates.html

| Component | What it draws | Data |
|---|---|---|
| `wb/upd-flash` | the line after a click, with Undo | `WbUpgradeFlash` |
| `wb/delivery` | template@version → the engine's | `WbDelivery` |
| `wb/upgrade-tree` | a square per feature | `WbUpgradeTree` |
| `wb/filters` | group filter pills with counts | `[]WbFilterChip` |
| `wb/unlocks` | what a feature unlocks, linked | `[]WbUnlock` |
| `wb/upgrade-card` | one proposed feature and its actions | `WbUpgradeCard` |
| `wb/upgrade-more` | the "N more" row | `WbUpgradeMore` |
| `wb/dept-templates` | Departments you can add | `[]WbDeptTemplate` |
| `wb/dept-updates` | Departments with new template text (`department update`) | `[]WbDeptUpdate` |
| `wb/declined` | declined features with Re-open | `[]WbDeclinedRow` |
| `wb/upd-tally` | the numbers across projects | `[]WbUpdTally` |
| `wb/upd-project` | one project's template and state | `UpdatesOverviewRow` |
| `wb/upd-note` | what "never scanned" means | `none` |

### templates/_workshop_yolo.html

| Component | What it draws | Data |
|---|---|---|
| `wb/yolo` | the header's part: switch (L1), count, band, dialog | `WbYOLO` |
| `wb/yolo-switch` | the switch in the company's header (L1) | `WbYOLO` |
| `wb/hazard-band` | the band under the header while YOLO is on | `WbYOLO` |
| `wb/yolo-dialog` | the turn-on dialog (L1) | `WbYOLODialog` |
| `wb/yolo-tag` | the yellow YOLO marker | `string (text after "YOLO")` |
| `wb/yolo-review` | "Passed in YOLO": Agree / Back to (L3) | `WbYOLOReview` |
| `wb/yolo-queue` | "To review after YOLO" (L1 side panel) | `CompanyData` |
