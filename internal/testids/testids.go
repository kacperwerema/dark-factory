// Package testids is the single list of data-testid ids the board's templates
// carry. Playtest sessions (playtest/sessions/*.yaml) point at these ids;
// `make playtest-check` rejects a session target that matches no entry, and
// TestTemplateTestIDs rejects a template id that is not listed here (or an
// entry no template uses).
//
// Ids are `page.area.element`, lower snake case, dots between levels. In an
// entry, `*` is any one segment (a status, a slug) and `#` is an index.
package testids

import (
	"regexp"
	"strings"
)

// IDs lists every test id and pattern. Keep it grouped by page.
var IDs = []string{
	// Project sidebar (templates/_sidebar.html), on every project page.
	"nav.sidebar",
	"nav.project",
	"nav.create",
	"nav.tab.*",
	"nav.tab.status.count",
	"nav.tab.updates.count",
	"nav.more",
	"nav.agents_running",
	"nav.yolo",
	"nav.yolo.extend",
	"nav.yolo.off",
	"nav.yolo.queue",
	"nav.yolo.row.*",
	"nav.panel",
	"nav.panel.system.*",
	"nav.panel.legend",
	"nav.panel.family.*",
	"nav.panel.row.*",
	"nav.page.#",
	"nav.actions",
	"nav.theme.*",
	"header.count",

	// Workboard (/p/<project>/workboard): the board with card markers.
	"workboard.page",
	"workboard.group.*",
	"workboard.filter.version",
	"workboard.filter.system",
	"workboard.filter.waiting",
	"workboard.filter.all",
	"workboard.search",
	"workboard.empty",
	"workboard.column.*",
	"workboard.department.*",
	"workboard.workflow_error",
	"workboard.column.*.count",
	"workboard.column.*.more",
	"workboard.column.*.quiet",
	"workboard.card.*",
	"workboard.card.*.age",
	"workboard.card.*.epic",
	"workboard.card.*.markers",
	"workboard.card.*.agent",
	// Card marker chips (templates/_markers.html), on any view showing cards.
	"marker.*",
	// Words box under approve buttons (templates/_approve_words.html):
	// approve_words.detail, approve_words.agents_panel.
	"approve_words.*",
	// Status (/p/<project>/status).
	"status.page",
	"status.tabs",
	"status.tab.project",
	"status.tab.builtin",
	"status.copy_md",
	"status.raw_md",
	"status.strip",
	"status.waiting.pop",
	"status.waiting.pop.summary",
	"status.working.pop",
	"status.working.pop.summary",
	"status.next.pop",
	"status.next.pop.summary",
	"status.milestone",
	"status.milestone.bar",
	"status.milestone.segment.*",
	"status.milestone.area.*",
	"status.milestone.areas_more",
	"status.milestone.epics",
	"status.all_progress",
	"status.all_progress.bar",
	"status.no_versions",
	"status.version.*",
	"status.epics",
	"status.epics.tab.active",
	"status.epics.tab.not_started",
	"status.epics.tab.all",
	"status.epics.version",
	"status.epics.filter",
	"status.epics.panel.*",
	"status.epics.area.*",
	"status.epics.area.*.more",
	"status.epics.empty",
	"status.epic.*",
	"status.epic.close.*",
	"detail.epic",
	"detail.epic.label",
	"detail.epic.version.*",
	"detail.epic.child.*",
	"status.epic.later.*",
	"status.working",
	"status.lead",
	"status.working.row.*",
	"status.working.more",
	"status.design_agents",
	"status.design_agents.more",
	"status.next",
	"status.next.source",
	"status.backlog",
	"status.backlog.more",
	"status.parked",
	"status.parked.more",
	"status.waiting",
	"status.waiting.count",
	"status.waiting.gate.*",
	"status.waiting.playtest.*",
	"status.waiting.checks.*",
	"status.waiting.question.*",
	"status.waiting.not_ready",
	"status.waiting.more.*",
	"status.waiting.ci",
	"status.waiting.cli_bugs",
	"status.health",
	"status.health.open",
	"status.health.derived",
	"status.health.opinion",
	"status.health.recommendations",
	"status.privacy",
	"status.privacy_card",
	"status.privacy.badge.*",
	"status.privacy.line.*",
	"status.privacy.setup.*",
	"status.privacy.note.*",
	"status.health.lint",
	"status.ci",
	"status.ci.state",
	"status.ci.line",
	"status.ci.runs",
	"status.ci.run.*",
	"status.ci.failed",
	"status.ci.blamed",
	"status.ci.error",
	"status.ci.log",
	"status.release_wait",
	"status.release_wait.state",
	"status.release_wait.milestone",
	"status.release_wait.checks",
	"status.release_wait.check.*",
	"status.release_wait.ready",
	"status.release_wait.cut_rc",
	"status.release_wait.publish",
	"status.release_wait.candidate",
	"status.rc_badge",
	"status.release_wait.candidate.fold",
	"status.release_wait.candidate.summary",
	"status.release_wait.candidate.tag",
	"status.release_wait.candidate.running",
	"status.release_wait.candidate.projects",
	"status.release_wait.candidate.project.*",
	"status.release_wait.candidate.reports",
	"status.release_wait.candidate.report.*",
	"status.release_wait.candidate.fixed",
	"status.release_wait.open",
	"status.release_wait.open.row.*",
	"status.release_wait.open.epic.*",
	"status.release_wait.open.fold",
	"status.release_wait.open.more",
	"status.release_wait.unreleased",
	"status.release_wait.unreleased.more",
	"status.release_wait.unpushed",
	"status.release_wait.fit",
	"status.shipped",
	"status.shipped.full_list",
	"status.shipped.area.*",
	"status.shipped.more_areas",
	"status.versions",
	"status.versions.more",
	"status.versions.fit",
	"status.released",
	"status.released.fit",
	"status.released.hint",
	"status.release.*",

	// Board (/p/<project>/board).
	"board.filters",
	"board.search",
	"board.filter.version",
	"board.filter.system",
	"board.filter.type",
	"board.filter.assignee",
	"board.clear",
	"board.hide_empty",
	"board.layout_toggle",
	"board.column.*",
	"board.column.*.title",
	"board.column.*.count",
	"board.column.*.add",
	"board.column.*.cards",
	"board.card.*",
	"board.card.*.title",
	"board.transition_modal",
	"board.transition_modal.title",
	"board.transition_modal.error",
	"board.transition_modal.confirm",
	"board.transition_modal.cancel",
	"board.toast",

	// List (/p/<project>/).
	"list.filters",
	"list.search",
	"list.filter.status",
	"list.filter.system",
	"list.filter.priority",
	"list.filter.assignee",
	"list.filter.type",
	"list.filter.label",
	"list.clear",
	"list.row.*",
	"list.row.*.title",
	"list.row.*.status",
	"list.empty",

	// Detail (/p/<project>/issue/<slug>).
	"detail.back",
	"detail.title",
	"detail.title.input",
	"detail.title.save",
	"detail.tab.preview",
	"detail.tab.edit",
	"detail.body",
	"detail.body.checkbox.#",
	"detail.body.textarea",
	"detail.body.save",
	"detail.sidebar",
	// Issue tab (templates/_issue_tab.html, issue_tab.go): the band, the
	// main column's cards, the folded body and the side column.
	"detail.brief",
	"detail.brief.text",
	"detail.brief.words",
	"detail.path",
	"detail.path.step.*",
	"detail.time",
	"detail.needs",
	"detail.needs.state",
	"detail.needs.approve.*",
	"detail.needs.command",
	"detail.needs.copy",
	"detail.needs.run_checks",
	"detail.needs.run_playtest",
	"detail.needs.playtest_result",
	"detail.needs.playtest_tick",
	"detail.needs.answer",
	"detail.needs.answer.send",
	"detail.needs.also",
	"detail.approving",
	"detail.approving.count.*",
	"detail.approving.event",
	"detail.mockups",
	"detail.decisions",
	"detail.decision.*",
	"detail.fold",
	"detail.fold.section.*",
	"detail.fold.toggle.*",
	"detail.fold.expand_all",
	"detail.fold.not_started",
	"detail.agent",
	"detail.agent.open",
	"detail.dispatch.*",
	"detail.criteria",
	"detail.relations",
	"detail.fields",
	"detail.status",
	"detail.priority",
	"detail.assignee",
	"detail.approve.*",
	"detail.approve_then.*",
	"detail.labels",
	"detail.label.input",
	"detail.label.add",
	"detail.block.#",
	"detail.comment.eye.#",
	"detail.comment.input.#",
	"detail.comment.submit.#",
	"detail.toast",
	"detail.lead",
	"detail.lead.name",
	"detail.lead.state",
	"detail.pagetabs",
	"detail.pagetab.issue",
	"detail.pagetab.timeline",
	"detail.pagetab.evidence",
	// The Evidence tab (evidence.go, _evidence.html).
	"evidence.label",
	"evidence.tab",
	"evidence.missing",
	"evidence.empty",
	"evidence.shots",
	"evidence.shots.empty",
	"evidence.shots.other",
	"evidence.shot.*",
	"evidence.shot.vp.*",
	"evidence.criteria",
	"evidence.criterion.*",
	"evidence.link",
	"evidence.strays",
	"evidence.change",
	"evidence.change.area",
	"evidence.verdict",
	"evidence.playtest",
	"evidence.playtest.step.*",
	"evidence.playtest.run",
	"evidence.playtest.result",
	"evidence.playtest.tick",
	"evidence.key",
	"evidence.watch",
	"evidence.stale",
	"evidence.stale.line",

	// Timeline tab (/p/<project>/issue/<slug>?tab=timeline, templates/_timeline.html).
	"timeline.tab",
	"timeline.missing",
	"timeline.total",
	"timeline.working",
	"timeline.waiting",
	"timeline.idle",
	"timeline.rework",
	"timeline.commits",
	"timeline.bar",
	"timeline.filter.*",
	"timeline.band",
	"timeline.band.working",
	"timeline.wait",
	"timeline.idlegap",
	"timeline.reworkloop",
	"timeline.folded",
	"timeline.cli.call",
	"timeline.cli.actions",
	"timeline.approval.words",
	"timeline.side.part",
	"timeline.side.words",
	"timeline.side.open",
	"timeline.side.ci",

	// The dispatch modal's refusal when the issue only waits on the owner's
	// approval (board and issue page): approve link, Dispatch anyway.
	"dispatch.gate",
	"dispatch.gate.approve",
	"dispatch.gate.force",

	// Project actions bar and its dispatch modal (list, board, graph).
	"actions.spawn_lead",
	"actions.modal",
	"actions.modal.steps",

	// Agents canvas (/p/<project>/agents). Nodes are keyed by tmux session
	// name (lead-<project>, agent-<slug>).
	"agents.toolbar",
	"agents.layout.tree",
	"agents.layout.radial",
	"agents.opt.issues",
	"agents.opt.stopped",
	"agents.live",
	"agents.spawn_lead",
	"agents.canvas",
	"agents.tree",
	"agents.radial",
	"agents.you",
	"agents.node.*",
	"agents.node.*.state",
	"agents.node.*.issue",
	"agents.unfinished",
	"agents.panel",
	"agents.panel.title",
	"agents.panel.issue",
	"agents.panel.last_call",
	"agents.panel.approve",
	"agents.panel.spawn",
	"agents.panel.open",
	"agents.panel.open_status",
	"agents.panel.copy",
	"agents.panel.activity",
	"agents.panel.branch",
	"agents.panel.diff",
	"agents.spawn_modal",
	"agents.spawn_modal.steps",
	"agents.spawn_modal.close",
	"detail.canvas_link",

	// Issues canvas (/p/<project>/canvas). Lanes are keyed by epic slug,
	// "no_epic" or the system; columns and cells by version ("0_1",
	// "no_version").
	"canvas.toolbar",
	"canvas.count",
	"canvas.rows",
	"canvas.epic",
	"canvas.collapse_done",
	"canvas.show_no_version",
	"canvas.no_version_note",
	"canvas.expand_all",
	"canvas.fold_all",
	"canvas.highlight",
	"canvas.highlight.name",
	"canvas.highlight.clear",
	"canvas.zoom.out",
	"canvas.zoom.label",
	"canvas.zoom.in",
	"canvas.zoom.fit",
	"canvas.viewport",
	"canvas.world",
	"canvas.column.*",
	"canvas.column.*.progress",
	"canvas.column.*.extra",
	"canvas.lane.*",
	"canvas.lane.*.fold",
	"canvas.lane.*.kind",
	"canvas.lane.*.status",
	"canvas.lane.*.parent",
	"canvas.lane.*.title",
	"canvas.lane.*.summary",
	"canvas.lane.*.highlight",
	"canvas.lane.*.note",
	"canvas.lane.*.show_no_version",
	"canvas.cell.*.*",
	"canvas.cell.*.*.closed",
	"canvas.cell.*.*.empty",
	"canvas.cell.*.*.fold",
	"canvas.card.*",
	"canvas.card.*.status",
	"canvas.card.*.type",
	"canvas.card.*.title",
	"canvas.card.*.epic",
	"canvas.card.*.unknown_parent",
	// Decisions index (/p/<project>/decisions). Rows are keyed by the
	// Systems index and system / family pages (templates/systems.html, system.html).
	"systems.index",
	"systems.notfound",
	"systems.waiting",
	"systems.empty",
	"systems.card.*",
	"systems.nosystem",
	// The Workshop Board Systems page (templates/systems_wb.html and
	// _workshop_systems.html, /p/<project>/systems): L2.
	"systems.wb.page",
	"systems.wb.legend",
	"systems.wb.classic",
	"systems.wb.empty",
	"systems.wb.card.*",
	"systems.wb.gates",
	"systems.wb.crates",
	"systems.wb.code",
	"systems.wb.decisions",
	"systems.wb.line",
	"systems.wb.codemap",
	"systems.wb.codemap.empty",
	"systems.wb.shared_hot",
	"systems.wb.unlock",
	"systems.wb.unlock.send",
	"system.header",
	"system.summary",
	"system.hint",
	"system.chip.counts",
	"system.chip.umbrella",
	"system.chip.waiting",
	"system.links",
	"system.statusbar",
	"system.issues",
	"system.issue.*",
	"system.issues.more",
	"system.families",
	"system.family.*",
	"system.epic.*",
	"system.doc",
	"system.doc.missing",
	"system.doc.section.*",
	"system.decisions",
	"system.decision.*",
	"system.balance",

	// recording issue's slug and the decision id ("d-1").
	"decisions.stats",
	// Updates tab (templates/updates.html), and /updates (updates_overview.html).
	"updates.page",
	"updates.flash",
	"updates.undo",
	"updates.template",
	"updates.state",
	"updates.empty",
	"updates.filters",
	"updates.filter.*",
	"updates.card.*",
	"updates.card.*.installing",
	"updates.card.*.session",
	"updates.card.*.details",
	"updates.card.*.snippet",
	"updates.card.*.setup",
	"updates.card.*.install",
	"updates.card.*.words",
	"updates.card.*.start",
	"updates.card.*.decline",
	"updates.installed",
	"updates.declined",
	"updates.declined.*",
	"updates.declined.*.reopen",
	"updates.overview",
	"updates.overview.stats",
	"updates.overview.row",
	// The hall's Updates screen (templates/updates_wb.html and
	// _workshop_updates.html, /p/<project>/updates): L2 of the Workshop Board.
	"upd.page",
	"upd.classic",
	"upd.flash",
	"upd.undo",
	"upd.delivery",
	"upd.tree",
	"upd.empty",
	"upd.filters",
	"upd.filter.*",
	"upd.card.*",
	"upd.card.*.installing",
	"upd.card.*.unlocks",
	"upd.card.*.session",
	"upd.card.*.install",
	"upd.card.*.words",
	"upd.card.*.start",
	"upd.card.*.details",
	"upd.card.*.decline",
	"upd.more",
	"upd.show_all",
	"upd.declined",
	"upd.declined.*",
	"upd.declined.*.reopen",
	// The map of projects (templates/map.html, "/"): L0 of the Workshop Board design.
	"map.brand",
	"map.agents",
	"map.done_week",
	"map.waiting",
	"map.lot.*",
	"map.lot.buildings",
	"map.lot.robots",
	"map.lot.quote",
	"map.lot.progress",
	"map.lot.waiting",
	"map.lot.yolo",
	"map.lot.after_fact",
	"map.site.start_ceo",
	"map.leads_link",
	"map.updates_link",
	"map.tab.queue",
	"map.tab.radio",
	"map.queue.row",
	"map.queue.empty",
	// The company (templates/company.html, /p/<project>/company): L1.
	"company.goal",
	"company.ceo.quote",
	"company.ceo.talk",
	"company.ceo.start",
	"company.room.hq",
	"company.room.product",
	"company.room.product.yolo",
	"company.room.dept",
	"company.room.dept.line",
	"company.room.dept.workflow_error",
	"company.room.goals",
	"company.room.mission",
	"company.hq.rollup",
	"company.hq.rollup.child",
	"company.room.outputs",
	"company.hq.outputs",
	"company.queue.outputs",
	"shelf.page",
	"shelf.depts",
	"shelf.states",
	"shelf.list",
	"shelf.row",
	"shelf.item",
	"shelf.thumb",
	"shelf.title",
	"shelf.state",
	"shelf.missing",
	"shelf.issue",
	"shelf.approve",
	"shelf.reject",
	"shelf.empty",
	"station.outputs",
	"report.waiting.link",
	"company.line",
	"company.enter_hall",
	"company.queue.row",
	"company.yolo_queue",
	"company.yolo_queue.row",
	"company.queue.empty",
	"company.queue.more",
	// The hall (templates/hall.html, /p/<project>/hall): L2.
	"hall.line",
	"hall.station",
	"hall.gate",
	"hall.gate.raised",
	"hall.crate.yolo",
	"hall.crate",
	"hall.done",
	"hall.ci",
	"hall.parking",
	"hall.agent",
	"hall.agent.open",
	"hall.numbers",
	// Docs (templates/docs_wb.html, /p/<project>/docs): the binder (L2).
	"docs.binder",
	"docs.item.*",
	"docs.reader",
	"docs.title",
	"docs.guard_rule",
	"docs.sections",
	"docs.body",
	"docs.empty",
	"docs.classic",
	"docs.guards",
	"docs.freshness",
	"docs.agents",
	"docs.system_decisions",
	"docs.edit",
	"docs.ask",
	"docs.start_ceo",
	"company.radio",
	"company.message",
	// The station (templates/station.html, /p/<project>/issue/<slug>): L3,
	// one issue to verify; components in templates/_workshop_station.html.
	"station.title",
	"station.classic",
	"station.belt",
	"station.time",
	"station.move",
	"station.yolo",
	"station.yolo.pass",
	"station.yolo.agree",
	"station.yolo.back",
	"station.move.task.*",
	"station.move.run_playtest",
	"station.move.playtest_result",
	"station.move.playtest_tick",
	"station.stamp",
	"station.stamp.go",
	"station.stamp.back",
	"station.quality",
	"station.quality.*",
	"station.decisions",
	"station.change",
	"station.tests",
	"station.timeline",
	"station.timeline.filter.*",
	"station.agent",
	"station.agent.open",
	"station.agent.prompt",
	"station.sessions",
	"station.body",
	"station.body.nvim",
	"station.fields",
	// Workshop Board components (templates/_workshop.html), on every level.
	"chip.*",
	"tile.*",
	"plan.*",
	// YOLO mode (templates/_workshop_yolo.html): the switch on L1, the band
	// under the header on every project page, the turn-on dialog.
	"yolo.switch",
	"yolo.count",
	"yolo.band",
	"yolo.band.passed",
	"yolo.band.extend",
	"yolo.band.off",
	"yolo.dialog",
	"yolo.dialog.gates",
	"yolo.dialog.guards",
	"yolo.dialog.ci_valve",
	"yolo.dialog.words",
	"yolo.dialog.go",
	// The report (templates/report.html, /p/<project>/status): L1's Report,
	// and the Board | Report toggle on L1 (templates/_workshop_report.html).
	"view.*",
	"report.page",
	"report.title",
	"report.badge",
	"report.copy_md",
	"report.raw_md",
	"report.classic",
	"report.action.*",
	"report.card.*",
	"report.bottleneck",
	"report.where",
	"report.where.locked",
	"report.shipped",
	"report.shipped.row",
	"report.health",
	"report.unlock",
	"report.unlock.*",
	// The CEO chat (/p/<project>/lead, templates/_workshop_ceo.html) and
	// every CEO (/leads).
	"lead_chat.page",
	"lead_chat.session",
	"lead_chat.state",
	"lead_chat.not_live",
	"lead_chat.spawn",
	"lead_chat.pane",
	"lead_chat.form",
	"lead_chat.text",
	"lead_chat.attach_image", "lead_chat.image_input", "lead_chat.images", "lead_chat.image", "lead_chat.image.remove", "lead_chat.turn.images",
	"lead_chat.send", "lead_chat.send_now",
	"lead_chat.key.*",
	"lead_chat.status",
	"lead_chat.waiting",
	"lead_chat.waiting.count",
	"lead_chat.waiting.*.*",
	"lead_chat.questions",
	"lead_chat.question.answer",
	"lead_chat.mode",
	"lead_chat.mode_switch",
	"lead_chat.view.*",
	"lead_chat.open_tmux",
	"lead_chat.conversation",
	"lead_chat.older",
	"lead_chat.empty",
	"lead_chat.turn.you",
	"lead_chat.turn.ceo",
	"lead_chat.tools",
	"lead_chat.working",
	"lead_chat.interrupt",
	"lead_chat.prompt",
	"lead_chat.prompt.choice.#",
	"lead_chat.workers",
	"lead_chat.worker",
	"lead_chat.attach",
	"leads.page",
	"leads.row.*",
	"leads.row.*.live",
	"leads.row.*.chat",
	// Health (/p/<project>/health): scores, components, the review, trends.
	"health.page",
	"health.range",
	"health.range.*",
	"health.flash",
	"health.lint",
	"health.lint.count",
	"health.lint.error",
	"health.lint.list",
	"health.lint.row",
	"health.lint.more",
	"health.lint.cmd",
	"health.derived",
	"health.derived.value",
	"health.derived.empty",
	"health.score_gauge",
	"health.components",
	"health.warnings",
	"health.component.*",
	"health.opinion",
	"health.opinion.value",
	"health.opinion.empty",
	"health.opinion.stale",
	"health.run_review",
	"health.recommendation",
	"health.file_recommendation",
	"health.dismiss_recommendation",
	"health.chart.*",
	"health.chart.empty",
	"health.chart_table",
	"health.list",
	// The hall's Health page (templates/health_wb.html, Workshop Board L2);
	// it reuses the ids above where they mean the same thing.
	"hall.tabs",
	"hall.tab.*",
	"health.classic",
	"health.score",
	"health.score.empty",
	"health.score.value",
	"health.score.bar",
	"health.score.hint",
	"health.give_agent",
	"health.opinion.session",
	"health.opinion.tmux",
	"health.sensors",
	"health.sensor_group.*",
	"health.sensor.*",
	"health.look",
	"health.look.refactor",
	"health.trends",
	"health.trend.*",
	"health.setup",
	// The hall's Retro page (templates/retro_wb.html, Workshop Board L2):
	// retros to sort, the selected one's findings and their bins.
	"retro.page",
	"retro.classic",
	"retro.presort",
	"retro.presort.agent.*",
	"retro.presort.run",
	"retro.loop",
	"retro.counts",
	"retro.list.*",
	"retro.list.more",
	"retro.item",
	"retro.head",
	"retro.state",
	"retro.issue",
	"retro.progress",
	"retro.close",
	"retro.reopen",
	"retro.note",
	"retro.unsorted",
	"retro.empty",
	"retro.finding.*",
	"retro.finding.done",
	"retro.finding.send",
	"retro.bin.*",
	"retro.pattern",

	"stats.dashboard",
	"stats.options",
	"stats.range",
	"stats.range.*",
	"stats.group",
	"stats.gauge",
	"stats.chart.*",
	"stats.card.*",
	"stats.snapshots",
	"stats.done",
	"stats.table.*",
	"stats.table_btn.*",
	"stats.pending",
	"stats.drilldown",
	"stats.backfill_hint",
	"stats.tables",
	"stats.overview",
	"stats.overview.row",
	"stats.overview.privacy",
	"stats.overview.privacy.*",
	"stats.overview.none",
	"stats.time",
	"stats.time.unsplit",
	"stats.time.unread",
	"stats.time.types",
	"stats.time.status",
	"stats.time.approvals",
	"stats.time.failures",
	"decisions.stat.total",
	"decisions.stat.active",
	"decisions.stat.replaced",
	"decisions.stat.stale",
	"decisions.empty",
	"decisions.filters",
	"decisions.filter.system.*",
	"decisions.filter.state.*",
	"decisions.nomatch",
	"decisions.nomatch.clear",
	"decisions.table",
	"decisions.row.*.*",
	"decisions.row.*.*.copy",
	"decisions.row.*.*.text",
	"decisions.row.*.*.how",
	"decisions.row.*.*.cited",
	"decisions.row.*.*.state",
	"decisions.toast",
	"decisions.new_view",
	// Decisions log (Workshop Board L2, templates/decisions_wb.html and
	// _workshop_decisions.html); the old table stays at ?classic=1.
	"decisions.classic",
	"decisions.count",
	"decisions.log",
	"decisions.day",
	"decisions.thread.*",
	"decisions.row.*.*.quote",
	"decisions.row.*.*.confirm",
	"decisions.row.*.*.question",
	"decisions.row.*.*.yolo",
	"decisions.hygiene",
	"decisions.start_ceo",
	"summary.*",
	"search.*",
	"filter.*",
	"bars.*",
	"command.*",

	// Metrics view (templates/metrics.html): guard metrics, knobs, branches.
	"balance.group.*.guard",
	"metrics.page",
	"metrics.counts",
	"metrics.tab.metrics",
	"metrics.tab.knobs",
	"metrics.tab.branches",
	"metrics.git_problem",
	"metrics.problems",
	"metrics.run.master",
	"metrics.run.since",
	"metrics.run.branches",
	"metrics.filter.system",
	"metrics.filter.offonly",
	"metrics.table",
	"metrics.row.*",
	"metrics.row.*.branch",
	"metrics.row.*.verdict",
	"knobs.filter.edited",
	"knobs.row.*",
	"knobs.row.*.history",
	"knobs.row.*.edits",
	"branches.row.*",
	"branches.idle",
	"branches.filter.hot3",
	"branches.filter.hot2",
	"branches.hide_shared",
	"branches.hot.*",
	// System-page panels (templates/_metrics_panels.html).
	"panel.guard_metrics",
	"panel.guard_metrics.row.*",
	"panel.knobs",
	"panel.knobs.row.*",
	// Issue detail: dependency and shared-file rows.
	"detail.field.depends_on",
	"detail.field.depended_on_by",
	"detail.field.decided_by",
	"detail.field.shared_files",

	// Balance board (/p/<project>/balance). Groups are keyed by metric,
	// cards by metric and source slug, passes by number.
	"balance.stats",
	"balance.system",
	"balance.stat.notes",
	"balance.stat.open",
	"balance.stat.metrics",
	"balance.stat.unsorted",
	"balance.off",
	"balance.passes",
	"balance.pass.#",
	"balance.passes.planned",
	"balance.start",
	"balance.filter.*",
	"balance.empty",
	"balance.nomatch",
	"balance.group.*",
	"balance.group.*.count",
	"balance.card.*.*",
	"balance.card.*.*.state",
	"balance.card.*.*.foot",
	"balance.card.*.*.history",
	"balance.unsorted",
	"balance.unsorted.row.#",
	"balance.unsorted.row.#.more",
	"balance.toast",
	// Project page frame (templates/page.html): /status with a status_page
	// and /pages/<n>.
	"page.view",
	"page.frame",
	"page.source",
	"page.source.age",
	"page.source.reloaded",
	"page.raw_link",
	"page.missing",
	"page.missing.builtin_link",
}

// FixtureIDs lists the ids that playtest scenario pages carry: project
// pages (playtest/scenarios/*/root/**/*.html) whose scripts a session
// checks by opening the raw page. TestTemplateTestIDs checks them against
// those files instead of the templates.
var FixtureIDs = []string{
	"fixture.page.title",
	"fixture.page.script_ran",
	"fixture.page.post_result",
	"fixture.page.status_open",
}

// Segment turns a free-form value (a status, a slug) into one id segment:
// lower case, with spaces and dots replaced by `_`. Templates call it as
// `tid`.
func Segment(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.NewReplacer(" ", "_", ".", "_").Replace(s)
}

// segRe matches one concrete segment.
var segRe = `[^.]+`

// Regexp compiles an id or pattern (`*` any one segment, `#` digits) into an
// anchored regexp that matches concrete ids.
func Regexp(pattern string) *regexp.Regexp {
	parts := strings.Split(pattern, ".")
	for i, p := range parts {
		switch p {
		case "*":
			parts[i] = segRe
		case "#":
			parts[i] = `[0-9]+`
		default:
			parts[i] = regexp.QuoteMeta(p)
		}
	}
	return regexp.MustCompile(`^` + strings.Join(parts, `\.`) + `$`)
}

// Compatible reports whether two ids or patterns can name the same element:
// same segment count, and each segment pair is equal or one side is a
// wildcard (`*` matches anything, `#` matches digits or a wildcard).
func Compatible(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	if len(as) != len(bs) {
		return false
	}
	for i := range as {
		if !segCompatible(as[i], bs[i]) {
			return false
		}
	}
	return true
}

func segCompatible(x, y string) bool {
	if x == y || x == "*" || y == "*" {
		return true
	}
	if x == "#" {
		return y == "#" || isDigits(y)
	}
	if y == "#" {
		return isDigits(x)
	}
	return false
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func allIDs() []string { return append(append([]string(nil), IDs...), FixtureIDs...) }

// Known reports whether id (concrete or a pattern) matches an entry in IDs.
func Known(id string) bool {
	for _, e := range allIDs() {
		if Compatible(e, id) {
			return true
		}
	}
	return false
}

// Suggest returns the listed entry closest to id (by edit distance), for
// "did you mean" hints. An entry's wildcard segments take id's segment at
// the same place, so `board.colum.backlog` is close to `board.column.*`.
// Returns "" when nothing is reasonably close.
func Suggest(id string) string {
	best, bestD := "", len(id)/2+2
	idSegs := strings.Split(id, ".")
	for _, e := range allIDs() {
		filled := e
		if segs := strings.Split(e, "."); len(segs) == len(idSegs) {
			for i, s := range segs {
				if s == "*" || s == "#" {
					segs[i] = idSegs[i]
				}
			}
			filled = strings.Join(segs, ".")
		}
		if d := distance(id, filled); d < bestD {
			best, bestD = e, d
		}
	}
	return best
}

func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// templateAttrRe finds data-testid="…" attributes; JS assignments of the
// form `.dataset.testid = '…'` are found by datasetRe.
var (
	templateAttrRe = regexp.MustCompile(`data-testid="([^"]*)"`)
	datasetRe      = regexp.MustCompile(`dataset\.testid\s*=\s*'([^']*)'(\s*\+)?`)
	goActionRe     = regexp.MustCompile(`\{\{[^}]*\}\}`)
	jsConcatRe     = regexp.MustCompile(`'\s*\+[^+]*\+\s*'`)
)

// ScanTemplate returns the ids a template source declares, with every
// dynamic part (a Go template action, a JS string concatenation) turned
// into `*`.
func ScanTemplate(src string) []string {
	var out []string
	for _, m := range templateAttrRe.FindAllStringSubmatch(src, -1) {
		id := goActionRe.ReplaceAllString(m[1], "*")
		id = jsConcatRe.ReplaceAllString(id, "*")
		out = append(out, id)
	}
	for _, m := range datasetRe.FindAllStringSubmatch(src, -1) {
		id := m[1]
		if m[2] != "" {
			id += "*"
		}
		out = append(out, id)
	}
	return out
}
