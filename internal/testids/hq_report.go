package testids

func init() {
	IDs = append(IDs,
		// The HQ report (templates/hq_report.html, /p/<project>/hq/report).
		"hq_report.page",
		"hq_report.expand",
		"hq_report.md",
		"hq_report.list",
		"hq_report.no_departments",
		// One box (by box key): its fold, its headline, its empty line and
		// its detail rows (by row key: goals, outputs, spend…).
		"hq_report.box.*",
		"hq_report.headline.*",
		"hq_report.empty.*",
		"hq_report.row.*.*",
	)
}
