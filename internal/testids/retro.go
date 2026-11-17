package testids

func init() {
	IDs = append(IDs,
		// The hall's Retro page (templates/retro_wb.html, Workshop Board L2):
		// retros to sort, the selected one's findings and their bins.
		"retro.page",
		"retro.classic",
		"retro.classic.pager",
		"retro.presort",
		"retro.presort.agent.*",
		"retro.presort.run",
		"retro.loop",
		"retro.counts",
		"retro.list.*",
		"retro.list.more",
		// The show-all view's pager under each list.
		"retro.list.range",
		"retro.list.newer",
		"retro.list.older",
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
		// The Bugs view (?view=bugs); a card is keyed by the report's file.
		"retro.bugs",
		"retro.bugs.open",
		"retro.bugs.closed",
		"retro.bugs.empty",
		"retro.bug.*",
		"retro.bug.*.status",
		"retro.bug.*.open",
		"retro.bug.*.fixed",
		"retro.bug.*.wontfix",
		"retro.pattern.experiment",
	)
}
