package proof

import "strings"

// rustTestAttr matches a Rust test attribute: #[test], #[cfg(test)] (an
// inline mod tests) and a crate's own, such as #[tokio::test] or
// #[tokio::test(flavor = "multi_thread")]. git grep -E reads it (POSIX ERE).
const rustTestAttr = `#\[(cfg\(test\)|([A-Za-z_][A-Za-z0-9_]*::)*test)[](]`

// testFiles reports which files hold tests: a path in the usual test layouts
// (IsTestPath), or a Rust source file with a test attribute in it, since
// Rust's unit tests live inline in src/*.rs. grep runs git in the checkout
// with the given arguments; ref is the tree the paths come from ("" for the
// worktree). The Rust files are read once, and only when a .rs path the
// layouts miss is asked about.
type testFiles struct {
	grep func(args ...string) (string, error)
	ref  string
	rust map[string]bool
}

func (f *testFiles) is(path string) bool {
	if IsTestPath(path) {
		return true
	}
	if !strings.HasSuffix(path, ".rs") {
		return false
	}
	if f.rust == nil {
		f.rust = map[string]bool{}
		args := []string{"grep", "-l", "-I", "-E", "-e", rustTestAttr}
		if f.ref != "" {
			args = append(args, f.ref)
		}
		out, _ := f.grep(append(args, "--", "*.rs")...)
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if f.ref != "" {
				line = strings.TrimPrefix(line, f.ref+":")
			}
			if line != "" {
				f.rust[line] = true
			}
		}
	}
	return f.rust[path]
}
