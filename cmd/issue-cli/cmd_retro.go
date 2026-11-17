package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/postmortem"
	"github.com/kacperwerema/dark-factory/internal/retro"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

const retroUsage = `Usage:
  issue-cli retro list [--since <date>] [--json]
  issue-cli retro archive --until <time> [--since <date>] [--json]`

// retroWhoFiles is the sweep's one rule on filing, printed with the list.
// docs/CLI/overview.md and docs/patterns/lead-session.md quote it word for
// word (TestSweepFilingRuleIsWordedOnceInDocs).
const retroWhoFiles = "A sweep files issues when its brief asks for them; otherwise it lists its proposals in its report and the lead files them."

var retroCommand = &Command{
	Name:      "retro",
	ShortHelp: "Retro sweeps: list the retros and post-mortems since the last sweep, archive them (sub: list|archive)",
	LongHelp: `A retro sweep reads the files retrospective and postmortem left in retros/,
then moves them to retros/archive/. The last archive is the sweep's mark:
retros/archive/log.jsonl holds one line per archive, and list starts after
the newest one.

  list      the retros and post-mortems in retros/ written since the mark
            (with no mark, all of them), oldest first, and the archive
            command that ends this sweep. --since <YYYY-MM-DD or RFC 3339>
            reads from that time instead. It only reads.
  archive   moves the same files to retros/archive/, writes the log line and
            so moves the mark. --until is the time list printed: a retro
            written while the sweep read stays for the next one. A
            post-mortem whose actions the owner hasn't answered stays in
            retros/, in the next sweep's sight, and the output names it.
            No file is ever overwritten.

` + retroWhoFiles + `

` + retroUsage,
	Run:         runRetro,
	Subcommands: []string{"list", "archive"},
}

func init() {
	registerCommand(retroCommand)
}

func runRetro(ctx *Context, args []string) error {
	if ctx.Project == nil {
		return fmt.Errorf("retro needs a project — pass --project <slug> or run from a project root")
	}
	if len(args) == 0 {
		fmt.Fprintln(ctx.Stdout, retroUsage)
		return nil
	}
	switch args[0] {
	case "list":
		return runRetroList(ctx, args[1:])
	case "archive":
		return runRetroArchive(ctx, args[1:])
	}
	return &usageError{msg: fmt.Sprintf("unknown retro subcommand %q\n\n%s", args[0], retroUsage)}
}

// parseRetroTime reads --since and --until: RFC 3339, or for since a day
// (its local midnight).
func parseRetroTime(flag, s string, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if flag == "--since" {
		if t, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
			return t, nil
		}
		return time.Time{}, fmt.Errorf("--since %q: want YYYY-MM-DD or an RFC 3339 time", s)
	}
	return time.Time{}, fmt.Errorf("%s %q: want an RFC 3339 time, as retro list prints it", flag, s)
}

// retroSince is where a sweep starts reading: --since, else after the mark,
// else the beginning. mark is zero with no sweep logged.
func retroSince(ctx *Context, since string, now time.Time) (from, mark time.Time, err error) {
	mark, _, err = retro.LastSweep(retrosDir(ctx))
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if since != "" {
		from, err = parseRetroTime("--since", since, now.Location())
		return from, mark, err
	}
	if !mark.IsZero() {
		from = retro.AfterMark(mark)
	}
	return from, mark, nil
}

// retroRow is a retro as list prints it.
type retroRow struct {
	File  string `json:"file"`
	Path  string `json:"path"`
	Kind  string `json:"kind"`
	Issue string `json:"issue"`
	Date  string `json:"date"`
	// Waiting is the actions the owner hasn't answered, on a post-mortem
	// that stays in retros/ at archive.
	Waiting int `json:"waiting,omitempty"`
}

func retroRows(picked []*retro.Entry, waits map[string]int) []retroRow {
	rows := make([]retroRow, 0, len(picked))
	for _, e := range picked {
		kind := "retro"
		if e.Kind == postmortem.Kind {
			kind = "postmortem"
		}
		rows = append(rows, retroRow{File: e.FileName, Path: e.FilePath, Kind: kind, Issue: e.IssueSlug,
			Date: e.Time().Format(time.RFC3339), Waiting: waits[e.FilePath]})
	}
	return rows
}

func runRetroList(ctx *Context, args []string) error {
	fs := newFlagSet("retro list", ctx)
	sinceFlag := fs.String("since", "", "read from this time (YYYY-MM-DD or RFC 3339) instead of the last sweep's mark")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	now := ctx.now().Truncate(time.Second)
	from, mark, err := retroSince(ctx, *sinceFlag, now)
	if err != nil {
		return err
	}
	dir := retrosDir(ctx)
	entries, err := retro.Load(dir)
	if err != nil {
		return err
	}
	rows := retroRows(retro.Select(entries, dir, retro.Window{Since: from, Until: now}), postmortem.Waits(entries))
	until := now.Format(time.RFC3339)
	archive := "issue-cli retro archive --until " + until
	if *sinceFlag != "" {
		archive += " --since " + strings.TrimSpace(*sinceFlag)
	}
	archive = tracker.InjectProject(ctx.Project, archive)
	if ctx.JSONOutput {
		out := struct {
			Since   string     `json:"since,omitempty"`
			Until   string     `json:"until"`
			Retros  []retroRow `json:"retros"`
			Archive string     `json:"archive"`
		}{Until: until, Retros: rows, Archive: archive}
		if !from.IsZero() {
			out.Since = from.Format(time.RFC3339)
		}
		return writeJSON(ctx.Stdout, out)
	}
	switch {
	case *sinceFlag != "":
		fmt.Fprintf(ctx.Stdout, "== Retros since %s ==\n", strings.TrimSpace(*sinceFlag))
	case mark.IsZero():
		fmt.Fprintln(ctx.Stdout, "== Retros: no sweep recorded yet, so every file in retros/ ==")
	default:
		fmt.Fprintf(ctx.Stdout, "== Retros since the last sweep (%s) ==\n", mark.Format(time.RFC3339))
	}
	posts, waiting := 0, 0
	for _, r := range rows {
		note := ""
		if r.Kind == "postmortem" {
			posts++
		}
		if r.Waiting > 0 {
			waiting++
			note = fmt.Sprintf("  (%d action(s) wait for the owner: stays at archive)", r.Waiting)
		}
		fmt.Fprintf(ctx.Stdout, "  %s  %-10s  %s%s\n", r.Date, r.Kind, r.File, note)
	}
	if len(rows) == 0 {
		fmt.Fprintln(ctx.Stdout, "  none")
		return nil
	}
	fmt.Fprintf(ctx.Stdout, "%d file(s) in %s: %d retro(s), %d post-mortem(s), %d of them waiting for the owner.\n",
		len(rows), dir, len(rows)-posts, posts, waiting)
	fmt.Fprintf(ctx.Stdout, "When the sweep is done: %s\n%s\n", archive, retroWhoFiles)
	return nil
}

func runRetroArchive(ctx *Context, args []string) error {
	fs := newFlagSet("retro archive", ctx)
	sinceFlag := fs.String("since", "", "the --since the list used, if it used one")
	untilFlag := fs.String("until", "", "the time retro list printed: nothing newer moves, and the mark moves here")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	if strings.TrimSpace(*untilFlag) == "" {
		return &usageError{msg: "retro archive needs --until, the time retro list printed: a retro written while the sweep read stays for the next one\n\n" + retroUsage}
	}
	now := ctx.now()
	until, err := parseRetroTime("--until", *untilFlag, now.Location())
	if err != nil {
		return err
	}
	from, _, err := retroSince(ctx, *sinceFlag, now)
	if err != nil {
		return err
	}
	dir := retrosDir(ctx)
	entries, err := retro.Load(dir)
	if err != nil {
		return err
	}
	waits := postmortem.Waits(entries)
	by := whoAmI(ctx.Project, nil).Name
	if by == "" {
		by = "owner"
	}
	done, err := retro.Archive(dir, retro.Select(entries, dir, retro.Window{Since: from, Until: until}),
		func(e *retro.Entry) bool { return waits[e.FilePath] > 0 },
		retro.Sweep{At: now, By: by, Since: from, Until: until})
	if err != nil {
		return err
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, done)
	}
	fmt.Fprintf(ctx.Stdout, "✓ %d file(s) moved to %s; the next sweep reads after %s\n",
		len(done.Moved), filepath.Join(dir, retro.ArchiveDir), until.Format(time.RFC3339))
	fmt.Fprintf(ctx.Stdout, "log: %s\n", retro.LogPath(dir))
	for _, f := range done.Waiting {
		fmt.Fprintf(ctx.Stdout, "  stays, waits for the owner: %s\n", f)
	}
	for _, f := range done.Clashes {
		fmt.Fprintf(ctx.Stdout, "  stays, archive/ has this name already: %s\n", f)
	}
	return nil
}
