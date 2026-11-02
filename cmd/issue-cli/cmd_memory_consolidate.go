package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/retro"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Phase 3 of shared memory: candidates from an issue's trail and the
// consolidation pack. Both only read: the engine gathers, the agent
// decides through the update step (docs/memory.md, Consolidation and decay).

// candidatePrefixes are the comments that can hold a fact. Machine-written
// ones (approval:, dispatch:, tests:, yolo-owner-check:) record the
// process and are skipped.
var candidatePrefixes = []string{"progress:", "question:", "answer:", "risk:", "decision:"}

// candidate is one piece of an issue's trail an agent could turn into a
// memory.
type candidate struct {
	N    int    `json:"n"`
	From string `json:"from"` // comment, decision, retro
	// Comment is the 1-based comment index (memory add --comment N).
	Comment int    `json:"comment,omitempty"`
	Ref     string `json:"ref"` // "comment 12", "D-3", "retro <file> · <label>"
	Text    string `json:"text,omitempty"`
	// Secret is the kind FindSecret saw; the text is then left out.
	Secret string `json:"secret,omitempty"`
	// Remembered is the memory already made from it.
	Remembered string       `json:"remembered,omitempty"`
	Similar    []memory.Hit `json:"similar,omitempty"`
	hash       string
}

// issueCandidates gathers the trail of issue: fact-bearing comments, its
// decisions and its retros. No transcript or pane text is read.
func issueCandidates(issue *tracker.Issue, comments []tracker.Comment, retros []*retro.Entry) []candidate {
	var out []candidate
	addC := func(c candidate) {
		c.Text = strings.TrimSpace(c.Text)
		if c.Text == "" {
			return
		}
		if k := memory.FindSecret(c.Text); k != "" {
			c.Secret, c.Text = k, ""
		} else {
			c.hash = memory.Hash(c.Text)
			if r := []rune(c.Text); len(r) > memory.MaxText {
				c.Text = string(r[:memory.MaxText-1]) + "…"
			}
		}
		c.N = len(out) + 1
		out = append(out, c)
	}
	for i, cm := range comments {
		text := strings.TrimSpace(cm.Text)
		low := strings.ToLower(text)
		for _, p := range candidatePrefixes {
			if strings.HasPrefix(low, p) {
				addC(candidate{From: "comment", Comment: i + 1, Ref: fmt.Sprintf("comment %d (%s)", i+1, strings.TrimSuffix(p, ":")), Text: strings.TrimSpace(text[len(p):])})
				break
			}
		}
	}
	for _, d := range tracker.ParseDecisions(issue.Slug, issue.BodyRaw) {
		addC(candidate{From: "decision", Ref: d.ID, Text: strings.TrimPrefix(strings.TrimSpace(d.Text), "(provisional) ")})
	}
	for _, e := range retros {
		if e.IssueSlug != issue.Slug {
			continue
		}
		secs := retro.Sections(e.Body)
		if len(secs) == 0 {
			addC(candidate{From: "retro", Ref: "retro " + e.FileName, Text: e.Body})
			continue
		}
		for _, s := range secs {
			if retro.None(s.Text) {
				continue
			}
			for _, item := range retro.Items(s.Text) {
				addC(candidate{From: "retro", Ref: "retro " + e.FileName + " · " + s.Label, Text: item})
			}
		}
	}
	return out
}

// markRemembered sets Remembered on each candidate a memory already came
// from (the same issue and comment, or the same words).
func markRemembered(cands []candidate, slug string, mems []memory.Memory) {
	for i := range cands {
		c := &cands[i]
		for _, m := range mems {
			if (c.Comment > 0 && m.Source.Issue == slug && m.Source.Comment == c.Comment) || (c.hash != "" && m.Hash == c.hash) {
				c.Remembered = m.ID
				break
			}
		}
	}
}

func runMemoryCandidates(ctx *Context, args []string) error {
	slug, rest, err := requireSlug(args, "memory candidates")
	if err != nil {
		return err
	}
	fs := newFlagSet("memory candidates", ctx)
	all := fs.Bool("all", false, "also list the ones already remembered")
	if err := parseFlagsArgs(ctx, fs, rest); err != nil {
		return err
	}
	issue, issues, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	comments, err := tracker.LoadComments(issue.FilePath)
	if err != nil {
		return err
	}
	retros, _ := retro.Load(filepath.Join(projectRoot(ctx.Project), "retros"))
	cands := issueCandidates(issue, comments, retros)

	who := memoryCaller(ctx.Project, issues)
	mems, err := visibleMemories(ctx, who.Viewer, memFilter{All: true})
	if err != nil {
		return err
	}
	markRemembered(cands, issue.Slug, mems)
	var active []memory.Memory
	for _, m := range mems {
		if m.Valid(ctx.now()) {
			active = append(active, m)
		}
	}
	ranker, _ := memoryRanker(ctx)
	var shown []candidate
	for _, c := range cands {
		if c.Remembered != "" && !*all {
			continue
		}
		if c.Text != "" && c.Remembered == "" {
			c.Similar = ranker.Rank(memory.Query{Text: c.Text, System: issue.System, Limit: 3}, active)
		}
		shown = append(shown, c)
	}
	if ctx.JSONOutput {
		if shown == nil {
			shown = []candidate{}
		}
		return writeJSON(ctx.Stdout, shown)
	}
	printCandidates(ctx.Stdout, issue, shown, len(cands)-len(shown))
	return nil
}

func printCandidates(w io.Writer, issue *tracker.Issue, cands []candidate, hidden int) {
	fmt.Fprintf(w, "== Memory candidates: %s ==\n", issue.Slug)
	if len(cands) == 0 {
		if hidden > 0 {
			fmt.Fprintf(w, "All %d candidates are remembered already (--all lists them).\n", hidden)
		} else {
			fmt.Fprintf(w, "No candidates on %s (no progress:, question:, answer:, risk: or decision: comments, decisions or retro).\n", issue.Slug)
		}
		return
	}
	fmt.Fprintln(w, "From the issue's comments, decisions and retro; no transcript is read. Nothing is written: turn the ones worth keeping into a fact in your own words.")
	for _, c := range cands {
		fmt.Fprintf(w, "\nc%d · %s\n", c.N, c.Ref)
		switch {
		case c.Secret != "":
			fmt.Fprintf(w, "  (looks like a %s; not shown, and not a memory: name where it lives, env:<VAR>)\n", c.Secret)
			continue
		case c.Remembered != "":
			fmt.Fprintf(w, "  %s\n  remembered as %s\n", c.Text, c.Remembered)
			continue
		}
		fmt.Fprintf(w, "  %s\n", c.Text)
		for _, h := range c.Similar {
			fmt.Fprintf(w, "  ~ %s (%s) %s\n", h.ID, h.Kind, h.Text)
		}
	}
	if hidden > 0 {
		fmt.Fprintf(w, "\n(%d already remembered: --all lists them)\n", hidden)
	}
	fmt.Fprintf(w, "\nKeep one: issue-cli memory add \"<the fact, in your words>\" --issue %s [--comment N] --kind fact|convention|pitfall|howto|who\n", issue.Slug)
	fmt.Fprintln(w, "Close to a ~ memory? --updates <id> (same fact, better worded), --supersedes <id> (no longer true), or --new (a different fact).")
}

func runMemoryConsolidate(ctx *Context, args []string) error {
	fs := newFlagSet("memory consolidate", ctx)
	scope := fs.String("scope", "", "only this scope: project, department:<key>, agent:<name> or machine")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "usage: issue-cli memory consolidate [--scope S] [--json]"}
	}
	issues, _ := tracker.LoadIssues(ctx.Project.IssueDir)
	who := memoryCaller(ctx.Project, issues)
	mems, err := visibleMemories(ctx, who.Viewer, memFilter{All: true})
	if err != nil {
		return err
	}
	if s := strings.TrimSpace(*scope); s != "" {
		var keep []memory.Memory
		for _, m := range mems {
			if m.Scope == s {
				keep = append(keep, m)
			}
		}
		mems = keep
	}
	pack := memory.Consolidate(mems, ctx.now())
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, pack)
	}
	printPack(ctx.Stdout, pack, ctx)
	return nil
}

func printPack(w io.Writer, p memory.Pack, ctx *Context) {
	now := ctx.now()
	fmt.Fprintln(w, "== Memory consolidation ==")
	fmt.Fprintln(w, "Nothing here is written or deleted: you decide each one through the update step; old memories stay in history.")
	line := func(m memory.Memory) {
		fmt.Fprintf(w, "  %s (%s · %s · %d uses · strength %.2f) %s\n", m.ID, m.Kind, m.Day(), m.Uses, memory.Strength(m, now), m.Text)
	}
	fmt.Fprintf(w, "\nNear-duplicates (%d)\n", len(p.Clusters)+p.More)
	if len(p.Clusters) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for i, c := range p.Clusters {
		fmt.Fprintf(w, "cluster %d · %s · %.0f%% the same words\n", i+1, c.Scope, c.Similarity*100)
		for _, m := range c.Members {
			line(m)
		}
		fmt.Fprintf(w, "  merge: issue-cli memory add \"<one fact>\" --scope <its scope> --supersedes %s (or keep the best: --updates <id>, then forget the rest --reason \"merged into <id>\"; different facts: leave them)\n", strings.Join(c.IDs(), ","))
	}
	if p.More > 0 {
		fmt.Fprintf(w, "  +%d more clusters: run again after merging\n", p.More)
	}
	fmt.Fprintf(w, "\nStale: strength under %.2f, not pinned (%d)\n", memory.StaleStrength, len(p.Stale))
	if len(p.Stale) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, m := range p.Stale {
		line(m)
	}
	if len(p.Stale) > 0 {
		fmt.Fprintln(w, "  still true? leave it (decay already ranks it low). No longer true: issue-cli memory forget <id> --reason \"...\"")
	}
	fmt.Fprintf(w, "\nExpired (%d)\n", len(p.Expired))
	if len(p.Expired) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, m := range p.Expired {
		fmt.Fprintf(w, "  %s (expired %s) %s\n", m.ID, m.Expires, m.Text)
	}
	if len(p.Expired) > 0 {
		fmt.Fprintln(w, "  true again or still? add a fresh one: issue-cli memory add \"...\" --supersedes <id> [--expires YYYY-MM-DD]")
	}
	fmt.Fprintf(w, "\nPromote to CLAUDE.md: project conventions, pitfalls and howtos with %d+ uses, %d+ days old (%d)\n", memory.PromoteUses, memory.PromoteDays, len(p.Promote))
	if len(p.Promote) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, m := range p.Promote {
		line(m)
	}
	if len(p.Promote) > 0 {
		fmt.Fprintln(w, "  propose, never edit: file one issue for the owner listing the ids and the lines you would add (issue-cli create --title \"Promote memories to CLAUDE.md\"). CLAUDE.md is the owner's.")
	}
}
