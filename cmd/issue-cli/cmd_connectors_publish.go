package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/kacperwerema/dark-factory/internal/connectors"
	"github.com/kacperwerema/dark-factory/internal/directpost"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// A publish: connector is a direct publisher (internal/directpost):
// issue-cli makes the platform's calls itself, with the secrets read into
// this process only.

// PublishStatus is the reel type's status a worker publishes at: the
// owner's owner_only publish stamp is the way in.
const PublishStatus = "publish"

// Test seams: the HTTP client and the poll interval of a publisher.
var (
	publishHTTP = directpost.HTTPClient
	publishPoll time.Duration
)

func publishUsage(addr, platform string) string {
	return fmt.Sprintf(`usage: issue-cli connector exec %[1]s -- <call>      (%[2]s, a direct publisher)
  publish --file <mp4> --caption-file <txt> [--title "…"] [--link <url>] [--board "…"] [--cover-at <ms>] [--privacy <level>] [--ai-label] [--send]
          without --send a dry run: what would go out and what still holds it; nothing is sent
  token check                        does the key work and until when (free, read-only; never prints a key)
  token refresh --as-owner           the owner's shell only: new keys to Keychain (Meta reads a fresh user token on stdin)
Which channel goes direct and which through Postiz: issue-cli reel routes`, addr, platform)
}

// publishClient is c's publisher with its secrets read now: one that is
// not set is left out (Require says what publishing needs), an invalid
// reference is an error naming the reference.
func publishClient(ctx *Context, addr string, c *tracker.Connector) (*directpost.Client, error) {
	if c.URL != "" {
		if err := tracker.CheckConnectorURL(c.URL); err != nil {
			return nil, fmt.Errorf("connector %s: %w", addr, err)
		}
	}
	secrets := map[string]string{}
	for name, raw := range c.Secrets {
		ref, err := tracker.ParseSecretRef(raw)
		if err != nil {
			return nil, fmt.Errorf("connector %s: secret %s: %v", addr, name, err)
		}
		if v, ok := tracker.SecretValue(ref); ok {
			secrets[name] = v
		}
	}
	account := map[string]string{}
	for k, v := range c.Account {
		account[k] = v
	}
	return &directpost.Client{
		Platform: c.Publish,
		HTTP:     publishHTTP(),
		Hosts:    directpost.HostsFor(c.URL),
		Secrets:  secrets,
		Account:  account,
		Poll:     publishPoll,
		Now:      ctx.now,
		Progress: func(s string) { fmt.Fprintln(ctx.Stderr, s) },
	}, nil
}

// publishMissing names what c lacks to publish: secrets by reference, ids
// by account: key.
func publishMissing(c *tracker.Connector, cl *directpost.Client) []string {
	need := directpost.Require(c.Publish)
	var out []string
	for _, alts := range need.Secrets {
		set := false
		for _, k := range alts {
			if cl.Secrets[k] != "" {
				set = true
			}
		}
		if !set {
			var refs []string
			for _, k := range alts {
				if r := c.Secrets[k]; r != "" {
					refs = append(refs, k+" ("+r+")")
				} else {
					refs = append(refs, k+" (no secrets: entry)")
				}
			}
			out = append(out, "secret "+strings.Join(refs, " or ")+" is not set on this machine")
		}
	}
	for _, k := range need.Account {
		if strings.TrimSpace(c.Account[k]) == "" {
			out = append(out, "account: "+k+" is empty")
		}
	}
	return out
}

func runConnectorPublish(ctx *Context, dept *tracker.Department, addr string, c *tracker.Connector, child []string, caller connectors.Caller, hold func() error) error {
	usage := publishUsage(addr, c.Publish)
	if !directpost.Known(c.Publish) {
		return fmt.Errorf("connector %s: publish: %q is not %s (issue-cli health lint --check departments)", addr, c.Publish, strings.Join(directpost.Platforms, ", "))
	}
	if len(child) == 0 {
		return &usageError{msg: usage}
	}
	run, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch child[0] {
	case "publish":
		return runPublish(run, ctx, dept, addr, c, child[1:], caller, hold, usage)
	case "token":
		if len(child) == 2 && child[1] == "check" {
			return runTokenCheck(run, ctx, addr, c)
		}
		if len(child) >= 2 && child[1] == "refresh" {
			return runTokenRefresh(run, ctx, addr, c, child[2:], caller)
		}
	}
	return &usageError{msg: usage}
}

func runTokenCheck(run context.Context, ctx *Context, addr string, c *tracker.Connector) error {
	cl, err := publishClient(ctx, addr, c)
	if err != nil {
		return err
	}
	lines, bad, err := cl.Check(run)
	for _, l := range lines {
		fmt.Fprintln(ctx.Stdout, cl.Redact(l))
	}
	if err != nil {
		return fmt.Errorf("connector %s: %s", addr, cl.Redact(err.Error()))
	}
	if bad {
		fmt.Fprintf(ctx.Stdout, "token check: something needs the owner (connector exec %s --as-owner -- token refresh, in their own shell)\n", addr)
		return &exitCodeError{Code: 1}
	}
	return nil
}

func runTokenRefresh(run context.Context, ctx *Context, addr string, c *tracker.Connector, args []string, caller connectors.Caller) error {
	if len(args) > 0 {
		return &usageError{msg: publishUsage(addr, c.Publish)}
	}
	if !caller.Owner || caller.Agent {
		return fmt.Errorf("token refresh writes new keys to Keychain: it is the owner's, in their own shell (issue-cli connector exec %s --as-owner -- token refresh); an agent runs token check and asks the owner", addr)
	}
	cl, err := publishClient(ctx, addr, c)
	if err != nil {
		return err
	}
	user := ""
	if c.Publish == directpost.InstagramReels || c.Publish == directpost.FacebookReels {
		fmt.Fprintln(ctx.Stderr, "Paste a user token from Graph API Explorer (with "+strings.Join(directpost.MetaScopes, ", ")+"), then Enter. It is not shown or kept:")
		b, err := io.ReadAll(io.LimitReader(ctx.Stdin, 8<<10))
		if err != nil {
			return fmt.Errorf("token refresh: stdin: %v", err)
		}
		user = strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
	}
	fresh, err := cl.Refresh(run, user)
	if err != nil {
		return fmt.Errorf("connector %s: %s", addr, cl.Redact(err.Error()))
	}
	names := make([]string, 0, len(fresh))
	for k := range fresh {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		raw := c.Secrets[name]
		ref, err := tracker.ParseSecretRef(raw)
		if raw == "" || err != nil || ref.Scheme != "keychain" {
			return fmt.Errorf("connector %s: the new %s has nowhere to go: give it a keychain: reference under secrets: in projects.yaml, then refresh again", addr, name)
		}
		if err := tracker.SecretWrite(ref, fresh[name]); err != nil {
			return fmt.Errorf("connector %s: %s: %v", addr, name, err)
		}
		fmt.Fprintf(ctx.Stdout, "stored the new %s in %s\n", name, ref)
	}
	return nil
}

func runPublish(run context.Context, ctx *Context, dept *tracker.Department, addr string, c *tracker.Connector, args []string, caller connectors.Caller, hold func() error, usage string) error {
	fs := newFlagSet("connector exec "+addr+" -- publish", ctx)
	file := fs.String("file", "", "the reel, an .mp4 (or .mov)")
	captionFile := fs.String("caption-file", "", "the caption, a UTF-8 file: exactly the approved text")
	title := fs.String("title", "", "Pinterest's pin title (Facebook's video title)")
	link := fs.String("link", "", "Pinterest's destination link")
	board := fs.String("board", "", "Pinterest's board, by name (default: account: board)")
	coverAt := fs.Int("cover-at", -1, "the cover frame, in ms from the start")
	privacy := fs.String("privacy", "", "TikTok's privacy level (default SELF_ONLY)")
	aiLabel := fs.Bool("ai-label", false, "the reel is AI-made: TikTok's is_aigc; elsewhere a note to set the label by hand")
	send := fs.Bool("send", false, "publish it; without it a dry run")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 || *file == "" || *captionFile == "" {
		return &usageError{msg: usage}
	}
	caption, err := os.ReadFile(*captionFile)
	if err != nil {
		return fmt.Errorf("--caption-file: %v", err)
	}
	post := directpost.Post{File: *file, Caption: strings.TrimRight(string(caption), "\n"), Title: *title, Link: *link, Board: *board, Privacy: *privacy, AILabel: *aiLabel}
	if *coverAt >= 0 {
		post.CoverAtMS, post.HasCover = *coverAt, true
	}
	cl, err := publishClient(ctx, addr, c)
	if err != nil {
		return err
	}
	p := ctx.Project

	fmt.Fprintf(ctx.Stdout, "route: direct, %s through connector %s\n", c.Publish, addr)
	fmt.Fprintf(ctx.Stdout, "file: %s; caption: %d characters\n", filepath.Base(*file), len([]rune(post.Caption)))
	var holds []string
	holds = append(holds, directpost.Problems(c.Publish, post, c.Account)...)
	holds = append(holds, publishMissing(c, cl)...)
	if why := publishCallerHold(p, addr, caller); why != "" {
		holds = append(holds, why)
	}
	if hold != nil {
		if err := hold(); err != nil {
			holds = append(holds, err.Error())
		}
	}
	sha := ""
	if len(directpost.Problems(c.Publish, directpost.Post{File: *file, Caption: "x", Title: "x", Board: "x"}, nil)) == 0 {
		if sha, err = directpost.FileHash(*file); err == nil {
			if s := directpost.AlreadySent(p.IssueDir, addr, sha); s != nil {
				holds = append(holds, fmt.Sprintf("this file already went out through %s on %s: %s", addr, s.Date.Format("2006-01-02"), s.URL))
			}
		}
	}
	if !*send {
		if len(publishMissing(c, cl)) == 0 {
			lines, err := cl.Preflight(run, post)
			for _, l := range lines {
				fmt.Fprintln(ctx.Stdout, cl.Redact(l))
			}
			if err != nil {
				holds = append(holds, cl.Redact(err.Error()))
			}
		}
		if post.AILabel && c.Publish != directpost.TikTok {
			fmt.Fprintf(ctx.Stdout, "note: %s's API sets no AI label: turn it on by hand after it is out\n", c.Publish)
		}
		if len(holds) > 0 {
			fmt.Fprintln(ctx.Stdout, "would not send yet:")
			for _, h := range holds {
				fmt.Fprintln(ctx.Stdout, "  - "+cl.Redact(h))
			}
		} else {
			fmt.Fprintln(ctx.Stdout, "ready: add --send to publish it")
		}
		fmt.Fprintln(ctx.Stdout, "dry run: nothing was sent")
		return nil
	}
	if len(holds) > 0 {
		return fmt.Errorf("connector %s: not sent:\n  - %s", addr, cl.Redact(strings.Join(holds, "\n  - ")))
	}
	res, err := cl.Publish(run, post)
	if err != nil {
		var pe *directpost.Error
		if errors.As(err, &pe) && pe.Kind == directpost.KindToken {
			return fmt.Errorf("connector %s: %s\n(the owner checks it with: issue-cli connector exec %s -- token check)", addr, cl.Redact(err.Error()), addr)
		}
		return fmt.Errorf("connector %s: %s", addr, cl.Redact(err.Error()))
	}
	rec := directpost.Sent{Connector: addr, Platform: c.Publish, Issue: caller.Issue, File: filepath.Base(*file), SHA256: sha, ID: res.ID, URL: res.URL, Date: ctx.now().UTC()}
	if err := directpost.AppendSent(p.IssueDir, rec); err != nil {
		fmt.Fprintf(ctx.Stderr, "connector %s: published, but the published log was not written: %v\n", addr, err)
	}
	fmt.Fprintf(ctx.Stdout, "published: %s\n", res.URL)
	for _, n := range res.Notes {
		fmt.Fprintln(ctx.Stdout, "note: "+cl.Redact(n))
	}
	fmt.Fprintf(ctx.Stdout, "next: mark it on the shelf: issue-cli output publish <id> --url %s\n", res.URL)
	return nil
}

// publishCallerHold is why caller may not send yet: "" for the owner's
// shell and for a worker whose issue is at the publish status (the
// owner's publish stamp let it in); a lead or the CEO never sends.
func publishCallerHold(p *tracker.Project, addr string, caller connectors.Caller) string {
	switch {
	case caller.Owner:
		return ""
	case caller.Issue == "":
		return fmt.Sprintf("only a worker whose reel is at %q (after the owner's publish stamp) sends through %s, or the owner with --as-owner in their own shell", PublishStatus, addr)
	}
	issues, err := tracker.LoadIssues(p.IssueDir)
	if err != nil {
		return "the issues could not be read to check the publish stamp"
	}
	is := tracker.FindIssue(issues, caller.Issue)
	if is == nil {
		return fmt.Sprintf("issue %s is not on the board", caller.Issue)
	}
	if !strings.EqualFold(is.Status, PublishStatus) {
		return fmt.Sprintf("issue %s is at %q: it sends only at %q, after the owner's publish stamp (the owner's, even in YOLO)", caller.Issue, is.Status, PublishStatus)
	}
	return ""
}
