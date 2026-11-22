package tracker

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// pathToken is a repository path as an issue body writes one: two or more
// slash-separated parts, the last with an extension, optionally followed
// by #Symbol or :line, which are dropped.
var pathToken = regexp.MustCompile(`(?:^|[\s` + "`" + `("'\[])((?:[A-Za-z0-9_.-]+/)+[A-Za-z0-9_-]+\.[A-Za-z0-9]+)(?:[#:][A-Za-z0-9_.]*)?`)

// PlannedFiles are the repository paths an issue body names, in order of
// first mention: the files a design plans to change, before the branch has
// them. A path counts when it is a file at root or would be a new one in a
// folder that exists there; URLs and paths outside the checkout don't.
func PlannedFiles(root, body string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range pathToken.FindAllStringSubmatchIndex(body, -1) {
		p := strings.TrimPrefix(body[m[2]:m[3]], "./")
		if strings.HasPrefix(p, "../") || seen[p] {
			continue
		}
		full := filepath.Join(root, filepath.FromSlash(p))
		if fi, err := os.Stat(full); err == nil && fi.IsDir() {
			continue
		} else if err != nil {
			if di, derr := os.Stat(filepath.Dir(full)); derr != nil || !di.IsDir() {
				continue
			}
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}
