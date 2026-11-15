package lint

import (
	"fmt"
	"github.com/kacperwerema/dark-factory/internal/shell"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/partner"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// licenceRule is the licence rule's AGPL clause, as the warning quotes it
// (docs/departments.md#the-licence-rule).
const licenceRule = "the licence rule allows AGPL only as an exception the owner records per tool"

// licenceFindings flags each of d's connectors and its partner: block that
// the partners catalog knows as AGPL (a connector by its name or its
// per_run: tool: id, since a channel's connector is named by its slot:
// Marketing's `social` runs Postiz as tool: postiz) and whose exception: names no
// decision in force on this board: none at all, a ref that doesn't parse,
// one naming no decision, a replaced or expired one, or one without the
// owner's words (provisional, or no quote). A tool the catalog
// doesn't know, or knows under another licence, raises nothing. The ref
// must resolve on the project's own board: each project's owner records
// their own exception, the catalog's is the engine owner's. Part of the
// departments check.
func licenceFindings(env *Env, d tracker.Department) []Finding {
	rows, err := partner.Catalog()
	if err != nil || len(rows) == 0 {
		return nil
	}
	type use struct{ label, exception string }
	type hit struct {
		use
		row *partner.CatalogRow
	}
	var hits []hit
	for _, c := range d.Connectors {
		tool := ""
		if c.PerRun != nil {
			tool = c.PerRun.Tool
		}
		if r := partner.Match(rows, c.Name, tool); r != nil && r.AGPL() {
			hits = append(hits, hit{use{"connector " + c.Name, c.Exception}, r})
		}
	}
	if pt := d.Partner; pt != nil {
		if r := partner.Match(rows, pt.Adapter, pt.Name); r != nil && r.AGPL() {
			hits = append(hits, hit{use{"partner " + pt.Label(), pt.Exception}, r})
		}
	}
	if len(hits) == 0 {
		return nil
	}
	x := tracker.BuildDecisionIndex(env.Issues, nil)
	var out []Finding
	for _, h := range hits {
		why := exceptionProblem(x, h.exception, env)
		if why == "" {
			continue
		}
		out = append(out, Finding{
			Message: fmt.Sprintf("%s: %s is %s (partners catalog): %s, and %s", h.label, h.row.Name, h.row.Licence, licenceRule, why),
			Fix: fmt.Sprintf("the owner records the exception (issue-cli decide <slug> --text %s --quote \"…\") and the entry names it as exception: <slug>#D-<n>; or pick a permissive tool: issue-cli department partners --slot %s",
				shell.Quote(h.row.Name+" accepted as an AGPL exception …"), h.row.Slot),
		})
	}
	return out
}

// exceptionProblem says what is wrong with an entry's exception: ref, or
// "" when it names a decision in force on the board.
func exceptionProblem(x *tracker.DecisionIndex, ref string, env *Env) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "this entry names none (exception: <slug>#D-<n>)"
	}
	norm := tracker.NormalizeDecisionRef("", ref)
	if norm == "" || strings.HasPrefix(norm, "#") {
		return fmt.Sprintf("exception: %q is not a decision ref (<slug>#D-<n>)", ref)
	}
	e := x.Lookup(norm)
	switch {
	case e == nil:
		return fmt.Sprintf("exception: %s names no decision on this board", ref)
	case e.ActiveOn(env.Now):
		if tracker.NeedsOwnerWords(e.Decision) {
			return fmt.Sprintf("exception: %s has no owner's words (provisional, or no quote): only the owner records an exception", ref)
		}
		return ""
	case e.ReplacedBy != nil:
		return fmt.Sprintf("exception: %s is replaced by %s", ref, e.ReplacedBy.Ref)
	}
	return fmt.Sprintf("exception: %s is out of force (%s)", ref, e.StateOn(env.Now))
}
