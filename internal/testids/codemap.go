package testids

func init() {
	IDs = append(IDs,
		// The code map page (templates/codemap.html, /p/<project>/codemap).
		"codemap.page",
		"codemap.plan",
		"codemap.lane",
		"codemap.context.*",
		"codemap.chip.*",
		"codemap.footprint",
		"codemap.detail",
		"codemap.clear",
		"codemap.matrix",
		"codemap.upward",
		"codemap.unmapped",
		"codemap.empty",
		"codemap.error",
	)
}
