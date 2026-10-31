package testids

func init() {
	IDs = append(IDs,
		// Crate store (Workshop Board L2, the hall's Issues tab:
		// templates/issues_wb.html and _workshop_issues.html); the classic list
		// stays at ?classic=1.
		"issues.page",
		"issues.classic",
		"issues.count",
		"issues.shelves",
		"issues.empty",
		"issues.nomatch",
		"issues.nomatch.clear",
		"issues.new",
		"issues.create",
		"issues.create.title",
		"issues.create.system",
		"issues.create.status",
		"issues.create.type",
		"issues.create.priority",
		"issues.create.labels",
		"issues.create.body",
		"issues.create.error",
		"issues.create.cancel",
		"issues.create.submit",
		"issues.shelf.label",
		"issues.shelf.*",
		"issues.shelf.*.more",
		"issues.crate.*",
		"issues.crate.*.title",
		"issues.crate.*.you",
		"create.modal",
	)
}
