package testids

func init() {
	IDs = append(IDs,
		// List (/p/<project>/).
		"list.filters",
		"list.search",
		"list.filter.status",
		"list.filter.system",
		"list.filter.priority",
		"list.filter.assignee",
		"list.filter.type",
		"list.filter.label",
		"list.clear",
		"list.row.*",
		"list.row.*.title",
		"list.row.*.status",
		"list.empty",
	)
}
