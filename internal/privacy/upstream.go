package privacy

import "strings"

// The upstream-name guard: the upstream author's name appears in LICENSE.md
// only (the Required Notice lines), never anywhere else in the tracked tree,
// in any case. Decision factory/public-repo-cleanup-v0-60-0-module-path-layout-leftovers-naming-licence-and-attribution#D-4.
// TestTrackedFilesNameUpstreamOnlyInLicense runs it in make validate.

// upstreamName is the word the guard looks for, compared without case. It is
// split so this file does not carry it.
var upstreamName = "mich" + "al"

// UpstreamExempt is the one tracked file that may carry the upstream name.
const UpstreamExempt = "LICENSE.md"

// ScanUpstreamName lists each line of a tracked file other than LICENSE.md
// that holds the upstream name (any case): the working tree's tracked files
// when rev is "", else the tree at rev. Binary files are skipped.
func ScanUpstreamName(dir, rev string) ([]Leak, error) {
	lines, err := grepLines(dir, rev, []string{"-i", "-F", "-e", upstreamName}, ":(exclude,top)"+UpstreamExempt)
	if err != nil {
		return nil, err
	}
	var hits []Leak
	for _, l := range lines {
		if strings.Contains(strings.ToLower(l.text), upstreamName) {
			hits = append(hits, Leak{Path: l.path, Line: l.line, What: "the upstream author's name (allowed only in " + UpstreamExempt + ")"})
		}
	}
	return hits, nil
}
