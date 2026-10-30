package testids

func init() {
	IDs = append(IDs,
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
	)
}
