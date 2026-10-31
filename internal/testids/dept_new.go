package testids

func init() {
	IDs = append(IDs,
		// Adding a department (templates/dept_new.html, /p/<project>/departments/new).
		"dept_new.page",
		"dept_new.back",
		"dept_new.plan",
		"dept_new.file.*",
		"dept_new.entry",
		"dept_new.child.*",
		"dept_new.reparent",
		"dept_new.add",
		"dept_new.error",
	)
}
