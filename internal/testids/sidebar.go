package testids

func init() {
	IDs = append(IDs,
		// Project sidebar (templates/_sidebar.html), on every project page.
		"nav.sidebar",
		"nav.project",
		"nav.create",
		"nav.tab.*",
		"nav.tab.status.count",
		"nav.tab.updates.count",
		"nav.more",
		"nav.agents_running",
		"nav.autostart",
		"nav.yolo",
		"nav.yolo.extend",
		"nav.yolo.off",
		"nav.yolo.queue",
		"nav.yolo.row.*",
		"nav.panel",
		"nav.panel.system.*",
		"nav.panel.legend",
		"nav.panel.family.*",
		"nav.panel.row.*",
		"nav.page.#",
		"nav.actions",
		"nav.theme.*",
		"header.count",
	)
}
