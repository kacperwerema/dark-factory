package validations

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func init() { register("file_exists", FileExists) }

// FileExists passes when action.Path, relative to the project root, is a
// regular file with something in it besides whitespace. The path is
// templated with {{slug}}, {{number}}, {{repo}}, {{system}} and
// {{department}} (the issue's department key). It runs no shell, so unlike
// command_succeeds it needs no allow_shell. A path that leaves the root
// (absolute, or .. out of it) is a config error, not a pass.
func FileExists(action Action, issue *IssueView, cfg Config) error {
	raw := strings.TrimSpace(action.Path)
	if raw == "" {
		return fmt.Errorf("file_exists requires 'path'")
	}
	if strings.Contains(raw, "{{department}}") && issue.Department == "" {
		return fail(action, issue,
			fmt.Sprintf("file_exists: %s names the department, but %s belongs to no department", raw, issue.Slug),
			"")
	}
	rel, err := renderTemplate(raw, issue)
	if err != nil {
		return fmt.Errorf("file_exists: template error: %w", err)
	}
	rel = filepath.Clean(filepath.FromSlash(strings.TrimSpace(rel)))
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("file_exists: path %q must stay inside the project root", raw)
	}
	if cfg.ProjectRoot == "" {
		return fmt.Errorf("file_exists: no project root to look for %s in", filepath.ToSlash(rel))
	}
	abs := filepath.Join(cfg.ProjectRoot, rel)
	shown := filepath.ToSlash(rel)
	info, err := os.Stat(abs)
	switch {
	case os.IsNotExist(err):
		return fail(action, issue, fmt.Sprintf("%s is missing", shown), "")
	case err != nil:
		return fail(action, issue, fmt.Sprintf("can't read %s: %v", shown, err), "")
	case !info.Mode().IsRegular():
		return fail(action, issue, fmt.Sprintf("%s is not a file", shown), "")
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return fail(action, issue, fmt.Sprintf("can't read %s: %v", shown, err), "")
	}
	if strings.TrimSpace(string(data)) == "" {
		return fail(action, issue, fmt.Sprintf("%s is empty", shown), "")
	}
	return nil
}
