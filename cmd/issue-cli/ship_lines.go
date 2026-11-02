package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// dispatchBase is the base the issue's work branch was cut from, as the
// dispatch that cut it recorded; ok is false when none did.
func dispatchBase(issue *tracker.Issue) (tracker.Dispatch, bool) {
	store, err := tracker.LoadStats(issue.FilePath)
	if err != nil {
		return tracker.Dispatch{}, false
	}
	return tracker.DispatchBase(store)
}

// baseLine is "Base: main at 4d4d4e7 (cut by the dispatch on 2026-10-30)",
// or "" when no dispatch recorded a base.
func baseLine(issue *tracker.Issue) string {
	d, ok := dispatchBase(issue)
	if !ok {
		return ""
	}
	return fmt.Sprintf("Base: %s at %s (cut by the dispatch on %s)", d.BaseBranch, tracker.ShortSHA(d.BaseSHA), d.At.Format("2006-01-02"))
}

// baseJSON is show --json's "base", or nil.
func baseJSON(issue *tracker.Issue) map[string]string {
	d, ok := dispatchBase(issue)
	if !ok {
		return nil
	}
	return map[string]string{"branch": d.BaseBranch, "sha": d.BaseSHA, "at": d.At.Format("2006-01-02T15:04:05Z07:00")}
}

// shipDep is one depends_on issue as the ship lines show it.
type shipDep struct {
	Slug   string
	State  string // its status, or "unknown issue"
	Closed bool
	Branch string // its open work branch, "" when none
}

// shipDeps lists issue's depends_on issues with their status and, while
// one is open, its work branch (where its code is until it ships).
func shipDeps(ctx *Context, issue *tracker.Issue, issues []*tracker.Issue) []shipDep {
	var out []shipDep
	closed := tracker.ClosedIssueFunc(ctx.Project)
	for _, d := range tracker.IssueDeps(issue, issues, nil, closed) {
		if d.Kind != "depends on" {
			continue
		}
		dep := shipDep{Slug: d.Target, State: d.State, Closed: d.Closed}
		if d.Slug != "" && !d.Closed && tracker.RefExists(projectRoot(ctx.Project), "refs/heads/work/"+d.Slug) {
			dep.Branch = "work/" + d.Slug
		}
		out = append(out, dep)
	}
	return out
}

// printStartDeps prints start's "Depends on:" line: each depends_on issue
// with its status and its open branch.
func printStartDeps(w io.Writer, deps []shipDep) {
	if len(deps) == 0 {
		return
	}
	parts := make([]string, len(deps))
	for i, d := range deps {
		parts[i] = fmt.Sprintf("%s (%s)", d.Slug, d.State)
		if d.Branch != "" {
			parts[i] = fmt.Sprintf("%s (%s, branch %s)", d.Slug, d.State, d.Branch)
		}
	}
	fmt.Fprintf(w, "Depends on: %s\n", strings.Join(parts, ", "))
}

// openDepWarnings is one warning per depends_on issue that isn't done:
// the issue ships after it.
func openDepWarnings(deps []shipDep) []string {
	var out []string
	for _, d := range deps {
		if !d.Closed {
			out = append(out, fmt.Sprintf("depends_on %s is %s, not done: this issue ships after it.", d.Slug, d.State))
		}
	}
	return out
}

// isShipStatus reports whether status is where issue ships under wf.
func isShipStatus(wf *tracker.WorkflowConfig, status string) bool {
	ship := wf.ShipStatus()
	return ship != "" && strings.EqualFold(ship, status)
}

// printShippingBlock prints the facts the static shipping guidance can't
// know: which branch the main checkout has checked out (and whether it is
// the issue's base), and the depends_on issues it ships after.
func printShippingBlock(ctx *Context, issue *tracker.Issue, issues []*tracker.Issue) {
	w := ctx.Stdout
	st, haveCheckout := tracker.MainCheckoutState(projectRoot(ctx.Project))
	deps := shipDeps(ctx, issue, issues)
	if !haveCheckout && len(deps) == 0 {
		return
	}
	fmt.Fprintln(w, "== Shipping ==")
	if haveCheckout {
		fmt.Fprintf(w, "Main checkout: %s on %s\n", st.Path, st.Label())
		want, known := "", false
		if d, ok := dispatchBase(issue); ok {
			want, known = d.BaseBranch, true
		}
		switch {
		case known && st.Branch != want:
			fmt.Fprintf(w, "⚠ The main checkout has %s checked out, not %s (this issue's base). Stop and ask the lead which branch this issue ships into before fast-forwarding.\n", st.Label(), want)
		case !known && st.Branch != "main" && st.Branch != "master":
			fmt.Fprintf(w, "⚠ The main checkout has %s checked out, not main. Stop and ask the lead which branch this issue ships into before fast-forwarding.\n", st.Label())
		default:
			fmt.Fprintf(w, "Ships into %s: fast-forward it in the main checkout.\n", st.Label())
		}
	}
	for _, d := range deps {
		fmt.Fprintf(w, "Ships after %s (%s)\n", d.Slug, d.State)
		if !d.Closed {
			fmt.Fprintf(w, "⚠ %s is not done: fast-forward only after it ships, or ask the lead.\n", d.Slug)
		}
	}
	fmt.Fprintln(w)
}

// printWarnings prints one "⚠ " line per warning.
func printWarnings(w io.Writer, warnings []string) {
	for _, s := range warnings {
		fmt.Fprintf(w, "⚠ %s\n", s)
	}
}
