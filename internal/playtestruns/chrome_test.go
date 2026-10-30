package playtestruns

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlaywrightChromePicksTheNewest(t *testing.T) {
	home := t.TempDir()
	if got := PlaywrightChrome(home); got != "" {
		t.Fatalf("empty cache: %q", got)
	}
	bin := func(n string) string {
		return filepath.Join(home, "Library", "Caches", "ms-playwright", "chromium-"+n, "chrome-mac-arm64",
			"Google Chrome for Testing.app", "Contents", "MacOS", "Google Chrome for Testing")
	}
	for _, n := range []string{"999", "1248", "1100"} {
		os.MkdirAll(filepath.Dir(bin(n)), 0o755)
		os.WriteFile(bin(n), nil, 0o755)
	}
	if got := PlaywrightChrome(home); got != bin("1248") {
		t.Fatalf("got %q, want chromium-1248", got)
	}
}
