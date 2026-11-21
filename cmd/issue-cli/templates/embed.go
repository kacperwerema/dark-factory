// Package templates holds the files issue-cli bundles for workflow.yaml: the
// templates `workflow init` copies (workflow/*.yaml), the feature catalog
// `workflow scan` and `workflow upgrade` read (workflow-features/<id>.yaml,
// one entry per file), the department templates `department add` installs
// (departments/<name>/, the lines they share in departments/_shared), the
// job briefs a jobs: entry names as brief: builtin:<name> (jobs/), and the
// partners catalog (partners.yaml, read by internal/partner) and the reel
// kit's starter Remotion template (reel/, copied by issue-cli reel init).
// It is a package of its own so the viewer reads the same files (the Updates
// tab).
package templates

import "embed"

//go:embed workflow/*.yaml workflow-features departments/*/* jobs/*.md partners.yaml reel reel/.gitignore
var FS embed.FS
