package testids

func init() {
	IDs = append(IDs,
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
	)
}
