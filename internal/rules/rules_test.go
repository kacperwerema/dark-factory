package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	got, stray := Parse(`# Rules

- [no-force] Never force-push main.
- Keep commits small
  and on one topic.
* [a_b-2] Star bullets count.

<!-- a comment
that spans -->
A paragraph that reaches nobody.
`)
	if len(got) != 3 {
		t.Fatalf("rules: %+v", got)
	}
	if got[0].Key != "no-force" || got[0].Text != "Never force-push main." || got[0].Line != 3 {
		t.Errorf("keyed rule: %+v", got[0])
	}
	if got[1].Key != "" || got[1].Text != "Keep commits small and on one topic." {
		t.Errorf("continued rule: %+v", got[1])
	}
	if got[2].Key != "a_b-2" {
		t.Errorf("star bullet: %+v", got[2])
	}
	if len(stray) != 1 || stray[0] != 10 {
		t.Errorf("stray: %v", stray)
	}
}

func TestLoadMachine(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	f, err := LoadMachine()
	if err != nil || len(f.Rules) != 0 {
		t.Fatalf("no file: %+v %v", f, err)
	}
	p := MachinePath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("- one rule\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if f, err = LoadMachine(); err != nil || len(f.Rules) != 1 {
		t.Fatalf("file: %+v %v", f, err)
	}
}

func TestConflicts(t *testing.T) {
	machine := []Rule{
		{Key: "no-force", Text: "Never force-push main.", Line: 1},
		{Text: "Keep commits small and on one topic.", Line: 2},
	}
	cases := []struct {
		name string
		rule Rule
		want string
	}{
		{"opposite wording", Rule{Text: "Always force-push main before a release."}, "wording"},
		{"same name", Rule{Key: "no-force", Text: "Rebase before a merge."}, "name"},
		{"unkeyed machine rule", Rule{Text: "Don't keep commits small."}, "wording"},
		{"declared narrowing", Rule{Key: "x", Text: "Force-push main only on a tag day.", Narrows: "no-force"}, ""},
		{"declared with the same name", Rule{Key: "no-force", Text: "Never force-push any branch.", Narrows: "no-force"}, ""},
		{"same direction", Rule{Text: "Never force-push main or release branches."}, ""},
		{"another subject", Rule{Text: "Run the linter before each commit."}, ""},
		{"one shared word", Rule{Text: "Don't edit main."}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Conflicts(machine, []Rule{c.rule})
			switch {
			case c.want == "" && len(got) != 0:
				t.Errorf("a false conflict: %+v", got)
			case c.want != "" && (len(got) != 1 || got[0].Why != c.want):
				t.Errorf("want %s: %+v", c.want, got)
			}
		})
	}
}

func TestCutKeepsWholeRules(t *testing.T) {
	long := Rule{Text: strings.Repeat("x", BriefChars-10)}
	shown, more := Cut([]Rule{long, {Text: strings.Repeat("y", 20)}, {Text: "z"}})
	if len(shown) != 1 || more != 2 {
		t.Errorf("shown %d, more %d", len(shown), more)
	}
	if shown, more := Cut([]Rule{{Text: strings.Repeat("x", BriefChars+1)}}); len(shown) != 1 || more != 0 {
		t.Errorf("one long rule is still shown: %d, %d", len(shown), more)
	}
}
