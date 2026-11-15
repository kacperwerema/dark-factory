package shell

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestQuoteForms(t *testing.T) {
	for in, want := range map[string]string{
		"/usr/bin/issue-cli": "/usr/bin/issue-cli",
		"--if-untested":      "--if-untested",
		"cli/lead-tab":       "cli/lead-tab",
		"sess:window":        "sess:window",
		"a@b+c":              "a@b+c",
		"a b":                "'a b'",
		"it's":               `'it'\''s'`,
		"":                   "''",
		"=ls":                "'=ls'",
		"A=1":                "'A=1'",
	} {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %q, want %q", in, got, want)
		}
	}
}

// hintQuote finds a printed issue-cli, tmux or git command whose format
// quotes a value with Go's %q or a hand-written '%s' or "%s": pasted into a shell, a
// backtick or $ in the value runs. The command runs from its name to the end
// of the line or a ", " / ": " that ends the sentence.
var hintQuote = regexp.MustCompile(`(?:^|[\s(\x60'"]|%s)(?:issue-cli|tmux|git) (?:[^\n,:]|[,:][^ \n])*?(%q|'%s'|"%s)`)

// copyFindings lists what in src is a shell quoter of its own: a func named
// shellQuote or shellWord, the single-quote escape written out, or a printed
// command quoting a value with %q or '%s'.
func copyFindings(t *testing.T, path string, src any) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncDecl:
			if name := strings.ToLower(n.Name.Name); name == "shellquote" || name == "shellword" {
				out = append(out, fset.Position(n.Pos()).String()+": func "+n.Name.Name)
			}
		case *ast.BasicLit:
			v, err := strconv.Unquote(n.Value)
			if n.Kind != token.STRING || err != nil {
				break
			}
			if v == `'\''` {
				out = append(out, fset.Position(n.Pos()).String()+": the quote escape written out")
			}
			if m := hintQuote.FindStringSubmatch(v); m != nil {
				out = append(out, fset.Position(n.Pos()).String()+": "+m[1]+" in a printed command")
			}
		}
		return true
	})
	return out
}

// TestOneShellQuote: Quote is the only quoter in the tree, and every form it
// writes comes back from sh as the same one word.
func TestOneShellQuote(t *testing.T) {
	root := filepath.Join("..", "..")
	var scanned int
	for _, dir := range []string{"cmd", "internal", "tools"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				if err == nil && d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return err
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || filepath.Base(filepath.Dir(path)) == "shell" {
				return nil
			}
			scanned++
			for _, bad := range copyFindings(t, path, nil) {
				t.Errorf("%s: use shell.Quote", bad)
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if scanned < 100 {
		t.Fatalf("scanned only %d files: the walk is broken", scanned)
	}

	for _, in := range []string{
		"", " ", "plain", "a b", "it's", "''", `"`, `\`, `a\'b`, "$HOME", "$(touch pwned)", "`id`",
		"a;b", "a&b", "a|b", "a>b", "*", "?", "~", "~/x", "#x", "=ls", "A=1", "!", "-n", "--flag=x",
		"line\nbreak", "tab\there", "żółć", "sess:win", "a@b+c", "%s", "{a,b}", "[x]",
	} {
		out, err := exec.Command("sh", "-c", "printf '[%s]' "+Quote(in)).CombinedOutput()
		if err != nil {
			t.Fatalf("sh on %q (%s): %v: %s", in, Quote(in), err, out)
		}
		if want := "[" + in + "]"; string(out) != want {
			t.Errorf("%q written as %s came back as %q", in, Quote(in), out)
		}
	}
}

func TestOneShellQuoteGuardCatchesHints(t *testing.T) {
	for src, want := range map[string]int{
		"fmt.Sprintf(\"issue-cli check %s %q\", a, b)":                              1,
		"fmt.Sprintf(\"add: issue-cli append %s --section %q --body x\", a, b)":     1,
		"fmt.Sprintf(\"%stmux %s -t '%s'\", a, b, c)":                               1,
		"fmt.Sprintf(\"(git log --oneline %s --grep '%s')\", a, b)":                 1,
		"fmt.Sprintf(\"x\\n  issue-cli rounds %s --cannot-fix %q\", a, b)":          1,
		"fmt.Sprintf(`issue-cli check-edit %s %s --text %q`, a, b, c)":              1,
		"fmt.Sprintf(\"git cat-file: unexpected %q\", a)":                           0,
		"fmt.Sprintf(\"issue-cli show's output, first %q\", a)":                     0,
		"fmt.Sprintf(\"issue-cli check %s %s\", a, shell.Quote(b))":                 0,
		"fmt.Sprintf(\"issue-cli check-edit %s --text \\\"%s (visual)\\\"\", a, b)": 1,
		"fmt.Sprintf(\"digit %q\", a)":                                              0,
	} {
		file := "package x\nvar _ = " + src + "\n"
		if got := copyFindings(t, "x.go", file); len(got) != want {
			t.Errorf("%s: findings %q, want %d", src, got, want)
		}
	}
}

func TestOneShellQuoteGuardCatches(t *testing.T) {
	src := "package x\nimport \"strings\"\nfunc shellQuote(s string) string { return strings.ReplaceAll(s, \"'\", `'\\''`) }\nfunc ShellWord(s string) string { return s }\n"
	if got := copyFindings(t, "x.go", src); len(got) != 3 {
		t.Errorf("findings = %q, want the two funcs and the escape", got)
	}
}
