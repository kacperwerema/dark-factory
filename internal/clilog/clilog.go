// Package clilog is the local log of issue-cli calls the Timeline tab,
// the agents canvas and the retry hint read. One record per call, written
// when the call ends: when it started, how it ended, and its arguments with
// free text replaced (Redact). Telemetry (internal/telemetry) is the separate,
// names-only usage log.
package clilog

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/telemetry"
)

// FileName is the per-project clilog inside <root>/.agent-logs/ and the
// global fallback inside $XDG_STATE_HOME/issue-cli/.
const FileName = "clilog.jsonl"

// Record is one issue-cli call. Exit and DurMS are nil in records written
// before v0.54, which were logged before the call ran.
type Record struct {
	TS       string   `json:"ts"`
	PID      int      `json:"pid"`
	PPID     int      `json:"ppid"`
	Args     []string `json:"args"`
	Exit     *int     `json:"exit,omitempty"`
	ErrClass string   `json:"err_class,omitempty"`
	DurMS    *int64   `json:"dur_ms,omitempty"`
	// Sandbox: the worker guard passed an owner-only command on a marked
	// sandbox config; SandboxRefused: it refused a never-in-sandbox one
	// there (cmd/issue-cli/sandbox_pass.go).
	Sandbox        bool `json:"sandbox,omitempty"`
	SandboxRefused bool `json:"sandbox_refused,omitempty"`
}

// Start is when the call started; zero when TS does not parse.
func (r Record) Start() time.Time {
	t, err := time.Parse(time.RFC3339, r.TS)
	if err != nil {
		return time.Time{}
	}
	return t
}

// End is when the call ended: Start plus DurMS, or Start for old records.
func (r Record) End() time.Time {
	t := r.Start()
	if t.IsZero() || r.DurMS == nil {
		return t
	}
	return t.Add(time.Duration(*r.DurMS) * time.Millisecond)
}

// ProjectPath is the per-project clilog: <root>/.agent-logs/clilog.jsonl,
// root being the project's telemetry root (its primary checkout).
func ProjectPath(root string) string {
	return filepath.Join(root, ".agent-logs", FileName)
}

// GlobalPath is the clilog for calls with no resolvable project, next to the
// global telemetry file. "" when no home directory can be determined.
func GlobalPath() string {
	p := telemetry.GlobalPath()
	if p == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(p), FileName)
}

// LegacyPath is the shared $TMPDIR log written before v0.54. Nothing writes
// it any more; the Timeline tab still reads it while it exists.
func LegacyPath() string {
	return filepath.Join(os.TempDir(), "issue-cli-logs", "actions.jsonl")
}

// AgentPath is the clilog of one agent (tmux session or assignee name) under
// root: <root>/.agent-logs/<name>/<name>.clilog.
func AgentPath(root, name string) string {
	return filepath.Join(root, ".agent-logs", name, name+".clilog")
}

// Append writes rec as one line to path, rotating to path+".1" past
// telemetry.MaxFileBytes.
func Append(path string, rec Record) error {
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return telemetry.AppendLine(path, append(line, '\n'), 0)
}

// TailBytes is how much of a clilog's end Newest reads: enough for the newest
// record even when its args carry a long --body.
const TailBytes = 64 * 1024

// Newest is the newest record across the clilogs at paths for project: the
// one that ended last (start + duration; old records have only the start).
// Records naming another --project are skipped: test runs in an agent's shell
// inherit ISSUE_CLI_LOG and log their fixture projects. Both a worker's state
// and the no-first-call report read it.
func Newest(project string, paths ...string) (Record, bool) {
	var newest Record
	found := false
	for _, p := range paths {
		if r, ok := tailRecord(p, project); ok && (!found || r.End().After(newest.End())) {
			newest, found = r, true
		}
	}
	return newest, found
}

func tailRecord(path, project string) (Record, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Record{}, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Record{}, false
	}
	start := max(info.Size()-TailBytes, 0)
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return Record{}, false
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return Record{}, false
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		var rec Record
		if json.Unmarshal([]byte(lines[i]), &rec) != nil {
			continue // the first line may be cut mid-record
		}
		if p := projectFlag(rec.Args); p != "" && project != "" && p != project {
			continue
		}
		if rec.End().IsZero() {
			continue
		}
		return rec, true
	}
	return Record{}, false
}

// projectFlag is the value after --project in args, or "".
func projectFlag(args []string) string {
	for i, a := range args {
		if a == "--project" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// Read visits the records of path+".1" then path, oldest first, skipping
// malformed lines. Missing files are not an error.
func Read(path string, visit func(Record)) {
	for _, p := range []string{path + ".1", path} {
		readFile(p, visit)
	}
}

func readFile(path string, visit func(Record)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var rec Record
		if json.Unmarshal(scanner.Bytes(), &rec) != nil || len(rec.Args) == 0 && rec.TS == "" {
			continue
		}
		visit(rec)
	}
}

// StripGlobal removes issue-cli's global flags (--json, --debug, --config
// <val>, --project <val>) so args[0] is the command.
func StripGlobal(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json", "--debug":
			continue
		case "--config", "--project":
			if i+1 < len(args) {
				i++
			}
			continue
		}
		out = append(out, args[i])
	}
	return out
}

// FlagValue is the value of flag in args ("--flag v" or "--flag=v"), "" when
// absent.
func FlagValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, flag+"="); ok {
			return v
		}
	}
	return ""
}
