package tracker

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

// The attribute group: a D-n line may end with
//
//	{scope: <s>; hard; at: <status>, <status>; paths: <glob>, <glob>; expires: <YYYY-MM-DD>; ended: <YYYY-MM-DD>}
//
// after its quote, "(also considered …)" and "(replaces …)". It says whom
// the decision reaches (scope, at:), whether it is the owner's hard rule
// (hard, paths: the files it guards) and until when (expires:; ended: is
// the day the owner let an expired one go, decide --let-go). A trailing
// {…} counts as the group only when every part is one of these keys with a
// value that parses; anything else stays part of the text, as it was before
// the group existed (`{from: marketing, to: product, via: hq}`).

// Decision scopes. A scope with a name is "<kind>:<name>".
const (
	ScopeIssue      = "issue" // the default: this issue, issues below it, issues citing it
	ScopeProject    = "project"
	ScopeSystem     = "system"
	ScopeDepartment = "department"
	ScopeType       = "type"
)

// DecisionAttrs is a D-n line's attribute group. The zero value (scope
// issue, soft, every status, no paths, no expiry) is a line without one.
type DecisionAttrs struct {
	// Scope is "issue" (also when empty), "project", "system:<S>",
	// "department:<key>" or "type:<t>".
	Scope string `json:"scope"`
	Hard  bool   `json:"hard,omitempty"`
	// At lists the statuses where the decision matters; empty is all.
	At []string `json:"at,omitempty"`
	// Paths are the globs a hard decision guards (tracker.MatchGlob).
	Paths []string `json:"paths,omitempty"`
	// Expires is the last day the decision is in force (YYYY-MM-DD).
	Expires string `json:"expires,omitempty"`
	// Ended is the day an expired decision was let go (YYYY-MM-DD): it is
	// closed out and waits on no one; renewing it is refused.
	Ended string `json:"ended,omitempty"`
}

var decisionAttrsRe = regexp.MustCompile(`\s\{([^{}]*)\}$`)

// ScopeOrDefault is the scope, "issue" when unset.
func (a DecisionAttrs) ScopeOrDefault() string {
	if s := strings.TrimSpace(a.Scope); s != "" {
		return s
	}
	return ScopeIssue
}

// ScopeKind splits the scope into its kind and name ("system", "Workflow").
func (a DecisionAttrs) ScopeKind() (kind, name string) {
	kind, name, _ = strings.Cut(a.ScopeOrDefault(), ":")
	return kind, strings.TrimSpace(name)
}

// Empty reports whether the group says nothing a line without one wouldn't.
func (a DecisionAttrs) Empty() bool {
	return a.ScopeOrDefault() == ScopeIssue && !a.Hard && len(a.At) == 0 && len(a.Paths) == 0 && a.Expires == "" && a.Ended == ""
}

// ExpiresOn is the expiry day, ok false when there is none.
func (a DecisionAttrs) ExpiresOn() (time.Time, bool) {
	if a.Expires == "" {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation(decisionDateLayout, a.Expires, time.Local)
	return t, err == nil
}

// ExpiredOn reports whether the decision is out of force on day today: the
// day after its expires: date.
func (a DecisionAttrs) ExpiredOn(today time.Time) bool {
	exp, ok := a.ExpiresOn()
	if !ok || today.IsZero() {
		return false
	}
	y, m, d := today.Date()
	return exp.Before(time.Date(y, m, d, 0, 0, 0, 0, exp.Location()))
}

// AtStatus reports whether the decision matters at status (an empty at:
// is every status).
func (a DecisionAttrs) AtStatus(status string) bool {
	if len(a.At) == 0 {
		return true
	}
	for _, s := range a.At {
		if strings.EqualFold(strings.TrimSpace(s), strings.TrimSpace(status)) {
			return true
		}
	}
	return false
}

// RenderAttrs writes the group in its fixed key order (scope, hard, at,
// paths, expires, ended), or "" when it is empty.
func RenderAttrs(a DecisionAttrs) string {
	if a.Empty() {
		return ""
	}
	var parts []string
	if s := a.ScopeOrDefault(); s != ScopeIssue {
		parts = append(parts, "scope: "+s)
	}
	if a.Hard {
		parts = append(parts, "hard")
	}
	if len(a.At) > 0 {
		parts = append(parts, "at: "+strings.Join(a.At, ", "))
	}
	if len(a.Paths) > 0 {
		parts = append(parts, "paths: "+strings.Join(a.Paths, ", "))
	}
	if a.Expires != "" {
		parts = append(parts, "expires: "+a.Expires)
	}
	if a.Ended != "" {
		parts = append(parts, "ended: "+a.Ended)
	}
	return "{" + strings.Join(parts, "; ") + "}"
}

// splitAttrsTail finds the attribute group at the end of a line: the line
// without it, the group, and where it starts (-1 when the line has none or
// its trailing {…} is not a group).
func splitAttrsTail(line string) (head string, a DecisionAttrs, at int) {
	m := decisionAttrsRe.FindStringSubmatchIndex(line)
	if m == nil {
		return line, DecisionAttrs{}, -1
	}
	a, err := ParseAttrs(line[m[2]:m[3]])
	if err != nil {
		return line, DecisionAttrs{}, -1
	}
	return line[:m[0]], a, m[0]
}

// ParseAttrs reads the inside of a group ("scope: project; hard"). Every
// part must be a known key with a value that parses, else it is an error.
func ParseAttrs(inner string) (DecisionAttrs, error) {
	var a DecisionAttrs
	seen := map[string]bool{}
	parts := strings.Split(inner, ";")
	if strings.TrimSpace(inner) == "" {
		return a, fmt.Errorf("empty group")
	}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		key, val, hasVal := strings.Cut(part, ":")
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		if seen[key] {
			return a, fmt.Errorf("%s twice", key)
		}
		seen[key] = true
		switch {
		case key == "hard" && !hasVal:
			a.Hard = true
		case key == "scope" && hasVal:
			s, err := NormalizeScope(val)
			if err != nil {
				return a, err
			}
			a.Scope = s
		case key == "at" && hasVal:
			list, err := splitAttrList(val)
			if err != nil {
				return a, fmt.Errorf("at: %w", err)
			}
			a.At = list
		case key == "paths" && hasVal:
			list, err := splitAttrList(val)
			if err != nil {
				return a, fmt.Errorf("paths: %w", err)
			}
			for _, g := range list {
				if err := ValidateGlob(g); err != nil {
					return a, err
				}
			}
			a.Paths = list
		case key == "expires" && hasVal:
			if _, err := time.Parse(decisionDateLayout, val); err != nil {
				return a, fmt.Errorf("expires: %q is not a YYYY-MM-DD date", val)
			}
			a.Expires = val
		case key == "ended" && hasVal:
			if _, err := time.Parse(decisionDateLayout, val); err != nil {
				return a, fmt.Errorf("ended: %q is not a YYYY-MM-DD date", val)
			}
			a.Ended = val
		default:
			return a, fmt.Errorf("%q is not a decision attribute (scope, hard, at, paths, expires, ended)", part)
		}
	}
	if a.Scope == "" {
		a.Scope = ScopeIssue
	}
	return a, nil
}

// SplitAttrList splits a flag's comma list ("in design, in progress") into
// its trimmed items; "" is an empty list.
func SplitAttrList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	list, _ := splitAttrList(s)
	return list
}

func splitAttrList(s string) ([]string, error) {
	var out []string
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			return nil, fmt.Errorf("an empty item in %q", s)
		}
		out = append(out, item)
	}
	return out, nil
}

// NormalizeScope checks a scope's form: issue, project, or system:,
// department:, type: with a name. Kinds are lower-cased; names kept.
func NormalizeScope(s string) (string, error) {
	s = strings.TrimSpace(s)
	kind, name, hasName := strings.Cut(s, ":")
	kind, name = strings.ToLower(strings.TrimSpace(kind)), strings.TrimSpace(name)
	switch kind {
	case ScopeIssue, ScopeProject:
		if hasName {
			return "", fmt.Errorf("scope %q: %s takes no name", s, kind)
		}
		return kind, nil
	case ScopeSystem, ScopeDepartment, ScopeType:
		if name == "" || strings.ContainsAny(name, "{};,") {
			return "", fmt.Errorf("scope %q: want %s:<name>", s, kind)
		}
		return kind + ":" + name, nil
	}
	return "", fmt.Errorf("scope %q: want issue, project, system:<S>, department:<key> or type:<t>", s)
}

// ValidateGlob checks a paths: glob compiles (tracker.MatchGlob syntax).
func ValidateGlob(g string) error {
	if strings.ContainsAny(g, " {};") {
		return fmt.Errorf("paths: %q: a glob has no spaces, braces or ';'", g)
	}
	if _, err := path.Match(strings.ReplaceAll(g, "**", "*"), "x"); err != nil {
		return fmt.Errorf("paths: %q: %v", g, err)
	}
	return nil
}
