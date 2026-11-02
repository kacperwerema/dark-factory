// Package templates holds the files issue-cli bundles for workflow.yaml: the
// templates `workflow init` copies (workflow/*.yaml), the feature catalog
// `workflow scan` and `workflow upgrade` read (workflow-features/<id>.yaml,
// one entry per file), the department templates `department add` installs
// (departments/<name>/), and the job briefs a jobs: entry names as
// brief: builtin:<name> (jobs/).
// It is a package of its own so the viewer reads the same files (the Updates
// tab).
package templates

import "embed"

//go:embed workflow/*.yaml workflow-features departments/*/* jobs/*.md
var FS embed.FS
