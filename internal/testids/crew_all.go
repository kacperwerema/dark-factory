package testids

func init() {
	IDs = append(IDs,
		// The Crew page across projects (templates/crew_all.html and
		// _workshop_crew_all.html, crew_all.go).
		"crew_all.page",
		"crew_all.live",
		"crew_all.machine",
		"crew_all.signin",
		"crew_all.log",
		"crew_all.project.*",
		"crew_all.project.*.autostart",
		"crew_all.project.*.queue",
		"crew_all.switch.*.*",
		"crew_all.row.*",
		"crew_all.row.*.state",
		"crew_all.row.*.status",
		"crew_all.row.*.start",
		"crew_all.row.*.tail",
		"crew_all.row.*.nudge",
		"crew_all.row.*.stop",
		"crew_all.confirm",
		"crew_all.confirm.yes",
		"crew_all.confirm.no",
		"crew_all.nudge",
		"crew_all.nudge.text",
		"crew_all.nudge.send",
		"crew_all.tail",
		"crew_all.tail.pane",
	)
}
