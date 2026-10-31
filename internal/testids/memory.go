package testids

func init() {
	IDs = append(IDs,
		// The Memory page (templates/memory_wb.html, /p/<project>/memory).
		"memory.page",
		"memory.flash",
		"memory.error",
		"memory.filters",
		// One filter group by name (scope, kind, state, pinned).
		"memory.filter.*",
		"memory.recent",
		// The entity index (who knows what), one entity by key
		// (topic-<name>, agent-<name>), the ?entity= filter line, and a
		// group under ?group=entity (key, or "none").
		"memory.entities",
		"memory.entity.*",
		"memory.entity_filter",
		"memory.group.*",
		"memory.list",
		"memory.count",
		"memory.empty",
		// One memory by id: state, pin suggestion, source, text, actions, history.
		"memory.row.*",
		"memory.row.*.state",
		"memory.row.*.suggested",
		"memory.row.*.source",
		"memory.row.*.text",
		"memory.row.*.save",
		"memory.row.*.pin",
		"memory.row.*.unpin",
		"memory.row.*.delete",
		"memory.row.*.history",
	)
}
