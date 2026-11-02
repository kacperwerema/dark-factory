// Package wfedit edits one station of a workflow file for the hall's Edit
// form: the status's description and prompt, the actions on the base edges
// into and out of it, and the owner's gate on the edge in. An edit
// re-encodes only the YAML items it changes and splices them into the
// file's own text, so every other line (comments, quoting, block scalars)
// stays byte for byte. Apply previews it (diff, checks), Save writes it and
// commits that one file on main with the owner's reason quoted.
// Design: issues/Factory/hall-edit-the-workflow-from-the-hall.md.
package wfedit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Station is what the Edit form shows for one status of one workflow file.
type Station struct {
	File        string
	Name        string
	Description string
	Prompt      string
	// Line is the status entry's line in the file.
	Line int
	// In and Out are the base edges into and out of the station, in file
	// order. A self edge is in both.
	In  []Edge
	Out []Edge
	// ReadOnly is what also shapes the station but is edited by hand:
	// wildcard edges and type or system overlay entries.
	ReadOnly []Ref
	// Hash is the file's sha256 when it was read; Apply refuses an edit made
	// on an older one.
	Hash string
}

// Edge is one base transitions[] entry.
type Edge struct {
	// Index is the entry's position in transitions[]; a Change names edges
	// by it.
	Index   int
	From    string
	To      string
	Line    int
	Actions []tracker.WorkflowAction
}

// Label is "from → to".
func (e Edge) Label() string { return e.From + " → " + e.To }

// Gate reports whether the edge carries the owner's approval for its
// target.
func (e Edge) Gate() bool { return HasGate(e.Actions, e.To) }

// Ref is a read-only entry: what it is and its line.
type Ref struct {
	What string
	Line int
}

// HasGate reports whether actions hold a require_human_approval for to
// (an action without status: approves the edge's own target).
func HasGate(actions []tracker.WorkflowAction, to string) bool {
	for _, a := range actions {
		if isGate(a, to) {
			return true
		}
	}
	return false
}

func isGate(a tracker.WorkflowAction, to string) bool {
	if a.Type != "require_human_approval" {
		return false
	}
	s := strings.TrimSpace(a.Status)
	return s == "" || s == to
}

// SetGate is actions with the owner's gate for to switched on or off. On
// adds a require_human_approval before the first append_section,
// inject_prompt or set_fields (checks first, side effects after, as the
// bundled templates order them); off drops every gate for to.
func SetGate(actions []tracker.WorkflowAction, to string, on bool) []tracker.WorkflowAction {
	if HasGate(actions, to) == on {
		return actions
	}
	var out []tracker.WorkflowAction
	if !on {
		for _, a := range actions {
			if !isGate(a, to) {
				out = append(out, a)
			}
		}
		return out
	}
	gate := tracker.WorkflowAction{Type: "require_human_approval", Status: to}
	at := len(actions)
	for i, a := range actions {
		if a.Type == "append_section" || a.Type == "inject_prompt" || a.Type == "set_fields" {
			at = i
			break
		}
	}
	out = append(out, actions[:at]...)
	out = append(out, gate)
	return append(out, actions[at:]...)
}

// file is a parsed workflow file and the nodes an edit touches.
type file struct {
	path  string
	raw   []byte
	hash  string
	lines []string
	root  *yaml.Node
	// statuses and transitions are the top-level sequences; their ends
	// are the line after the sequence's last item (1-based), the next
	// top-level key's line or EOF.
	statuses, transitions       *yaml.Node
	statusesEnd, transitionsEnd int
}

func hashOf(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func parse(path string, raw []byte) (*file, error) {
	f := &file{path: path, raw: raw, hash: hashOf(raw), lines: strings.Split(string(raw), "\n")}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s does not parse: %w", path, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s is not a mapping of keys", path)
	}
	f.root = doc.Content[0]
	for i := 0; i+1 < len(f.root.Content); i += 2 {
		end := len(f.lines) + 1
		if i+2 < len(f.root.Content) {
			end = f.root.Content[i+2].Line
		}
		switch val := f.root.Content[i+1]; f.root.Content[i].Value {
		case "statuses":
			if val.Kind == yaml.SequenceNode {
				f.statuses, f.statusesEnd = val, end
			}
		case "transitions":
			if val.Kind == yaml.SequenceNode {
				f.transitions, f.transitionsEnd = val, end
			}
		}
	}
	if f.statuses == nil {
		return nil, fmt.Errorf("%s has no statuses: list", path)
	}
	return f, nil
}

// mapValue is the value under key in mapping m, or nil.
func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func scalar(m *yaml.Node, key string) string {
	if v := mapValue(m, key); v != nil && v.Kind == yaml.ScalarNode {
		return v.Value
	}
	return ""
}

// status is the statuses[] item named name and its index, or -1.
func (f *file) status(name string) (*yaml.Node, int) {
	for i, it := range f.statuses.Content {
		if scalar(it, "name") == name {
			return it, i
		}
	}
	return nil, -1
}

func (f *file) edges() []*yaml.Node {
	if f.transitions == nil {
		return nil
	}
	return f.transitions.Content
}

// Open reads path and returns station's form model.
func Open(path, station string) (*Station, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := parse(path, raw)
	if err != nil {
		return nil, err
	}
	return f.station(station)
}

func (f *file) station(name string) (*Station, error) {
	item, _ := f.status(name)
	if item == nil {
		return nil, fmt.Errorf("%s has no status %q", f.path, name)
	}
	st := &Station{File: f.path, Name: name, Description: scalar(item, "description"), Prompt: scalar(item, "prompt"), Line: item.Line, Hash: f.hash}
	for i, it := range f.edges() {
		from, to := scalar(it, "from"), scalar(it, "to")
		if from == "*" {
			if to == name {
				st.ReadOnly = append(st.ReadOnly, Ref{What: "transitions: * → " + to, Line: it.Line})
			}
			continue
		}
		if from != name && to != name {
			continue
		}
		e := Edge{Index: i, From: from, To: to, Line: it.Line}
		if acts := mapValue(it, "actions"); acts != nil {
			if err := acts.Decode(&e.Actions); err != nil {
				return nil, fmt.Errorf("%s:%d: actions: %w", f.path, acts.Line, err)
			}
		}
		if to == name {
			st.In = append(st.In, e)
		}
		if from == name {
			st.Out = append(st.Out, e)
		}
	}
	st.ReadOnly = append(st.ReadOnly, f.overlayRefs(name)...)
	return st, nil
}

// overlayRefs lists types.<t> and systems.<s> statuses and transitions
// entries that name the station.
func (f *file) overlayRefs(name string) []Ref {
	var out []Ref
	for _, block := range []string{"types", "systems"} {
		m := mapValue(f.root, block)
		if m == nil || m.Kind != yaml.MappingNode {
			continue
		}
		for i := 0; i+1 < len(m.Content); i += 2 {
			key, ov := m.Content[i].Value, m.Content[i+1]
			prefix := block + "." + key
			if s := mapValue(ov, "statuses"); s != nil {
				for _, it := range s.Content {
					if scalar(it, "name") == name {
						out = append(out, Ref{What: prefix + ".statuses: " + name, Line: it.Line})
					}
				}
			}
			if t := mapValue(ov, "transitions"); t != nil {
				for _, it := range t.Content {
					if from, to := scalar(it, "from"), scalar(it, "to"); from == name || to == name {
						out = append(out, Ref{What: prefix + ".transitions: " + from + " → " + to, Line: it.Line})
					}
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out
}
