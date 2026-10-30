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
	)
}
