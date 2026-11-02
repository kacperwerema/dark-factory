package proof

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/timeline"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Outcomes of one named test in a run.
const (
	RunPass     = "pass"
	RunFail     = "fail"
	RunSkip     = "skip"
	RunNotRun   = "not run"
	RunNotFound = "not found"
	// RunSuperseded: not found, and only older trailers name it; a newer
	// link on each of its criteria names a test that is there. Skipped.
	RunSuperseded = "superseded"
)

// RunResult is one test name a test link gives, and what go test said.
type RunResult struct {
	Name    string `json:"name"`
	Package string `json:"package,omitempty"`
	Outcome string `json:"outcome"`
	// SupersededBy describes the newer link that superseded a
	// RunSuperseded name ("TestB (trailer on abc1234)").
	SupersededBy string `json:"superseded_by,omitempty"`
	// Output is the tail of the test's output (its package's when it did
	// not run), kept for a test that didn't pass.
	Output []string `json:"output,omitempty"`
}

// TestRun is one go test run of the tests an issue's test links name.
type TestRun struct {
	Command  string      `json:"command"`
	Head     string      `json:"head,omitempty"`
	Packages []string    `json:"packages"`
	Results  []RunResult `json:"results"`
	Posted   bool        `json:"posted"`
}

// Passed reports whether every named test ran and passed, superseded ones
// aside, and at least one did.
func (r TestRun) Passed() bool {
	passed := false
	for _, res := range r.Results {
		switch res.Outcome {
		case RunPass:
			passed = true
		case RunSuperseded:
		default:
			return false
		}
	}
	return passed
}

// Names returns the run's names with the given outcome.
func (r TestRun) Names(outcome string) []string {
	var out []string
	for _, res := range r.Results {
		if res.Outcome == outcome {
			out = append(out, res.Name)
		}
	}
	return out
}

// Comment is the tests: comment a passing run posts.
func (r TestRun) Comment() string {
	names := r.Names(RunPass)
	at := ""
	if r.Head != "" {
		at = " at " + r.Head
	}
	c := fmt.Sprintf("tests: prove --run%s: %s — %d passed: %s", at, r.Command, len(names), strings.Join(names, ", "))
	if old := r.Names(RunSuperseded); len(old) > 0 {
		c += fmt.Sprintf("; %d superseded: %s", len(old), strings.Join(old, ", "))
	}
	return c
}

// PlanReportRun is PlanGoRun over the test names r's links give, with each
// name not found that a newer link supersedes (Superseded) marked
// RunSuperseded instead: a Proves: trailer pins its commit, and a test
// renamed or removed later can't be taken out of it.
func PlanReportRun(dir string, r Report) TestRun {
	run := PlanGoRun(dir, TestNames(r))
	found := func(n string) bool {
		for _, res := range run.Results {
			if res.Name == n {
				return res.Outcome != RunNotFound
			}
		}
		return false
	}
	for i, res := range run.Results {
		if res.Outcome != RunNotFound {
			continue
		}
		if by, ok := Superseded(r, res.Name, found); ok {
			run.Results[i].Outcome, run.Results[i].SupersededBy = RunSuperseded, by
		}
	}
	return run
}

// Superseded reports whether the test name is superseded on every
// criterion it is linked to, and by what (the first one's newer link). On
// a criterion, a trailer link to name is superseded by a test link whose
// test is found: from a newer commit's trailer (trailer links come newest
// commit first) or from issue-cli prove. A link from issue-cli prove to
// name is never superseded: prove --remove takes it out.
func Superseded(r Report, name string, found func(string) bool) (string, bool) {
	by := ""
	for _, c := range r.Criteria {
		for j, l := range c.Links {
			if l.Kind != tracker.ProofTest || l.Ref != name {
				continue
			}
			newer, ok := supersededIn(c.Links, j, found)
			if !ok {
				return "", false
			}
			if by == "" {
				by = newer.Ref + " (" + linkFrom(newer.Link) + ")"
			}
		}
	}
	return by, by != ""
}

// supersededIn finds the link that supersedes links[j] (see Superseded).
func supersededIn(links []CheckedLink, j int, found func(string) bool) (CheckedLink, bool) {
	old := links[j]
	if old.Source != SourceTrailer {
		return CheckedLink{}, false
	}
	for k, l := range links {
		if l.Kind != tracker.ProofTest || l.Ref == old.Ref || !found(l.Ref) {
			continue
		}
		if l.Source == SourceProve || (l.Source == SourceTrailer && k < j && l.From != old.From) {
			return l, true
		}
	}
	return CheckedLink{}, false
}

// linkFrom says where a link was declared: "trailer on abc1234", "prove".
func linkFrom(l Link) string {
	if l.Source == SourceTrailer && l.From != "" {
		return "trailer on " + l.From
	}
	return l.Source
}

// TestNames are the test names r's test links give, in criteria order,
// each once.
func TestNames(r Report) []string {
	var out []string
	for _, c := range r.Criteria {
		for _, l := range c.Links {
			if l.Kind == tracker.ProofTest && !containsString(out, l.Ref) {
				out = append(out, l.Ref)
			}
		}
	}
	return out
}

// goTestNameRe is a top-level test go test -run can run.
var goTestNameRe = regexp.MustCompile(`^(Test|Example|Fuzz)[A-Za-z0-9_]*$`)

// topLevel is a test name's top-level test: TestA for TestA/case.
func topLevel(name string) string {
	top, _, _ := strings.Cut(name, "/")
	return top
}

// PlanGoRun finds the package of each name in the Go module at dir: the
// folder of the tracked _test.go file that declares its top-level test.
// Names with no such declaration come back as not found.
func PlanGoRun(dir string, names []string) TestRun {
	var tops []string
	for _, n := range names {
		if t := topLevel(n); goTestNameRe.MatchString(t) && !containsString(tops, t) {
			tops = append(tops, t)
		}
	}
	pkgOf := map[string]string{}
	if len(tops) > 0 {
		out, _ := git(dir, "grep", "-n", "-E", `^func (`+strings.Join(tops, "|")+`)\(`, "--", "*_test.go")
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			file, rest, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			_, decl, _ := strings.Cut(rest, ":")
			name, _, _ := strings.Cut(strings.TrimPrefix(decl, "func "), "(")
			if _, seen := pkgOf[name]; !seen {
				pkgOf[name] = pkgPath(file)
			}
		}
	}
	run := TestRun{Packages: []string{}}
	for _, n := range names {
		res := RunResult{Name: n, Outcome: RunNotFound}
		if pkg, ok := pkgOf[topLevel(n)]; ok {
			res.Package, res.Outcome = pkg, RunNotRun
			if !containsString(run.Packages, pkg) {
				run.Packages = append(run.Packages, pkg)
			}
		}
		run.Results = append(run.Results, res)
	}
	sort.Strings(run.Packages)
	var found []string
	for _, t := range tops {
		if _, ok := pkgOf[t]; ok {
			found = append(found, t)
		}
	}
	run.Command = "go " + strings.Join(GoTestArgs(found, run.Packages), " ")
	return run
}

// GoTestArgs are the go test arguments that run the top-level tests in pkgs.
func GoTestArgs(tops, pkgs []string) []string {
	return append([]string{"test", "-json", "-run", "^(" + strings.Join(tops, "|") + ")$"}, pkgs...)
}

func pkgPath(file string) string {
	if d := path.Dir(file); d != "." {
		return "./" + d
	}
	return "."
}

type goTestEvent struct {
	Action     string
	Package    string
	ImportPath string // build-output: "x/pkg [x/pkg.test]"
	Test       string
	Output     string
}

// tailLines is how much of a failing test's output a run keeps.
const tailLines = 20

// ReadGoTestJSON fills run's results from go test -json output: each name's
// own outcome, else its top-level test's. A name that has none did not run
// (its package failed to build, say) and keeps its package's output.
func ReadGoTestJSON(run *TestRun, data []byte) {
	outcome := map[string]string{}
	output := map[string][]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		var ev goTestEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		key := ev.Test
		if key == "" {
			key = "pkg " + ev.Package
		}
		if ev.Action == "build-output" {
			ev.Action = "output"
			pkg, _, _ := strings.Cut(ev.ImportPath, " ")
			key = "pkg " + pkg
		}
		switch ev.Action {
		case "pass", "fail", "skip":
			if ev.Test != "" {
				outcome[ev.Test] = ev.Action
			}
		case "output":
			output[key] = append(output[key], strings.TrimRight(ev.Output, "\n"))
		}
	}
	// go test -json names a package by its import path; match a result's
	// ./dir to the path ending in it, "." to the shortest (the module's).
	pkgOutput := func(dir string) []string {
		best := ""
		for k := range output {
			p, ok := strings.CutPrefix(k, "pkg ")
			if !ok {
				continue
			}
			if dir == "." && (best == "" || len(k) < len(best)) || dir != "." && strings.HasSuffix(p, strings.TrimPrefix(dir, ".")) {
				best = k
			}
		}
		return output[best]
	}
	for i, res := range run.Results {
		if res.Outcome == RunNotFound || res.Outcome == RunSuperseded {
			continue
		}
		name := strings.ReplaceAll(res.Name, " ", "_")
		got, ok := outcome[name]
		if !ok {
			got, ok = outcome[topLevel(name)]
			name = topLevel(name)
		}
		switch {
		case !ok:
			run.Results[i].Outcome = RunNotRun
			run.Results[i].Output = tail(pkgOutput(res.Package))
		case got == "pass":
			run.Results[i].Outcome = RunPass
		default:
			run.Results[i].Outcome = got
			run.Results[i].Output = tail(output[name])
		}
	}
}

func tail(lines []string) []string {
	if len(lines) > tailLines {
		return lines[len(lines)-tailLines:]
	}
	return lines
}

// ShortHead is the short sha of HEAD in dir, "" when unreadable.
func ShortHead(dir string) string {
	out, err := git(dir, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return timeline.ShortSHA(strings.TrimSpace(out))
}

// DirtyCode lists the changed or untracked files in the checkout at dir that
// are code (NonCode false): running tests on them reports on something no
// commit holds.
func DirtyCode(dir string) ([]string, error) {
	out, err := git(dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	var dirty []string
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		f := line[3:]
		if _, to, ok := strings.Cut(f, " -> "); ok {
			f = to
		}
		if !NonCode(strings.Trim(f, `"`)) {
			dirty = append(dirty, f)
		}
	}
	return dirty, nil
}

// IsGoModule reports whether dir holds a go.mod.
func IsGoModule(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "go.mod"))
	return err == nil
}
