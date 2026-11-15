package main

import (
	"fmt"
	"sort"
	"strings"
)

// commandHints maps words agents type for a command that is named otherwise
// to the command that does the job. A hint is never a registered command or
// alias (those resolve), and always names one
// (TestCommandHintsNameRealCommands). Unlike an alias it doesn't run the
// command: it only names it in the unknown-command error.
var commandHints = map[string]string{
	"body":          "show",
	"view":          "show",
	"read":          "show",
	"cat":           "show",
	"get":           "show",
	"info":          "show",
	"list-comments": "comments",
	"comment-list":  "comments",
	"thread":        "comments",
	"move":          "transition",
	"advance":       "transition",
	"tick":          "check",
	"untick":        "uncheck",
	"note":          "comment",
	"reply":         "answer",
	"retros":        "retro",
	"edit":          "update",
	"new":           "create",
	"ls":            "list",
	"find":          "search",
	"grep":          "search",
}

// unknownCommandError is the error for a command name nothing registers: the
// name, the command that does the job when one is near (a hint from
// commandHints, else the closest name), and where the full list is.
func unknownCommandError(name string) error {
	msg := "unknown command: " + name
	if near := suggestCommand(name); near != "" {
		msg += fmt.Sprintf("\n\nDid you mean: issue-cli %s", near)
		if c := lookupCommand(near); c != nil && c.ShortHelp != "" {
			msg += " — " + c.ShortHelp
		}
	}
	return fmt.Errorf("%s\n\nRun: issue-cli help", msg)
}

// suggestCommand names the command an unknown name most likely meant: its
// hint, else the one command or alias the name starts ("trans"), else the
// one within two edits (one for a name under five letters; the fewest wins,
// then the first by name). "" when nothing is near.
func suggestCommand(name string) string {
	lower := strings.ToLower(name)
	if to, ok := commandHints[lower]; ok {
		return to
	}
	names := commandNames()
	for alias := range commandAliases {
		names = append(names, alias)
	}
	sort.Strings(names)
	if lookupCommand(lower) != nil {
		return lower // a known command typed in another case
	}
	if len(lower) >= 3 {
		var starts []string
		for _, n := range names {
			if strings.HasPrefix(n, lower) {
				starts = append(starts, n)
			}
		}
		if len(starts) == 1 {
			return starts[0]
		}
	}
	// Two edits for a name of five letters or more, one below: "foo" is two
	// edits from "job" but means neither.
	best, bestD := "", 3
	if len(lower) < 5 {
		bestD = 2
	}
	if len(lower) <= 40 {
		for _, n := range names {
			if d := editDistance(lower, n); d < bestD {
				best, bestD = n, d
			}
		}
	}
	return best
}

// editDistance is the Levenshtein distance between a and b, in bytes.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
