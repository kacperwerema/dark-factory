package connectors

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// AtError is connector c's at: answer for a tools/call of tool with args
// (the call's params.arguments, raw) at status: nil when it passes. A status
// at: doesn't name allows no tool; an entry's allow:, when set, names every
// tool it allows; deny_args: refuses a call whose argument holds a listed
// value (a string as is, any other value by its JSON text). The argument is
// a top-level name, or a path such as socialPost[].type into objects and
// every item of an array (argHits); a malformed path refuses every call of
// that tool, so a typo never opens it. An argument the call leaves out
// passes. Nothing here names a tool: it is all
// config (factory/factory-departments-floor-map-and-capabilities#D-9).
func AtError(addr string, c *tracker.Connector, status, tool string, args json.RawMessage) error {
	if c == nil || len(c.At) == 0 {
		return nil
	}
	at, ok := c.At[status]
	if !ok {
		for k, v := range c.At {
			if strings.EqualFold(strings.TrimSpace(k), strings.TrimSpace(status)) {
				at, ok = v, true
			}
		}
	}
	if !ok {
		return fmt.Errorf("connector %s at %s: no tools are allowed (at: names %s); issue-cli connectors lists what each status allows", addr, status, strings.Join(c.AtStatuses(), ", "))
	}
	if len(at.Allow) > 0 && !slices.Contains(trimmed(at.Allow), tool) {
		return fmt.Errorf("connector %s at %s: %s is not allowed (at: %s allows %s)", addr, status, tool, status, strings.Join(trimmed(at.Allow), ", "))
	}
	deny := at.DenyArgs[tool]
	if len(deny) == 0 {
		return nil
	}
	var got any
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.UseNumber() // a number keeps its own text, as the call wrote it
	_ = dec.Decode(&got)
	for _, key := range sortedKeys(deny) {
		arg := strings.TrimSpace(key)
		if err := tracker.ArgPathError(arg); err != nil {
			return fmt.Errorf("connector %s at %s: %s is refused: at: %s: deny_args: %s: %v, so no call of %s passes until it is fixed (issue-cli health lint names it)", addr, status, tool, status, tool, err, tool)
		}
		for _, h := range argHits(got, arg) {
			v := argText(h.raw)
			for _, bad := range deny[key] {
				if v == strings.TrimSpace(bad) {
					return fmt.Errorf("connector %s at %s: %s with %s: %s is refused (at: %s: deny_args:); another value of %s passes", addr, status, tool, h.at, v, status, arg)
				}
			}
		}
	}
	return nil
}

// argHit is one value a deny_args: key reaches in a call's arguments: the
// value as JSON and where it sits (socialPost[1].type).
type argHit struct {
	at  string
	raw json.RawMessage
}

// argHits are the values key reaches in args (the decoded arguments). A
// plain name is the top-level argument of that name, as written. A path
// walks names into objects and "[]" over every item of an array; "[]" on
// a value that isn't an array takes the value as the one item, so a server
// that accepts an object for a list can't be slipped past. A step that
// finds no object or no such key reaches nothing there.
func argHits(args any, key string) []argHit {
	obj, _ := args.(map[string]any)
	if !tracker.IsArgPath(key) {
		v, ok := obj[key]
		if !ok {
			return nil
		}
		return []argHit{{at: key, raw: mustJSON(v)}}
	}
	type node struct {
		at string
		v  any
	}
	cur := []node{{v: args}}
	for i, seg := range strings.Split(key, ".") {
		name := strings.TrimRight(seg, "[]")
		lists := (len(seg) - len(name)) / 2
		var next []node
		for _, n := range cur {
			o, ok := n.v.(map[string]any)
			if !ok {
				continue
			}
			v, ok := o[name]
			if !ok {
				continue
			}
			at := name
			if i > 0 {
				at = n.at + "." + name
			}
			next = append(next, node{at, v})
		}
		for range lists {
			var items []node
			for _, n := range next {
				list, ok := n.v.([]any)
				if !ok {
					items = append(items, n)
					continue
				}
				for j, item := range list {
					items = append(items, node{fmt.Sprintf("%s[%d]", n.at, j), item})
				}
			}
			next = items
		}
		cur = next
	}
	out := make([]argHit, 0, len(cur))
	for _, n := range cur {
		out = append(out, argHit{at: n.at, raw: mustJSON(n.v)})
	}
	return out
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// AtCallerError is the at: refusal for a caller who has no issue to read a
// status from (a lead or the CEO); nil for the owner.
func AtCallerError(addr string, caller Caller) error {
	if caller.Owner || caller.Issue != "" {
		return nil
	}
	return fmt.Errorf("connector %s allows its tools per issue status (at:): it runs for a worker on an issue, or for the owner with --as-owner in their own shell", addr)
}

// argText is an argument's value as at: compares it: a JSON string
// unquoted, anything else compacted.
func argText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var b bytes.Buffer
	if json.Compact(&b, raw) == nil {
		return b.String()
	}
	return strings.TrimSpace(string(raw))
}

func trimmed(list []string) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		out = append(out, strings.TrimSpace(s))
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
