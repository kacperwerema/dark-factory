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
		"postmortems.rows",
		"postmortems.group.*",
		"postmortems.group.*.count",
		"postmortems.group.*.title",
		"postmortems.group.*.from",
		"postmortems.copies.*",
		"postmortems.row.*.*",
		"postmortems.row.*.*.msg",
		"postmortems.row.fix",
		"postmortems.foot.*",
		"postmortems.note.*",
		"postmortems.agree.*",
		"postmortems.refuse.*",
		"postmortems.all.*",
		"postmortems.sha.*",
		"postmortems.overtaken.*",
		// Fixed already: what needs no answer.
		"postmortems.fixed",
		"postmortems.fixed.link",
		"postmortems.fixed.count",
		"postmortems.fixed.row.*.*",
		"postmortems.fixed.row.*.*.by",
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
