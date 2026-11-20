package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/ci"
	"github.com/kacperwerema/dark-factory/internal/gitmain"
)

// runWorktreeDeps is `issue-cli worktree deps [--own|--shared] [--dir <wt>]`:
// a worktree's node_modules links to the main checkout's (ShareDeps, made
// with the worktree). With no flag it says each folder's state; --own
// removes the links (the link only, never through it) and runs the lock
// file's install in the worktree, for a branch that changes dependencies;
// --shared links again where node_modules is missing and the lock file
// matches the main checkout's.
func runWorktreeDeps(ctx *Context, args []string) error {
	const usage = "usage: issue-cli worktree deps [--own | --shared] [--dir <worktree>]"
	fs := newFlagSet("worktree deps", ctx)
	own := fs.Bool("own", false, "remove the links to the main checkout's node_modules and install this worktree's own")
	shared := fs.Bool("shared", false, "link the main checkout's node_modules where this worktree has none and the lock file matches")
	dirFlag := fs.String("dir", "", "the worktree (default: the one you are in)")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 || (*own && *shared) {
		return &usageError{msg: usage}
	}
	dir := *dirFlag
	if dir == "" {
		dir, _ = os.Getwd()
	}
	wt, err := gitmain.Git(dir, "rev-parse", "--show-toplevel")
	if err != nil || wt == "" {
		return fmt.Errorf("worktree deps: %s is not in a git checkout", dir)
	}
	main := gitmain.Root(wt)
	if sameDir(main, wt) {
		return fmt.Errorf("worktree deps: %s is the main checkout, whose node_modules the worktrees share; run it in a worktree (or --dir <worktree>)", wt)
	}
	switch {
	case *own:
		owned, err := agentrun.OwnDeps(main, wt)
		for _, d := range owned {
			fmt.Fprintf(ctx.Stdout, "✓ %s is this worktree's own copy of the main checkout's now (a clone where the disk has them, no download; the main checkout's stay)\n", filepath.Join(wt, d, "node_modules"))
		}
		if err != nil {
			return fmt.Errorf("worktree deps --own: %w", err)
		}
		install := agentrun.InstallCommand(wt)
		if install == "" {
			fmt.Fprintf(ctx.Stdout, "no lock file in %s: install the packages yourself\n", wt)
			return nil
		}
		if _, _, err := ci.PrepareInstall(wt, install); err != nil {
			return err
		}
		fmt.Fprintf(ctx.Stdout, "$ %s   (in %s)\n", install, wt)
		c := exec.Command("sh", "-c", install)
		c.Dir, c.Stdout, c.Stderr = wt, ctx.Stdout, ctx.Stderr
		if err := c.Run(); err != nil {
			return fmt.Errorf("worktree deps --own: %s: %w", install, err)
		}
		fmt.Fprintf(ctx.Stdout, "✓ %s has its own node_modules now\n", wt)
		return nil
	case *shared:
		st, ok := agentrun.DepsStep(main, agentrun.ShareDeps(main, wt))
		if !ok {
			fmt.Fprintln(ctx.Stdout, "nothing to share: the main checkout has no node_modules with a lock file this worktree also has")
			return nil
		}
		fmt.Fprintln(ctx.Stdout, "✓ "+st.Name)
		return nil
	}
	dirs := agentrun.MainDepDirs(main)
	if len(dirs) == 0 {
		fmt.Fprintf(ctx.Stdout, "the main checkout %s has no node_modules: nothing is shared\n", main)
		return nil
	}
	for _, d := range dirs {
		link := filepath.Join(wt, d, "node_modules")
		state := "none (issue-cli worktree deps --shared links it)"
		if fi, err := os.Lstat(link); err == nil {
			switch {
			case fi.Mode()&os.ModeSymlink == 0:
				state = "its own folder"
			default:
				target, _ := os.Readlink(link)
				state = "shared: a link to " + target + " (before you add, remove or update a package: issue-cli worktree deps --own)"
			}
		}
		fmt.Fprintf(ctx.Stdout, "%s: %s\n", strings.TrimPrefix(filepath.Join(d, "node_modules"), "./"), state)
	}
	return nil
}

func sameDir(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}
