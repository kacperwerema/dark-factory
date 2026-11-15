// Package guidesync copies the markdown guides of one folder into another, so
// a guide has one source: docs/patterns/playtest is written by hand and
// cmd/issue-cli/guides/playtest (embedded in issue-cli, go:embed cannot reach
// ..) is generated from it by `go generate ./cmd/issue-cli/...`.
package guidesync

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Command is what a person runs to bring the embedded playtest guides in line
// with docs/patterns/playtest; TestPlaytestGuidesInSync names it.
const Command = "go generate ./cmd/issue-cli/..."

// Sync makes dst's *.md files equal to src's: each one copied, and a *.md in
// dst with no source removed. Other files in dst are left alone. It returns
// the names it wrote or removed, sorted.
func Sync(src, dst string) ([]string, error) {
	want, err := mdFiles(src)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", src, err)
	}
	if len(want) == 0 {
		return nil, fmt.Errorf("%s has no .md guides", src)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return nil, err
	}
	var changed []string
	for _, name := range want {
		data, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			return changed, err
		}
		out := filepath.Join(dst, name)
		if old, err := os.ReadFile(out); err == nil && string(old) == string(data) {
			continue
		}
		if err := os.WriteFile(out, data, 0o644); err != nil {
			return changed, err
		}
		changed = append(changed, name)
	}
	have, err := mdFiles(dst)
	if err != nil {
		return changed, err
	}
	keep := map[string]bool{}
	for _, n := range want {
		keep[n] = true
	}
	for _, name := range have {
		if keep[name] {
			continue
		}
		if err := os.Remove(filepath.Join(dst, name)); err != nil {
			return changed, err
		}
		changed = append(changed, name)
	}
	sort.Strings(changed)
	return changed, nil
}

func mdFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}
