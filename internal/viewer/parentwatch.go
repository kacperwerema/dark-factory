package viewer

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ExitWithPIDEnv names a process the viewer lives no longer than: a test or
// the playtest runner sets it to its own pid, so a viewer it started stops
// even when it is killed (go test -timeout, SIGKILL) and its cleanup never
// runs. Unset, the viewer runs until a signal stops it.
const ExitWithPIDEnv = "ISSUE_VIEWER_EXIT_WITH_PID"

// parentPoll is how often the viewer looks for that process.
var parentPoll = 2 * time.Second

// exitWithParent returns ctx, cancelled once the process ExitWithPIDEnv
// names is gone (as a shutdown signal does). A value that is not a pid
// above 1 is ignored with a warning.
func exitWithParent(ctx context.Context) context.Context {
	raw := strings.TrimSpace(os.Getenv(ExitWithPIDEnv))
	if raw == "" {
		return ctx
	}
	pid, err := strconv.Atoi(raw)
	if err != nil || pid <= 1 {
		fmt.Fprintf(os.Stderr, "warning: %s=%q is not a pid; ignored\n", ExitWithPIDEnv, raw)
		return ctx
	}
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		t := time.NewTicker(parentPoll)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if syscall.Kill(pid, 0) == syscall.ESRCH {
					fmt.Fprintf(os.Stderr, "issue-viewer: process %d (%s) is gone; stopping\n", pid, ExitWithPIDEnv)
					cancel()
					return
				}
			}
		}
	}()
	return ctx
}
