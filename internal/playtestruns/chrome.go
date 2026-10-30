package playtestruns

import (
	"path/filepath"
	"strconv"
	"strings"
)

// PlaywrightChrome is the newest Chrome for Testing in Playwright's cache
// under home (~/Library/Caches/ms-playwright/chromium-<n>/…), or "" when
// there is none. A Mac without Chrome in /Applications has only this one.
func PlaywrightChrome(home string) string {
	if home == "" {
		return ""
	}
	pattern := filepath.Join(home, "Library", "Caches", "ms-playwright", "chromium-*", "chrome-mac*",
		"Google Chrome for Testing.app", "Contents", "MacOS", "Google Chrome for Testing")
	found, _ := filepath.Glob(pattern)
	best, bestN := "", -1
	for _, p := range found {
		rel, err := filepath.Rel(filepath.Join(home, "Library", "Caches", "ms-playwright"), p)
		if err != nil {
			continue
		}
		dir := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
		n, err := strconv.Atoi(strings.TrimPrefix(dir, "chromium-"))
		if err != nil {
			continue
		}
		if n > bestN {
			best, bestN = p, n
		}
	}
	return best
}
