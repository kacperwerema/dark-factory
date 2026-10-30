package testids

func init() {
	IDs = append(IDs,
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

		"workboard.needs_you",
	)
}
