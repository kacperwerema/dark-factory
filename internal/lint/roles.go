package lint

// checkRoles reports what a workflow file's status roles get wrong
// (tracker.RoleProblems): a rename the engine would otherwise lose silently.
func checkRoles(env *Env) []Finding {
	var out []Finding
	for _, p := range env.Workflow.RoleProblems() {
		out = append(out, Finding{Where: env.where(env.lineOf("statuses:")), Message: p[0], Fix: p[1]})
	}
	return out
}
