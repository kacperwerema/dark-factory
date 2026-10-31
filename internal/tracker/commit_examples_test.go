package tracker

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// conventionalRe is the commit hook's subject check (Dex post-commit-guard).
var conventionalRe = regexp.MustCompile(`^(feat|fix|refactor|perf|docs|test|chore|build|ci|style|revert)(\([^)]+\))?!?: .+`)

var (
	// -m "<subject>" on a git command.
	dashMRe = regexp.MustCompile(`-m "([^"]+)"`)
	// A backticked subject with a scope, `chore(merge): …`; trailers and
	// comment prefixes (Proves:, tests:) have none.
	scopedSubjectRe = regexp.MustCompile("`([a-z]+\\([^)`]+\\)!?: [^`]+)`")
	// A git merge that isn't --ff-only takes git's default message.
	bareMergeRe = regexp.MustCompile("git merge (?:--no-ff )?[a-z<][^`\"]*`")
)

// Every commit message the project's workflow tells agents to write passes
// the commit hook: a worker that copies an example never has it rejected.
func TestWorkflowCommitExamplesAreConventional(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	var examples []string
	for _, m := range dashMRe.FindAllStringSubmatch(text, -1) {
		examples = append(examples, m[1])
	}
	for _, m := range scopedSubjectRe.FindAllStringSubmatch(text, -1) {
		examples = append(examples, m[1])
	}
	if len(examples) < 3 {
		t.Fatalf("found %d commit examples (%q), want the shipping step's", len(examples), examples)
	}
	for _, e := range examples {
		if !conventionalRe.MatchString(e) {
			t.Errorf("commit example %q fails the conventional-commit hook", e)
		}
	}
	for _, m := range bareMergeRe.FindAllString(text, -1) {
		if !regexp.MustCompile(`--ff-only`).MatchString(m) {
			t.Errorf("%s takes git's default merge message, which the hook rejects: give it -m \"chore(merge): …\"", m)
		}
	}
}
