package wfedit

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/michal-franc/issue-viewer/internal/lint"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// ErrStale is returned when the file changed after the form was opened.
var ErrStale = errors.New("the workflow file changed since the editor opened it: reopen Edit and make the change again")

// Change is one edit of a station. Nil fields and edges missing from
// Edges stay as they are.
type Change struct {
	Description *string
	// Prompt "" removes the status's prompt:.
	Prompt *string
	// Edges is the new action list of base transitions[] entries, by
	// Edge.Index. Each must be an edge into or out of the station.
	Edges map[int][]tracker.WorkflowAction
}

// Preview is an edit before it is written.
type Preview struct {
	File    string
	Station string
	// Hash is the file's hash the edit was made on; Save refuses if the
	// file moved since.
	Hash string
	Old  []byte
	New  []byte
	// Diff is a unified diff of Old against New; "" when nothing changed.
	Diff string
	// Summary says what changed, for the commit subject:
	// "prompt edited, gate into testing removed".
	Summary string
	// Gates lists the gates the edit adds or removes ("gate into testing
	// removed"); the viewer warns about them while YOLO is on.
	Gates []string
	// Errors block the save; Warnings are the file's other lint findings.
	Errors   []string
	Warnings []string
}

// OK reports whether the preview may be saved: it changes something and
// nothing blocks it.
func (p *Preview) OK() bool { return p != nil && len(p.Errors) == 0 && !bytes.Equal(p.Old, p.New) }

// Apply makes ch on station in path, as read at hash, without writing.
// Problems with the change itself land in Preview.Errors; an error is
// returned when the file can't be read or edited at all (ErrStale among
// them).
func Apply(path, station, hash string, ch Change) (*Preview, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := parse(path, raw)
	if err != nil {
		return nil, err
	}
	if hash != "" && hash != f.hash {
		return nil, ErrStale
	}
	st, err := f.station(station)
	if err != nil {
		return nil, err
	}
	edges := map[int]Edge{}
	for _, e := range append(append([]Edge(nil), st.In...), st.Out...) {
		edges[e.Index] = e
	}

	style := f.quoteStyle()
	var edits []splice
	var summary, gates []string

	if ch.Description != nil || ch.Prompt != nil {
		item, idx := f.status(station)
		n := deepCopy(item)
		if ch.Description != nil && strings.TrimSpace(*ch.Description) != st.Description {
			setScalar(n, "description", strings.TrimSpace(*ch.Description), style, "name")
			summary = append(summary, "description edited")
		}
		if ch.Prompt != nil {
			p := normalizeText(*ch.Prompt)
			if p != normalizeText(st.Prompt) {
				if p == "" {
					deleteKey(n, "prompt")
					summary = append(summary, "prompt removed")
				} else {
					setScalar(n, "prompt", p, style, "description", "name")
					summary = append(summary, "prompt edited")
				}
			}
		}
		if len(summary) > 0 {
			edits = append(edits, f.itemSplice(f.statuses, idx, f.statusesEnd, n))
		}
	}

	idxs := make([]int, 0, len(ch.Edges))
	for i := range ch.Edges {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	for _, i := range idxs {
		e, ok := edges[i]
		if !ok {
			return nil, fmt.Errorf("transitions[%d] is not an edge into or out of %q", i, station)
		}
		acts := ch.Edges[i]
		if reflect.DeepEqual(normalizeActions(acts), normalizeActions(e.Actions)) {
			continue
		}
		item := f.edges()[i]
		n := deepCopy(item)
		setActions(n, mapValue(item, "actions"), e.Actions, acts, style)
		edits = append(edits, f.itemSplice(f.transitions, i, f.transitionsEnd, n))
		was, now := HasGate(e.Actions, e.To), HasGate(acts, e.To)
		switch {
		case !was && now:
			gates = append(gates, "gate into "+e.To+" added")
		case was && !now:
			gates = append(gates, "gate into "+e.To+" removed")
		}
		if !reflect.DeepEqual(normalizeActions(SetGate(e.Actions, e.To, false)), normalizeActions(SetGate(acts, e.To, false))) {
			summary = append(summary, "actions on "+e.Label()+" edited")
		}
	}
	summary = append(summary, gates...)

	pv := &Preview{File: path, Station: station, Hash: f.hash, Old: raw, Summary: strings.Join(summary, ", "), Gates: gates}
	pv.New = []byte(f.apply(edits))
	pv.Diff = unifiedDiff(filepath.Base(path), string(pv.Old), string(pv.New))
	pv.check(f, st, ch)
	return pv, nil
}

// normalizeActions makes nil and empty lists compare equal.
func normalizeActions(a []tracker.WorkflowAction) []tracker.WorkflowAction {
	if len(a) == 0 {
		return nil
	}
	return a
}

// normalizeText is a form textarea's text as the file holds it: \n line
// ends, a multi-line text ending in one \n (a | block), a single line
// trimmed.
func normalizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if strings.Contains(strings.TrimSpace(s), "\n") {
		return strings.TrimRight(s, " \t\n") + "\n"
	}
	return strings.TrimSpace(s)
}

// quoteStyle is how the file writes string values: double-quoted when
// its first transition's type: is, else plain.
func (f *file) quoteStyle() yaml.Style {
	for _, seq := range []*yaml.Node{f.transitions, f.statuses} {
		if seq == nil {
			continue
		}
		for _, it := range seq.Content {
			for _, key := range []string{"from", "name"} {
				if v := mapValue(it, key); v != nil && v.Kind == yaml.ScalarNode {
					return v.Style & (yaml.DoubleQuotedStyle | yaml.SingleQuotedStyle)
				}
			}
		}
	}
	return 0
}

func deepCopy(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	c := *n
	c.Content = make([]*yaml.Node, len(n.Content))
	for i, k := range n.Content {
		c.Content[i] = deepCopy(k)
	}
	return &c
}

// strNode is a string scalar in the file's style: a | block when
// multi-line.
func strNode(v string, style yaml.Style) *yaml.Node {
	n := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v, Style: style}
	if strings.Contains(v, "\n") {
		n.Style = yaml.LiteralStyle
	}
	return n
}

// setScalar sets key in mapping m to v, adding the key after the first
// of after that m has (else at the end) when m lacks it.
func setScalar(m *yaml.Node, key, v string, style yaml.Style, after ...string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			old := m.Content[i+1]
			n := strNode(v, style)
			if !strings.Contains(v, "\n") && old.Kind == yaml.ScalarNode && old.Style != yaml.LiteralStyle && old.Style != yaml.FoldedStyle {
				n.Style = old.Style
			}
			n.LineComment = old.LineComment
			m.Content[i+1] = n
			return
		}
	}
	at := len(m.Content)
	for _, a := range after {
		if i := keyIndex(m, a); i >= 0 {
			at = i + 2
			break
		}
	}
	kv := []*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, strNode(v, style)}
	m.Content = append(m.Content[:at], append(kv, m.Content[at:]...)...)
}

func keyIndex(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return i
		}
	}
	return -1
}

func deleteKey(m *yaml.Node, key string) {
	if i := keyIndex(m, key); i >= 0 {
		m.Content = append(m.Content[:i], m.Content[i+2:]...)
	}
}

// setActions sets edge item n's actions: to acts. An action equal to one
// of the old ones keeps that one's node (its comments and quoting); a new
// or changed one is encoded in the file's style.
func setActions(n, oldSeq *yaml.Node, old, acts []tracker.WorkflowAction, style yaml.Style) {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	if oldSeq != nil {
		seq.Style, seq.LineComment = oldSeq.Style&^yaml.FlowStyle, oldSeq.LineComment
	}
	used := make([]bool, len(old))
	for _, a := range acts {
		var node *yaml.Node
		for j, o := range old {
			if !used[j] && reflect.DeepEqual(o, a) && oldSeq != nil && j < len(oldSeq.Content) {
				used[j], node = true, deepCopy(oldSeq.Content[j])
				break
			}
		}
		if node == nil {
			node = actionNode(a, style)
		}
		seq.Content = append(seq.Content, node)
	}
	if len(acts) == 0 {
		seq.Style = yaml.FlowStyle
	}
	if i := keyIndex(n, "actions"); i >= 0 {
		n.Content[i+1] = seq
		return
	}
	n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "actions"}, seq)
}

func actionNode(a tracker.WorkflowAction, style yaml.Style) *yaml.Node {
	var n yaml.Node
	_ = n.Encode(a) // a struct of strings, ints and string lists always encodes
	var walk func(*yaml.Node, bool)
	walk = func(x *yaml.Node, key bool) {
		switch x.Kind {
		case yaml.ScalarNode:
			if !key && x.Tag == "!!str" {
				*x = *strNode(x.Value, style)
			}
		case yaml.MappingNode:
			for i, c := range x.Content {
				walk(c, i%2 == 0)
			}
		case yaml.SequenceNode:
			for _, c := range x.Content {
				walk(c, false)
			}
		}
	}
	walk(&n, false)
	return &n
}

// splice replaces lines [start, end) (0-based) with text's lines.
type splice struct {
	start, end int
	lines      []string
}

// itemSplice is seq's item idx re-encoded from n, over the item's own
// lines: from its "- " line to the line before the next item (or seqEnd),
// less trailing blank and comment lines, which belong to what follows (the
// next item's head comment among them).
// The item's head comment sits above it and stays as written. Lines whose
// text didn't change keep their original bytes (trailing spaces in a |
// block).
func (f *file) itemSplice(seq *yaml.Node, idx, seqEnd int, n *yaml.Node) splice {
	item := seq.Content[idx]
	start := item.Line - 1
	end := seqEnd - 1
	if idx+1 < len(seq.Content) {
		end = seq.Content[idx+1].Line - 1
	}
	for end > start && trailing(f.lines[end-1]) {
		end--
	}
	indent := item.Column - 3
	if indent < 0 {
		indent = 0
	}
	c := deepCopy(n)
	c.HeadComment, c.FootComment = "", ""
	lines := encodeItem(c, indent)
	return splice{start: start, end: end, lines: keepOriginal(f.lines[start:end], lines)}
}

func trailing(line string) bool {
	t := strings.TrimSpace(line)
	return t == "" || strings.HasPrefix(t, "#")
}

// encodeItem is n as a sequence item at indent: "- " on the first line.
func encodeItem(n *yaml.Node, indent int) []string {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	_ = enc.Encode(n) // a node tree decoded from this file, or built from strings
	_ = enc.Close()
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	for len(lines) > 1 && trailing(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	pad := strings.Repeat(" ", indent)
	for i, l := range lines {
		switch {
		case i == 0:
			lines[i] = pad + "- " + l
		case l != "":
			lines[i] = pad + "  " + l
		}
	}
	return lines
}

// commentGap is the spaces before a line comment, which the encoder
// writes as one.
var commentGap = regexp.MustCompile(`[ \t]+#`)

// keepOriginal is next with each line that matches an old line, once
// trailing spaces and the gap before a line comment are ignored, taken
// from old: whitespace the encoder drops doesn't show as a change. (A
// quoted value whose only change is such spacing reads back wrong and is
// refused by check.)
func keepOriginal(old, next []string) []string {
	norm := func(s string) string { return commentGap.ReplaceAllString(strings.TrimRight(s, " \t"), " #") }
	pairs := lcs(old, next, func(a, b string) bool { return norm(a) == norm(b) })
	out := append([]string(nil), next...)
	for _, p := range pairs {
		out[p[1]] = old[p[0]]
	}
	return out
}

func (f *file) apply(edits []splice) string {
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	lines := append([]string(nil), f.lines...)
	for _, e := range edits {
		lines = append(lines[:e.start], append(append([]string(nil), e.lines...), lines[e.end:]...)...)
	}
	return strings.Join(lines, "\n")
}

// check fills Errors and Warnings: the new text must parse, add no schema
// finding, keep every action well formed and decode to exactly the old
// workflow with ch applied (so a splice slip never reaches the file).
func (p *Preview) check(f *file, st *Station, ch Change) {
	var oldCfg, newCfg tracker.WorkflowConfig
	if err := yaml.Unmarshal(p.New, &newCfg); err != nil {
		p.Errors = append(p.Errors, "the edited file does not parse: "+err.Error())
		return
	}
	_ = yaml.Unmarshal(p.Old, &oldCfg) // parse() already read it

	names := map[string]bool{}
	for _, s := range oldCfg.Statuses {
		names[s.Name] = true
	}
	for _, i := range sortedKeys(ch.Edges) {
		for n, a := range ch.Edges[i] {
			if msg := actionProblem(a, names); msg != "" {
				e := oldCfg.Transitions[i]
				p.Errors = append(p.Errors, fmt.Sprintf("%s → %s, action %d: %s", e.From, e.To, n+1, msg))
			}
		}
	}

	want := oldCfg
	want.Statuses = append([]tracker.WorkflowStatus(nil), oldCfg.Statuses...)
	want.Transitions = append([]tracker.WorkflowTransition(nil), oldCfg.Transitions...)
	for i := range want.Statuses {
		if want.Statuses[i].Name != st.Name {
			continue
		}
		if ch.Description != nil {
			want.Statuses[i].Description = strings.TrimSpace(*ch.Description)
		}
		if ch.Prompt != nil && normalizeText(*ch.Prompt) != normalizeText(st.Prompt) {
			want.Statuses[i].Prompt = normalizeText(*ch.Prompt)
		}
	}
	for i, acts := range ch.Edges {
		want.Transitions[i].Actions = normalizeActions(acts)
	}
	got := newCfg
	for i := range got.Transitions {
		got.Transitions[i].Actions = normalizeActions(got.Transitions[i].Actions)
	}
	for i := range want.Transitions {
		want.Transitions[i].Actions = normalizeActions(want.Transitions[i].Actions)
	}
	if !reflect.DeepEqual(want, got) {
		p.Errors = append(p.Errors, "the edited file does not read back as the change (a bug in the editor): nothing was written; edit "+p.File+" by hand")
	}

	before, _ := lint.File(p.File, p.Old, time.Now())
	after, err := lint.File(p.File, p.New, time.Now())
	if err != nil {
		p.Errors = append(p.Errors, "the edited file does not parse: "+err.Error())
		return
	}
	had := map[string]bool{}
	for _, fd := range before {
		had[fd.Check+"\x00"+fd.Message] = true
	}
	for _, fd := range after {
		msg := fd.Where + ": " + fd.Message
		if fd.Fix != "" {
			msg += " (" + fd.Fix + ")"
		}
		if fd.Check == "schema" && !had[fd.Check+"\x00"+fd.Message] {
			p.Errors = append(p.Errors, msg)
			continue
		}
		p.Warnings = append(p.Warnings, msg)
	}
}

func sortedKeys(m map[int][]tracker.WorkflowAction) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

// actionProblem is why a is not a well-formed action, or "".
func actionProblem(a tracker.WorkflowAction, statuses map[string]bool) string {
	known := false
	for _, t := range tracker.WorkflowActionTypes {
		if t.Name == a.Type {
			known = true
		}
	}
	if !known {
		var types []string
		for _, t := range tracker.WorkflowActionTypes {
			types = append(types, t.Name)
		}
		return fmt.Sprintf("unknown action type %q (types: %s)", a.Type, strings.Join(types, ", "))
	}
	switch a.Type {
	case "validate":
		if strings.TrimSpace(a.Rule) == "" {
			return "validate needs a rule"
		}
		name, _, _ := strings.Cut(a.Rule, ":")
		if !knownRule(strings.TrimSpace(name)) {
			return fmt.Sprintf("unknown validation rule %q (rules: issue-cli process schema)", name)
		}
	case "require_human_approval":
		if s := strings.TrimSpace(a.Status); s != "" && !statuses[s] {
			return fmt.Sprintf("approval for %q, a status the file lacks", s)
		}
	case "append_section":
		if strings.TrimSpace(a.Title) == "" {
			return "append_section needs a title"
		}
	case "inject_prompt":
		if strings.TrimSpace(a.Prompt) == "" {
			return "inject_prompt needs a prompt"
		}
	case "set_fields":
		if strings.TrimSpace(a.Field) == "" {
			return "set_fields needs a field"
		}
	}
	return ""
}

func knownRule(name string) bool {
	for _, r := range tracker.WorkflowValidationRules {
		n, _, _ := strings.Cut(r.Name, ":")
		if n == name {
			return true
		}
	}
	return false
}
