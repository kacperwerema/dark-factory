package experiment

import (
	"sort"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/retro"
)

// Finding is one Workflow-bin finding of a retro (a Base workflow or
// Subsystem workflow item, the findings the sorting room proposes updates
// for).
type Finding struct {
	Retro *retro.Entry
	Text  string
}

// WorkflowFindings are the Workflow-bin findings of entries.
func WorkflowFindings(entries []*retro.Entry) []Finding {
	var out []Finding
	for _, e := range entries {
		for _, sec := range retro.Sections(e.Body) {
			if !sec.Workflow() || retro.None(sec.Text) {
				continue
			}
			for _, item := range retro.Items(sec.Text) {
				out = append(out, Finding{Retro: e, Text: strings.Join(strings.Fields(item), " ")})
			}
		}
	}
	return out
}

// Candidate is a proposal for an experiment: a term that comes back in the
// Workflow findings of several issues, or a single recent finding.
type Candidate struct {
	// Term is the pattern term; "" for a single finding.
	Term   string   `json:"term,omitempty"`
	Issues []string `json:"issues"`
	Retros []string `json:"retros"`
	Sample string   `json:"sample"`
	// Experiment is the open experiment issue whose source names one of
	// Retros, if any.
	Experiment string `json:"experiment,omitempty"`
}

// Open is an open experiment issue and the retros its block names.
type Open struct {
	Slug   string
	Source []string
}

// Candidates proposes experiments from entries' Workflow findings: patterns
// (a term found in the findings of min or more issues, most issues first),
// then the single findings of the last 30 days before now that no pattern
// covers. stop lists terms that never make a pattern (issue-cli, project
// and system names).
func Candidates(entries []*retro.Entry, open []Open, stop map[string]bool, min int, now time.Time) (patterns, singles []Candidate) {
	findings := WorkflowFindings(entries)
	issueOf := func(f Finding) string {
		if f.Retro.IssueSlug != "" {
			return f.Retro.IssueSlug
		}
		return f.Retro.FileName
	}
	type group struct {
		term     string
		issues   map[string]bool
		findings []Finding
	}
	groups := map[string]*group{}
	var order []string
	for _, f := range findings {
		for _, term := range retro.Terms(f.Text, stop) {
			lt := strings.ToLower(term)
			g := groups[lt]
			if g == nil {
				g = &group{term: term, issues: map[string]bool{}}
				groups[lt] = g
				order = append(order, lt)
			}
			g.issues[issueOf(f)] = true
			g.findings = append(g.findings, f)
		}
	}
	covered := map[*retro.Entry]map[string]bool{}
	for _, lt := range order {
		g := groups[lt]
		if len(g.issues) < min {
			continue
		}
		c := Candidate{Term: g.term, Sample: g.findings[0].Text}
		retros := map[string]bool{}
		for _, f := range g.findings {
			retros["retros/"+f.Retro.FileName] = true
			if covered[f.Retro] == nil {
				covered[f.Retro] = map[string]bool{}
			}
			covered[f.Retro][f.Text] = true
		}
		c.Issues, c.Retros = keys(g.issues), keys(retros)
		c.Experiment = openNaming(open, c.Retros)
		patterns = append(patterns, c)
	}
	sort.SliceStable(patterns, func(i, j int) bool {
		if len(patterns[i].Issues) != len(patterns[j].Issues) {
			return len(patterns[i].Issues) > len(patterns[j].Issues)
		}
		return strings.ToLower(patterns[i].Term) < strings.ToLower(patterns[j].Term)
	})
	since := now.AddDate(0, 0, -30)
	for _, f := range findings {
		if covered[f.Retro][f.Text] || f.Retro.Time().Before(since) {
			continue
		}
		c := Candidate{Issues: []string{issueOf(f)}, Retros: []string{"retros/" + f.Retro.FileName}, Sample: f.Text}
		c.Experiment = openNaming(open, c.Retros)
		singles = append(singles, c)
	}
	return patterns, singles
}

func openNaming(open []Open, retros []string) string {
	for _, o := range open {
		for _, s := range o.Source {
			for _, r := range retros {
				if strings.TrimPrefix(s, "retros/") == strings.TrimPrefix(r, "retros/") {
					return o.Slug
				}
			}
		}
	}
	return ""
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
