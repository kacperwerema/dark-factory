package workflowupgrade

// ExemptSources are paths catalog-check never counts as a change to a
// catalog entry's sources, even under a `dir/**` glob: tests, their data and
// the playtest runner's scenario fixtures change nothing a project's
// workflow.yaml can use. cmd/playtest/scenario.go reads only
// playtest/scenarios/*/scenario.yaml (this repository's ux-test board); the
// session keys a project writes are in session.go and vocab.go, still
// watched. The list narrows catalog-check's file match only: the guard tests
// that every workflow.yaml key has an entry don't read it.
var ExemptSources = []string{
	"**/*_test.go",
	"**/testdata/**",
	"playtest/scenarios/**",
	"cmd/playtest/scenario.go",
}

// Exempt reports whether the repo-relative slash path p matches one of
// ExemptSources.
func Exempt(p string) bool {
	for _, g := range ExemptSources {
		if MatchSource(g, p) {
			return true
		}
	}
	return false
}

// TextSources are Go files whose string literals and comments are
// engine-built text no project adopts (the built-in dispatch briefing): a
// change to that text alone is no catalog entry's change. A change to their
// code still counts for every entry that lists them.
var TextSources = []string{
	"internal/tracker/dispatch_prompt.go",
}

// TextSource reports whether the repo-relative slash path p matches one of
// TextSources.
func TextSource(p string) bool {
	for _, g := range TextSources {
		if MatchSource(g, p) {
			return true
		}
	}
	return false
}
