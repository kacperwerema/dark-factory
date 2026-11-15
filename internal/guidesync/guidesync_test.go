package guidesync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
)

func TestMain(m *testing.M) { tmuxguard.Main(m) }

func TestSyncCopiesAndPrunes(t *testing.T) {
	tmp := t.TempDir()
	src, dst := filepath.Join(tmp, "src"), filepath.Join(tmp, "dst")
	os.MkdirAll(src, 0o755)
	os.MkdirAll(dst, 0o755)
	os.WriteFile(filepath.Join(src, "a.md"), []byte("A2"), 0o644)
	os.WriteFile(filepath.Join(src, "b.md"), []byte("B"), 0o644)
	os.WriteFile(filepath.Join(dst, "a.md"), []byte("A1"), 0o644)
	os.WriteFile(filepath.Join(dst, "old.md"), []byte("X"), 0o644)
	os.WriteFile(filepath.Join(dst, "keep.txt"), []byte("T"), 0o644)

	changed, err := Sync(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(changed); got != 3 {
		t.Errorf("changed = %q, want a.md b.md old.md", changed)
	}
	for name, want := range map[string]string{"a.md": "A2", "b.md": "B", "keep.txt": "T"} {
		if got, _ := os.ReadFile(filepath.Join(dst, name)); string(got) != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "old.md")); !os.IsNotExist(err) {
		t.Error("old.md should be removed")
	}
	if changed, _ := Sync(src, dst); len(changed) != 0 {
		t.Errorf("second sync changed %q", changed)
	}
	if _, err := Sync(filepath.Join(tmp, "none"), dst); err == nil {
		t.Error("an empty source should be refused, not wipe the copies")
	}
}
