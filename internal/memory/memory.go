// Package memory is the agents' shared memory: facts one agent learned (a
// convention, a pitfall, how something is done, who knows what) with their
// source, for the agents that come after. It takes its patterns from memory
// libraries (mem0, Letta, Graphiti, LangMem) without their engines: the
// calling agent is the LLM, so the engine retrieves similar memories and
// refuses near-duplicates, and the agent decides ADD / UPDATE / SUPERSEDE /
// NOOP / forget (mem0's update step). No model, no server, nothing leaves
// the machine.
//
// A store is an append-only event log (events.jsonl) folded into the
// current set, so every memory has its history (mem0 history) and a
// superseded or forgotten memory is invalidated, not deleted (Graphiti).
// The project's log sits with the work files in <issues>/.memory/, so a
// private tracker keeps it private; the machine store sits under
// $XDG_DATA_HOME, in no repo. See docs/memory.md and
// factory/shared-memory-between-agents-mem0-or-similar-open-source (Design v2).
package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// Scopes. A department scope is "department:<key>", an agent's
// "agent:<session name>".
const (
	ScopeProject = "project"
	ScopeMachine = "machine"
	deptPrefix   = "department:"
	agentPrefix  = "agent:"
)

// DepartmentScope is the scope of a department's memories.
func DepartmentScope(key string) string { return deptPrefix + key }

// AgentScope is the scope of one session's own memories.
func AgentScope(name string) string { return agentPrefix + name }

// Kinds (LangMem's semantic, procedural; "who" for who knows what).
const (
	KindFact       = "fact"
	KindConvention = "convention"
	KindPitfall    = "pitfall"
	KindHowto      = "howto"
	KindWho        = "who"
)

// Kinds is every kind, in the order help lists them.
var Kinds = []string{KindFact, KindConvention, KindPitfall, KindHowto, KindWho}

// MaxText is the longest memory in characters: one fact, not a document.
const MaxText = 500

// Memory is one fact as it stands now (folded from its events).
type Memory struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	Kind  string `json:"kind"`
	Scope string `json:"scope"`
	// Project is the slug of the project that wrote it.
	Project    string   `json:"project,omitempty"`
	System     string   `json:"system,omitempty"`
	Department string   `json:"department,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Source     Source   `json:"source,omitempty"`
	Author     string   `json:"author,omitempty"`
	Created    string   `json:"created"`
	Updated    string   `json:"updated,omitempty"`
	// Expires (YYYY-MM-DD): the memory stops being valid after that day.
	Expires string `json:"expires,omitempty"`
	// InvalidAt is when it was superseded or forgotten; it stays in the log.
	InvalidAt     string   `json:"invalid_at,omitempty"`
	InvalidReason string   `json:"invalid_reason,omitempty"`
	SupersededBy  string   `json:"superseded_by,omitempty"`
	Supersedes    string   `json:"supersedes,omitempty"`
	Links         []string `json:"links,omitempty"`
	Pinned        bool     `json:"pinned,omitempty"`
	PinnedBy      string   `json:"pinned_by,omitempty"`
	SuggestPin    bool     `json:"suggest_pin,omitempty"`
	Uses          int      `json:"uses,omitempty"`
	LastUsed      string   `json:"last_used,omitempty"`
	Hash          string   `json:"hash,omitempty"`
}

// Source is where a memory was learned.
type Source struct {
	Issue  string `json:"issue,omitempty"`
	Commit string `json:"commit,omitempty"`
	// Comment is the 1-based index of the issue comment it came from.
	Comment int    `json:"comment,omitempty"`
	Retro   string `json:"retro,omitempty"`
}

// String is the source in words: "issue <slug> · commit <sha> · …", or "".
func (s Source) String() string {
	var parts []string
	if s.Issue != "" {
		parts = append(parts, "issue "+s.Issue)
	}
	if s.Commit != "" {
		parts = append(parts, "commit "+s.Commit)
	}
	if s.Comment > 0 {
		parts = append(parts, fmt.Sprintf("comment %d", s.Comment))
	}
	if s.Retro != "" {
		parts = append(parts, "retro "+s.Retro)
	}
	return strings.Join(parts, " · ")
}

// Valid reports whether m is still true at now: not superseded, forgotten
// or past its expiry.
func (m Memory) Valid(now time.Time) bool {
	if m.InvalidAt != "" {
		return false
	}
	return !m.Expired(now)
}

// Expired: now is past the memory's Expires day.
func (m Memory) Expired(now time.Time) bool {
	if m.Expires == "" {
		return false
	}
	d, err := time.Parse("2006-01-02", m.Expires)
	return err == nil && !now.UTC().Before(d.Add(24*time.Hour))
}

// State is valid, superseded, forgotten or expired.
func (m Memory) State(now time.Time) string {
	switch {
	case m.SupersededBy != "":
		return "superseded"
	case m.InvalidAt != "":
		return "forgotten"
	case m.Expired(now):
		return "expired"
	}
	return "valid"
}

// Changed is when m was last written.
func (m Memory) Changed() string {
	if m.Updated != "" {
		return m.Updated
	}
	return m.Created
}

// Day is the date m was last written (YYYY-MM-DD).
func (m Memory) Day() string {
	c := m.Changed()
	if len(c) >= 10 {
		return c[:10]
	}
	return c
}

// ScopeDepartment is the department key of a department scope ("" else).
func (m Memory) ScopeDepartment() string {
	k, _ := strings.CutPrefix(m.Scope, deptPrefix)
	if k == m.Scope {
		return ""
	}
	return k
}

// Viewer is who reads: a department's agent sees its department's
// memories, a session its own; All (the CEO, the owner) sees every scope.
type Viewer struct {
	Department string
	Agent      string
	All        bool
}

// VisibleTo reports whether v may read m.
func (m Memory) VisibleTo(v Viewer) bool {
	switch {
	case m.Scope == ScopeProject || m.Scope == ScopeMachine || m.Scope == "":
		return true
	case v.All:
		return true
	case strings.HasPrefix(m.Scope, deptPrefix):
		return v.Department != "" && strings.TrimPrefix(m.Scope, deptPrefix) == v.Department
	case strings.HasPrefix(m.Scope, agentPrefix):
		return v.Agent != "" && strings.TrimPrefix(m.Scope, agentPrefix) == v.Agent
	}
	return false
}

// CheckScope is why scope is not one a memory can have, or nil.
func CheckScope(scope string) error {
	switch {
	case scope == ScopeProject || scope == ScopeMachine:
		return nil
	case strings.HasPrefix(scope, deptPrefix) && len(scope) > len(deptPrefix):
		return nil
	case strings.HasPrefix(scope, agentPrefix) && len(scope) > len(agentPrefix):
		return nil
	}
	return fmt.Errorf("no scope %q: project, department:<key>, agent:<session> or machine", scope)
}

// CheckKind is why kind is unknown, or nil.
func CheckKind(kind string) error {
	for _, k := range Kinds {
		if k == kind {
			return nil
		}
	}
	return fmt.Errorf("no kind %q: %s", kind, strings.Join(Kinds, ", "))
}

// CheckText is why text can't be a memory, or nil: empty, too long, or
// holding something that looks like a secret. The error never quotes it.
func CheckText(text string) error {
	t := strings.TrimSpace(text)
	if t == "" {
		return errors.New("a memory needs text: one fact in a sentence or two")
	}
	if n := utf8.RuneCountInString(t); n > MaxText {
		return fmt.Errorf("a memory is one fact of at most %d characters (this one has %d): split it, or put the long version in the docs and remember where it is", MaxText, n)
	}
	if kind := FindSecret(t); kind != "" {
		return &SecretError{Kind: kind}
	}
	return nil
}

// SecretError is text refused because it looks like a secret.
type SecretError struct{ Kind string }

func (e *SecretError) Error() string {
	return "refused: the text looks like it holds a secret (" + e.Kind + "). Memories never store secrets; name where it lives instead (env:<VAR> or keychain:<service>)"
}

// ErrNotFound is an id no memory in the store has.
var ErrNotFound = errors.New("no such memory")

// Store is one event log: a project's, or the machine's.
type Store struct {
	Path    string
	Machine bool
}

// ProjectPath is the project's memory log under its issues dir.
func ProjectPath(issueDir string) string {
	return filepath.Join(issueDir, ".memory", "events.jsonl")
}

// MachinePath is the machine-wide log: $XDG_DATA_HOME, else ~/.local/share.
func MachinePath() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = os.TempDir()
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "ai-workflow-engine", "memory", "events.jsonl")
}

// ProjectStore is a project's store.
func ProjectStore(issueDir string) Store { return Store{Path: ProjectPath(issueDir)} }

// MachineStore is the machine-wide store.
func MachineStore() Store { return Store{Path: MachinePath(), Machine: true} }

// StoreOfID is "project" for an m-… id, "machine" for s-…, "" else.
func StoreOfID(id string) string {
	switch {
	case strings.HasPrefix(id, "m-"):
		return "project"
	case strings.HasPrefix(id, "s-"):
		return "machine"
	}
	return ""
}
