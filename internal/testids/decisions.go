package testids

func init() {
	IDs = append(IDs,
		// Decisions index (/p/<project>/decisions). Rows are keyed by the
		// recording issue's slug and the decision id ("d-1").
		"decisions.stats",
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
		"decisions.row.*.*.words",
		"decisions.row.*.*.question",
		"decisions.row.*.*.yolo",
		"decisions.hygiene",
		"decisions.start_ceo",
	)
}
