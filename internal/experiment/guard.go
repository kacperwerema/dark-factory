package experiment

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

// DefaultProtected are the paths an experiment branch may not touch when the
// owner's bench config names none: the benchmark, its scoring, its health
// family and this package's verdict rule. The loop can't change its own
// yardstick.
var DefaultProtected = []string{
	"internal/bench/**",
	"internal/experiment/**",
	"internal/health/bench*",
	"cmd/issue-cli/cmd_bench*",
	"cmd/issue-cli/cmd_experiment*",
}

// BenchDir is the owner's bench folder, outside every repo:
// $ISSUE_VIEWER_BENCH_DIR, else $XDG_CONFIG_HOME (~/.config)/ai-workflow-engine/bench.
// The same rule as internal/bench Dir.
func BenchDir() string {
	if d := strings.TrimSpace(os.Getenv("ISSUE_VIEWER_BENCH_DIR")); d != "" {
		return d
	}
	base := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "ai-workflow-engine", "bench")
}

// Protected is the protected list for benchmark: the `protected:` list of
// the owner's <bench dir>/<benchmark>.yaml when it has one, else
// DefaultProtected. Only read here; nothing in the loop writes it.
func Protected(benchDir, benchmark string) ([]string, error) {
	if benchDir == "" || benchmark == "" {
		return DefaultProtected, nil
	}
	data, err := os.ReadFile(filepath.Join(benchDir, benchmark+".yaml"))
	if errors.Is(err, os.ErrNotExist) {
		return DefaultProtected, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Protected []string `yaml:"protected"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", benchmark+".yaml", err)
	}
	if len(cfg.Protected) == 0 {
		return DefaultProtected, nil
	}
	return cfg.Protected, nil
}

// Problem is one reason the guard refuses a branch.
type Problem struct {
	Path string
	Why  string
}

func (p Problem) String() string { return p.Path + ": " + p.Why }

// Guard checks the files a branch changed against main: none may match a
// protected glob, and when change globs are set, each must match one.
func Guard(changed, protected, change []string) []Problem {
	var out []Problem
	for _, f := range changed {
		if g := matchAny(protected, f); g != "" {
			out = append(out, Problem{Path: f, Why: "protected (" + g + "): the experiment can't change its own yardstick"})
			continue
		}
		if len(change) > 0 && matchAny(change, f) == "" {
			out = append(out, Problem{Path: f, Why: "outside the experiment's change: " + strings.Join(change, ", ")})
		}
	}
	return out
}

// WorkRecords drops the files under the tracker's folders (issue files,
// sidecars, retros): the record of the work, not the setup an experiment
// changes. dirs are repo-relative slash paths.
func WorkRecords(changed, dirs []string) []string {
	var out []string
	for _, f := range changed {
		keep := true
		for _, d := range dirs {
			d = strings.Trim(d, "/")
			if d != "" && d != "." && (f == d || strings.HasPrefix(f, d+"/")) {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, f)
		}
	}
	return out
}

func matchAny(globs []string, f string) string {
	for _, g := range globs {
		if workflowupgrade.MatchSource(g, f) {
			return g
		}
	}
	return ""
}
