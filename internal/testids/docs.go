package testids

func init() {
	IDs = append(IDs,
		// A folder of the binder (wb/binder), folded unless it holds the doc on show.
		"docs.folder.*",
		// Docs (templates/docs_wb.html, /p/<project>/docs): the binder (L2).
		"docs.binder",
		"docs.item.*",
		"docs.reader",
		"docs.title",
		"docs.guard_rule",
		"docs.sections",
		"docs.body",
		"docs.empty",
		"docs.classic",
		"docs.guards",
		"docs.freshness",
		"docs.agents",
		"docs.system_decisions",
		"docs.edit",
		"docs.ask",
		"docs.start_ceo",
	)
}
