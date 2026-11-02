package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// readOnly says whether a call (args after the command name) only reads the
// project it runs on. A read-only call by a caller who isn't working in that
// project writes nothing there, not even its clilog line or telemetry event
// (logProject).
type readOnly func(args []string) bool

// commandAccess marks every registered command read-only, writing, or
// read-only for some subcommands or flags. TestEveryCommandHasAccess makes a
// new command pick one; TestReadCommandsLeaveAnotherProjectUnchanged runs the
// read-only ones on a sandbox repo and checks its git status. When unsure,
// mark a command writes: it then logs into the project as before.
var commandAccess = map[string]readOnly{
	"answer":          writes,
	"append":          writes,
	"approve":         writes,
	"ask":             writes,
	"agents":          readSubs("status"),
	"autostart":       readSubs("status", "next"),
	"queue":           readSubsUnlessFlag("start", "list", "next"),
	"balance":         readWithFlag("list"),
	"bench":           readSubs("candidates", "show", "report", "compare"),
	"check":           writes,
	"check-edit":      writes,
	"check-remove":    writes,
	"check-script":    readWithFlag("lib"),
	"checklist":       reads,
	"ci":              readSubs("status"),
	"claim":           writes,
	"clarify":         readUnlessFlags("assume", "answer"),
	"close":           writes,
	"comment":         writes,
	"comments":        reads,
	"connections":     reads,
	"connector":       writes,
	"connectors":      reads,
	"context":         reads,
	"create":          writes,
	"data":            readSubs("list"),
	"decide":          writes,
	"decision":        readSubs("challenges"),
	"decisions":       reads,
	"department":      readSubsUnlessFlag("write", "list", "show", "week"),
	"dispatch":        writes,
	"dispatch-prompt": reads,
	"done":            writes,
	"experiment":      readSubs("candidates", "show", "list"),
	"postmortem":      readSubs("brief", "show", "list"),
	"goal":            writes,
	"goals":           reads,
	"handoff":         writes,
	"handoffs":        reads,
	"health":          readSubs("lint"),
	"help":            reads,
	"init":            writes,
	"job":             readSubs("due"),
	"jobs":            reads,
	"lead":            readSubs("whoami", "list"),
	"list":            reads,
	"memory":          readSubs("search", "list", "show", "candidates", "consolidate"),
	"next":            reads,
	"output":          readSubs("show"),
	"outputs":         reads,
	"playtest":        writes,
	"privacy":         readSubs("check"),
	"process":         reads,
	"project":         writes,
	"projects":        reads,
	"critique":        writes,
	"prove":           writes,
	"rounds":          writes,
	"release":         writes,
	"replace":         writes,
	"report-bug":      writes,
	"retrospective":   writes,
	"search":          reads,
	"set-meta":        writes,
	"set-type":        writes,
	"spend":           reads,
	"start":           writes,
	"stats":           reads,
	"status":          reads,
	"telemetry":       readSubs("report", "path", "sent"),
	"tracker":         writes,
	"transition":      readWithFlag("dry-run"),
	"uncheck":         writes,
	"unclaim":         writes,
	"update":          writes,
	"upgrade":         reads,
	"version":         reads,
	"workflow":        readSubsOrFlag("dry-run", []string{"upgrade", "catalog-check", "commit-msg"}, "scan", "catalog-bump"),
	"yolo":            readSubs("status", "queue"),
}

func reads([]string) bool  { return true }
func writes([]string) bool { return false }

// readSubs: read-only for the listed subcommands, and with none (the
// command's own default, a status or a usage error).
func readSubs(subs ...string) readOnly {
	return func(args []string) bool {
		return len(args) == 0 || strings.HasPrefix(args[0], "-") || slices.Contains(subs, args[0])
	}
}

// readWithFlag: read-only only when --name is passed.
func readWithFlag(name string) readOnly {
	return func(args []string) bool { return hasFlag(args, name) }
}

// readUnlessFlags: read-only unless one of the --names is passed (clarify
// --assume or --answer).
func readUnlessFlags(names ...string) readOnly {
	return func(args []string) bool {
		for _, n := range names {
			if hasFlag(args, n) {
				return false
			}
		}
		return true
	}
}

// readSubsUnlessFlag: readSubs, but a --name flag makes the call write
// (department week --write).
func readSubsUnlessFlag(name string, subs ...string) readOnly {
	inner := readSubs(subs...)
	return func(args []string) bool { return inner(args) && !hasFlag(args, name) }
}

// readSubsOrFlag: read-only for subs, and for flagSubs when --name is passed
// (workflow scan --dry-run).
func readSubsOrFlag(name string, subs []string, flagSubs ...string) readOnly {
	inner := readSubs(subs...)
	return func(args []string) bool {
		if inner(args) {
			return true
		}
		return slices.Contains(flagSubs, args[0]) && hasFlag(args, name)
	}
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "--"+name || a == "-"+name || strings.HasPrefix(a, "--"+name+"=") || strings.HasPrefix(a, "-"+name+"=") {
			return true
		}
	}
	return false
}

// isReadOnly reports whether this invocation only reads its project. An
// unknown command, or one missing from commandAccess, counts as writing.
func isReadOnly(inv *invocation) bool {
	if inv == nil || inv.cmd == nil {
		return false
	}
	rule := commandAccess[inv.cmd.Name]
	return rule != nil && rule(inv.args)
}

// callerDir is the caller's working directory; a var so tests can pin it.
var callerDir = os.Getwd

// logProject is the project whose .agent-logs/ this call's clilog record and
// telemetry event go to. It is the project the call ran on, unless the call
// is read-only and the caller works outside that project (their working
// directory is not under its root or issues folder): then it is the
// caller's own project, the one whose root holds the working directory, or
// nil for the machine log. foreign is true in that case, and the target's
// per-agent clilog is skipped too.
func logProject(inv *invocation) (proj *tracker.Project, foreign bool) {
	if inv == nil || inv.ctx == nil || inv.ctx.Project == nil {
		return nil, false
	}
	target := inv.ctx.Project
	if !isReadOnly(inv) {
		return target, false
	}
	cwd, err := callerDir()
	if err != nil || cwd == "" {
		return target, false
	}
	cwd = resolvedPath(cwd)
	if worksIn(cwd, target) {
		return target, false
	}
	var own *tracker.Project
	best := -1
	for i := range inv.ctx.AllProjects {
		p := &inv.ctx.AllProjects[i]
		if p.Slug == target.Slug {
			continue
		}
		root := resolvedPath(p.TelemetryRoot())
		if root != "" && within(cwd, root) && len(root) > best {
			own, best = p, len(root)
		}
	}
	return own, true
}

// worksIn: cwd is under p's root (its checkout and .worktrees) or its
// issues folder.
func worksIn(cwd string, p *tracker.Project) bool {
	for _, dir := range []string{p.TelemetryRoot(), p.IssueDir} {
		if dir == "" {
			continue
		}
		if abs, err := filepath.Abs(dir); err == nil && within(cwd, resolvedPath(abs)) {
			return true
		}
	}
	return false
}

func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// resolvedPath follows symlinks (macOS /var → /private/var) when it can.
func resolvedPath(p string) string {
	if p == "" {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}
