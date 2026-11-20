package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/kacperwerema/dark-factory/internal/checks"
	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// staleBuildWait caps how long main waits, after the command, for the
// stale-build check it started beside it: a slow disk never holds a call.
const staleBuildWait = 2 * time.Second

// startStaleBuildCheck compares the running binary with its checkout's code
// beside the command (the walk takes ~70 ms) and returns a func that prints
// the warning when the build is stale. Only a branch build is compared: a
// file named ./issue-cli at the root of a checkout holding cmd/issue-cli,
// the one the briefing tells a worker to build. The installed build and
// test binaries print nothing.
func startStaleBuildCheck() func(io.Writer) {
	exe, err := os.Executable()
	if err != nil {
		return func(io.Writer) {}
	}
	done := make(chan string, 1)
	go func() {
		defer func() {
			if recover() != nil {
				done <- ""
			}
		}()
		done <- staleBuildWarning(exe)
	}()
	return func(w io.Writer) {
		select {
		case line := <-done:
			if line != "" {
				fmt.Fprintln(w, line)
			}
		case <-time.After(staleBuildWait):
		}
	}
}

// staleBuildWarning is the one line a call run by a stale branch build
// prints on stderr, "" when exe is no branch build or holds the newest code.
// The call itself has run: the line says its result came from the old code.
// check-script --yes refuses such a build instead (refuseStaleBuild).
func staleBuildWarning(exe string) string {
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if filepath.Base(exe) != tracker.BranchBuild {
		return ""
	}
	root := filepath.Dir(exe)
	if fi, err := os.Stat(filepath.Join(root, "cmd", "issue-cli")); err != nil || !fi.IsDir() {
		return ""
	}
	stale, err := checks.BuildStale(root, exe)
	if err != nil || stale == nil {
		return ""
	}
	const when = "2006-01-02 15:04:05"
	return fmt.Sprintf("issue-cli: warning: this ./%s is older than its code (built %s, %s changed %s): this call ran the old code; rebuild: (cd %s && go build -o ./%s ./cmd/issue-cli)",
		tracker.BranchBuild, stale.Built.Format(when), stale.What, stale.Changed.Format(when), shell.Quote(root), tracker.BranchBuild)
}
