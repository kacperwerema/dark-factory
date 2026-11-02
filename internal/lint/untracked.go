package lint

import (
	"fmt"
	"slices"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/depttemplate"
)

// checkUntracked flags each department whose files (role prompt, workflow
// file, job briefs, departments/<key>/) git does not track: left after
// department add, they can be lost or slip into an unrelated commit.
// Ignored files are the owner's choice and not flagged; a project outside
// git has nothing to commit to.
func checkUntracked(env *Env) []Finding {
	p := env.Project
	root := p.Root()
	if root == "" || !depttemplate.InRepo(root) {
		return nil
	}
	var out []Finding
	for i := range p.Departments {
		d := &p.Departments[i]
		paths := depttemplate.DepartmentFiles(p, d)
		files, err := depttemplate.Untracked(root, paths)
		if err != nil {
			continue
		}
		// department update --side's comparison copies of the department's
		// own files are scratch: named apart, to delete rather than commit.
		side := map[string]bool{}
		for _, f := range paths {
			side[depttemplate.SidePath(f)] = true
		}
		var copies []string
		files = slices.DeleteFunc(files, func(f string) bool {
			if side[f] {
				copies = append(copies, f)
			}
			return side[f]
		})
		where := "projects.yaml (" + d.Name + ")"
		if len(files) > 0 {
			subject := "chore(departments): track the " + d.Name + " department's files"
			out = append(out, Finding{
				Where:   where,
				Message: fmt.Sprintf("%d department file(s) git does not track: %s", len(files), strings.Join(files, ", ")),
				Fix:     depttemplate.CommitCommand(root, files, subject),
			})
		}
		if len(copies) > 0 {
			out = append(out, Finding{
				Where:   where,
				Message: fmt.Sprintf("comparison copies from department update --side left in place: %s; delete them once compared, never commit them", strings.Join(copies, ", ")),
				Fix:     depttemplate.RemoveCommand(root, copies),
			})
		}
	}
	return out
}
