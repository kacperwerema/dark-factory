package depttemplate

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/cmd/issue-cli/templates"
)

// sharedTexts are the lines department templates share. Each has one source,
// departments/_shared/<file>; a template that uses one quotes it word for
// word. Installed files are plain text with no include step, so the quote is
// a copy, and this table is what keeps it one: the trigger is what marks a
// use, so a reworded copy is caught by still naming the trigger.
var sharedTexts = []struct {
	file, trigger string
	docs          bool // docs/departments.md quotes it too
}{
	{file: "heavy-tools.md", trigger: "mem-guard check", docs: true},
	{file: "publish-exactly.md", trigger: "Publish exactly"},
	{file: "published-box.md", trigger: "[ ] Published:"},
	{file: "shelf-first.md", trigger: "on the shelf first (/p/"},
	{file: "agpl-changes.md", trigger: "publish your changes", docs: true},
	{file: "stamp-header.md", trigger: "# Bundled template stamp"},
}

// oneLine folds wrapped text: role.md wraps its lines, a workflow prompt
// doesn't, and the words are what must match.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// quoteDrift is what is wrong with text's uses of snippet, "" when every use
// of trigger is the snippet word for word.
func quoteDrift(text, snippet, trigger string) string {
	text, snippet = oneLine(text), oneLine(snippet)
	uses, quotes := strings.Count(text, trigger), strings.Count(text, snippet)
	if uses == quotes {
		return ""
	}
	return fmt.Sprintf("names %q %d times, carries the snippet %d times", trigger, uses, quotes)
}

func sharedFile(t *testing.T, name string) string {
	t.Helper()
	raw, err := fs.ReadFile(templates.FS, path.Join(templatesDir, "_shared", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// templateTexts is every bundled text a department or a workflow template
// installs, by its path in the embedded folder.
func templateTexts(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, dir := range []string{templatesDir, "workflow", "jobs"} {
		err := fs.WalkDir(templates.FS, dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "_shared" {
					return fs.SkipDir
				}
				return nil
			}
			raw, err := fs.ReadFile(templates.FS, p)
			out[p] = string(raw)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// TestSharedTextsQuotedWordForWord: every template that names a shared line
// carries it word for word. Fix a failure by copying the file in
// departments/_shared, or change that file and every quote together.
func TestSharedTextsQuotedWordForWord(t *testing.T) {
	texts := templateTexts(t)
	if len(texts) == 0 {
		t.Fatal("no template texts read")
	}
	docs, err := os.ReadFile(filepath.Join("..", "..", "docs", "departments.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sharedTexts {
		snippet := sharedFile(t, s.file)
		quoted := 0
		for p, text := range texts {
			if d := quoteDrift(text, snippet, s.trigger); d != "" {
				t.Errorf("%s %s (source: departments/_shared/%s)", p, d, s.file)
			}
			quoted += strings.Count(oneLine(text), oneLine(snippet))
		}
		if quoted == 0 {
			t.Errorf("no template quotes departments/_shared/%s: its trigger or the file is stale", s.file)
		}
		if s.docs && !strings.Contains(oneLine(string(docs)), oneLine(snippet)) {
			t.Errorf("docs/departments.md doesn't quote departments/_shared/%s", s.file)
		}
	}
}

func TestQuoteDriftCatchesAReword(t *testing.T) {
	snippet := sharedFile(t, "heavy-tools.md")
	wrapped := strings.Replace(snippet, "starts a browser", "starts\n  a browser", 1)
	if d := quoteDrift("intro "+wrapped+" outro", snippet, "mem-guard check"); d != "" {
		t.Errorf("a wrapped quote is a quote: %s", d)
	}
	if d := quoteDrift("Run `mem-guard check` before a model load.", snippet, "mem-guard check"); d == "" {
		t.Error("a reworded copy passed")
	}
	if d := quoteDrift(snippet+"\nAlso `mem-guard check` first.", snippet, "mem-guard check"); d == "" {
		t.Error("a second, reworded use beside a quote passed")
	}
	if d := quoteDrift("nothing about memory", snippet, "mem-guard check"); d != "" {
		t.Errorf("a template that doesn't name it needs no quote: %s", d)
	}
}

// TestSharedIsNoTemplate: the _shared folder sits among the templates and
// must not show up as a department to add.
func TestSharedIsNoTemplate(t *testing.T) {
	for _, tm := range List() {
		if strings.HasPrefix(tm.Key, "_") {
			t.Errorf("template %q listed", tm.Key)
		}
	}
}
