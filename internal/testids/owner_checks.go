package testids

func init() {
	IDs = append(IDs,
		// The Owner checks page (templates/owner_checks.html,
		// /p/<project>/owner-checks?epic=<slug>).
		"owner_checks.page",
		"owner_checks.count",
		"owner_checks.pick",
		"owner_checks.epic",
		// One issue (by slug) and its boxes (by slug and box id).
		"owner_checks.issue.*",
		"owner_checks.issue.*.evidence",
		"owner_checks.box.*.*",
		"owner_checks.box.*.*.tick",
		"owner_checks.box.*.*.why",
		"owner_checks.box.*.*.msg",
		"owner_checks.box.*.*.shot",
		"owner_checks.box.*.*.noshot",
		"owner_checks.empty",
	)
}
