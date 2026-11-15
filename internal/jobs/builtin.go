package jobs

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/cmd/issue-cli/templates"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// BuiltinPrefix marks a brief bundled with the engine: brief:
// builtin:memory-consolidate reads cmd/issue-cli/templates/jobs/
// memory-consolidate.md from the binary, so the project needs no file and
// the brief updates with the engine.
const BuiltinPrefix = "builtin:"

// Builtins are the bundled briefs' names, sorted.
func Builtins() []string {
	entries, _ := fs.ReadDir(templates.FS, "jobs")
	var out []string
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".md"); ok && !e.IsDir() {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// builtinTypes are the bundled briefs whose runs need one type of work: a
// growth-scout run is research that ends on the shelf, never a piece that
// walks to publish.
var builtinTypes = map[string]string{
	"growth-scout": "report",
	// A flush ends by itself: on the report path each night's run would
	// wait on the owner.
	"lead-flush": tracker.ChoreType,
}

// LeadFlushBrief is the bundled brief of the nightly lead flush.
const LeadFlushBrief = BuiltinPrefix + "lead-flush"

// BuiltinType is the type of work the bundled brief named by brief needs
// its runs to have; empty for a file brief or a bundled one that needs none.
func BuiltinType(brief string) string {
	name, ok := strings.CutPrefix(strings.TrimSpace(brief), BuiltinPrefix)
	if !ok {
		return ""
	}
	return builtinTypes[strings.TrimSpace(name)]
}

// IsBuiltin reports a brief: value naming a bundled brief.
func IsBuiltin(brief string) bool {
	return strings.HasPrefix(strings.TrimSpace(brief), BuiltinPrefix)
}

// BriefText is the raw text of job j's brief: a bundled one for
// builtin:<name>, else the file under the project root. The error names
// what is missing.
func BriefText(p *tracker.Project, j tracker.Job) (string, error) {
	brief := strings.TrimSpace(j.Brief)
	if brief == "" {
		return "", fmt.Errorf("no brief: (the markdown file each run starts from)")
	}
	if name, ok := strings.CutPrefix(brief, BuiltinPrefix); ok {
		name = strings.TrimSpace(name)
		var data []byte
		err := fmt.Errorf("bad name")
		if name != "" && !strings.ContainsAny(name, "/\\.") {
			data, err = fs.ReadFile(templates.FS, path.Join("jobs", name+".md"))
		}
		if err != nil {
			return "", fmt.Errorf("brief %s is not a bundled brief (bundled: %s)", brief, strings.Join(withPrefix(Builtins()), ", "))
		}
		return string(data), nil
	}
	data, err := os.ReadFile(filepath.Join(p.Root(), filepath.FromSlash(brief)))
	if err != nil {
		return "", fmt.Errorf("brief %s: %w", brief, err)
	}
	return string(data), nil
}

func withPrefix(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = BuiltinPrefix + n
	}
	return out
}
