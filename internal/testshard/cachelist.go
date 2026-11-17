package testshard

import (
	"bufio"
	"bytes"
	"fmt"
	"sort"
	"strings"
)

// CacheList is scripts/test-cache.txt: which test packages make test may
// take from go test's cache and which always run (-count=1). go test's cache
// reruns a package when its code, the environment variables it reads or the
// files it opens change; what it can't see (a git command's answer, this
// machine's memory, the live board that changes every few seconds) needs a
// fresh run. One line each:
//
//	cached ./internal/x
//	fresh ./internal/y <why it must always run>
//	fresh-test ./internal/z TestName <why that test must always run>
//
// Every test package is listed once, cached or fresh. A fresh-test line names
// one test of a cached package that also runs on its own with -count=1, so
// the rest of that package can come from the cache.
type CacheList struct {
	Cached     map[string]bool              // package dir ("./internal/x")
	Fresh      map[string]string            // package dir → why
	FreshTests map[string]map[string]string // package dir → test → why
}

// Dirs is every package the list names, sorted.
func (c *CacheList) Dirs() []string {
	var out []string
	for d := range c.Cached {
		out = append(out, d)
	}
	for d := range c.Fresh {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// ParseCacheList reads the list. A package listed twice, a fresh line with
// no reason, or a fresh-test line for a package that isn't cached is an
// error naming its line.
func ParseCacheList(data []byte) (*CacheList, error) {
	c := &CacheList{Cached: map[string]bool{}, Fresh: map[string]string{}, FreshTests: map[string]map[string]string{}}
	seen := map[string]int{}
	type ft struct {
		line      int
		dir, test string
	}
	var tests []ft
	sc := bufio.NewScanner(bytes.NewReader(data))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 || !strings.HasPrefix(f[1], ".") {
			return nil, fmt.Errorf("line %d: want `cached|fresh|fresh-test ./<dir> ...`: %q", n, line)
		}
		kind, dir := f[0], strings.TrimSuffix(f[1], "/")
		switch kind {
		case "cached", "fresh":
			if prev, ok := seen[dir]; ok {
				return nil, fmt.Errorf("line %d: %s is already listed on line %d: each package goes in one list", n, dir, prev)
			}
			seen[dir] = n
			if kind == "cached" {
				if len(f) > 2 {
					return nil, fmt.Errorf("line %d: a cached line takes no reason: %q", n, line)
				}
				c.Cached[dir] = true
				continue
			}
			if len(f) < 3 {
				return nil, fmt.Errorf("line %d: fresh %s: say why it must always run", n, dir)
			}
			c.Fresh[dir] = strings.Join(f[2:], " ")
		case "fresh-test":
			if len(f) < 4 || !strings.HasPrefix(f[2], "Test") {
				return nil, fmt.Errorf("line %d: want `fresh-test ./<dir> Test<Name> <why>`: %q", n, line)
			}
			if c.FreshTests[dir] == nil {
				c.FreshTests[dir] = map[string]string{}
			}
			if _, ok := c.FreshTests[dir][f[2]]; ok {
				return nil, fmt.Errorf("line %d: %s %s is listed twice", n, dir, f[2])
			}
			c.FreshTests[dir][f[2]] = strings.Join(f[3:], " ")
			tests = append(tests, ft{n, dir, f[2]})
		default:
			return nil, fmt.Errorf("line %d: unknown kind %q (cached, fresh or fresh-test)", n, kind)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	for _, t := range tests {
		if !c.Cached[t.dir] {
			return nil, fmt.Errorf("line %d: fresh-test %s %s: its package isn't on a cached line (a fresh package runs all its tests anyway)", t.line, t.dir, t.test)
		}
	}
	return c, nil
}

// FreshTestRegex is a go test -run pattern matching exactly the named
// top-level tests.
func FreshTestRegex(tests []string) string {
	sorted := append([]string(nil), tests...)
	sort.Strings(sorted)
	return "^(" + strings.Join(sorted, "|") + ")$"
}
