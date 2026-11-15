package docnumbers

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const root = "../.."

// TestDocNumbersInSync: docs/numbers.md is what the constants say today.
func TestDocNumbersInSync(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root, Page))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != Render() {
		t.Errorf("%s is stale: run go generate ./internal/docnumbers/...", Page)
	}
	src, err := os.ReadFile("numbers.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "//go:generate go run ../../tools/doc-numbers ../../"+Page+"\n") {
		t.Error("numbers.go lost its go:generate line")
	}
}

// scanned is every text that may quote a number, by its path from the root:
// the docs, CLAUDE.md, the bundled templates, the pages and the code's own
// strings. Tests, fixtures, the generated page and this package are not.
func scanned(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	read := func(rel string) {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		out[rel] = string(raw)
	}
	read("CLAUDE.md")
	read("README.md")
	for _, dir := range []string{"docs", "cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if d.Name() == "testdata" || rel == "internal/docnumbers" {
					return fs.SkipDir
				}
				return nil
			}
			switch filepath.Ext(p) {
			case ".md", ".yaml", ".html", ".go":
			default:
				return nil
			}
			if rel != Page && !strings.HasSuffix(p, "_test.go") {
				read(rel)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// disagreements is each place in texts that quotes n with another value,
// and the copies that match nowhere.
func disagreements(n Number, texts map[string]string) []string {
	var out []string
	for _, c := range n.Copies {
		re := regexp.MustCompile(c)
		found := false
		for path, text := range texts {
			for _, m := range re.FindAllStringSubmatch(text, -1) {
				found = true
				if v, err := strconv.Atoi(m[1]); err != nil || v != n.Value {
					out = append(out, fmt.Sprintf("%s: %q says %s, %s is %s", path, m[0], m[1], n.Source, n.Text()))
				}
			}
		}
		if !found {
			out = append(out, fmt.Sprintf("%s: nothing quotes %q any more: drop it from Copies", n.Name, c))
		}
	}
	return out
}

// TestDocNumbersCopiesAgree: a sentence that quotes a number reads the
// constant's value, wherever it sits. Fix a failure by changing the sentence
// (the constant is the source).
func TestDocNumbersCopiesAgree(t *testing.T) {
	texts := scanned(t)
	for _, n := range Numbers() {
		for _, d := range disagreements(n, texts) {
			t.Error(d)
		}
	}
}

func TestDisagreementsCatchADriftedCopy(t *testing.T) {
	n := Number{Name: "Idle gap", Value: 15, Unit: "min", Source: "timeline.IdleAfter", Copies: []string{`(\d+) min or more`}}
	if d := disagreements(n, map[string]string{"a.md": "a gap of 15 min or more"}); len(d) != 0 {
		t.Errorf("an agreeing copy: %v", d)
	}
	d := disagreements(n, map[string]string{"a.md": "a gap of 15 min or more", "b.md": "a gap of 20 min or more"})
	if len(d) != 1 || !strings.Contains(d[0], "b.md") || !strings.Contains(d[0], "says 20") {
		t.Errorf("a drifted copy: %v", d)
	}
	d = disagreements(n, map[string]string{"a.md": "no such sentence"})
	if len(d) != 1 || !strings.Contains(d[0], "drop it from Copies") {
		t.Errorf("a copy that matches nothing: %v", d)
	}
}

func TestTextAndRender(t *testing.T) {
	for _, c := range []struct {
		n    Number
		want string
	}{
		{Number{Value: 15, Unit: "min"}, "15 min"},
		{Number{Value: 50, Unit: "%"}, "50%"},
		{Number{Value: 12}, "12"},
		{Number{Value: 30, Unit: "days"}, "30 days"},
	} {
		if got := c.n.Text(); got != c.want {
			t.Errorf("Text = %q, want %q", got, c.want)
		}
	}
	page := Render()
	for _, n := range Numbers() {
		if !strings.Contains(page, "| "+n.Name+" | "+n.Text()+" |") {
			t.Errorf("the page has no row for %s", n.Name)
		}
	}
}
