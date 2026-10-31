package testids

func init() {
	IDs = append(IDs,
		// The post-mortem page (templates/postmortem.html,
		// /p/<project>/postmortem/<slug>).
		"postmortem.page",
		"postmortem.count",
		"postmortem.empty",
		"postmortem.sections",
		"postmortem.section.*",
		"postmortem.actions",
		"postmortem.none",
		// One action (by id: a1, a2 …).
		"postmortem.action.*",
		"postmortem.action.*.place",
		"postmortem.action.*.outcome",
		"postmortem.action.*.answer",
		"postmortem.action.*.msg",
		"postmortem.submit",
		"postmortem.status",
	)
}
