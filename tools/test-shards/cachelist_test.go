package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/testshard"
)

// moduleRoot is this repo's root, two folders up.
func moduleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("no go.mod at %s: %v", root, err)
	}
	return root
}

// testDirs is every folder of the module holding a _test.go file, as the
// cache list spells it. It walks the tree itself (no go list), so go test's
// cache sees the folders it read and reruns this test when one is added.
func testDirs(t *testing.T, root string) map[string][]string {
	t.Helper()
	dirs := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || name == "node_modules") {
				return filepath.SkipDir
			}
			if path != root {
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir // another module
				}
			}
			return nil
		}
		if strings.HasSuffix(name, "_test.go") {
			rel, _ := filepath.Rel(root, filepath.Dir(path))
			dir := "."
			if rel != "." {
				dir = "./" + filepath.ToSlash(rel)
			}
			dirs[dir] = append(dirs[dir], path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return dirs
}

// topLevelTests is every Test* function the files declare.
func topLevelTests(t *testing.T, files []string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	fset := token.NewFileSet()
	for _, f := range files {
		file, err := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && strings.HasPrefix(fd.Name.Name, "Test") {
				out[fd.Name.Name] = true
			}
		}
	}
	return out
}

// Every test package is on scripts/test-cache.txt once, cached or fresh, the
// list names no folder that has no tests, and each fresh-test line names a
// test its package declares. A new package can't slip into the cache (or out
// of it) without someone deciding which.
func TestEveryTestPackageIsInTheCacheList(t *testing.T) {
	root := moduleRoot(t)
	listPath := filepath.Join(root, "scripts", "test-cache.txt")
	data, err := os.ReadFile(listPath)
	if err != nil {
		t.Fatal(err)
	}
	list, err := testshard.ParseCacheList(data)
	if err != nil {
		t.Fatalf("scripts/test-cache.txt: %v", err)
	}
	dirs := testDirs(t, root)
	listed := map[string]bool{}
	for _, d := range list.Dirs() {
		listed[d] = true
		if _, ok := dirs[d]; !ok {
			t.Errorf("scripts/test-cache.txt lists %s, which has no _test.go: drop its line", d)
		}
	}
	var missing []string
	for d := range dirs {
		if !listed[d] {
			missing = append(missing, d)
		}
	}
	sort.Strings(missing)
	for _, d := range missing {
		t.Errorf("%s has tests but no line in scripts/test-cache.txt: add `cached %s`, or `fresh %s <why>` when a test reads what go test's cache can't see (git about this checkout, this machine, the live board)", d, d, d)
	}
	for d, tests := range list.FreshTests {
		declared := topLevelTests(t, dirs[d])
		for name := range tests {
			if !declared[name] {
				t.Errorf("scripts/test-cache.txt: fresh-test %s %s: no such test in %s", d, name, d)
			}
		}
	}
}

func TestParseCacheListRefusesWhatItCantPlace(t *testing.T) {
	for _, c := range []struct{ list, want string }{
		{"cached ./a\nfresh ./a reads git\n", "already listed on line 1"},
		{"cached ./a\ncached ./a\n", "already listed"},
		{"fresh ./a\n", "say why"},
		{"cached ./a extra\n", "takes no reason"},
		{"fresh-test ./a TestX reads the board\n", "isn't on a cached line"},
		{"fresh ./a reads git\nfresh-test ./a TestX reads the board\n", "isn't on a cached line"},
		{"cached ./a\nfresh-test ./a TestX\n", "want `fresh-test"},
		{"cached ./a\nfresh-test ./a TestX why\nfresh-test ./a TestX why\n", "listed twice"},
		{"stale ./a\n", "unknown kind"},
		{"cached internal/a\n", "want `cached|fresh|fresh-test"},
	} {
		if _, err := testshard.ParseCacheList([]byte(c.list)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: err = %v, want %q", c.list, err, c.want)
		}
	}
	l, err := testshard.ParseCacheList([]byte("# note\ncached ./a/\nfresh ./b reads this machine\ncached .\nfresh-test ./a TestX reads the live board\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintln(l.Dirs(), l.Fresh["./b"], l.FreshTests["./a"]["TestX"]); got != "[. ./a ./b] reads this machine reads the live board\n" {
		t.Fatalf("parsed = %s", got)
	}
}
