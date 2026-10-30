package testids

func init() {
	IDs = append(IDs,
		// Balance board (/p/<project>/balance). Groups are keyed by metric,
		// cards by metric and source slug, passes by number.
		"balance.group.*.guard",
		"balance.stats",
		"balance.system",
		"balance.stat.notes",
		"balance.stat.open",
		"balance.stat.metrics",
		"balance.stat.unsorted",
		"balance.off",
		"balance.passes",
		"balance.pass.#",
		"balance.passes.planned",
		"balance.start",
		"balance.filter.*",
		"balance.empty",
		"balance.nomatch",
		"balance.group.*",
		"balance.group.*.count",
		"balance.card.*.*",
		"balance.card.*.*.state",
		"balance.card.*.*.foot",
		"balance.card.*.*.history",
		"balance.unsorted",
		"balance.unsorted.row.#",
		"balance.unsorted.row.#.more",
		"balance.toast",
	)
}
