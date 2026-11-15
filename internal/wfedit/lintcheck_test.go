package wfedit

import (
	"time"

	"github.com/kacperwerema/dark-factory/internal/lint"
)

// lintCheck is the viewer's check (internal/viewer editCheck), for tests:
// a test may import lint, the package may not.
func lintCheck(path string, raw []byte, now time.Time) ([]Finding, error) {
	fs, err := lint.EditCheck(path, raw, now)
	out := make([]Finding, len(fs))
	for i, f := range fs {
		out[i] = Finding(f)
	}
	return out, err
}
