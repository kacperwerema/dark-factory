package tokens

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestFormat(t *testing.T) {
	for n, want := range map[int64]string{
		0: "0", 950: "950", 1000: "1k", 12300: "12.3k", 850000: "850k",
		4_100_000: "4.1M", 150_000_000: "150M", 14_200_000: "14.2M", 2_000_000_000: "2B",
	} {
		if got := Format(n); got != want {
			t.Errorf("Format(%d) = %q, want %q", n, got, want)
		}
	}
}

// TestOneTokenFormatter: a token count is shortened in one place, so the
// same run doesn't read 12k on one page and 12.3k on the next. The chart
// axis (internal/chart) rounds any number, not tokens, so it keeps its own.
func TestOneTokenFormatter(t *testing.T) {
	copyOf := regexp.MustCompile(`"%\.\df[kKM]"`)
	root := filepath.Join("..", "..")
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "chart" || d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if m := copyOf.Find(raw); m != nil {
				t.Errorf("%s shortens a count with %s: use tokens.Format", p, m)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
