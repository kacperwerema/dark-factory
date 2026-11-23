package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/briefing"
	"github.com/kacperwerema/dark-factory/internal/gitfile"
	"github.com/kacperwerema/dark-factory/internal/leadstate"
	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/ownerstep"
	"github.com/kacperwerema/dark-factory/internal/queue"
	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// lead state: the lead's compact state file, rendered from the board and
// the lead's own notes (internal/leadstate). The tick re-renders it on
// every run; a rotated lead starts from it
// (docs/patterns/lead-session.md#the-state-file).

const leadStateUsage = `Usage:
  issue-cli lead state [--path] [--all] [--lead <name>]
  issue-cli lead state note "<text>" [--tomorrow]
  issue-cli lead state ask --quote "<the owner's words>"
  issue-cli lead state done <n>`

func runLeadState(ctx *Context, args []string) error {
	fs := newFlagSet("lead state", ctx)
	pathOnly := fs.Bool("path", false, "print the state file's path only")
	all := fs.Bool("all", false, "print every Today, Tomorrow and Notes item (the file keeps the 5 newest of each)")
	leadFlag := fs.String("lead", "", "the lead whose state this is (default: you as a lead, else the CEO)")
	tomorrow := fs.Bool("tomorrow", false, "note: something to check tomorrow")
	quote := fs.String("quote", "", "ask: the owner's words")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	proj := ctx.Project
	if proj == nil {
		return fmt.Errorf("lead state needs a project: pass --project <slug> or run from a project root")
	}
	issues, err := proj.LoadIssues()
	if err != nil {
		return fmt.Errorf("cannot load issues: %w", err)
	}
	me := guardWho(ctx, issues, "lead state")
	if me.Role == tracker.RoleWorker {
		guardNever(ctx, "lead state")
		return fmt.Errorf("workers don't keep the lead's state (you are %s): your notes go in HANDOFF.md and on your issue", valueOr(me.Name, "a worker"))
	}
	lead := leadStateName(proj, me, *leadFlag)
	if leadstate.Dir() == "" {
		return fmt.Errorf("no state dir: set XDG_STATE_HOME or HOME")
	}
	sub := fs.Arg(0)
	store, err := leadstate.Load(lead)
	if err != nil {
		return err
	}
	now := ctx.now()
	switch sub {
	case "":
		if *pathOnly {
			fmt.Fprintln(ctx.Stdout, leadstate.Path(lead))
			return nil
		}
		if *all {
			v := leadStateView(ctx, lead, issues, now)
			v.Full = true
			fmt.Fprint(ctx.Stdout, leadstate.Render(v, store))
			return nil
		}
		out, err := renderLeadState(ctx, lead, issues)
		if err != nil {
			return err
		}
		fmt.Fprint(ctx.Stdout, out)
		return nil
	case "note":
		text := strings.TrimSpace(strings.Join(fs.Args()[1:], " "))
		if text == "" {
			return &usageError{msg: "lead state note needs the text\n\n" + leadStateUsage}
		}
		kind := leadstate.KindNote
		if *tomorrow {
			kind = leadstate.KindTomorrow
		}
		return leadStateAdd(ctx, lead, issues, store, kind, text, now)
	case "ask":
		text := strings.TrimSpace(*quote)
		if text == "" {
			text = strings.TrimSpace(strings.Join(fs.Args()[1:], " "))
		}
		if text == "" {
			return &usageError{msg: "lead state ask needs the owner's words (--quote)\n\n" + leadStateUsage}
		}
		return leadStateAdd(ctx, lead, issues, store, leadstate.KindAsk, text, now)
	case "done":
		n, err := strconv.Atoi(strings.TrimPrefix(fs.Arg(1), "#"))
		if err != nil {
			return &usageError{msg: "lead state done needs a note number\n\n" + leadStateUsage}
		}
		if err := store.Done(n); err != nil {
			return err
		}
		if err := store.Save(lead); err != nil {
			return err
		}
		fmt.Fprintf(ctx.Stdout, "✓ #%d done\n", n)
		_, err = renderLeadState(ctx, lead, issues)
		return err
	}
	return &usageError{msg: fmt.Sprintf("unknown lead state step %q\n\n%s", sub, leadStateUsage)}
}

func leadStateAdd(ctx *Context, lead string, issues []*tracker.Issue, store leadstate.Store, kind, text string, now time.Time) error {
	if what := memory.FindSecret(text); what != "" {
		return fmt.Errorf("that looks like a secret (%s): the state file goes into the next lead's first prompt. Nothing was written", what)
	}
	id := store.Add(kind, text, now)
	if err := store.Save(lead); err != nil {
		return err
	}
	fmt.Fprintf(ctx.Stdout, "✓ #%d %s added to %s\n", id, kind, leadstate.Path(lead))
	_, err := renderLeadState(ctx, lead, issues)
	return err
}

// leadStateName is the lead the state belongs to: --lead, the caller when
// it is a lead, else the CEO.
func leadStateName(proj *tracker.Project, me Identity, flag string) string {
	if f := strings.TrimSpace(flag); f != "" {
		return f
	}
	if (me.Role == tracker.RoleProjectLead || me.Role == tracker.RoleDepartmentLead) && me.Name != "" {
		return me.Name
	}
	return tracker.LeadSessionName(proj)
}

// renderLeadState folds yesterday's notes into their day's summary, drops
// watches whose process is gone, writes the state file and returns it.
func renderLeadState(ctx *Context, lead string, issues []*tracker.Issue) (string, error) {
	proj := ctx.Project
	now := ctx.now()
	store, err := leadstate.Load(lead)
	if err != nil {
		return "", err
	}
	if leadstate.Dir() == "" {
		return leadstate.Render(leadStateView(ctx, lead, issues, now), store), nil
	}
	changed := store.Prune(pidAlive)
	sessions := filepath.Join(projectRoot(proj), "sessions")
	folded := store.Fold(now)
	days := make([]string, 0, len(folded))
	for d := range folded {
		days = append(days, d)
	}
	sort.Strings(days)
	for _, d := range days {
		day, _ := time.ParseInLocation("2006-01-02", d, time.Local)
		if err := leadstate.FoldInto(filepath.Join(sessions, tracker.HandoffSummaryName(day, lead)), folded[d]); err != nil {
			return "", fmt.Errorf("folding the notes of %s: %w", d, err)
		}
		changed = true
	}
	if changed {
		if err := store.Save(lead); err != nil {
			return "", err
		}
	}
	out := leadstate.Render(leadStateView(ctx, lead, issues, now), store)
	if what := memory.FindSecret(out); what != "" {
		return "", fmt.Errorf("the state file would hold a secret (%s): not written", what)
	}
	path := leadstate.Path(lead)
	if path == "" {
		return out, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := gitfile.WriteAtomic(path, []byte(out), 0o644); err != nil {
		return "", err
	}
	return out, nil
}

// pidAlive: a watch's process still runs. A var for tests.
var pidAlive = func(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// leadStateView reads the board part of lead's state.
func leadStateView(ctx *Context, lead string, issues []*tracker.Issue, now time.Time) leadstate.View {
	proj := ctx.Project
	inject := func(s string) string { return tracker.InjectProject(proj, s) }
	v := leadstate.View{Lead: lead, Project: proj.Slug, At: now, Refill: leadstate.RefillLine,
		Commands: leadstate.Commands{
			Workers:    inject("issue-cli agents"),
			Queue:      inject("issue-cli queue list"),
			Status:     inject("issue-cli status"),
			Decisions:  inject("issue-cli decisions --lead ceo"),
			Memories:   inject("issue-cli memory list --scope lead"),
			Note:       inject(`issue-cli lead state note "<text>" [--tomorrow] | ask --quote "<owner's words>"`),
			Done:       inject("issue-cli lead state done <n>"),
			History:    inject("issue-cli lead history <words>"),
			OwnerSteps: inject("issue-cli owner-step list"),
			All:        inject("issue-cli lead state --all"),
		}}
	if steps, err := ownerstep.LoadOpen(proj.IssueDir, now); err == nil {
		v.OwnerSteps = ownerstep.Lines(steps, now)
	}
	dept := ""
	if d := proj.DepartmentOfLead(lead); d != nil {
		dept = d.Name
	}
	in := func(issue *tracker.Issue) bool {
		if dept == "" {
			return true
		}
		owner := proj.DepartmentOf(issue)
		if strings.TrimSpace(owner) == "" {
			owner = tracker.ProductName
		}
		return proj.InScope(dept, owner)
	}
	bySession := map[string]*tracker.Issue{}
	for _, is := range issues {
		bySession[tracker.AgentSessionName(is.Slug)] = is
	}
	today := now.Local().Format("2006-01-02")
	for _, s := range agentrun.ForProject(proj, projectRoot(proj), agentrun.ListSessions()) {
		name := agentrun.BareName(s.Name)
		if !strings.HasPrefix(name, "agent-") {
			continue
		}
		w := leadstate.Worker{Session: name}
		if is := bySession[name]; is != nil {
			if !in(is) {
				continue
			}
			w.Slug, w.Status = is.Slug, is.Status
			w.Last = lastProgress(is)
		}
		v.Workers = append(v.Workers, w)
	}
	sort.Slice(v.Workers, func(i, j int) bool { return v.Workers[i].Session < v.Workers[j].Session })

	if queue.Exists(proj.IssueDir) {
		if q, err := queue.Load(proj.IssueDir); err == nil {
			for _, it := range q.ActiveItems() {
				if it.State != queue.Open {
					continue
				}
				if it.Slug != "" {
					is, ok := issueExists(issues, it.Slug)
					if !ok || proj.IssueClosed(is) || !in(is) {
						continue
					}
				}
				v.QueueOpen++
				v.Queue = append(v.Queue, leadstate.QueueItem{ID: it.ID, Label: it.Label(), Quote: it.Quote})
			}
			if q.Paused {
				v.QueueState = "The queue is paused."
			}
		}
	}

	for _, is := range issues {
		if !in(is) {
			continue
		}
		for _, d := range is.Decisions() {
			if d.Date.Format("2006-01-02") == today {
				v.Decisions = append(v.Decisions, leadstate.Ref{ID: d.Ref, Text: shorten(d.Text, 80), At: d.Date})
			}
		}
	}
	if set, err := memory.ProjectStore(proj.IssueDir).Load(); err == nil {
		for _, m := range set.Active(now) {
			if m.Pinned {
				v.Pinned = append(v.Pinned, leadstate.Ref{ID: m.ID, Text: briefing.PinTitle(m.Text)})
			}
			if !strings.HasPrefix(m.Created, today) || !leadMemory(m) {
				continue
			}
			at, _ := time.Parse(time.RFC3339, m.Created)
			v.Memories = append(v.Memories, leadstate.Ref{ID: m.ID, Text: shorten(m.Text, 80), At: at})
		}
	}
	return v
}

// leadMemory: a memory in the lead's scope or tagged as a lead rule.
func leadMemory(m memory.Memory) bool {
	if m.Scope == "lead" {
		return true
	}
	for _, t := range m.Tags {
		if t == "lead-rule" {
			return true
		}
	}
	return false
}

// lastProgress is the first line of the issue's newest progress: comment.
func lastProgress(is *tracker.Issue) string {
	cs := is.Comments()
	for i := len(cs) - 1; i >= 0; i-- {
		t := strings.TrimSpace(cs[i].Text)
		if strings.HasPrefix(strings.ToLower(t), "progress:") {
			return shorten(firstLineOf(t), 120)
		}
	}
	return ""
}

func shorten(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// leadWatchArgs are this process's arguments, as a watch records them; a
// var for tests.
var leadWatchArgs = func() []string { return os.Args[1:] }

// registerLeadWatch records this process as a background shell lead waits
// on (openLeadWatch, cmd_lead_tick_signal.go); the returned func drops it
// as one that exited on its own. Only a lead's own shells are recorded.
func registerLeadWatch(ctx *Context, me Identity) func() {
	return openLeadWatch(ctx, me).done
}

// shellJoin quotes each arg that needs it for /bin/sh.
func shellJoin(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = shell.Quote(a)
	}
	return strings.Join(out, " ")
}
