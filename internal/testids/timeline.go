package testids

func init() {
	IDs = append(IDs,
		// Timeline tab (/p/<project>/issue/<slug>?tab=timeline, templates/_timeline.html).
		"timeline.tab",
		"timeline.missing",
		"timeline.total",
		"timeline.working",
		"timeline.waiting",
		"timeline.idle",
		"timeline.rework",
		"timeline.commits",
		"timeline.bar",
		"timeline.filter.*",
		"timeline.band",
		"timeline.band.working",
		"timeline.wait",
		"timeline.idlegap",
		"timeline.reworkloop",
		"timeline.folded",
		"timeline.cli.call",
		"timeline.cli.actions",
		"timeline.approval.words",
		"timeline.side.part",
		"timeline.side.words",
		"timeline.side.open",
		"timeline.side.ci",
	)
}
