package main

import (
	"errors"
	"flag"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/yolo"
)

// The decision ledger's part of `decide`: the attribute group's flags
// (--scope, --hard, --soft, --at, --paths, --expires), --amend, and the owner
// act rule for every change to a hard decision (approve's rule: a worker is
// refused, a lead relays the owner's --quote, the owner's own shell may).

// attrFlags are the attribute group's flags and which of them were given.
type attrFlags struct {
	scope, at, paths, expires *string
	hard, soft                *bool
	set                       map[string]bool
}

func addAttrFlags(fs *flag.FlagSet) *attrFlags {
	return &attrFlags{
		scope:   fs.String("scope", "", "whom it reaches: issue (default), project, system:<S>, department:<key>, type:<t>"),
		at:      fs.String("at", "", `statuses where it matters, comma-separated ("" = all)`),
		paths:   fs.String("paths", "", "globs a hard decision guards, comma-separated"),
		expires: fs.String("expires", "", "last day in force, YYYY-MM-DD"),
		hard:    fs.Bool("hard", false, "the owner's hard rule (needs the owner's words; the owner's act)"),
		soft:    fs.Bool("soft", false, "drop hard (an owner act on a hard decision)"),
	}
}

// visit records which flags were given, so --at "" clears at: while an
// absent --at keeps it.
func (f *attrFlags) visit(fs *flag.FlagSet) {
	f.set = map[string]bool{}
	fs.Visit(func(fl *flag.Flag) { f.set[fl.Name] = true })
}

func (f *attrFlags) any() bool {
	for _, k := range []string{"scope", "at", "paths", "expires", "hard", "soft"} {
		if f.set[k] {
			return true
		}
	}
	return false
}

// apply sets the given flags on a.
func (f *attrFlags) apply(a tracker.DecisionAttrs) (tracker.DecisionAttrs, error) {
	if *f.hard && *f.soft {
		return a, fmt.Errorf("--hard and --soft together: pick one")
	}
	if f.set["scope"] {
		s := strings.TrimSpace(*f.scope)
		if s == "" {
			s = tracker.ScopeIssue
		}
		norm, err := tracker.NormalizeScope(s)
		if err != nil {
			return a, fmt.Errorf("--%w", err)
		}
		a.Scope = norm
	}
	if f.set["at"] {
		a.At = tracker.SplitAttrList(*f.at)
	}
	if f.set["paths"] {
		a.Paths = tracker.SplitAttrList(*f.paths)
		for _, g := range a.Paths {
			if err := tracker.ValidateGlob(g); err != nil {
				return a, fmt.Errorf("--%w", err)
			}
		}
	}
	if f.set["expires"] {
		a.Expires = strings.TrimSpace(*f.expires)
	}
	if *f.hard {
		a.Hard = true
	}
	if *f.soft {
		a.Hard = false
	}
	return a, nil
}

// checkAttrs validates a group against the project: a scope's name, at:'s
// statuses, paths only on hard, expires: today or later when it was set.
func checkAttrs(ctx *Context, issues []*tracker.Issue, a tracker.DecisionAttrs, expiresSet bool) error {
	kind, name := a.ScopeKind()
	switch kind {
	case tracker.ScopeSystem:
		known := map[string]bool{}
		for _, i := range issues {
			if i.System != "" {
				if strings.EqualFold(i.System, name) {
					return checkAttrsRest(ctx, issues, a, expiresSet)
				}
				known[i.System] = true
			}
		}
		return fmt.Errorf("--scope %s: no issue has system %q; systems: %s", a.Scope, name, strings.Join(sortedKeys(known), ", "))
	case tracker.ScopeDepartment:
		if tracker.DepartmentKey(name) == "product" || ctx.Project.DepartmentByKey(name) != nil {
			break
		}
		var keys []string
		for _, d := range ctx.Project.Departments {
			keys = append(keys, tracker.DepartmentKey(d.Name))
		}
		return fmt.Errorf("--scope %s: no department %q in projects.yaml; keys: product%s", a.Scope, name, prefixJoin(", ", keys))
	case tracker.ScopeType:
		types := map[string]bool{}
		for _, wf := range projectWorkflows(ctx, issues) {
			for t := range wf.Types {
				types[t] = true
			}
		}
		for _, i := range issues {
			if i.Type != "" {
				types[i.Type] = true
			}
		}
		if !types[name] {
			return fmt.Errorf("--scope %s: no type %q; types: %s", a.Scope, name, strings.Join(sortedKeys(types), ", "))
		}
	}
	return checkAttrsRest(ctx, issues, a, expiresSet)
}

func checkAttrsRest(ctx *Context, issues []*tracker.Issue, a tracker.DecisionAttrs, expiresSet bool) error {
	if len(a.At) > 0 {
		statuses := map[string]bool{}
		var order []string
		for _, wf := range projectWorkflows(ctx, issues) {
			for _, s := range wf.GetStatusOrder() {
				if !statuses[strings.ToLower(s)] {
					statuses[strings.ToLower(s)] = true
					order = append(order, s)
				}
			}
		}
		for _, s := range a.At {
			if len(statuses) > 0 && !statuses[strings.ToLower(s)] {
				return fmt.Errorf("--at %q is not a status of this project; statuses: %s", s, strings.Join(order, ", "))
			}
		}
	}
	if len(a.Paths) > 0 && !a.Hard {
		return fmt.Errorf("paths: guard only hard decisions; drop them with --paths \"\" (or make it hard, the owner's act)")
	}
	if expiresSet && a.Expires != "" {
		exp, ok := a.ExpiresOn()
		if !ok {
			return fmt.Errorf("--expires %q: want YYYY-MM-DD", a.Expires)
		}
		y, m, d := ctx.now().Date()
		if exp.Before(time.Date(y, m, d, 0, 0, 0, 0, exp.Location())) {
			return fmt.Errorf("--expires %s is in the past: a decision expires today or later", a.Expires)
		}
	}
	return nil
}

// projectWorkflows is each distinct workflow the issues use.
func projectWorkflows(ctx *Context, issues []*tracker.Issue) []*tracker.WorkflowConfig {
	seen := map[string]bool{}
	var out []*tracker.WorkflowConfig
	for _, i := range issues {
		key := i.System + "|" + i.Type + "|" + ctx.Project.DepartmentOf(i)
		if seen[key] {
			continue
		}
		seen[key] = true
		if wf := ctx.Project.LoadWorkflowForIssue(i); wf != nil {
			out = append(out, wf)
		}
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func prefixJoin(sep string, items []string) string {
	if len(items) == 0 {
		return ""
	}
	return sep + strings.Join(items, sep)
}

// ownerActRules says, for a write, who may run it as an owner act.
type ownerActRules struct {
	me    Identity
	relay *tracker.DecisionRelay
	err   error // the refusal when this session may not run an owner act
}

// ownerActFor applies approve's rule to this session: a worker is refused,
// a lead (in scope) relays the owner's --quote, the owner's shell may.
func ownerActFor(ctx *Context, issue *tracker.Issue, issues []*tracker.Issue, quote string) ownerActRules {
	me := whoAmI(ctx.Project, issues)
	r := ownerActRules{me: me}
	switch me.Role {
	case tracker.RoleWorker:
		route := tracker.LeadFor(ctx.Project, issue, issues, sessionLive(ctx.Project))
		own := me.Slug
		if own == "" {
			own = issue.Slug
		}
		r.err = fmt.Errorf("changes to a hard decision are the owner's: a worker can't make a decision hard, amend a hard one or replace it. Ask the CEO (%s, %s) on your issue trail:\n  %s",
			route.Lead, liveWord(route.Live), tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli comment %s \"question: …\"", own)))
	case tracker.RoleProjectLead, tracker.RoleDepartmentLead:
		if refusal := outOfScope(ctx.Project, me, issue, "changes decisions of"); refusal != "" {
			r.err = errors.New(refusal)
			break
		}
		if strings.TrimSpace(quote) == "" {
			r.err = fmt.Errorf("a change to a hard decision is the owner's act: relay the owner's own words with --quote \"…\" (a lead never makes it on its own)")
			break
		}
		_, on, _ := yolo.Active(yoloRoot(ctx.Project), ctx.Project.Slug, ctx.now())
		r.relay = &tracker.DecisionRelay{Lead: me.Name, Quote: strings.TrimSpace(quote), YOLO: on}
	}
	return r
}

// decisionWrite is where this session's decision write is logged and who
// runs it; relay is set for an owner act a lead relays.
func decisionWrite(ctx *Context, me Identity, relay *tracker.DecisionRelay) tracker.DecisionWrite {
	w := tracker.DecisionWrite{IssueDir: ctx.Project.IssueDir, Actor: tracker.DecisionActor{Name: me.Name, Role: tracker.ActorAgent}}
	switch {
	case relay != nil:
		w.Actor.Role, w.Relay = tracker.ActorLeadRelay, relay
	case me.Role == tracker.RoleUnmanaged:
		w.Actor = tracker.DecisionActor{Name: "owner", Role: tracker.ActorOwnerShell}
	}
	return w
}

// workerScopeRefusal: a worker may scope a soft decision to its issue, a
// type or a system; project and department scopes reach every briefing in
// the project or department, so they are the lead's.
func workerScopeRefusal(ctx *Context, me Identity, issue *tracker.Issue, issues []*tracker.Issue, scope string) error {
	if me.Role != tracker.RoleWorker {
		return nil
	}
	kind, _ := tracker.DecisionAttrs{Scope: scope}.ScopeKind()
	if kind != tracker.ScopeProject && kind != tracker.ScopeDepartment {
		return nil
	}
	route := tracker.LeadFor(ctx.Project, issue, issues, sessionLive(ctx.Project))
	return fmt.Errorf("--scope %s reaches every issue in the %s: ask the lead to scope it (%s, %s):\n  %s", scope, kind,
		route.Lead, liveWord(route.Live), tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli comment %s \"question: scope %s to %s?\"", issue.Slug, "D-n", scope)))
}

// runDecideAmend changes only the attribute group of one of this issue's
// decisions.
func runDecideAmend(ctx *Context, issue *tracker.Issue, issues []*tracker.Issue, id, quote string, f *attrFlags) error {
	ref := tracker.NormalizeDecisionRef(issue.Slug, id)
	if ref == "" || !strings.HasPrefix(ref, issue.Slug+"#") {
		refSlug, _, _ := strings.Cut(strings.TrimPrefix(ref, "#"), "#")
		if refSlug != "" {
			return fmt.Errorf("--amend %s: a decision is amended on its own issue:\n  %s", id, tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli decide %s --amend %s …", refSlug, strings.TrimPrefix(ref, refSlug+"#"))))
		}
		return fmt.Errorf("--amend %q: want a D-n of this issue", id)
	}
	if !f.any() {
		return fmt.Errorf("--amend %s: nothing to amend; give --scope, --hard, --soft, --at, --paths or --expires", ref)
	}
	_, num, _ := strings.Cut(ref, "#")
	e := tracker.BuildDecisionIndex(issues, tracker.ClosedIssueFunc(ctx.Project)).Lookup(ref)
	switch {
	case e == nil:
		return fmt.Errorf("--amend %s: no such decision\n\nList the decisions in this issue:\n  issue-cli decisions --issue %s", ref, issue.Slug)
	case e.ReplacedBy != nil:
		return fmt.Errorf("--amend %s: it is replaced by %s; amend that one", ref, e.ReplacedBy.Ref)
	}
	if f.set["expires"] {
		switch {
		case e.Ended != "":
			return fmt.Errorf("--amend %s", tracker.ExpiryRefusal(ref, tracker.ErrDecisionEnded))
		case e.ExpiredOn(ctx.now()):
			return fmt.Errorf("--amend %s: it expired after %s; renew it or let it go, so the log says which:\n  %s\n  %s", ref, e.Expires,
				tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli decide %s --renew %s --expires YYYY-MM-DD", issue.Slug, e.ID)),
				tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli decide %s --let-go %s", issue.Slug, e.ID)))
		}
	}
	owner := ownerActFor(ctx, issue, issues, quote)
	me := owner.me
	if me.Role == tracker.RoleWorker && me.Slug != issue.Slug {
		route := tracker.LeadFor(ctx.Project, issue, issues, sessionLive(ctx.Project))
		return fmt.Errorf("--amend %s: a worker amends decisions of its own issue only; ask the lead (%s, %s) on your issue trail", ref, route.Lead, liveWord(route.Live))
	}
	// Refuse what we can before the write; fn checks again under the lock.
	check := func(cur tracker.Decision) (tracker.DecisionAttrs, error) {
		next, err := f.apply(cur.DecisionAttrs)
		if err != nil {
			return next, err
		}
		if cur.Hard || next.Hard {
			if owner.err != nil {
				return next, owner.err
			}
			if !cur.Hard && tracker.NeedsOwnerWords(cur) {
				return next, fmt.Errorf("--hard needs the owner's words on the line: confirm it first:\n  %s",
					tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli decide %s --confirm %s --quote \"…\"", issue.Slug, cur.ID)))
			}
		}
		if next.Scope != cur.ScopeOrDefault() {
			if err := workerScopeRefusal(ctx, me, issue, issues, next.Scope); err != nil {
				return next, err
			}
		}
		return next, checkAttrs(ctx, issues, next, f.set["expires"])
	}
	if _, err := check(e.Decision); err != nil {
		return err
	}
	relay := owner.relay
	if !e.Hard && !*f.hard {
		relay = nil // a soft amend is not an owner act
	}
	before, after, err := tracker.AmendDecisionAs(issue.FilePath, issue.Slug, num, check, ctx.now(), decisionWrite(ctx, me, relay))
	if errors.Is(err, tracker.ErrNothingChanged) {
		return fmt.Errorf("--amend %s: nothing changed; its group is already %s", ref, groupOrNone(tracker.RenderAttrs(before.DecisionAttrs)))
	}
	if err != nil {
		return fmt.Errorf("failed to amend %s: %w", ref, err)
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, decideOutput{Decision: after, StaleCiters: []staleCiter{}, Decisions: decisionsURL(ctx.Project)})
	}
	fmt.Fprintf(ctx.Stdout, "✓ Amended %s: %s → %s\n", after.Ref, groupOrNone(tracker.RenderAttrs(before.DecisionAttrs)), groupOrNone(tracker.RenderAttrs(after.DecisionAttrs)))
	fmt.Fprintln(ctx.Stdout, after.Raw)
	if relay != nil {
		fmt.Fprintf(ctx.Stdout, "Relayed the owner's words in a decision: comment on %s%s.\n", issue.Slug, yoloNote(relay))
	}
	fmt.Fprintf(ctx.Stdout, "History: %s\n", tracker.InjectProject(ctx.Project, "issue-cli decisions log --ref "+after.Ref))
	return nil
}

func groupOrNone(g string) string {
	if g == "" {
		return "{} (scope issue, soft)"
	}
	return g
}

func yoloNote(r *tracker.DecisionRelay) string {
	if r != nil && r.YOLO {
		return "; YOLO is on, so it waits in the after-the-fact review (issue-cli yolo queue)"
	}
	return ""
}

// attrsSummary is the scope line decide prints after recording.
func attrsSummary(a tracker.DecisionAttrs) string {
	s := "scope: " + a.ScopeOrDefault()
	if a.ScopeOrDefault() == tracker.ScopeIssue {
		s += " (default)"
	}
	if a.Hard {
		s += " · hard"
	} else {
		s += " · soft"
	}
	if len(a.At) > 0 {
		s += " · at: " + strings.Join(a.At, ", ")
	}
	if len(a.Paths) > 0 {
		s += " · paths: " + strings.Join(a.Paths, ", ")
	}
	if a.Expires != "" {
		s += " · expires: " + a.Expires
	}
	return s
}
