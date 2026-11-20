package main

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/leadstate"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// printLeadTickMissing tells a lead, after any issue-cli call, that its
// background lead tick is not running, with the command to start it again.
// A session that ended (a crash, a watchdog's resume) takes its background
// shells with it and nothing else says so: the lead would wait on a tick
// that never wakes it. A tick watch whose pid is gone is said at once; a
// tick that exited on news gets leadstate.TickGrace for the lead to act
// first. The tick's own runs and a rotate or flush (the session is ending)
// say nothing.
func printLeadTickMissing(ctx *Context, command string, args []string) {
	if ctx == nil || ctx.Project == nil || ctx.JSONOutput || ctx.Stderr == nil || leadstate.Dir() == "" {
		return
	}
	if (command == "lead" || command == "ceo") && len(args) > 0 {
		switch args[0] {
		case "tick", "watch", "rotate", "flush", "spawn":
			return
		}
	}
	me := whoAmI(ctx.Project, nil)
	if me.Role != tracker.RoleProjectLead && me.Role != tracker.RoleDepartmentLead || me.Name == "" {
		return
	}
	store, err := leadstate.Load(me.Name)
	if err != nil {
		return
	}
	st := store.TickStatus(pidAlive, ctx.now())
	if !st.Missing {
		return
	}
	cmd := st.Command
	if cmd == "" {
		cmd = defaultLeadTickCommand(ctx.Project, me)
	}
	why := "none has run"
	switch {
	case st.DeadPID != 0:
		why = fmt.Sprintf("its process (pid %d) is gone without a word: killed, or the session that ran it ended", st.DeadPID)
	case st.Signal != "":
		why = fmt.Sprintf("the last one (pid %d) was stopped by %s at %s", st.PID, st.Signal, st.Ended.Local().Format("15:04"))
	case !st.Ended.IsZero():
		why = "the last one exited at " + st.Ended.Local().Format("15:04") + " and none started since"
	}
	fmt.Fprintf(ctx.Stderr, "\nlead tick: no background tick is running (%s). Start it again with Bash run_in_background:\n  %s\n", why, cmd)
}

// defaultLeadTickCommand is the tick a lead runs when its store names
// none: the project lead's own, a department lead's read-only one.
func defaultLeadTickCommand(proj *tracker.Project, me Identity) string {
	cmd := "issue-cli --project " + proj.Slug + " lead tick --watch --exit-on-news"
	if me.Role == tracker.RoleDepartmentLead {
		cmd += " --observe"
		if key := strings.TrimSpace(me.Department); key != "" {
			cmd += " --department " + tracker.DepartmentKey(key)
		}
	}
	return cmd
}
