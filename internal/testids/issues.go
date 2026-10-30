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
		"issues.shelf.label",
		"issues.shelf.*",
		"issues.shelf.*.more",
		"issues.crate.*",
		"issues.crate.*.title",
		"issues.crate.*.you",
		"create.modal",
	)
}
