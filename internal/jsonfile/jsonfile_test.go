package jsonfile

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := Write(path, map[string]int{"a": 1}, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n  \"a\": 1\n}\n"; string(got) != want {
		t.Errorf("file = %q, want %q", got, want)
	}
	for _, perm := range []os.FileMode{0o600, 0o644} {
		if err := Write(path, 1, perm); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != perm {
			t.Errorf("mode = %o, want %o", info.Mode().Perm(), perm)
		}
	}

	// A value that can't be written leaves the old file, a failed rename no
	// temp file.
	if err := Write(path, make(chan int), 0o600); err == nil {
		t.Error("a channel marshalled")
	}
	if got, _ := os.ReadFile(path); string(got) != "1\n" {
		t.Errorf("after a failed marshal the file is %q", got)
	}
	into := filepath.Join(dir, "a-folder")
	if err := os.MkdirAll(filepath.Join(into, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Write(into, 1, 0o600); err == nil {
		t.Error("a write over a non-empty folder passed")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("dir holds %v, want the file and the folder only", entries)
	}
}

// writeFindings lists each function in src that marshals JSON and renames a
// file itself, or hands the bytes to an atomic byte writer (a function
// named like writeFileAtomically, writeAtomic, gitfile.WriteAtomic): the
// write this package is the one copy of.
func writeFindings(t *testing.T, path string, src any) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := func(body ast.Node, pkg string, names ...string) bool {
		found := false
		ast.Inspect(body, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == pkg {
				for _, name := range names {
					found = found || sel.Sel.Name == name
				}
			}
			return true
		})
		return found
	}
	// atomicWrite: a call to a function whose name says it writes atomically.
	atomicWrite := func(body ast.Node) bool {
		found := false
		ast.Inspect(body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			var name string
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				name = fun.Name
			case *ast.SelectorExpr:
				name = fun.Sel.Name
			}
			name = strings.ToLower(name)
			found = found || strings.HasPrefix(name, "write") && strings.Contains(name, "atomic")
			return true
		})
		return found
	}
	var out []string
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if name := strings.ToLower(fn.Name.Name); name == "writejsonfile" ||
			calls(fn.Body, "json", "Marshal", "MarshalIndent") && (calls(fn.Body, "os", "Rename") || atomicWrite(fn.Body)) {
			out = append(out, fset.Position(fn.Pos()).String()+": func "+fn.Name.Name)
		}
	}
	return out
}

// notThisWrite are the functions that write JSON aside on purpose some other way.
var notThisWrite = map[string]string{
	"internal/clilog/scrub.go":  "JSON lines clilogs, one record a line, rewritten after a scrub",
	"internal/memory/embed.go":  "a vector cache: compact, one small file per text, rebuilt when lost",
	"internal/memory/events.go": "a JSON lines log, one record a line",
	"internal/trace/trace.go":   "a JSON lines trace, one span a line, rebuilt whole",
}

// TestOneJSONWrite: a JSON file rewritten in place goes through Write.
func TestOneJSONWrite(t *testing.T) {
	root := filepath.Join("..", "..")
	var scanned int
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				if err == nil && d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return err
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || filepath.Base(filepath.Dir(path)) == "jsonfile" {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			scanned++
			if _, ok := notThisWrite[filepath.ToSlash(rel)]; ok {
				return nil
			}
			for _, bad := range writeFindings(t, path, nil) {
				t.Errorf("%s: use jsonfile.Write", bad)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if scanned < 100 {
		t.Fatalf("scanned only %d files: the walk is broken", scanned)
	}
}

func TestOneJSONWriteGuardCatches(t *testing.T) {
	src := `package x
import ("encoding/json"; "os")
func save(path string, v any) error {
	data, _ := json.MarshalIndent(v, "", " ")
	os.WriteFile(path+".tmp", data, 0o644)
	return os.Rename(path+".tmp", path)
}
func writeJSONFile(path string, v any) error { return nil }
func read(path string) { os.Rename(path, path) }
`
	if got := writeFindings(t, "x.go", src); len(got) != 2 {
		t.Errorf("findings = %q, want save and writeJSONFile", got)
	}
}

// TestOneJSONWriteCatchesByteWriterCopies: marshalling and handing the bytes
// to an atomic byte writer is a copy of Write too, whatever writer it is.
func TestOneJSONWriteCatchesByteWriterCopies(t *testing.T) {
	src := `package x
import ("encoding/json"; "os"; "github.com/kacperwerema/dark-factory/internal/gitfile")
func saveStats(path string, v any) error {
	encoded, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomically(path, append(encoded, '\n'), 0644)
}
func saveJob(path string, v any) error {
	raw, _ := json.MarshalIndent(v, "", "  ")
	return writeAtomic(path, raw)
}
func saveViaGitfile(path string, v any) error {
	raw, _ := json.Marshal(v)
	return gitfile.WriteAtomic(path, raw, 0o644)
}
func saveComment(path, text string) error { return writeFileAtomically(path, []byte(text), 0o644) }
func encode(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }
func writeFileAtomically(path string, data []byte, perm os.FileMode) error { return nil }
`
	got := writeFindings(t, "x.go", src)
	want := []string{"x.go:3:1: func saveStats", "x.go:10:1: func saveJob", "x.go:14:1: func saveViaGitfile"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("findings = %q, want %q", got, want)
	}
}
