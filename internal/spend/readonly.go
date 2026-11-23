package spend

import (
	"path/filepath"
	"slices"
	"strings"
)

// Read-only calls: a tool call that only looks (a Read, Grep or Glob, or a
// shell call of greps, ls, sed -n, cat, read-only git or a read-only
// issue-cli command). Two turns in a row of one such call each could have
// been one message: spend --turns counts them (follow_on_read_only) and
// the worker's read-nudge hook (ReadNudge) asks for one turn.

// readOnlyTools are Claude Code tools that only read.
var readOnlyTools = map[string]bool{"Read": true, "Grep": true, "Glob": true, "LS": true}

// ReadOnlyCall reports whether a tool call only reads: name is the tool,
// command a Bash call's command (else ignored).
func ReadOnlyCall(name, command string) bool {
	if readOnlyTools[name] {
		return true
	}
	if name != "Bash" {
		return false
	}
	cmds, ok := shellCommands(command)
	if !ok || len(cmds) == 0 {
		return false
	}
	for _, pipeline := range cmds {
		for _, stage := range pipeline {
			if !readOnlyStage(stage) {
				return false
			}
		}
	}
	return true
}

// lookers are programs that only print: given no redirection (which
// shellCommands refuses), they write nothing.
var lookers = map[string]bool{
	"cat": true, "head": true, "tail": true, "nl": true, "less": true, "wc": true,
	"grep": true, "egrep": true, "rg": true, "ls": true, "tree": true, "file": true, "stat": true,
	"sort": true, "uniq": true, "cut": true, "tr": true, "column": true, "jq": true, "diff": true,
	"echo": true, "printf": true, "pwd": true, "cd": true, "basename": true, "dirname": true,
	"realpath": true, "which": true, "du": true, "date": true, "true": true,
}

// gitLooks are git subcommands that only read (branch, tag and stash only
// as a list).
var gitLooks = map[string]bool{
	"log": true, "show": true, "diff": true, "status": true, "rev-parse": true, "ls-files": true,
	"grep": true, "blame": true, "merge-base": true, "rev-list": true, "describe": true, "shortlog": true,
	"cat-file": true, "ls-tree": true, "reflog": true, "name-rev": true,
}

// issueCLILooks are issue-cli commands that only read; memory, output, ci
// and bench only with the listed subcommands.
var issueCLILooks = map[string][]string{
	"show": nil, "context": nil, "checklist": nil, "ref": nil, "codemap": nil, "affected": nil,
	"comments": nil, "decisions": nil, "list": nil, "process": nil, "rules": nil, "stats": nil,
	"spend": nil, "search": nil, "status": nil, "handoffs": nil, "outputs": nil, "next": nil,
	"memory": {"search", "show", "list"}, "output": {"show", "list"}, "ci": {"status"},
}

// readOnlyStage reports whether one pipeline stage (its words) only reads.
func readOnlyStage(words []string) bool {
	for len(words) > 0 && (words[0] == "rtk" || words[0] == "proxy") {
		words = words[1:]
	}
	if len(words) == 0 {
		return false
	}
	prog := filepath.Base(words[0])
	args := words[1:]
	switch {
	case prog == "read" && words[0] != prog: // a path ending in /read is no rtk read
		return false
	case prog == "read":
		return true // rtk read
	case prog == "sed":
		return slices.Contains(args, "-n") && !slices.ContainsFunc(args, func(a string) bool { return strings.HasPrefix(a, "-i") })
	case prog == "find":
		return !slices.ContainsFunc(args, func(a string) bool {
			return a == "-delete" || strings.HasPrefix(a, "-exec") || strings.HasPrefix(a, "-ok") || strings.HasPrefix(a, "-fprint")
		})
	case prog == "git":
		return gitReadOnly(args)
	case prog == "issue-cli":
		return issueCLIReadOnly(args)
	}
	return lookers[prog]
}

// gitReadOnly: git's global -C <dir> / --no-pager, then a reading
// subcommand; branch, tag and stash only listing.
func gitReadOnly(args []string) bool {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		if args[0] == "-C" || args[0] == "-c" {
			if len(args) < 2 {
				return false
			}
			args = args[1:]
		}
		args = args[1:]
	}
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "branch", "tag":
		return !slices.ContainsFunc(args[1:], func(a string) bool {
			return !strings.HasPrefix(a, "-") || slices.Contains([]string{"-d", "-D", "-m", "-M", "--delete", "--move", "-f", "--force", "-u"}, a)
		})
	case "stash":
		return len(args) > 1 && (args[1] == "list" || args[1] == "show")
	}
	return gitLooks[args[0]]
}

// issueCLIReadOnly: global flags (--project <slug>, --config <file>,
// --json), then a reading command.
func issueCLIReadOnly(args []string) bool {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		if (args[0] == "--project" || args[0] == "--config") && len(args) > 1 {
			args = args[1:]
		}
		args = args[1:]
	}
	if len(args) == 0 {
		return false
	}
	subs, ok := issueCLILooks[args[0]]
	if !ok {
		return false
	}
	if subs == nil {
		return true
	}
	return len(args) > 1 && slices.Contains(subs, args[1])
}
