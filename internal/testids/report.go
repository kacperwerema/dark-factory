package testids

func init() {
	IDs = append(IDs,
		// The report (templates/report.html, /p/<project>/status): L1's Report,
		// and the Board | Report toggle on L1 (templates/_workshop_report.html).
		"report.waiting.link",
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
		"report.bench",
		"report.bench.row.*",
		"report.bench.empty",
		"report.bench.pass",
		"report.bench.stop",
		"report.bench.delta",
	)
}
