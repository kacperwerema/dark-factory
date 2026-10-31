package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/michal-franc/issue-viewer/internal/telemetry"
	"github.com/michal-franc/issue-viewer/internal/telemetry/share"
	"github.com/michal-franc/issue-viewer/internal/tracker"
	"github.com/michal-franc/issue-viewer/internal/workflowupgrade"
)

// shareHint is the line telemetry subcommands print while sharing is off.
const shareHint = "share: off (turn it back on: issue-cli telemetry share on)"

// offHowTo is how to turn sharing off, in the notice and status lines.
const offHowTo = "issue-cli telemetry share off, or ISSUE_CLI_TELEMETRY=off"

// shareStatusLine is the share state in one line, for telemetry path/report.
func shareStatusLine() string {
	st := share.Current()
	switch {
	case st.Sending:
		return "share: on, anonymous daily usage stats go to " + st.URL + " (what was sent: issue-cli telemetry sent; off: " + offHowTo + ")"
	case st.Settings.Share:
		return "share: on, but " + st.Why + " (off: " + offHowTo + ")"
	}
	return shareHint
}

func runTelemetryShare(ctx *Context, args []string) error {
	rest, err := parsePositionals(ctx, "telemetry share", args, 1)
	if err != nil {
		return err
	}
	if len(rest) != 1 || (rest[0] != "on" && rest[0] != "off" && rest[0] != "status") {
		return fmt.Errorf("usage: issue-cli telemetry share on|off|status")
	}
	switch rest[0] {
	case "on":
		s, err := share.TurnOn(ctx.Now())
		if err != nil {
			return fmt.Errorf("turning sharing on: %w", err)
		}
		fmt.Fprintf(ctx.Stdout, "✓ Sharing on for this install (id %s…, random, kept in %s)\n", s.Install[:8], share.SettingsPath())
	case "off":
		if _, err := share.TurnOff(ctx.Now()); err != nil {
			return fmt.Errorf("turning sharing off: %w", err)
		}
		fmt.Fprintln(ctx.Stdout, "✓ Sharing off; the install id is forgotten, so turning it on again starts a new install")
	}
	st := share.Current()
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, map[string]interface{}{
			"share": st.Settings.Share, "sending": st.Sending, "url": st.URL, "why": st.Why,
			"settings": share.SettingsPath(), "sent_log": share.SentLogPath(),
		})
	}
	if st.Sending {
		fmt.Fprintf(ctx.Stdout, "share: on, anonymous daily usage stats go to %s\n", st.URL)
	} else {
		fmt.Fprintf(ctx.Stdout, "share: %s; nothing is sent (%s)\n", onOff(st.Settings.Share), st.Why)
	}
	fmt.Fprintln(ctx.Stdout, "What is sent: docs/CLI/telemetry.md, \"Sharing usage\" · What was sent: issue-cli telemetry sent · Off: "+offHowTo)
	return nil
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// shareVocab is the closed vocabulary a batch may name.
func shareVocab() share.Vocab {
	var features []string
	if entries, err := workflowupgrade.Catalog(); err == nil {
		features = workflowupgrade.IDs(entries)
	}
	return share.NewVocab(buildManifest(), workflowupgrade.Templates(), features)
}

// shareSources are the local telemetry files a batch is built from: every
// configured project with telemetry on, plus the global fallback.
func shareSources(ctx *Context) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for i := range ctx.AllProjects {
		p := &ctx.AllProjects[i]
		if !p.TelemetryEnabled() {
			continue
		}
		if root := p.TelemetryRoot(); root != "" {
			add(telemetry.ProjectPath(root))
		}
	}
	add(telemetry.GlobalPath())
	return out
}

// shareMeta is what a batch says about the install: build, platform, the
// bundled templates and catalog features in use, and counts.
func shareMeta(ctx *Context, install string) share.Meta {
	m := share.Meta{Install: install, Ver: Version, OS: runtime.GOOS, Arch: runtime.GOARCH}
	tmpl, feat := map[string]bool{}, map[string]bool{}
	entries, _ := workflowupgrade.Catalog()
	for i := range ctx.AllProjects {
		p := &ctx.AllProjects[i]
		m.Counts.Projects++
		m.Counts.Departments += len(p.Departments)
		m.Counts.Issues += countIssueFiles(p.IssueDir)
		raw, err := os.ReadFile(p.WorkflowPath())
		if err != nil {
			continue
		}
		name, _, ok := workflowupgrade.ParseStamp(workflowupgrade.ReadStamp(raw))
		if ok && workflowupgrade.IsBundled(name) {
			tmpl[name] = true
		} else {
			tmpl[share.CustomTemplate] = true
		}
		if doc := workflowupgrade.ParseDoc(raw); doc != nil {
			for _, e := range entries {
				if workflowupgrade.Detect(e, doc).Matches > 0 {
					feat[e.ID] = true
				}
			}
		}
	}
	for t := range tmpl {
		m.Templates = append(m.Templates, t)
	}
	for f := range feat {
		m.Features = append(m.Features, f)
	}
	return m
}

func countIssueFiles(dir string) int {
	if dir == "" {
		return 0
	}
	n := 0
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && p != dir && tracker.NotIssueFolder(d.Name()) {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".md") {
			n++
		}
		return nil
	})
	return n
}

// shareBatches builds the batch for each day with calls, from the local
// files.
func shareBatches(ctx *Context, install string, days []string) ([]share.Batch, error) {
	if len(days) == 0 {
		return nil, nil
	}
	since, _ := time.Parse("2006-01-02", days[0])
	var events []telemetry.Event
	for _, p := range shareSources(ctx) {
		if err := telemetry.ReadFiles(p, since, func(e telemetry.Event) { events = append(events, e) }); err != nil {
			return nil, fmt.Errorf("reading %s: %w", p, err)
		}
	}
	vocab := shareVocab()
	meta := shareMeta(ctx, install)
	var out []share.Batch
	for _, d := range days {
		b := share.Build(events, d, meta, vocab)
		if err := share.Validate(b, vocab); err != nil {
			return nil, fmt.Errorf("batch for %s failed its own check (%v); nothing is sent", d, err)
		}
		if len(b.Commands) == 0 {
			continue
		}
		out = append(out, b)
	}
	return out, nil
}

// shareSender posts batches; tests swap in one with no backoff.
var shareSender = share.Sender{}

func runTelemetrySend(ctx *Context, args []string) error {
	fset := newFlagSet("telemetry send", ctx)
	quiet := fset.Bool("quiet", false, "print nothing (the automatic daily send)")
	if err := parseFlags(ctx, fset, args); err != nil {
		return err
	}
	st := share.Current()
	if !st.Sending {
		if *quiet {
			return nil
		}
		return fmt.Errorf("nothing sent: %s\n%s", st.Why, shareHint)
	}
	settings, _, err := share.EnsureInstall(ctx.Now())
	if err != nil {
		return fmt.Errorf("writing the share setting: %w", err)
	}
	sent, err := share.ReadSent()
	if err != nil {
		return fmt.Errorf("reading the sent log: %w", err)
	}
	days := share.PendingDays(ctx.Now(), share.SentDays(sent, st.URL))
	batches, err := shareBatches(ctx, settings.Install, days)
	if err != nil {
		return err
	}
	sender := shareSender
	failed := 0
	for _, b := range batches {
		status, err := sender.Send(st.URL, b)
		rec := share.SentRecord{At: ctx.Now().UTC().Format(time.RFC3339), Day: b.Day, URL: st.URL, Status: status, OK: err == nil, Batch: b}
		if err != nil {
			rec.Error = telemetry.Truncate(err.Error(), 200)
			failed++
		}
		_ = share.AppendSent(rec)
		if !*quiet {
			if err != nil {
				fmt.Fprintf(ctx.Stdout, "✗ %s: %v\n", b.Day, err)
			} else {
				fmt.Fprintf(ctx.Stdout, "✓ %s sent (%d commands)\n", b.Day, len(b.Commands))
			}
		}
	}
	if !*quiet && len(batches) == 0 {
		fmt.Fprintln(ctx.Stdout, "nothing pending: no unsent calls on the full days of the last week")
	}
	if failed > 0 && !*quiet {
		return fmt.Errorf("%d of %d batches not sent; they stay pending for the next send", failed, len(batches))
	}
	return nil
}

func runTelemetrySent(ctx *Context, args []string) error {
	fset := newFlagSet("telemetry sent", ctx)
	full := fset.Bool("full", false, "print each sent batch's JSON too")
	if err := parseFlags(ctx, fset, args); err != nil {
		return err
	}
	recs, err := share.ReadSent()
	if err != nil {
		return fmt.Errorf("reading the sent log: %w", err)
	}
	if ctx.JSONOutput {
		if recs == nil {
			recs = []share.SentRecord{}
		}
		return writeJSON(ctx.Stdout, map[string]interface{}{"log": share.SentLogPath(), "sent": recs})
	}
	fmt.Fprintln(ctx.Stdout, share.SentLogPath())
	if len(recs) == 0 {
		fmt.Fprintln(ctx.Stdout, "nothing sent yet")
		fmt.Fprintln(ctx.Stdout, shareStatusLine())
		return nil
	}
	for _, r := range recs {
		mark := "✓"
		if !r.OK {
			mark = "✗"
		}
		line := fmt.Sprintf("%s %s  day %s  → %s  (%d)", mark, r.At, r.Day, r.URL, r.Status)
		if r.Error != "" {
			line += "  " + r.Error
		}
		fmt.Fprintln(ctx.Stdout, line)
		if *full {
			_ = writeJSON(ctx.Stdout, r.Batch)
		}
	}
	return nil
}

// maybeAutoSend starts the daily send in a detached child when sharing is
// on, a URL is set and the last start is a day old. It never blocks the
// call, prints nothing, and runs only from an installed issue-cli binary
// (never a test binary).
func maybeAutoSend() {
	defer func() { _ = recover() }()
	if !share.Current().Sending {
		return
	}
	exe, err := os.Executable()
	if err != nil || filepath.Base(exe) != "issue-cli" {
		return
	}
	stamp := share.StampPath()
	if stamp == "" {
		return
	}
	if fi, err := os.Stat(stamp); err == nil && time.Since(fi.ModTime()) < 24*time.Hour {
		return
	}
	if err := os.MkdirAll(filepath.Dir(stamp), 0o755); err != nil {
		return
	}
	unlock, err := telemetry.LockFile(stamp+".lock", 50*time.Millisecond)
	if err != nil {
		return
	}
	defer unlock()
	if fi, err := os.Stat(stamp); err == nil && time.Since(fi.ModTime()) < 24*time.Hour {
		return // another call started it while we waited
	}
	if err := os.WriteFile(stamp, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600); err != nil {
		return
	}
	cmd := exec.Command(exe, "telemetry", "send", "--quiet")
	cmd.Env = append(os.Environ(), telemetry.SkipEnv+"=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if cmd.Start() == nil {
		_ = cmd.Process.Release()
	}
}

// shareNotice prints the one-time first-run line on w: sharing is on by
// default (D-7), so the first call says so once, says how to turn it off,
// and records that it was shown by writing the settings file (with a fresh
// install id). Nothing when telemetry is off, the call is skipped, or a
// settings file exists (a "share off" in this very call wrote one).
func shareNotice(w io.Writer, now time.Time) {
	defer func() { _ = recover() }()
	if telemetry.EnvDisabled() || os.Getenv(telemetry.SkipEnv) != "" {
		return
	}
	s, created, err := share.EnsureInstall(now)
	if err != nil || !created {
		return
	}
	line := "issue-cli: anonymous usage stats are on (command and flag names and counts only, once a day"
	if s.EffectiveURL() == "" {
		line += "; no collector is set yet, so nothing is sent"
	}
	line += "). What is sent: docs/CLI/telemetry.md. Turn off: " + offHowTo + "."
	fmt.Fprintln(w, line)
}
