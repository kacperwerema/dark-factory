package gitfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestTempPatternFitsNameMax: a temp name stays within 255 bytes and on a
// rune boundary whatever the target's name; a short name is kept whole.
func TestTempPatternFitsNameMax(t *testing.T) {
	if got := TempPattern(".", "state.json"); got != ".state.json.tmp-*" {
		t.Errorf("short name: %q", got)
	}
	for _, base := range []string{strings.Repeat("a", 240), strings.Repeat("é", 125)} {
		for _, prefix := range []string{"", "."} {
			p := TempPattern(prefix, base)
			name := strings.Replace(p, "*", "4294967295", 1)
			if len(name) > 255 || !utf8.ValidString(name) {
				t.Errorf("TempPattern(%q, %d bytes): %d bytes, valid %v", prefix, len(base), len(name), utf8.ValidString(name))
			}
		}
	}
}

// TestWriteAtomicLongName: a 250-byte file name is written; its temp name
// used to pass NAME_MAX.
func TestWriteAtomicLongName(t *testing.T) {
	path := filepath.Join(t.TempDir(), strings.Repeat("b", 250))
	if err := WriteAtomic(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "x" {
		t.Errorf("file = %q", got)
	}
}
