package tracker

import (
	"slices"
	"sync"
)

// issueMemo keeps what is derived from one parse of an issue file: the
// cached Issue and every copy LoadIssues hands out share it, so a board
// built many times a request (BuildDecisionIndex from Needs you, the
// challenges, connections, lint) parses each file's decisions once per file
// version. Each value is kept with the slug and body it was made from: a
// caller that changes either on its copy gets a fresh parse.
type issueMemo struct {
	mu sync.Mutex

	decSlug, decBody string
	decisions        []Decision
	decOK            bool

	refBody string
	refs    []refCandidate
	refOK   bool
}

// refCandidate is one `<slug>#D-n` match in a body before its slug is
// resolved against the board's known slugs.
type refCandidate struct {
	slug, num string
}

// Decisions are the issue's D-n lines (ParseDecisions on its slug and body),
// parsed once per file version for an issue from LoadIssues. The slice is
// the caller's own; the decisions' own slices are shared and must not be
// changed.
func (i *Issue) Decisions() []Decision {
	m := i.memo
	if m == nil {
		return ParseDecisions(i.Slug, i.BodyRaw)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.decOK || m.decSlug != i.Slug || m.decBody != i.BodyRaw {
		m.decSlug, m.decBody = i.Slug, i.BodyRaw
		m.decisions, m.decOK = ParseDecisions(i.Slug, i.BodyRaw), true
	}
	return slices.Clone(m.decisions)
}

// refCandidates are the body's `<slug>#D-n` matches outside fenced code and
// `(replaces …)` parentheticals, kept once per file version like Decisions.
func (i *Issue) refCandidates() []refCandidate {
	m := i.memo
	if m == nil {
		return scanRefCandidates(i.BodyRaw)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.refOK || m.refBody != i.BodyRaw {
		m.refBody = i.BodyRaw
		m.refs, m.refOK = scanRefCandidates(i.BodyRaw), true
	}
	return m.refs
}

func scanRefCandidates(body string) []refCandidate {
	var out []refCandidate
	for _, line := range refLines(body) {
		for _, m := range decisionRefRe.FindAllStringSubmatch(line, -1) {
			out = append(out, refCandidate{slug: m[1], num: m[2]})
		}
	}
	return out
}
