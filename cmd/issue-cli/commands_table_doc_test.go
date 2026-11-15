package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// commandsTableRowRE finds each `issue-cli <word>` a row's first cell names;
// a row may name several (`issue-cli ask …` / `issue-cli answer …`).
var commandsTableRowRE = regexp.MustCompile("`issue-cli ([a-z][a-z0-9-]*)")

// commandsTableNames reads docs/CLI/overview.md's "## Commands" table and
// returns the commands its rows name, aliases resolved to their command.
func commandsTableNames(t *testing.T) map[string]bool {
	t.Helper()
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI", "overview.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(doc)
	start := strings.Index(text, "\n## Commands\n")
	if start < 0 {
		t.Fatal(`docs/CLI/overview.md has no "## Commands" section`)
	}
	section := text[start+1:]
	if end := strings.Index(section, "\n### "); end >= 0 {
		section = section[:end]
	}
	named := map[string]bool{}
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cells := strings.SplitN(line, " | ", 2)
		for _, m := range commandsTableRowRE.FindAllStringSubmatch(cells[0], -1) {
			name := m[1]
			if canonical, ok := commandAliases[name]; ok {
				name = canonical
			}
			named[name] = true
		}
	}
	return named
}

// Every command in the registry has a row in docs/CLI/overview.md's
// Commands table, and every row names a command that exists, so the table
// stays the one list a reader scans for what issue-cli can do.
func TestCommandsTableListsEveryCommand(t *testing.T) {
	named := commandsTableNames(t)
	for _, name := range commandNames() {
		if !named[name] {
			t.Errorf("docs/CLI/overview.md's Commands table has no row for `issue-cli %s`: add one, worded from its --help short line (%q)",
				name, commandRegistry[name].ShortHelp)
		}
	}
	for name := range named {
		if commandRegistry[name] == nil {
			t.Errorf("docs/CLI/overview.md's Commands table names `issue-cli %s`, which is not a command or alias", name)
		}
	}
}
