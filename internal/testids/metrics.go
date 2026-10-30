package testids

func init() {
	IDs = append(IDs,
		// Metrics view (templates/metrics.html): guard metrics, knobs, branches.
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
	)
}
