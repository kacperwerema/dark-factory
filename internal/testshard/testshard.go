// Package testshard splits one package's tests into groups that run as
// separate go test processes (tools/test-shards). A package whose tests
// can't run in parallel inside one binary (t.Setenv, os.Chdir) still uses
// several cores this way.
//
// Plan spreads the tests by recorded time, Regex turns a group into a -run
// pattern, and Check proves every listed test runs in exactly one group.
package testshard

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ListedTests reads go test -list output: one name per line, then go test's
// own "ok <pkg> <time>" line, which is dropped. Benchmarks are dropped too:
// -run doesn't run them.
func ListedTests(out []byte) []string {
	var names []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.TrimSpace(line)
		if !isTestName(name) || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

var testName = regexp.MustCompile(`^(Test|Example|Fuzz)[\p{L}\p{N}_]*$`)

func isTestName(s string) bool { return testName.MatchString(s) }

// Times reads a go test -json stream and returns each top-level test's
// elapsed seconds in pkg (an import path). A test seen twice keeps the last.
func Times(stream []byte, pkg string) map[string]float64 {
	times := map[string]float64{}
	sc := bufio.NewScanner(bytes.NewReader(stream))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var e struct {
			Action, Package, Test string
			Elapsed               float64
		}
		if json.Unmarshal(line, &e) != nil || e.Package != pkg || e.Test == "" || strings.Contains(e.Test, "/") {
			continue
		}
		switch e.Action {
		case "pass", "fail", "skip":
			times[e.Test] = e.Elapsed
		}
	}
	return times
}

// Plan splits tests into n groups of about equal recorded time: longest
// first, each to the group with the least so far. A test with no recorded
// time counts as the median of the known ones (1 s when none is known), so a
// new test lands somewhere sensible. The result is the same for the same
// input; empty groups are dropped, so it never has more groups than tests.
func Plan(tests []string, times map[string]float64, n int) [][]string {
	if n < 1 {
		n = 1
	}
	guess := median(tests, times)
	type job struct {
		name string
		secs float64
	}
	jobs := make([]job, 0, len(tests))
	for _, t := range tests {
		s, ok := times[t]
		if !ok {
			s = guess
		}
		jobs = append(jobs, job{t, s})
	}
	sort.SliceStable(jobs, func(i, j int) bool {
		if jobs[i].secs != jobs[j].secs {
			return jobs[i].secs > jobs[j].secs
		}
		return jobs[i].name < jobs[j].name
	})
	groups := make([][]string, n)
	load := make([]float64, n)
	for _, j := range jobs {
		k := 0
		for i := 1; i < n; i++ {
			if load[i] < load[k] {
				k = i
			}
		}
		groups[k] = append(groups[k], j.name)
		load[k] += j.secs
	}
	var out [][]string
	for _, g := range groups {
		if len(g) > 0 {
			sort.Strings(g)
			out = append(out, g)
		}
	}
	return out
}

func median(tests []string, times map[string]float64) float64 {
	var known []float64
	for _, t := range tests {
		if s, ok := times[t]; ok {
			known = append(known, s)
		}
	}
	if len(known) == 0 {
		return 1
	}
	sort.Float64s(known)
	return known[len(known)/2]
}

// Regex is the -run pattern for one group: its names and nothing else at
// the top level (subtests all run, as without -run).
func Regex(group []string) string {
	quoted := make([]string, len(group))
	for i, n := range group {
		quoted[i] = regexp.QuoteMeta(n)
	}
	return "^(" + strings.Join(quoted, "|") + ")$"
}

// Check proves the patterns run every listed test exactly once: each name
// matches one pattern, as go test -run matches a top-level name. It returns
// one line per name that matches none or several.
func Check(tests []string, patterns []string) []string {
	res := make([]*regexp.Regexp, len(patterns))
	for i, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return []string{fmt.Sprintf("shard %d: bad pattern: %v", i+1, err)}
		}
		res[i] = re
	}
	var bad []string
	for _, t := range tests {
		var in []string
		for i, re := range res {
			if re.MatchString(t) {
				in = append(in, fmt.Sprint(i+1))
			}
		}
		switch len(in) {
		case 1:
		case 0:
			bad = append(bad, t+": in no shard")
		default:
			bad = append(bad, t+": in shards "+strings.Join(in, ", "))
		}
	}
	return bad
}

// MergeProfiles joins coverage profiles from several go test runs into one:
// the first mode: line, then every block line. go tool cover adds up blocks
// that appear more than once, as it does for one run over several packages.
func MergeProfiles(profiles [][]byte) ([]byte, error) {
	var out bytes.Buffer
	mode := ""
	for i, p := range profiles {
		lines := strings.Split(strings.TrimRight(string(p), "\n"), "\n")
		if len(lines) == 0 || lines[0] == "" {
			continue
		}
		if !strings.HasPrefix(lines[0], "mode: ") {
			return nil, fmt.Errorf("profile %d: first line %q is not mode:", i+1, lines[0])
		}
		if mode == "" {
			mode = lines[0]
			out.WriteString(mode + "\n")
		} else if lines[0] != mode {
			return nil, fmt.Errorf("profile %d: %s, the others %s", i+1, lines[0], mode)
		}
		for _, l := range lines[1:] {
			if l != "" {
				out.WriteString(l + "\n")
			}
		}
	}
	if mode == "" {
		return nil, fmt.Errorf("no coverage profile written")
	}
	return out.Bytes(), nil
}
