package doclinks

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
)

func TestMain(m *testing.M) { tmuxguard.Main(m) }

func TestLinksSkipsCodeAndExternal(t *testing.T) {
	md := strings.Join([]string{
		"See [a](a.md) and [b](sub/b.md#part) and ![img](img/x.png \"title\").",
		"[web](https://example.com) [mail](mailto:x@y) [abs](/docs/x) [frag](#here)",
		"Inline `[no](code.md)` is code.",
		"```markdown",
		"[fenced](fenced.md)",
		"```",
		"[space](my%20doc.md)",
	}, "\n")
	got := Links([]byte(md))
	want := []Link{{1, "a.md"}, {1, "sub/b.md"}, {1, "img/x.png"}, {7, "my doc.md"}}
	if len(got) != len(want) {
		t.Fatalf("Links = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Links[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestCheckReportsMissingTargets(t *testing.T) {
	root := t.TempDir()
	must := func(p, body string) {
		t.Helper()
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("docs/other.md", "x")
	must("docs/a.md", "[ok](other.md)\n[bad](other)\n[up](../README.md)\n")
	got, err := Check(root, []string{"docs/a.md"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].String() != "docs/a.md:2: other" || got[1].String() != "docs/a.md:3: ../README.md" {
		t.Errorf("Check = %v", got)
	}
}

// TestCheckReadsTheExactCase is why Check compares names and not only os.Stat:
// on a Mac's case-insensitive disk Stat finds workflow/ for docs/Workflow/,
// so such a link passed here and broke on Linux. The test holds on both.
func TestCheckReadsTheExactCase(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	for p, body := range map[string]string{
		"outside.md":                 "x",
		"repo/README.md":             "x",
		"repo/docs/cafe\u0301.md":    "x", // decomposed, as a Mac may store it
		"repo/docs/other.md":         "x",
		"repo/docs/Workflow/rule.md": "x",
		"repo/docs/a.md": strings.Join([]string{
			"[dir](workflow/rule.md)",
			"[dir and fragment](workflow/rule.md#x)",
			"[file](OTHER.md)",
			"[parent](../readme.md)",
			"[bare dir](workflow)",
			"[composed](caf\u00e9.md)",
			"[ok](Workflow/rule.md)",
			"[ok dir](Workflow/)",
			"[ok up](../README.md)",
			"[ok here](./other.md)",
			"[ok through](Workflow/../other.md)",
			"[ok root](../)",
			"[ok outside the root](../../outside.md)",
		}, "\n") + "\n",
	} {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Check(repo, []string{"docs/a.md"})
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, b := range got {
		lines = append(lines, fmt.Sprintf("%d: %s", b.Line, b.Target))
	}
	want := []string{
		"1: workflow/rule.md",
		"2: workflow/rule.md",
		"3: OTHER.md",
		"4: ../readme.md",
		"5: workflow",
		"6: caf\u00e9.md",
	}
	if !slices.Equal(lines, want) {
		t.Errorf("Check = %q, want %q", lines, want)
	}
	b := Broken{File: "docs/a.md", Line: 1, Target: "workflow/rule.md", Note: "case differs, on disk: Workflow"}
	if s := b.String(); s != "docs/a.md:1: workflow/rule.md (case differs, on disk: Workflow)" {
		t.Errorf("String = %q", s)
	}
}

// TestRepoDocLinks is the link check (make link-check): every relative link
// in the repo's tracked markdown points at a file. Issue files, scenario
// fixtures and testdata are left out: their links point into other trees.
func TestRepoDocLinks(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", root, "ls-files", "-z", "--", "*.md").Output()
	if err != nil {
		t.Skipf("git ls-files: %v", err)
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f == "" || skipped(f) {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			continue // not in this checkout (sparse)
		}
		files = append(files, f)
	}
	broken, err := Check(root, files)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range broken {
		t.Errorf("broken link %s", b)
	}
}

func skipped(f string) bool {
	if strings.HasPrefix(f, "issues/") || strings.HasPrefix(f, "playtest/scenarios/") {
		return true
	}
	return strings.Contains("/"+f, "/testdata/")
}
