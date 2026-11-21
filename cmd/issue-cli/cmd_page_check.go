package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/pagecheck"
)

var pageCheckCommand = &Command{
	Name:      "page-check",
	ShortHelp: "Check pages against the page rules: <title> at most 60 characters, charset=utf-8 header (read-only GETs)",
	LongHelp: `Check each URL against the page rules every marketing department ships by:

  - <title> at most 60 characters including the brand suffix, the primary
    phrase first (counted in characters after decoding entities). The h1 and
    og:title may be longer. A page with no <title> or two fails.
  - Every text response (HTML, XML such as sitemap.xml, plain text such as
    robots.txt and llms.txt) served with charset=utf-8 in its Content-Type
    header. A <meta charset> is not enough: without the header Bing reads
    the page as Latin-1 and shows mojibake in og:title and og:description.

A sitemap.xml (or a sitemap index) is checked itself, then every <loc> in it.
Each failing URL gets a line naming the rule and the fix. Exit code 1 when a
URL breaks a rule, 0 when every URL passes.

It only reads: one GET per URL, one at a time, a timeout per URL, at most
--max URLs. It needs no project or connector.

Usage:
  issue-cli page-check https://example.com/sitemap.xml
  issue-cli page-check https://example.com/guide/ https://example.com/robots.txt
  issue-cli page-check <url> --json

Where it is used: the Draft box of a piece or seo-pass (draft → owner review),
the seo-baseline's Technical basics and the weekly SEO pass
(docs/departments.md, "Page rules").`,
	Run: runPageCheck,
}

func init() {
	registerCommand(pageCheckCommand)
}

func runPageCheck(ctx *Context, args []string) error {
	fs := newFlagSet("page-check", ctx)
	maxURLs := fs.Int("max", 500, "check at most this many URLs, sitemaps expanded")
	maxTitle := fs.Int("max-title", pagecheck.MaxTitle, "longest <title> allowed, in characters")
	timeout := fs.Duration("timeout", 10*time.Second, "timeout per URL")
	delay := fs.Duration("delay", 200*time.Millisecond, "pause between requests")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	targets := fs.Args()
	if len(targets) == 0 {
		return &usageError{msg: "page-check needs a URL or a sitemap.xml URL: issue-cli page-check https://<site>/sitemap.xml" + helpHint(fs)}
	}
	for _, t := range targets {
		if !strings.HasPrefix(t, "http://") && !strings.HasPrefix(t, "https://") {
			return &usageError{msg: fmt.Sprintf("page-check: %q is not an http(s) URL", t) + helpHint(fs)}
		}
	}
	rep := pagecheck.Run(context.Background(), targets, pagecheck.Options{
		Timeout:  *timeout,
		MaxURLs:  *maxURLs,
		MaxTitle: *maxTitle,
		Delay:    *delay,
	})
	if ctx.JSONOutput {
		if err := writeJSON(ctx.Stdout, rep); err != nil {
			return err
		}
	} else {
		printPageCheck(ctx, rep, *maxTitle)
	}
	if rep.Failed > 0 {
		return &exitCodeError{Code: 1}
	}
	return nil
}

func printPageCheck(ctx *Context, rep pagecheck.Report, maxTitle int) {
	w := ctx.Stdout
	fmt.Fprintf(w, "== Page check (title ≤ %d characters, charset=utf-8) ==\n", maxTitle)
	for _, r := range rep.Results {
		mark := "✓"
		if !r.OK() {
			mark = "✗"
		}
		var facts []string
		if r.Status != 0 {
			facts = append(facts, fmt.Sprint(r.Status))
		}
		if r.ContentType != "" {
			facts = append(facts, r.ContentType)
		}
		if r.Title != "" {
			facts = append(facts, fmt.Sprintf("title %d", r.TitleLength))
		}
		fmt.Fprintf(w, "%s %s", mark, r.URL)
		if len(facts) > 0 {
			fmt.Fprintf(w, "  (%s)", strings.Join(facts, ", "))
		}
		fmt.Fprintln(w)
		for _, n := range r.Notes {
			fmt.Fprintf(w, "    note: %s\n", n)
		}
		for _, p := range r.Problems {
			fmt.Fprintf(w, "    %s: %s\n      fix: %s\n", p.Rule, p.Text, p.Fix)
		}
		if r.Title != "" && !r.OK() {
			fmt.Fprintf(w, "    title: %q\n", r.Title)
		}
	}
	fmt.Fprintln(w)
	if rep.Skipped > 0 {
		fmt.Fprintf(w, "%d URL(s) left out by --max %d\n", rep.Skipped, len(rep.Results))
	}
	if rep.Failed == 0 {
		fmt.Fprintf(w, "page-check: PASS (%d URL(s))\n", len(rep.Results))
		return
	}
	fmt.Fprintf(w, "page-check: FAIL (%d of %d URL(s) break a page rule)\n", rep.Failed, len(rep.Results))
}
