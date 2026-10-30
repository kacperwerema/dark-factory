// Package bench is the factory benchmark: an owner-kept list of golden tasks
// (done issues with a known good result, the merged commit) that a worker
// re-solves in a sandbox from the task's starting commit, so two setups of
// prompts, workflow.yaml and issue-cli can be compared on the same work.
//
// Everything lives in the owner's folder, outside every repo and worktree:
//
//	<config>/bench/<benchmark>.yaml                 the benchmark (Benchmark)
//	<config>/bench/tasks/<project>/<slug>/issue.md  the issue as it stood at the task's start
//	<config>/bench/runs/<benchmark>/<run>/run.json  the run (Run, holdout as passed n/m only)
//	<config>/bench/runs/<benchmark>/<run>/holdout.json  the holdout tasks' records
//
// <config> is $XDG_CONFIG_HOME/ai-workflow-engine, default
// ~/.config/ai-workflow-engine; $ISSUE_VIEWER_BENCH_DIR replaces
// <config>/bench. The holdout is kept out of every prompt and every output an
// agent reads (report, compare, run.json, the health family, the Report
// card); it is not hidden from a shell that reads holdout.json.
package bench

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Budget caps a run. Zero fields are no cap.
type Budget struct {
	// Tokens is the run's cap on input+output+cache tokens.
	Tokens int64 `yaml:"tokens,omitempty" json:"tokens,omitempty"`
	// USD is the run's cap on cost (from Prices).
	USD float64 `yaml:"usd,omitempty" json:"usd,omitempty"`
	// Wall is the run's cap on wall time ("6h").
	Wall string `yaml:"wall,omitempty" json:"wall,omitempty"`
	// TaskWall is each task's cap on the worker's wall time ("45m").
	TaskWall string `yaml:"task_wall,omitempty" json:"task_wall,omitempty"`
}

// WallDur is Wall parsed; 0 for none or a bad value.
func (b Budget) WallDur() time.Duration { return parseDur(b.Wall) }

// TaskWallDur is TaskWall parsed, default DefaultTaskWall.
func (b Budget) TaskWallDur() time.Duration {
	if d := parseDur(b.TaskWall); d > 0 {
		return d
	}
	return DefaultTaskWall
}

// DefaultTaskWall caps a task when the benchmark sets no task_wall.
const DefaultTaskWall = 45 * time.Minute

func parseDur(s string) time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil || d < 0 {
		return 0
	}
	return d
}

// Price is one model's price in USD per million tokens.
type Price struct {
	In         float64 `yaml:"in" json:"in"`
	Out        float64 `yaml:"out" json:"out"`
	CacheRead  float64 `yaml:"cache_read,omitempty" json:"cache_read,omitempty"`
	CacheWrite float64 `yaml:"cache_write,omitempty" json:"cache_write,omitempty"`
}

// Task is one golden task.
type Task struct {
	Project string `yaml:"project"`
	Slug    string `yaml:"slug"`
	// Type is the issue's type of work (feature, bugfix, tweak), for the
	// per-type rows.
	Type string `yaml:"type,omitempty"`
	// Base is the commit the worker starts from; Merged the known good
	// result.
	Base   string `yaml:"base"`
	Merged string `yaml:"merged"`
	// Start is the status the issue snapshot is in when the worker gets it.
	Start string `yaml:"start,omitempty"`
	// Hidden are the test files taken from Merged after the worker stops.
	Hidden []string `yaml:"hidden,omitempty"`
	// HiddenTests are the Go test functions declared in Hidden.
	HiddenTests []string `yaml:"hidden_tests,omitempty"`
	// Check is the shell command that must pass besides the hidden tests,
	// run in the task's checkout.
	Check   string `yaml:"check,omitempty"`
	Holdout bool   `yaml:"holdout,omitempty"`
	Added   string `yaml:"added,omitempty"`
}

// ID is the task's name in records: <project>/<slug>.
func (t Task) ID() string { return t.Project + "/" + t.Slug }

// Benchmark is <config>/bench/<name>.yaml.
type Benchmark struct {
	Name   string           `yaml:"name"`
	Budget Budget           `yaml:"budget,omitempty"`
	Prices map[string]Price `yaml:"prices,omitempty"`
	// Worker is the command a task's worker runs, the prompt on stdin, in
	// the task's checkout; default DefaultWorker.
	Worker string `yaml:"worker,omitempty"`
	// Judge is the command that scores a finished task 0-5, the judge
	// prompt on stdin; empty: no judge score.
	Judge string `yaml:"judge,omitempty"`
	Tasks []Task `yaml:"tasks"`

	path string
}

// DefaultWorker runs Claude Code headless with its JSON result (usage and
// cost) on stdout, without the tools that reach other sessions. It skips
// permission prompts: the sandbox has no remote and a scratch tracker, but
// the worker runs as the owner on this machine, which is why only the owner
// starts a run.
const DefaultWorker = "claude -p --output-format json --permission-mode bypassPermissions --disallowedTools SendMessage,ListAgents"

// Path is where the benchmark was loaded from or will be saved.
func (b *Benchmark) Path() string { return b.path }

// Task returns the task with id <project>/<slug>, or a bare slug when it is
// unique.
func (b *Benchmark) Task(id string) (int, *Task) {
	found := -1
	for i, t := range b.Tasks {
		if t.ID() == id {
			return i, &b.Tasks[i]
		}
		if t.Slug == id {
			if found >= 0 {
				return -1, nil
			}
			found = i
		}
	}
	if found >= 0 {
		return found, &b.Tasks[found]
	}
	return -1, nil
}

// Dir is the owner's bench folder.
func Dir() string {
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

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// ValidName reports whether name can name a benchmark (a file name).
func ValidName(name string) bool { return validName.MatchString(name) }

// BenchmarkPath is the file of benchmark name.
func BenchmarkPath(name string) string { return filepath.Join(Dir(), name+".yaml") }

// TaskDir holds a task's issue snapshot.
func TaskDir(project, slug string) string {
	return filepath.Join(Dir(), "tasks", project, filepath.FromSlash(slug))
}

// SnapshotPath is the task's issue as it stood at its start.
func SnapshotPath(t Task) string { return filepath.Join(TaskDir(t.Project, t.Slug), "issue.md") }

// ErrNoBenchmark is returned by Load for a benchmark with no file.
var ErrNoBenchmark = errors.New("no such benchmark")

// Load reads benchmark name.
func Load(name string) (*Benchmark, error) {
	if !ValidName(name) {
		return nil, fmt.Errorf("benchmark name %q: use lowercase letters, digits, . _ -", name)
	}
	path := BenchmarkPath(name)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w %q (%s)", ErrNoBenchmark, name, path)
	}
	if err != nil {
		return nil, err
	}
	var b Benchmark
	if err := yaml.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if b.Name == "" {
		b.Name = name
	}
	b.path = path
	return &b, nil
}

// New is an empty benchmark named name, not yet saved.
func New(name string) *Benchmark {
	return &Benchmark{Name: name, path: BenchmarkPath(name)}
}

// Save writes the benchmark to its file.
func (b *Benchmark) Save() error {
	if b.path == "" {
		b.path = BenchmarkPath(b.Name)
	}
	data, err := yaml.Marshal(b)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(b.path), 0o755); err != nil {
		return err
	}
	tmp := b.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, b.path)
}

// List names the benchmarks in Dir, sorted.
func List() []string {
	entries, _ := os.ReadDir(Dir())
	var out []string
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".yaml"); ok && !e.IsDir() && ValidName(name) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
