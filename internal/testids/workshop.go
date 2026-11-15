package testids

func init() {
	IDs = append(IDs,
		// Workshop Board components (templates/_workshop.html), on every level.
		"chip.*",
		"tile.*",
		"plan.*",

		"seg.*",
		"seg.*.*",

		"summary.*",
		"search.*",
		"search.*.input",
		"filter.*",
		"bars.*",
		"command.*",
		// A Workshop Board page's own title (its h1), where the page names no
		// other: the phone checks put it in the first half of the screen.
		"wb.title",
	)
}
