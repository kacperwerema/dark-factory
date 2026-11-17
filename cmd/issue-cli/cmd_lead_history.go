package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/leadchat"
	"github.com/kacperwerema/dark-factory/internal/period"
	"github.com/kacperwerema/dark-factory/internal/spend"
)

// lead history: search what a lead's old sessions said and ran, so a fresh
// lead looks a thing up instead of carrying the whole chat. It reads the
// digests rotate wrote and the lead's transcripts still on disk; never a
// tool's output (docs/patterns/lead-session.md#history).

func runLeadHistory(ctx *Context, args []string) error {
	fs := newFlagSet("lead history", ctx)
	days := fs.Int("days", 30, "how far back the transcripts go (digests are always read)")
	maxHits := fs.Int("max", 40, "print at most this many lines, newest first")
	leadFlag := fs.String("lead", "", "whose history (default: you as a lead, else the CEO)")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	words := fs.Args()
	if len(words) == 0 {
		return &usageError{msg: "lead history needs the words to look for\n\nUsage:\n  issue-cli lead history <words> [--days 30] [--max 40] [--lead <name>]"}
	}
	proj := ctx.Project
	if proj == nil {
		return fmt.Errorf("lead history needs a project: pass --project <slug> or run from a project root")
	}
	issues, err := proj.LoadIssues()
	if err != nil {
		return fmt.Errorf("cannot load issues: %w", err)
	}
	lead := leadStateName(proj, whoAmI(proj, issues), *leadFlag)
	sources := map[string][]leadchat.Entry{}
	for _, p := range leadchat.DigestFiles(filepath.Join(projectRoot(proj), "sessions"), lead) {
		sources[filepath.Base(p)] = leadchat.ReadDigest(p)
	}
	since := period.DaysBack(ctx.now(), *days)
	src := transcripts{}
	for _, top := range src.Sessions(spend.DefaultProjectsDir(), projectRoot(proj)) {
		st, err := os.Stat(top)
		if err != nil || st.ModTime().Before(since) || src.AgentName(top) != lead {
			continue
		}
		id := strings.TrimSuffix(filepath.Base(top), ".jsonl")
		if len(id) > 8 {
			id = id[:8]
		}
		sources["transcript "+id] = leadchat.ReadEntries(top, since)
	}
	hits := leadchat.Search(words, sources)
	w := ctx.Stdout
	if len(hits) == 0 {
		fmt.Fprintf(w, "Nothing in %s's history matches %q (%d digest(s) and transcript(s) searched, %d days).\n", lead, strings.Join(words, " "), len(sources), *days)
		return nil
	}
	for i, h := range hits {
		if i == *maxHits {
			fmt.Fprintf(w, "+%d more: add words, or --max %d\n", len(hits)-i, len(hits))
			break
		}
		fmt.Fprintln(w, leadchat.HitLine(h))
	}
	return nil
}
