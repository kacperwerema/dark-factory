package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// TestSubredditsLint is AC2's check: lint `departments` warns about a row of
// a department's subreddits.md whose self-promotion rule is missing or still
// the template's placeholder, a name not shaped r/<name>, a subreddit
// listed twice and a table without its header; a commented example row, a
// filled row and no file at all are quiet.
func TestSubredditsLint(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	root := env.Project.Root()
	env.Project.Departments = []tracker.Department{{Name: "Marketing"}, {Name: "Sales"}}
	dir := filepath.Join(root, "departments", "marketing")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "subreddits.md"), []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	findings := func() string {
		var got []string
		for _, f := range checkDepartments(env) {
			if strings.Contains(f.Where, "subreddits.md") {
				got = append(got, f.Where+": "+f.Message)
			}
		}
		return strings.Join(got, "\n")
	}

	if s := findings(); s != "" {
		t.Fatalf("no subreddits.md, yet findings:\n%s", s)
	}
	write(`# Subreddits

| Subreddit | Why | Self-promotion rule | Allowed | Checked |
|---|---|---|---|---|
<!-- A row looks like this:
| r/example | <segment> | <the rule> | <types> | <date> |
-->
| r/selfhosted | devs | "Self-promotion on Saturdays only" (https://www.reddit.com/r/selfhosted/about/rules) | self | 2026-11-14 |
| r/golang | devs | — | link | 2026-11-14 |
| r/tmux | devs |  | self | 2026-11-14 |
| r/vim | devs | <the rule in its own words> | self | |
| golang | devs | "no spam" | self | |
| r/SelfHosted | devs | "again" | self | |
`)
	all := findings()
	for _, want := range []string{
		"departments/marketing/subreddits.md:9: r/golang has no self-promotion rule",
		"departments/marketing/subreddits.md:10: r/tmux has no self-promotion rule",
		"departments/marketing/subreddits.md:11: r/vim has no self-promotion rule",
		`departments/marketing/subreddits.md:12: subreddit "golang" is not shaped r/<name>`,
		"departments/marketing/subreddits.md:13: r/SelfHosted is listed twice",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	for _, quiet := range []string{"r/example", "r/selfhosted has", ":8:"} {
		if strings.Contains(all, quiet) {
			t.Errorf("%q flagged:\n%s", quiet, all)
		}
	}

	write("| Name | Rule |\n|---|---|\n| r/golang | \"no spam\" |\n")
	if s := findings(); !strings.Contains(s, "no header naming the Subreddit and Self-promotion rule columns") {
		t.Errorf("a table without the header isn't flagged:\n%s", s)
	}

	write("# Subreddits\n\nNone yet.\n\n| Subreddit | Why | Self-promotion rule | Allowed | Checked |\n|---|---|---|---|---|\n")
	if s := findings(); s != "" {
		t.Errorf("the empty starter is flagged:\n%s", s)
	}
}
