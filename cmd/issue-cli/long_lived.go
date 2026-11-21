package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/leaks"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// What runs past its bound (internal/leaks LongReport): lead sessions past
// their rotation, lead-* sessions no project owns, long-lived processes
// that doubled in a day. issue-cli health prints it; the lead tick says
// each once as news. Nothing here rotates, closes or restarts anything.

// longReport reads the report over every project; a var for tests.
var longReport = func(projects []*tracker.Project, now time.Time) leaks.LongReport {
	return leaks.LongNow(projects, now)
}

// everyProject is ctx's projects as pointers, the current one alone
// without a projects.yaml.
func everyProject(ctx *Context) []*tracker.Project {
	var out []*tracker.Project
	for i := range ctx.AllProjects {
		out = append(out, &ctx.AllProjects[i])
	}
	if len(out) == 0 && ctx.Project != nil {
		out = append(out, ctx.Project)
	}
	return out
}

// engineProject reports whether proj's checkout is the engine's own (it
// holds cmd/issue-viewer): the machine's lines (an orphan lead, the
// viewer's growth) go to that project's tick only, not to every lead's.
func engineProject(proj *tracker.Project) bool {
	if proj == nil {
		return false
	}
	_, err := os.Stat(filepath.Join(projectRoot(proj), "cmd", "issue-viewer"))
	return err == nil
}

// printLongLived prints the report and proj's idle workers that hold a
// slot (the same reading the lead tick frees them by) under one heading;
// nothing when both are empty.
func printLongLived(ctx *Context, r leaks.LongReport) {
	w, proj := ctx.Stdout, ctx.Project
	var idle []agentrun.Reclaim
	if proj != nil {
		if issues, err := proj.LoadIssues(); err == nil {
			idle = reclaimableSessions(proj, issues, ctx.now())
		}
	}
	if r.Empty() && len(idle) == 0 {
		return
	}
	fmt.Fprintln(w, "\n== Past its bound (sessions and processes) ==")
	for _, s := range r.Sessions {
		fmt.Fprintln(w, "  "+s.Line())
	}
	for _, g := range r.Grown {
		fmt.Fprintln(w, "  "+g.Line())
	}
	for _, i := range idle {
		fmt.Fprintln(w, "  idle worker holds a slot: "+i.Line()+": "+tracker.InjectProject(proj, "issue-cli lead tick")+" closes it (re-dispatch resumes it)")
	}
}

// leadTickLong says, once each until it goes away, this project's leads
// past their rotation and its leads that doubled in a day; the engine's
// project also hears of orphan leads and the other grown processes.
func leadTickLong(ctx *Context, t *leadTicker) {
	proj := ctx.Project
	projects := everyProject(ctx)
	r := longReport(projects, t.now)
	engine := engineProject(proj)
	for _, s := range r.Sessions {
		if s.Project == proj.Slug || (engine && s.Kind == leaks.SessionOrphan) {
			t.say("long:"+s.Session, "⚠ session: "+s.Line())
		}
	}
	for _, g := range r.Grown {
		owner := ""
		if strings.HasPrefix(g.Name, "lead-") {
			if p := leaks.OwnerOf(g.Name, projects); p != nil {
				owner = p.Slug
			}
		}
		if owner == proj.Slug || (owner == "" && engine) {
			t.say("grown:"+g.Name+":"+g.Since.Format("0102"), "⚠ memory: "+g.Line())
		}
	}
}
