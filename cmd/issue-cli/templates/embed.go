// Package templates holds the files issue-cli bundles for workflow.yaml: the
// templates `workflow init` copies (workflow/*.yaml) and the feature catalog
// `workflow scan` and `workflow upgrade` read (workflow-features.yaml and
// workflow-features/<id>.yaml), and
// the department templates `department add` installs (departments/<name>/).
// It is a package of its own so the viewer reads the same files (the Updates
// tab).
package templates

import "embed"

//go:embed workflow/*.yaml workflow-features.yaml workflow-features departments/*/*
var FS embed.FS
