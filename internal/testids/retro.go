package testids

func init() {
	IDs = append(IDs,
		// The hall's Retro page (templates/retro_wb.html, Workshop Board L2):
		// retros to sort, the selected one's findings and their bins.
		"retro.page",
		"retro.classic",
		"retro.presort",
		"retro.presort.agent.*",
		"retro.presort.run",
		"retro.loop",
		"retro.counts",
		"retro.list.*",
		"retro.list.more",
		"retro.item",
		"retro.head",
		"retro.state",
		"retro.issue",
		"retro.progress",
		"retro.close",
		"retro.reopen",
		"retro.note",
		"retro.unsorted",
		"retro.empty",
		"retro.finding.*",
		"retro.finding.done",
		"retro.finding.send",
		"retro.bin.*",
		"retro.pattern",
		"retro.pattern.experiment",
	)
}
