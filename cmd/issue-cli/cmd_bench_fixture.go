package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/depttemplate"
	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// runBenchFixture is `bench fixture init <dir> [--config <projects.yaml>]`:
// the fixture project with three done tasks and known solutions
// (bench.FixtureInit). It adds no task to a benchmark: the owner picks.
func runBenchFixture(ctx *Context, args []string) error {
	if len(args) == 0 || args[0] != "init" {
		if _, err := parsePositionals(ctx, "bench fixture", args, -1); err != nil {
			return err
		}
		return &usageError{msg: "usage: issue-cli bench fixture init <dir> [--config <projects.yaml>]"}
	}
	fs := newFlagSet("bench fixture init", ctx)
	config := fs.String("config", "", "add the fixture's entry to this projects.yaml (spliced as text; created when missing); default: print it")
	if err := parseFlagsArgs(ctx, fs, args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return &usageError{msg: "usage: issue-cli bench fixture init <dir> [--config <projects.yaml>]"}
	}
	if *config != "" {
		if err := fixtureConfigFree(*config); err != nil {
			return err
		}
	}
	f, err := bench.FixtureInit(fs.Arg(0))
	if err != nil {
		return err
	}
	wrote := ""
	if *config != "" {
		if wrote, err = addFixtureEntry(*config, f); err != nil {
			return fmt.Errorf("fixture written to %s, but projects.yaml not changed: %w", f.Dir, err)
		}
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, map[string]any{"fixture": f, "config": wrote})
	}
	fmt.Fprintf(ctx.Stdout, "✓ Fixture project written to %s\n", f.Dir)
	fmt.Fprintf(ctx.Stdout, "  repo: %s (main merged the three tasks' branches)\n  tracker: %s\n", f.Repo, f.Tracker)
	fmt.Fprintln(ctx.Stdout, "  done tasks, each with a design (features), hidden tests and docs:")
	for _, s := range f.Tasks {
		fmt.Fprintf(ctx.Stdout, "    %s\n", s)
	}
	if wrote != "" {
		fmt.Fprintf(ctx.Stdout, "  added project %s to %s\n", bench.FixtureSlug, wrote)
	} else {
		fmt.Fprintf(ctx.Stdout, "\nAdd it to projects.yaml (or run again with --config <file>):\n\n%s\n", indentLines(f.Entry, "  "))
	}
	// The commands run as printed: the entry is in wrote, not the default config.
	cli := "issue-cli --project " + bench.FixtureSlug
	if wrote != "" {
		cli = "issue-cli --config " + shell.Quote(wrote) + " --project " + bench.FixtureSlug
	}
	fmt.Fprintf(ctx.Stdout, "\nNext: %s bench candidates\n", cli)
	fmt.Fprintf(ctx.Stdout, "An agent proposes on its issue trail; the owner adds: %s bench task add <benchmark> <slug>\n", cli)
	return nil
}

// fixtureConfigFree refuses a config that already has the fixture's slug,
// before anything is written.
func fixtureConfigFree(config string) error {
	if _, err := os.Stat(config); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	cfg, err := tracker.LoadProjectsConfig(config)
	if err != nil {
		return err
	}
	for _, p := range cfg.Projects {
		if p.Slug == bench.FixtureSlug {
			return fmt.Errorf("%s already has a project %s: remove it or pass another --config", config, bench.FixtureSlug)
		}
	}
	return nil
}

// addFixtureEntry splices the entry into config, or creates the file.
func addFixtureEntry(config string, f *bench.Fixture) (string, error) {
	abs, err := filepath.Abs(config)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(abs); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return "", err
		}
		out, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return "", err
		}
		_, werr := out.WriteString("projects:\n" + indentLines(f.Entry, "  "))
		if cerr := out.Close(); werr == nil {
			werr = cerr
		}
		return abs, werr
	}
	check := func(p *tracker.Project) error {
		if p.IssueDir != f.Issues {
			return fmt.Errorf("project %s in the result points at %s, not %s", bench.FixtureSlug, p.IssueDir, f.Issues)
		}
		return nil
	}
	return abs, depttemplate.AddProject(abs, bench.FixtureSlug, f.Entry, check)
}
