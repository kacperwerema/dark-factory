package health

import (
	"fmt"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// matchGlob reports whether the slash path p matches pattern
// (tracker.MatchGlob: ** any directories, a pattern without a slash matches
// the file name anywhere).
func matchGlob(pattern, p string) bool { return tracker.MatchGlob(pattern, p) }

// matchAny reports whether p matches one of the patterns.
func matchAny(patterns []string, p string) bool {
	for _, pat := range patterns {
		if matchGlob(pat, p) {
			return true
		}
	}
	return false
}

// globNote names a glob setting for a warning: health.<key> with the
// configured patterns, or with "default" and the defaults when unset.
func globNote(key string, configured bool, globs []string) string {
	if configured {
		return fmt.Sprintf("health.%s (%s)", key, strings.Join(globs, ", "))
	}
	return fmt.Sprintf("health.%s (default %s)", key, strings.Join(globs, ", "))
}
