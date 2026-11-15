package checks

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestFindReadsAScriptAgainOnlyWhenItChanges: Find keeps a script's header
// by its stamp; an edit (new size or mtime) is read again, a new script is
// seen, a removed one is gone.
func TestFindReadsAScriptAgainOnlyWhenItChanges(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, Dir)
	os.MkdirAll(dir, 0o755)
	old := time.Now().Add(-time.Hour)
	write := func(name, slug string, at time.Time) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/usr/bin/env bash\n# issue: "+slug+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(p, at, at)
		return p
	}
	a := write("a.sh", "cli/x", old)
	for range 3 {
		if got := Find([]string{root}, "cli/x"); len(got) != 1 || got[0].Name != "a" {
			t.Fatalf("Find = %+v, want a", got)
		}
	}
	if n := ScriptReads(root)[a]; n != 1 {
		t.Fatalf("a.sh read %d times, want once", n)
	}
	write("a.sh", "cli/yy", old.Add(time.Minute))
	if got := Find([]string{root}, "cli/x"); len(got) != 0 {
		t.Fatalf("Find after the header changed = %+v, want none", got)
	}
	if got := Find([]string{root}, "cli/yy"); len(got) != 1 {
		t.Fatalf("Find for the new header = %+v", got)
	}
	write("b.sh", "cli/yy", old)
	if got := Find([]string{root}, "cli/yy"); len(got) != 2 {
		t.Fatalf("Find with b.sh added = %+v", got)
	}
	os.Remove(a)
	if got := Find([]string{root}, "cli/yy"); len(got) != 1 || got[0].Name != "b" {
		t.Fatalf("Find with a.sh removed = %+v", got)
	}
}
