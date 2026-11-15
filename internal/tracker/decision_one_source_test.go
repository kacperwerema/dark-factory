package tracker

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestDecisionKeysHaveOneSource holds every written list of the attribute
// group's keys to decisionKeyOrder: the docs that spell it out, the parser,
// the renderer and the fields of DecisionAttrs.
func TestDecisionKeysHaveOneSource(t *testing.T) {
	want := DecisionKeys()
	full := DecisionAttrs{Scope: ScopeProject, Hard: true, At: []string{"testing"}, Paths: []string{"cmd/**"}, Expires: "2027-01-01", Ended: "2027-01-02"}
	if n := reflect.TypeOf(full).NumField(); n != len(want) {
		t.Errorf("DecisionAttrs has %d fields, DecisionKeys %d: %v", n, len(want), want)
	}
	if got := groupKeys(strings.Trim(RenderAttrs(full), "{}")); !slices.Equal(got, want) {
		t.Errorf("RenderAttrs writes keys %v, want %v", got, want)
	}
	for _, key := range want {
		group := key + ": 2027-01-01"
		switch key {
		case "scope":
			group = "scope: project"
		case "hard":
			group = "hard"
		}
		if _, err := ParseAttrs(group); err != nil {
			t.Errorf("ParseAttrs(%q): %v", group, err)
		}
	}

	group := regexp.MustCompile("`\\{(scope:[^`]*)\\}`")
	for _, c := range []struct {
		file string
		read func(string) []string
	}{
		{"docs/issue-format.md", func(s string) []string { return firstGroup(group, s) }},
		{"CLAUDE.md", func(s string) []string { return firstGroup(group, s) }},
		{"docs/workflow.md", func(s string) []string {
			var keys []string
			for _, m := range regexp.MustCompile("(?m)^- \\*\\*[A-Z][a-z]+\\*\\* \\(`([a-z]+):?`\\):").FindAllStringSubmatch(ledgerSection(s), -1) {
				keys = append(keys, m[1])
			}
			return keys
		}},
		{"docs/workbench-files.md", func(s string) []string {
			m := regexp.MustCompile(`names a decision key \(([^)]*)\)`).FindStringSubmatch(s)
			if m == nil {
				return nil
			}
			return strings.Split(m[1], ", ")
		}},
	} {
		if got := c.read(readRepoFile(t, c.file)); !slices.Equal(got, want) {
			t.Errorf("%s lists the keys %v, want %v (decisionKeyOrder)", c.file, got, want)
		}
	}
	if s := readRepoFile(t, "docs/issue-format.md"); regexp.MustCompile(`(?i)\b(one|two|three|four|five|six|seven|eight) keys\b`).MatchString(s) {
		t.Error("docs/issue-format.md counts the keys in words: a count drifts, name them")
	}
}

// TestScopeKindsHaveOneSource: ScopeKinds runs most specific first, each
// kind is one NormalizeScope accepts, and the doc's list follows it.
func TestScopeKindsHaveOneSource(t *testing.T) {
	want := []string{ScopeIssue, ScopeType, ScopeSystem, ScopeDepartment, ScopeProject}
	if got := ScopeKinds(); !slices.Equal(got, want) {
		t.Fatalf("ScopeKinds = %v, want %v", got, want)
	}
	for _, k := range want {
		s := k
		if k != ScopeIssue && k != ScopeProject {
			s += ":x"
		}
		if _, err := NormalizeScope(s); err != nil {
			t.Errorf("NormalizeScope(%q): %v", s, err)
		}
	}
	m := regexp.MustCompile("`decisions.in_force_by_scope` \\| `scope` \\(([^)]*)\\)").FindStringSubmatch(readRepoFile(t, "docs/workbench-files.md"))
	if m == nil {
		t.Fatal("docs/workbench-files.md: no decisions.in_force_by_scope row")
	}
	if got := strings.Split(strings.ReplaceAll(m[1], "`", ""), ", "); !slices.Equal(got, want) {
		t.Errorf("docs/workbench-files.md lists scopes %v, want %v", got, want)
	}
}

// TestActiveCheckHasOneHome refuses the "not replaced, not deferred, not
// expired" check written inline: every reader goes through ActiveOn.
func TestActiveCheckHasOneHome(t *testing.T) {
	inline := regexp.MustCompile(`ReplacedBy (!= nil \|\| \w+\.Deferred|== nil && !\w+\.Deferred)`)
	for _, root := range []string{"../../internal", "../../cmd"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			if strings.HasSuffix(path, "internal/tracker/decision_expiry.go") {
				return nil // ActiveOn itself
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if m := inline.Find(b); m != nil {
				t.Errorf("%s: %q: use DecisionEntry.ActiveOn", path, m)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestActiveOnAndProvisionalOn(t *testing.T) {
	today := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	yolo := Decision{How: "YOLO, provisional", Text: "x", Quote: ""}
	cases := []struct {
		name           string
		e              DecisionEntry
		active, marked bool
		needsWords     bool
	}{
		{"marked", DecisionEntry{Decision: yolo}, true, true, true},
		{"no quote, no mark", DecisionEntry{Decision: Decision{How: "chat", Text: "x"}}, true, false, true},
		{"quoted", DecisionEntry{Decision: Decision{How: "chat", Text: "x", Quote: "yes"}}, true, false, false},
		{"confirmed, no quote", DecisionEntry{Decision: Decision{How: "YOLO, confirmed by owner 2026-11-01", Text: "x"}}, true, false, false},
		{"replaced", DecisionEntry{Decision: yolo, ReplacedBy: &DecisionEntry{}}, false, false, true},
		{"deferred", DecisionEntry{Decision: Decision{How: "YOLO", Text: "x", Deferred: true}}, false, false, true},
		{"expired", DecisionEntry{Decision: Decision{How: "YOLO", Text: "x", DecisionAttrs: DecisionAttrs{Expires: "2026-11-03"}}}, false, false, true},
		{"last day", DecisionEntry{Decision: Decision{How: "YOLO", Text: "x", DecisionAttrs: DecisionAttrs{Expires: "2026-11-04"}}}, true, true, true},
	}
	for _, c := range cases {
		e := c.e
		if got := e.ActiveOn(today); got != c.active {
			t.Errorf("%s: ActiveOn = %v, want %v", c.name, got, c.active)
		}
		if got := e.ProvisionalOn(today); got != c.marked {
			t.Errorf("%s: ProvisionalOn = %v, want %v", c.name, got, c.marked)
		}
		if got := NeedsOwnerWords(e.Decision); got != c.needsWords {
			t.Errorf("%s: NeedsOwnerWords = %v, want %v", c.name, got, c.needsWords)
		}
		if IsProvisional(e.Decision) && !NeedsOwnerWords(e.Decision) {
			t.Errorf("%s: provisional but not waiting for the owner's words", c.name)
		}
	}
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func firstGroup(re *regexp.Regexp, s string) []string {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	return groupKeys(m[1])
}

// groupKeys is the keys of an attribute group's inside, in order.
func groupKeys(group string) []string {
	var keys []string
	for _, part := range strings.Split(group, ";") {
		key, _, _ := strings.Cut(part, ":")
		keys = append(keys, strings.TrimSpace(key))
	}
	return keys
}

// ledgerSection is docs/workflow.md's Decision ledger section.
func ledgerSection(s string) string {
	_, rest, _ := strings.Cut(s, "## Decision ledger\n")
	section, _, _ := strings.Cut(rest, "\n## ")
	return section
}
