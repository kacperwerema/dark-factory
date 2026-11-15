// Package partner reads where a department's outside partner is with a
// piece of work: the partner's own stage and whether it waits for the
// owner. A partner is a separate program with its own stages and approval
// gates (OpenMontage for Video); the engine only reads its files as data,
// through an Adapter chosen by the department's partner: block, so the
// partner can be swapped by config without touching the department's
// workflow
// (factory/video-department-openmontage-as-a-video-producer-department-template-workflow-connector-videos-on-the-shelf#D-4).
//
// Nothing here runs, imports or writes anything in the partner's checkout:
// adapters open files read-only, with a size cap. See
// docs/departments.md#partners.
package partner

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// ErrFormat is a partner file in a format the adapter doesn't know: the
// pages say "partner format not understood" and show no stage, never a
// guess.
var ErrFormat = errors.New("partner format not understood")

// FormatWords is what the pages show for ErrFormat.
const FormatWords = "partner format not understood"

// States of a project at the partner.
const (
	// StateNone: the partner has no such project yet.
	StateNone = "none"
	// StateWorking: the current stage is in progress (or not started).
	StateWorking = "working"
	// StateAwaiting: the current stage waits for the owner's approval.
	StateAwaiting = "awaiting"
	// StateFailed: the current stage failed.
	StateFailed = "failed"
	// StateDone: every stage is completed.
	StateDone = "done"
)

// Status is one project at the partner: what an adapter read from its
// files.
type Status struct {
	// Project is the id the adapter looked for.
	Project string
	// Stage is the current stage; "" when none (StateNone, StateDone).
	Stage string
	// State is one of the State* constants.
	State string
	// Summary is the current stage's own summary (its review), if any.
	Summary string
	// Artifacts are the names of what the current stage produced.
	Artifacts []string
	// Cost is the newest cost snapshot; nil when the partner wrote none.
	Cost *Cost
	// Error is a failed stage's error.
	Error string
	// Since is the current stage's checkpoint time; zero when unknown.
	Since time.Time
	// LiveURL is the partner's own viewer for this project, when it
	// has one (shown only while it answers: Live).
	LiveURL string
	// HealthURL is what Live asks to know the viewer answers.
	HealthURL string
}

// Awaiting reports whether the project waits for the owner.
func (s Status) Awaiting() bool { return s.State == StateAwaiting }

// Cost is a partner's spend on one project, in US dollars.
type Cost struct {
	Spent, Reserved, Remaining float64
}

// Budget is spent plus what is left: the cap the partner works under.
func (c Cost) Budget() float64 { return c.Spent + c.Reserved + c.Remaining }

// Adapter reads one partner's files.
type Adapter interface {
	// Status reads project at the partner whose checkout is dir. A project
	// that isn't there is State StateNone with a nil error; a file in a
	// format the adapter doesn't know is an error wrapping ErrFormat.
	Status(cfg tracker.Partner, dir, project string) (Status, error)
}

var (
	mu       sync.RWMutex
	adapters = map[string]Adapter{}
)

// Register adds an adapter under name (lower case). Adapters register in
// init; tests register stand-ins and remove them with the returned func.
func Register(name string, a Adapter) (unregister func()) {
	name = strings.ToLower(strings.TrimSpace(name))
	mu.Lock()
	defer mu.Unlock()
	prev, had := adapters[name]
	adapters[name] = a
	return func() {
		mu.Lock()
		defer mu.Unlock()
		if had {
			adapters[name] = prev
		} else {
			delete(adapters, name)
		}
	}
}

// Names are the registered adapters, sorted.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(adapters))
	for n := range adapters {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// For is the adapter cfg names.
func For(cfg tracker.Partner) (Adapter, error) {
	name := strings.ToLower(strings.TrimSpace(cfg.Adapter))
	if name == "" {
		return nil, fmt.Errorf("partner %s names no adapter: (one of %s)", cfg.Label(), strings.Join(Names(), ", "))
	}
	mu.RLock()
	a, ok := adapters[name]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("partner adapter %q is unknown (one of %s)", name, strings.Join(Names(), ", "))
	}
	return a, nil
}

// ProjectField is the issue field naming the partner's project.
const ProjectField = "partner_project"

var projectIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ProjectID is the partner project an issue is: its partner_project:
// field, else the last element of its slug. It must be one safe path
// element (no /, no .., no leading dot), since adapters join it to the
// partner's folder.
func ProjectID(issue *tracker.Issue) (string, error) {
	id := strings.TrimSpace(issue.Field(ProjectField))
	from := ProjectField + ": " + id
	if id == "" {
		id = issue.Slug
		if i := strings.LastIndex(id, "/"); i >= 0 {
			id = id[i+1:]
		}
		from = "the slug's last element " + id
	}
	if !projectIDRe.MatchString(id) || strings.Contains(id, "..") {
		return "", fmt.Errorf("partner project id from %s is not one plain folder name (issue-cli set-meta %s %s <id>)", from, issue.Slug, ProjectField)
	}
	return id, nil
}

// Read is one issue's partner status: the partner block's adapter over
// the checkout at cfg's path (relative to root). Errors are words for the
// pages: errors.Is(err, ErrFormat) for a format the adapter doesn't know.
func Read(cfg tracker.Partner, root string, issue *tracker.Issue) (Status, error) {
	id, err := ProjectID(issue)
	if err != nil {
		return Status{}, err
	}
	a, err := For(cfg)
	if err != nil {
		return Status{Project: id}, err
	}
	dir := cfg.Dir(root)
	if dir == "" {
		return Status{Project: id}, fmt.Errorf("partner %s has no path:", cfg.Label())
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return Status{Project: id}, fmt.Errorf("partner %s: path %s is missing", cfg.Label(), dir)
	}
	st, err := a.Status(cfg, dir, id)
	st.Project = id
	return st, err
}

// MaxFile is the most an adapter reads of one partner file.
const MaxFile = 1 << 20

// ReadFile reads a partner file read-only, refusing one over MaxFile.
func ReadFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxFile+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxFile {
		return nil, fmt.Errorf("%s is over %d bytes: not read", path, MaxFile)
	}
	return b, nil
}

// Line words one read for the pages and issue-cli: "OpenMontage: pitch ·
// awaiting you", "OpenMontage: no project <id> yet", or the problem
// ("OpenMontage: partner format not understood (checkpoint_x.json version
// "2.0")").
func Line(cfg tracker.Partner, st Status, err error) string {
	name := cfg.Label()
	if err != nil {
		return name + ": " + Problem(err)
	}
	switch st.State {
	case StateNone:
		return fmt.Sprintf("%s: no project %s yet", name, st.Project)
	case StateDone:
		return name + ": every stage done"
	case StateAwaiting:
		return name + ": " + st.Stage + " · awaiting you"
	case StateFailed:
		return name + ": " + st.Stage + " · failed"
	}
	return name + ": " + st.Stage + " · working"
}

// Problem words a read's error: FormatWords with the file and version in
// brackets for ErrFormat, the error itself otherwise.
func Problem(err error) string {
	if !errors.Is(err, ErrFormat) {
		return err.Error()
	}
	detail := strings.TrimPrefix(strings.TrimPrefix(err.Error(), FormatWords), ": ")
	if detail == "" {
		return FormatWords
	}
	return FormatWords + " (" + detail + ")"
}

// isDir reports whether path is a folder.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
