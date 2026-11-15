package testids

func init() {
	IDs = append(IDs,
		// The post-mortem queue (templates/postmortems.html,
		// /p/<project>/postmortems).
		"postmortems.page",
		"postmortems.count",
		"postmortems.empty",
		"postmortems.bar",
		"postmortems.joined",
		"postmortems.place.*",
		"postmortems.warn",
		"postmortems.group.*",
		"postmortems.row.*.*",
		"postmortems.row.*.*.msg",
		"postmortems.kept",
		"postmortems.foot.*",
		"postmortems.note.*",
		"postmortems.refuse.*",
		"postmortems.all.*",
		"postmortems.sha.*",
		"postmortems.overtaken.*",
		// Marked lately: what Undo can reopen.
		"postmortems.marked",
		"postmortems.marked.count",
		"postmortems.marked.row.*.*",
		"postmortems.marked.row.*.*.mark",
		"postmortems.marked.row.*.*.tick",
		"postmortems.marked.row.*.*.msg",
		"postmortems.marked.all",
		"postmortems.undo",
		// The ways into it.
		"needs_you.postmortems",
		"postmortem.queue",
	)
}
