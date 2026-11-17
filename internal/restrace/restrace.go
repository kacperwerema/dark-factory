// Package restrace records, when asked, every start and stop of a resource
// the factory runs outside its own process: a playtest Chrome, a tmux
// session, an agent CLI launch, a worktree, a per_run: tool, a detached
// child. It exists to find leaks: fseventsd grew again while nothing in the
// Go code watched files, and the suspects were children nobody stopped.
//
// Off by default. DF_TRACE=resources (or a comma list naming it), or
// --debug on issue-cli and the viewer (SetDebug), turns it on. Off, a call
// returns after one environment read and opens no file. On, each event is
// one JSONL line in $XDG_STATE_HOME/ai-workflow-engine/trace/resources.jsonl
// (Path), the file rotated to resources.jsonl.1 at 10 MB. issue-cli doctor
// leaks reads it back (Read, Open). docs/getting-started.md#troubleshooting.
package restrace

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kacperwerema/dark-factory/internal/procs"
	"github.com/kacperwerema/dark-factory/internal/viewerstate"
)

// EnvVar turns tracing on: DF_TRACE=resources.
const EnvVar = "DF_TRACE"

// The kinds of resource a line names.
const (
	KindChrome      = "chrome"
	KindTmuxSession = "tmux-session"
	KindAgentCLI    = "agent-cli"
	KindWorktree    = "worktree"
	KindPerRun      = "per-run"
	KindChild       = "child"
	// KindContainer is a per_run: tool whose start: runs a container
	// (docker, podman): the only way the engine starts one.
	KindContainer = "container"
	// KindFseventsd is the 10-minute sample of fseventsd's footprint.
	KindFseventsd = "fseventsd"
)

// The events.
const (
	EventStart  = "start"
	EventStop   = "stop"
	EventSample = "sample"
)

// RotateAt is the size at which the file moves to .1.
const RotateAt = 10 << 20

var (
	debug atomic.Bool
	owner atomic.Value // string
	mu    sync.Mutex
)

// PerRunKind is the kind of a per_run: tool with this start: command.
func PerRunKind(start string) string {
	for _, f := range strings.Fields(start) {
		switch filepath.Base(f) {
		case "docker", "podman", "docker-compose", "nerdctl":
			return KindContainer
		}
	}
	return KindPerRun
}

// Event is one line.
type Event struct {
	At    time.Time `json:"at"`
	Event string    `json:"event"`
	Kind  string    `json:"kind"`
	// PID is the resource's process, 0 when it has none of its own (a
	// worktree, a per_run: tool started by a shell step).
	PID int `json:"pid,omitempty"`
	// ID names it where a pid can't: a session, a path, a connector.
	ID string `json:"id,omitempty"`
	// Detail is a short note (the agent, the command).
	Detail string `json:"detail,omitempty"`
	// Owner is the issue the work is for, else the command that started it.
	Owner string `json:"owner,omitempty"`
	Cwd   string `json:"cwd,omitempty"`
	// By is the pid of the process that wrote the line.
	By int `json:"by"`
	// FootprintMB and Live are a sample's: fseventsd's physical footprint
	// and the traced resources alive at that time.
	FootprintMB float64 `json:"footprint_mb,omitempty"`
	Live        *int    `json:"live,omitempty"`
}

// On reports whether tracing is on.
func On() bool {
	if debug.Load() {
		return true
	}
	return Wants(os.Getenv(EnvVar))
}

// Wants reads a DF_TRACE value: "resources", "all" or "1", alone or in a
// comma list.
func Wants(v string) bool {
	if v == "" {
		return false
	}
	for _, f := range strings.Split(v, ",") {
		switch strings.TrimSpace(strings.ToLower(f)) {
		case "resources", "all", "1":
			return true
		}
	}
	return false
}

// SetDebug turns tracing on for this process (--debug) and for every child
// it starts, which inherit DF_TRACE.
func SetDebug(on bool) {
	debug.Store(on)
	if on && !Wants(os.Getenv(EnvVar)) {
		v := "resources"
		if cur := os.Getenv(EnvVar); cur != "" {
			v = cur + ",resources"
		}
		os.Setenv(EnvVar, v)
	}
}

// SetOwner names the command this process runs ("issue-cli dispatch"),
// the owner of a line when no issue is set.
func SetOwner(s string) { owner.Store(s) }

// Owner is the issue (ISSUE_VIEWER_ISSUE_SLUG) else the command.
func Owner() string {
	if s := strings.TrimSpace(os.Getenv("ISSUE_VIEWER_ISSUE_SLUG")); s != "" {
		return s
	}
	if s, _ := owner.Load().(string); s != "" {
		return s
	}
	return filepath.Base(os.Args[0])
}

// Path is the trace file, "" without a state dir.
func Path() string {
	d := viewerstate.Dir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "trace", "resources.jsonl")
}

// Start records that a resource started.
func Start(kind string, pid int, id, detail string) { write(EventStart, kind, pid, id, detail) }

// Stop records that a resource was stopped.
func Stop(kind string, pid int, id string) { write(EventStop, kind, pid, id, "") }

// Sample records fseventsd's footprint beside the count of live traced
// resources.
func Sample(pid int, footprintMB float64, live int) {
	if !On() {
		return
	}
	e := base(EventSample, KindFseventsd, pid, "", "")
	e.FootprintMB, e.Live = footprintMB, &live
	_ = Append(Path(), e)
}

func write(event, kind string, pid int, id, detail string) {
	if !On() {
		return
	}
	_ = Append(Path(), base(event, kind, pid, id, detail))
}

func base(event, kind string, pid int, id, detail string) Event {
	cwd, _ := os.Getwd()
	return Event{At: time.Now().UTC(), Event: event, Kind: kind, PID: pid, ID: id, Detail: detail, Owner: Owner(), Cwd: cwd, By: os.Getpid()}
}

// Append writes e as one line to path, rotating the file first when it
// has reached RotateAt.
func Append(path string, e Event) error {
	if path == "" {
		return nil
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	rotate(path)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// rotate moves path to path.1 once it has reached RotateAt (the old .1
// goes): the trace keeps at most twice that.
func rotate(path string) {
	if info, err := os.Stat(path); err == nil && info.Size() >= RotateAt {
		_ = os.Rename(path, path+".1")
	}
}

// Read is every event in the rotated file and then the current one, oldest
// first. Lines that don't parse are skipped; a missing file is no events.
func Read(path string) []Event {
	var out []Event
	for _, p := range []string{path + ".1", path} {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			var e Event
			if json.Unmarshal(sc.Bytes(), &e) == nil && e.Kind != "" {
				out = append(out, e)
			}
		}
		f.Close()
	}
	return out
}

// key is what pairs a stop with its start: the kind and the id, or the
// pid when there is no id.
func key(e Event) string {
	if e.ID != "" {
		return e.Kind + "\x00" + e.ID
	}
	return e.Kind + "\x00#" + strconv.Itoa(e.PID)
}

// Live is the open starts whose process is still alive (procs.Alive);
// a start with no pid of its own (a worktree, a session) isn't counted.
func Live(open []Event) []Event {
	var out []Event
	for _, e := range open {
		if e.PID > 0 && procs.Alive(e.PID) {
			out = append(out, e)
		}
	}
	return out
}

// SampleNow records fseventsd's reading beside the traced resources alive
// now; a no-op unless tracing is on.
func SampleNow(pid int, footprintMB float64) {
	if !On() {
		return
	}
	Sample(pid, footprintMB, len(Live(Open(Read(Path())))))
}

// Open is each start with no later stop, oldest first.
func Open(events []Event) []Event {
	open := map[string]int{}
	var starts []Event
	for _, e := range events {
		switch e.Event {
		case EventStart:
			open[key(e)] = len(starts)
			starts = append(starts, e)
		case EventStop:
			k := key(e)
			if i, ok := open[k]; ok {
				starts[i].Event = "" // stopped
				delete(open, k)
			} else if e.ID != "" && e.PID != 0 {
				// A stop that knows both: match a start by pid too.
				pk := e.Kind + "\x00#" + strconv.Itoa(e.PID)
				if i, ok := open[pk]; ok {
					starts[i].Event = ""
					delete(open, pk)
				}
			}
		}
	}
	var out []Event
	for _, s := range starts {
		if s.Event == EventStart {
			out = append(out, s)
		}
	}
	return out
}
