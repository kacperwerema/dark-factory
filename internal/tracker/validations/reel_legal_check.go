package validations

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/reellegal"
)

func init() { register("reel_legal_check", ReelLegalCheck) }

// DefaultReelLegalRules is the rules file a reel_legal_check action reads
// when it names no path: the department's own copy.
const DefaultReelLegalRules = "departments/{{department}}/reel-legal.yaml"

// ReelLegalCheck holds a reel whose script breaks the legal floor
// (internal/reellegal): it reads the issue's '## <action.section>' (Script
// by default), the '## Legal check' declarations, the storyboard JSON they
// name (storyboard: <path>, when it is there) and the rules file at
// action.path (departments/{{department}}/reel-legal.yaml by default,
// relative to the project root). It runs no shell. A missing rules file
// holds the reel: a check that can't read its rules never passes.
func ReelLegalCheck(action Action, issue *IssueView, cfg Config) error {
	findings, err := ReelLegalFindings(action, issue, cfg)
	if err != nil {
		return fail(action, issue, "reel legal check: "+err.Error(),
			"restore it: issue-cli department update "+deptOr(issue)+" writes the bundled copy when yours is gone")
	}
	if len(findings) == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "reel legal check holds %s (%d finding(s)):", issue.Slug, len(findings))
	for _, f := range findings {
		b.WriteString("\n  - " + f.String())
	}
	return fail(action, issue, b.String(),
		"fix each under ## Script or ## "+reellegal.DeclarationsSection+", then check again: issue-cli reel-legal "+issue.Slug)
}

func deptOr(issue *IssueView) string {
	if issue.Department != "" {
		return issue.Department
	}
	return "video"
}

// ReelLegalFindings runs the check and returns its findings; an error is a
// rules file or section it can't read.
func ReelLegalFindings(action Action, issue *IssueView, cfg Config) ([]reellegal.Finding, error) {
	rulesPath := strings.TrimSpace(action.Path)
	if rulesPath == "" {
		rulesPath = DefaultReelLegalRules
	}
	if strings.Contains(rulesPath, "{{department}}") && issue.Department == "" {
		return nil, fmt.Errorf("%s names the department, but %s belongs to no department", rulesPath, issue.Slug)
	}
	rel, err := renderTemplate(rulesPath, issue)
	if err != nil {
		return nil, fmt.Errorf("template error in %q: %w", rulesPath, err)
	}
	abs, ok := projectFile(cfg, strings.TrimSpace(rel))
	if !ok {
		return nil, fmt.Errorf("rules file %s must be a path inside the project", rel)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("can't read the rules file %s", rel)
	}
	rules, err := reellegal.ParseRules(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", rel, err)
	}

	section := strings.TrimSpace(action.Section)
	if section == "" {
		section = "Script"
	}
	script, ok := findSectionContent(issue.BodyRaw, section)
	if !ok || strings.TrimSpace(script) == "" {
		return []reellegal.Finding{{Rule: "script", Problem: "no ## " + section + " section with the script",
			Fix: "write every spoken line and on-screen caption under ## " + section}}, nil
	}
	declText, _ := findSectionContent(issue.BodyRaw, reellegal.DeclarationsSection)
	decl := reellegal.ParseDeclarations(declText)

	fileOK := func(p string) bool {
		abs, ok := projectFile(cfg, p)
		if !ok {
			return false
		}
		info, err := os.Stat(abs)
		return err == nil && info.Mode().IsRegular() && info.Size() > 0
	}
	if sb := decl.Get("storyboard"); sb != "" {
		if abs, ok := projectFile(cfg, sb); ok {
			if data, err := os.ReadFile(abs); err == nil {
				text, err := reellegal.StoryboardText(data)
				if err != nil {
					return []reellegal.Finding{{Rule: "storyboard", Problem: fmt.Sprintf("the storyboard %s is not JSON: %v", sb, err),
						Fix: "fix it (issue-cli reel validate " + sb + ")"}}, nil
				}
				script += "\n" + text
			}
		}
	}
	return reellegal.Check(reellegal.Input{Script: script, Decl: decl, Rules: rules, FileOK: fileOK}), nil
}

// projectFile resolves p against the project root, or the issues folder
// for a path starting with <issues>/; ok=false when it leaves them.
func projectFile(cfg Config, p string) (string, bool) {
	base := cfg.ProjectRoot
	if rest, ok := strings.CutPrefix(p, IssuesPrefix); ok {
		p, base = rest, cfg.IssuesRoot
	}
	rel := filepath.Clean(filepath.FromSlash(p))
	if base == "" || filepath.IsAbs(rel) || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.Join(base, rel), true
}
